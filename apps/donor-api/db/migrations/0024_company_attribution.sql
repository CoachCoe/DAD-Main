-- Company-code attribution (PRD D-3/D-4). Phase 1.4.
--
-- A company code is an employer's code, not a donor's referral code, so it does
-- NOT belong in donor_referral_touches (that table's referrer_id is a NOT NULL
-- FK to donors and its kind is constrained to LINK/IRL for the financial
-- projection path). The partner/employer a company code resolves to does not
-- exist until the partner + employee-giving phases; until then the code is
-- captured and carried as an opaque string, and Phase 6 resolves it to a partner
-- and computes matching. Keeping it separate leaves the donor-referral financial
-- path untouched.
--
-- donor_company_touches captures a pre-signup company-code visit keyed by an
-- anonymous visitor token (like a referral touch), so the attribution survives
-- the app-store install gap. Last touch wins; it expires after a window at least
-- as long as install latency.
CREATE TABLE donor_company_touches (
	token_hash VARCHAR(64) NOT NULL,
	company_code VARCHAR(64) NOT NULL,
	touched_at TIMESTAMP WITH TIME ZONE NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_company_code CHECK (company_code ~ '^[A-Z0-9][A-Z0-9-]{1,63}$'),
	UNIQUE (token_hash)
);

CREATE INDEX ix_donor_company_touches_expires_at ON donor_company_touches (expires_at);

-- company_code is the code bound to the donor once captured attribution is
-- claimed (at first gift). It is a plain string, not yet a partner FK; Phase 6
-- resolves it to an approved partner and drives employer matching. NULL until a
-- donor arrives through a company code.
ALTER TABLE donors ADD COLUMN company_code VARCHAR(64);
ALTER TABLE donors ADD CONSTRAINT ck_donor_company_code_fmt
	CHECK (company_code IS NULL OR company_code ~ '^[A-Z0-9][A-Z0-9-]{1,63}$');
