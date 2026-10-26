ALTER TABLE donor_partner_signups ADD COLUMN logo_png BYTEA;
ALTER TABLE donor_partner_signups ADD CONSTRAINT ck_partner_logo_size CHECK (octet_length(logo_png) <= 1048576);
ALTER TABLE donor_partner_reports ADD COLUMN pdf BYTEA;
ALTER TABLE donor_partner_reports ADD COLUMN snapshot JSONB;
ALTER TABLE donor_partner_reports ADD CONSTRAINT ck_partner_report_artifact
    CHECK ((pdf IS NULL) = (snapshot IS NULL) AND octet_length(pdf) <= 2097152);
CREATE INDEX ix_billing_failure_reports ON donor_billing_observations(occurred_at, period_id)
    WHERE status='FAILED' AND billing_reason='subscription_cycle';
