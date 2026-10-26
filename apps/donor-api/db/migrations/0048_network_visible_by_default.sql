-- Network visibility is opt-out (PRD §10 Privacy, N-3): a donor someone
-- invited is listed to them by first name and last initial unless they turn
-- it off. It defaulted to off. Donors who never recorded a choice take the
-- new default; a recorded choice is kept.
ALTER TABLE donor_preferences ALTER COLUMN network_visible SET DEFAULT true;

UPDATE donor_preferences p
SET network_visible = true
WHERE NOT p.network_visible
  AND NOT EXISTS (
    SELECT 1 FROM donor_preference_events e
    WHERE e.donor_id = p.donor_id AND e.network_visible IS NOT NULL
  );
