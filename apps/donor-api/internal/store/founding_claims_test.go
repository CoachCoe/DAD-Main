package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestWaitlistClaimsEarnFounding covers D-6: an imported waitlist address earns
// its donor Founding Donor without a gift, and claims stop counting and are
// deleted after they expire.
func TestWaitlistClaimsEarnFounding(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	_, admin := staffFixture(t, pool, "SUPER_ADMIN")
	_, growth := staffFixture(t, pool, "OP_GROWTH")
	claims := FoundingClaimService{Staff: &StaffService{DB: as(t, dbroles.API)}}
	listed, listedEmail := donor(t, pool)
	late, lateEmail := donor(t, pool)
	unlisted, _ := donor(t, pool)
	hashes := []string{FoundingEmailHash(listedEmail), FoundingEmailHash(lateEmail)}
	t.Cleanup(func() { cleanExec(t, "DELETE FROM donor_founding_claims WHERE email_hash=ANY($1)", hashes) })

	sheet := "email,signed_up_on,source\n" + strings.ToUpper(listedEmail) + ",2025-06-01,WAITLIST\n" +
		listedEmail + ",2025-03-01,WEBSITE_DONOR\n" + lateEmail + ",2026-12-31,WAITLIST\n"
	if _, err := claims.Import(ctx, growth, sheet); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("growth operator importing claims: %v; want ErrStaffForbidden", err)
	}
	bad := "email,signed_up_on,source\nnot-an-email,2027-01-01,FRIEND\n"
	var refused *PublicationError
	if _, err := claims.Import(ctx, admin, bad); !errors.As(err, &refused) || len(refused.Problems) != 3 {
		t.Fatalf("invalid sheet: %v; want a problem for each of the three cells", err)
	}
	result, err := claims.Import(ctx, admin, sheet)
	if err != nil || result.Rows != 2 {
		t.Fatalf("import: %+v, %v; want two distinct addresses", result, err)
	}
	var source, signedUp string
	if err := pool.QueryRow(ctx, "SELECT source, signed_up_on::text FROM donor_founding_claims WHERE email_hash=$1", hashes[0]).
		Scan(&source, &signedUp); err != nil || source != "WEBSITE_DONOR" || signedUp != "2025-03-01" {
		t.Fatalf("claim kept %s from %s, err %v; want the earliest sign-up", source, signedUp, err)
	}
	var stored int

	founding := func(id pgtype.UUID, at time.Time) bool {
		t.Helper()
		var awarded pgtype.Timestamptz
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			p, err := projectFacts(ctx, queries.New(tx), id, at)
			awarded = p.FoundingAwardedAt
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return awarded.Valid
	}
	now := time.Now().UTC()
	if !founding(listed, now) {
		t.Fatal("a donor on the waitlist did not earn Founding Donor")
	}
	if founding(unlisted, now) {
		t.Fatal("a donor not on the waitlist earned Founding Donor")
	}
	if founding(late, FoundingClaimExpiry) {
		t.Fatal("an expired claim earned Founding Donor")
	}

	if _, err := (&TouchRetention{DB: as(t, dbroles.ProjectionWorker)}).PurgeNext(ctx, FoundingClaimExpiry); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_founding_claims WHERE email_hash=ANY($1)", hashes).Scan(&stored); err != nil ||
		stored != 0 {
		t.Fatalf("%d expired claims kept, err %v", stored, err)
	}
}
