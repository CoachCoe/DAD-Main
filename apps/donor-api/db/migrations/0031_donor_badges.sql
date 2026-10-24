-- Badge earn-state (PRD §6.9 B-1..B-4, Appendix A). Phase 3.2.
--
-- One row per (donor, badge) records that a donor has earned a badge. Badges
-- are PERMANENT once earned (B-2): the engine re-evaluates current qualification
-- each refresh, but the stored row is never removed by automatic re-evaluation —
-- only a super-admin revoke (B-4) clears it, and that is audited. So the badge
-- code catalog lives in code (internal/badges); this table is only the per-donor
-- earned facts.
--
-- source distinguishes an automatically-earned badge from an admin grant, and
-- revoked_at supports the admin revoke (a revoked badge is kept as a tombstone
-- for audit rather than deleted, and is treated as not-earned while revoked).
CREATE TABLE donor_badges (
	donor_id UUID NOT NULL,
	code VARCHAR(16) NOT NULL,
	source VARCHAR(16) NOT NULL DEFAULT 'AUTO',
	earned_at TIMESTAMP WITH TIME ZONE NOT NULL,
	revoked_at TIMESTAMP WITH TIME ZONE,
	PRIMARY KEY (donor_id, code),
	CONSTRAINT ck_donor_badges_source CHECK (source IN ('AUTO', 'GRANT')),
	FOREIGN KEY (donor_id) REFERENCES donors (id) ON DELETE CASCADE
);
