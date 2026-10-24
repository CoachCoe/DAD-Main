-- Initiatives model (PRD §8 Initiatives, A-1/A-2, P-11). Phase 2.1.
--
-- An initiative is a funded program a donor's giving supports: the unit the
-- daily rotation (2.4) surfaces and that spend (2.2) and verified metrics (2.3)
-- attach to. Phase 1's content-publication snapshot (donor_content_*) carried a
-- thin, flat initiative inside a published JSON blob; Phase 2 promotes it to a
-- relational row so spend can aggregate by category and the rotation can select
-- active initiatives with a cheap indexed lookup rather than scanning a blob
-- (the §13 risk #6 scale constraint: this path must stay a lookup at 1M donors).
--
-- Visibility follows the same draft->publish discipline as the content system:
-- a row is DRAFT until staff publish it, and only PUBLISHED initiatives are ever
-- visible to donors or eligible for rotation. status (ACTIVE/COMPLETE) is the
-- program's lifecycle, orthogonal to the publish state: a COMPLETE initiative
-- stays visible but drops out of the daily rotation.
--
-- region/county: location is a human label; region is the coarse grouping the
-- app filters by; counties is the set a region covers (PRD §8 "county set"),
-- stored as a text[] because it is a small, read-mostly tag set, not its own
-- entity. committed_minor is the dollars committed to the initiative, in minor
-- units (integer cents) to match every other money column in the schema.
--
-- partner_signup_id links an initiative to the partner that runs it
-- (donor_partner_signups, the only partner entity that exists in R1); it is
-- nullable because an initiative may be internal or its partner not yet onboarded.
CREATE TABLE donor_initiatives (
	id UUID NOT NULL,
	slug VARCHAR(100) NOT NULL,
	title VARCHAR(150) NOT NULL,
	summary VARCHAR(4000) NOT NULL,
	type VARCHAR(40) NOT NULL,
	location VARCHAR(200) NOT NULL,
	region VARCHAR(100) NOT NULL,
	counties TEXT[] NOT NULL DEFAULT '{}',
	started_on DATE NOT NULL,
	status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
	committed_minor BIGINT NOT NULL DEFAULT 0,
	partner_signup_id UUID,
	publish_state VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
	published_at TIMESTAMP WITH TIME ZONE,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (slug),
	CONSTRAINT ck_donor_initiatives_status CHECK (status IN ('ACTIVE', 'COMPLETE')),
	CONSTRAINT ck_donor_initiatives_publish_state CHECK (publish_state IN ('DRAFT', 'PUBLISHED')),
	CONSTRAINT ck_donor_initiatives_committed_minor CHECK (committed_minor >= 0),
	-- A published initiative must carry its publish time; a draft must not.
	CONSTRAINT ck_donor_initiatives_published_at CHECK (
		(publish_state = 'PUBLISHED') = (published_at IS NOT NULL)
	),
	FOREIGN KEY (partner_signup_id) REFERENCES donor_partner_signups (id)
);

-- The rotation (2.4) and the donor-facing list both ask for "published, active
-- initiatives"; this partial index serves that selection without scanning
-- drafts or completed programs.
CREATE INDEX ix_donor_initiatives_rotatable ON donor_initiatives (started_on, id)
	WHERE publish_state = 'PUBLISHED' AND status = 'ACTIVE';

-- Up to 6 headline metrics per initiative, of which the app shows 4 (PRD §8).
-- Each is a verified figure in the same unit vocabulary as the impact ledger
-- (USD_CENTS/PEOPLE/INITIATIVES/PARTNERS), carries its own draft->publish state
-- and a verification note + date (2.3, §7.3): only published metrics are ever
-- shown, unverified figures are hidden and never estimated. position orders the
-- headline metrics (0-5); the application enforces the "<= 6, show first 4" rule.
CREATE TABLE donor_initiative_metrics (
	id UUID NOT NULL,
	initiative_id UUID NOT NULL,
	position INTEGER NOT NULL,
	label VARCHAR(150) NOT NULL,
	value BIGINT NOT NULL,
	unit VARCHAR(16) NOT NULL,
	verification_note VARCHAR(2000) NOT NULL DEFAULT '',
	verified_on DATE,
	publish_state VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (initiative_id, position),
	CONSTRAINT ck_donor_initiative_metrics_position CHECK (position >= 0 AND position < 6),
	CONSTRAINT ck_donor_initiative_metrics_unit CHECK (unit IN ('USD_CENTS', 'PEOPLE', 'INITIATIVES', 'PARTNERS')),
	CONSTRAINT ck_donor_initiative_metrics_publish_state CHECK (publish_state IN ('DRAFT', 'PUBLISHED')),
	-- A published metric must be verified (note + date); a draft need not be.
	CONSTRAINT ck_donor_initiative_metrics_verified CHECK (
		publish_state = 'DRAFT' OR (verified_on IS NOT NULL AND verification_note <> '')
	),
	FOREIGN KEY (initiative_id) REFERENCES donor_initiatives (id) ON DELETE CASCADE
);

CREATE INDEX ix_donor_initiative_metrics_initiative ON donor_initiative_metrics (initiative_id, position);
