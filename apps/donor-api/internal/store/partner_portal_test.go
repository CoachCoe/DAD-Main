package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/mail"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// deliverPartnerLinks runs the login-email worker until its queue is empty and
// returns the link token each recipient received last.
func deliverPartnerLinks(t *testing.T, now time.Time) map[string]string {
	t.Helper()
	transport := &recordingMail{}
	w := PartnerLoginEmailWorker{
		DB:        as(t, dbroles.LoginEmailWorker),
		Settings:  mail.Settings{Host: "smtp.fixture", Port: 587, Sender: "sender@example.org", EncryptionKeys: emailTestKeys},
		Origin:    "https://donor.example.org",
		Transport: transport,
	}
	for {
		worked, err := w.DeliverNext(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	links := map[string]string{}
	for _, m := range transport.messages {
		if !strings.Contains(m.Body, "https://donor.example.org/partner/accept#token=") {
			t.Fatalf("%s: email has no portal link:\n%s", m.To, m.Body)
		}
		links[m.To] = tokenFromMail(t, m)
	}
	return links
}

// TestPartnerPortalSignInJourney walks P-2..P-5 as a partner lives them: the
// approval emails the signer a link that opens an admin session, the admin
// invites a viewer by email, each link works once, and a user whose session
// has ended requests a fresh link with nothing but their email.
func TestPartnerPortalSignInJourney(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	apps := PartnerApplicationService{DB: pool, Staff: &StaffService{DB: as(t, dbroles.API)}}
	portal := PartnerPortalService{DB: as(t, dbroles.API)}
	_, superToken := staffFixture(t, pool, "SUPER_ADMIN")
	cleanPartners(t, pool)
	mustExec(t, pool, "DELETE FROM donor_partner_login_emails")

	appID := testID(t)
	if err := apps.Apply(ctx, appID, validApplication("Gamma Co", "lead@gamma.test")); err != nil {
		t.Fatal(err)
	}
	partnerID, err := apps.Approve(ctx, superToken, appID)
	if err != nil {
		t.Fatal(err)
	}
	var code string
	if err := pool.QueryRow(ctx, "SELECT company_code FROM donor_partner_signups WHERE id=$1", partnerID).Scan(&code); err != nil ||
		!strings.HasPrefix(code, "GAMMAC-") || !companyCode.MatchString(code) {
		t.Fatalf("approved partner has company code %q (%v), want GAMMAC-…", code, err)
	}

	links := deliverPartnerLinks(t, time.Now().UTC())
	adminSession, err := portal.AcceptInvite(ctx, links["lead@gamma.test"])
	if err != nil {
		t.Fatalf("approval link did not sign the signer in: %v", err)
	}
	if _, err := portal.AcceptInvite(ctx, links["lead@gamma.test"]); !errors.Is(err, ErrPartnerInvite) {
		t.Fatalf("approval link worked twice: %v", err)
	}

	if err := portal.Invite(ctx, adminSession, "viewer@gamma.test", "Val Viewer", "VIEWER"); err != nil {
		t.Fatalf("admin invite: %v", err)
	}
	viewerSession, err := portal.AcceptInvite(ctx, deliverPartnerLinks(t, time.Now().UTC())["viewer@gamma.test"])
	if err != nil {
		t.Fatalf("invitation link: %v", err)
	}
	if _, err := portal.AcceptInvite(ctx, "not-a-real-invite-token-0123456789abcdef"); !errors.Is(err, ErrPartnerInvite) {
		t.Fatalf("bad invite accepted: %v", err)
	}

	if users, err := portal.Users(ctx, viewerSession); err != nil || len(users) != 2 {
		t.Fatalf("viewer users list: %d %v", len(users), err)
	}
	if err := portal.Invite(ctx, viewerSession, "x@gamma.test", "X", "VIEWER"); !errors.Is(err, ErrPartnerForbidden) {
		t.Fatalf("viewer invited a user: %v", err)
	}

	if err := portal.SignOut(ctx, adminSession); err != nil {
		t.Fatal(err)
	}
	if _, err := portal.Users(ctx, adminSession); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("signed-out session still authenticated: %v", err)
	}
	if err := portal.RequestSignIn(ctx, " Lead@Gamma.test "); err != nil {
		t.Fatal(err)
	}
	if err := portal.RequestSignIn(ctx, "lead@gamma.test"); err != nil {
		t.Fatal(err)
	}
	if err := portal.RequestSignIn(ctx, "nobody@gamma.test"); err != nil {
		t.Fatalf("unknown email must not be distinguishable: %v", err)
	}
	signIn := deliverPartnerLinks(t, time.Now().UTC())
	if len(signIn) != 1 {
		t.Fatalf("sign-in requests sent %v, want one throttled link for the admin only", signIn)
	}
	if adminSession, err = portal.AcceptInvite(ctx, signIn["lead@gamma.test"]); err != nil {
		t.Fatalf("requested sign-in link: %v", err)
	}
	if _, err := portal.Users(ctx, adminSession); err != nil {
		t.Fatalf("new admin session: %v", err)
	}

	var viewerID, signerID string
	if err := pool.QueryRow(ctx, "SELECT id FROM donor_partner_users WHERE partner_id=$1 AND role='VIEWER'", partnerID).Scan(&viewerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT id FROM donor_partner_users WHERE partner_id=$1 AND role='ADMIN'", partnerID).Scan(&signerID); err != nil {
		t.Fatal(err)
	}
	if err := portal.RemoveUser(ctx, adminSession, mustUUID(t, signerID)); !errors.Is(err, ErrPartnerForbidden) {
		t.Fatalf("admin removed self: %v", err)
	}
	if err := portal.RemoveUser(ctx, adminSession, mustUUID(t, viewerID)); err != nil {
		t.Fatalf("admin remove viewer: %v", err)
	}
	if _, err := portal.Users(ctx, viewerSession); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("removed viewer still authenticated: %v", err)
	}
	if err := portal.RequestSignIn(ctx, "viewer@gamma.test"); err != nil {
		t.Fatal(err)
	}
	if sent := deliverPartnerLinks(t, time.Now().UTC()); len(sent) != 0 {
		t.Fatalf("a removed user was sent a sign-in link: %v", sent)
	}
}

