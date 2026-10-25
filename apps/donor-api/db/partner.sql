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

-- name: PartnerSpendByCategory :many
-- P-12: spend by category across a partner's linked initiatives (totals only).
SELECT s.category, sum(s.amount_minor)::bigint AS amount_minor
FROM donor_initiative_spend s
JOIN donor_initiatives i ON i.id = s.initiative_id
WHERE i.partner_signup_id=$1 AND i.publish_state='PUBLISHED'
GROUP BY s.category
ORDER BY s.category;
