-- Employee giving: the link that resolves a donor's company_code to an approved
-- partner, plus teams. (PRD §7.4 EM-1/EM-2/EM-4.) Phase 6.
--
-- Phase 3 bound a company_code string onto the donor at first gift but left it
-- unresolved ("applied in a later phase"). This is that phase: a confirmed link
-- row ties the donor to the partner org whose company_code matches, so the
-- partner's Our People aggregates and the matching engine can count that
-- donor's gifts — including gifts made BEFORE the link existed (EM-6).
--
-- A donor belongs to at most one partner (one employer) and, within it, at most
-- one team (EM-4). Teams carry a name only — no membership roster is exposed,
-- and every partner-facing figure is k-anonymised (<5 suppressed) in the query
-- layer, never here.

CREATE TABLE donor_partner_teams (
	id UUID NOT NULL,
	partner_id UUID NOT NULL,
	name VARCHAR(80) NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (partner_id, name),
	FOREIGN KEY (partner_id) REFERENCES donor_partner_signups (id)
);

CREATE TABLE donor_partner_employees (
	donor_id UUID NOT NULL,
	partner_id UUID NOT NULL,
	team_id UUID,
	enrolled_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (donor_id),
	FOREIGN KEY (donor_id) REFERENCES donors (id) ON DELETE CASCADE,
	FOREIGN KEY (partner_id) REFERENCES donor_partner_signups (id),
	FOREIGN KEY (team_id) REFERENCES donor_partner_teams (id),
	-- A team must belong to the same partner the employee is linked to; enforced
	-- in the service (a cross-partner team_id is rejected before insert).
	CONSTRAINT ck_donor_partner_employees_team CHECK (team_id IS NULL OR team_id <> donor_id)
);

CREATE INDEX ix_donor_partner_employees_partner ON donor_partner_employees (partner_id);
CREATE INDEX ix_donor_partner_employees_team ON donor_partner_employees (team_id) WHERE team_id IS NOT NULL;

-- Offline match payments (EM-7). DAD computes the match owed; an admin records
-- a payment received out of band. No charge path exists in the app.
CREATE TABLE donor_partner_match_payments (
	id UUID NOT NULL,
	partner_id UUID NOT NULL,
	year INTEGER NOT NULL,
	amount_minor BIGINT NOT NULL,
	received_at TIMESTAMP WITH TIME ZONE NOT NULL,
	recorded_by UUID NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY (partner_id) REFERENCES donor_partner_signups (id),
	FOREIGN KEY (recorded_by) REFERENCES donor_staff (id),
	CONSTRAINT ck_donor_partner_match_payments_amount CHECK (amount_minor > 0),
	CONSTRAINT ck_donor_partner_match_payments_year CHECK (year >= 2024 AND year < 3000)
);

CREATE INDEX ix_donor_partner_match_payments_partner ON donor_partner_match_payments (partner_id, year);
