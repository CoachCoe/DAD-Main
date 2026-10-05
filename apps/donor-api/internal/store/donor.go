package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

const ConsentVersion = "r1-notifications-v1"

var ErrPreferenceConflict = errors.New("preferences changed; refresh before saving")
var ErrPublicName = errors.New("choose a public first name or nickname of 1 to 80 characters")

type DonorService struct {
	DB     *pgxpool.Pool
	Origin string
}
type Profile struct {
	Email           string  `json:"email"`
	PublicName      *string `json:"public_name"`
	ReferralEmails  bool    `json:"referral_emails"`
	MarketingEmails bool    `json:"marketing_emails"`
	Version         int32   `json:"version"`
	ConsentVersion  string  `json:"consent_version"`
}
type Preferences struct {
	Version                         int32
	ReferralEmails, MarketingEmails bool
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

func textPointer(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
func randomToken(size int) (string, error) {
	secret := make([]byte, size)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}

func donorTransaction[T any](ctx context.Context, pool *pgxpool.Pool, token string, write bool, operation func(*queries.Queries, queries.Donor) (T, error)) (T, error) {
	var empty T
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	if write {
		options = pgx.TxOptions{}
	}
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	donor, err := Authenticated(ctx, q, token, time.Now())
	if err != nil {
		return empty, err
	}
	if write {
		if _, err = q.LockDonor(ctx, donor.ID); err != nil {
			return empty, err
		}
		donor, err = Authenticated(ctx, q, token, time.Now())
		if err != nil {
			return empty, err
		}
	}
	result, err := operation(q, donor)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return result, nil
}
func readProfile(ctx context.Context, q *queries.Queries, donor queries.Donor) (Profile, error) {
	out := Profile{Email: donor.Email, PublicName: textPointer(donor.PublicName), ConsentVersion: ConsentVersion}
	pref, err := q.DonorPreference(ctx, donor.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.ReferralEmails = pref.ReferralEmails
	out.MarketingEmails = pref.MarketingEmails
	out.Version = pref.Version
	return out, nil
}
func (s DonorService) Profile(ctx context.Context, token string) (Profile, error) {
	return donorTransaction(ctx, s.DB, token, false, func(q *queries.Queries, d queries.Donor) (Profile, error) { return readProfile(ctx, q, d) })
}
func (s DonorService) SavePreferences(ctx context.Context, token string, pref Preferences) (Profile, error) {
	return donorTransaction(ctx, s.DB, token, true, func(q *queries.Queries, d queries.Donor) (Profile, error) {
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
		if err = q.SavePreference(ctx, queries.SavePreferenceParams{ID: id, DonorID: d.ID, Version: version, ReferralEmails: pref.ReferralEmails, MarketingEmails: pref.MarketingEmails}); err != nil {
			return Profile{}, err
		}
		event, err := NewID()
		if err != nil {
			return Profile{}, err
		}
		if err = q.RecordPreferenceConsent(ctx, queries.RecordPreferenceConsentParams{ID: event, DonorID: d.ID, Version: version, ReferralEmails: pref.ReferralEmails, MarketingEmails: pref.MarketingEmails}); err != nil {
			return Profile{}, err
		}
		return readProfile(ctx, q, d)
	})
}
func (s DonorService) SignOutEverywhere(ctx context.Context, token string) error {
	_, err := donorTransaction(ctx, s.DB, token, true, func(q *queries.Queries, d queries.Donor) (bool, error) {
		return true, q.RevokeAllSessions(ctx, queries.RevokeAllSessionsParams{DonorID: d.ID, RevokedAt: timestamp(time.Now())})
	})
	return err
}
func sharing(d queries.Donor, origin string) Sharing {
	out := Sharing{PublicName: textPointer(d.PublicName)}
	if d.ReferralCode.Valid && d.ReferralCode.String != "" && d.PublicName.Valid && d.PublicName.String != "" {
		link := origin + "/r/" + d.ReferralCode.String
		irl := link + "?via=irl"
		out.Link = &link
		out.IRLLink = &irl
	}
	return out
}
func (s DonorService) Sharing(ctx context.Context, token string) (Sharing, error) {
	if err := payments.ValidateOrigin(s.Origin); err != nil {
		return Sharing{}, err
	}
	return donorTransaction(ctx, s.DB, token, false, func(q *queries.Queries, d queries.Donor) (Sharing, error) { return sharing(d, s.Origin), nil })
}
func (s DonorService) SaveSharing(ctx context.Context, token, name string, replace bool) (Sharing, error) {
	if err := payments.ValidateOrigin(s.Origin); err != nil {
		return Sharing{}, err
	}
	name = identity.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 80 || strings.Contains(name, "@") || strings.ContainsFunc(name, func(r rune) bool { return r < 32 }) {
		return Sharing{}, ErrPublicName
	}
	return donorTransaction(ctx, s.DB, token, true, func(q *queries.Queries, d queries.Donor) (Sharing, error) {
		code := d.ReferralCode
		if !code.Valid || replace {
			value, err := randomToken(24)
			if err != nil {
				return Sharing{}, err
			}
			code = pgtype.Text{String: value, Valid: true}
		}
		d, err := q.SaveSharing(ctx, queries.SaveSharingParams{ID: d.ID, PublicName: pgtype.Text{String: name, Valid: true}, ReferralCode: code})
		if err != nil {
			return Sharing{}, err
		}
		return sharing(d, s.Origin), nil
	})
}

// ReferrerName resolves a shareable code to its owner's chosen public name.
func (s DonorService) ReferrerName(ctx context.Context, code string) (string, error) {
	referrer, err := queries.New(s.DB).ReferrerByCode(ctx, pgtype.Text{String: code, Valid: true})
	if err != nil {
		return "", err
	}
	return referrer.PublicName.String, nil
}

func (s DonorService) CaptureReferral(ctx context.Context, code, kind, visitor string, now time.Time) (ReferralCapture, error) {
	if kind != "LINK" && kind != "IRL" {
		return ReferralCapture{}, payments.ErrFacts
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return ReferralCapture{}, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	token := ""
	if visitor != "" {
		touch, err := q.ReferralTouchByToken(ctx, HashToken(visitor))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ReferralCapture{}, err
		}
		if err == nil && touch.ExpiresAt.Time.After(now) {
			token = visitor
		}
	}
	if token == "" {
		referrer, err := q.ReferrerByCode(ctx, pgtype.Text{String: code, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return ReferralCapture{}, nil
		}
		if err != nil {
			return ReferralCapture{}, err
		}
		token, err = randomToken(32)
		if err != nil {
			return ReferralCapture{}, err
		}
		id, err := NewID()
		if err != nil {
			return ReferralCapture{}, err
		}
		if err = q.InsertReferralTouch(ctx, queries.InsertReferralTouchParams{ID: id, TokenHash: HashToken(token), ReferrerID: referrer.ID, Kind: kind, TouchedAt: timestamp(now), ExpiresAt: timestamp(now.Add(30 * 24 * time.Hour))}); err != nil {
			return ReferralCapture{}, err
		}
	}
	name, err := q.CapturedReferrerName(ctx, HashToken(token))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ReferralCapture{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ReferralCapture{}, err
	}
	return ReferralCapture{Token: token, PublicName: textPointer(name)}, nil
}
func (s DonorService) Gifts(ctx context.Context, token string, offset int64) (GiftsPage, error) {
	if offset < 0 || offset > math.MaxInt64-50 {
		return GiftsPage{}, payments.ErrFacts
	}
	return donorTransaction(ctx, s.DB, token, false, func(q *queries.Queries, d queries.Donor) (GiftsPage, error) {
		gifts, err := q.DonorGiftsPage(ctx, queries.DonorGiftsPageParams{DonorID: d.ID, PageOffset: offset})
		if err != nil {
			return GiftsPage{}, err
		}
		out := GiftsPage{Email: d.Email, Mode: "test", Gifts: gifts}
		if len(gifts) > 50 {
			next := offset + 50
			out.NextOffset = &next
			out.Gifts = gifts[:50]
		}
		return out, nil
	})
}
func (s DonorService) Receipt(ctx context.Context, token string, gift pgtype.UUID) (Receipt, error) {
	return donorTransaction(ctx, s.DB, token, false, func(q *queries.Queries, d queries.Donor) (Receipt, error) {
		return ReadReceipt(ctx, q, d.ID, gift, time.Now())
	})
}
func (s DonorService) AnnualSummary(ctx context.Context, token string, year int) (AnnualSummary, error) {
	return donorTransaction(ctx, s.DB, token, false, func(q *queries.Queries, d queries.Donor) (AnnualSummary, error) {
		return ReadAnnualSummary(ctx, q, d.ID, year, time.Now())
	})
}
