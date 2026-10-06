package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"impactgraph/donor-api/internal/httpapi"
	"impactgraph/donor-api/internal/store"
)

// TestAppSessionLifecycle runs the donor app's sign-in against real session
// storage: a claimed bearer session reads and writes until it is signed out,
// revoked everywhere, or expires.
func TestAppSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("TEST_DATABASE_URL")
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || dsn == "" || (config.ConnConfig.Database != "donor_go" && !strings.HasPrefix(config.ConnConfig.Database, "donor_go_")) {
		t.Fatal("TEST_DATABASE_URL must name a migrated donor_go test database")
	}
	pool, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	id, err := store.NewID()
	if err != nil {
		t.Fatal(err)
	}
	email := "app-session-" + id.String() + "@example.org"
	if _, err = pool.Exec(ctx, "INSERT INTO donors(id,email) VALUES($1,$2)", id, email); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"donor_preference_events", "donor_preferences", "donor_metric_projections", "donor_sessions", "donor_login_links"} {
			if _, err := pool.Exec(ctx, "DELETE FROM "+table+" WHERE donor_id=$1", id); err != nil {
				t.Error(err)
			}
		}
		if _, err := pool.Exec(ctx, "DELETE FROM donors WHERE id=$1", id); err != nil {
			t.Error(err)
		}
	})

	const origin = "https://donor.example.org"
	server := httpapi.NewServer("", httpapi.Services{Origin: origin, Account: store.AccountService{DB: pool}, Donor: store.DonorService{DB: pool, Origin: origin}})
	call := func(method, path, body, session string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-DAD-Client", "native")
		if session != "" {
			r.Header.Set("Authorization", "Bearer "+session)
		}
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, r)
		return response
	}
	signIn := func() string {
		t.Helper()
		linkID, err := store.NewID()
		if err != nil {
			t.Fatal(err)
		}
		proof := strings.Repeat("p", 11) + linkID.String()
		if _, err = pool.Exec(ctx, "INSERT INTO donor_login_links(id,donor_id,token_hash,expires_at) VALUES($1,$2,$3,$4)", linkID, id, store.HashToken(proof), time.Now().Add(15*time.Minute)); err != nil {
			t.Fatal(err)
		}
		response := call("POST", "/donor/claim", `{"token":"`+proof+`"}`, "")
		var body struct {
			SessionToken string `json:"session_token"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK || body.SessionToken == "" || len(response.Result().Cookies()) != 0 {
			t.Fatalf("app claim: got %d %s, want 200 with a session token and no cookie", response.Code, response.Body)
		}
		return body.SessionToken
	}
	expect := func(step string, response *httptest.ResponseRecorder, status int) {
		t.Helper()
		if response.Code != status {
			t.Fatalf("%s: got %d %s, want %d", step, response.Code, response.Body, status)
		}
	}

	session := signIn()
	profile := call("GET", "/donor/me/profile", "", session)
	expect("read with bearer", profile, http.StatusOK)
	if !strings.Contains(profile.Body.String(), email) {
		t.Fatalf("profile is not the claimed donor's: %s", profile.Body)
	}
	expect("write with bearer", call("POST", "/donor/me/preferences", `{"version":0,"referral_emails":true,"marketing_emails":false}`, session), http.StatusOK)
	expect("sign out", call("POST", "/donor/logout", "", session), http.StatusOK)
	expect("read after sign-out", call("GET", "/donor/me/profile", "", session), http.StatusUnauthorized)

	session, other := signIn(), signIn()
	expect("sign out everywhere", call("POST", "/donor/logout-all", "", session), http.StatusOK)
	expect("other session after sign-out everywhere", call("GET", "/donor/me/profile", "", other), http.StatusUnauthorized)

	session = signIn()
	if _, err = pool.Exec(ctx, "UPDATE donor_sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1", store.HashToken(session)); err != nil {
		t.Fatal(err)
	}
	expect("read with expired session", call("GET", "/donor/me/profile", "", session), http.StatusUnauthorized)
}
