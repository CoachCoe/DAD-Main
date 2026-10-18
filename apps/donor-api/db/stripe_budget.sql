-- name: ReserveStripeRequest :one
-- Reserves the bucket's next request when it can start within the caller's
-- wait, returning the seconds until it may; no row means none was reserved.
INSERT INTO donor_stripe_budgets AS b (bucket, next_at)
VALUES (sqlc.arg(bucket), clock_timestamp() + make_interval(secs => sqlc.arg(interval_seconds)::float8))
ON CONFLICT (bucket) DO UPDATE
SET next_at = GREATEST(b.next_at, clock_timestamp()) + make_interval(secs => sqlc.arg(interval_seconds)::float8)
WHERE b.next_at - clock_timestamp() <= make_interval(secs => sqlc.arg(tolerance_seconds)::float8 + sqlc.arg(max_wait_seconds)::float8)
RETURNING GREATEST(0, EXTRACT(EPOCH FROM b.next_at - clock_timestamp())::float8 - sqlc.arg(interval_seconds)::float8 - sqlc.arg(tolerance_seconds)::float8)::float8 AS wait_seconds;
