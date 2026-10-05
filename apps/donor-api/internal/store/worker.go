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
	"impactgraph/donor-api/internal/recognition"
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
	now := time.Now().UTC()
	applyErr := w.applyEvent(ctx, work, event, now)
	if applyErr == nil {
		applyErr = work.Commit(ctx)
	}
	if applyErr != nil {
		if err = work.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			return false, err
		}
		if err = q.FailStripeEvent(ctx, queries.FailStripeEventParams{ID: event.ID, LastError: pgtype.Text{String: eventFailure(applyErr), Valid: true}}); err != nil {
			return false, err
		}
	} else if err = q.CompleteStripeEvent(ctx, queries.CompleteStripeEventParams{ID: event.ID, ProcessedAt: timestamp(now)}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// eventFailure classifies a failed event for operators without recording
// processor data that error text may carry.
func eventFailure(err error) string {
	switch {
	case errors.Is(err, payments.ErrCheckoutUnavailable):
		return "PaymentUnavailable"
	case errors.Is(err, pgx.ErrNoRows):
		return "MissingRecord"
	case errors.Is(err, payments.ErrFacts), errors.Is(err, recognition.ErrInvalid):
		return "InvalidFacts"
	}
	return "ProcessingError"
}

func parseID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		return id, payments.ErrFacts
	}
	return id, nil
}

// applyEvent fetches the processor state an event needs before taking the
// donor lock, so a slow processor cannot block that donor's other work.
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
		snapshot, err := q.PaymentUpdateSnapshot(ctx, id)
		if err != nil {
			return err
		}
		gateway, ok := w.Gateway.(payments.PaymentMethodGateway)
		if !ok {
			return payments.ErrCheckoutUnavailable
		}
		facts, err := fetchPaymentUpdate(ctx, q, gateway, snapshot, data.Text("id"), now)
		if err != nil {
			return err
		}
		donorID = snapshot.DonorID
		if _, err = q.LockDonor(ctx, donorID); err != nil {
			return err
		}
		row, err := q.PaymentUpdateByID(ctx, id)
		if err != nil {
			return err
		}
		status, err := applyPaymentUpdate(ctx, q, row, facts, now)
		if err != nil {
			return err
		}
		if !terminalPaymentUpdate(status) {
			return payments.ErrFacts
		}
	case strings.HasPrefix(event.EventType, "refund."):
		payment := pgtype.Text{String: data.Text("payment_intent"), Valid: true}
		snapshot, err := q.GiftSnapshotByPayment(ctx, payment)
		if err != nil {
			return err
		}
		observations, err := q.GiftRefundObservations(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		current, err := w.retrieveRefund(ctx, data.Text("id"))
		if err != nil {
			return err
		}
		donorID = snapshot.DonorID
		if _, err = q.LockDonor(ctx, donorID); err != nil {
			return err
		}
		gift, err := q.GiftByPayment(ctx, payment)
		if err != nil {
			return err
		}
		recorded, err := q.GiftRefundObservations(ctx, gift.ID)
		if err != nil {
			return err
		}
		// Another refund state recorded meanwhile may be newer than the one read.
		if recorded != observations || gift.ID != snapshot.ID {
			if current, err = w.retrieveRefund(ctx, data.Text("id")); err != nil {
				return err
			}
		}
		if err = RecordRefundInTx(ctx, q, gift, current, event.ID, pgtype.UUID{}, now); err != nil {
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
		facts, err := w.prefetch(ctx, q, event, data, draft)
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
		if err = w.applyCheckoutEvent(ctx, tx, event, data, draft, attempt, facts, now); err != nil {
			return err
		}
	}
	_, err = RefreshProjectionInTx(ctx, tx, donorID, now)
	return err
}

