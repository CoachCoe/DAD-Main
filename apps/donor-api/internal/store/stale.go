package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

// liveReadWindow bounds the processor read behind a view. A view that cannot
// read the processor in time shows the last observed state instead, marked
// stale and offering no changes; commands always reconcile live.
const liveReadWindow = 5 * time.Second

// liveRead runs a view's processor read. live is false, with no error, when
// the processor could not answer in time or is not configured.
func liveRead[T any](ctx context.Context, read func(context.Context) (T, error)) (value T, live bool, err error) {
	bounded, cancel := context.WithTimeout(ctx, liveReadWindow)
	defer cancel()
	value, err = read(bounded)
	if err != nil && ctx.Err() == nil && (errors.Is(err, payments.ErrProcessorUnavailable) || errors.Is(err, payments.ErrUnconfigured) || errors.Is(err, context.DeadlineExceeded)) {
		return value, false, nil
	}
	return value, err == nil, err
}

// storedDetails describes the donor's monthly gift as last observed, without
// the processor.
func storedDetails(ctx context.Context, q *queries.Queries, donor pgtype.UUID) (RecurringDetails, error) {
	attempt, err := q.LatestMonthlyAttemptSnapshot(ctx, donor)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecurringDetails{Mode: "test", State: "ready"}, nil
	}
	if err != nil {
		return RecurringDetails{}, err
	}
	preparing := RecurringDetails{Mode: "test", State: "preparing", Stale: true}
	if !attempt.StripeSubscriptionID.Valid {
		return preparing, nil
	}
	row, err := q.SubscriptionForCheckout(ctx, attempt.CheckoutID)
	if errors.Is(err, pgx.ErrNoRows) {
		return preparing, nil
	}
	if err != nil {
		return RecurringDetails{}, err
	}
	view, err := subscriptionView(ctx, q, attempt, row)
	view.Stale, view.ObservedAt = true, &row.ObservedAt.Time
	return view, err
}

// storedRefundQuote is a refund preview from the refund states last observed
// for gift: pending refunds still reserve their amount.
func storedRefundQuote(ctx context.Context, q *queries.Queries, gift queries.DonorGift) (RefundQuote, error) {
	refunds, err := q.ReceiptRefunds(ctx, gift.ID)
	if err != nil {
		return RefundQuote{}, err
	}
	quote := RefundQuote{GiftID: gift.ID, DonorID: gift.DonorID, AmountMinor: gift.AmountMinor, AvailableMinor: gift.AmountMinor, Currency: "USD", Mode: "test", Stale: true}
	for _, r := range refunds {
		if r.Status != "FAILED" && r.Status != "CANCELED" {
			quote.AvailableMinor -= r.AmountMinor
		}
		if quote.ObservedAt == nil || r.ObservedAt.Time.After(*quote.ObservedAt) {
			quote.ObservedAt = &r.ObservedAt.Time
		}
	}
	if quote.AvailableMinor < 0 {
		return RefundQuote{}, payments.ErrFacts
	}
	return quote, nil
}
