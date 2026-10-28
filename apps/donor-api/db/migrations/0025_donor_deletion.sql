-- Donor self-service account deletion (PRD §6.12; Apple/Google requirement).
-- Phase 1.6.
--
-- Deletion anonymises the donor in place rather than removing the row: the
-- financial ledger (gifts, checkouts, billing periods, refunds) must be retained
-- for legal/tax reasons and references donor_id. Personal data is scrubbed from
-- the donors row and its personal satellites are removed; the ledger rows, which
-- carry no personal data of their own, stay.
--
-- donor_deletions is the durable audit of the erasure. It must outlive the
-- scrub, so it is its own table (auditing into a table being scrubbed would lose
-- the record) and carries no personal data — only which donor id was deleted and
-- when. The id is retained precisely because the ledger still references it.
CREATE TABLE donor_deletions (
	donor_id UUID NOT NULL,
	deleted_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (donor_id),
	FOREIGN KEY (donor_id) REFERENCES donors (id)
);