func (w EventWorker) retrieveRefund(ctx context.Context, id string) (payments.Object, error) {
	if w.Gateway == nil {
		return nil, payments.ErrCheckoutUnavailable
	}
	current, err := w.Gateway.RetrieveRefund(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.Text("id") != id {
		return nil, payments.ErrFacts
	}
	return current, nil
}

// eventFacts is processor state for a checkout event, fetched before the
// donor lock is taken.
type eventFacts struct {
	charged      time.Time
	subscription *subscriptionSnapshot
}

func (w EventWorker) prefetch(ctx context.Context, q *queries.Queries, event queries.DonorStripeEvent, data payments.Object, draft queries.DonorCheckout) (eventFacts, error) {
	var facts eventFacts
	subscription := data.Text("subscription")
	switch kind := event.EventType; {
	case kind == "payment_intent.succeeded":
		if draft.Frequency != "ONE_TIME" || data.Text("status") != "succeeded" {
			return facts, payments.ErrFacts
		}
		var err error
		facts.charged, err = w.chargeSucceededAt(ctx, data.Text("id"))
		return facts, err
	case strings.HasPrefix(kind, "customer.subscription."):
		subscription = data.Text("id")
	case strings.HasPrefix(kind, "invoice."):
		pending, err := q.PendingAmountChange(ctx, draft.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return facts, err
		}
		if !pending {
			return facts, nil
		}
	default:
		return facts, nil
	}
	if !strings.HasPrefix(subscription, "sub_") {
		return facts, payments.ErrFacts
	}
	attempt, err := q.AttemptSnapshotForCheckout(ctx, draft.ID)
	if err != nil {
		return facts, err
	}
	facts.subscription, err = fetchSubscription(ctx, q, w.Gateway, attempt.ID, subscription)
	return facts, err
}

// chargeSucceededAt returns when a one-time gift's card charge succeeded,
// which is what the Founding Donor cutoff names. The event may be created or
// delivered later.
func (w EventWorker) chargeSucceededAt(ctx context.Context, id string) (time.Time, error) {
	if w.Gateway == nil {
		return time.Time{}, payments.ErrCheckoutUnavailable
	}
	intent, err := w.Gateway.RetrievePaymentIntent(ctx, id)
	if err != nil {
		return time.Time{}, err
	}
	charge, err := intent.Object("latest_charge")
	if err != nil {
		return time.Time{}, err
	}
	created, err := charge.Int("created")
	if err != nil || intent.Text("id") != id || intent.Text("status") != "succeeded" || !intent.BoolIs("livemode", false) || charge.Text("payment_intent") != id || charge.Text("status") != "succeeded" || !charge.BoolIs("paid", true) || !charge.BoolIs("livemode", false) || created < 0 || created > 253402300799 {
		return time.Time{}, payments.ErrFacts
	}
	return time.Unix(created, 0).UTC(), nil
}

func bindSubscription(ctx context.Context, q *queries.Queries, attempt queries.DonorStripeCheckout, subscription string) error {
	if !strings.HasPrefix(subscription, "sub_") || (attempt.StripeSubscriptionID.Valid && attempt.StripeSubscriptionID.String != subscription) {
		return payments.ErrFacts
	}
	return q.BindSubscription(ctx, queries.BindSubscriptionParams{ID: attempt.ID, StripeSubscriptionID: pgtype.Text{String: subscription, Valid: true}})
}
func (w EventWorker) applyCheckoutEvent(ctx context.Context, tx pgx.Tx, event queries.DonorStripeEvent, data payments.Object, draft queries.DonorCheckout, attempt queries.DonorStripeCheckout, facts eventFacts, now time.Time) error {
	q := queries.New(tx)
	kind := event.EventType
	switch {
	case strings.HasPrefix(kind, "checkout.session."):
		return applySessionEvent(ctx, q, kind, data, attempt)
	case strings.HasPrefix(kind, "customer.subscription."):
		if err := bindSubscription(ctx, q, attempt, data.Text("id")); err != nil {
			return err
		}
		attempt.StripeSubscriptionID = pgtype.Text{String: data.Text("id"), Valid: true}
		_, err := reconcileSubscription(ctx, tx, w.Gateway, draft, attempt, facts.subscription, now)
		return err
	}
	expected := draft.AmountMinor
	if strings.HasPrefix(kind, "invoice.") {
		var err error
		if expected, err = w.invoiceAmount(ctx, tx, data, draft, attempt, facts.subscription, now); err != nil {
			return err
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
		period, err := recordPeriod(ctx, q, draft, data, expected, false)
		if err != nil {
			return err
		}
		return recordBillingObservation(ctx, q, period, event, "FAILED", amount, data.Text("billing_reason"), event.OccurredAt.Time, now)
	}
	payment, err := paymentFacts(ctx, q, event, data, draft, attempt, expected, facts.charged)
	if err != nil {
		return err
	}
	if err = recordGift(ctx, tx, draft, payment, now); err != nil {
		return err
	}
	if payment.period.Valid {
		return recordBillingObservation(ctx, q, payment.period, event, "PAID", payment.amount, data.Text("billing_reason"), payment.paid, now)
	}
	return nil
}

func recordBillingObservation(ctx context.Context, q *queries.Queries, period pgtype.UUID, event queries.DonorStripeEvent, status string, amount int64, reason string, occurred, recorded time.Time) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	return q.InsertBillingObservation(ctx, queries.InsertBillingObservationParams{
		ID: id, PeriodID: period, SourceEventID: event.ID, Status: status, AmountMinor: amount,
		BillingReason: reason, OccurredAt: timestamp(occurred), EventCreatedAt: event.OccurredAt, RecordedAt: timestamp(recorded),
	})
}

func applySessionEvent(ctx context.Context, q *queries.Queries, kind string, data payments.Object, attempt queries.DonorStripeCheckout) error {
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

// invoiceAmount returns the amount the invoice's period must charge: a
// confirmed amount change applies from its effective renewal, never prorated.
func (w EventWorker) invoiceAmount(ctx context.Context, tx pgx.Tx, data payments.Object, draft queries.DonorCheckout, attempt queries.DonorStripeCheckout, snapshot *subscriptionSnapshot, now time.Time) (int64, error) {
	q := queries.New(tx)
	pending, err := q.PendingAmountChange(ctx, draft.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if pending {
		if err = bindSubscription(ctx, q, attempt, data.Text("subscription")); err != nil {
			return 0, err
		}
		attempt.StripeSubscriptionID = pgtype.Text{String: data.Text("subscription"), Valid: true}
		if _, err = reconcileSubscription(ctx, tx, w.Gateway, draft, attempt, snapshot, now); err != nil {
			return 0, err
		}
	}
	start, _, err := invoiceBounds(data)
	if err != nil {
		return 0, err
	}
	amount, err := q.InvoiceAmountChange(ctx, queries.InvoiceAmountChangeParams{CheckoutID: draft.ID, EffectiveAt: timestamp(start)})
	if errors.Is(err, pgx.ErrNoRows) {
		return draft.AmountMinor, nil
	}
	return amount, err
}

type paidGift struct {
	objectID      string
	amount        int64
	paid          time.Time
	paymentIntent string
	period        pgtype.UUID
}

func paymentFacts(ctx context.Context, q *queries.Queries, event queries.DonorStripeEvent, data payments.Object, draft queries.DonorCheckout, attempt queries.DonorStripeCheckout, expected int64, charged time.Time) (paidGift, error) {
	out := paidGift{objectID: data.Text("id")}
	var err error
	switch event.EventType {
	case "payment_intent.succeeded":
		if draft.Frequency != "ONE_TIME" || data.Text("status") != "succeeded" {
			return out, payments.ErrFacts
		}
		if out.amount, err = data.Int("amount_received"); err != nil {
			return out, err
		}
		out.paid = charged
		out.paymentIntent = data.Text("id")
	case "invoice.payment_succeeded":
		before, e1 := data.IntDefault("starting_balance")
		after, e2 := data.IntDefault("ending_balance")
		total, e3 := data.Int("total")
		due, e4 := data.Int("amount_due")
		out.amount, err = data.Int("amount_paid")
		if draft.Frequency != "MONTHLY" || data.Text("status") != "paid" || !billingReason(data) || e1 != nil || e2 != nil || e3 != nil || e4 != nil || err != nil || before != 0 || after != 0 || out.amount != total || out.amount != due {
			return out, payments.ErrFacts
		}
		if err = bindSubscription(ctx, q, attempt, data.Text("subscription")); err != nil {
			return out, err
		}
		epoch, err := data.Int("paid_at")
		if err != nil || epoch < 0 || epoch > 253402300799 {
			return out, payments.ErrFacts
		}
		out.paid = time.Unix(epoch, 0).UTC()
		if out.paymentIntent, err = invoicePaymentIntent(data, out.amount); err != nil {
			return out, err
		}
		if out.period, err = recordPeriod(ctx, q, draft, data, expected, true); err != nil {
			return out, err
		}
	default:
		return out, payments.ErrFacts
	}
	if out.amount != expected || out.amount <= 0 || data.Text("currency") != "usd" {
		return out, payments.ErrFacts
	}
	return out, nil
}

// recordGift writes the payment with the checkout's attribution as it stood
// when charged; an expired referral window leaves the gift unattributed.
func recordGift(ctx context.Context, tx pgx.Tx, draft queries.DonorCheckout, p paidGift, now time.Time) error {
	q := queries.New(tx)
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
	var referrer pgtype.UUID
	var kind pgtype.Text
	if draft.ReferrerID.Valid && p.paid.Before(draft.ReferralExpiresAt.Time) {
		referrer, kind = draft.ReferrerID, draft.ReferralKind
	}
	pi := pgtype.Text{String: p.paymentIntent, Valid: true}
	if err = q.InsertGift(ctx, queries.InsertGiftParams{ID: id, DonorID: donor.ID, CheckoutID: draft.ID, StripeObjectID: p.objectID, AmountMinor: p.amount, Frequency: draft.Frequency, PaidAt: timestamp(p.paid), StripePaymentIntentID: pi, PeriodID: p.period, ReferrerID: referrer, ReferralKind: kind}); err != nil {
		return err
	}
	gift, err := q.GiftByStripeObject(ctx, p.objectID)
	if err != nil {
		return err
	}
	if gift.DonorID != donor.ID || gift.CheckoutID != draft.ID || gift.AmountMinor != p.amount || gift.Frequency != draft.Frequency || gift.ReferrerID != referrer || gift.ReferralKind != kind || (gift.StripePaymentIntentID.Valid && gift.StripePaymentIntentID != pi) || (gift.PeriodID.Valid && gift.PeriodID != p.period) {
		return payments.ErrFacts
	}
	if err = q.EnrichGift(ctx, queries.EnrichGiftParams{ID: gift.ID, StripePaymentIntentID: pi, PeriodID: p.period}); err != nil {
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
