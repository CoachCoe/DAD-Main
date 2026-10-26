CREATE TABLE donor_projection_schedules (
    id VARCHAR(50) PRIMARY KEY,
    next_run_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE donor_event_alert_jobs (
    event_id UUID PRIMARY KEY REFERENCES donor_events(id) ON DELETE CASCADE,
    last_donor_id UUID,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX ix_event_alert_jobs_pending ON donor_event_alert_jobs(updated_at,event_id) WHERE completed_at IS NULL;
CREATE INDEX ix_donors_alert_city ON donors(lower(city),id) WHERE NOT disabled;
CREATE INDEX ix_donors_alert_zip ON donors(left(zip,3),id) WHERE NOT disabled;
