package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/payments"
)

func nativeCheckoutParams(t *testing.T, gateway *checkoutGateway, call int) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(gateway.calls[call].params, &params); err != nil {
		t.Fatal(err)
	}
	return params
}

func TestNativeCheckoutOpensTheSignedInDonorsMonthlyGift(t *testing.T) {
	service, request, id, gateway := checkoutFixture(t)
	ctx := context.Background()
	token := claimSession(t, database(t), id, app).Access

	result, err := service.BeginNativeCheckout(ctx, token, request.RequestID, 6000)
	if err != nil {
		t.Fatal(err)
	}
	params := nativeCheckoutParams(t, gateway, 0)
	line := params["line_items"].([]any)[0].(map[string]any)["price_data"].(map[string]any)
	if params["mode"] != "subscription" || params["customer_email"] != request.Email || line["unit_amount"] != float64(6000) ||
		params["success_url"] != "http://localhost:3000/app/return?result=success&session_id={CHECKOUT_SESSION_ID}" ||
		params["cancel_url"] != "http://localhost:3000/app/return?result=cancel" {
		t.Fatalf("wrong native parameters: %v", params)
	}
	if want := time.Unix(int64(params["expires_at"].(float64)), 0).UTC(); !result.ExpiresAt.Equal(want) {
		t.Fatalf("expires %v, want %v", result.ExpiresAt, want)
	}
	if result.SessionID != "cs_test_dad-checkout-"+request.RequestID.String() ||
		result.URL != "https://checkout.stripe.com/c/dad-checkout-"+request.RequestID.String() {
		t.Fatalf("unexpected session %+v", result)
	}

	repeated, err := service.BeginNativeCheckout(ctx, token, request.RequestID, 6000)
	if err != nil || repeated != result || len(gateway.calls) != 1 {
		t.Fatalf("a retry did not reuse the session: %+v %v with %d processor calls", repeated, err, len(gateway.calls))
	}
	if _, err = service.BeginNativeCheckout(ctx, token, request.RequestID, 9000); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatalf("a changed amount reused the key: %v", err)
	}
	other, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.BeginNativeCheckout(ctx, token, other, 6000); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatalf("a second monthly checkout was allowed: %v", err)
	}
}

func TestNativeCheckoutRefusesAnInvalidAmountOrSession(t *testing.T) {
	service, request, id, gateway := checkoutFixture(t)
	ctx := context.Background()
	token := claimSession(t, database(t), id, app).Access
	for _, amount := range []int64{0, 2999, 4500, 93000} {
		if _, err := service.BeginNativeCheckout(ctx, token, request.RequestID, amount); !errors.Is(err, payments.ErrCheckoutInput) {
			t.Errorf("amount %d: %v", amount, err)
		}
	}
	if _, err := service.BeginNativeCheckout(ctx, strings.Repeat("x", 43), request.RequestID, 3000); !errors.Is(err, ErrDenied) {
		t.Errorf("an unknown session was admitted: %v", err)
	}
	unconfigured := service
	unconfigured.Gateway = nil
	if _, err := unconfigured.BeginNativeCheckout(ctx, token, request.RequestID, 3000); !errors.Is(err, payments.ErrUnconfigured) {
		t.Errorf("no processor: %v", err)
	}
	if len(gateway.calls) != 0 {
		t.Fatalf("a refused request reached the processor %d times", len(gateway.calls))
	}
}

func TestNativeCheckoutReturnsToItsOwnOrigin(t *testing.T) {
	service, request, id, gateway := checkoutFixture(t)
	token := claimSession(t, database(t), id, app).Access
	service.NativeReturnOrigin = "https://app.example.org"
	if _, err := service.BeginNativeCheckout(context.Background(), token, request.RequestID, 3000); err != nil {
		t.Fatal(err)
	}
	params := nativeCheckoutParams(t, gateway, 0)
	if params["success_url"] != "https://app.example.org/app/return?result=success&session_id={CHECKOUT_SESSION_ID}" ||
		params["cancel_url"] != "https://app.example.org/app/return?result=cancel" {
		t.Fatalf("returned to %v and %v", params["success_url"], params["cancel_url"])
	}
}

// A checkout the app starts completes through the same verified webhooks as one
// the website starts: the donor ends with an active monthly gift.
func TestNativeCheckoutCompletesThroughTheWebhook(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	ctx := context.Background()
	token := claimSession(t, f.db, f.donorID, app).Access
	started, err := f.service.BeginNativeCheckout(ctx, token, f.request.RequestID, 3000)
	if err != nil {
		t.Fatal(err)
	}
	recurring := RecurringService{DB: as(t, dbroles.API), Origin: f.service.Origin}
	before, err := recurring.Details(ctx, token)
	if err != nil || before.Subscription != nil {
		t.Fatalf("a gift was active before payment: %+v %v", before, err)
	}
	var checkoutID string
	if err = f.db.QueryRow(ctx, "SELECT checkout_id::text FROM donor_stripe_checkouts WHERE id=$1", f.request.RequestID).Scan(&checkoutID); err != nil {
		t.Fatal(err)
	}
	subID := "sub_" + checkoutID
	start := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	f.gateway.subscriptions[subID] = paymentObject(t, subscription(checkoutID, start))
	event := f.receive(t, "checkout.session.completed", map[string]any{
		"id":           started.SessionID,
		"status":       "complete",
		"subscription": subID,
		"metadata":     map[string]any{"dad_checkout_id": checkoutID},
	})
	f.process(t)
	assertEventProcessed(t, f, event, true)

	after, err := recurring.Details(ctx, token)
	if err != nil || after.Subscription == nil || after.Subscription.Status != "active" || after.Subscription.AmountMinor != 3000 {
		t.Fatalf("the webhook did not activate the gift: %+v %v", after, err)
	}
	if _, err = f.service.BeginNativeCheckout(ctx, token, f.request.RequestID, 3000); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatalf("a retry of the completed checkout reopened it: %v", err)
	}
	other, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.BeginNativeCheckout(ctx, token, other, 3000); !errors.Is(err, payments.ErrCheckoutConflict) {
		t.Fatalf("a donor with an active gift started another: %v", err)
	}
}
