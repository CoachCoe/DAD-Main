-- PRD §8 Growth: staff can cancel a published event, which releases every
-- reservation and notifies the attendees and the waitlist. A cancelled event
-- stays readable by id so a notice's link explains what happened.
ALTER TABLE donor_events ADD COLUMN cancelled_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE donor_push_notifications
    DROP CONSTRAINT donor_push_notifications_category_check,
    ADD CONSTRAINT donor_push_notifications_category_check
        CHECK (category IN (
            'REFERRAL_CONVERTED','STREAK_AT_RISK','STREAK_MILESTONE',
            'EVENT_PROMOTED','EVENT_REMINDER','EVENT_CANCELLED',
            'STREAK_LAPSED','BADGE_EARNED'
        ));
