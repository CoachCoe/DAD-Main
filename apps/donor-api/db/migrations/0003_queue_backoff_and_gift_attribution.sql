ALTER TABLE donor_stripe_events ADD COLUMN available_at timestamptz NOT NULL DEFAULT now();
UPDATE donor_stripe_events SET available_at=created_at WHERE processed_at IS NULL;
CREATE INDEX donor_stripe_events_pending ON donor_stripe_events(available_at,id)
WHERE processed_at IS NULL AND attempts<10;

DROP INDEX ix_donor_messages_available_at;
CREATE INDEX donor_messages_pending ON donor_messages(available_at,id)
WHERE completed_at IS NULL AND attempts<5;
DROP INDEX ix_donor_login_emails_available_at;
CREATE INDEX donor_login_emails_pending ON donor_login_emails(available_at,id)
WHERE completed_at IS NULL AND attempts<5;

-- A gift records the checkout attribution only if it was still valid when charged.
ALTER TABLE donor_gifts ADD COLUMN referrer_id uuid REFERENCES donors(id),
ADD COLUMN referral_kind varchar(4),
ADD CONSTRAINT ck_donor_gift_attribution CHECK ((referrer_id IS NULL AND referral_kind IS NULL)
 OR (referrer_id IS NOT NULL AND referral_kind IN ('LINK','IRL'))),
ADD CONSTRAINT ck_donor_gift_self_referral CHECK (referrer_id IS NULL OR referrer_id<>donor_id);
UPDATE donor_gifts g SET referrer_id=c.referrer_id,referral_kind=c.referral_kind
FROM donor_checkouts c WHERE c.id=g.checkout_id AND c.referrer_id IS NOT NULL AND g.paid_at<c.referral_expires_at;
CREATE INDEX ix_donor_gifts_referrer_id ON donor_gifts(referrer_id);

-- Every donor has a projection row so the refresh worker can poll the
-- next_refresh_at index instead of scanning donors for missing projections.
ALTER TABLE donor_metric_projections ADD COLUMN refresh_failures integer NOT NULL DEFAULT 0,
ADD COLUMN refresh_error varchar(80);
INSERT INTO donor_metric_projections(id,donor_id,computed_at,next_refresh_at)
SELECT gen_random_uuid(),d.id,now(),now() FROM donors d
WHERE NOT EXISTS(SELECT 1 FROM donor_metric_projections p WHERE p.donor_id=d.id);
