CREATE TABLE donor_referral_corrections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    donor_id UUID NOT NULL REFERENCES donors(id),
    old_first_gift_id VARCHAR(255) NOT NULL,
    new_first_gift_id VARCHAR(255),
    old_referrer_id UUID REFERENCES donors(id),
    new_referrer_id UUID REFERENCES donors(id),
    old_kind VARCHAR(4) CHECK (old_kind IN ('LINK', 'IRL')),
    new_kind VARCHAR(4) CHECK (new_kind IN ('LINK', 'IRL')),
    recorded_at TIMESTAMPTZ NOT NULL,
    rules_version INTEGER NOT NULL,
    CHECK ((old_referrer_id IS NULL) = (old_kind IS NULL)),
    CHECK ((new_referrer_id IS NULL) = (new_kind IS NULL))
);

CREATE INDEX ix_donor_referral_corrections_donor ON donor_referral_corrections(donor_id, recorded_at, id);

-- Recognize notices queued under legacy dedup keys without resending them.
CREATE INDEX ix_donor_messages_referral_identity
    ON donor_messages(donor_id, (context->>'referred_id'), (context->>'kind')) WHERE kind='REFERRAL';
CREATE INDEX ix_donor_push_referral_identity
    ON donor_push_notifications(donor_id, (context->>'referred_id'), (context->>'kind')) WHERE category='REFERRAL_CONVERTED';
