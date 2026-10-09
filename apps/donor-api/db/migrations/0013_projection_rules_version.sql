-- A projection records the version of the projection rules that computed it, so
-- a release that changes the rules recomputes every donor at the projection
-- worker's pace rather than at each donor's next refresh, which can now be up
-- to 30 days away. Existing projections are version 0 and recompute once.
ALTER TABLE donor_metric_projections ADD COLUMN rules_version integer NOT NULL DEFAULT 0;

CREATE INDEX ix_donor_metric_projections_rules_version ON donor_metric_projections (rules_version);
