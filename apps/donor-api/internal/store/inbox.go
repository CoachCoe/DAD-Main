package store

import (
	"context"

	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StripeInbox struct{ DB *pgxpool.Pool }

func (inbox StripeInbox) SaveVerifiedEvent(ctx context.Context, event payments.VerifiedEvent) error {
	if event.ID() == "" {
		return payments.ErrInvalidEvent
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	// This single autocommit statement finishes before HTTP acknowledgement. A
	// duplicate delivery keeps the original verified facts and processing state.
	return queries.New(inbox.DB).
		InsertStripeEvent(ctx, queries.InsertStripeEventParams{ID: id, StripeEventID: event.ID(), EventType: event.Kind(), OccurredAt: timestamp(event.OccurredAt()), Data: event.Data()})
}
