CREATE TABLE donor_retention_milestones (
    donor_id UUID NOT NULL REFERENCES donors(id) ON DELETE CASCADE,
    days INTEGER NOT NULL CHECK (days IN (30, 90, 180, 365)),
    cohort_at TIMESTAMPTZ NOT NULL,
    checkpoint_at TIMESTAMPTZ NOT NULL,
    retained BOOLEAN,
    computed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (donor_id, days)
);
CREATE INDEX ix_donor_retention_cohort ON donor_retention_milestones(cohort_at, days, checkpoint_at);
