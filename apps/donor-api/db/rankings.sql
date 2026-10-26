-- name: RefreshRankings :exec
-- Recompute the whole ranking population in one set-based pass (PRD §13 risk #6:
-- periodic, never per request). The eligible population is donors with a current
-- streak who have not opted out; ties break on the earlier streak start, so a
-- longer-standing identical streak ranks higher. national_percentile is the
-- top-percentile bucket (1 = top 1%); city_rank is 1-based within donor.city.
-- Replaces the prior snapshot wholesale.
WITH eligible AS (
  SELECT d.id AS donor_id, d.city AS city,
         p.current_streak AS streak, p.streak_since AS since
  FROM donors d
  JOIN donor_metric_projections p ON p.donor_id = d.id
  LEFT JOIN donor_preferences pref ON pref.donor_id = d.id
  WHERE d.disabled = false
    AND p.current_streak IS NOT NULL
    AND p.current_streak > 0
    AND COALESCE(pref.rankings_opted_out, false) = false
),
ranked AS (
  SELECT donor_id, city, since,
         -- percent_rank is 0 for the top; invert to a top-percentile 1..100
         -- (1 = best). A single-row population is the top, percentile 1.
         GREATEST(1, LEAST(100,
           CEIL((1.0 - PERCENT_RANK() OVER (ORDER BY streak ASC, since DESC)) * 100)::int
         )) AS national_percentile,
         CASE WHEN city IS NOT NULL
              THEN RANK() OVER (PARTITION BY city ORDER BY streak DESC, since ASC)::int
         END AS city_rank
  FROM eligible
)
INSERT INTO donor_rankings (donor_id, national_percentile, city, city_rank, computed_at)
SELECT donor_id, national_percentile, city, city_rank, sqlc.arg(computed_at)::timestamptz
FROM ranked
ON CONFLICT (donor_id) DO UPDATE SET
  national_percentile = EXCLUDED.national_percentile,
  city = EXCLUDED.city,
  city_rank = EXCLUDED.city_rank,
  computed_at = EXCLUDED.computed_at;

-- name: PruneStaleRankings :exec
-- Remove rows for donors no longer in the eligible population (opted out,
-- lapsed, disabled) as of the given refresh time.
DELETE FROM donor_rankings WHERE computed_at < $1;

-- name: DonorRanking :one
-- A donor's own ranking row (K-3: only ever their own position).
SELECT national_percentile, city, city_rank, computed_at
FROM donor_rankings WHERE donor_id = $1;

-- name: LastRankingRefresh :one
-- The most recent ranking computation time, or NULL if never computed. Used to
-- throttle the global refresh to a periodic cadence inside the projections worker.
SELECT MAX(computed_at)::timestamptz AS last FROM donor_rankings;

-- name: AwardRankingBadges :exec
-- Top 2% and Top 50 City for every donor the rollup just ranked into them,
-- queueing the badge-earned push for each new one, in one pass.
WITH qualifying AS (
  SELECT donor_id, 'NAT2'::varchar AS code FROM donor_rankings
  WHERE computed_at = sqlc.arg(computed_at)::timestamptz AND national_percentile <= 2
  UNION ALL
  SELECT donor_id, 'CITY50'::varchar FROM donor_rankings
  WHERE computed_at = sqlc.arg(computed_at)::timestamptz AND city_rank <= 50
), earned AS (
  INSERT INTO donor_badges (donor_id, code, source, earned_at)
  SELECT donor_id, code, 'AUTO', sqlc.arg(computed_at)::timestamptz FROM qualifying
  ON CONFLICT (donor_id, code) DO NOTHING
  RETURNING donor_id, code
)
INSERT INTO donor_push_notifications (id, donor_id, category, dedup_key, context, available_at, expires_at)
SELECT gen_random_uuid(), e.donor_id, 'BADGE_EARNED', 'push:badge:' || e.donor_id::text || ':' || e.code,
  json_build_object('code', e.code), sqlc.arg(computed_at)::timestamptz, sqlc.arg(expires_at)::timestamptz
FROM earned e
WHERE EXISTS (SELECT 1 FROM donor_sessions s WHERE s.donor_id = e.donor_id AND s.push_token IS NOT NULL
  AND coalesce(s.refresh_expires_at, s.expires_at) > sqlc.arg(computed_at)::timestamptz)
ON CONFLICT (dedup_key) DO NOTHING;
