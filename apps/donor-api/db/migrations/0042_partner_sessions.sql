-- Partner portal sessions + invites (PRD §7.1 P-4, §7.2). Phase 5.2.
--
-- The partner portal has its OWN session, separate from donor and staff auth: a
-- partner user signs in through an emailed invite/sign-in link, exchanges the
-- link token for a session token, and authenticates with it. A partner session
-- is scoped to one partner user (and thus one partner org and role), and can
-- never donate (P-5) — it is not a donor session.
--
-- invite_token_hash + invite_expires_at on donor_partner_users carry the
-- pending email link; it is cleared (accepted_at set) when the user first signs
-- in. token_hash on donor_partner_sessions is the SHA-256 of the bearer token,
-- the same scheme as donor_staff_sessions.
ALTER TABLE donor_partner_users
	ADD COLUMN invite_token_hash VARCHAR(64),
	ADD COLUMN invite_expires_at TIMESTAMP WITH TIME ZONE;

CREATE UNIQUE INDEX uq_donor_partner_users_invite
	ON donor_partner_users (invite_token_hash)
	WHERE invite_token_hash IS NOT NULL;

CREATE TABLE donor_partner_sessions (
	id UUID NOT NULL,
	partner_user_id UUID NOT NULL,
	token_hash VARCHAR(64) NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	revoked_at TIMESTAMP WITH TIME ZONE,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (token_hash),
	FOREIGN KEY (partner_user_id) REFERENCES donor_partner_users (id) ON DELETE CASCADE
);

CREATE INDEX ix_donor_partner_sessions_user ON donor_partner_sessions (partner_user_id);
