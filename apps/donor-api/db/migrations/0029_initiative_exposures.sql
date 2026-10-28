-- Daily initiative rotation exposure ledger (PRD §8 A-1/A-4, Badge #20). Phase 2.4.
--
-- The daily rotation shows each donor one initiative per day, chosen
-- deterministically from the published, active set by hashing
-- (donor_id, donor-local date) — a cheap lookup over a small set, never a scan
-- of all initiatives (PRD §13 risk #6: this path must stay O(active set) at 1M
-- donors, and the active set is small). The CHOICE is not stored: it is a pure
-- function of (donor, date, active set), so it needs no per-donor-per-day row
-- and the table does not grow with days.
--
-- What IS stored is the distinct set of initiatives a donor has ever been shown,
-- one row per (donor, initiative). Badge #20 ("saw N distinct initiatives") is
-- then a cheap COUNT over this donor's rows, and re-showing an initiative is an
-- idempotent upsert that bumps last_shown_on without adding a row. first_shown_on
-- records when the donor first saw it; last_shown_on, the most recent day.
CREATE TABLE donor_initiative_exposures (
	donor_id UUID NOT NULL,
	initiative_id UUID NOT NULL,
	first_shown_on DATE NOT NULL,
	last_shown_on DATE NOT NULL,
	PRIMARY KEY (donor_id, initiative_id),
	FOREIGN KEY (donor_id) REFERENCES donors (id) ON DELETE CASCADE,
	FOREIGN KEY (initiative_id) REFERENCES donor_initiatives (id) ON DELETE CASCADE
);

-- Badge #20 counts a donor's distinct exposures; the PK's leading donor_id
-- column already serves that COUNT, so no extra index is needed.
