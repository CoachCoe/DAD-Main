-- Network milestones + visibility (PRD §6.5 I-2, §6.7 N-1..N-3). Phase 3.4.
--
-- N-1/I-2 is the ANNUALIZED giving of every currently ACTIVE donor the donor
-- brought in -- not their lifetime net. Lifetime net (net_minor) is the wrong
-- basis: a lapsed referral still carries years of past gifts, but "your network
-- funds $X a year" must reflect what the network gives RIGHT NOW. So each
-- projection records the donor's own current monthly pledge and whether they are
-- currently active, and the network total is sum(current_monthly_minor) * 12
-- over active referrals -- a cheap indexed aggregate over ix_..._referrer_id, not
-- a per-request scan (PRD §13 risk #6).
--
-- current_monthly_minor is the donor's live monthly amount (0 when they have
-- never given or have lapsed). active mirrors "has an unbroken current streak":
-- the projection worker already computes current_streak, so this is a cheap
-- co-write, kept consistent by the same refresh.
ALTER TABLE donor_metric_projections
	ADD COLUMN current_monthly_minor BIGINT NOT NULL DEFAULT 0,
	ADD COLUMN active BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE donor_metric_projections
	ADD CONSTRAINT ck_donor_metrics_monthly CHECK (current_monthly_minor >= 0);

-- Finding referrals cheaply already has ix_donor_metric_projections_referrer_id;
-- a partial index on the active ones keeps the network aggregate tight as the
-- population grows.
CREATE INDEX ix_donor_metric_projections_active_referrer
	ON donor_metric_projections (referrer_id)
	WHERE active AND referrer_id IS NOT NULL;

-- N-3: a donor appears in their referrer's invitee list by first name + last
-- initial ONLY if they allow it; otherwise they count anonymously. Opt-IN, so
-- the default is invisible (false). Both this and rankings_opted_out (added in
-- 0032 to donor_preferences only) belong to the VERSIONED preference record, so
-- they must also exist on the append-only events table that mirrors every
-- preference change -- 0032 missed the events column, which this corrects.
ALTER TABLE donor_preferences
	ADD COLUMN network_visible BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE donor_preference_events
	ADD COLUMN rankings_opted_out BOOLEAN,
	ADD COLUMN network_visible BOOLEAN;
