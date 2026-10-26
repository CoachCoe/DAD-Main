-- name: InsertInitiative :exec
INSERT INTO donor_initiatives
  (id, slug, title, summary, type, location, region, counties, started_on, status, committed_minor, partner_signup_id, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13);

-- name: UpdateInitiative :exec
UPDATE donor_initiatives
SET title=$2, summary=$3, type=$4, location=$5, region=$6, counties=$7,
    started_on=$8, status=$9, committed_minor=$10, partner_signup_id=$11, updated_at=$12
WHERE id=$1;

-- name: PublishInitiative :exec
UPDATE donor_initiatives
SET publish_state='PUBLISHED', published_at=$2, updated_at=$2
WHERE id=$1;

-- name: InitiativeByID :one
SELECT * FROM donor_initiatives WHERE id=$1;

-- name: StaffInitiativesPage :many
SELECT * FROM donor_initiatives
ORDER BY created_at DESC, id
LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;

-- name: SetInitiativeRotation :execrows
UPDATE donor_initiatives SET in_rotation=$2, updated_at=$3 WHERE id=$1;

-- name: UpsertInitiativeMetric :exec
INSERT INTO donor_initiative_metrics
  (id, initiative_id, position, label, value, unit, verification_note, verified_on, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
ON CONFLICT (initiative_id, position) DO UPDATE
SET label=EXCLUDED.label, value=EXCLUDED.value, unit=EXCLUDED.unit,
    verification_note=EXCLUDED.verification_note, verified_on=EXCLUDED.verified_on,
    updated_at=EXCLUDED.updated_at;

-- name: PublishInitiativeMetric :exec
UPDATE donor_initiative_metrics
SET publish_state='PUBLISHED', updated_at=$3
WHERE initiative_id=$1 AND position=$2;

-- name: DeleteInitiativeMetric :exec
DELETE FROM donor_initiative_metrics WHERE initiative_id=$1 AND position=$2;

-- name: InitiativeMetrics :many
SELECT * FROM donor_initiative_metrics
WHERE initiative_id=$1
ORDER BY position;

-- name: PublishedInitiativeMetrics :many
SELECT * FROM donor_initiative_metrics
WHERE initiative_id=$1 AND publish_state='PUBLISHED'
ORDER BY position;

-- name: UpsertInitiativeSpend :exec
INSERT INTO donor_initiative_spend (id, initiative_id, category, amount_minor, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$5)
ON CONFLICT (initiative_id, category) DO UPDATE
SET amount_minor=EXCLUDED.amount_minor, updated_at=EXCLUDED.updated_at;

-- name: DeleteInitiativeSpend :exec
DELETE FROM donor_initiative_spend WHERE initiative_id=$1 AND category=$2;

-- name: InitiativeSpend :many
SELECT category, amount_minor FROM donor_initiative_spend
WHERE initiative_id=$1
ORDER BY category;

-- name: InitiativeSpendTotal :one
SELECT COALESCE(SUM(amount_minor), 0)::bigint AS total_minor
FROM donor_initiative_spend WHERE initiative_id=$1;

-- name: RotatableInitiatives :many
-- The published, active set the daily rotation chooses from. Ordered stably by
-- (started_on, id) so the deterministic index the application computes is stable
-- across requests. Served by ix_donor_initiatives_rotatable.
SELECT * FROM donor_initiatives
WHERE publish_state='PUBLISHED' AND status='ACTIVE' AND in_rotation
ORDER BY started_on, id;

-- name: RecordInitiativeExposure :exec
INSERT INTO donor_initiative_exposures (donor_id, initiative_id, first_shown_on, last_shown_on)
VALUES ($1,$2,$3,$3)
ON CONFLICT (donor_id, initiative_id) DO UPDATE SET last_shown_on=EXCLUDED.last_shown_on;

-- name: DistinctInitiativeExposures :one
SELECT count(*)::bigint AS shown FROM donor_initiative_exposures WHERE donor_id=$1;
