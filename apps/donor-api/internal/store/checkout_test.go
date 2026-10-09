package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/dbroles"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type checkoutCall struct {
	params []byte
	key    string
}
type checkoutGateway struct {
	mu     sync.Mutex
	calls  []checkoutCall
	fail   bool
	remote *payments.RemoteCheckout
	pool   *pgxpool.Pool
}

func (g *checkoutGateway) CreateCheckout(ctx context.Context, params []byte, key string) (payments.RemoteCheckout, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, checkoutCall{append([]byte(nil), params...), key})
	// A separate connection must see the immutable request before the network call.
	var exists bool
	if err := g.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM donor_stripe_checkouts WHERE id::text=$1)", key[len("dad-checkout-"):]).Scan(&exists); err != nil || !exists {
		return payments.RemoteCheckout{}, errors.New("uncommitted checkout")
	}
	if g.fail {
		g.fail = false
		return payments.RemoteCheckout{}, errors.New("lost provider acknowledgement")
	}
	if g.remote != nil {
		return *g.remote, nil
	}
	live := false
	return payments.RemoteCheckout{ID: "cs_test_" + key, URL: "https://checkout.stripe.com/c/" + key, Livemode: &live}, nil
}
func checkoutFixture(t *testing.T) (CheckoutService, payments.CheckoutRequest, pgtype.UUID, *checkoutGateway) {
	t.Helper()
	pool := database(t)
	id, email := donor(t, pool)
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			"DELETE FROM donor_recurring_reservations WHERE donor_id=$1",
			"DELETE FROM donor_stripe_checkouts WHERE checkout_id IN (SELECT id FROM donor_checkouts WHERE donor_id=$1)",
			"DELETE FROM donor_checkouts WHERE donor_id=$1",
			"DELETE FROM donor_referral_touches WHERE referrer_id=$1",
		} {
			if _, err := pool.Exec(ctx, sql, id); err != nil {
				t.Error(err)
			}
		}
	})
	gateway := &checkoutGateway{pool: pool}
	return CheckoutService{DB: as(t, dbroles.API), Gateway: gateway, Origin: "http://localhost:3000"}, payments.CheckoutRequest{RequestID: testID(t), Email: email, Amount: 3000, Frequency: "MONTHLY"}, id, gateway
}
func TestCheckoutRetriesAndReservations(t *testing.T) {
	service, request, _, gateway := checkoutFixture(t)
	ctx := context.Background()
	gateway.fail = true
	if _, err := service.BeginCheckout(ctx, request); !errors.Is(err, payments.ErrProcessorUnavailable) {
		t.Fatal("lost response not retryable")
	}
	service.Origin = "https://changed.example.org"
	result, err := service.BeginCheckout(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(gateway.calls) != 2 || !bytes.Equal(gateway.calls[0].params, gateway.calls[1].params) || gateway.calls[0].key != gateway.calls[1].key {
		t.Fatal("retry parameters or key changed")
	}
	var params map[string]any
	if err = json.Unmarshal(gateway.calls[0].params, &params); err != nil {
		t.Fatal(err)
	}
	if params["customer"] != nil || params["customer_email"] != request.Email || params["mode"] != "subscription" || params["payment_method_types"] != nil || params["allowed_payment_method_types"].([]any)[0] != "card" {
		t.Fatalf("wrong guest parameters: %v", params)
	}
	// Stripe accepts an expiry 30 minutes to 24 hours ahead; a shorter one frees the reservation sooner.
	if expires := time.Unix(int64(params["expires_at"].(float64)), 0); expires.Before(time.Now().Add(55*time.Minute)) || expires.After(time.Now().Add(65*time.Minute)) {
		t.Fatalf("checkout expires at %v, want about an hour from now", expires)
	}
	repeated, err := service.BeginCheckout(ctx, request)
	if err != nil || repeated != result || len(gateway.calls) != 2 {
		t.Fatal("completed creation not reused")
	}
	changed := request
	changed.Amount = 9000
	if _, err = service.BeginCheckout(ctx, changed); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatal("changed request reused key")
	}
	changed = request
	changed.RequestID = testID(t)
	if _, err = service.BeginCheckout(ctx, changed); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatal("second recurring checkout allowed")
	}
	changed.Frequency = "ONE_TIME"
	if _, err = service.BeginCheckout(ctx, changed); err != nil {
		t.Fatalf("one time blocked by reservation: %v", err)
	}
}
func TestCheckoutConcurrentMonthlyReservation(t *testing.T) {
	service, request, _, gateway := checkoutFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		r := request
		r.RequestID = testID(t)
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := service.BeginCheckout(ctx, r); results <- err }()
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, payments.ErrCheckoutConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 || len(gateway.calls) != 1 {
		t.Fatal("concurrent request escaped monthly reservation")
	}
}
func TestCheckoutUncertainWindowAndDisabledDonor(t *testing.T) {
	service, request, id, gateway := checkoutFixture(t)
	pool := database(t)
	ctx := context.Background()
	gateway.fail = true
	if _, err := service.BeginCheckout(ctx, request); err == nil {
		t.Fatal("expected lost response")
	}
	if _, err := pool.Exec(ctx, "UPDATE donor_stripe_checkouts SET created_at=$2 WHERE id=$1", request.RequestID, time.Now().Add(-23*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginCheckout(ctx, request); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatal("old uncertain request retried")
	}
	if len(gateway.calls) != 1 {
		t.Fatal("old request reached provider")
	}
	if _, err := pool.Exec(ctx, "UPDATE donors SET disabled=true WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	request.RequestID = testID(t)
	request.Frequency = "ONE_TIME"
	if _, err := service.BeginCheckout(ctx, request); !errors.Is(err, ErrDenied) {
		t.Fatal("disabled donor checked out")
	}
}
func TestCheckoutAttributionSnapshotAndSelfReferral(t *testing.T) {
	service, request, id, gateway := checkoutFixture(t)
	pool := database(t)
	ctx := context.Background()
	referrer, _ := donor(t, pool)
	// Registered after referrer cleanup so dependent rows disappear first.
	t.Cleanup(func() {
		for _, sql := range []string{"DELETE FROM donor_recurring_reservations WHERE donor_id=$1", "DELETE FROM donor_stripe_checkouts WHERE checkout_id IN (SELECT id FROM donor_checkouts WHERE donor_id=$1)", "DELETE FROM donor_checkouts WHERE donor_id=$1"} {
			if _, err := pool.Exec(ctx, sql, id); err != nil {
				t.Error(err)
			}
		}
		if _, err := pool.Exec(ctx, "DELETE FROM donor_referral_touches WHERE referrer_id=$1", referrer); err != nil {
			t.Error(err)
		}
	})
	token := "test-visitor"
	touch := testID(t)
	if _, err := pool.Exec(ctx, `INSERT INTO donor_referral_touches (id,token_hash,referrer_id,kind,touched_at,expires_at) VALUES ($1,$2,$3,'IRL',$4,$5)`, touch, HashToken(token), referrer, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	request.Visitor = &token
	gateway.fail = true
	if _, err := service.BeginCheckout(ctx, request); err == nil {
		t.Fatal("expected lost response")
	}
	if _, err := pool.Exec(ctx, "UPDATE donor_referral_touches SET expires_at=$2 WHERE id=$1", touch, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginCheckout(ctx, request); err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(gateway.calls[1].params, &params); err != nil {
		t.Fatal(err)
	}
	metadata := params["subscription_data"].(map[string]any)["metadata"].(map[string]any)
	if metadata["dad_referrer_id"] != referrer.String() || metadata["dad_referral_kind"] != "IRL" {
		t.Fatal("snapshot referral was lost on retry")
	}
	if _, err := pool.Exec(ctx, "UPDATE donor_referral_touches SET referrer_id=$2,expires_at=$3 WHERE id=$1", touch, id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	request.RequestID = testID(t)
	request.Frequency = "ONE_TIME"
	if _, err := service.BeginCheckout(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(gateway.calls[2].params, &params); err != nil {
		t.Fatal(err)
	}
	if params["metadata"].(map[string]any)["dad_referrer_id"] != nil {
		t.Fatal("self referral accepted")
	}
}
func TestUnexpectedProviderCheckoutIsNotAcknowledged(t *testing.T) {
	service, request, _, gateway := checkoutFixture(t)
	pool := database(t)
	ctx := context.Background()
	live := true
	gateway.remote = &payments.RemoteCheckout{ID: "cs_live_invalid", URL: "https://checkout.stripe.com/c/test", Livemode: &live}
	if _, err := service.BeginCheckout(ctx, request); !errors.Is(err, payments.ErrProcessorUnavailable) {
		t.Fatal("live response accepted")
	}
	attempt, err := queries.New(pool).StripeCheckout(ctx, request.RequestID)
	if err != nil || attempt.HostedUrl.Valid || attempt.StripeSessionID.Valid || attempt.Status != "PENDING" {
		t.Fatal("untrusted checkout acknowledged")
	}
}

func TestConcurrentRetrySharesOneDraftAndProviderKey(t *testing.T) {
	service, request, id, gateway := checkoutFixture(t)
	pool := database(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan payments.CheckoutResult, 4)
	failures := make(chan error, 4)
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			value, err := service.BeginCheckout(ctx, request)
			results <- value
			failures <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first payments.CheckoutResult
	for result := range results {
		if first.CheckoutID == "" {
			first = result
		} else if result != first {
			t.Fatal("concurrent retry split checkout")
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_checkouts WHERE donor_id=$1", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent retry created duplicate drafts")
	}
	for _, call := range gateway.calls {
		if call.key != gateway.calls[0].key || !bytes.Equal(call.params, gateway.calls[0].params) {
			t.Fatal("concurrent retry changed Stripe request")
		}
	}
}
