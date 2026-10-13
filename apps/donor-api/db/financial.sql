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
INSERT INTO donor_metric_projections
(id,donor_id,referrer_id,referral_kind,first_gift_id,net_minor,founding_awarded_at,
 best_streak,current_streak,at_risk,grace_ends_at,computed_at,next_refresh_at,rules_version)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (donor_id) DO UPDATE SET
referrer_id=EXCLUDED.referrer_id,referral_kind=EXCLUDED.referral_kind,first_gift_id=EXCLUDED.first_gift_id,
net_minor=EXCLUDED.net_minor,founding_awarded_at=EXCLUDED.founding_awarded_at,
best_streak=EXCLUDED.best_streak,current_streak=EXCLUDED.current_streak,at_risk=EXCLUDED.at_risk,
grace_ends_at=EXCLUDED.grace_ends_at,computed_at=EXCLUDED.computed_at,next_refresh_at=EXCLUDED.next_refresh_at,
rules_version=EXCLUDED.rules_version,refresh_failures=0,refresh_error=NULL;

-- name: ReferralTotals :one
SELECT count(*) FILTER (WHERE referral_kind='LINK' AND net_minor>0)::bigint AS link_count,
count(*) FILTER (WHERE referral_kind='IRL' AND net_minor>0)::bigint AS irl_count,
coalesce(sum(net_minor) FILTER (WHERE referral_kind='LINK'),0)::numeric AS link_minor,
coalesce(sum(net_minor) FILTER (WHERE referral_kind='IRL'),0)::numeric AS irl_minor,
coalesce(bool_and(coalesce(net_minor>=0 AND next_refresh_at>$2 AND referral_kind IN ('LINK','IRL'),false)),true)::boolean AS ready
FROM donor_metric_projections WHERE referrer_id=$1;

-- name: MissingReferredProjection :one
-- Work stays proportional to the referrer's checkouts: a gift's donor is its
-- checkout's, and the scalar subquery (NULL for a missing row or a missing
-- net) is probed per checkout, where a join lets the planner scan every
-- gift or projection once a tree is large.
SELECT EXISTS(SELECT 1 FROM donor_checkouts c WHERE c.referrer_id=$1
AND (SELECT p.net_minor FROM donor_metric_projections p WHERE p.donor_id=c.donor_id) IS NULL
AND EXISTS(SELECT 1 FROM donor_gifts g WHERE g.checkout_id=c.id))::boolean;
