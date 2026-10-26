package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestWaitlistClaimsEarnFounding covers D-6: an imported waitlist address
// earns Founding Donor without a gift, whether its donor already exists or
// gives later; only a keyed hash of the address is stored; and claims stop
// counting and are deleted after they expire.
func TestWaitlistClaimsEarnFounding(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	_, admin := staffFixture(t, pool, "SUPER_ADMIN")
	_, growth := staffFixture(t, pool, "OP_GROWTH")
	staff := &StaffService{DB: as(t, dbroles.API)}
	key := FoundingClaimKey(bytes.Repeat([]byte{7}, 32))
	claims := FoundingClaimService{Staff: staff, Key: key}
	listed, listedEmail := donor(t, pool)
	unlisted, _ := donor(t, pool)
	later := "later-" + testID(t).String() + "@example.org"
	late := "late-" + testID(t).String() + "@example.org"
	hashes := []string{key.hash(listedEmail), key.hash(later), key.hash(late)}
	t.Cleanup(func() { cleanExec(t, "DELETE FROM donor_founding_claims WHERE email_hash=ANY($1)", hashes) })

	if err := (FoundingClaimService{Staff: staff}).Authorize(ctx, admin); !errors.Is(err, ErrFoundingClaimsUnconfigured) {
		t.Fatalf("import without a key: %v; want ErrFoundingClaimsUnconfigured", err)
	}
	sheet := "email,signed_up_on,source\n" + strings.ToUpper(listedEmail) + ",2025-06-01,WAITLIST\n" +
		listedEmail + ",2025-03-01,WEBSITE_DONOR\n" + later + ",2026-12-31,WAITLIST\n" + late + ",2026-11-30,WAITLIST\n"
	if _, err := claims.Import(ctx, growth, sheet); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("growth operator importing claims: %v; want ErrStaffForbidden", err)
	}
	bad := "email,signed_up_on,source\nnot-an-email,2027-01-01,FRIEND\n"
	var refused *PublicationError
	if _, err := claims.Import(ctx, admin, bad); !errors.As(err, &refused) || len(refused.Problems) != 3 {
		t.Fatalf("invalid sheet: %v; want a problem for each of the three cells", err)
	}
	result, err := claims.Import(ctx, admin, sheet)
	if err != nil || result.Rows != 3 {
		t.Fatalf("import: %+v, %v; want three distinct addresses", result, err)
	}
	var source, signedUp string
	if err := pool.QueryRow(ctx, "SELECT source, signed_up_on::text FROM donor_founding_claims WHERE email_hash=$1", hashes[0]).
		Scan(&source, &signedUp); err != nil || source != "WEBSITE_DONOR" || signedUp != "2025-03-01" {
		t.Fatalf("claim kept %s from %s, err %v; want the earliest sign-up", source, signedUp, err)
	}
	unkeyed := sha256.Sum256([]byte(listedEmail))
	var guessable int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_founding_claims WHERE email_hash=$1", hex.EncodeToString(unkeyed[:])).
		Scan(&guessable); err != nil || guessable != 0 {
		t.Fatalf("%d claims stored an unkeyed hash, err %v", guessable, err)
	}

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
		t.Fatal("a donor already on the waitlist did not earn Founding Donor")
	}
	if founding(unlisted, now) {
		t.Fatal("a donor not on the waitlist earned Founding Donor")
	}
	guest := func(email string, at time.Time) pgtype.UUID {
		t.Helper()
		var id pgtype.UUID
		if err := pgx.BeginFunc(ctx, as(t, dbroles.API), func(tx pgx.Tx) error {
			donor, err := lockGuest(ctx, queries.New(tx), key, email, at)
			id = donor.ID
			return err
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, query := range []string{"DELETE FROM donor_metric_projections WHERE donor_id=$1", "DELETE FROM donors WHERE id=$1"} {
				cleanExec(t, query, id)
			}
		})
		return id
	}
	if !founding(guest(later, now), now) {
		t.Fatal("a waitlisted address that gave after the import did not earn Founding Donor")
	}
	if founding(guest(late, FoundingClaimExpiry), FoundingClaimExpiry) {
		t.Fatal("an expired claim earned Founding Donor")
	}

	if _, err := (&TouchRetention{DB: as(t, dbroles.ProjectionWorker)}).PurgeNext(ctx, FoundingClaimExpiry); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_founding_claims WHERE email_hash=ANY($1)", hashes).Scan(&stored); err != nil ||
		stored != 0 {
		t.Fatalf("%d expired claims kept, err %v", stored, err)
	}
}
