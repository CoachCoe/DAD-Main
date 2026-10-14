-- name: GiftHistory :many
SELECT g.stripe_object_id,g.donor_id,g.amount_minor,g.currency,g.frequency,g.paid_at,g.period_id,
g.referrer_id,g.referral_kind,c.referral_expires_at
FROM donor_gifts g JOIN donor_checkouts c ON c.id=g.checkout_id WHERE g.donor_id=$1;

-- name: RefundHistory :many
SELECT r.*, g.stripe_object_id FROM donor_refunds r
JOIN donor_gifts g ON g.id=r.gift_id WHERE g.donor_id=$1;

-- name: PeriodHistory :many
SELECT * FROM donor_billing_periods WHERE donor_id=$1;

-- name: RefundObservations :many
SELECT r.stripe_refund_id, o.status, o.observed_at
FROM donor_refund_observations o JOIN donor_refunds r ON r.id=o.refund_id
JOIN donor_gifts g ON g.id=r.gift_id
WHERE g.donor_id=$1 AND o.observed_at <= $2 ORDER BY o.observed_at, o.id;

-- name: PauseWindows :many
SELECT p.starts_at, p.resumes_at, s.stops_at, r.next_renewal
FROM donor_pauses p JOIN donor_subscriptions s ON s.attempt_id=p.attempt_id
LEFT JOIN donor_resumptions r ON r.pause_id=p.id AND r.confirmed_at IS NOT NULL
WHERE p.donor_id=$1 AND p.confirmed_at IS NOT NULL;

-- name: StoppedPeriods :many
SELECT stops_at FROM donor_subscriptions WHERE donor_id=$1 AND stops_at IS NOT NULL;

-- name: Projection :one
SELECT * FROM donor_metric_projections WHERE donor_id=$1;

-- name: SaveProjection :exec
-- The four referred_* columns carry this donor's own denormalized referral
-- totals (set from ReferralTotalsFromReferred before each save, so the write
-- is authoritative). Listing them in the DO UPDATE SET makes the referrer's
-- own recompute the reconciling backstop: it resets the totals to the live
-- aggregate on every refresh, so an incremental ApplyReferralDelta that was
-- dropped (because the referrer row did not exist yet) still converges once
-- the referrer is projected. ApplyReferralDelta remains the fast path and
-- never collides with this write: it targets the *referrer's* row while this
-- save writes *this donor's* row.
INSERT INTO donor_metric_projections
(id,donor_id,referrer_id,referral_kind,first_gift_id,net_minor,founding_awarded_at,
 best_streak,current_streak,at_risk,grace_ends_at,computed_at,next_refresh_at,rules_version,
 referred_link_count,referred_irl_count,referred_link_minor,referred_irl_minor)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (donor_id) DO UPDATE SET
referrer_id=EXCLUDED.referrer_id,referral_kind=EXCLUDED.referral_kind,first_gift_id=EXCLUDED.first_gift_id,
net_minor=EXCLUDED.net_minor,founding_awarded_at=EXCLUDED.founding_awarded_at,
best_streak=EXCLUDED.best_streak,current_streak=EXCLUDED.current_streak,at_risk=EXCLUDED.at_risk,
grace_ends_at=EXCLUDED.grace_ends_at,computed_at=EXCLUDED.computed_at,next_refresh_at=EXCLUDED.next_refresh_at,
rules_version=EXCLUDED.rules_version,refresh_failures=0,refresh_error=NULL,
referred_link_count=EXCLUDED.referred_link_count,referred_irl_count=EXCLUDED.referred_irl_count,
referred_link_minor=EXCLUDED.referred_link_minor,referred_irl_minor=EXCLUDED.referred_irl_minor;

-- name: ReferralTotalsFromReferred :one
-- Recompute a referrer's four denormalized totals from its referred rows,
-- computed exactly as the OLD ReferralTotals aggregate (and the 0016 backfill)
-- did: counts filter on net_minor>0, the minor sums are unfiltered by sign
-- (net_minor is CHECK >= 0). Runs only on the referrer's own projection refresh
-- (projectFacts), never on a dashboard view, so the per-view read stays a
-- single-row lookup. This is the reconciling backstop that makes the
-- incremental ApplyReferralDelta converge for every referred-vs-referrer
-- projection ordering: whenever a referrer is projected it SETs its own totals
-- to the live aggregate, so a dropped delta cannot persist.
SELECT
count(*) FILTER (WHERE referral_kind='LINK' AND net_minor>0)::bigint AS link_count,
count(*) FILTER (WHERE referral_kind='IRL' AND net_minor>0)::bigint AS irl_count,
coalesce(sum(net_minor) FILTER (WHERE referral_kind='LINK'),0)::bigint AS link_minor,
coalesce(sum(net_minor) FILTER (WHERE referral_kind='IRL'),0)::bigint AS irl_minor
FROM donor_metric_projections WHERE referrer_id=$1;

-- name: ApplyReferralDelta :exec
-- Adjust a referrer's denormalized referred totals by the signed delta a single
-- referred donor's projection change contributes. The update is atomic in SQL
-- (col = col + delta) so sibling referred donors, each under their own donor
-- lock, can update the same referrer row concurrently without a read-modify-write
-- race. It affects 0 rows when the referrer has no projection row yet; the
-- referred donor's next recompute reapplies the delta once that row exists.
UPDATE donor_metric_projections SET
referred_link_count=referred_link_count+$2,
referred_irl_count=referred_irl_count+$3,
referred_link_minor=referred_link_minor+$4,
referred_irl_minor=referred_irl_minor+$5
WHERE donor_id=$1;

-- name: ReferralTotals :one
-- The totals are maintained incrementally on the referrer's own row (see
-- ApplyReferralDelta), so this reads them directly instead of scanning every
-- referred row. Readiness stays a bounded freshness check over the referred
-- rows: it is inherently a property of the set of referred rows (it flips when
-- any one's next_refresh_at moves), so denormalizing it would risk changing the
-- observable "show referrals only when all referred projections are fresh" rule;
-- per the plan we keep it exact rather than trade correctness for marginal perf.
-- dashboard() reads the referrer's own row first and returns "preparing" before
-- calling this, so own-row always exists here.
SELECT own.referred_link_count::bigint AS link_count,
own.referred_irl_count::bigint AS irl_count,
own.referred_link_minor::numeric AS link_minor,
own.referred_irl_minor::numeric AS irl_minor,
coalesce((SELECT bool_and(coalesce(ref.net_minor>=0 AND ref.next_refresh_at>$2 AND ref.referral_kind IN ('LINK','IRL'),false))
FROM donor_metric_projections ref WHERE ref.referrer_id=$1),true)::boolean AS ready
FROM donor_metric_projections own WHERE own.donor_id=$1;

-- name: MissingReferredProjection :one
-- Work stays proportional to the referrer's checkouts: a gift's donor is its
-- checkout's, and the scalar subquery (NULL for a missing row or a missing
-- net) is probed per checkout, where a join lets the planner scan every
-- gift or projection once a tree is large.
SELECT EXISTS(SELECT 1 FROM donor_checkouts c WHERE c.referrer_id=$1
AND (SELECT p.net_minor FROM donor_metric_projections p WHERE p.donor_id=c.donor_id) IS NULL
AND EXISTS(SELECT 1 FROM donor_gifts g WHERE g.checkout_id=c.id))::boolean;
