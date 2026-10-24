-- name: InsertEvent :exec
INSERT INTO donor_events
  (id, slug, title, type, description, venue, address, city, zip, starts_at, ends_at, capacity, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13);

-- name: UpdateEvent :exec
UPDATE donor_events
SET title=$2, type=$3, description=$4, venue=$5, address=$6, city=$7, zip=$8,
    starts_at=$9, ends_at=$10, capacity=$11, updated_at=$12
WHERE id=$1;

-- name: PublishEvent :exec
UPDATE donor_events
SET publish_state='PUBLISHED', published_at=$2, updated_at=$2
WHERE id=$1;

-- name: EventByID :one
SELECT * FROM donor_events WHERE id=$1;

-- name: EventBySlug :one
SELECT * FROM donor_events WHERE slug=$1;

-- name: StaffEventsPage :many
SELECT * FROM donor_events
ORDER BY starts_at DESC, id
LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;

-- name: UpcomingEventsNearDonor :many
-- E-2: published events in and near the donor's city (same city label OR same
-- three-digit ZIP region), upcoming only, soonest first. The near-city match
-- is empty-safe: a donor with no city/zip matches nothing on that predicate.
-- INVITE_ONLY events are excluded here; they surface only through the 4.4
-- gated path (qualifying badge or admin guest list), never the open list.
SELECT * FROM donor_events
WHERE publish_state='PUBLISHED'
  AND type <> 'INVITE_ONLY'
  AND starts_at > sqlc.arg(now)::timestamptz
  AND (
    (sqlc.arg(city)::text <> '' AND lower(city) = lower(sqlc.arg(city)::text))
    OR (sqlc.arg(zip3)::text <> '' AND left(zip, 3) = sqlc.arg(zip3)::text)
  )
ORDER BY starts_at ASC, id
LIMIT 100;
