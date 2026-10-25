-- name: InsertPartnerApplication :exec
-- P-1: a new partner application. The partial unique index rejects a second
-- PENDING application for the same work email.
INSERT INTO donor_partner_applications
  (id, organization, website, type, contact_name, title, work_email, phone, workforce_size, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10);

-- name: PartnerApplicationByID :one
SELECT * FROM donor_partner_applications WHERE id=$1;

-- name: PendingPartnerApplications :many
-- §8 Partnerships approval queue, oldest first.
SELECT * FROM donor_partner_applications
WHERE status='PENDING'
ORDER BY created_at ASC, id
LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;

-- name: DecidePartnerApplication :exec
-- Records an approve/reject decision (status + decider + time + reason) and, on
-- approval, the partner-org row it produced.
UPDATE donor_partner_applications
SET status=$2, decided_by=$3, decided_at=$4, decision_reason=$5, approved_partner_id=$6, updated_at=$4
WHERE id=$1 AND status='PENDING';

-- name: PromotePartnerOrg :exec
-- Promotes an approved application's organization into the partner-org anchor
-- (donor_partner_signups) that initiatives reference. Reuses the signups row so
-- the existing initiative FK is unchanged; sets the approved + §8 settings.
INSERT INTO donor_partner_signups
  (id, organization, name, email, website, message, request_hash, consent_version,
   approved_at, partner_type, workforce_size, company_code)
VALUES ($1,$2,$3,$4,$5,'','','r1-partner-approved-v1',$6,$7,$8,$9);

-- name: PartnerOrgByID :one
SELECT * FROM donor_partner_signups WHERE id=$1;

-- name: SetPartnerMatch :exec
-- §8 Partnerships: set workforce size, match rate (basis points) and cap.
UPDATE donor_partner_signups
SET workforce_size=$2, match_rate_bp=$3, match_cap_minor=$4
WHERE id=$1 AND approved_at IS NOT NULL;

-- name: UpdatePartnerProfile :exec
-- Partner admin edits the org profile (name, logo, description) (§7.2).
UPDATE donor_partner_signups
SET organization=$2, logo_url=$3, description=$4
WHERE id=$1 AND approved_at IS NOT NULL;

-- name: LinkInitiativeToPartner :exec
-- §8/P-3: link an initiative to its partner org.
UPDATE donor_initiatives SET partner_signup_id=$2, updated_at=$3 WHERE id=$1;

-- name: PartnerInitiatives :many
-- A partner's linked initiatives (for the portal dashboards), published only.
SELECT * FROM donor_initiatives
WHERE partner_signup_id=$1 AND publish_state='PUBLISHED'
ORDER BY started_on DESC, id;

