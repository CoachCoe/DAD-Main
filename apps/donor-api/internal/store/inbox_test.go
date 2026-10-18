package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/payments"
	"github.com/stripe/stripe-go/v87"
)

func TestVerifiedInboxDurabilityAndConcurrentRedelivery(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	id := testID(t)
	eventID := "evt_" + id.String()
	body, err := json.Marshal(
		map[string]any{
			"object":      "event",
			"id":          eventID,
			"type":        "payment_intent.succeeded",
			"created":     1700000000,
			"api_version": payments.APIVersion,
			"livemode":    false,
			"data": map[string]any{
				"object": map[string]any{
					"id":              "pi_test",
					"metadata":        map[string]any{"dad_checkout_id": id.String()},
					"customer":        "private customer",
					"currency":        "usd",
					"amount_received": 3000,
					"status":          "succeeded",
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "whsec_public_integration_test"
	now := time.Now()
	signature := fmt.Sprintf("t=%d,v1=%x", now.Unix(), stripe.ComputeSignature(now, body, secret))
	receiver := payments.Receiver{Secret: secret, Inbox: StripeInbox{DB: api}}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DELETE FROM donor_stripe_events WHERE stripe_event_id=$1", eventID); err != nil {
			t.Error(err)
		}
	})
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- receiver.Receive(ctx, body, signature) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var data []byte
	var processed *time.Time
	var attempts int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM donor_stripe_events WHERE stripe_event_id=$1", eventID).Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("duplicate inbox records: %d %v", count, err)
	}
	if err = pool.QueryRow(ctx, "SELECT data,processed_at,attempts FROM donor_stripe_events WHERE stripe_event_id=$1", eventID).Scan(&data, &processed, &attempts); err != nil {
		t.Fatal(err)
	}
	if processed != nil || attempts != 0 || strings.Contains(string(data), "private") {
		t.Fatal("receipt processed money or retained private data")
	}
	if _, err = pool.Exec(ctx, "UPDATE donor_stripe_events SET attempts=2,processed_at=$2 WHERE stripe_event_id=$1", eventID, now); err != nil {
		t.Fatal(err)
	}
	if err = receiver.Receive(ctx, body, signature); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "SELECT processed_at,attempts FROM donor_stripe_events WHERE stripe_event_id=$1", eventID).Scan(&processed, &attempts); err != nil ||
		processed == nil ||
		attempts != 2 {
		t.Fatal("redelivery reset worker state")
	}
}
