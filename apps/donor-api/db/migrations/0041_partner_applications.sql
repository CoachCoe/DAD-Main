-- Partner applications, approval, and partner users (PRD §7.1 P-1..P-5). Phase 5.1/5.2.
--
-- Replaces the R1 contact-form (donor_partner_signups) as the ENTRY point with a
-- real application + approval state machine, while keeping donor_partner_signups
-- as the partner-org anchor that donor_initiatives already references (its FK is
-- unchanged — least blast radius). On approval the application's organization is
-- promoted into a partner-org row there and the signer becomes its partner admin.
--
-- P-1 fields: organization, website, type, contact name, title, work email,
-- phone, workforce size. status is the P-2 machine: PENDING until a DAD admin
-- decides, then APPROVED or REJECTED (decided_by/decided_at/decision_reason
-- record the §8-audited decision). approved_partner_id links an approved
-- application to the partner-org row it produced.
CREATE TABLE donor_partner_applications (
	id UUID NOT NULL,
	organization VARCHAR(200) NOT NULL,
	website VARCHAR(500) NOT NULL DEFAULT '',
	type VARCHAR(80) NOT NULL DEFAULT '',
	contact_name VARCHAR(100) NOT NULL,
	title VARCHAR(100) NOT NULL DEFAULT '',
	work_email VARCHAR(320) NOT NULL,
	phone VARCHAR(40) NOT NULL DEFAULT '',
	workforce_size INTEGER NOT NULL DEFAULT 0,
	status VARCHAR(16) NOT NULL DEFAULT 'PENDING',
	decided_by UUID,
	decided_at TIMESTAMP WITH TIME ZONE,
	decision_reason VARCHAR(500) NOT NULL DEFAULT '',
	approved_partner_id UUID,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_donor_partner_applications_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
	CONSTRAINT ck_donor_partner_applications_workforce CHECK (workforce_size >= 0),
	-- A decided application carries its decider and time; a pending one does not.
	CONSTRAINT ck_donor_partner_applications_decided CHECK (
		(status = 'PENDING') = (decided_at IS NULL)
	),
	FOREIGN KEY (decided_by) REFERENCES donor_staff (id),
	FOREIGN KEY (approved_partner_id) REFERENCES donor_partner_signups (id)
);

-- One pending application per normalized work email: a partner cannot stack
-- duplicate pending applications, but may re-apply after a rejection.
CREATE UNIQUE INDEX uq_donor_partner_applications_pending
	ON donor_partner_applications (work_email)
	WHERE status = 'PENDING';

-- The approval queue (§8 Partnerships) reads pending applications oldest-first.
CREATE INDEX ix_donor_partner_applications_pending
	ON donor_partner_applications (created_at, id)
	WHERE status = 'PENDING';

-- Partner-org fields on the existing anchor. A row authored by the old contact
-- form stays a lead (approved_at NULL); an approved application sets these.
-- workforce_size/match_rate_bp/match_cap_minor are the §8 Partnerships settings
-- (match rate in basis points, cap in minor units); company_code + irl_code are
-- the EM-1 employee-giving codes.
ALTER TABLE donor_partner_signups
	ADD COLUMN approved_at TIMESTAMP WITH TIME ZONE,
	ADD COLUMN partner_type VARCHAR(80) NOT NULL DEFAULT '',
	ADD COLUMN workforce_size INTEGER NOT NULL DEFAULT 0,
	ADD COLUMN match_rate_bp INTEGER NOT NULL DEFAULT 0,
	ADD COLUMN match_cap_minor BIGINT NOT NULL DEFAULT 0,
	ADD COLUMN company_code VARCHAR(40),
	ADD COLUMN logo_url VARCHAR(500) NOT NULL DEFAULT '',
	ADD COLUMN description VARCHAR(4000) NOT NULL DEFAULT '';

ALTER TABLE donor_partner_signups
	ADD CONSTRAINT ck_donor_partner_signups_workforce CHECK (workforce_size >= 0),
	ADD CONSTRAINT ck_donor_partner_signups_match_rate CHECK (match_rate_bp >= 0),
	ADD CONSTRAINT ck_donor_partner_signups_match_cap CHECK (match_cap_minor >= 0);

CREATE UNIQUE INDEX uq_donor_partner_signups_company_code
	ON donor_partner_signups (company_code)
	WHERE company_code IS NOT NULL;

-- Partner portal users (P-4/P-5). A partner user is a login scoped to one
-- partner org with a role of ADMIN (manage org + users + exports) or VIEWER
-- (read-only). It links to a donor account id for authentication reuse, but a
-- partner account CANNOT donate (enforced in the app, P-5). The signer of an
-- approved application becomes the first ADMIN.
CREATE TABLE donor_partner_users (
	id UUID NOT NULL,
	partner_id UUID NOT NULL,
	email VARCHAR(320) NOT NULL,
	name VARCHAR(100) NOT NULL DEFAULT '',
	role VARCHAR(16) NOT NULL,
	invited_by UUID,
	accepted_at TIMESTAMP WITH TIME ZONE,
	disabled BOOLEAN NOT NULL DEFAULT false,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (partner_id, email),
	CONSTRAINT ck_donor_partner_users_role CHECK (role IN ('ADMIN', 'VIEWER')),
	FOREIGN KEY (partner_id) REFERENCES donor_partner_signups (id) ON DELETE CASCADE,
	FOREIGN KEY (invited_by) REFERENCES donor_partner_users (id)
);

CREATE INDEX ix_donor_partner_users_partner ON donor_partner_users (partner_id);
