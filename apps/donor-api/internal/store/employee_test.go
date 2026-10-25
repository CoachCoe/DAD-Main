package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// approvedPartner creates an APPROVED partner org with a company code and match
// terms, returning its id.
func approvedPartner(t *testing.T, pool *pgxpool.Pool, org, code string, rateBP int32, capMinor int64) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	id, _ := NewID()
	mustExec(
		t,
		pool,
		`INSERT INTO donor_partner_signups (id, organization, name, email, website, message, request_hash, consent_version, approved_at, company_code, match_rate_bp, match_cap_minor)
		 VALUES ($1,$2,'C','c@x.test','https://x.test','',$3,'v1',now(),$4,$5,$6)`,
		id,
		org,
		"h-"+code,
		code,
		rateBP,
		capMinor,
	)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM donor_partner_match_payments WHERE partner_id=$1", id)
		_, _ = pool.Exec(ctx, "DELETE FROM donor_partner_employees WHERE partner_id=$1", id)
		_, _ = pool.Exec(ctx, "DELETE FROM donor_partner_teams WHERE partner_id=$1", id)
		_, _ = pool.Exec(ctx, "DELETE FROM donor_partner_signups WHERE id=$1", id)
	})
	return id
}

// linkDonorWithGift makes a donor, links them to partnerID, and records a gift
// paid at paidAt. Returns the donor id.
func linkDonorWithGift(t *testing.T, pool *pgxpool.Pool, partnerID pgtype.UUID, amount int64, paidAt time.Time, streak int32) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	id, _ := donor(t, pool)
	mustExec(t, pool, "INSERT INTO donor_partner_employees (donor_id, partner_id, enrolled_at) VALUES ($1,$2,now())", id, partnerID)
	if amount > 0 {
		gift, _ := NewID()
		chk, _ := NewID()
		mustExec(t, pool,
			"INSERT INTO donor_checkouts (id, donor_id, amount_minor, frequency) VALUES ($1,$2,$3,'MONTHLY')", chk, id, amount)
		mustExec(t, pool,
			`INSERT INTO donor_gifts (id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at)
			 VALUES ($1,$2,$3,$4,$5,'USD','MONTHLY',$6)`,
			gift, id, chk, "pi_"+gift.String(), amount, paidAt)
	}
	mustExec(t, pool,
		`INSERT INTO donor_metric_projections (donor_id, current_streak, best_streak, computed_at, next_refresh_at, id)
		 VALUES ($1,$2,$2,now(),now()+interval '1 day', $3) ON CONFLICT (donor_id) DO UPDATE SET current_streak=$2`,
		id, streak, mustNewID(t))
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM donor_gifts WHERE donor_id=$1", id)
		_, _ = pool.Exec(ctx, "DELETE FROM donor_checkouts WHERE donor_id=$1", id)
		_, _ = pool.Exec(ctx, "DELETE FROM donor_metric_projections WHERE donor_id=$1", id)
	})
	return id
}

