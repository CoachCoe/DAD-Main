-- PRD §6.11: every push category can be turned off, and volunteers get a push
-- when an event is published near them (E-1). The event categories had no
-- choice of their own. New choices start on, like the other app
-- notifications; earlier preference events predate them and hold NULL.
-- Cancellation notices stay on: they tell a donor their place is gone.
ALTER TABLE donor_preferences
    ADD COLUMN push_event_nearby boolean NOT NULL DEFAULT true,
    ADD COLUMN push_event_reminder boolean NOT NULL DEFAULT true,
    ADD COLUMN push_event_promoted boolean NOT NULL DEFAULT true;
ALTER TABLE donor_preference_events
    ADD COLUMN push_event_nearby boolean,
    ADD COLUMN push_event_reminder boolean,
    ADD COLUMN push_event_promoted boolean;

ALTER TABLE donor_push_notifications
    DROP CONSTRAINT donor_push_notifications_category_check,
    ADD CONSTRAINT donor_push_notifications_category_check
        CHECK (category IN (
            'REFERRAL_CONVERTED','STREAK_AT_RISK','STREAK_MILESTONE',
            'EVENT_PROMOTED','EVENT_REMINDER','EVENT_CANCELLED','EVENT_NEARBY',
            'STREAK_LAPSED','BADGE_EARNED'
        ));
