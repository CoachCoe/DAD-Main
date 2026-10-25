-- Event reminder pushes (PRD §6.10 E-7). Phase 4.6.
--
-- A reminder push goes to every reserved donor 24 hours and 2 hours before an
-- event. It reuses the existing push pipeline, so the only schema change is to
-- admit the new category (0036 added EVENT_PROMOTED; this adds EVENT_REMINDER).
-- The two windows are deduped per (event, donor, window) by the push intent's
-- dedup_key, so each window fires at most once.
ALTER TABLE donor_push_notifications DROP CONSTRAINT donor_push_notifications_category_check;
ALTER TABLE donor_push_notifications ADD CONSTRAINT donor_push_notifications_category_check
	CHECK (category IN ('REFERRAL_CONVERTED', 'STREAK_AT_RISK', 'STREAK_MILESTONE', 'EVENT_PROMOTED', 'EVENT_REMINDER'));
