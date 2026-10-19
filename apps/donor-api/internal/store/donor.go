package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// consentVersion names the notification consent wording a donor accepted,
// which preference events record.
const consentVersion = "r2-notifications-v1"

// referralWindow is how long the first valid referral touch holds.
const referralWindow = 30 * 24 * time.Hour

// pageSize is the length of every offset-paged list. Queries fetch one extra
// row to learn whether another page follows.
const pageSize = 50

func pageOffsetValid(offset int64) bool { return offset >= 0 && offset <= math.MaxInt64-pageSize }

// page trims a query's look-ahead row and returns the next page's offset, if any.
func page[T any](rows []T, offset int64) ([]T, *int64) {
	if len(rows) > pageSize {
		next := offset + pageSize
		return rows[:pageSize], &next
	}
	return rows, nil
}

var ErrPreferenceConflict = errors.New("preferences changed; refresh before saving")
var ErrPublicName = errors.New("choose a public first name or nickname of 1 to 80 characters")

// ErrPublicNameCharacters is an ErrPublicName for a name that looks like an
// email address or contains control characters.
var ErrPublicNameCharacters = fmt.Errorf("%w, without @ or control characters", ErrPublicName)

type DonorService struct {
	DB     *pgxpool.Pool
	Origin string
}

type Profile struct {
	Email          string  `json:"email"`
	PublicName     *string `json:"public_name"`
	ConsentVersion string  `json:"consent_version"`
	Preferences
}

// Preferences are the donor's notification choices. Email is opt-in; app
// notifications start on, since the device's permission prompt is the opt-in.
type Preferences struct {
	Version               int32 `json:"version"`
	ReferralEmails        bool  `json:"referral_emails"`
	MarketingEmails       bool  `json:"marketing_emails"`
	PushReferralConverted bool  `json:"push_referral_converted"`
	PushStreakAtRisk      bool  `json:"push_streak_at_risk"`
	PushStreakMilestone   bool  `json:"push_streak_milestone"`
}

// defaultPreferences apply until a donor first saves a choice.
var defaultPreferences = Preferences{PushReferralConverted: true, PushStreakAtRisk: true, PushStreakMilestone: true}

// readPreferences returns the donor's current choices.
func readPreferences(ctx context.Context, q *queries.Queries, donor pgtype.UUID) (Preferences, error) {
	pref, err := q.DonorPreference(ctx, donor)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultPreferences, nil
	}
	return Preferences{
		Version: pref.Version, ReferralEmails: pref.ReferralEmails, MarketingEmails: pref.MarketingEmails,
		PushReferralConverted: pref.PushReferralConverted, PushStreakAtRisk: pref.PushStreakAtRisk, PushStreakMilestone: pref.PushStreakMilestone,
	}, err
}

type Sharing struct {
	PublicName *string `json:"public_name"`
	Link       *string `json:"link"`
	IRLLink    *string `json:"irl_link"`
}

type ReferralCapture struct {
	Token      string
	PublicName *string
}

type GiftsPage struct {
	Email      string                      `json:"email"`
	Mode       string                      `json:"mode"`
	Gifts      []queries.DonorGiftsPageRow `json:"gifts"`
	NextOffset *int64                      `json:"next_offset"`
}

func readProfile(ctx context.Context, q *queries.Queries, donor queries.Donor) (Profile, error) {
	pref, err := readPreferences(ctx, q, donor.ID)
	return Profile{Email: donor.Email, PublicName: textPointer(donor.PublicName), ConsentVersion: consentVersion, Preferences: pref}, err
}

func (s DonorService) Profile(ctx context.Context, token string) (Profile, error) {
	return donorRead(
		ctx,
		s.DB,
		token,
		func(q *queries.Queries, d queries.Donor, _ time.Time) (Profile, error) { return readProfile(ctx, q, d) },
	)
}

