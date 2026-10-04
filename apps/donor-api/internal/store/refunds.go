package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

func (w EventWorker) reconcileRefund(ctx context.Context, tx pgx.Tx, event queries.DonorStripeEvent, data payments.Object, now time.Time) error {
	q := queries.New(tx)
	gift, err := q.GiftByPayment(ctx, pgtype.Text{String: data.Text("payment_intent"), Valid: true})
	if err != nil {
		return err
	}
	if w.Gateway == nil {
		return payments.ErrCheckoutUnavailable
	}
	current, err := w.Gateway.RetrieveRefund(ctx, data.Text("id"))
	if err != nil {
		return err
	}
	if current.Text("id") != data.Text("id") {
		return payments.ErrFacts
	}
	return RecordRefundInTx(ctx, q, gift, current, event.ID, pgtype.UUID{}, now)
}

// The caller holds the donor then gift lock. Read current processor state under
// those locks; webhook timestamps cannot order competing refund states reliably.
func RecordRefundInTx(ctx context.Context, q *queries.Queries, gift queries.DonorGift, current payments.Object, eventID, actionID pgtype.UUID, now time.Time) error {
	amount, err := current.Int("amount")
	status := strings.ToUpper(current.Text("status"))
	remoteID := current.Text("id")
	if err != nil || !strings.HasPrefix(remoteID, "re_") || current.Text("payment_intent") != gift.StripePaymentIntentID.String || current.Text("currency") != "usd" || amount <= 0 || amount > gift.AmountMinor || eventID.Valid == actionID.Valid {
		return payments.ErrFacts
	}
	switch status {
	case "PENDING", "REQUIRES_ACTION", "SUCCEEDED", "FAILED", "CANCELED":
	default:
		return payments.ErrFacts
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	if err = q.InsertRefund(ctx, queries.InsertRefundParams{ID: id, GiftID: gift.ID, StripeRefundID: remoteID, AmountMinor: amount, Status: status, ObservedAt: timestamp(now)}); err != nil {
		return err
	}
	refund, err := q.RefundByStripeID(ctx, remoteID)
	if err != nil {
		return err
	}
	if refund.GiftID != gift.ID || refund.AmountMinor != amount {
		return payments.ErrFacts
	}
	if err = q.UpdateRefund(ctx, queries.UpdateRefundParams{ID: refund.ID, Status: status, ObservedAt: timestamp(now)}); err != nil {
		return err
	}
	amounts, err := q.SucceededRefundAmounts(ctx, gift.ID)
	if err != nil {
		return err
	}
	total := int64(0)
	for _, value := range amounts {
		if value > gift.AmountMinor-total {
			return payments.ErrFacts
		}
		total += value
	}
	observation, err := NewID()
	if err != nil {
		return err
	}
	return q.InsertRefundObservation(ctx, queries.InsertRefundObservationParams{ID: observation, RefundID: refund.ID, SourceEventID: eventID, SourceActionID: actionID, Status: status, ObservedAt: timestamp(now)})
}
