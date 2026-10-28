-- Team Captains reserve event spots 48 hours early (PRD B-3, Appendix A). Staff
-- may set when booking opens; until then only Team Captains can reserve, from
-- 48 hours before it. Unset, booking opens when the event is published.
ALTER TABLE donor_events ADD COLUMN booking_opens_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE donor_events ADD CONSTRAINT ck_donor_events_booking_opens CHECK (booking_opens_at < starts_at);
