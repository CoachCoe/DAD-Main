ALTER TABLE donor_partner_sessions
    ADD COLUMN app boolean NOT NULL DEFAULT false,
    ADD COLUMN platform text CHECK (platform IN ('IOS','ANDROID')),
    ADD COLUMN push_token varchar(1024) UNIQUE,
    ADD COLUMN push_environment text CHECK (push_environment IN ('SANDBOX','PRODUCTION')),
    ADD CHECK (push_token IS NULL OR (app AND revoked_at IS NULL AND platform IS NOT NULL
        AND (platform='IOS')=(push_environment IS NOT NULL))),
    ADD CHECK (push_token IS NOT NULL OR push_environment IS NULL);

CREATE TABLE donor_partner_push_notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES donor_partner_sessions(id) ON DELETE CASCADE,
    partner_id uuid NOT NULL REFERENCES donor_partner_signups(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('APPROVED','METRICS_VERIFIED','INITIATIVE_COMPLETE')),
    dedup_key text NOT NULL UNIQUE,
    available_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    last_error varchar(80),
    completed_at timestamptz,
    suppressed_reason varchar(80)
);
CREATE INDEX donor_partner_push_pending ON donor_partner_push_notifications(available_at,id)
WHERE completed_at IS NULL AND attempts<5;