// SavePreferences replaces the donor's notification choices if pref.Version
// is still current, and records the consent change.
func (s DonorService) SavePreferences(ctx context.Context, token string, pref Preferences) (Profile, error) {
	return donorWrite(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, _ time.Time) (Profile, error) {
		q := queries.New(tx)
		current, err := readProfile(ctx, q, d)
		if err != nil {
			return Profile{}, err
		}
		if pref.Version < 0 || pref.Version != current.Version || pref.Version == math.MaxInt32 {
			return Profile{}, ErrPreferenceConflict
		}
		id, err := NewID()
		if err != nil {
			return Profile{}, err
		}
		version := current.Version + 1
		if err = q.SavePreference(ctx, queries.SavePreferenceParams{
			ID: id, DonorID: d.ID, Version: version, ReferralEmails: pref.ReferralEmails, MarketingEmails: pref.MarketingEmails,
			PushReferralConverted: pref.PushReferralConverted, PushStreakAtRisk: pref.PushStreakAtRisk, PushStreakMilestone: pref.PushStreakMilestone,
		}); err != nil {
			return Profile{}, err
		}
		event, err := NewID()
		if err != nil {
			return Profile{}, err
		}
		if err = q.RecordPreferenceConsent(ctx, queries.RecordPreferenceConsentParams{
			ID: event, DonorID: d.ID, Version: version, ReferralEmails: pref.ReferralEmails, MarketingEmails: pref.MarketingEmails,
			PushReferralConverted: pgtype.Bool{Bool: pref.PushReferralConverted, Valid: true}, PushStreakAtRisk: pgtype.Bool{Bool: pref.PushStreakAtRisk, Valid: true},
			PushStreakMilestone: pgtype.Bool{Bool: pref.PushStreakMilestone, Valid: true}, ConsentVersion: consentVersion,
		}); err != nil {
			return Profile{}, err
		}
		return readProfile(ctx, q, d)
	})
}

// sharing offers referral links only once the donor has chosen a public name.
func sharing(d queries.Donor, site publicSite) Sharing {
	out := Sharing{PublicName: textPointer(d.PublicName)}
	if d.ReferralCode.String != "" && d.PublicName.String != "" {
		link, irl := site.referral(d.ReferralCode.String), site.irlReferral(d.ReferralCode.String)
		out.Link, out.IRLLink = &link, &irl
	}
	return out
}

func (s DonorService) Sharing(ctx context.Context, token string) (Sharing, error) {
	site, err := site(s.Origin)
	if err != nil {
		return Sharing{}, err
	}
	return donorRead(
		ctx,
		s.DB,
		token,
		func(_ *queries.Queries, d queries.Donor, _ time.Time) (Sharing, error) { return sharing(d, site), nil },
	)
}

// SaveSharing sets the donor's public name, issuing a referral code on first
// use or a replacement when replace is set; a replaced code stops working.
func (s DonorService) SaveSharing(ctx context.Context, token, name string, replace bool) (Sharing, error) {
	site, err := site(s.Origin)
	if err != nil {
		return Sharing{}, err
	}
	name = identity.TrimSpace(name)
	if !plainText(name, false) || strings.Contains(name, "@") {
		return Sharing{}, ErrPublicNameCharacters
	}
	if name == "" || utf8.RuneCountInString(name) > 80 {
		return Sharing{}, ErrPublicName
	}
	return donorWrite(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, _ time.Time) (Sharing, error) {
		code := d.ReferralCode
		if !code.Valid || replace {
			value, err := randomToken(24)
			if err != nil {
				return Sharing{}, err
			}
			code = text(value)
		}
		d, err := queries.New(tx).SaveSharing(ctx, queries.SaveSharingParams{ID: d.ID, PublicName: text(name), ReferralCode: code})
		if err != nil {
			return Sharing{}, err
		}
		return sharing(d, site), nil
	})
}

// referralCode matches the codes SaveSharing issues; anything else cannot
// name a referrer.
var referralCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ReferrerName resolves a shareable code to its owner's chosen public name.
func (s DonorService) ReferrerName(ctx context.Context, code string) (string, error) {
	if !referralCode.MatchString(code) {
		return "", pgx.ErrNoRows
	}
	referrer, err := queries.New(s.DB).ReferrerByCode(ctx, text(code))
	if err != nil {
		return "", err
	}
	return referrer.PublicName.String, nil
}

