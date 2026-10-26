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
		cleanExec(t, "DELETE FROM donor_partner_match_payments WHERE partner_id=$1", id)
		cleanExec(t, "DELETE FROM donor_partner_employees WHERE partner_id=$1", id)
		cleanExec(t, "DELETE FROM donor_partner_teams WHERE partner_id=$1", id)
		cleanExec(t, "DELETE FROM donor_partner_signups WHERE id=$1", id)
	})
	return id
}

// linkDonorWithGift makes a donor, links them to partnerID, and records a gift
// paid at paidAt. Returns the donor id.
func linkDonorWithGift(t *testing.T, pool *pgxpool.Pool, partnerID pgtype.UUID, amount int64, paidAt time.Time, streak int32) pgtype.UUID {
	t.Helper()
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
		cleanExec(t, "DELETE FROM donor_gifts WHERE donor_id=$1", id)
		cleanExec(t, "DELETE FROM donor_checkouts WHERE donor_id=$1", id)
		cleanExec(t, "DELETE FROM donor_metric_projections WHERE donor_id=$1", id)
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
	emp := EmployeeService{DB: pool, Staff: &StaffService{DB: as(t, dbroles.API)}}
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
	adminSession := issueTestPartnerSession(t, pool, adminID)
	op, err := emp.OurPeople(ctx, adminSession)
	if err != nil {
		t.Fatal(err)
	}
	if !op.Suppressed || op.Enrolled != 0 || op.GivenMinor != 0 {
		t.Fatalf("expected suppression under 5: %+v", op)
	}

	// A fifth employee crosses the threshold; now figures are shown.
	linkDonorWithGift(t, pool, partnerID, 10000, jan, 20)
	op, err = emp.OurPeople(ctx, adminSession)
	if err != nil {
		t.Fatal(err)
	}
	if op.Suppressed || op.Enrolled != 5 || op.GivenMinor != 50000 {
		t.Fatalf("expected 5 enrolled / $500 given: %+v", op)
	}

	// Matching: 5 × $100 × 10% = $50 owed, exactly at the $50 cap.
	status, err := emp.Match(ctx, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.GiftsMinor != 50000 || status.OwedMinor != 5000 {
		t.Fatalf("expected $500 gifts / $50 owed (capped): %+v", status)
	}

	// A sixth employee's gift pushes raw match over the cap — owed stays capped.
	linkDonorWithGift(t, pool, partnerID, 10000, jan, 5)
	status, err = emp.Match(ctx, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.OwedMinor != 5000 {
		t.Fatalf("owed must stay at the $50 cap: %+v", status)
	}

	// A refunded gift no longer counts toward the match or the giving total.
	refunded := linkDonorWithGift(t, pool, partnerID, 10000, jan, 3)
	mustExec(t, pool, `INSERT INTO donor_refunds (id, gift_id, stripe_refund_id, amount_minor, currency, status, observed_at)
		SELECT gen_random_uuid(), id, 're_'||id::text, amount_minor, 'USD', 'SUCCEEDED', now() FROM donor_gifts WHERE donor_id=$1`, refunded)
	t.Cleanup(func() {
		cleanExec(t, "DELETE FROM donor_refunds WHERE gift_id IN (SELECT id FROM donor_gifts WHERE donor_id=$1)", refunded)
	})
	op, err = emp.OurPeople(ctx, adminSession)
	if err != nil || op.GivenMinor != 60000 || op.MatchedMinor != 5000 {
		t.Fatalf("refunded gift counted, or match not shown to the partner: %+v %v", op, err)
	}

	// A gift in LAST year does not count toward this year (annual reset).
	linkDonorWithGift(t, pool, partnerID, 10000, lastYear, 1)
	status, err = emp.Match(ctx, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.GiftsMinor != 60000 { // the six unrefunded this-year $100 gifts only
		t.Fatalf("last-year gift leaked into this year: %+v", status)
	}

	// Recording an offline payment reduces the outstanding balance; no charge.
	if err := emp.RecordMatchPayment(ctx, superToken, partnerID, thisYear, 2000); err != nil {
		t.Fatal(err)
	}
	status, err = emp.Match(ctx, superToken, partnerID, thisYear)
	if err != nil {
		t.Fatal(err)
	}
	if status.PaidMinor != 2000 || status.OutstandingMinor != 3000 {
		t.Fatalf("expected $20 paid / $30 outstanding: %+v", status)
	}

	// An operator without partner.manage cannot read the match.
	_, growthToken := staffFixture(t, pool, "OP_GROWTH")
	if _, err := emp.Match(ctx, growthToken, partnerID, thisYear); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("OP_GROWTH read the match: %v", err)
	}
}

func TestEmployeeEnrollAndTeams(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	emp := EmployeeService{DB: pool}
	partnerID := approvedPartner(t, pool, "Gamma Co", "GAMMA", 0, 0)
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
	if org, err := emp.Enroll(ctx, donorToken, "GAMMA"); err != nil || org != "Gamma Co" {
		t.Fatalf("re-entering the same code: %q %v", org, err)
	}
	approvedPartner(t, pool, "Other Co", "OTHERCO", 0, 0)
	if org, err := emp.Enroll(ctx, donorToken, "OTHERCO"); !errors.Is(err, ErrEmployerLinked) {
		t.Fatalf("a second employer's code linked the donor to %q: %v", org, err)
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
	rows, err := emp.Teams(ctx, adminSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("team under 5 members must be suppressed: %+v", rows)
	}

	// Two full teams rank by combined active giving: five $30/month givers
	// beat five members of whom only one gives $90/month and the rest lapsed.
	fill := func(name string, monthly []int64) {
		for _, amount := range monthly {
			id := linkDonorWithGift(t, pool, partnerID, 3000, time.Now().UTC(), 1)
			if _, err := emp.JoinTeam(ctx, sessionFor(t, pool, id), name); err != nil {
				t.Fatal(err)
			}
			mustExec(t, pool, `UPDATE donor_metric_projections SET active=$2, current_monthly_minor=$3,
				streak_since=now()-interval '40 days' WHERE donor_id=$1`, id, amount > 0, amount)
		}
	}
	fill("Ops", []int64{9000, 0, 0, 0, 0})
	fill("Sales", []int64{3000, 3000, 3000, 3000, 3000})
	rows, err = emp.Teams(ctx, adminSession)
	if err != nil || len(rows) != 2 {
		t.Fatalf("leaderboard: %+v %v", rows, err)
	}
	if rows[0].Name != "Sales" || rows[0].AnnualizedMinor != 180000 || rows[0].ActiveGivers != 5 ||
		rows[1].Name != "Ops" || rows[1].AnnualizedMinor != 108000 || rows[1].ActiveGivers != 1 || rows[0].AvgStreakDays != 40 {
		t.Fatalf("leaderboard order or totals: %+v", rows)
	}
	team, err = emp.Team(ctx, donorToken)
	if err != nil || len(team.Leaderboard) != 2 || team.Leaderboard[0].Name != "Sales" {
		t.Fatalf("donor's company leaderboard: %+v %v", team, err)
	}
}

// approvePartnerAdmin creates a first ADMIN partner user for an approved
// partner, returning (userID, userID) — the id is reused as the session owner.
func approvePartnerAdmin(t *testing.T, pool *pgxpool.Pool, partnerID pgtype.UUID) (pgtype.UUID, string) {
	t.Helper()
	uid := mustNewID(t)
	mustExec(t, pool,
		`INSERT INTO donor_partner_users (id, partner_id, email, name, role, accepted_at, created_at)
		 VALUES ($1,$2,$3,'Admin','ADMIN',now(),now())`,
		uid, partnerID, "admin-"+uid.String()+"@x.test")
	t.Cleanup(func() { cleanExec(t, "DELETE FROM donor_partner_users WHERE id=$1", uid) })
	return uid, uid.String()
}
