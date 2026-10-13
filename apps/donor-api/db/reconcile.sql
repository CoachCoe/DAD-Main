-- Read-only queries for the daily Stripe reconciliation job (cmd/reconcile).
-- Both are keyed on the window the job reconciles: gifts by paid_at (the
-- successful charge's time), refunds by observed_at. They return ids and the
-- fields the diff compares, never donor identifiers or payloads.

-- name: GiftsPaidBetween :many
SELECT stripe_object_id, stripe_payment_intent_id, amount_minor, currency, paid_at
FROM donor_gifts
WHERE paid_at >= sqlc.arg(start)::timestamptz AND paid_at < sqlc.arg(end_at)::timestamptz
ORDER BY paid_at, stripe_object_id;

-- name: RefundsObservedBetween :many
SELECT stripe_refund_id, amount_minor, status, observed_at
FROM donor_refunds
WHERE observed_at >= sqlc.arg(start)::timestamptz AND observed_at < sqlc.arg(end_at)::timestamptz
ORDER BY observed_at, stripe_refund_id;
