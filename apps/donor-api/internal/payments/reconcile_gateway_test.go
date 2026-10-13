package payments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87"
)

// stripeTestGateway points the pinned SDK at a local test server, exactly as
// checkout_test.go does, so no live Stripe is touched.
func stripeTestGateway(t *testing.T, handler http.HandlerFunc) (*StripeGateway, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	backends := stripe.NewBackendsWithConfig(&stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0), LeveledLogger: &stripe.LeveledLogger{Level: stripe.LevelNull}})
	return &StripeGateway{client: stripe.NewClient("sk_test_public_fixture", stripe.WithBackends(backends))}, server.Close
}

func TestReconcileListPagesByCreatedWindow(t *testing.T) {
	// A full day in New_York converted to a UTC created range.
	start := time.Date(2027, 3, 1, 5, 0, 0, 0, time.UTC)
	end := time.Date(2027, 3, 2, 4, 59, 59, 0, time.UTC)
	for _, tc := range []struct {
		name, path string
		call       func(g *StripeGateway) ([]Object, error)
	}{
		{"charges", "/v1/charges", func(g *StripeGateway) ([]Object, error) {
			return g.ListChargesByCreated(context.Background(), start, end)
		}},
		{"refunds", "/v1/refunds", func(g *StripeGateway) ([]Object, error) {
			return g.ListRefundsByCreated(context.Background(), start, end)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			g, closeServer := stripeTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != tc.path || r.Header.Get("Stripe-Version") != APIVersion {
					t.Errorf("Stripe boundary changed: %s %s", r.Method, r.URL.Path)
				}
				q := r.URL.Query()
				if q.Get("created[gte]") != "1803877200" || q.Get("created[lte]") != "1803963599" || q.Get("limit") != "100" {
					t.Errorf("wrong window query: %s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				// First page has_more=true, second closes the window.
				if q.Get("starting_after") == "" {
					_, _ = w.Write([]byte(`{"object":"list","has_more":true,"data":[{"id":"obj_1","amount":3000,"livemode":false},{"id":"obj_2","amount":5000,"livemode":false}]}`))
				} else if q.Get("starting_after") == "obj_2" {
					_, _ = w.Write([]byte(`{"object":"list","has_more":false,"data":[{"id":"obj_3","amount":7000,"livemode":false}]}`))
				} else {
					t.Errorf("unexpected cursor: %s", q.Get("starting_after"))
				}
			})
			defer closeServer()
			rows, err := tc.call(g)
			if err != nil {
				t.Fatalf("list failed: %v", err)
			}
			if calls != 2 || len(rows) != 3 {
				t.Fatalf("calls=%d rows=%d, want 2 pages of 3 objects", calls, len(rows))
			}
			if rows[0].Text("id") != "obj_1" || rows[2].Text("id") != "obj_3" {
				t.Errorf("pages not concatenated in order: %v", rows)
			}
		})
	}
}

func TestReconcileListRejectsLivemodeObject(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	end := time.Unix(2000, 0).UTC()
	g, closeServer := stripeTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","has_more":false,"data":[{"id":"ch_live","amount":1,"livemode":true}]}`))
	})
	defer closeServer()
	if _, err := g.ListChargesByCreated(context.Background(), start, end); err == nil {
		t.Fatal("a livemode charge was reconciled")
	}
	if _, err := g.ListRefundsByCreated(context.Background(), start, end); err == nil {
		t.Fatal("a livemode refund was reconciled")
	}
}

// Ensure StripeGateway satisfies the read-only ReconcileGateway interface.
var _ ReconcileGateway = (*StripeGateway)(nil)
