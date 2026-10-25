-- name: DonorBadges :many
SELECT code, source, earned_at, revoked_at FROM donor_badges
WHERE donor_id=$1
ORDER BY earned_at, code;

-- name: EarnBadge :execrows
-- Records an auto-earned badge. Permanent: if the row already exists it is left
-- as-is (first-earned time wins); a previously revoked badge is NOT silently
-- re-earned here (a revoke is an admin decision, honoured until an admin grants).
-- Returns rows affected so the caller fires the badge-earned push only for a
-- genuinely new earn.
INSERT INTO donor_badges (donor_id, code, source, earned_at)
VALUES ($1, $2, 'AUTO', $3)
ON CONFLICT (donor_id, code) DO NOTHING;

-- name: BadgeEarned :one
-- True if the donor currently holds this badge (earned and not revoked), for
-- rechecking a badge-earned push at send time (an admin may revoke in between).
SELECT EXISTS(
    SELECT 1 FROM donor_badges WHERE donor_id=$1 AND code=$2 AND revoked_at IS NULL
);

-- name: QueueBadgePush :exec
-- Queues a badge-earned push (§6.11) for the donor, keyed so it fires at most
-- once per badge, and only while the donor has a live app device so nothing
-- accumulates for a donor without the app.
INSERT INTO donor_push_notifications(id,donor_id,category,dedup_key,context,available_at,expires_at)
SELECT gen_random_uuid(),sqlc.arg(donor_id)::uuid,'BADGE_EARNED',
    'push:badge:'||sqlc.arg(donor_id)::text||':'||sqlc.arg(code)::text,
    json_build_object('code',sqlc.arg(code)::text),
    sqlc.arg(as_of)::timestamptz,sqlc.arg(expires_at)::timestamptz
WHERE EXISTS(SELECT 1 FROM donor_sessions s WHERE s.donor_id=sqlc.arg(donor_id)::uuid AND s.push_token IS NOT NULL
    AND coalesce(s.refresh_expires_at,s.expires_at)>sqlc.arg(as_of)::timestamptz)
ON CONFLICT(dedup_key) DO NOTHING;

-- name: GrantBadge :exec
-- Admin grant (B-4). Clears any revocation and marks the source as GRANT.
INSERT INTO donor_badges (donor_id, code, source, earned_at)
VALUES ($1, $2, 'GRANT', $3)
ON CONFLICT (donor_id, code) DO UPDATE SET source='GRANT', revoked_at=NULL;

-- name: RevokeBadge :exec
-- Admin revoke (B-4): tombstone the row rather than delete it, for audit.
UPDATE donor_badges SET revoked_at=$3 WHERE donor_id=$1 AND code=$2;

-- name: DonorRaisedAmount :one
-- True if the donor has ever confirmed an amount increase (Level Up badge).
SELECT EXISTS (
  SELECT 1 FROM donor_amount_changes
  WHERE donor_id=$1 AND confirmed_at IS NOT NULL AND amount_minor > previous_minor
)::boolean AS raised;
