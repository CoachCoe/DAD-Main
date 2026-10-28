-- Website waitlist sign-ups and earlier website donors are honoured as
-- Founding Donors (D-6). Only a SHA-256 hash of the lower-cased address is
-- kept, never the address. A donor whose email matches an unexpired claim earns
-- Founding Donor at their next projection. Claims expire 90 days after the
-- Founding cutoff and are then deleted.
CREATE TABLE donor_founding_claims (
	email_hash TEXT NOT NULL PRIMARY KEY CHECK (email_hash ~ '^[0-9a-f]{64}$'),
	signed_up_on DATE NOT NULL,
	source TEXT NOT NULL CHECK (source IN ('WAITLIST', 'WEBSITE_DONOR')),
	imported_at TIMESTAMP WITH TIME ZONE NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX ix_donor_founding_claims_expires ON donor_founding_claims (expires_at);