// A user holds one live link: a link replaced by a newer one before its email
// was sent is dropped instead of mailed.
func TestPartnerLinkReplacedBeforeDeliveryIsNotSent(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	apps := PartnerApplicationService{DB: pool, Staff: &StaffService{DB: as(t, dbroles.API)}}
	_, superToken := staffFixture(t, pool, "SUPER_ADMIN")
	cleanPartners(t, pool)
	mustExec(t, pool, "DELETE FROM donor_partner_login_emails")

	appID := testID(t)
	if err := apps.Apply(ctx, appID, validApplication("Kappa Org", "kay@kappa.test")); err != nil {
		t.Fatal(err)
	}
	if _, err := apps.Approve(ctx, superToken, appID); err != nil {
		t.Fatal(err)
	}
	transport := &recordingMail{failure: errors.New("connection reset")}
	w := PartnerLoginEmailWorker{
		DB:        as(t, dbroles.LoginEmailWorker),
		Settings:  mail.Settings{Host: "smtp.fixture", Port: 587, Sender: "sender@example.org", EncryptionKeys: emailTestKeys},
		Origin:    "https://donor.example.org",
		Transport: transport,
	}
	now := time.Now().UTC()
	if worked, err := w.DeliverNext(ctx, now); err != nil || !worked {
		t.Fatal(worked, err)
	}
	mustExec(t, pool, "UPDATE donor_partner_users SET invite_token_hash='replaced' WHERE email='kay@kappa.test'")
	transport.failure, transport.messages = nil, nil
	if worked, err := w.DeliverNext(ctx, now.Add(2*time.Minute)); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if len(transport.messages) != 0 {
		t.Fatalf("sent a replaced link: %v", transport.messages)
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
