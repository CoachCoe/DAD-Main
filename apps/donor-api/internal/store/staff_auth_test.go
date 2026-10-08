package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

const (
	staffTestOrigin = "https://staff.example.org"
	staffTestRPID   = "staff.example.org"
)

func staffService(t *testing.T, pool *pgxpool.Pool) *StaffService {
	t.Helper()
	s, err := NewStaffService(pool, staffTestOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// invitedStaff creates an account with an open invitation, removing every
// record its passkey journey leaves behind.
func invitedStaff(t *testing.T, pool *pgxpool.Pool, role string) (pgtype.UUID, string) {
	t.Helper()
	id, _ := staffFixture(t, pool, role)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			"DELETE FROM donor_staff_ceremonies WHERE invite_id IN (SELECT id FROM donor_staff_invites WHERE staff_id=$1)",
			"DELETE FROM donor_staff_invites WHERE staff_id=$1",
			"DELETE FROM donor_staff_passkeys WHERE staff_id=$1",
			"DELETE FROM donor_staff_audit WHERE target=$1::text",
		} {
			if _, err := pool.Exec(ctx, sql, id); err != nil {
				t.Error(err)
			}
		}
	})
	invite, err := createStaffInvite(context.Background(), queries.New(pool), id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return id, invite
}

// activatedStaff enrols a software passkey for a new account.
func activatedStaff(t *testing.T, pool *pgxpool.Pool, s *StaffService, role string) (pgtype.UUID, *authenticator) {
	t.Helper()
	ctx := context.Background()
	id, invite := invitedStaff(t, pool, role)
	key := newAuthenticator(t, staffTestOrigin, staffTestRPID)
	invitation, err := s.BeginActivation(ctx, invite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Activate(ctx, invitation.ID, key.register(t, invitation.Options)); err != nil {
		t.Fatal(err)
	}
	return id, key
}

func signIn(t *testing.T, s *StaffService, key *authenticator) (string, error) {
	t.Helper()
	ceremony, err := s.BeginSignIn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s.SignIn(context.Background(), ceremony.ID, key.assert(t, ceremony.Options))
}

func auditCount(t *testing.T, pool *pgxpool.Pool, id pgtype.UUID, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM donor_staff_audit WHERE target=$1 AND action=$2", id.String(), action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStaffPasskeyActivationSignsInOnceAndSignInIsSingleUse(t *testing.T) {
	pool := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := staffService(t, pool)
	id, invite := invitedStaff(t, pool, "SUPPORT")
	invitation, err := s.BeginActivation(ctx, invite)
	if err != nil {
		t.Fatal(err)
	}
	creation := invitation.Options.(protocol.PublicKeyCredentialCreationOptions)
	selection := creation.AuthenticatorSelection
	if invitation.Email != id.String()+"@example.org" || invitation.Name != "Test staff" || creation.RelyingParty.ID != staffTestRPID ||
		selection.ResidentKey != protocol.ResidentKeyRequirementRequired || selection.UserVerification != protocol.VerificationRequired ||
		string(creation.User.ID.(protocol.URLEncodedBase64)) != string(id.Bytes[:]) {
		t.Fatalf("registration does not require a discoverable, verified passkey for this account: %+v", invitation)
	}
	key := newAuthenticator(t, staffTestOrigin, staffTestRPID)
	attestation := key.register(t, invitation.Options)
	session, err := s.Activate(ctx, invitation.ID, attestation)
	if err != nil {
		t.Fatal(err)
	}
	if me, err := s.Me(ctx, session); err != nil || !me.Active || me.ID != id.String() {
		t.Fatal("activation did not sign the staff member in", me, err)
	}
	if _, err = s.Activate(ctx, invitation.ID, attestation); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("activation ceremony replay accepted", err)
	}
	if _, err = s.BeginActivation(ctx, invite); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("consumed invitation reused", err)
	}

	ceremony, err := s.BeginSignIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := ceremony.Options.(protocol.PublicKeyCredentialRequestOptions)
	if request.RelyingPartyID != staffTestRPID || request.UserVerification != protocol.VerificationRequired || len(request.AllowedCredentials) != 0 {
		t.Fatalf("sign-in is not a usernameless, verified request: %+v", request)
	}
	assertion := key.assert(t, ceremony.Options)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	tokens := make(chan string, 2)
	for range 2 {
		wg.Go(func() {
			token, err := s.SignIn(ctx, ceremony.ID, assertion)
			if err == nil {
				tokens <- token
			}
			results <- err
		})
	}
	wg.Wait()
	close(results)
	close(tokens)
	successes, denied := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrStaffDenied):
			denied++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || denied != 1 {
		t.Fatalf("challenge replay: %d successes, %d denied", successes, denied)
	}
	for token := range tokens {
		if _, err = s.Me(ctx, token); err != nil {
			t.Fatal(err)
		}
		if err = s.SignOut(ctx, token); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Me(ctx, token); !errors.Is(err, ErrStaffDenied) {
			t.Fatal("signed-out session accepted", err)
		}
	}
	var count int64
	var used pgtype.Timestamptz
	if err = pool.QueryRow(ctx, "SELECT sign_count,last_used_at FROM donor_staff_passkeys WHERE staff_id=$1", id).Scan(&count, &used); err != nil || count != 1 || !used.Valid {
		t.Fatal("passkey use not recorded", count, used, err)
	}
	if auditCount(t, pool, id, "staff.activated") != 1 || auditCount(t, pool, id, "staff.signed_in") != 2 {
		t.Fatal("activation or sign-in audit missing or duplicated")
	}
}

