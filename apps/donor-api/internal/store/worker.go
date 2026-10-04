package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type EventWorker struct {
	DB      *pgxpool.Pool
	Gateway payments.FinancialGateway
}

func (w EventWorker) ProcessNext(ctx context.Context) (bool, error) {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	event, err := q.NextStripeEvent(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = q.IncrementEventAttempt(ctx, event.ID); err != nil {
		return false, err
	}
	work, err := tx.Begin(ctx)
	if err != nil {
		return false, err
	}
	applyErr := w.applyEvent(ctx, work, event, time.Now().UTC())
	if applyErr == nil {
		applyErr = work.Commit(ctx)
	}
	if applyErr != nil {
		if err = work.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return false, err
		}
		category := "ValueError"
		if errors.Is(applyErr, payments.ErrCheckoutUnavailable) {
			category = "PaymentUnavailable"
		}
		if err = q.FailStripeEvent(ctx, queries.FailStripeEventParams{ID: event.ID, LastError: pgtype.Text{String: category, Valid: true}}); err != nil {
			return false, err
		}
	} else if err = q.CompleteStripeEvent(ctx, queries.CompleteStripeEventParams{ID: event.ID, ProcessedAt: timestamp(time.Now())}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func parseID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		return id, payments.ErrFacts
	}
	return id, nil
}
func (w EventWorker) applyEvent(ctx context.Context, tx pgx.Tx, event queries.DonorStripeEvent, now time.Time) error {
	q := queries.New(tx)
	data, err := payments.DecodeObject(event.Data)
	if err != nil {
		return err
	}
	var donorID pgtype.UUID
	switch {
	case data.Text("payment_update_id") != "":
		id, err := parseID(data.Text("payment_update_id"))
		if err != nil {
			return err
		}
		donorID, err = q.PaymentUpdateOwner(ctx, id)
		if err != nil {
			return err
		}
		if _, err = q.LockDonor(ctx, donorID); err != nil {
			return err
		}
		row, err := q.PaymentUpdateByID(ctx, id)
		if err != nil {
			return err
		}
		gateway, ok := w.Gateway.(payments.PaymentMethodGateway)
		if !ok {
			return payments.ErrCheckoutUnavailable
		}
		status, err := reconcilePaymentUpdate(ctx, tx, gateway, row, data.Text("id"), now)
		if err != nil {
			return err
		}
		if !terminalPaymentUpdate(status) {
			return payments.ErrFacts
		}
	case strings.HasPrefix(event.EventType, "refund."):
		donorID, err = q.GiftOwnerByPayment(ctx, pgtype.Text{String: data.Text("payment_intent"), Valid: true})
		if err != nil {
			return err
		}
		if _, err = q.LockDonor(ctx, donorID); err != nil {
			return err
		}
		if err = w.reconcileRefund(ctx, tx, event, data, now); err != nil {
			return err
		}
	default:
		checkoutID, err := parseID(data.Text("checkout_id"))
		if err != nil {
			return err
		}
		draft, err := q.CheckoutDraft(ctx, checkoutID)
		if err != nil {
			return err
		}
		donorID = draft.DonorID
		if _, err = q.LockDonor(ctx, donorID); err != nil {
			return err
		}
		attempt, err := q.AttemptForCheckout(ctx, checkoutID)
		if err != nil {
			return err
		}
		if err = w.applyCheckoutEvent(ctx, tx, event, data, draft, attempt, now); err != nil {
			return err
		}
	}
	_, err = RefreshProjectionInTx(ctx, tx, donorID, now)
	return err
}

