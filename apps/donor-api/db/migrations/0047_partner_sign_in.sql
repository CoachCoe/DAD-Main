-- Partner portal sign-in links (PRD §7.1 P-2..P-4, §7.5).
--
-- A partner user signs in only through an emailed single-use link: the
-- approval email to the signer, the invitation to a new user, and a sign-in
-- link requested from the portal once a session has expired. Each queued email
-- is delivered by the login-email worker, which mints the link token, stores
-- its hash on donor_partner_users.invite_token_hash (the one live link per
-- user), and keeps the token encrypted here only until it is sent. No token is
-- ever returned through the API.
CREATE TABLE donor_partner_login_emails (
	id UUID NOT NULL,
	partner_user_id UUID NOT NULL,
	kind VARCHAR(16) NOT NULL,
	encrypted_token BYTEA,
	expires_at TIMESTAMP WITH TIME ZONE,
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error VARCHAR(40),
	available_at TIMESTAMP WITH TIME ZONE NOT NULL,
	completed_at TIMESTAMP WITH TIME ZONE,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_partner_login_emails_kind CHECK (kind IN ('APPROVED', 'INVITED', 'SIGN_IN')),
	CONSTRAINT ck_donor_partner_login_emails_attempts CHECK (attempts >= 0),
	FOREIGN KEY (partner_user_id) REFERENCES donor_partner_users (id) ON DELETE CASCADE
);

CREATE INDEX ix_donor_partner_login_emails_due
	ON donor_partner_login_emails (available_at, id)
	WHERE completed_at IS NULL;

-- Sign-in requests are throttled per user by their recent history.
CREATE INDEX ix_donor_partner_login_emails_user
	ON donor_partner_login_emails (partner_user_id, created_at);

-- The approval notice is now the signer's APPROVED sign-in email, which reaches
-- them before they have ever signed in; the org-wide APPROVED message could
-- only reach users who already had.
UPDATE donor_partner_messages
SET completed_at = now(), suppressed_reason = 'superseded'
WHERE kind = 'APPROVED' AND completed_at IS NULL;
