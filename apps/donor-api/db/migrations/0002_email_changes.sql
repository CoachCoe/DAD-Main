CREATE TABLE donor_email_changes (
    id uuid PRIMARY KEY,
    donor_id uuid NOT NULL REFERENCES donors(id),
    old_email varchar(320) NOT NULL,
    new_email varchar(320) NOT NULL,
    stage text NOT NULL CHECK (stage IN ('CURRENT','NEW','COMPLETED','CANCELED')),
    token_hash varchar(64) NOT NULL UNIQUE,
    encrypted_token bytea,
    expires_at timestamptz NOT NULL,
    sent_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CHECK (old_email <> new_email)
);
CREATE INDEX donor_email_changes_pending ON donor_email_changes(available_at,id)
WHERE sent_at IS NULL AND stage IN ('CURRENT','NEW') AND attempts < 5;
CREATE INDEX donor_email_changes_donor ON donor_email_changes(donor_id);

CREATE TABLE donor_identity_events (
    id uuid PRIMARY KEY,
    donor_id uuid NOT NULL REFERENCES donors(id),
    email_change_id uuid NOT NULL REFERENCES donor_email_changes(id),
    action text NOT NULL CHECK (action IN ('REQUESTED','CURRENT_VERIFIED','CHANGED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(email_change_id,action)
);
