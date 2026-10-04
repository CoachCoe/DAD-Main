-- name: StaffDonorSearch :many
SELECT id,email,public_name,claimed_at,disabled FROM donors
WHERE email ILIKE sqlc.arg(term)::text ESCAPE '\'
ORDER BY email,id LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;

-- name: StaffDonorByID :one
SELECT * FROM donors WHERE id=$1;

-- name: StaffReferredPage :many
SELECT d.id,d.email,p.referral_kind,p.net_minor FROM donors d
JOIN donor_metric_projections p ON p.donor_id=d.id WHERE p.referrer_id=$1
ORDER BY d.id LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;

-- name: StaffAuditPage :many
SELECT id,actor_id,action,target,created_at,details FROM donor_staff_audit
ORDER BY created_at DESC,id LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;

-- name: StaffMessagePage :many
SELECT id,kind,attempts,completed_at,last_error,suppressed_reason,available_at
FROM donor_messages WHERE donor_id=$1
ORDER BY created_at DESC,id LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;

-- name: StaffMessageByID :one
SELECT * FROM donor_messages WHERE id=$1;

-- name: MessageByDedupKey :one
SELECT * FROM donor_messages WHERE dedup_key=$1;

-- name: RecentDonorMessage :one
SELECT EXISTS(SELECT 1 FROM donor_messages WHERE donor_id=$1 AND kind=$2 AND created_at>$3);

-- name: StaffActionPage :many
SELECT id,actor_id,kind,target_id,amount_minor,period_end,reason,status,completed_at
FROM donor_staff_actions WHERE donor_id=$1 ORDER BY created_at DESC,id
LIMIT 100;
