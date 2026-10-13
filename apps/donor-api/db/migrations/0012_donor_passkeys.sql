-- Donor passkeys: sign in without an email link.

CREATE TABLE donor_passkeys (
	id UUID NOT NULL,
	donor_id UUID NOT NULL,
	credential_id BYTEA NOT NULL,
	public_key BYTEA NOT NULL,
	sign_count BIGINT NOT NULL,
	transports VARCHAR(16)[] NOT NULL,
	aaguid BYTEA NOT NULL,
	attestation_format VARCHAR(32) NOT NULL,
	user_verified BOOLEAN NOT NULL,
	backup_eligible BOOLEAN NOT NULL,
	backup_state BOOLEAN NOT NULL,
	last_used_at TIMESTAMP WITH TIME ZONE,
	name VARCHAR(64),
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_passkeys_sign_count CHECK (sign_count BETWEEN 0 AND 4294967295),
	FOREIGN KEY (donor_id) REFERENCES donors (id),
	UNIQUE (credential_id)
);

CREATE INDEX ix_donor_passkeys_donor_id ON donor_passkeys (donor_id);

-- A ceremony holds one WebAuthn challenge between options and the browser's answer.
-- SIGN_IN: donor_id IS NULL (usernameless discovery).
-- REGISTER: donor_id IS NOT NULL (authenticated registration).
CREATE TABLE donor_ceremonies (
	token_hash VARCHAR(64) NOT NULL,
	kind VARCHAR(16) NOT NULL,
	donor_id UUID,
	session JSONB NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (token_hash),
	CONSTRAINT ck_donor_ceremonies_kind
		CHECK ((kind = 'SIGN_IN' AND donor_id IS NULL)
			OR (kind = 'REGISTER' AND donor_id IS NOT NULL)),
	FOREIGN KEY (donor_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_ceremonies_expires_at ON donor_ceremonies (expires_at);
CREATE INDEX ix_donor_ceremonies_donor_id   ON donor_ceremonies (donor_id);
