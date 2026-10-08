package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"impactgraph/donor-api/internal/httpapi"
	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/store"
)

func TestParseRequiresAnExplicitConfirmedDatabase(t *testing.T) {
	const url = "postgresql://seed:seed@127.0.0.1:5432/loadtest_a"
	if o, err := parse([]string{"-database-url", url, "-confirm", "loadtest_a", "-fixture", "/tmp/x.json"}); err != nil || o.donors != 50000 || o.sessions != 2000 {
		t.Fatalf("valid invocation rejected: %v", err)
	}
	for name, args := range map[string][]string{
		"no url":             {"-confirm", "loadtest_a", "-sessions", "0"},
		"no confirmation":    {"-database-url", url, "-sessions", "0"},
		"wrong confirmation": {"-database-url", url, "-confirm", "loadtest", "-sessions", "0"},
		"sessions, no file":  {"-database-url", url, "-confirm", "loadtest_a"},
		"no donors":          {"-database-url", url, "-confirm", "loadtest_a", "-sessions", "0", "-donors", "0"},
		"stray argument":     {"-database-url", url, "-confirm", "loadtest_a", "-sessions", "0", "now"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFixtureMustStayOutOfRepositories(t *testing.T) {
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if inside, err := insideRepository(filepath.Join(repository, "deep", "fixture.json")); err != nil || !inside {
		t.Fatal("a fixture inside a working tree was allowed")
	}
	if inside, err := insideRepository(filepath.Join(t.TempDir(), "fixture.json")); err != nil || inside {
		t.Fatal("a fixture outside any working tree was refused")
	}
	err := run(context.Background(), []string{"-database-url", "postgresql://x@127.0.0.1/loadtest_a", "-confirm", "loadtest_a", "-fixture", filepath.Join(repository, "f.json")}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "outside any git working tree") {
		t.Fatalf("run wrote a fixture into a repository: %v", err)
	}
}

// isolatedDatabase creates a migrated database next to the donor_go test
// database, since seeding refuses one that other tests have written donors to.
func isolatedDatabase(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || (config.ConnConfig.Database != "donor_go" && !strings.HasPrefix(config.ConnConfig.Database, "donor_go_")) {
		t.Fatal("TEST_DATABASE_URL must name the dedicated donor_go test database")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var suffix [8]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "donor_seed_test_" + hex.EncodeToString(suffix[:])
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	config = config.Copy()
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.Apply(ctx, pool, false); err != nil {
		t.Fatal(err)
	}
	location, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	location.Path = "/" + name
	return pool, location.String()
}

// TestSeededDonorsAreServedFromTheirFacts seeds a small population and reads
// every claimed donor's dashboard through the API, comparing the projections
// the worker derived with what the generator meant the facts to say.
func TestSeededDonorsAreServedFromTheirFacts(t *testing.T) {
	pool, location := isolatedDatabase(t)
	ctx := context.Background()
	o := options{donors: 200, seed: 7, workers: 4}
	pop, r, err := seed(ctx, pool, o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows["donors"] != 200 || r.Rows["donor_gifts"] == 0 || r.SuppressedMessages == 0 {
		t.Fatalf("unexpected seed report: %+v", r)
	}
	var due, pending int
	if err = pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM donor_metric_projections WHERE next_refresh_at<=now()), (SELECT count(*) FROM donor_messages WHERE completed_at IS NULL)").Scan(&due, &pending); err != nil || due != 0 || pending != 0 {
		t.Fatalf("seeding left %d projections due and %d messages to send: %v", due, pending, err)
	}

	sessions, err := pop.sessions(ctx, pool, len(pop.donors))
	if err != nil || len(sessions) < 50 {
		t.Fatalf("%d sessions: %v", len(sessions), err)
	}
	const origin = "https://donor.example.org"
	server := httpapi.NewServer("", httpapi.Services{Origin: origin, Dashboard: store.DashboardReader{DB: pool}, Donor: store.DonorService{DB: pool, Origin: origin}})
	get := func(path, token string, native bool, into any) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if native {
			request.Header.Set("X-DAD-Client", "native")
			request.Header.Set("Authorization", "Bearer "+token)
		} else if token != "" {
			request.AddCookie(&http.Cookie{Name: "dad_donor_session", Value: token})
		}
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
				t.Fatal(err)
			}
		}
		return response.Code
	}
	checkedStreaks, referrers := 0, 0
	for i, s := range sessions {
		d := s.donor
		var dashboard store.DonorDashboard
		if status := get("/donor/me/dashboard", s.token, i%2 == 0, &dashboard); status != http.StatusOK || dashboard.State != "ready" {
			t.Fatalf("donor %d dashboard: %d %+v", d.index, status, dashboard)
		}
		if !dashboard.FoundingDonor {
			t.Errorf("donor %d gave before the cutoff but is not a founding donor", d.index)
		}
		link, irl := d.referred["LINK"], d.referred["IRL"]
		if link == nil {
			link, irl = &referralTally{}, &referralTally{}
		}
		if got := dashboard.Referrals; got == nil || got.Link != link.count || got.LinkMinor != link.minor || got.IRL != irl.count || got.IRLMinor != irl.minor {
			t.Errorf("donor %d referrals %+v, want link %+v irl %+v", d.index, got, *link, *irl)
		}
		if d.referrals() > 0 {
			referrers++
		}
		if d.streakKnown {
			checkedStreaks++
			if got := dashboard.Streak; got == nil || got.Current != int32(d.streak) || got.AtRisk != d.atRisk {
				t.Errorf("donor %d streak %+v, want %d at risk %v", d.index, got, d.streak, d.atRisk)
			}
		}
		var gifts store.GiftsPage
		if status := get("/donor/me/gifts?offset=0", s.token, false, &gifts); status != http.StatusOK || len(gifts.Gifts) != min(50, len(d.gifts())) {
			t.Errorf("donor %d gift history: %d with %d gifts, want %d", d.index, status, len(gifts.Gifts), len(d.gifts()))
		}
		if d.code != "" {
			var referrer map[string]string
			if status := get("/donor/referrals/"+d.code, "", false, &referrer); status != http.StatusOK || referrer["public_name"] != d.publicName {
				t.Errorf("referral landing for donor %d: %d %v", d.index, status, referrer)
			}
		}
	}
	if checkedStreaks < len(sessions)*9/10 || referrers == 0 {
		t.Fatalf("too little coverage: %d streaks and %d referrers among %d donors", checkedStreaks, referrers, len(sessions))
	}

	name := pool.Config().ConnConfig.Database
	fixturePath := filepath.Join(t.TempDir(), "fixture.json")
	refused := []string{"-database-url", location, "-confirm", name, "-donors", "10", "-sessions", "1", "-fixture", fixturePath}
	if err = run(ctx, refused, io.Discard); err == nil || !strings.Contains(err.Error(), "-append") {
		t.Fatalf("a second population was seeded without -append: %v", err)
	}
	if _, err = os.Stat(fixturePath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused seed left its fixture behind: %v", err)
	}
	args := []string{"-database-url", location, "-confirm", name, "-donors", "60", "-append", "-sessions", "5", "-workers", "2", "-fixture", fixturePath}
	if err = run(ctx, args, io.Discard); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(fixturePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("fixture must be private: %v %v", info, err)
	}
	var f fixture
	if data, err := os.ReadFile(fixturePath); err != nil || json.Unmarshal(data, &f) != nil || len(f.Sessions) != 5 || len(f.ReferralCodes) != fixtureCodes {
		t.Fatalf("fixture holds %d sessions and %d codes: %v", len(f.Sessions), len(f.ReferralCodes), err)
	}
	if err = run(ctx, args, io.Discard); err == nil {
		t.Fatal("an existing fixture was overwritten")
	}

	if _, err = pool.Exec(ctx, "INSERT INTO donors(id,email) VALUES(gen_random_uuid(),'person@example.org')"); err != nil {
		t.Fatal(err)
	}
	o.appendPopulation = true
	if _, _, err = seed(ctx, pool, o, time.Now()); err == nil || !strings.Contains(err.Error(), "not seeded") {
		t.Fatalf("seeded a database holding a real donor: %v", err)
	}
}
