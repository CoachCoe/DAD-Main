-- name: InsertFinancialMessage :exec
INSERT INTO donor_messages (id,donor_id,kind,dedup_key,context,available_at)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (dedup_key) DO NOTHING;

-- name: QueueLoginEmail :exec
INSERT INTO donor_login_emails (id,donor_id,available_at,created_at)
SELECT $1,$2,$3,$3 WHERE NOT EXISTS
(SELECT 1 FROM donor_login_emails WHERE donor_id=$2 AND created_at>$3::timestamptz-interval '1 minute');

-- name: DueProjectionDonor :one
SELECT d.id FROM donors d LEFT JOIN donor_metric_projections p ON p.donor_id=d.id
WHERE p.id IS NULL OR p.next_refresh_at<=$1 ORDER BY d.id
LIMIT 1 FOR UPDATE OF d SKIP LOCKED;
