-- Processor creation and local observation can fall on different days.

-- name: ReconciliationGifts :many
SELECT stripe_object_id, stripe_payment_intent_id, amount_minor, currency, paid_at
FROM donor_gifts
WHERE (paid_at >= sqlc.arg(start)::timestamptz AND paid_at < sqlc.arg(end_at)::timestamptz)
OR stripe_payment_intent_id=ANY(sqlc.arg(payment_intents)::text[])
OR stripe_object_id=ANY(sqlc.arg(stripe_objects)::text[])
ORDER BY paid_at, stripe_object_id LIMIT 10001;

-- name: ReconciliationRefunds :many
SELECT stripe_refund_id, amount_minor, currency, status, observed_at
FROM donor_refunds
WHERE (observed_at >= sqlc.arg(start)::timestamptz AND observed_at < sqlc.arg(end_at)::timestamptz)
OR stripe_refund_id=ANY(sqlc.arg(stripe_refunds)::text[])
OR status IN ('PENDING','REQUIRES_ACTION')
ORDER BY observed_at, stripe_refund_id LIMIT 10001;
