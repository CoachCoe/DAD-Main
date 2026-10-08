-- name: PublishedContent :one
SELECT p.id, p.created_at, p.payload
FROM donor_content_publications p
JOIN donor_content_head h ON h.publication_id = p.id
WHERE h.id = 'r1';

-- name: DonorByEmail :one
SELECT * FROM donors WHERE email = $1;

-- name: LockDonor :one
SELECT * FROM donors WHERE id = $1 FOR UPDATE;

-- name: InsertGuest :exec
INSERT INTO donors (id, email) VALUES ($1, $2) ON CONFLICT (email) DO NOTHING;

-- name: LoginLink :one
SELECT * FROM donor_login_links WHERE token_hash = $1;

-- name: RedeemLoginLink :one
UPDATE donor_login_links SET consumed_at = $2
WHERE id = $1 AND consumed_at IS NULL AND expires_at > $2 RETURNING id;

-- name: ConsumeDonorLinks :exec
UPDATE donor_login_links SET consumed_at = $2 WHERE donor_id = $1 AND consumed_at IS NULL;

-- name: MarkClaimed :exec
UPDATE donors SET claimed_at = $2 WHERE id = $1 AND claimed_at IS NULL;

-- name: InsertStripeEvent :exec
INSERT INTO donor_stripe_events (id,stripe_event_id,event_type,occurred_at,data)
VALUES ($1,$2,$3,$4,$5) ON CONFLICT (stripe_event_id) DO NOTHING;
