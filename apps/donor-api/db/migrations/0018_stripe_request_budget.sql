-- Stripe limits requests per account, so every replica that calls Stripe
-- reserves its requests from one row per bucket. next_at is the bucket's
-- theoretical arrival time in the generic cell rate algorithm.
CREATE TABLE donor_stripe_budgets (
    bucket VARCHAR(32) PRIMARY KEY,
    next_at TIMESTAMPTZ NOT NULL
);
