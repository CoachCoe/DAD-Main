package store

import (
	"context"
	"errors"
	"time"

	"dad/donor-api/internal/badges"
	"dad/donor-api/internal/recognition"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BadgeService is the donor-facing badge read and the staff grant and revoke
// (PRD §6.9).
type BadgeService struct {
	DB    *pgxpool.Pool
	Staff *StaffService
}

// BadgeView is one badge's state for a donor: its catalog identity, how it is
// earned, and whether this donor has (earned badges show in color, unearned
// greyed with their rule, B-1).
type BadgeView struct {
	Code     string     `json:"code"`
	Title    string     `json:"title"`
	Rule     string     `json:"rule"`
	Perk     string     `json:"perk"`
	Earned   bool       `json:"earned"`
	EarnedAt *time.Time `json:"earned_at,omitempty"`
}

// signalsFor assembles the donor's current earning signals: the projection,
// the initiative exposures, amount changes, the ranking rollup, volunteering
// and their company team.
func signalsFor(ctx context.Context, q *queries.Queries, donor queries.Donor, now time.Time) (badges.Signals, error) {
	var s badges.Signals
	proj, err := q.Projection(ctx, donor.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, err
	}
	if err == nil {
		s.FirstGiftMade = proj.FirstGiftID.Valid
		s.FoundingQualified = proj.FoundingAwardedAt.Valid
		s.ConvertedReferrals = proj.ReferredLinkCount + proj.ReferredIrlCount
		s.HasIRLReferral = proj.ReferredIrlCount > 0
		if proj.StreakSince.Valid {
			since := proj.StreakSince.Time
			s.StreakDays = recognition.Streak{Since: &since}.Ladder(now, recognition.Zone(donor.Timezone.String)).Days
			first, err := q.DonorFirstGiftAt(ctx, donor.ID)
			if err != nil {
				return s, err
			}
			// A run whose first period came due after the donor's first gift is
			// a restart: an earlier run ended before it began.
			s.ComebackReached30 = first.Valid && first.Time.Before(since) && s.StreakDays >= 30
		}
	}
	if s.DistinctInitiatives, err = q.DistinctInitiativeExposures(ctx, donor.ID); err != nil {
		return s, err
	}
	if s.RaisedAmount, err = q.DonorRaisedAmount(ctx, donor.ID); err != nil {
		return s, err
	}
	rank, err := ForDonor(ctx, q, donor.ID)
	if err != nil {
		return s, err
	}
	if rank != nil {
		pct := rank.NationalPercentile
		s.NationalTopPercent = &pct
		s.CityRank = rank.CityRank
	}
	pref, err := readPreferences(ctx, q, donor.ID)
	if err != nil {
		return s, err
	}
	s.VolunteerOptIn = pref.Volunteer
	vol, err := q.DonorVolunteerStats(ctx, donor.ID)
	if err != nil {
		return s, err
	}
	s.EventCheckins = vol.Events
	link, err := q.EmployeeLink(ctx, donor.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, err
	}
	s.JoinedTeam = err == nil && link.TeamID.Valid
	return s, nil
}

// awardBadges records every badge the donor now qualifies for and queues the
// badge-earned push for each new one (§6.11). It runs wherever a donor's facts
// change, so earning does not wait for the donor to look: perks such as
// invite-only events depend on it (B-3). Earned badges stay earned (B-2).
func awardBadges(ctx context.Context, q *queries.Queries, donor queries.Donor, now time.Time) error {
	signals, err := signalsFor(ctx, q, donor, now)
	if err != nil {
		return err
	}
	for code := range badges.Evaluate(signals) {
		if err := earnBadge(ctx, q, donor.ID, code, now); err != nil {
			return err
		}
	}
	return nil
}

// earnBadge records one auto-earned badge, queueing its push if it is new. The
// push is deduped per badge, so retrying after a crash between the two is safe.
func earnBadge(ctx context.Context, q *queries.Queries, donor pgtype.UUID, code string, now time.Time) error {
	earned, err := q.EarnBadge(ctx, queries.EarnBadgeParams{DonorID: donor, Code: code, EarnedAt: timestamp(now)})
	if err != nil || earned == 0 {
		return err
	}
	return q.QueueBadgePush(ctx, queries.QueueBadgePushParams{
		DonorID: donor, Code: code, AsOf: timestamp(now), ExpiresAt: timestamp(now.Add(pushLifetime)),
	})
}

// Badges returns every catalog badge with this donor's earned state and its
// rule, so an unearned badge can show how to earn it (B-1). It brings the earn
// state up to date first, under the donor lock.
func (b BadgeService) Badges(ctx context.Context, token string) ([]BadgeView, error) {
	return donorWrite(ctx, b.DB, donorOwner(token), func(tx pgx.Tx, donor queries.Donor, now time.Time) ([]BadgeView, error) {
		q := queries.New(tx)
		if err := awardBadges(ctx, q, donor, now); err != nil {
			return nil, err
		}
		rows, err := q.DonorBadges(ctx, donor.ID)
		if err != nil {
			return nil, err
		}
		earned := map[string]*time.Time{}
		for _, r := range rows {
			if r.RevokedAt.Valid {
				continue
			}
			at := r.EarnedAt.Time
			earned[r.Code] = &at
		}
		catalog, err := badgeWording(ctx, q)
		if err != nil {
			return nil, err
		}
		views := make([]BadgeView, 0, len(catalog))
		for _, cat := range catalog {
			v := BadgeView{Code: cat.Code, Title: cat.Title, Rule: cat.Rule, Perk: string(cat.Perk)}
			if at, ok := earned[cat.Code]; ok {
				v.Earned = true
				v.EarnedAt = at
			}
			views = append(views, v)
		}
		return views, nil
	})
}

// Grant awards a badge to a donor by admin action (B-4), clearing any revoke.
func (b BadgeService) Grant(ctx context.Context, token string, donorID pgtype.UUID, code string) error {
	if _, ok := badges.Lookup(code); !ok {
		return ErrStaffInput
	}
	if b.Staff == nil {
		return ErrStaffDenied
	}
	_, err := staffDonorWrite(ctx, b.Staff, token, "badge.manage", donorID,
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (struct{}, error) {
			q := queries.New(tx)
			if err := q.GrantBadge(ctx, queries.GrantBadgeParams{DonorID: donorID, Code: code, EarnedAt: timestamp(now)}); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, auditStaff(ctx, q, actor.ID, "badge.granted", donorID.String(), map[string]string{"code": code}, now)
		})
	return err
}

// Revoke removes a badge from a donor by admin action (B-4), audited.
func (b BadgeService) Revoke(ctx context.Context, token string, donorID pgtype.UUID, code string) error {
	if _, ok := badges.Lookup(code); !ok {
		return ErrStaffInput
	}
	if b.Staff == nil {
		return ErrStaffDenied
	}
	_, err := staffDonorWrite(ctx, b.Staff, token, "badge.manage", donorID,
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (struct{}, error) {
			q := queries.New(tx)
			if err := q.RevokeBadge(ctx, queries.RevokeBadgeParams{DonorID: donorID, Code: code, RevokedAt: timestamp(now)}); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, auditStaff(ctx, q, actor.ID, "badge.revoked", donorID.String(), map[string]string{"code": code}, now)
		})
	return err
}