-- name: InsertPartnerUser :exec
-- P-4: a partner admin invites a user (ADMIN or VIEWER). The signer of an
-- approved application is inserted as the first ADMIN (invited_by NULL).
INSERT INTO donor_partner_users (id, partner_id, email, name, role, invited_by, accepted_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (partner_id, email) DO NOTHING;

-- name: PartnerUsersForPartner :many
SELECT * FROM donor_partner_users WHERE partner_id=$1 AND NOT disabled ORDER BY created_at, id;

-- name: PartnerUserByEmail :one
SELECT * FROM donor_partner_users WHERE partner_id=$1 AND email=$2;

-- name: DisablePartnerUser :exec
-- P-4: a partner admin removes a user (soft delete).
UPDATE donor_partner_users SET disabled=true WHERE partner_id=$1 AND id=$2;

-- name: InvitePartnerUser :exec
-- P-4: a partner admin invites a VIEWER (or ADMIN) by email with a pending
-- link token. On conflict (re-invite) the token and role are refreshed.
INSERT INTO donor_partner_users (id, partner_id, email, name, role, invited_by, invite_token_hash, invite_expires_at, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (partner_id, email) DO UPDATE
SET role=EXCLUDED.role, invite_token_hash=EXCLUDED.invite_token_hash, invite_expires_at=EXCLUDED.invite_expires_at, disabled=false;

-- name: PartnerUserByInvite :one
SELECT * FROM donor_partner_users WHERE invite_token_hash=$1;

-- name: AcceptPartnerInvite :exec
-- Clears the pending invite and marks the user active on first sign-in.
UPDATE donor_partner_users
SET accepted_at=$2, invite_token_hash=NULL, invite_expires_at=NULL
WHERE id=$1;

-- name: InsertPartnerSession :exec
INSERT INTO donor_partner_sessions (id, partner_user_id, token_hash, expires_at, created_at)
VALUES ($1,$2,$3,$4,$5);

-- name: AuthenticatedPartnerUser :one
-- Resolves a live partner session to its user + org, rejecting expired/revoked
-- sessions and disabled users.
SELECT u.*, p.organization, p.approved_at
FROM donor_partner_sessions s
JOIN donor_partner_users u ON u.id = s.partner_user_id
JOIN donor_partner_signups p ON p.id = u.partner_id
WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at > sqlc.arg(as_of)::timestamptz
  AND NOT u.disabled;

-- name: RevokePartnerSession :exec
UPDATE donor_partner_sessions SET revoked_at=$2 WHERE token_hash=$1 AND revoked_at IS NULL;

-- name: PartnerSpendByCategory :many
-- P-12: spend by category across a partner's linked initiatives (totals only).
SELECT s.category, sum(s.amount_minor)::bigint AS amount_minor
FROM donor_initiative_spend s
JOIN donor_initiatives i ON i.id = s.initiative_id
WHERE i.partner_signup_id=$1 AND i.publish_state='PUBLISHED'
GROUP BY s.category
ORDER BY s.category;

-- name: PartnerOverviewTotals :one
-- P-10 tiles: committed (sum of committed_minor over the partner's published
-- initiatives), deployed (sum of their spend), and people reached (sum of
-- published verified PEOPLE metrics). Totals only, published only; an
-- unverified metric is never counted (§7.3 "hidden, never estimated").
SELECT
  coalesce((SELECT sum(i.committed_minor) FROM donor_initiatives i
            WHERE i.partner_signup_id=$1 AND i.publish_state='PUBLISHED'), 0)::bigint AS committed_minor,
  coalesce((SELECT sum(s.amount_minor) FROM donor_initiative_spend s
            JOIN donor_initiatives i ON i.id=s.initiative_id
            WHERE i.partner_signup_id=$1 AND i.publish_state='PUBLISHED'), 0)::bigint AS deployed_minor,
  coalesce((SELECT sum(m.value) FROM donor_initiative_metrics m
            JOIN donor_initiatives i ON i.id=m.initiative_id
            WHERE i.partner_signup_id=$1 AND i.publish_state='PUBLISHED'
              AND m.publish_state='PUBLISHED' AND m.unit='PEOPLE'), 0)::bigint AS people_reached;

-- name: InsertPartnerReport :exec
-- P-14: record one dashboard PDF export for the export-history list.
INSERT INTO donor_partner_reports
  (id, partner_id, exported_by, committed_minor, deployed_minor, people_reached, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7);

-- name: PartnerReports :many
-- P-14: previous exports for a partner, newest first, with the exporter name.
SELECT r.id, r.committed_minor, r.deployed_minor, r.people_reached, r.created_at, u.name AS exported_by_name
FROM donor_partner_reports r
JOIN donor_partner_users u ON u.id = r.exported_by
WHERE r.partner_id=$1
ORDER BY r.created_at DESC
LIMIT 50;

-- name: PartnerInitiativesList :many
-- P-11: the partner's published initiatives with committed + deployed totals.
SELECT i.id, i.title, i.summary, i.committed_minor,
  coalesce((SELECT sum(s.amount_minor) FROM donor_initiative_spend s WHERE s.initiative_id=i.id), 0)::bigint AS deployed_minor
FROM donor_initiatives i
WHERE i.partner_signup_id=$1 AND i.publish_state='PUBLISHED'
ORDER BY i.title;

-- name: QueuePartnerMessage :exec
-- Queues one org-level partner email intent, deduped by key so the same event
-- enqueues at most once. Delivery fans out to the org's users (PartnerMessageRecipients).
INSERT INTO donor_partner_messages (id,partner_id,kind,dedup_key,context,available_at)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (dedup_key) DO NOTHING;

-- name: NextPartnerMessage :one
SELECT * FROM donor_partner_messages WHERE completed_at IS NULL AND attempts<5 AND available_at<=$1
ORDER BY available_at,id LIMIT 1 FOR UPDATE SKIP LOCKED;

-- name: PartnerMessageByID :one
SELECT * FROM donor_partner_messages WHERE id=$1;

-- name: RenderPartnerMessage :exec
UPDATE donor_partner_messages SET subject=$2,body=$3 WHERE id=$1;

-- name: ClaimPartnerMessage :exec
UPDATE donor_partner_messages SET available_at=$2 WHERE id=$1;

-- name: CompletePartnerMessage :exec
UPDATE donor_partner_messages SET completed_at=$2,suppressed_reason=$3 WHERE id=$1;

-- name: FailPartnerMessage :exec
UPDATE donor_partner_messages SET attempts=attempts+1,last_error=$2,available_at=$3 WHERE id=$1;

-- name: PartnerMessageRecipients :many
-- The org's users who can receive mail: accepted (completed first sign-in) and
-- not disabled. A pending invitee has their own invite email and is not yet a
-- recipient of status notices.
SELECT email,name FROM donor_partner_users
WHERE partner_id=$1 AND disabled=false AND accepted_at IS NOT NULL
ORDER BY email;

-- name: PartnerForMessage :one
SELECT id,organization,approved_at FROM donor_partner_signups WHERE id=$1;

-- name: PartnersDueMonthlyReport :many
-- Approved partners with at least one published initiative that have not had a
-- MONTHLY_REPORT queued for the given year-month. Drives the monthly sweep;
-- the dedup_key carries the period so each partner is mailed once per month.
SELECT s.id FROM donor_partner_signups s
WHERE s.approved_at IS NOT NULL
AND EXISTS(SELECT 1 FROM donor_initiatives i WHERE i.partner_signup_id=s.id AND i.publish_state='PUBLISHED')
AND NOT EXISTS(
    SELECT 1 FROM donor_partner_messages m
    WHERE m.partner_id=s.id AND m.kind='MONTHLY_REPORT'
    AND m.dedup_key='partner-monthly:'||s.id::text||':'||sqlc.arg(period)::text
)
ORDER BY s.id
LIMIT sqlc.arg(batch)::int;
