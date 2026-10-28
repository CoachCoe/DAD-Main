-- Phone/SMS sign-in and Apple/Google sign-in (PRD §5.1). Phase 1.3.
--
-- donor_phone_codes holds one pending SMS verification per phone: the code is
-- stored only as a hash, with an expiry and an attempt counter so a code cannot
-- be brute-forced or replayed. A new request for the same phone overwrites the
-- pending code (last request wins), mirroring the login-link single-use model.
CREATE TABLE donor_phone_codes (
	phone VARCHAR(20) NOT NULL,
	code_hash VARCHAR(64) NOT NULL,
	attempts INTEGER DEFAULT 0 NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (phone),
	CONSTRAINT ck_donor_phone_codes_phone CHECK (phone ~ '^\+[1-9][0-9]{1,14}$'),
	CONSTRAINT ck_donor_phone_codes_attempts CHECK (attempts BETWEEN 0 AND 100)
);

CREATE INDEX ix_donor_phone_codes_expires_at ON donor_phone_codes (expires_at);

-- donor_oidc_identities links a verified external identity (Apple or Google) to
-- a donor. (provider, subject) is the external identity's stable id and is
-- unique, so one Apple/Google account maps to exactly one donor; a donor may
-- hold several identities (one Apple, one Google). The subject is the provider's
-- opaque 'sub' claim, never an email, which can change.
CREATE TABLE donor_oidc_identities (
	id UUID NOT NULL,
	donor_id UUID NOT NULL,
	provider VARCHAR(16) NOT NULL,
	subject VARCHAR(255) NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	last_used_at TIMESTAMP WITH TIME ZONE,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_oidc_provider CHECK (provider IN ('APPLE', 'GOOGLE')),
	FOREIGN KEY (donor_id) REFERENCES donors (id),
	UNIQUE (provider, subject)
);

CREATE INDEX ix_donor_oidc_identities_donor_id ON donor_oidc_identities (donor_id);
