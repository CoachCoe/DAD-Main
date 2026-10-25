package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// earnedSet returns the codes a badge view list reports as earned.
func earnedSet(views []BadgeView) map[string]bool {
	m := map[string]bool{}
	for _, v := range views {
		if v.Earned {
			m[v.Code] = true
		}
	}
	return m
}

func TestBadgesAutoEarnPermanenceAndAdminGrantRevoke(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	donorID, donorToken := signedInDonor(t, pool)
	b := BadgeService{DB: pool, Staff: &StaffService{DB: as(t, dbroles.API)}}
	_, adminToken := staffFixture(t, pool, "SUPER_ADMIN")
	_, support := staffFixture(t, pool, "OP_PARTNERSHIPS")

	// With no projection/signals, only badges with no requirement are earned
	// (none) — the catalog still returns all 24 as views.
	views, err := b.Badges(ctx, donorToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 24 {
		t.Fatalf("expected 24 badge views, got %d", len(views))
	}
	if len(earnedSet(views)) != 0 {
		t.Fatalf("no signals but badges earned: %v", earnedSet(views))
	}

	// Seed a projection that qualifies the donor for first-gift + founding +
	// a 100-day streak + 3 referrals. Then a read auto-earns them.
	seedBadgeProjection(t, pool, donorID, time.Now().UTC())
	views, err = b.Badges(ctx, donorToken)
	if err != nil {
		t.Fatal(err)
	}
	got := earnedSet(views)
	for _, code := range []string{"1D", "F", "7", "30", "100", "REF1", "REF3"} {
		if !got[code] {
			t.Fatalf("expected auto-earned %q; earned=%v", code, got)
		}
	}

	// Permanence (B-2): wipe the projection; the earned badges must remain.
	if _, err := pool.Exec(ctx, "DELETE FROM donor_metric_projections WHERE donor_id=$1", donorID); err != nil {
		t.Fatal(err)
	}
	views, err = b.Badges(ctx, donorToken)
	if err != nil {
		t.Fatal(err)
	}
	if !earnedSet(views)["100"] {
		t.Fatal("badge un-earned after signal removed (should be permanent)")
	}

	// Admin grant of an inert/not-yet-earned badge; SUPPORT is refused.
	if err := b.Grant(ctx, support, donorID, "V"); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("SUPPORT granted a badge: %v", err)
	}
	if err := b.Grant(ctx, adminToken, donorID, "V"); err != nil {
		t.Fatal(err)
	}
	if err := b.Grant(ctx, adminToken, donorID, "NOT_A_BADGE"); !errors.Is(err, ErrStaffInput) {
		t.Fatalf("unknown badge code granted: %v", err)
	}
	views, _ = b.Badges(ctx, donorToken)
	if !earnedSet(views)["V"] {
		t.Fatal("admin-granted badge not shown earned")
	}

	// Admin revoke: the badge reads as unearned afterward (B-4).
	if err := b.Revoke(ctx, adminToken, donorID, "V"); err != nil {
		t.Fatal(err)
	}
	views, _ = b.Badges(ctx, donorToken)
	if earnedSet(views)["V"] {
		t.Fatal("revoked badge still shown earned")
	}
}

// seedBadgeProjection writes a projection row that qualifies the donor for the
// first-gift, founding, 100-day-streak and 3-referral badges.
func seedBadgeProjection(t *testing.T, pool *pgxpool.Pool, donorID pgtype.UUID, now time.Time) {
	t.Helper()
	id, _ := NewID()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO donor_metric_projections
		 (id, donor_id, first_gift_id, founding_awarded_at, best_streak, current_streak,
		  streak_since, referred_link_count, referred_irl_count,
		  computed_at, next_refresh_at)
		 VALUES ($1,$2,'gift-1',$3,4,4,$4,2,1,$3,$5)
		 ON CONFLICT (donor_id) DO UPDATE SET
		   first_gift_id=EXCLUDED.first_gift_id, founding_awarded_at=EXCLUDED.founding_awarded_at,
		   best_streak=EXCLUDED.best_streak, current_streak=EXCLUDED.current_streak,
		   streak_since=EXCLUDED.streak_since, referred_link_count=EXCLUDED.referred_link_count,
		   referred_irl_count=EXCLUDED.referred_irl_count, next_refresh_at=EXCLUDED.next_refresh_at`,
		id, donorID, now, now.Add(-100*24*time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
}

var _ = pgx.ErrNoRows
