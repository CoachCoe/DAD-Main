-- name: QueueHealth :many
WITH queues AS (
 SELECT 'stripe'::text AS queue,available_at AS due_at,attempts>=10 AS parked FROM donor_stripe_events WHERE processed_at IS NULL
 UNION ALL
 SELECT 'messages',available_at,attempts>=5 FROM donor_messages WHERE completed_at IS NULL
 UNION ALL
 SELECT 'login-email',available_at,attempts>=5 FROM donor_login_emails WHERE completed_at IS NULL
 UNION ALL
 SELECT 'email-changes',available_at,attempts>=5 FROM donor_email_changes WHERE stage IN ('CURRENT','NEW') AND sent_at IS NULL
 UNION ALL
 SELECT 'projections',next_refresh_at,refresh_error IS NOT NULL FROM donor_metric_projections
 WHERE next_refresh_at<=sqlc.arg(as_of)::timestamptz
), names AS (
 SELECT unnest(ARRAY['stripe','messages','login-email','email-changes','projections']) AS queue
)
SELECT n.queue,count(q.queue)::bigint AS due,
 count(q.queue) FILTER(WHERE q.parked)::bigint AS parked,
 coalesce(extract(epoch FROM sqlc.arg(as_of)::timestamptz-min(q.due_at)),0)::bigint AS oldest_seconds
FROM names n LEFT JOIN queues q ON q.queue=n.queue AND q.due_at<=sqlc.arg(as_of)::timestamptz
GROUP BY n.queue ORDER BY n.queue;