func TestStaffSignInRejectsForgedOrUnusableAssertions(t *testing.T) {
	pool := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := staffService(t, pool)
	id, key := activatedStaff(t, pool, s, "SUPPORT")
	for range 2 {
		if _, err := signIn(t, s, key); err != nil {
			t.Fatal(err)
		}
	}
	failures := auditCount(t, pool, id, "staff.sign_in_failed")
	for _, tc := range []struct {
		name   string
		forge  func(*authenticator)
		repair func()
	}{
		{"wrong origin", func(a *authenticator) { a.origin = "https://donors.example.org" }, nil},
		{"wrong relying party", func(a *authenticator) { a.rpID = "donors.example.org" }, nil},
		{"user not verified", func(a *authenticator) { a.flags &^= flagUV }, nil},
		{"counter regression", func(a *authenticator) { a.count -= 2 }, nil},
		{"disabled account", func(*authenticator) { mustExec(t, pool, "UPDATE donor_staff SET disabled=true WHERE id=$1", id) },
			func() { mustExec(t, pool, "UPDATE donor_staff SET disabled=false WHERE id=$1", id) }},
	} {
		forged := *key
		tc.forge(&forged)
		if _, err := signIn(t, s, &forged); !errors.Is(err, ErrStaffDenied) {
			t.Fatalf("%s: got %v, want ErrStaffDenied", tc.name, err)
		}
		if tc.repair != nil {
			tc.repair()
		}
		failures++
		if got := auditCount(t, pool, id, "staff.sign_in_failed"); got != failures {
			t.Fatalf("%s: %d failed sign-ins audited, want %d", tc.name, got, failures)
		}
	}
	if _, err := signIn(t, s, newAuthenticator(t, staffTestOrigin, staffTestRPID)); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("unknown passkey accepted", err)
	}
	ceremony, err := s.BeginSignIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, "UPDATE donor_staff_ceremonies SET expires_at=now() WHERE token_hash=$1", HashToken(ceremony.ID))
	if _, err = s.SignIn(ctx, ceremony.ID, key.assert(t, ceremony.Options)); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("expired challenge accepted", err)
	}
	if _, err = s.SignIn(ctx, ceremony.ID, []byte(`{"id":"x"}`)); !errors.Is(err, ErrStaffInput) {
		t.Fatal("malformed credential not rejected as input", err)
	}
	if _, err = signIn(t, s, key); err != nil {
		t.Fatal("genuine passkey rejected after forged attempts", err)
	}
}

func TestStaffActivationCountsAttemptsAndRejectsForgedAttestations(t *testing.T) {
	pool := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := staffService(t, pool)
	id, invite := invitedStaff(t, pool, "FINANCE")
	for _, forge := range []func(*authenticator){
		func(a *authenticator) { a.origin = "https://donors.example.org" },
		func(a *authenticator) { a.rpID = "example.org" },
		func(a *authenticator) { a.flags &^= flagUV },
	} {
		invitation, err := s.BeginActivation(ctx, invite)
		if err != nil {
			t.Fatal(err)
		}
		key := newAuthenticator(t, staffTestOrigin, staffTestRPID)
		forge(key)
		if _, err = s.Activate(ctx, invitation.ID, key.register(t, invitation.Options)); !errors.Is(err, ErrStaffDenied) {
			t.Fatal("forged attestation accepted", err)
		}
	}
	var passkeys, attempts int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM donor_staff_passkeys WHERE staff_id=$1), (SELECT attempts FROM donor_staff_invites WHERE staff_id=$1)", id).Scan(&passkeys, &attempts); err != nil || passkeys != 0 || attempts != 3 {
		t.Fatal("rejected attestations stored a passkey or went uncounted", passkeys, attempts, err)
	}
	mustExec(t, pool, "UPDATE donor_staff_invites SET attempts=5 WHERE staff_id=$1", id)
	if _, err := s.BeginActivation(ctx, invite); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("exhausted invitation accepted", err)
	}
	mustExec(t, pool, "UPDATE donor_staff_invites SET attempts=0, expires_at=now() WHERE staff_id=$1", id)
	if _, err := s.BeginActivation(ctx, invite); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("expired invitation accepted", err)
	}
}