func bindSubscription(ctx context.Context, q *queries.Queries, attempt queries.DonorStripeCheckout, subscription string) error {
	if !strings.HasPrefix(subscription, "sub_") || (attempt.StripeSubscriptionID.Valid && attempt.StripeSubscriptionID.String != subscription) {
		return payments.ErrFacts
	}
	return q.BindSubscription(ctx, queries.BindSubscriptionParams{ID: attempt.ID, StripeSubscriptionID: pgtype.Text{String: subscription, Valid: true}})
}
func (w EventWorker) applyCheckoutEvent(ctx context.Context, tx pgx.Tx, event queries.DonorStripeEvent, data payments.Object, draft queries.DonorCheckout, attempt queries.DonorStripeCheckout, now time.Time) error {
	q := queries.New(tx)
	kind := event.EventType
	if strings.HasPrefix(kind, "checkout.session.") {
		if attempt.StripeSessionID.Valid && attempt.StripeSessionID.String != data.Text("id") {
			return payments.ErrFacts
		}
		status := attempt.Status
		if kind == "checkout.session.expired" {
			if data.Text("status") != "expired" {
				return payments.ErrFacts
			}
			if status != "COMPLETE" && status != "CANCELLED" {
				status = "EXPIRED"
				if err := q.ReleaseReservation(ctx, attempt.ID); err != nil {
					return err
				}
			}
		} else {
			if data.Text("status") != "complete" {
				return payments.ErrFacts
			}
			if status != "CANCELLED" {
				status = "COMPLETE"
			}
			if subscription := data.Text("subscription"); subscription != "" {
				if err := bindSubscription(ctx, q, attempt, subscription); err != nil {
					return err
				}
			}
		}
		return q.RecordCheckoutSession(ctx, queries.RecordCheckoutSessionParams{ID: attempt.ID, StripeSessionID: pgtype.Text{String: data.Text("id"), Valid: true}, Status: status})
	}
	if strings.HasPrefix(kind, "customer.subscription.") {
		if err := bindSubscription(ctx, q, attempt, data.Text("id")); err != nil {
			return err
		}
		attempt.StripeSubscriptionID = pgtype.Text{String: data.Text("id"), Valid: true}
		_, err := reconcileSubscription(ctx, tx, w.Gateway, draft, attempt, now)
		return err
	}
	expected := draft.AmountMinor
	if strings.HasPrefix(kind, "invoice.") {
		pending, err := q.PendingAmountChange(ctx, draft.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if pending {
			if err = bindSubscription(ctx, q, attempt, data.Text("subscription")); err != nil {
				return err
			}
			attempt.StripeSubscriptionID = pgtype.Text{String: data.Text("subscription"), Valid: true}
			if _, err = reconcileSubscription(ctx, tx, w.Gateway, draft, attempt, now); err != nil {
				return err
			}
		}
		start, _, err := invoiceBounds(data)
		if err != nil {
			return err
		}
		amount, err := q.InvoiceAmountChange(ctx, queries.InvoiceAmountChangeParams{CheckoutID: draft.ID, EffectiveAt: timestamp(start)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			expected = amount
		}
	}
	if kind == "invoice.payment_failed" {
		amount, err := data.Int("amount_due")
		if err != nil || draft.Frequency != "MONTHLY" || data.Text("status") != "open" || data.Text("currency") != "usd" || amount != expected || !billingReason(data) {
			return payments.ErrFacts
		}
		if err = bindSubscription(ctx, q, attempt, data.Text("subscription")); err != nil {
			return err
		}
		_, err = recordPeriod(ctx, q, draft, data, expected, false)
		return err
	}
	var amount int64
	var paid time.Time
	var paymentIntent string
	var period pgtype.UUID
	var err error
	switch kind {
	case "payment_intent.succeeded":
		if draft.Frequency != "ONE_TIME" || data.Text("status") != "succeeded" {
			return payments.ErrFacts
		}
		amount, err = data.Int("amount_received")
		if err != nil {
			return err
		}
		paid = event.OccurredAt.Time
		paymentIntent = data.Text("id")
	case "invoice.payment_succeeded":
		before, e1 := data.IntDefault("starting_balance")
		after, e2 := data.IntDefault("ending_balance")
		total, e3 := data.Int("total")
		due, e4 := data.Int("amount_due")
		amount, err = data.Int("amount_paid")
		if draft.Frequency != "MONTHLY" || data.Text("status") != "paid" || !billingReason(data) || e1 != nil || e2 != nil || e3 != nil || e4 != nil || err != nil || before != 0 || after != 0 || amount != total || amount != due {
			return payments.ErrFacts
		}
		if err = bindSubscription(ctx, q, attempt, data.Text("subscription")); err != nil {
			return err
		}
		epoch, err := data.Int("paid_at")
		if err != nil || epoch < 0 || epoch > 253402300799 {
			return payments.ErrFacts
		}
		paid = time.Unix(epoch, 0).UTC()
		paymentIntent, err = invoicePaymentIntent(data, amount)
		if err != nil {
			return err
		}
		period, err = recordPeriod(ctx, q, draft, data, expected, true)
		if err != nil {
			return err
		}
	default:
		return payments.ErrFacts
	}
	if amount != expected || amount <= 0 || data.Text("currency") != "usd" {
		return payments.ErrFacts
	}
	donor, err := q.LockDonor(ctx, draft.DonorID)
	if err != nil {
		return err
	}
	hadGift, err := q.HasGift(ctx, donor.ID)
	if err != nil {
		return err
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	pi := pgtype.Text{String: paymentIntent, Valid: true}
	if err = q.InsertGift(ctx, queries.InsertGiftParams{ID: id, DonorID: donor.ID, CheckoutID: draft.ID, StripeObjectID: data.Text("id"), AmountMinor: amount, Frequency: draft.Frequency, PaidAt: timestamp(paid), StripePaymentIntentID: pi, PeriodID: period}); err != nil {
		return err
	}
	gift, err := q.GiftByStripeObject(ctx, data.Text("id"))
	if err != nil {
		return err
	}
	if gift.DonorID != donor.ID || gift.CheckoutID != draft.ID || gift.AmountMinor != amount || gift.Frequency != draft.Frequency || (gift.StripePaymentIntentID.Valid && gift.StripePaymentIntentID != pi) || (gift.PeriodID.Valid && gift.PeriodID != period) {
		return payments.ErrFacts
	}
	if err = q.EnrichGift(ctx, queries.EnrichGiftParams{ID: gift.ID, StripePaymentIntentID: pi, PeriodID: period}); err != nil {
		return err
	}
	if !hadGift && !donor.ClaimedAt.Valid {
		return QueueLoginEmailInTx(ctx, tx, donor.Email, now)
	}
	return nil
}
func billingReason(data payments.Object) bool {
	return data.Text("billing_reason") == "subscription_create" || data.Text("billing_reason") == "subscription_cycle"
}
func invoiceBounds(data payments.Object) (time.Time, time.Time, error) {
	lines, err := data.Array("lines")
	if err != nil || !data.BoolIs("lines_complete", true) || len(lines) != 1 {
		return time.Time{}, time.Time{}, payments.ErrFacts
	}
	bounds, err := lines[0].Object("period")
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start, e1 := bounds.Int("start")
	end, e2 := bounds.Int("end")
	if e1 != nil || e2 != nil || start < 0 || end > 253402300799 || end < start || end-start < 27*86400 || end-start > 32*86400 {
		return time.Time{}, time.Time{}, payments.ErrFacts
	}
	return time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC(), nil
}
func recordPeriod(ctx context.Context, q *queries.Queries, draft queries.DonorCheckout, data payments.Object, expected int64, paid bool) (pgtype.UUID, error) {
	empty := pgtype.UUID{}
	start, end, err := invoiceBounds(data)
	if err != nil {
		return empty, err
	}
	lines, err := data.Array("lines")
	if err != nil {
		return empty, err
	}
	line := lines[0]
	amount, err := line.Int("amount")
	if err != nil || amount != expected || line.Text("parent_type") != "subscription_item_details" || !line.BoolIs("proration", false) || line.Text("subscription") != data.Text("subscription") || line.Text("currency") != "usd" {
		return empty, payments.ErrFacts
	}
	id, err := NewID()
	if err != nil {
		return empty, err
	}
	if err = q.InsertBillingPeriod(ctx, queries.InsertBillingPeriodParams{ID: id, DonorID: draft.DonorID, CheckoutID: draft.ID, StripeInvoiceID: data.Text("id"), StripeSubscriptionID: data.Text("subscription"), DueAt: timestamp(start), EndsAt: timestamp(end)}); err != nil {
		return empty, err
	}
	p, err := q.BillingPeriodByInvoice(ctx, data.Text("id"))
	if err != nil {
		return empty, err
	}
	if p.CheckoutID != draft.ID || p.StripeSubscriptionID != data.Text("subscription") || !p.DueAt.Time.Equal(start) || !p.EndsAt.Time.Equal(end) {
		return empty, payments.ErrFacts
	}
	if paid {
		if err = q.MarkPeriodPaid(ctx, p.ID); err != nil {
			return empty, err
		}
	}
	return p.ID, nil
}
func invoicePaymentIntent(data payments.Object, amount int64) (string, error) {
	rows, err := data.Array("payments")
	if err != nil || !data.BoolIs("payments_complete", true) || len(rows) != 1 {
		return "", payments.ErrFacts
	}
	p := rows[0]
	paid, err := p.Int("amount_paid")
	if err != nil || paid != amount || p.Text("status") != "paid" || p.Text("currency") != "usd" || p.Text("invoice") != data.Text("id") || !p.BoolIs("livemode", false) || p.Text("type") != "payment_intent" || !strings.HasPrefix(p.Text("payment_intent"), "pi_") {
		return "", payments.ErrFacts
	}
	return p.Text("payment_intent"), nil
}
