-- Phase 7.1 partner notifications (§6.11 / §7.5). Partners have sessions and
-- invites (Phase 5) but no transactional email, so this adds a partner email
-- outbox mirroring donor_messages: one queued intent per org-level event,
-- deduped, rendered on first use, delivered with a lease + bounded retries.
--
-- An intent is org-scoped (it belongs to the partner, not one user) and fans
-- out at delivery to the org's active, accepted users — the same shape as a
-- donor push fanning out to each device. This keeps "approved", "metrics
-- verified", "initiative complete", and the "monthly report" sweep on one
-- pipeline. Partner emails have no per-category opt-out in R1: they are the
-- small set of essential partnership-status notices.
CREATE TABLE donor_partner_messages (
    id uuid PRIMARY KEY,
    partner_id uuid NOT NULL REFERENCES donor_partner_signups (id) ON DELETE CASCADE,
    kind varchar(24) NOT NULL,
    dedup_key varchar(200) NOT NULL UNIQUE,
    context json NOT NULL,
    available_at timestamptz NOT NULL,
    completed_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    last_error varchar(80),
    suppressed_reason varchar(80),
    subject varchar(200),
    body text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_donor_partner_messages_kind
        CHECK (kind IN ('APPROVED', 'METRICS_VERIFIED', 'INITIATIVE_COMPLETE', 'MONTHLY_REPORT'))
);
CREATE INDEX ix_donor_partner_messages_pending ON donor_partner_messages (available_at, id)
    WHERE completed_at IS NULL AND attempts < 5;
