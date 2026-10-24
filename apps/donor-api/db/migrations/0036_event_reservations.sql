-- Event reservations + waitlist (PRD §6.10 E-4, E-9). Phase 4.3.
--
-- A donor reserves a spot at an event; when the event is full the reservation
-- is WAITLISTED instead, and when a RESERVED donor cancels, the head of the
-- waitlist is promoted automatically (E-4). A donor can cancel up to the event
-- start (E-9).
--
-- One row per (event, donor): a donor is either RESERVED, WAITLISTED, or
-- CANCELLED for a given event, never two at once, so the unique constraint is
-- the whole identity and re-reserving after cancelling reuses the row. The
-- active (RESERVED/WAITLISTED) count against capacity is computed from this
-- table under the event row lock, so capacity is enforced race-free without a
-- denormalized counter that could drift.
--
-- reserved_at orders the waitlist (earliest first) and is the promotion order;
-- it is set when the row first enters RESERVED or WAITLISTED and preserved
-- across a promotion so FIFO holds. created_at/updated_at are the row's audit
-- timestamps.
CREATE TABLE donor_event_reservations (
	id UUID NOT NULL,
	event_id UUID NOT NULL,
	donor_id UUID NOT NULL,
	status VARCHAR(16) NOT NULL,
	reserved_at TIMESTAMP WITH TIME ZONE NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (event_id, donor_id),
	CONSTRAINT ck_donor_event_reservations_status CHECK (status IN ('RESERVED', 'WAITLISTED', 'CANCELLED')),
	FOREIGN KEY (event_id) REFERENCES donor_events (id) ON DELETE CASCADE,
	FOREIGN KEY (donor_id) REFERENCES donors (id) ON DELETE CASCADE
);

-- The capacity count and the waitlist head are both "active rows for this
-- event, by reserved_at"; a partial index over the non-cancelled rows serves
-- both without scanning cancellations.
CREATE INDEX ix_donor_event_reservations_active
	ON donor_event_reservations (event_id, reserved_at)
	WHERE status IN ('RESERVED', 'WAITLISTED');

-- Waitlist promotion queues a push (E-4). donor_push_notifications constrains
-- its category set (0011); add the new event category so the promotion push
-- (and the 4.6 EVENT_REMINDER, added in its own migration) can be enqueued.
ALTER TABLE donor_push_notifications DROP CONSTRAINT donor_push_notifications_category_check;
ALTER TABLE donor_push_notifications ADD CONSTRAINT donor_push_notifications_category_check
	CHECK (category IN ('REFERRAL_CONVERTED', 'STREAK_AT_RISK', 'STREAK_MILESTONE', 'EVENT_PROMOTED'));

