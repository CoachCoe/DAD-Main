-- Staff sign in with passkeys instead of a password and authenticator codes.
-- Earlier credentials, sessions, and invitations end here: every staff member
-- enrols again through a recovery invitation on the staff host.
CREATE TABLE donor_staff_passkeys (
	staff_id UUID NOT NULL,
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
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_staff_passkeys_sign_count CHECK (sign_count BETWEEN 0 AND 4294967295),
	FOREIGN KEY(staff_id) REFERENCES donor_staff (id),
	UNIQUE (credential_id)
);

CREATE INDEX ix_donor_staff_passkeys_staff_id ON donor_staff_passkeys (staff_id);

-- A ceremony holds one WebAuthn challenge between its options and the
-- browser's answer. It is deleted when answered, so it can be used once.
CREATE TABLE donor_staff_ceremonies (
	token_hash VARCHAR(64) NOT NULL,
	kind VARCHAR(16) NOT NULL,
	invite_id UUID,
	session JSONB NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (token_hash),
	CONSTRAINT ck_donor_staff_ceremonies_kind CHECK ((kind = 'SIGN_IN' AND invite_id IS NULL) OR (kind = 'ACTIVATION' AND invite_id IS NOT NULL)),
	FOREIGN KEY(invite_id) REFERENCES donor_staff_invites (id)
);

CREATE INDEX ix_donor_staff_ceremonies_expires_at ON donor_staff_ceremonies (expires_at);
CREATE INDEX ix_donor_staff_ceremonies_invite_id ON donor_staff_ceremonies (invite_id);

ALTER TABLE donor_staff
	DROP COLUMN password_hash,
	DROP COLUMN totp_secret,
	DROP COLUMN totp_counter,
	DROP COLUMN failures,
	DROP COLUMN locked_until;

ALTER TABLE donor_staff_invites DROP COLUMN pending_secret;

UPDATE donor_staff_sessions SET revoked_at = now() WHERE revoked_at IS NULL;
UPDATE donor_staff_invites SET consumed_at = now() WHERE consumed_at IS NULL;