func mustNewID(t *testing.T) pgtype.UUID {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEmployeeMatchingAndKAnonymity(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	emp := EmployeeService{DB: pool}
	staff := &StaffService{DB: as(t, dbroles.API)}
	_, superToken := staffFixture(t, pool, "SUPER_ADMIN")

	// 10% match, $50 annual cap.
	partnerID := approvedPartner(t, pool, "Delta Inc", "DELTA", 1000, 5000)
	thisYear := int32(time.Now().UTC().Year())
	jan := time.Date(int(thisYear), 1, 15, 0, 0, 0, 0, time.UTC)
	lastYear := jan.AddDate(-1, 0, 0)

	// Four employees this year ($100 each) — under the k-anon threshold of 5.
	for i := 0; i < 4; i++ {
		linkDonorWithGift(t, pool, partnerID, 10000, jan, int32(10+i))
	}

	// Our People is SUPPRESSED at 4 linked employees.
	// (Driven through a partner session for the org.)
	_, adminID := approvePartnerAdmin(t, pool, partnerID)
	portal := PartnerPortalService{DB: pool}
	adminSession := issueTestPartnerSession(t, pool, adminID)
	op, err := emp.OurPeople(ctx, adminSession, portal)
	if err != nil {
		t.Fatal(err)
	}
	if !op.Suppressed || op.Enrolled != 0 || op.GivenMinor != 0 {
		t.Fatalf("expected suppression under 5: %+v", op)
	}

	// A fifth employee crosses the threshold; now figures are shown.
	linkDonorWithGift(t, pool, partnerID, 10000, jan, 20)
	op, err = emp.OurPeople(ctx, adminSession, portal)
	if err != nil {
		t.Fatal(err)
	}
	if op.Suppressed || op.Enrolled != 5 || op.GivenMinor != 50000 {
		t.Fatalf("expected 5 enrolled / $500 given: %+v", op)
	}

	// Matching: 5 × $100 × 10% = $50 owed, exactly at the $50 cap.
	status, err := emp.Match(ctx, staff, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.GiftsMinor != 50000 || status.OwedMinor != 5000 {
		t.Fatalf("expected $500 gifts / $50 owed (capped): %+v", status)
	}

	// A sixth employee's gift pushes raw match over the cap — owed stays capped.
	linkDonorWithGift(t, pool, partnerID, 10000, jan, 5)
	status, err = emp.Match(ctx, staff, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.OwedMinor != 5000 {
		t.Fatalf("owed must stay at the $50 cap: %+v", status)
	}

	// A gift in LAST year does not count toward this year (annual reset).
	linkDonorWithGift(t, pool, partnerID, 10000, lastYear, 1)
	status, err = emp.Match(ctx, staff, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.GiftsMinor != 60000 { // the six this-year $100 gifts only
		t.Fatalf("last-year gift leaked into this year: %+v", status)
	}

	// Recording an offline payment reduces the outstanding balance; no charge.
	if err := emp.RecordMatchPayment(ctx, staff, superToken, partnerID, thisYear, 2000); err != nil {
		t.Fatal(err)
	}
	status, err = emp.Match(ctx, staff, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.PaidMinor != 2000 || status.OutstandingMinor != 3000 {
		t.Fatalf("expected $20 paid / $30 outstanding: %+v", status)
	}

	// An operator without partner.manage cannot read the match.
	_, growthToken := staffFixture(t, pool, "OP_GROWTH")
	if _, err := emp.Match(ctx, staff, growthToken, partnerID, thisYear); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("OP_GROWTH read the match: %v", err)
	}
}

func TestEmployeeEnrollAndTeams(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	emp := EmployeeService{DB: pool}
	partnerID := approvedPartner(t, pool, "Gamma Co", "GAMMA", 0, 0)
	portal := PartnerPortalService{DB: pool}
	_, adminID := approvePartnerAdmin(t, pool, partnerID)
	adminSession := issueTestPartnerSession(t, pool, adminID)

	// A donor enrolls with a good code; a bad code is refused.
	_, donorToken := signedInDonor(t, pool)
	if _, err := emp.Enroll(ctx, donorToken, "nope!"); !errors.Is(err, ErrEmployeeLink) {
		t.Fatalf("bad code accepted: %v", err)
	}
	if _, err := emp.Enroll(ctx, donorToken, "UNKNOWNCO"); !errors.Is(err, ErrEmployeeLink) {
		t.Fatalf("unknown code accepted: %v", err)
	}
	org, err := emp.Enroll(ctx, donorToken, "gamma")
	if err != nil || org != "Gamma Co" {
		t.Fatalf("enroll: %q %v", org, err)
	}

	// Joining a team is reflected in /donor/me/team.
	if _, err := emp.JoinTeam(ctx, donorToken, "Engineering"); err != nil {
		t.Fatal(err)
	}
	team, err := emp.Team(ctx, donorToken)
	if err != nil || team.Organization != "Gamma Co" || team.TeamName != "Engineering" {
		t.Fatalf("team: %+v %v", team, err)
	}

	// Leaderboard suppresses a team under 5 members (just this one donor).
	rows, err := emp.Teams(ctx, adminSession, portal)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("team under 5 members must be suppressed: %+v", rows)
	}
}

// approvePartnerAdmin creates a first ADMIN partner user for an approved
// partner, returning (userID, userID) — the id is reused as the session owner.
func approvePartnerAdmin(t *testing.T, pool *pgxpool.Pool, partnerID pgtype.UUID) (pgtype.UUID, string) {
	t.Helper()
	ctx := context.Background()
	uid := mustNewID(t)
	mustExec(t, pool,
		`INSERT INTO donor_partner_users (id, partner_id, email, name, role, accepted_at, created_at)
		 VALUES ($1,$2,$3,'Admin','ADMIN',now(),now())`,
		uid, partnerID, "admin-"+uid.String()+"@x.test")
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM donor_partner_users WHERE id=$1", uid) })
	return uid, uid.String()
}
