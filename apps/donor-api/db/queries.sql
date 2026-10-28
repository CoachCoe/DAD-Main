-- name: PublishedContent :one
SELECT p.id, p.created_at, p.payload
FROM donor_content_publications p
JOIN donor_content_head h ON h.publication_id = p.id
WHERE h.id = 'r1';

-- name: DonorByEmail :one
SELECT * FROM donors WHERE email = $1;

-- name: LockDonor :one
-- Serialize donor writes without blocking foreign keys that reference this donor.
SELECT * FROM donors WHERE id = $1 FOR NO KEY UPDATE;

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

-- name: LockEmailIdentity :exec
-- Serializes every check-then-create of an identity for one address, so two
-- concurrent sign-ups cannot both find it free (PRD §3, one email one role).
SELECT pg_advisory_xact_lock(hashtextextended('email-identity:' || sqlc.arg(email)::text, 0));

-- name: EmailIdentities :many
-- Live identities using an address. A deleted donor's address is erased, and a
-- removed partner or staff login no longer holds one.
SELECT 'DONOR'::text AS role, NULL::uuid AS partner_id FROM donors WHERE email = sqlc.arg(email)::text
UNION ALL
SELECT 'STAFF', NULL FROM donor_staff WHERE email = sqlc.arg(email)::text AND NOT disabled
UNION ALL
SELECT 'PARTNER', partner_id FROM donor_partner_users WHERE email = sqlc.arg(email)::text AND NOT disabled;
