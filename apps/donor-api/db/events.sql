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

-- name: LockEvent :one
-- Serializes capacity decisions: a reserve/cancel holds this row lock so two
-- donors cannot each take the last seat.
SELECT * FROM donor_events WHERE id=$1 FOR UPDATE;

-- name: ActiveReservationCount :one
SELECT count(*) FROM donor_event_reservations
WHERE event_id=$1 AND status IN ('RESERVED', 'WAITLISTED');

-- name: ReservedCount :one
SELECT count(*) FROM donor_event_reservations
WHERE event_id=$1 AND status='RESERVED';

-- name: ReservedCountsForEvents :many
-- RESERVED count per event, for the near-city list's spots-left without N+1.
SELECT event_id, count(*)::bigint AS reserved
FROM donor_event_reservations
WHERE event_id = ANY(sqlc.arg(event_ids)::uuid[]) AND status='RESERVED'
GROUP BY event_id;

-- name: DonorReservationsForEvents :many
-- The signed-in donor's active reservation status per event, for the list.
SELECT event_id, status FROM donor_event_reservations
WHERE donor_id=$1 AND event_id = ANY(sqlc.arg(event_ids)::uuid[])
  AND status IN ('RESERVED', 'WAITLISTED');

-- name: ReservationFor :one
SELECT * FROM donor_event_reservations WHERE event_id=$1 AND donor_id=$2;

-- name: UpsertReservation :exec
-- Reuses the (event,donor) row when a donor re-reserves after cancelling;
-- reserved_at is only set on the row's first non-cancelled entry (passed in).
INSERT INTO donor_event_reservations (id, event_id, donor_id, status, reserved_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$6)
ON CONFLICT (event_id, donor_id) DO UPDATE
SET status=EXCLUDED.status, reserved_at=EXCLUDED.reserved_at, updated_at=EXCLUDED.updated_at;

-- name: SetReservationStatus :exec
UPDATE donor_event_reservations SET status=$3, updated_at=$4 WHERE event_id=$1 AND donor_id=$2;

-- name: WaitlistHead :one
-- The earliest-queued waitlisted donor for an event, the one promoted when a
-- seat frees (E-4 FIFO).
SELECT * FROM donor_event_reservations
WHERE event_id=$1 AND status='WAITLISTED'
ORDER BY reserved_at ASC, id ASC
LIMIT 1;

-- name: PromoteReservation :exec
UPDATE donor_event_reservations SET status='RESERVED', updated_at=$2 WHERE id=$1;

-- name: QueueEventPush :exec
-- Queues one push notification for a donor about an event (e.g. waitlist
-- promotion). dedup_key makes it idempotent; the worker gates on the donor's
-- category preference and token at send time, so this only enqueues.
INSERT INTO donor_push_notifications (id, donor_id, category, dedup_key, context, available_at, expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (dedup_key) DO NOTHING;

