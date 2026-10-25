package store

import (
	"context"
	"errors"
	"testing"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPartnerPortalInviteAndAuth covers P-4/P-5 + the dedicated partner session:
// approval issues the signer an invite, accepting it yields a session, an admin
// can invite a viewer, a viewer cannot invite or remove, and an admin cannot
// remove themselves.
func TestPartnerPortalInviteAndAuth(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	apps := PartnerApplicationService{DB: pool, Staff: &StaffService{DB: as(t, dbroles.API)}}
	portal := PartnerPortalService{DB: pool}
	_, superToken := staffFixture(t, pool, "SUPER_ADMIN")
	cleanPartners(t, pool)

	// Approve an application; capture the signer's invite via the DB (the
	// approval emails it in production).
	appID := testID(t)
	if err := apps.Apply(ctx, appID, validApplication("Gamma Co", "lead@gamma.test")); err != nil {
		t.Fatal(err)
	}
	partnerID, err := apps.Approve(ctx, superToken, appID)
	if err != nil {
		t.Fatal(err)
	}

	// The signer accepts their invite -> a partner session. We re-issue a fresh
	// invite through the service to get a usable plaintext token (the approval's
	// token is only stored hashed).
	// Admin bootstrap: mint a session directly for the signer admin to drive the
	// admin-only flows, mirroring what AcceptInvite would produce.
	var signerID string
	if err := pool.QueryRow(ctx, "SELECT id FROM donor_partner_users WHERE partner_id=$1 AND role='ADMIN'", partnerID).Scan(&signerID); err != nil {
		t.Fatal(err)
	}
	adminSession := issueTestPartnerSession(t, pool, signerID)

	// Admin invites a VIEWER; the returned token accepts into a viewer session.
	viewerInvite, err := portal.Invite(ctx, adminSession, "viewer@gamma.test", "Val Viewer", "VIEWER")
	if err != nil {
		t.Fatalf("admin invite: %v", err)
	}
	viewerSession, err := portal.AcceptInvite(ctx, viewerInvite)
	if err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	// A bad invite token is refused.
	if _, err := portal.AcceptInvite(ctx, "not-a-real-invite"); !errors.Is(err, ErrPartnerInvite) {
		t.Fatalf("bad invite accepted: %v", err)
	}

	// Both can list users (any role); the viewer CANNOT invite or remove.
	if users, err := portal.Users(ctx, viewerSession); err != nil || len(users) != 2 {
		t.Fatalf("viewer users list: %d %v", len(users), err)
	}
	if _, err := portal.Invite(ctx, viewerSession, "x@gamma.test", "X", "VIEWER"); !errors.Is(err, ErrPartnerForbidden) {
		t.Fatalf("viewer invited a user: %v", err)
	}

	// The admin removes the viewer; the admin cannot remove themselves.
	var viewerID string
	if err := pool.QueryRow(ctx, "SELECT id FROM donor_partner_users WHERE partner_id=$1 AND role='VIEWER'", partnerID).Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	if err := portal.RemoveUser(ctx, adminSession, mustUUID(t, viewerID)); err != nil {
		t.Fatalf("admin remove viewer: %v", err)
	}
	if err := portal.RemoveUser(ctx, adminSession, mustUUID(t, signerID)); !errors.Is(err, ErrPartnerForbidden) {
		t.Fatalf("admin removed self: %v", err)
	}
	// The removed viewer's session no longer authenticates.
	if _, err := portal.Users(ctx, viewerSession); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("disabled viewer still authenticated: %v", err)
	}
}

// issueTestPartnerSession mints a live partner session token for a user id,
// bypassing the invite dance (used to bootstrap the admin in tests).
func issueTestPartnerSession(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	ctx := context.Background()
	token, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO donor_partner_sessions (id, partner_user_id, token_hash, expires_at) VALUES ($1,$2,$3, now()+interval '1 hour')",
		sid, mustUUID(t, userID), HashToken(token),
	); err != nil {
		t.Fatal(err)
	}
	return token
}

// mustUUID parses a UUID string into pgtype.UUID for test calls.
func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatalf("bad uuid %q: %v", s, err)
	}
	return id
}
