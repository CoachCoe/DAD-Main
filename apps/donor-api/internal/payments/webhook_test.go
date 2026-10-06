package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v87"
)

const testSecret = "whsec_public_test_only"
const checkoutID = "22d8a6d3-95d2-4caa-84d0-f1ea2ab73a4f"

func signed(t *testing.T, event map[string]any, at time.Time) ([]byte, string) {
	t.Helper()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return body, fmt.Sprintf("t=%d,v1=%x", at.Unix(), stripe.ComputeSignature(at, body, testSecret))
}
func event() map[string]any {
	return map[string]any{"object": "event", "id": "evt_test", "type": "payment_intent.succeeded", "api_version": APIVersion, "created": 1700000000, "livemode": false, "data": map[string]any{"object": map[string]any{"id": "pi_test", "metadata": map[string]any{"dad_checkout_id": checkoutID, "private": "must disappear"}, "customer": "cus_private", "description": "private description", "currency": "usd", "status": "succeeded", "amount_received": int64(9007199254740993)}}}
}
func TestSignedWebhookAndMinimalFacts(t *testing.T) {
	if stripe.APIVersion != APIVersion {
		t.Fatal("SDK API version drift")
	}
	body, signature := signed(t, event(), time.Now())
	verified, err := verifyEvent(body, signature, testSecret)
	if err != nil || verified == nil {
		t.Fatalf("valid event: %v", err)
	}
	data := string(verified.Data())
	if !strings.Contains(data, "9007199254740993") || strings.Contains(data, "private") || strings.Contains(data, "customer") || strings.Contains(data, "metadata") {
		t.Fatalf("lossy or non-minimal facts: %s", data)
	}
	if verified.ID() != "evt_test" || verified.Kind() != "payment_intent.succeeded" || verified.OccurredAt().Unix() != 1700000000 {
		t.Fatal("envelope changed")
	}
	copied := verified.Data()
	copied[0] = '!'
	if verified.Data()[0] != '{' {
		t.Fatal("verified event was mutable")
	}
	if _, err = verifyEvent(append(body, ' '), signature, testSecret); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal("tamper accepted")
	}
	if _, err = verifyEvent(body, signature, ""); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unconfigured verifier")
	}
	_, oldSignature := signed(t, event(), time.Now().Add(-10*time.Minute))
	if _, err = verifyEvent(body, oldSignature, testSecret); !errors.Is(err, ErrInvalidEvent) {
		t.Fatal("old signature accepted")
	}
	for _, change := range []map[string]any{{"livemode": true}, {"livemode": nil}, {"account": "acct_other"}, {"api_version": "2026-10-01.endive"}, {"object": "thin_event"}, {"created": "1700000000"}} {
		input := event()
		for key, value := range change {
			input[key] = value
		}
		body, signature = signed(t, input, time.Now())
		if _, err = verifyEvent(body, signature, testSecret); err == nil {
			t.Fatalf("invalid envelope accepted: %v", change)
		}
	}
}
func TestIgnoredAndMalformedEvents(t *testing.T) {
	input := event()
	input["type"] = "customer.created"
	body, signature := signed(t, input, time.Now())
	if result, err := verifyEvent(body, signature, testSecret); err != nil || result != nil {
		t.Fatal("unsupported event should be ignored")
	}
	input = event()
	obj := input["data"].(map[string]any)["object"].(map[string]any)
	delete(obj, "metadata")
	body, signature = signed(t, input, time.Now())
	if result, err := verifyEvent(body, signature, testSecret); err != nil || result != nil {
		t.Fatal("unrelated account payment not ignored")
	}
	obj["metadata"] = map[string]any{"dad_checkout_id": "not-a-uuid"}
	body, signature = signed(t, input, time.Now())
	if _, err := verifyEvent(body, signature, testSecret); !errors.Is(err, ErrEventShape) {
		t.Fatal("bad UUID accepted")
	}
}
func TestInvoiceAllowlist(t *testing.T) {
	input := event()
	input["type"] = "invoice.payment_succeeded"
	input["data"] = map[string]any{"object": map[string]any{
		"id": "in_test", "parent": map[string]any{"subscription_details": map[string]any{"subscription": "sub_test", "metadata": map[string]any{"dad_checkout_id": checkoutID}}},
		"lines":              map[string]any{"has_more": false, "data": []any{map[string]any{"amount": 3000, "currency": "usd", "description": "private line", "period": map[string]any{"start": 1700000000, "end": 1702592000}, "parent": map[string]any{"type": "subscription_item_details", "subscription_item_details": map[string]any{"subscription": "sub_test", "proration": false}}}}},
		"payments":           map[string]any{"has_more": false, "data": []any{map[string]any{"status": "paid", "amount_paid": 3000, "currency": "usd", "invoice": "in_test", "livemode": false, "payment": map[string]any{"type": "payment_intent", "payment_intent": "pi_test"}}}},
		"status_transitions": map[string]any{"paid_at": 1700000001},
	}}
	body, signature := signed(t, input, time.Now())
	verified, err := verifyEvent(body, signature, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	var facts map[string]any
	if err = json.Unmarshal(verified.Data(), &facts); err != nil {
		t.Fatal(err)
	}
	if facts["lines_complete"] != true || facts["payments_complete"] != true || facts["subscription"] != "sub_test" || facts["checkout_id"] != checkoutID || strings.Contains(string(verified.Data()), "private") {
		t.Fatalf("wrong invoice facts: %s", verified.Data())
	}
	lines := facts["lines"].([]any)
	if lines[0].(map[string]any)["proration"] != false {
		t.Fatal("line constraints dropped")
	}
}

type failingInbox struct{ called bool }

func (f *failingInbox) SaveVerifiedEvent(context.Context, VerifiedEvent) error {
	f.called = true
	return errors.New("database unavailable")
}
func TestReceiverPropagatesPersistenceFailure(t *testing.T) {
	inbox := &failingInbox{}
	receiver := Receiver{Secret: testSecret, Inbox: inbox}
	body, signature := signed(t, event(), time.Now())
	if err := receiver.Receive(context.Background(), body, signature); err == nil || !inbox.called {
		t.Fatal("acknowledged uncommitted event")
	}
}
