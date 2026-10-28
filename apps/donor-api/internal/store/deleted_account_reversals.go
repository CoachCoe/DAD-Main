package store

import (
	"context"
	"errors"
	"time"

	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// reversalLease keeps a claimed reversal from a second worker while its
	// Stripe call is in flight; Stripe's idempotency key covers a lease that
	// lapses mid-call.
	reversalLease   = 5 * time.Minute
	reversalBackoff = 10 * time.Minute
)

// queueDeletedAccountReversals queues the refund of money a checkout took after
// its donor deleted the account, and the cancellation of its subscription. For
// a donor who has not deleted the account it does nothing.
func queueDeletedAccountReversals(ctx context.Context, q *queries.Queries, checkout pgtype.UUID, now time.Time) error {
	if err := q.QueueDeletedAccountRefunds(ctx, queries.QueueDeletedAccountRefundsParams{
		AsOf: timestamp(now), CheckoutID: checkout,
	}); err != nil {
		return err
	}
	return q.QueueDeletedAccountCancellation(ctx, queries.QueueDeletedAccountCancellationParams{
		AsOf: timestamp(now), CheckoutID: checkout,
	})
}

// DeletedAccountReversals makes the Stripe calls queued for deleted accounts.
// The refund and cancellation arrive back as ordinary Stripe events and are
// recorded like any other.
type DeletedAccountReversals struct {
	DB      *pgxpool.Pool
	Gateway payments.ReversalGateway
}

// RunNext performs the next due reversal and reports whether it found one.
func (r DeletedAccountReversals) RunNext(ctx context.Context, now time.Time) (bool, error) {
	job, err := queries.New(r.DB).ClaimDeletedAccountReversal(ctx, queries.ClaimDeletedAccountReversalParams{
		LeaseUntil: timestamp(now.Add(reversalLease)), AsOf: timestamp(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = r.reverse(ctx, job); err != nil {
		return true, queries.New(r.DB).FailDeletedAccountReversal(ctx, queries.FailDeletedAccountReversalParams{
			ID: job.ID, AvailableAt: timestamp(now.Add(reversalBackoff)), LastError: text(eventFailure(err)),
		})
	}
	return true, queries.New(r.DB).CompleteDeletedAccountReversal(ctx, queries.CompleteDeletedAccountReversalParams{
		ID: job.ID, CompletedAt: timestamp(now),
	})
}

func (r DeletedAccountReversals) reverse(ctx context.Context, job queries.DonorDeletedAccountReversal) error {
	if r.Gateway == nil {
		return payments.ErrUnconfigured
	}
	if job.Kind == "REFUND" {
		return r.Gateway.RefundAfterDeletion(ctx, job.Target, job.AmountMinor.Int64, job.ID.String())
	}
	err := r.Gateway.CancelSubscriptionNow(ctx, job.Target, "dad-deleted-account-cancel-"+job.ID.String())
	if err == nil {
		return nil
	}
	// Stripe refuses to cancel a subscription that has already ended.
	current, readErr := r.Gateway.RetrieveSubscription(ctx, job.Target)
	if readErr == nil && current.Text("id") == job.Target && terminalSubscription(current.Text("status")) {
		return nil
	}
	return err
}
