-- An app session holds the push token of its installation, so a token lives
-- exactly as long as the sign-in that registered it: revoking the session
-- clears it, and a token is unique because it names one installation, which
-- may sign in again as anyone. APNs tokens are bound to Apple's sandbox or
-- production environment; FCM tokens have none.
ALTER TABLE donor_sessions
    ADD COLUMN push_token varchar(1024) UNIQUE,
    ADD COLUMN push_environment text CHECK (push_environment IN ('SANDBOX','PRODUCTION')),
    ADD CHECK (push_token IS NULL OR (client = 'APP' AND revoked_at IS NULL AND platform IS NOT NULL
        AND (platform = 'IOS') = (push_environment IS NOT NULL))),
    ADD CHECK (push_token IS NOT NULL OR push_environment IS NULL);

-- App notification choices start on: the operating system's permission
-- prompt is the opt-in. Earlier preference events predate them and hold NULL.
ALTER TABLE donor_preferences
    ADD COLUMN push_referral_converted boolean NOT NULL DEFAULT true,
    ADD COLUMN push_streak_at_risk boolean NOT NULL DEFAULT true,
    ADD COLUMN push_streak_milestone boolean NOT NULL DEFAULT true;
ALTER TABLE donor_preference_events
    ADD COLUMN push_referral_converted boolean,
    ADD COLUMN push_streak_at_risk boolean,
    ADD COLUMN push_streak_milestone boolean;

-- A push intent is queued with the fact that calls for it and delivered to
-- every app session of the donor holding a token; sent_to records the
-- sessions already reached, so a retry after a partial failure reaches only
-- the rest.
CREATE TABLE donor_push_notifications (
    id uuid PRIMARY KEY,
    donor_id uuid NOT NULL REFERENCES donors(id),
    category text NOT NULL CHECK (category IN ('REFERRAL_CONVERTED','STREAK_AT_RISK','STREAK_MILESTONE')),
    dedup_key varchar(160) NOT NULL UNIQUE,
    context json NOT NULL,
    available_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    last_error varchar(80),
    completed_at timestamptz,
    suppressed_reason varchar(80),
    sent_to uuid[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX donor_push_notifications_pending ON donor_push_notifications(available_at,id)
WHERE completed_at IS NULL AND attempts<5;
