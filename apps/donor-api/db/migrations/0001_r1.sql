-- R1 donor baseline, compatible with the existing donor database.
CREATE TABLE donor_partner_signups (
	organization VARCHAR(200) NOT NULL,
	name VARCHAR(100) NOT NULL,
	email VARCHAR(320) NOT NULL,
	website VARCHAR(500) NOT NULL,
	message VARCHAR(2000) NOT NULL,
	request_hash VARCHAR(64) NOT NULL,
	consent_version VARCHAR(80) NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id)
);

CREATE INDEX ix_donor_partner_signups_email ON donor_partner_signups (email);
CREATE TABLE donor_staff (
	email VARCHAR(320) NOT NULL,
	name VARCHAR(100) NOT NULL,
	role VARCHAR(16) NOT NULL,
	disabled BOOLEAN DEFAULT 'false' NOT NULL,
	password_hash VARCHAR(255),
	totp_secret BYTEA,
	totp_counter BIGINT DEFAULT '-1' NOT NULL,
	failures INTEGER DEFAULT '0' NOT NULL,
	locked_until TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_staff_role CHECK (role IN ('SUPPORT', 'FINANCE', 'ADMIN')),
	UNIQUE (email)
);

CREATE TABLE donor_stripe_events (
	stripe_event_id VARCHAR(255) NOT NULL,
	event_type VARCHAR(80) NOT NULL,
	occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
	data JSON NOT NULL,
	processed_at TIMESTAMP WITH TIME ZONE,
	attempts INTEGER DEFAULT '0' NOT NULL,
	last_error VARCHAR(80),
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (stripe_event_id)
);

CREATE INDEX ix_donor_stripe_events_processed_at ON donor_stripe_events (processed_at);
CREATE TABLE donors (
	email VARCHAR(320) NOT NULL,
	claimed_at TIMESTAMP WITH TIME ZONE,
	disabled BOOLEAN DEFAULT 'false' NOT NULL,
	public_name VARCHAR(80),
	referral_code VARCHAR(64),
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (email),
	UNIQUE (referral_code)
);

