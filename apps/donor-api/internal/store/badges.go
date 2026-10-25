package store

import (
	"context"
	"time"

	"dad/donor-api/internal/badges"
	"dad/donor-api/internal/recognition"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BadgeService is the donor-facing badge read and the staff grant/revoke
// (PRD §6.9). Earning is automatic and permanent: a read re-evaluates current
// qualification from the donor's signals, records any newly-earned badge, and
// returns the union with the stored earn-state so a badge never un-earns (B-2).
type BadgeService struct {
	DB    *pgxpool.Pool
	Staff *StaffService
}

// BadgeView is one badge's state for a donor: its catalog identity and whether
// this donor has earned it (earned badges show in color, unearned greyed — B-1).
type BadgeView struct {
	Code     string     `json:"code"`
	Title    string     `json:"title"`
	Perk     string     `json:"perk"`
	Earned   bool       `json:"earned"`
	EarnedAt *time.Time `json:"earned_at,omitempty"`
}

// signalsFor assembles the donor's current earning signals from the projection,
// the initiative-exposure ledger and the amount-change history. Ranking signals
// (national %, city rank) are filled once the ranking rollup (3.3) is wired;
// until then they are nil and the ranking badges simply do not auto-earn.
func signalsFor(ctx context.Context, q *queries.Queries, donorID pgtype.UUID, now time.Time) (badges.Signals, error) {
	var s badges.Signals
	proj, err := q.Projection(ctx, donorID)
	if err != nil && err != pgx.ErrNoRows {
		return s, err
	}
	if err == nil {
		s.FirstGiftMade = proj.FirstGiftID.Valid
		s.FoundingQualified = proj.FoundingAwardedAt.Valid
		s.ConvertedReferrals = proj.ReferredLinkCount + proj.ReferredIrlCount
		s.HasIRLReferral = proj.ReferredIrlCount > 0
		var since *time.Time
		if proj.StreakSince.Valid {
			since = &proj.StreakSince.Time
		}
		s.StreakDays = (recognition.Streak{Since: since}).Ladder(now).Days
		// Comeback: a prior run lapsed (best exceeds the current run) and the
		// current run has climbed back to 30 days.
		if proj.CurrentStreak.Valid && proj.BestStreak > proj.CurrentStreak.Int32 && s.StreakDays >= 30 {
			s.ComebackReached30 = true
		}
	}
	shown, err := q.DistinctInitiativeExposures(ctx, donorID)
	if err != nil {
		return s, err
	}
	s.DistinctInitiatives = shown
	raised, err := q.DonorRaisedAmount(ctx, donorID)
	if err != nil {
		return s, err
	}
	s.RaisedAmount = raised
	// Ranking badges (Top 2% / Top 50 City) draw from the periodic rollup; an
	// unranked donor simply does not earn them.
	rank, err := ForDonor(ctx, q, donorID)
	if err != nil {
		return s, err
	}
	if rank != nil {
		pct := rank.NationalPercentile
		s.NationalTopPercent = &pct
		s.CityRank = rank.CityRank
	}
	// Volunteer signals (Phase 4.5): the opt-in from preferences lights the
	// Volunteer badge; checked-in events drive Showed Up / Volunteer ×N.
	pref, err := readPreferences(ctx, q, donorID)
	if err != nil {
		return s, err
	}
	s.VolunteerOptIn = pref.Volunteer
	vol, err := q.DonorVolunteerStats(ctx, donorID)
	if err != nil {
		return s, err
	}
	s.EventCheckins = vol.Events
	return s, nil
}

// Badges returns every catalog badge with this donor's earned state, recording
// any newly-earned badge first. The read holds the donor lock so the earn write
// is serialized with other changes to the donor's facts.
func (b BadgeService) Badges(ctx context.Context, token string) ([]BadgeView, error) {
	return donorWrite(ctx, b.DB, donorOwner(token), func(tx pgx.Tx, donor queries.Donor, now time.Time) ([]BadgeView, error) {
		q := queries.New(tx)
		signals, err := signalsFor(ctx, q, donor.ID, now)
		if err != nil {
			return nil, err
		}
		qualifies := badges.Evaluate(signals)
		for code := range qualifies {
			earned, err := q.EarnBadge(ctx, queries.EarnBadgeParams{DonorID: donor.ID, Code: code, EarnedAt: timestamp(now)})
			if err != nil {
				return nil, err
			}
			// Fire the badge-earned push (§6.11) only for a genuinely new earn;
			// re-evaluating an already-earned badge affects no rows. The intent
			// is itself deduped per (donor, badge), so a crash between the earn
			// and the queue is safe to retry.
			if earned > 0 {
				if err := q.QueueBadgePush(ctx, queries.QueueBadgePushParams{
					DonorID: donor.ID, Code: code, AsOf: timestamp(now), ExpiresAt: timestamp(now.Add(pushLifetime)),
				}); err != nil {
					return nil, err
				}
			}
		}
		rows, err := q.DonorBadges(ctx, donor.ID)
		if err != nil {
			return nil, err
		}
		earned := map[string]*time.Time{}
		for _, r := range rows {
			if r.RevokedAt.Valid { // a revoked badge reads as unearned (B-4)
				continue
			}
			at := r.EarnedAt.Time
			earned[r.Code] = &at
		}
		views := make([]BadgeView, 0, len(badges.Catalog))
		for _, cat := range badges.Catalog {
			v := BadgeView{Code: cat.Code, Title: cat.Title, Perk: string(cat.Perk)}
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
