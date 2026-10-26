-- name: InsertAnalyticsEvents :copyfrom
INSERT INTO analytics_events (id, name, donor_id, source, properties, occurred_at)
VALUES ($1, $2, $3, $4, $5, $6);