CREATE TABLE donor_checkouts (
	donor_id UUID NOT NULL,
	amount_minor BIGINT NOT NULL,
	currency VARCHAR(3) DEFAULT 'USD' NOT NULL,
	frequency VARCHAR(8) NOT NULL,
	referrer_id UUID,
	referral_kind VARCHAR(4),
	referral_expires_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_checkout_minimum CHECK (amount_minor >= 100),
	CONSTRAINT ck_donor_checkout_currency CHECK (currency = 'USD'),
	CONSTRAINT ck_donor_frequency CHECK (frequency IN ('MONTHLY', 'ONE_TIME')),
	CONSTRAINT ck_donor_checkout_attribution CHECK ((referrer_id IS NULL AND referral_kind IS NULL AND referral_expires_at IS NULL) OR (referrer_id IS NOT NULL AND referral_kind IS NOT NULL AND referral_kind IN ('LINK', 'IRL') AND referral_expires_at IS NOT NULL)),
	CONSTRAINT ck_donor_checkout_self_referral CHECK (referrer_id IS NULL OR referrer_id <> donor_id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(referrer_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_checkouts_donor_id ON donor_checkouts (donor_id);
CREATE INDEX ix_donor_checkouts_referrer_id ON donor_checkouts (referrer_id);
CREATE TABLE donor_content_publications (
	actor_id UUID NOT NULL,
	payload JSON NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(actor_id) REFERENCES donor_staff (id)
);

CREATE TABLE donor_content_sources (
	actor_id UUID NOT NULL,
	name VARCHAR(120) NOT NULL,
	sha256 VARCHAR(64) NOT NULL,
	encrypted_data BYTEA NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(actor_id) REFERENCES donor_staff (id)
);

CREATE TABLE donor_login_emails (
	donor_id UUID NOT NULL,
	encrypted_token BYTEA,
	expires_at TIMESTAMP WITH TIME ZONE,
	available_at TIMESTAMP WITH TIME ZONE NOT NULL,
	completed_at TIMESTAMP WITH TIME ZONE,
	attempts INTEGER DEFAULT '0' NOT NULL,
	last_error VARCHAR(80),
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_login_emails_available_at ON donor_login_emails (available_at);
CREATE INDEX ix_donor_login_emails_donor_id ON donor_login_emails (donor_id);
CREATE TABLE donor_login_links (
	donor_id UUID NOT NULL,
	token_hash VARCHAR(64) NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	consumed_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	UNIQUE (token_hash)
);

CREATE INDEX ix_donor_login_links_donor_id ON donor_login_links (donor_id);
CREATE TABLE donor_messages (
	donor_id UUID NOT NULL,
	dedup_key VARCHAR(160) NOT NULL,
	kind VARCHAR(20) NOT NULL,
	context JSON NOT NULL,
	available_at TIMESTAMP WITH TIME ZONE NOT NULL,
	completed_at TIMESTAMP WITH TIME ZONE,
	attempts INTEGER DEFAULT '0' NOT NULL,
	last_error VARCHAR(80),
	suppressed_reason VARCHAR(80),
	subject VARCHAR(200),
	body TEXT,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	UNIQUE (dedup_key)
);

CREATE INDEX ix_donor_messages_available_at ON donor_messages (available_at);
CREATE INDEX ix_donor_messages_donor_id ON donor_messages (donor_id);
CREATE TABLE donor_metric_projections (
	donor_id UUID NOT NULL,
	referrer_id UUID,
	referral_kind VARCHAR(4),
	first_gift_id VARCHAR(255),
	net_minor BIGINT,
	founding_awarded_at TIMESTAMP WITH TIME ZONE,
	best_streak INTEGER DEFAULT '0' NOT NULL,
	current_streak INTEGER,
	at_risk BOOLEAN DEFAULT 'false' NOT NULL,
	grace_ends_at TIMESTAMP WITH TIME ZONE,
	computed_at TIMESTAMP WITH TIME ZONE NOT NULL,
	next_refresh_at TIMESTAMP WITH TIME ZONE NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_metrics_net CHECK (net_minor IS NULL OR net_minor >= 0),
	CONSTRAINT ck_donor_metrics_streak CHECK (best_streak >= 0 AND (current_streak IS NULL OR current_streak >= 0)),
	CONSTRAINT ck_donor_metrics_referral CHECK ((referrer_id IS NULL AND referral_kind IS NULL) OR (referrer_id IS NOT NULL AND referral_kind IN ('LINK', 'IRL'))),
	UNIQUE (donor_id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(referrer_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_metric_projections_next_refresh_at ON donor_metric_projections (next_refresh_at);
CREATE INDEX ix_donor_metric_projections_referrer_id ON donor_metric_projections (referrer_id);
CREATE TABLE donor_preference_events (
	donor_id UUID NOT NULL,
	version INTEGER NOT NULL,
	referral_emails BOOLEAN NOT NULL,
	marketing_emails BOOLEAN NOT NULL,
	consent_version VARCHAR(80) NOT NULL,
	source VARCHAR(40) NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT uq_donor_preference_version UNIQUE (donor_id, version),
	FOREIGN KEY(donor_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_preference_events_donor_id ON donor_preference_events (donor_id);
CREATE TABLE donor_preferences (
	donor_id UUID NOT NULL,
	version INTEGER NOT NULL,
	referral_emails BOOLEAN NOT NULL,
	marketing_emails BOOLEAN NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (donor_id),
	FOREIGN KEY(donor_id) REFERENCES donors (id)
);

CREATE TABLE donor_referral_touches (
	token_hash VARCHAR(64) NOT NULL,
	referrer_id UUID NOT NULL,
	kind VARCHAR(4) NOT NULL,
	touched_at TIMESTAMP WITH TIME ZONE NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_touch_kind CHECK (kind IN ('LINK', 'IRL')),
	UNIQUE (token_hash),
	FOREIGN KEY(referrer_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_referral_touches_referrer_id ON donor_referral_touches (referrer_id);
CREATE TABLE donor_sessions (
	donor_id UUID NOT NULL,
	token_hash VARCHAR(64) NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	revoked_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	UNIQUE (token_hash)
);

CREATE INDEX ix_donor_sessions_donor_id ON donor_sessions (donor_id);
CREATE TABLE donor_staff_actions (
	actor_id UUID NOT NULL,
	donor_id UUID NOT NULL,
	kind VARCHAR(16) NOT NULL,
	target_id UUID NOT NULL,
	amount_minor BIGINT,
	period_end TIMESTAMP WITH TIME ZONE,
	reason VARCHAR(500) NOT NULL,
	stripe_refund_id VARCHAR(255),
	status VARCHAR(16) DEFAULT 'AUTHORIZED' NOT NULL,
	completed_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_staff_action_shape CHECK ((kind = 'REFUND' AND amount_minor IS NOT NULL AND amount_minor > 0 AND period_end IS NULL) OR (kind = 'CANCEL' AND amount_minor IS NULL AND period_end IS NOT NULL)),
	FOREIGN KEY(actor_id) REFERENCES donor_staff (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	UNIQUE (stripe_refund_id)
);

CREATE INDEX ix_donor_staff_actions_actor_id ON donor_staff_actions (actor_id);
CREATE INDEX ix_donor_staff_actions_donor_id ON donor_staff_actions (donor_id);
CREATE INDEX ix_donor_staff_actions_target_id ON donor_staff_actions (target_id);
CREATE TABLE donor_staff_audit (
	actor_id UUID,
	action VARCHAR(80) NOT NULL,
	target VARCHAR(160) NOT NULL,
	details JSON NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(actor_id) REFERENCES donor_staff (id)
);

CREATE INDEX ix_donor_staff_audit_action ON donor_staff_audit (action);
CREATE INDEX ix_donor_staff_audit_actor_id ON donor_staff_audit (actor_id);
CREATE TABLE donor_staff_invites (
	staff_id UUID NOT NULL,
	token_hash VARCHAR(64) NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	consumed_at TIMESTAMP WITH TIME ZONE,
	pending_secret BYTEA,
	attempts INTEGER DEFAULT '0' NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(staff_id) REFERENCES donor_staff (id),
	UNIQUE (token_hash)
);

CREATE INDEX ix_donor_staff_invites_staff_id ON donor_staff_invites (staff_id);
CREATE TABLE donor_staff_sessions (
	staff_id UUID NOT NULL,
	token_hash VARCHAR(64) NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	revoked_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(staff_id) REFERENCES donor_staff (id),
	UNIQUE (token_hash)
);

CREATE INDEX ix_donor_staff_sessions_staff_id ON donor_staff_sessions (staff_id);
CREATE TABLE donor_amount_changes (
	donor_id UUID NOT NULL,
	checkout_id UUID NOT NULL,
	previous_minor BIGINT NOT NULL,
	amount_minor BIGINT NOT NULL,
	effective_at TIMESTAMP WITH TIME ZONE NOT NULL,
	item_id VARCHAR(255) NOT NULL,
	product_id VARCHAR(255) NOT NULL,
	confirmed_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_amount_change_minor CHECK (amount_minor >= 100 AND amount_minor <= 99999999 AND previous_minor >= 100),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(checkout_id) REFERENCES donor_checkouts (id)
);

CREATE INDEX ix_donor_amount_changes_checkout_id ON donor_amount_changes (checkout_id);
CREATE INDEX ix_donor_amount_changes_donor_id ON donor_amount_changes (donor_id);
CREATE TABLE donor_billing_periods (
	donor_id UUID NOT NULL,
	checkout_id UUID NOT NULL,
	stripe_invoice_id VARCHAR(255) NOT NULL,
	stripe_subscription_id VARCHAR(255) NOT NULL,
	due_at TIMESTAMP WITH TIME ZONE NOT NULL,
	ends_at TIMESTAMP WITH TIME ZONE NOT NULL,
	status VARCHAR(8) NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_period_dates CHECK (ends_at > due_at),
	CONSTRAINT ck_donor_period_status CHECK (status IN ('EXPECTED', 'FAILED', 'PAID')),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(checkout_id) REFERENCES donor_checkouts (id),
	UNIQUE (stripe_invoice_id)
);

CREATE INDEX ix_donor_billing_periods_donor_id ON donor_billing_periods (donor_id);
CREATE TABLE donor_content_head (
	id VARCHAR(16) NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	publication_id UUID,
	PRIMARY KEY (id),
	FOREIGN KEY(publication_id) REFERENCES donor_content_publications (id)
);

CREATE TABLE donor_stripe_checkouts (
	checkout_id UUID NOT NULL,
	request_hash VARCHAR(64) NOT NULL,
	request_params JSON NOT NULL,
	stripe_session_id VARCHAR(255),
	stripe_subscription_id VARCHAR(255),
	hosted_url TEXT,
	status VARCHAR(16) NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (checkout_id),
	FOREIGN KEY(checkout_id) REFERENCES donor_checkouts (id),
	UNIQUE (stripe_session_id),
	UNIQUE (stripe_subscription_id)
);

CREATE TABLE donor_cancellations (
	donor_id UUID NOT NULL,
	attempt_id UUID NOT NULL,
	confirmed_period_end TIMESTAMP WITH TIME ZONE NOT NULL,
	completed_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(attempt_id) REFERENCES donor_stripe_checkouts (id)
);

CREATE INDEX ix_donor_cancellations_donor_id ON donor_cancellations (donor_id);
CREATE TABLE donor_gifts (
	donor_id UUID NOT NULL,
	checkout_id UUID NOT NULL,
	stripe_object_id VARCHAR(255) NOT NULL,
	amount_minor BIGINT NOT NULL,
	currency VARCHAR(3) NOT NULL,
	frequency VARCHAR(8) NOT NULL,
	paid_at TIMESTAMP WITH TIME ZONE NOT NULL,
	stripe_payment_intent_id VARCHAR(255),
	period_id UUID,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_gift_money CHECK (amount_minor > 0 AND currency = 'USD'),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(checkout_id) REFERENCES donor_checkouts (id),
	UNIQUE (stripe_object_id),
	UNIQUE (stripe_payment_intent_id),
	FOREIGN KEY(period_id) REFERENCES donor_billing_periods (id)
);

CREATE INDEX ix_donor_gifts_checkout_id ON donor_gifts (checkout_id);
CREATE INDEX ix_donor_gifts_donor_id ON donor_gifts (donor_id);
CREATE INDEX ix_donor_gifts_paid_at ON donor_gifts (paid_at);
CREATE TABLE donor_pauses (
	donor_id UUID NOT NULL,
	attempt_id UUID NOT NULL,
	months INTEGER NOT NULL,
	starts_at TIMESTAMP WITH TIME ZONE NOT NULL,
	resumes_at TIMESTAMP WITH TIME ZONE NOT NULL,
	confirmed_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_pause_months CHECK (months IN (1, 2, 3)),
	CONSTRAINT ck_donor_pause_dates CHECK (resumes_at > starts_at),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(attempt_id) REFERENCES donor_stripe_checkouts (id)
);

CREATE INDEX ix_donor_pauses_attempt_id ON donor_pauses (attempt_id);
CREATE INDEX ix_donor_pauses_donor_id ON donor_pauses (donor_id);
CREATE TABLE donor_payment_updates (
	donor_id UUID NOT NULL,
	attempt_id UUID NOT NULL,
	customer_id VARCHAR(255) NOT NULL,
	request_params JSON NOT NULL,
	stripe_session_id VARCHAR(255),
	hosted_url TEXT,
	status VARCHAR(16) NOT NULL,
	completed_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	FOREIGN KEY(attempt_id) REFERENCES donor_stripe_checkouts (id),
	UNIQUE (stripe_session_id)
);

CREATE INDEX ix_donor_payment_updates_donor_id ON donor_payment_updates (donor_id);
CREATE TABLE donor_recurring_reservations (
	donor_id UUID NOT NULL,
	attempt_id UUID NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (donor_id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	UNIQUE (attempt_id),
	FOREIGN KEY(attempt_id) REFERENCES donor_stripe_checkouts (id)
);

CREATE TABLE donor_subscriptions (
	attempt_id UUID NOT NULL,
	donor_id UUID NOT NULL,
	status VARCHAR(32) NOT NULL,
	period_end TIMESTAMP WITH TIME ZONE NOT NULL,
	stops_at TIMESTAMP WITH TIME ZONE,
	observed_at TIMESTAMP WITH TIME ZONE NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (attempt_id),
	FOREIGN KEY(attempt_id) REFERENCES donor_stripe_checkouts (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_subscriptions_donor_id ON donor_subscriptions (donor_id);
CREATE TABLE donor_refunds (
	gift_id UUID NOT NULL,
	stripe_refund_id VARCHAR(255) NOT NULL,
	amount_minor BIGINT NOT NULL,
	currency VARCHAR(3) NOT NULL,
	status VARCHAR(16) NOT NULL,
	observed_at TIMESTAMP WITH TIME ZONE NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_refund_money CHECK (amount_minor > 0 AND currency = 'USD'),
	CONSTRAINT ck_donor_refund_status CHECK (status IN ('PENDING', 'REQUIRES_ACTION', 'SUCCEEDED', 'FAILED', 'CANCELED')),
	FOREIGN KEY(gift_id) REFERENCES donor_gifts (id),
	UNIQUE (stripe_refund_id)
);

CREATE INDEX ix_donor_refunds_gift_id ON donor_refunds (gift_id);
CREATE TABLE donor_resumptions (
	donor_id UUID NOT NULL,
	pause_id UUID NOT NULL,
	next_renewal TIMESTAMP WITH TIME ZONE NOT NULL,
	confirmed_at TIMESTAMP WITH TIME ZONE,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id),
	UNIQUE (pause_id),
	FOREIGN KEY(pause_id) REFERENCES donor_pauses (id)
);

CREATE INDEX ix_donor_resumptions_donor_id ON donor_resumptions (donor_id);
CREATE TABLE donor_refund_observations (
	refund_id UUID NOT NULL,
	source_event_id UUID,
	source_action_id UUID,
	status VARCHAR(16) NOT NULL,
	observed_at TIMESTAMP WITH TIME ZONE NOT NULL,
	id UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_refund_observation_source CHECK ((source_event_id IS NULL) <> (source_action_id IS NULL)),
	FOREIGN KEY(refund_id) REFERENCES donor_refunds (id),
	UNIQUE (source_event_id),
	FOREIGN KEY(source_event_id) REFERENCES donor_stripe_events (id),
	FOREIGN KEY(source_action_id) REFERENCES donor_staff_actions (id)
);

CREATE INDEX ix_donor_refund_observations_refund_id ON donor_refund_observations (refund_id);
CREATE INDEX ix_donor_refund_observations_source_action_id ON donor_refund_observations (source_action_id);
