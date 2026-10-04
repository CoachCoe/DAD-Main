-- name: PublishedContent :one
SELECT p.id, p.created_at, p.payload
FROM donor_content_publications p
JOIN donor_content_head h ON h.publication_id = p.id
WHERE h.id = 'r1';

-- name: AuthenticatedDonor :one
SELECT d.* FROM donors d JOIN donor_sessions s ON s.donor_id = d.id
WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2
AND d.disabled = false AND d.claimed_at IS NOT NULL;

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

-- name: InsertSession :exec
INSERT INTO donor_sessions (id, donor_id, token_hash, expires_at) VALUES ($1, $2, $3, $4);

-- name: RevokeSession :exec
UPDATE donor_sessions SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL;
