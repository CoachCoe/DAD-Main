-- name: SavePreference :exec
INSERT INTO donor_preferences (id,donor_id,version,referral_emails,marketing_emails,
 push_referral_converted,push_streak_at_risk,push_streak_milestone)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(donor_id) DO UPDATE
SET version=EXCLUDED.version,referral_emails=EXCLUDED.referral_emails,marketing_emails=EXCLUDED.marketing_emails,
 push_referral_converted=EXCLUDED.push_referral_converted,push_streak_at_risk=EXCLUDED.push_streak_at_risk,
 push_streak_milestone=EXCLUDED.push_streak_milestone;

-- name: RecordPreferenceConsent :exec
INSERT INTO donor_preference_events(id,donor_id,version,referral_emails,marketing_emails,
 push_referral_converted,push_streak_at_risk,push_streak_milestone,consent_version,source)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'account');

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

-- name: PurgeReferralTouches :many
-- A checkout copies its touch's referrer, kind and expiry exactly, and its gift
-- keeps the referrer only if paid before that expiry, so a closed touch with no
-- such gift never converted. Rows are visited in key order after the cursor so
-- that touches kept for good are passed over once per pass.
WITH deleted AS (
 DELETE FROM donor_referral_touches WHERE id=ANY(ARRAY(
  SELECT t.id FROM donor_referral_touches t
  WHERE (t.touched_at,t.id)>(sqlc.arg(after_touched_at)::timestamptz,sqlc.arg(after_id)::uuid)
  AND t.touched_at<sqlc.arg(touched_before)::timestamptz AND t.expires_at<=sqlc.arg(as_of)::timestamptz
  AND NOT EXISTS(SELECT 1 FROM donor_checkouts c JOIN donor_gifts g ON g.checkout_id=c.id
   WHERE c.referrer_id=t.referrer_id AND c.referral_expires_at=t.expires_at AND c.referral_kind=t.kind
   AND g.referrer_id IS NOT NULL)
  ORDER BY t.touched_at,t.id LIMIT sqlc.arg(batch) FOR UPDATE OF t SKIP LOCKED
 )) RETURNING touched_at,id
)
SELECT touched_at,id FROM deleted ORDER BY touched_at,id;
