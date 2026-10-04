package payments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stripe/stripe-go/v87"
)

func TestCheckoutUsesPinnedSDKAndSavedParameters(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/checkout/sessions" || r.Header.Get("Stripe-Version") != APIVersion || r.Header.Get("Idempotency-Key") != "dad-checkout-test" {
			t.Error("Stripe boundary changed")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("allowed_payment_method_types[0]") != "card" || r.Form.Get("payment_method_types[0]") != "" || r.Form.Get("line_items[0][price_data][unit_amount]") != "3000" || r.Form.Get("customer") != "" {
			t.Errorf("wrong Stripe request form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"cs_test_fixture","url":"https://checkout.stripe.com/c/test","livemode":false}`))
	}))
	defer server.Close()
	backends := stripe.NewBackendsWithConfig(&stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0), LeveledLogger: &stripe.LeveledLogger{Level: stripe.LevelNull}})
	gateway := &StripeGateway{client: stripe.NewClient("sk_test_public_fixture", stripe.WithBackends(backends))}
	raw := []byte(`{"mode":"payment","customer_email":"donor@example.org","allowed_payment_method_types":["card"],"line_items":[{"price_data":{"currency":"usd","unit_amount":3000,"product_data":{"name":"DAD one-time gift"}},"quantity":1}]}`)
	result, err := gateway.CreateCheckout(context.Background(), raw, "dad-checkout-test")
	if err != nil || ValidateRemoteCheckout(result) != nil || calls != 1 {
		t.Fatalf("SDK request failed: %v", err)
	}
	if _, err = gateway.CreateCheckout(context.Background(), []byte(`{"payment_method_types":["card"]}`), "old-key"); err == nil || calls != 1 {
		t.Fatal("unknown persisted params silently changed")
	}
}
func TestPaymentBoundariesRejectLiveAndUnsafeOrigins(t *testing.T) {
	for _, key := range []string{"sk_live_test", "rk_live_test", ""} {
		if _, err := NewStripeGateway(key); err == nil {
			t.Fatal("non-test credentials accepted")
		}
	}
	for _, origin := range []string{"http://example.org", "https://user:pass@example.org", "https://example.org/", "https://example.org?x=y", "https://example.org#part", "https://"} {
		if ValidateOrigin(origin) == nil {
			t.Fatalf("unsafe origin accepted: %s", origin)
		}
	}
	for _, origin := range []string{"http://localhost:3000", "http://127.0.0.1:3000", "https://donor.example.org"} {
		if err := ValidateOrigin(origin); err != nil {
			t.Fatal(err)
		}
	}
	live := false
	for _, result := range []RemoteCheckout{{ID: "cs_test_fixture", URL: "https://checkout.stripe.com/c/test"}, {ID: "cs_live_fixture", URL: "https://checkout.stripe.com/c/test", Livemode: &live}, {ID: "cs_test_fixture", URL: "https://checkout.stripe.com.evil.example/c/test", Livemode: &live}, {ID: "cs_test_fixture", URL: "https://user@checkout.stripe.com/c/test", Livemode: &live}} {
		if ValidateRemoteCheckout(result) == nil {
			t.Fatal("unsafe checkout response accepted")
		}
	}
}
