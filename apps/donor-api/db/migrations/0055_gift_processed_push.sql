ALTER TABLE donor_preferences ADD COLUMN push_gift_processed boolean NOT NULL DEFAULT true;
ALTER TABLE donor_preference_events ADD COLUMN push_gift_processed boolean;

ALTER TABLE donor_push_notifications
    DROP CONSTRAINT donor_push_notifications_category_check,
    ADD CONSTRAINT donor_push_notifications_category_check CHECK (category IN (
        'REFERRAL_CONVERTED','STREAK_AT_RISK','STREAK_MILESTONE',
        'EVENT_PROMOTED','EVENT_REMINDER','EVENT_CANCELLED','EVENT_NEARBY',
        'STREAK_LAPSED','BADGE_EARNED','GIFT_PROCESSED'
    ));
