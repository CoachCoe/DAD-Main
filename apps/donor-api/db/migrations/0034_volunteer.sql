-- Volunteer opt-in + interests (PRD §6.10 E-1). Phase 4.1.
--
-- A donor turns on "I volunteer" and picks interests; this puts them in the
-- volunteer pool and turns on event alerts. Both belong to the VERSIONED
-- preference record (the donor can change them and each change is consent-
-- logged), so they live on donor_preferences AND on the append-only
-- donor_preference_events mirror -- the same shape every other preference
-- column follows. volunteer defaults off (opt-IN); interests is an empty set
-- until chosen, stored as text[] the way initiatives.counties already is (a
-- small, read-mostly tag set, not its own table).
ALTER TABLE donor_preferences
	ADD COLUMN volunteer BOOLEAN NOT NULL DEFAULT false,
	ADD COLUMN volunteer_interests TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE donor_preference_events
	ADD COLUMN volunteer BOOLEAN,
	ADD COLUMN volunteer_interests TEXT[];
