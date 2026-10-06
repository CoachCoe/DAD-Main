-- name: SavePreference :exec
INSERT INTO donor_preferences (id,donor_id,version,referral_emails,marketing_emails)
VALUES ($1,$2,$3,$4,$5) ON CONFLICT(donor_id) DO UPDATE
SET version=EXCLUDED.version,referral_emails=EXCLUDED.referral_emails,marketing_emails=EXCLUDED.marketing_emails;

-- name: RecordPreferenceConsent :exec
INSERT INTO donor_preference_events(id,donor_id,version,referral_emails,marketing_emails,consent_version,source)
VALUES($1,$2,$3,$4,$5,'r1-notifications-v1','account');

-- name: RevokeAllSessions :exec
UPDATE donor_sessions SET revoked_at=$2 WHERE donor_id=$1 AND revoked_at IS NULL;

-- name: SaveSharing :one
UPDATE donors SET public_name=$2,referral_code=$3 WHERE id=$1 RETURNING *;

-- name: ReferralTouchByToken :one
SELECT * FROM donor_referral_touches WHERE token_hash=$1 FOR UPDATE;

-- name: ReferrerByCode :one
SELECT * FROM donors WHERE referral_code=$1 AND claimed_at IS NOT NULL AND public_name IS NOT NULL AND disabled=false;

-- name: InsertReferralTouch :exec
INSERT INTO donor_referral_touches (id,token_hash,referrer_id,kind,touched_at,expires_at) VALUES($1,$2,$3,$4,$5,$6);

-- name: CapturedReferrerName :one
SELECT d.public_name FROM donors d JOIN donor_referral_touches t ON t.referrer_id=d.id
WHERE t.token_hash=$1 AND d.disabled=false;

-- name: DonorGiftsPage :many
SELECT g.id,g.amount_minor,g.currency,g.frequency,g.paid_at,r.refunded_minor,
(g.amount_minor-r.refunded_minor)::bigint AS net_minor
FROM donor_gifts g CROSS JOIN LATERAL (
 SELECT coalesce(sum(amount_minor),0)::bigint AS refunded_minor FROM donor_refunds
 WHERE gift_id=g.id AND status='SUCCEEDED'
) r
WHERE g.donor_id=$1
ORDER BY g.paid_at DESC,g.id DESC LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;
