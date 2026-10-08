package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadCountsOutcomesPerScenario(t *testing.T) {
	var good, bad, cookies atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "dad_donor_session=synthetic" {
			cookies.Add(1)
		}
		switch r.URL.Path {
		case "/ok":
			good.Add(1)
		case "/large":
			bad.Add(1)
			_, _ = w.Write([]byte(strings.Repeat("x", maxBodyBytes+1)))
		default:
			bad.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()

	scenarios, err := urlMix(server.URL + "/ok," + server.URL + "/unavailable," + server.URL + "/large")
	if err != nil {
		t.Fatal(err)
	}
	r := load(context.Background(), config{scenarios: scenarios, duration: 200 * time.Millisecond, concurrency: 4, sessions: []string{"dad_donor_session=synthetic"}})
	if r.Requests == 0 || r.Requests != good.Load()+bad.Load() || cookies.Load() != r.Requests {
		t.Fatalf("reported %d requests; server saw %d, %d with the session cookie", r.Requests, good.Load()+bad.Load(), cookies.Load())
	}
	ok, unavailable, large := r.Scenarios["/ok"], r.Scenarios["/unavailable"], r.Scenarios["/large"]
	if r.Failed != bad.Load() || ok.Failed != 0 || unavailable.Failed != unavailable.Requests || large.Errors["oversized"] != large.Requests {
		t.Fatalf("failures misattributed: %+v", r)
	}
	if ok.Statuses["200"] != ok.Requests || unavailable.Statuses["503"] != unavailable.Requests || ok.Requests != good.Load() {
		t.Fatalf("statuses misreported: %+v %+v", ok, unavailable)
	}
	if ok.Requests == 0 || unavailable.Requests == 0 || large.Requests == 0 {
		t.Fatal("the mix did not reach every scenario")
	}
	if r.P50ms > r.P95ms || r.P95ms > r.P99ms || r.P99ms > r.MaxMs+0.1 {
		t.Fatalf("percentiles out of order: %+v", r.stats)
	}
}

// TestSeededMixActsLikeBrowsersAppsAndVisitors checks each request carries
// exactly one kind of credential and that captures look like distinct visitors.
func TestSeededMixActsLikeBrowsersAppsAndVisitors(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	var visitorAddresses, userAddresses = map[string]bool{}, map[string]bool{}
	var problems []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		cookie, bearer := r.Header.Get("Cookie"), r.Header.Get("Authorization")
		switch path := r.URL.Path; {
		case strings.HasPrefix(path, "/donor/me/"):
			seen["signed in"]++
			if native := r.Header.Get("X-DAD-Client") == "native"; native {
				seen["native"]++
				if cookie != "" || !strings.HasPrefix(bearer, "Bearer token-") {
					problems = append(problems, "app request without a bearer token alone")
				}
			} else if !strings.HasPrefix(cookie, "dad_donor_session=token-") || bearer != "" {
				problems = append(problems, "browser request without its session cookie alone")
			}
			userAddresses[r.Header.Get("X-Real-IP")] = true
			if path == "/donor/me/dashboard" {
				problems = append(problems, "a scenario weighted 0 was requested")
			}
		case r.Method == http.MethodPost:
			seen["capture"]++
			body, _ := io.ReadAll(r.Body)
			if r.Header.Get("Origin") != "https://donor.example.org" || cookie != "" || bearer != "" || !strings.Contains(string(body), `"kind"`) {
				problems = append(problems, "capture without the site's Origin or with credentials")
			}
			visitorAddresses[r.Header.Get("X-Real-IP")] = true
		case strings.HasPrefix(path, "/donor/referrals/"):
			code := strings.TrimPrefix(path, "/donor/referrals/")
			if code == "codeA" || code == "codeB" {
				seen["lookup"]++
			} else {
				seen["missing"]++
				w.WriteHeader(http.StatusNotFound)
				return
			}
		default:
			seen[path]++
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	scenarios, err := seededMix(server.URL, "https://donor.example.org", fixture{ReferralCodes: []string{"codeA", "codeB"}}, map[string]int{"dashboard": 0})
	if err != nil {
		t.Fatal(err)
	}
	r := load(context.Background(), config{scenarios: scenarios, duration: 300 * time.Millisecond, concurrency: 8,
		sessions: []string{"token-1", "token-2", "token-3"}, nativeShare: 0.5, clientIPs: true})
	if len(problems) > 0 {
		t.Fatal(problems[0])
	}
	if r.Failed != 0 || seen["lookup"] == 0 || seen["missing"] == 0 || seen["capture"] < 2 || seen["/donor/public/content"] == 0 {
		t.Fatalf("mix incomplete: %v %+v", seen, r.stats)
	}
	if seen["native"] == 0 || seen["native"] == seen["signed in"] {
		t.Fatalf("signed-in users were not split between browser and app: %v", seen)
	}
	if len(visitorAddresses) != seen["capture"] || len(userAddresses) < 2 || visitorAddresses[""] {
		t.Fatalf("simulated clients do not have distinct addresses: %d captures from %d addresses", seen["capture"], len(visitorAddresses))
	}
	if _, ok := r.Scenarios["dashboard"]; ok {
		t.Fatal("a disabled scenario was reported")
	}
}

func TestSeededMixRejectsUnknownScenarios(t *testing.T) {
	f := fixture{ReferralCodes: []string{"code"}}
	if _, err := seededMix("http://api", "http://web", f, map[string]int{"dashbord": 5}); err == nil {
		t.Fatal("misspelt scenario accepted")
	}
	if _, err := seededMix("http://api", "http://web", fixture{}, nil); err == nil {
		t.Fatal("fixture without referral codes accepted")
	}
}

func TestRunEnforcesPerScenarioBudgets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()
	targets := server.URL + "/fast," + server.URL + "/slow"
	args := func(budget string) []string {
		return []string{"-url", targets, "-duration", "200ms", "-concurrency", "2", "-max-p99", budget}
	}
	noEnv := func(string) string { return "" }
	if err := run(context.Background(), args("1s"), noEnv, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), args("1s,/slow=5ms"), noEnv, io.Discard); err == nil || !strings.Contains(err.Error(), "/slow") {
		t.Fatalf("slow scenario passed its budget: %v", err)
	}
	if err := run(context.Background(), args("1s,/missing=5ms"), noEnv, io.Discard); err == nil {
		t.Fatal("budget for an unknown scenario accepted")
	}
	if err := run(context.Background(), []string{"-duration", "1s"}, noEnv, io.Discard); err == nil {
		t.Fatal("ran without -url or -fixture")
	}
}

