CREATE TABLE donor_billing_observations (
    id UUID PRIMARY KEY,
    period_id UUID NOT NULL REFERENCES donor_billing_periods(id),
    source_event_id UUID NOT NULL UNIQUE REFERENCES donor_stripe_events(id),
    status VARCHAR(8) NOT NULL CHECK (status IN ('FAILED', 'PAID')),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    currency VARCHAR(3) NOT NULL CHECK (currency = 'USD'),
    billing_reason VARCHAR(32) NOT NULL CHECK (billing_reason IN ('subscription_create', 'subscription_cycle')),
    occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
    event_created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    recorded_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX ix_donor_billing_observations_period_time
    ON donor_billing_observations(period_id, occurred_at, id);
