-- A checkout can complete after its donor deleted the account: Stripe may
-- report it later than the window DeleteAccount waits for. Money taken after
-- the deletion is refunded in full and the subscription is cancelled at once;
-- the gift itself stays in the ledger like every other refunded gift. The
-- Stripe worker queues a reversal with the event and calls Stripe afterwards,
-- outside the donor lock. target is the PaymentIntent to refund or the
-- subscription to cancel.
CREATE TABLE donor_deleted_account_reversals (
    id UUID PRIMARY KEY,
    donor_id UUID NOT NULL REFERENCES donors (id),
    kind VARCHAR(8) NOT NULL,
    target VARCHAR(255) NOT NULL,
    amount_minor BIGINT,
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMP WITH TIME ZONE NOT NULL,
    completed_at TIMESTAMP WITH TIME ZONE,
    last_error VARCHAR(64),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    CONSTRAINT ck_deleted_account_reversal_kind CHECK (kind IN ('REFUND', 'CANCEL')),
    CONSTRAINT ck_deleted_account_reversal_amount CHECK ((kind = 'REFUND') = (amount_minor IS NOT NULL AND amount_minor > 0)),
    UNIQUE (kind, target)
);
CREATE INDEX ix_deleted_account_reversals_due ON donor_deleted_account_reversals (available_at) WHERE completed_at IS NULL;
