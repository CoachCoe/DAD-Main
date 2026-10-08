-- A donor session is one signed-in browser or app installation. token_hash and
-- expires_at are the credential a request presents: a browser's cookie, which
-- lasts the whole session, or the app's short-lived access token. The app also
-- holds a rotating refresh token, "<family>.<secret>": the family part never
-- changes and finds the session, and the whole token must match refresh_hash,
-- or refresh_previous_hash for a short grace after refreshed_at. Sessions
-- issued before this migration are browser sessions, unchanged.
ALTER TABLE donor_sessions
    ADD COLUMN client text NOT NULL DEFAULT 'BROWSER' CHECK (client IN ('BROWSER','APP')),
    ADD COLUMN device_name varchar(64),
    ADD COLUMN platform text CHECK (platform IN ('IOS','ANDROID')),
    ADD COLUMN last_used_at timestamptz,
    ADD COLUMN refresh_family_hash varchar(64) UNIQUE,
    ADD COLUMN refresh_hash varchar(64),
    ADD COLUMN refresh_previous_hash varchar(64),
    ADD COLUMN refreshed_at timestamptz,
    ADD COLUMN refresh_expires_at timestamptz;
UPDATE donor_sessions SET last_used_at=created_at;
ALTER TABLE donor_sessions
    ALTER COLUMN last_used_at SET NOT NULL,
    ALTER COLUMN last_used_at SET DEFAULT now(),
    ADD CHECK ((client='APP') = (refresh_family_hash IS NOT NULL AND refresh_hash IS NOT NULL AND refresh_expires_at IS NOT NULL)),
    ADD CHECK (client='APP' OR (device_name IS NULL AND platform IS NULL));

CREATE TABLE donor_session_events (
    id uuid PRIMARY KEY,
    donor_id uuid NOT NULL REFERENCES donors(id),
    session_id uuid NOT NULL REFERENCES donor_sessions(id),
    action text NOT NULL CHECK (action IN ('SIGNED_IN','REFRESH_REUSED','SIGNED_OUT_SESSION','SIGNED_OUT_OTHERS','SIGNED_OUT_EVERYWHERE')),
    created_at timestamptz NOT NULL DEFAULT now()
);