func TestRunReadsTheSeedFixture(t *testing.T) {
	var signedIn atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "dad_donor_session=fixture-token" || r.Header.Get("Authorization") == "Bearer fixture-token" {
			signedIn.Add(1)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "fixture.json")
	data, _ := json.Marshal(fixture{Sessions: []string{"fixture-token"}, ReferralCodes: []string{"code"}})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	args := []string{"-api", server.URL, "-fixture", path, "-mix", "profile=1,referral-lookup=0,referral-capture=0,referral-missing=0,content=0,dashboard=0,gifts=0,sharing=0,recurring=0,annual-summary=0", "-duration", "100ms", "-concurrency", "2"}
	if err := run(context.Background(), args, func(string) string { return "" }, &out); err != nil {
		t.Fatal(err)
	}
	var r result
	if err := json.Unmarshal([]byte(out.String()), &r); err != nil || r.Scenarios["profile"].Requests == 0 || r.Scenarios["profile"].Requests != signedIn.Load() {
		t.Fatalf("fixture sessions not used: %s %v", out.String(), err)
	}
}

func TestParsersRejectUnsafeInput(t *testing.T) {
	if targets, err := parseTargets("http://127.0.0.1:8002/a, https://dad.example/b"); err != nil || len(targets) != 2 {
		t.Fatalf("valid targets rejected: %v %v", targets, err)
	}
	for _, invalid := range []string{"", "ftp://host/x", "http://user:pass@host/x", "/relative"} {
		if _, err := parseTargets(invalid); err == nil {
			t.Errorf("target %q accepted", invalid)
		}
	}
	if b, err := parseBudgets("250ms,dashboard=400ms"); err != nil || b.of("dashboard") != 400*time.Millisecond || b.of("content") != 250*time.Millisecond {
		t.Fatalf("budgets misread: %+v %v", b, err)
	}
	for _, invalid := range []string{"fast", "-1ms", "dashboard=0", "1s,2s", "1s,dashboard"} {
		if _, err := parseBudgets(invalid); err == nil {
			t.Errorf("budget %q accepted", invalid)
		}
	}
	for _, invalid := range []string{"dashboard", "dashboard=-1", "dashboard=x"} {
		if _, err := parseWeights(invalid); err == nil {
			t.Errorf("mix %q accepted", invalid)
		}
	}
}

func TestSummarizeReportsBucketUpperEdges(t *testing.T) {
	h := &histogram{}
	for range 98 {
		h.record(time.Millisecond)
	}
	h.record(50 * time.Millisecond)
	h.record(70 * time.Millisecond)
	s := summarize(2, h)
	if s.Requests != 100 || s.P50ms != 1.1 || s.P95ms != 1.1 || s.P99ms != 50.1 || s.MaxMs != 70 || s.RequestsPerSecond != 50 {
		t.Fatalf("summary %+v", s)
	}
}
