-- Partner report export history (PRD §7.3 P-14). Phase 5.3.
--
-- Each row records one branded PDF export: who (partner user) exported the
-- dashboard snapshot, for which partner org, and when. The figures themselves
-- are not stored — the PDF is regenerated on demand — only the audit of exports
-- so the portal can list "previous exports".
CREATE TABLE donor_partner_reports (
	id UUID NOT NULL,
	partner_id UUID NOT NULL,
	exported_by UUID NOT NULL,
	committed_minor BIGINT NOT NULL,
	deployed_minor BIGINT NOT NULL,
	people_reached BIGINT NOT NULL,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	FOREIGN KEY (partner_id) REFERENCES donor_partner_signups (id),
	FOREIGN KEY (exported_by) REFERENCES donor_partner_users (id)
);

CREATE INDEX ix_donor_partner_reports_partner ON donor_partner_reports (partner_id, created_at DESC);
