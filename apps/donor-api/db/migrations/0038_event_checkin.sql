-- Event check-in (PRD §6.10 E-8, EM-8). Phase 4.5.
--
-- A donor checks in at the venue by scanning the event's QR code from inside
-- the app; check-in is accepted only during the event window (starts_at..
-- ends_at) and only for a donor who holds a reservation. It stamps the
-- reservation's checked_in_at, which is what "Events volunteered," volunteer
-- hours (the event's duration summed over checked-in events, EM-8) and the
-- Showed Up / Volunteer ×N badges all count from. Nullable: unset until the
-- donor checks in, set once and not cleared.
ALTER TABLE donor_event_reservations ADD COLUMN checked_in_at TIMESTAMP WITH TIME ZONE;

-- The volunteer-hours / check-in-count aggregates read "this donor's checked-in
-- reservations"; a partial index over the checked-in rows keeps those a lookup.
CREATE INDEX ix_donor_event_reservations_checkin
	ON donor_event_reservations (donor_id)
	WHERE checked_in_at IS NOT NULL;
