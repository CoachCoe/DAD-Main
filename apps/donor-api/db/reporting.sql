-- R1 staff reporting (PRD §4 success metrics / docs/analytics.md).
--
-- Every metric here is computed from COMMITTED SERVER FACTS — gifts, billing
-- periods, refunds, and the donor metric projection — never from the browser
-- analytics_events stream, which docs/analytics.md marks deferred by product
-- agreement ("metrics requiring it must be marked unavailable, never displayed
-- as fabricated zeros"). The one browser-only metric (seven-day share rate) has
-- no query here; the service reports it unavailable.
--
-- Attribution is the FROZEN source on the donor's own projection row
-- (referrer_id / referral_kind, set at the earliest verified payment), not the
-- browser campaign parameters. DIRECT = no referrer. A donor's "first gift" is
-- the projection's first_gift_id, which references donor_gifts.stripe_object_id.
--
-- Reporting periods are half-open [start, end) on gift paid_at / period due_at,
-- in UTC. The caller supplies $1=start, $2=end; the service echoes the cutoff
-- and timezone alongside the numbers.

-- name: ReportFoundingDonors :one
-- Distinct donors whose permanent founding award was granted (projection
-- founding_awarded_at). A later refund/cancellation does not remove it. Scoped
-- to awards recorded in the period so the report is period-attributable.
SELECT count(*)::bigint AS founding
FROM donor_metric_projections
WHERE founding_awarded_at >= $1 AND founding_awarded_at < $2;

-- name: ReportFirstGiftBySource :many
-- First-gift conversions grouped by frozen source (DIRECT/LINK/IRL). A "first
-- gift" is the gift the projection points at (first_gift_id); its paid_at must
-- fall in the period. gift mix (frequency) rides along so first-gift donor mix
-- is reported without confusing gifts with donors.
SELECT
	CASE WHEN p.referral_kind IS NULL THEN 'DIRECT' ELSE p.referral_kind END AS source,
	g.frequency,
	count(*)::bigint AS donors,
	coalesce(sum(g.amount_minor), 0)::bigint AS gross_minor
FROM donor_metric_projections p
JOIN donor_gifts g ON g.stripe_object_id = p.first_gift_id
WHERE p.first_gift_id IS NOT NULL
	AND g.paid_at >= $1 AND g.paid_at < $2
GROUP BY source, g.frequency;

-- name: ReportGiftMix :many
-- Successful gift count and collected dollars by frequency over the period.
-- Counts every gift (not just first gifts), so recurring volume is visible.
SELECT g.frequency, count(*)::bigint AS gifts, coalesce(sum(g.amount_minor), 0)::bigint AS collected_minor
FROM donor_gifts g
WHERE g.paid_at >= $1 AND g.paid_at < $2
GROUP BY g.frequency;

-- name: ReportViralCoefficient :one
-- Converting direct referrals credited to referrers, over the donors who
-- referred in the period. Gross counts every attributed referral (first_gift
-- present); qualified counts only those still net-positive after refunds — the
-- two measures are reported separately so refunds cannot silently rewrite
-- acquisition. cohort is the number of donors who have at least one referral.
-- No second-level credit: a referred donor's own referrals are not counted here.
SELECT
	count(DISTINCT referrer.donor_id)::bigint AS cohort,
	coalesce(sum(CASE WHEN referred.first_gift_id IS NOT NULL THEN 1 ELSE 0 END), 0)::bigint AS gross_conversions,
	coalesce(sum(CASE WHEN referred.net_minor > 0 THEN 1 ELSE 0 END), 0)::bigint AS qualified_conversions
FROM donor_metric_projections referred
JOIN donor_metric_projections referrer ON referrer.donor_id = referred.referrer_id
WHERE referred.referrer_id IS NOT NULL
	AND referred.created_at >= $1 AND referred.created_at < $2;

-- name: ReportReferralDollars :one
-- Actual collected referred gifts less successful refunds, direct-only, split
-- LINK/IRL. net_minor on the referred row is already net of refunds (CHECK
-- >= 0), so summing it over referred rows in the period gives realized referral
-- dollars without re-joining refunds.
SELECT
	coalesce(sum(net_minor) FILTER (WHERE referral_kind = 'LINK'), 0)::bigint AS link_minor,
	coalesce(sum(net_minor) FILTER (WHERE referral_kind = 'IRL'), 0)::bigint AS irl_minor
FROM donor_metric_projections
WHERE referrer_id IS NOT NULL
	AND created_at >= $1 AND created_at < $2;

-- name: ReportInvoluntaryChurn :one
-- Involuntary churn/recovery from billing periods. A FAILED period whose retry
-- deadline (due_at + 14 days, matching the streak grace rule) has passed by the
-- observation cutoff ($3) is lost value, using the period's scheduled gift
-- amount once. A verified gift collected for that same period (period_id) is
-- recovered value. Cohort is failed periods whose due_at falls in [start, end).
-- Each period counts once; amounts are never multiplied by retry/webhook count.
WITH failed AS (
	SELECT bp.id, bp.due_at, c.amount_minor AS scheduled_minor,
		EXISTS(SELECT 1 FROM donor_gifts g WHERE g.period_id = bp.id) AS recovered,
		coalesce((SELECT sum(g.amount_minor) FROM donor_gifts g WHERE g.period_id = bp.id), 0)::bigint AS recovered_minor
	FROM donor_billing_periods bp
	JOIN donor_checkouts c ON c.id = bp.checkout_id
	WHERE bp.status = 'FAILED'
		AND bp.due_at >= $1 AND bp.due_at < $2
		AND bp.due_at + INTERVAL '14 days' <= $3
)
SELECT
	count(*)::bigint AS failed_periods,
	coalesce(sum(CASE WHEN NOT recovered THEN scheduled_minor ELSE 0 END), 0)::bigint AS lost_minor,
	count(*) FILTER (WHERE recovered)::bigint AS recovered_periods,
	coalesce(sum(recovered_minor), 0)::bigint AS recovered_minor
FROM failed;

-- name: ReportTimeToFirstGift :one
-- Median elapsed seconds from a donor's account creation to their verified
-- first collection, over donors whose first gift was collected in the period.
-- sample is the number of such donors; the service reports it so the median
-- carries its sample size. (Landing-journey start is a browser fact that is not
-- collected; account creation is the earliest committed server anchor.)
SELECT
	count(*)::bigint AS sample,
	coalesce(
		percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (g.paid_at - d.created_at))),
		0
	)::bigint AS median_seconds
FROM donor_metric_projections p
JOIN donor_gifts g ON g.stripe_object_id = p.first_gift_id
JOIN donors d ON d.id = p.donor_id
WHERE p.first_gift_id IS NOT NULL
	AND g.paid_at >= $1 AND g.paid_at < $2
	AND g.paid_at >= d.created_at;

-- name: ReportRetention :one
-- Month-three and month-six retention from the current projection streak. A
-- donor counts toward a milestone's denominator when their best_streak shows
-- they reached it; current_streak >= milestone counts toward the numerator.
-- Scoped to donors who began a recurring streak (streak_since) in the period.
-- Caveat echoed by the service: current snapshots approximate historical
-- retention; the streak projection is the authoritative within-R1 source.
SELECT
	count(*) FILTER (WHERE best_streak >= 3)::bigint AS reached_m3,
	count(*) FILTER (WHERE current_streak >= 3)::bigint AS retained_m3,
	count(*) FILTER (WHERE best_streak >= 6)::bigint AS reached_m6,
	count(*) FILTER (WHERE current_streak >= 6)::bigint AS retained_m6
FROM donor_metric_projections
WHERE streak_since IS NOT NULL
	AND streak_since >= $1 AND streak_since < $2;
