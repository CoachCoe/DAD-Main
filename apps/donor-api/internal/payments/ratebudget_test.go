package payments

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestGatewayWaitsForBudgetBeforeCallingStripe(t *testing.T) {
	for _, tc := range []struct {
		name, budget string
		call         func(context.Context, *StripeGateway) error
	}{
		{"checkout", BucketStaff, func(ctx context.Context, g *StripeGateway) error {
			_, err := g.CreateCheckout(ctx, []byte(`{"mode":"payment"}`), "fixture")
			return err
		}},
		{"checkout read", BucketWorker, func(ctx context.Context, g *StripeGateway) error {
			_, err := g.RetrieveCheckout(ctx, "cs_test_fixture")
			return err
		}},
		{"setup read", BucketWorker, func(ctx context.Context, g *StripeGateway) error {
			_, err := g.RetrieveSetupIntent(ctx, "seti_fixture")
			return err
		}},
		{"refund list", BucketStaff, func(ctx context.Context, g *StripeGateway) error {
			_, err := g.ListPaymentRefunds(ctx, "pi_fixture")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, closeServer := stripeTestGateway(t, func(http.ResponseWriter, *http.Request) {
				t.Error("exhausted budget reached Stripe")
			})
			defer closeServer()
			g.budget = NewRateBudget()
			g.budget.buckets[tc.budget] = newBucket(1, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			if err := tc.call(ctx, g); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("exhausted budget: %v", err)
			}
		})
	}
}

func TestRefundPaginationSpendsBudgetForEveryPage(t *testing.T) {
	calls := 0
	g, closeServer := stripeTestGateway(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","has_more":true,"data":[{"id":"re_fixture"}]}`))
	})
	defer closeServer()
	g.budget = &RateBudget{buckets: map[string]*bucket{BucketStaff: newBucket(0.001, 1)}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := g.ListPaymentRefunds(ctx, "pi_fixture"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exhausted second-page budget: %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want only the budgeted first page", calls)
	}
}

// A fresh bucket grants up to its burst immediately, then blocks until a refill.
func TestRateBudgetBurstThenRefill(t *testing.T) {
	// 10 tokens/second, burst 2: two immediate grants, the third waits ~100ms.
	rb := &RateBudget{buckets: map[string]*bucket{BucketWorker: newBucket(10, 2)}}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		start := time.Now()
		if err := rb.Acquire(ctx, BucketWorker); err != nil {
			t.Fatalf("burst grant %d failed: %v", i, err)
		}
		if waited := time.Since(start); waited > 20*time.Millisecond {
			t.Fatalf("burst grant %d blocked %v, expected immediate", i, waited)
		}
	}
	start := time.Now()
	if err := rb.Acquire(ctx, BucketWorker); err != nil {
		t.Fatalf("post-drain grant failed: %v", err)
	}
	waited := time.Since(start)
	if waited < 50*time.Millisecond {
		t.Fatalf("drained bucket granted after %v, expected a refill wait", waited)
	}
}

// Acquire returns ctx.Err() when the context is cancelled while it waits.
func TestRateBudgetHonorsContextCancellation(t *testing.T) {
	// 1 token/second, burst 1: drain it, then a second Acquire must wait ~1s.
	rb := &RateBudget{buckets: map[string]*bucket{BucketWorker: newBucket(1, 1)}}
	if err := rb.Acquire(context.Background(), BucketWorker); err != nil {
		t.Fatalf("initial grant failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := rb.Acquire(ctx, BucketWorker)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context deadline, got %v", err)
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatal("Acquire did not return promptly on cancellation")
	}
}

// Draining the worker bucket must not stop the staff bucket from granting a
// token within a bounded wait: the buckets are independent reservations.
func TestRateBudgetStaffReservationIndependentOfWorker(t *testing.T) {
	rb := NewRateBudget()
	// Fully drain the worker bucket (its burst is one second of its rate).
	drain := int(WorkerBudgetFraction*StripeBudgetPerSecond) + 1
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < drain; i++ {
		if err := rb.Acquire(ctx, BucketWorker); err != nil {
			t.Fatalf("worker drain %d failed: %v", i, err)
		}
	}
	// The staff bucket is untouched, so a staff grant arrives within a bounded wait.
	staffCtx, staffCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer staffCancel()
	start := time.Now()
	if err := rb.Acquire(staffCtx, BucketStaff); err != nil {
		t.Fatalf("staff grant blocked by worker drain: %v", err)
	}
	if waited := time.Since(start); waited > 100*time.Millisecond {
		t.Fatalf("staff grant waited %v despite an undrained bucket", waited)
	}
}

// TestRateBudgetReconcileReservationIndependentOfWorkerAndStaff proves X3's
// reconcile bucket owns its own slice of the ceiling: draining both the worker
// and staff buckets does not delay a reconcile grant.
func TestRateBudgetReconcileReservationIndependentOfWorkerAndStaff(t *testing.T) {
	rb := NewRateBudget()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	drain := func(bucket string, rate float64) {
		for i := 0; i < int(rate)+1; i++ {
			if err := rb.Acquire(ctx, bucket); err != nil {
				t.Fatalf("%s drain %d failed: %v", bucket, i, err)
			}
		}
	}
	drain(BucketWorker, WorkerBudgetFraction*StripeBudgetPerSecond)
	drain(BucketStaff, StaffBudgetFraction*StripeBudgetPerSecond)
	// Reconcile is untouched, so its grant arrives promptly.
	recCtx, recCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer recCancel()
	start := time.Now()
	if err := rb.Acquire(recCtx, BucketReconcile); err != nil {
		t.Fatalf("reconcile grant blocked by worker/staff drain: %v", err)
	}
	if waited := time.Since(start); waited > 100*time.Millisecond {
		t.Fatalf("reconcile grant waited %v despite an undrained bucket", waited)
	}
}
