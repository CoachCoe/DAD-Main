package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	stepTimeout = 2 * time.Minute
	maxBackoff  = time.Minute
)

// Run repeats step until ctx ends. Each step owns one transaction; cancellation
// interrupts idle waits and bounded processor requests, and an interrupted
// transaction remains available for replay.
//
// With once, Run returns after a single step, including its error. Otherwise a
// failing step is logged and retried with exponential backoff, so a transient
// database or provider outage does not stop the worker; persistent failures
// surface through queue age in the monitor.
func Run(ctx context.Context, once bool, idle time.Duration, step func(context.Context) (bool, error)) error {
	failures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, cancel := context.WithTimeout(ctx, stepTimeout)
		worked, err := step(work)
		cancel()
		if once {
			return err
		}
		wait := time.Duration(0)
		switch {
		case err != nil && ctx.Err() == nil:
			failures++
			wait = min(idle<<min(failures-1, 16), maxBackoff)
			// Error text may carry row values, so only the type is logged.
			slog.Error("worker step failed", "cause", fmt.Sprintf("%T", err), "consecutive_failures", failures, "retry_in", wait.String())
		case err == nil:
			failures = 0
			if !worked {
				wait = idle
			}
		}
		if wait > 0 && !sleep(ctx, wait) {
			return ctx.Err()
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
