-- name: NextStripeEvent :one
SELECT * FROM donor_stripe_events WHERE processed_at IS NULL AND attempts<10
ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED;

-- name: IncrementEventAttempt :exec
UPDATE donor_stripe_events SET attempts=attempts+1 WHERE id=$1;

-- name: CompleteStripeEvent :exec
UPDATE donor_stripe_events SET processed_at=$2,last_error=NULL WHERE id=$1;

-- name: FailStripeEvent :exec
UPDATE donor_stripe_events SET last_error=$2 WHERE id=$1;

-- name: CheckoutDraft :one
SELECT * FROM donor_checkouts WHERE id=$1;

-- name: AttemptForCheckout :one
SELECT * FROM donor_stripe_checkouts WHERE checkout_id=$1 FOR UPDATE;

-- name: GiftOwnerByPayment :one
SELECT donor_id FROM donor_gifts WHERE stripe_payment_intent_id=$1;

-- name: PaymentUpdateOwner :one
SELECT donor_id FROM donor_payment_updates WHERE id=$1;

-- name: BindSubscription :exec
UPDATE donor_stripe_checkouts SET stripe_subscription_id=$2 WHERE id=$1;

-- name: RecordCheckoutSession :exec
UPDATE donor_stripe_checkouts SET stripe_session_id=$2,status=$3 WHERE id=$1;

-- name: ReleaseReservation :exec
DELETE FROM donor_recurring_reservations WHERE attempt_id=$1;

-- name: BillingPeriodByInvoice :one
SELECT * FROM donor_billing_periods WHERE stripe_invoice_id=$1;

-- name: InsertBillingPeriod :exec
INSERT INTO donor_billing_periods (id,donor_id,checkout_id,stripe_invoice_id,stripe_subscription_id,due_at,ends_at,status)
VALUES ($1,$2,$3,$4,$5,$6,$7,'FAILED') ON CONFLICT (stripe_invoice_id) DO NOTHING;

-- name: MarkPeriodPaid :exec
UPDATE donor_billing_periods SET status='PAID' WHERE id=$1;

-- name: HasGift :one
SELECT EXISTS(SELECT 1 FROM donor_gifts WHERE donor_id=$1)::boolean;

-- name: InsertGift :exec
INSERT INTO donor_gifts (id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at,stripe_payment_intent_id,period_id)
VALUES ($1,$2,$3,$4,$5,'USD',$6,$7,$8,$9) ON CONFLICT (stripe_object_id) DO NOTHING;

-- name: GiftByStripeObject :one
SELECT * FROM donor_gifts WHERE stripe_object_id=$1 FOR UPDATE;

-- name: EnrichGift :exec
UPDATE donor_gifts SET stripe_payment_intent_id=$2,period_id=$3 WHERE id=$1;

-- name: InvoiceAmountChange :one
SELECT amount_minor FROM donor_amount_changes WHERE checkout_id=$1 AND confirmed_at IS NOT NULL
AND effective_at<=$2 ORDER BY effective_at DESC,created_at DESC LIMIT 1;

-- name: PendingAmountChange :one
SELECT (confirmed_at IS NULL)::boolean AS pending FROM donor_amount_changes WHERE checkout_id=$1
ORDER BY created_at DESC LIMIT 1;

-- name: GiftByPayment :one
SELECT * FROM donor_gifts WHERE stripe_payment_intent_id=$1 FOR UPDATE;

-- name: RefundByStripeID :one
SELECT * FROM donor_refunds WHERE stripe_refund_id=$1;

-- name: InsertRefund :exec
INSERT INTO donor_refunds (id,gift_id,stripe_refund_id,amount_minor,currency,status,observed_at)
VALUES ($1,$2,$3,$4,'USD',$5,$6) ON CONFLICT (stripe_refund_id) DO NOTHING;

-- name: UpdateRefund :exec
UPDATE donor_refunds SET status=$2,observed_at=$3 WHERE id=$1;

-- name: SucceededRefundAmounts :many
SELECT amount_minor FROM donor_refunds WHERE gift_id=$1 AND status='SUCCEEDED';

-- name: InsertRefundObservation :exec
INSERT INTO donor_refund_observations (id,refund_id,source_event_id,source_action_id,status,observed_at)
VALUES ($1,$2,$3,$4,$5,$6);

-- name: LatestAmountChange :one
SELECT * FROM donor_amount_changes WHERE checkout_id=$1 ORDER BY created_at DESC LIMIT 1;

-- name: ConfirmAmountChange :exec
UPDATE donor_amount_changes SET confirmed_at=COALESCE(confirmed_at,$2) WHERE id=$1;

-- name: LatestPause :one
SELECT * FROM donor_pauses WHERE attempt_id=$1 ORDER BY created_at DESC LIMIT 1;

-- name: ResumptionForPause :one
SELECT * FROM donor_resumptions WHERE pause_id=$1;

-- name: ConfirmPause :exec
UPDATE donor_pauses SET confirmed_at=COALESCE(confirmed_at,$2) WHERE id=$1;

-- name: ConfirmResumption :exec
UPDATE donor_resumptions SET confirmed_at=COALESCE(confirmed_at,$2) WHERE id=$1;

-- name: SaveSubscription :one
INSERT INTO donor_subscriptions (id,attempt_id,donor_id,status,period_end,stops_at,observed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (attempt_id) DO UPDATE SET status=EXCLUDED.status,period_end=EXCLUDED.period_end,
stops_at=EXCLUDED.stops_at,observed_at=EXCLUDED.observed_at RETURNING *;

-- name: CancelAttempt :exec
UPDATE donor_stripe_checkouts SET status='CANCELLED' WHERE id=$1;

-- name: PaymentUpdateByID :one
SELECT * FROM donor_payment_updates WHERE id=$1 FOR UPDATE;

-- name: AttemptByID :one
SELECT * FROM donor_stripe_checkouts WHERE id=$1 FOR UPDATE;

-- name: SavePaymentUpdate :exec
UPDATE donor_payment_updates SET stripe_session_id=$2,status=$3,hosted_url=$4,completed_at=$5 WHERE id=$1;

-- name: RetryStripeEvent :execrows
UPDATE donor_stripe_events SET attempts=0,last_error=NULL WHERE stripe_event_id=$1 AND processed_at IS NULL;
