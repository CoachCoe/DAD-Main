-- name: DonorBadges :many
SELECT code, source, earned_at, revoked_at FROM donor_badges
WHERE donor_id=$1
ORDER BY earned_at, code;

-- name: EarnBadge :exec
-- Records an auto-earned badge. Permanent: if the row already exists it is left
-- as-is (first-earned time wins); a previously revoked badge is NOT silently
-- re-earned here (a revoke is an admin decision, honoured until an admin grants).
INSERT INTO donor_badges (donor_id, code, source, earned_at)
VALUES ($1, $2, 'AUTO', $3)
ON CONFLICT (donor_id, code) DO NOTHING;

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
