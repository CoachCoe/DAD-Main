-- Phase 7.1 notifications matrix: complete the §6.11 donor triggers that were
-- deferred. Streak-lapsed (grace day 7, distinct from the day-0/day-5 recovery
-- reminders) and badge-earned each get a push category and a per-category
-- opt-out. Payment-failure email stays locked on and is not a toggle (§6.11).
--
-- "Someone joined through your code" is NOT added here: in this model a referral
-- "joins" the donor's network at its first verified gift (N-1/N-3 count the
-- converted-referral total), which is exactly when REFERRAL_CONVERTED already
-- fires. A separate trigger would double-notify the same event.

-- New app-notification choices start on, like the existing ones: the OS
-- permission prompt is the opt-in. Earlier preference events predate them and
-- hold NULL.
ALTER TABLE donor_preferences
    ADD COLUMN push_streak_lapsed boolean NOT NULL DEFAULT true,
    ADD COLUMN push_badge_earned boolean NOT NULL DEFAULT true;
ALTER TABLE donor_preference_events
    ADD COLUMN push_streak_lapsed boolean,
    ADD COLUMN push_badge_earned boolean;

-- Widen the push category domain to carry the new triggers. STREAK_LAPSED
-- announces the run ended after grace; BADGE_EARNED fires once per badge at the
-- earn moment.
ALTER TABLE donor_push_notifications
    DROP CONSTRAINT donor_push_notifications_category_check,
    ADD CONSTRAINT donor_push_notifications_category_check
        CHECK (category IN (
            'REFERRAL_CONVERTED','STREAK_AT_RISK','STREAK_MILESTONE',
            'EVENT_PROMOTED','EVENT_REMINDER',
            'STREAK_LAPSED','BADGE_EARNED'
        ));
