package payments

import (
	"context"
	"testing"
	"time"
)

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
