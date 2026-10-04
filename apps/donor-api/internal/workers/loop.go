package workers

import (
	"context"
	"time"
)

// Each step owns one transaction. Cancellation interrupts idle waits and bounded
// processor requests; an interrupted transaction remains available for replay.
func Run(ctx context.Context, once bool, idle time.Duration, step func(context.Context) (bool, error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, cancel := context.WithTimeout(ctx, 2*time.Minute)
		worked, err := step(work)
		cancel()
		if err != nil {
			return err
		}
		if once {
			return nil
		}
		if !worked {
			timer := time.NewTimer(idle)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
}
