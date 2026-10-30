-- The staff failed-payment queue (PRD §8) pages unresolved failed renewals
-- newest first; only FAILED periods are indexed, so it stays small and the
-- keyset page never scans the ledger.
CREATE INDEX ix_billing_periods_failed ON donor_billing_periods (due_at DESC, id DESC) WHERE status = 'FAILED';
