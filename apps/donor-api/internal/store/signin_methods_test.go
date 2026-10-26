package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/oidc"
	"dad/donor-api/internal/sms"
)

func TestPhoneSignIn(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	id, _ := donor(t, pool)
	ctx := context.Background()
	phone := "+14155550123"
	if _, err := pool.Exec(ctx, "UPDATE donors SET phone=$2 WHERE id=$1", id, phone); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM donor_phone_codes WHERE phone=$1", phone) })

	sender := &sms.StubSender{}
	svc := PhoneService{DB: api, Sender: sender}

	// Unconfigured service refuses.
	if err := (PhoneService{DB: api}).BeginSignIn(ctx, phone); !errors.Is(err, ErrSignInUnavailable) {
		t.Fatalf("unconfigured BeginSignIn = %v; want ErrSignInUnavailable", err)
	}

	// A code is sent for a known phone.
	if err := svc.BeginSignIn(ctx, phone); err != nil {
		t.Fatal(err)
	}
	msg, ok := sender.Last(phone)
	if !ok {
		t.Fatal("no code sent")
	}
	code := extractCode(t, msg)

	// A wrong code is rejected and counts an attempt.
	if _, err := svc.FinishSignIn(ctx, phone, "000000", Client{}); !errors.Is(err, ErrSignInCode) {
		t.Fatalf("wrong code = %v; want ErrSignInCode", err)
	}
	var attempts int
	if err := pool.QueryRow(ctx, "SELECT attempts FROM donor_phone_codes WHERE phone=$1", phone).Scan(&attempts); err != nil ||
		attempts != 1 {
		t.Fatalf("attempts = %d, err %v; want 1", attempts, err)
	}

	// The right code signs in and consumes the code (single use).
	tokens, err := svc.FinishSignIn(ctx, phone, code, Client{})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Access == "" {
		t.Fatal("no session issued")
	}
	var remaining int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_phone_codes WHERE phone=$1", phone).Scan(&remaining); err != nil ||
		remaining != 0 {
		t.Fatalf("code not consumed: count=%d err=%v", remaining, err)
	}

	// An unknown phone sends nothing but reports success (no enumeration).
	if err := svc.BeginSignIn(ctx, "+19998887777"); err != nil {
		t.Fatal(err)
	}
	if _, ok := sender.Last("+19998887777"); ok {
		t.Fatal("code sent to a phone with no account")
	}
}

func TestOIDCSignInLinksAndAuthenticates(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	id, email := donor(t, pool)
	ctx := context.Background()
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM donor_oidc_identities WHERE donor_id=$1", id) })

	settings := &oidc.Settings{Mode: "stub", StubKey: "secret", ClientIDs: map[oidc.Provider]string{
		oidc.Apple: "com.dad.app", oidc.Google: "g.apps",
	}}
	svc := OIDCService{DB: api, Verifier: oidc.NewVerifier(settings), Settings: settings}

	// Unconfigured provider refuses.
	empty := OIDCService{DB: api, Verifier: nil, Settings: &oidc.Settings{}}
	if _, err := empty.SignIn(ctx, oidc.Google, "x", Client{}); !errors.Is(err, ErrSignInUnavailable) {
		t.Fatalf("unconfigured = %v; want ErrSignInUnavailable", err)
	}

	// First sign-in: no prior link, matched by verified email -> links + signs in.
	token := oidc.SignStubToken("secret", oidc.Google, "goog-sub-1", "g.apps", email, time.Now().Add(time.Hour))
	tokens, err := svc.SignIn(ctx, oidc.Google, token, Client{})
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Access == "" {
		t.Fatal("no session issued on first sign-in")
	}
	var linked int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_oidc_identities WHERE donor_id=$1 AND provider='GOOGLE' AND subject='goog-sub-1'", id).Scan(&linked); err != nil ||
		linked != 1 {
		t.Fatalf("identity not linked: count=%d err=%v", linked, err)
	}

	// Second sign-in: resolved by the existing link (email no longer needed).
	token2 := oidc.SignStubToken("secret", oidc.Google, "goog-sub-1", "g.apps", "", time.Now().Add(time.Hour))
	if _, err := svc.SignIn(ctx, oidc.Google, token2, Client{}); err != nil {
		t.Fatalf("second sign-in via link failed: %v", err)
	}

	// A verified identity with no matching account is refused (no account creation).
	orphan := oidc.SignStubToken("secret", oidc.Apple, "apple-sub-x", "com.dad.app", "nobody@example.org", time.Now().Add(time.Hour))
	if _, err := svc.SignIn(ctx, oidc.Apple, orphan, Client{}); !errors.Is(err, ErrSignInCode) {
		t.Fatalf("orphan identity = %v; want ErrSignInCode", err)
	}

	// An unverifiable token is refused.
	if _, err := svc.SignIn(ctx, oidc.Google, "bad.token.sig", Client{}); !errors.Is(err, ErrSignInCode) {
		t.Fatalf("bad token = %v; want ErrSignInCode", err)
	}
}

// extractCode pulls the 6-digit code out of the stub SMS message.
func extractCode(t *testing.T, msg string) string {
	t.Helper()
	var code string
	for _, f := range []byte(msg) {
		if f >= '0' && f <= '9' {
			code += string(f)
		}
	}
	// The message contains only the code's digits and the "10 minutes" literal;
	// take the first run of 6 digits.
	for i := 0; i+6 <= len(code); i++ {
		return code[i : i+6]
	}
	t.Fatalf("no code in %q", msg)
	return ""
}

type unreachableProvider struct{}

func (unreachableProvider) Verify(context.Context, oidc.Provider, string) (oidc.Identity, error) {
	return oidc.Identity{}, oidc.ErrKeys
}

// TestOIDCProviderOutageIsNotABadToken keeps a provider outage from telling the
// donor their sign-in was wrong.
func TestOIDCProviderOutageIsNotABadToken(t *testing.T) {
	settings := &oidc.Settings{Mode: "live", ClientIDs: map[oidc.Provider]string{oidc.Google: "g.apps"}}
	svc := OIDCService{DB: as(t, dbroles.API), Verifier: unreachableProvider{}, Settings: settings}
	_, err := svc.SignIn(context.Background(), oidc.Google, "token", Client{})
	if !errors.Is(err, oidc.ErrKeys) || errors.Is(err, ErrSignInCode) {
		t.Fatalf("SignIn during a provider outage = %v; want ErrKeys", err)
	}
}
