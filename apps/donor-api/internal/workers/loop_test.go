package workers

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOnceAndError(t *testing.T) {
	for _, failure := range []error{nil, errors.New("failed")} {
		count := 0
		err := Run(context.Background(), true, time.Hour, StepBudget, func(ctx context.Context) (bool, error) {
			count++
			if _, ok := ctx.Deadline(); !ok {
				t.Error("unbounded step")
			}
			return true, failure
		})
		if !errors.Is(err, failure) || count != 1 {
			t.Fatal("step outcome changed", err, count)
		}
	}
}
func TestEachStepGetsTheBudgetItWasGiven(t *testing.T) {
	for _, budget := range []time.Duration{StepBudget, ProcessorStepBudget} {
		started := time.Now()
		var deadline time.Time
		if err := Run(context.Background(), true, time.Hour, budget, func(ctx context.Context) (bool, error) {
			deadline, _ = ctx.Deadline()
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		if remaining := deadline.Sub(started); remaining < budget || remaining > budget+time.Second {
			t.Fatalf("step budget %v, want %v", remaining, budget)
		}
	}
}
func TestCancellationInterruptsIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, false, time.Hour, StepBudget, func(context.Context) (bool, error) { close(called); return false, nil })
	}()
	<-called
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle wait ignored shutdown")
	}
}

func TestContinuousModeBacksOffAndRecoversFromFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []time.Time
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, false, 50*time.Millisecond, StepBudget, func(context.Context) (bool, error) {
			calls = append(calls, time.Now())
			switch len(calls) {
			case 1, 2, 3:
				return true, errors.New("transient")
			case 4:
				return true, nil
			default:
				cancel()
				return false, nil
			}
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker stopped retrying after a failure")
	}
	if len(calls) != 5 {
		t.Fatalf("got %d steps, want 5", len(calls))
	}
	first, second, third := calls[1].Sub(calls[0]), calls[2].Sub(calls[1]), calls[3].Sub(calls[2])
	if first < 50*time.Millisecond || second < 100*time.Millisecond || third < 200*time.Millisecond {
		t.Fatalf("backoff did not grow: %v %v %v", first, second, third)
	}
	if gap := calls[4].Sub(calls[3]); gap >= 50*time.Millisecond {
		t.Fatalf("successful work waited %v before continuing", gap)
	}
}
