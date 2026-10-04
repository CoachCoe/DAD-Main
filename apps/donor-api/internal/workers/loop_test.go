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
		err := Run(context.Background(), true, time.Hour, func(ctx context.Context) (bool, error) {
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
func TestCancellationInterruptsIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, false, time.Hour, func(context.Context) (bool, error) { close(called); return false, nil })
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
