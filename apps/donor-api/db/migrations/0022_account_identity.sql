-- Identity foundation (PRD §3, §5.1, §10, gaps D-1/D-2/E-2/K-1). Phase 1.1 + 1.2.
--
-- account_role fixes one principal to one role for its lifetime (§3,
-- one-email-one-role): a row is born DONOR and the application never updates
-- the column, so the role is immutable by construction. PARTNER is admitted by
-- the CHECK now so the partner principal that Phase 5 introduces needs no
-- further schema change; no row carries it yet.
--
-- The profile fields back donor-local time (§10): zip is the donor's input,
-- city is derived for display, and timezone is the IANA zone used to present
-- day-level streak state and schedule the streak-at-risk reminder in the
-- donor's own day. phone backs SMS sign-in (§5.1). All are nullable: existing
-- donors predate collection and a guest row is created before any of this is
-- known.
ALTER TABLE donors ADD COLUMN account_role VARCHAR(16) DEFAULT 'DONOR' NOT NULL;
ALTER TABLE donors ADD COLUMN zip VARCHAR(10);
ALTER TABLE donors ADD COLUMN city VARCHAR(80);
ALTER TABLE donors ADD COLUMN timezone VARCHAR(64);
ALTER TABLE donors ADD COLUMN phone VARCHAR(20);

ALTER TABLE donors ADD CONSTRAINT ck_donor_account_role CHECK (account_role IN ('DONOR', 'PARTNER'));

-- A five- or nine-digit US ZIP, the only form the ZIP->city/timezone lookup
-- accepts; stored normalised (digits, optional hyphen) by the application.
ALTER TABLE donors ADD CONSTRAINT ck_donor_zip CHECK (zip IS NULL OR zip ~ '^[0-9]{5}(-[0-9]{4})?$');

-- timezone must be an IANA name the application resolved via the ZIP lookup, so
-- a non-empty value is never a free-form string. The format guard is a floor;
-- the application only ever writes a name time.LoadLocation accepted.
ALTER TABLE donors ADD CONSTRAINT ck_donor_timezone CHECK (timezone IS NULL OR timezone ~ '^[A-Za-z0-9+_/-]+$');

-- E.164, the form the SMS provider requires.
ALTER TABLE donors ADD CONSTRAINT ck_donor_phone CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{1,14}$');
