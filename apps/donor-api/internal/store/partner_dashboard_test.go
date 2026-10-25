package store

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedPartnerInitiative creates a PUBLISHED initiative linked to partnerID with
// a committed amount, one spend category, and one verified PEOPLE metric.
func seedPartnerInitiative(t *testing.T, pool *pgxpool.Pool, partnerID pgtype.UUID, slug string, committed, spend, people int64) {
	t.Helper()
	ctx := context.Background()
	initID, _ := NewID()
	mustExec(
		t,
		pool,
		`INSERT INTO donor_initiatives (id, slug, title, summary, type, location, region, started_on, committed_minor, partner_signup_id, publish_state, published_at)
		 VALUES ($1,$2,$3,'Summary','PROGRAM','Reno, NV','Northern Nevada', current_date, $4, $5, 'PUBLISHED', now())`,
		initID,
		slug,
		"Initiative "+slug,
		committed,
		partnerID,
	)
	spendID, _ := NewID()
	mustExec(
		t,
		pool,
		`INSERT INTO donor_initiative_spend (id, initiative_id, category, amount_minor) VALUES ($1,$2,'Meals',$3)`,
		spendID,
		initID,
		spend,
	)
	metricID, _ := NewID()
	mustExec(
		t,
		pool,
		`INSERT INTO donor_initiative_metrics (id, initiative_id, position, label, value, unit, verification_note, verified_on, publish_state)
		 VALUES ($1,$2,0,'People served',$3,'PEOPLE','audited', current_date, 'PUBLISHED')`,
		metricID,
		initID,
		people,
	)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM donor_initiatives WHERE id=$1", initID)
	})
}

func TestPartnerDashboardAndReport(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	apps := PartnerApplicationService{DB: pool, Staff: &StaffService{DB: as(t, dbroles.API)}}
	dash := PartnerDashboardService{Portal: PartnerPortalService{DB: pool}}
	_, superToken := staffFixture(t, pool, "SUPER_ADMIN")
	cleanPartners(t, pool)

	appID := testID(t)
	if err := apps.Apply(ctx, appID, validApplication("Delta Inc", "cfo@delta.test")); err != nil {
		t.Fatal(err)
	}
	partnerID, err := apps.Approve(ctx, superToken, appID)
	if err != nil {
		t.Fatal(err)
	}
	var adminID string
	if err := pool.QueryRow(ctx, "SELECT id FROM donor_partner_users WHERE partner_id=$1 AND role='ADMIN'", partnerID).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminSession := issueTestPartnerSession(t, pool, adminID)

	// Two published initiatives: $1,000 committed / $400 deployed / 150 people,
	// and $500 committed / $100 deployed / 50 people. Tiles sum them.
	seedPartnerInitiative(t, pool, partnerID, "delta-a", 100000, 40000, 150)
	seedPartnerInitiative(t, pool, partnerID, "delta-b", 50000, 10000, 50)

	ov, err := dash.Overview(ctx, adminSession)
	if err != nil {
		t.Fatal(err)
	}
	if ov.CommittedMinor != 150000 || ov.DeployedMinor != 50000 || ov.PeopleReached != 200 {
		t.Fatalf("tiles wrong: %+v", ov)
	}
	if ov.DeployedPercent != 33 { // 50000/150000
		t.Fatalf("deployed %% wrong: %d", ov.DeployedPercent)
	}
	if !ov.OurPeopleAvailable {
		t.Fatal("Our People tile must be available from Phase 6")
	}

	inits, err := dash.Initiatives(ctx, adminSession)
	if err != nil || len(inits) != 2 {
		t.Fatalf("initiatives: %d %v", len(inits), err)
	}
	spend, err := dash.Spend(ctx, adminSession)
	if err != nil || len(spend) != 1 || spend[0].AmountMinor != 50000 {
		t.Fatalf("spend: %+v %v", spend, err)
	}

	// Export: a valid PDF, recorded in history. Admin-only.
	pdf, err := dash.ExportReport(ctx, adminSession)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 500 {
		t.Fatalf("not a PDF: %d bytes", len(pdf))
	}
	exports, err := dash.Exports(ctx, adminSession)
	if err != nil || len(exports) != 1 || exports[0].CommittedMinor != 150000 {
		t.Fatalf("export history: %+v %v", exports, err)
	}

	// A VIEWER can read tiles but cannot export (admin-only).
	viewerInvite, err := dash.Portal.Invite(ctx, adminSession, "analyst@delta.test", "Ana", "VIEWER")
	if err != nil {
		t.Fatal(err)
	}
	viewerSession, err := dash.Portal.AcceptInvite(ctx, viewerInvite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dash.Overview(ctx, viewerSession); err != nil {
		t.Fatalf("viewer overview: %v", err)
	}
	if _, err := dash.ExportReport(ctx, viewerSession); !errors.Is(err, ErrPartnerForbidden) {
		t.Fatalf("viewer exported a report: %v", err)
	}
}
