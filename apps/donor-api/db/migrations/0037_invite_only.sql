-- Invite-only event gating (PRD §6.10 E-5). Phase 4.4.
--
-- An INVITE_ONLY event is visible only to donors who either hold the qualifying
-- badge's perk or are on an admin-maintained guest list; it is marked
-- "Invitation earned at ..." in the app. The two gates are independent (OR):
--
-- qualifying_perk names the badge PERK that unlocks the event (the badges
-- package's Perk value, e.g. 'FOUNDING_EVENTS'), not a single badge code, so a
-- Founding Donor Dinner is reached by any badge carrying FOUNDING_EVENTS. It is
-- nullable: an INVITE_ONLY event with no perk is guest-list-only, and a
-- non-invite event ignores it. The donor "holds" the perk when they hold any
-- earned, non-revoked badge whose catalog entry carries that perk.
ALTER TABLE donor_events ADD COLUMN qualifying_perk VARCHAR(40) NOT NULL DEFAULT '';

-- donor_event_guests is the admin guest list: an explicit invitation to a
-- specific donor for a specific event, independent of any badge. One row per
-- (event, donor). Rows cascade with the event and the donor.
CREATE TABLE donor_event_guests (
	id UUID NOT NULL,
	event_id UUID NOT NULL,
	donor_id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (event_id, donor_id),
	FOREIGN KEY (event_id) REFERENCES donor_events (id) ON DELETE CASCADE,
	FOREIGN KEY (donor_id) REFERENCES donors (id) ON DELETE CASCADE
);
