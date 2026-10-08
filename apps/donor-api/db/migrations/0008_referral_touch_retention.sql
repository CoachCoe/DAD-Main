-- Retention deletes unconverted referral touches in capture order. A touch is
-- kept while a checkout carrying the referrer, expiry and kind it copied from
-- it has an attributed gift, found through ix_donor_gifts_checkout_id.
CREATE INDEX ix_donor_referral_touches_touched_at ON donor_referral_touches (touched_at, id);
CREATE INDEX ix_donor_checkouts_referral ON donor_checkouts (referrer_id, referral_expires_at);
DROP INDEX ix_donor_checkouts_referrer_id;
