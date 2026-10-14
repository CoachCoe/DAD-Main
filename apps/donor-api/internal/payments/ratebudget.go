package payments

import (
	"context"
	"sync"
	"time"
)

// Stripe caps a test-mode account at roughly 100 requests per second shared
// across every process. Worker reads can consume the whole ceiling and starve
// staff refund/cancel/amount commands, so the gateway spends from named buckets
// that each own a fraction of the ceiling. A worker self-limits its reads below
// the shared ceiling, which leaves provider capacity for the separate staff
// process without any cross-process coordination.
const (
	// StripeBudgetPerSecond is the shared Stripe request ceiling in req/s.
	StripeBudgetPerSecond = 100.0
	// WorkerBudgetFraction is the share of the ceiling worker reads may spend.
	WorkerBudgetFraction = 0.6
	// StaffBudgetFraction is the share of the ceiling staff mutations may spend.
	StaffBudgetFraction = 0.3
)

// Bucket names. Adding a new bucket (e.g. BucketReconcile for X3) is one
// constant here plus one registration line in NewRateBudget — no type change.
const (
	BucketWorker = "worker"
	BucketStaff  = "staff"
)

// bucket is a mutex-guarded token bucket. tokens refills at rate tokens/second,
// capped at burst, computed lazily from elapsed wall time so there is no
// background goroutine.
type bucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64 // maximum tokens held
	tokens float64
	last   time.Time
}

func newBucket(rate, burst float64) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: time.Now()}
}

// reserve refills from elapsed time and, if a token is available, spends one
// and returns 0. Otherwise it returns the duration until the next token.
func (b *bucket) reserve(now time.Time) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	if b.rate <= 0 {
		return 0
	}
	return time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
}

// RateBudget hands out tokens from a fixed set of named buckets so one bucket
// draining does not block another. Buckets are registered once at construction;
// Acquire is safe for concurrent use.
type RateBudget struct {
	buckets map[string]*bucket
}

// NewRateBudget registers the worker and staff buckets, each with a rate and
// burst derived from its fraction of StripeBudgetPerSecond. The burst equals one
// second of the bucket's rate so a quiet bucket absorbs a short spike without
// exceeding its average share.
func NewRateBudget() *RateBudget {
	workerRate := WorkerBudgetFraction * StripeBudgetPerSecond
	staffRate := StaffBudgetFraction * StripeBudgetPerSecond
	return &RateBudget{buckets: map[string]*bucket{
		BucketWorker: newBucket(workerRate, workerRate),
		BucketStaff:  newBucket(staffRate, staffRate),
	}}
}

var (
	defaultRateBudget     *RateBudget
	defaultRateBudgetOnce sync.Once
)

// DefaultRateBudget returns the process-wide budget every StripeGateway shares
// unless a test injects its own.
func DefaultRateBudget() *RateBudget {
	defaultRateBudgetOnce.Do(func() {
		defaultRateBudget = NewRateBudget()
	})
	return defaultRateBudget
}

// Acquire blocks until a token is available for the named bucket or ctx is
// done, returning ctx.Err() on cancellation. An unknown bucket name grants
// immediately so a missing registration never deadlocks a call path.
func (r *RateBudget) Acquire(ctx context.Context, name string) error {
	b, ok := r.buckets[name]
	if !ok {
		return ctx.Err()
	}
	for {
		wait := b.reserve(time.Now())
		if wait <= 0 {
			return ctx.Err()
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