// CaptureReferral records a visit through referral code, unless visitor
// already carries an unexpired capture: the first valid touch wins for its
// whole window. It returns the visitor token to keep, which is empty for an
// unknown code, with the captured referrer's public name.
func (s DonorService) CaptureReferral(ctx context.Context, code, kind, visitor string, now time.Time) (ReferralCapture, error) {
	if kind != "LINK" && kind != "IRL" {
		return ReferralCapture{}, payments.ErrFacts
	}
	return transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (ReferralCapture, error) {
		q := queries.New(tx)
		token, err := captureTouch(ctx, q, code, kind, visitor, now)
		if err != nil || token == "" {
			return ReferralCapture{}, err
		}
		name, err := q.CapturedReferrerName(ctx, HashToken(token))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ReferralCapture{}, err
		}
		return ReferralCapture{Token: token, PublicName: textPointer(name)}, nil
	})
}

func captureTouch(ctx context.Context, q *queries.Queries, code, kind, visitor string, now time.Time) (string, error) {
	if visitor != "" {
		touch, err := q.ReferralTouchByToken(ctx, HashToken(visitor))
		if err == nil && touch.ExpiresAt.Time.After(now) {
			return visitor, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}
	if !referralCode.MatchString(code) {
		return "", nil
	}
	referrer, err := q.ReferrerByCode(ctx, text(code))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	id, err := NewID()
	if err != nil {
		return "", err
	}
	return token, q.InsertReferralTouch(
		ctx,
		queries.InsertReferralTouchParams{
			ID:         id,
			TokenHash:  HashToken(token),
			ReferrerID: referrer.ID,
			Kind:       kind,
			TouchedAt:  timestamp(now),
			ExpiresAt:  timestamp(now.Add(referralWindow)),
		},
	)
}

func (s DonorService) Gifts(ctx context.Context, token string, offset int64) (GiftsPage, error) {
	if !pageOffsetValid(offset) {
		return GiftsPage{}, payments.ErrFacts
	}
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, _ time.Time) (GiftsPage, error) {
		gifts, err := q.DonorGiftsPage(ctx, queries.DonorGiftsPageParams{DonorID: d.ID, PageOffset: offset})
		if err != nil {
			return GiftsPage{}, err
		}
		out := GiftsPage{Email: d.Email, Mode: "test"}
		out.Gifts, out.NextOffset = page(gifts, offset)
		return out, nil
	})
}

// conversionPage bounds a conversions response, matching ReferralConversions.
const conversionPage = 20

// Conversions are the donor's latest referral conversions, newest first. More
// says older ones after since were left out.
type Conversions struct {
	Conversions []queries.ReferralConversionsRow `json:"conversions"`
	More        bool                             `json:"more"`
}

// Conversions lists the referrals that converted after since, if set: when
// each referred donor's first gift was paid, and its channel. Nothing about
// a referred donor is shown, as on the dashboard.
func (s DonorService) Conversions(ctx context.Context, token string, since *time.Time) (Conversions, error) {
	after := pgtype.Timestamptz{}
	if since != nil {
		after = timestamp(*since)
	}
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, _ time.Time) (Conversions, error) {
		rows, err := q.ReferralConversions(ctx, queries.ReferralConversionsParams{ReferrerID: d.ID, Since: after})
		if err != nil {
			return Conversions{}, err
		}
		if len(rows) > conversionPage {
			return Conversions{Conversions: rows[:conversionPage], More: true}, nil
		}
		return Conversions{Conversions: rows}, nil
	})
}

func (s DonorService) Receipt(ctx context.Context, token string, gift pgtype.UUID) (Receipt, error) {
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, now time.Time) (Receipt, error) {
		return readReceipt(ctx, q, d.ID, gift, now)
	})
}

func (s DonorService) AnnualSummary(ctx context.Context, token string, year int) (AnnualSummary, error) {
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, now time.Time) (AnnualSummary, error) {
		return readAnnualSummary(ctx, q, d.ID, year, now)
	})
}
