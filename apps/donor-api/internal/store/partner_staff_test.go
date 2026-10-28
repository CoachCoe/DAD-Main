package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5"
)

func TestStaffPartnerListDetailAndUsers(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	svc := PartnerApplicationService{DB: pool, Staff: &StaffService{DB: as(t, dbroles.API)}}
	_, superToken := staffFixture(t, pool, "SUPER_ADMIN")
	_, growthToken := staffFixture(t, pool, "OP_GROWTH")
	cleanInitiatives(t, pool)
	cleanPartners(t, pool)

	appID := testID(t)
	if err := svc.Apply(ctx, appID, validApplication("Lambda Co", "lee@lambda.test")); err != nil {
		t.Fatal(err)
	}
	partnerID, err := svc.Approve(ctx, superToken, appID)
	if err != nil {
		t.Fatal(err)
	}
	seedPartnerInitiative(t, pool, partnerID, "lambda-meals", 50_000_00, 10_000_00, 40)
	inquiry := testID(t)
	mustExec(t, pool, `INSERT INTO donor_partner_signups (id, organization, name, email, website, message, request_hash, consent_version)
		VALUES ($1, 'Inquiry Only', 'N', 'n@inquiry.test', '', 'hi', 'h', 'r1-partner-contact-v1')`, inquiry)
	t.Cleanup(func() { cleanExec(t, "DELETE FROM donor_partner_signups WHERE id=$1", inquiry) })

	if _, err := svc.Partners(ctx, growthToken, 0); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("OP_GROWTH listed partners: %v", err)
	}
	page, err := svc.Partners(ctx, superToken, 0)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(page.Partners, func(p StaffPartner) bool { return p.ID == partnerID.String() })
	if i < 0 || page.Partners[i].UserCount != 1 || page.Partners[i].InitiativeCount != 1 || page.Partners[i].CompanyCode == "" {
		t.Fatalf("approved partner missing or miscounted: %+v", page.Partners)
	}
	if slices.ContainsFunc(page.Partners, func(p StaffPartner) bool { return p.ID == inquiry.String() }) {
		t.Fatal("a contact-form inquiry is listed as a partner")
	}
	if _, err := svc.Partner(ctx, superToken, inquiry); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("inquiry detail: got %v, want not found", err)
	}

	if err := svc.InviteUser(ctx, superToken, partnerID, "viewer@lambda.test", "Vi", "OWNER"); !errors.Is(err, ErrPartnerApplication) {
		t.Fatalf("unknown role accepted: %v", err)
	}
	if err := svc.InviteUser(ctx, superToken, inquiry, "viewer@lambda.test", "Vi", "VIEWER"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("invited a user to an unapproved org: %v", err)
	}
	if err := svc.InviteUser(ctx, superToken, partnerID, "Viewer@Lambda.test", "Vi", "VIEWER"); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.Partner(ctx, superToken, partnerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Initiatives) != 1 || detail.Initiatives[0].CommittedMinor != 50_000_00 {
		t.Fatalf("linked initiatives: %+v", detail.Initiatives)
	}
	viewer := slices.IndexFunc(detail.Users, func(u StaffPartnerUser) bool { return u.Email == "viewer@lambda.test" })
	if len(detail.Users) != 2 || viewer < 0 || detail.Users[viewer].Role != "VIEWER" || detail.Users[viewer].Accepted {
		t.Fatalf("users after invite: %+v", detail.Users)
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM donor_partner_login_emails e JOIN donor_partner_users u ON u.id = e.partner_user_id
		WHERE u.email='viewer@lambda.test'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("invitation emails queued = %d (%v), want 1", queued, err)
	}

	userID := mustUUID(t, detail.Users[viewer].ID)
	mustExec(t, pool, `INSERT INTO donor_partner_sessions (id, partner_user_id, token_hash, expires_at, created_at)
		VALUES ($1, $2, 'staff-removal', now() + interval '1 hour', now())`, mustNewID(t), userID)
	if err := svc.RemoveUser(ctx, superToken, mustNewID(t), userID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("removed a user through another partner: %v", err)
	}
	if err := svc.RemoveUser(ctx, superToken, partnerID, userID); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_partner_sessions WHERE partner_user_id=$1 AND revoked_at IS NULL", userID).
		Scan(&live); err != nil || live != 0 {
		t.Fatalf("removed user keeps %d live sessions (%v)", live, err)
	}
	var audited int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_staff_audit WHERE target=$1 AND action IN ('partner.user_invited','partner.user_removed')",
		partnerID.String()).Scan(&audited); err != nil ||
		audited != 2 {
		t.Fatalf("audit rows = %d (%v), want 2", audited, err)
	}
}
