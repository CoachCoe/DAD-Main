CREATE INDEX donor_pending_refund_reconciliation_idx ON donor_refunds(id)
WHERE status IN ('PENDING','REQUIRES_ACTION');
