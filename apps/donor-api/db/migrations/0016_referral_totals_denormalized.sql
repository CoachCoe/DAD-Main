-- The four dashboard referral totals are denormalized onto the referrer's own
-- donor_metric_projections row and maintained incrementally in the projection
-- write path (see ApplyReferralDelta), so ReferralTotals reads a single row
-- instead of scanning every referred row per dashboard view. This is a pure
-- optimization: the stored values equal what the FILTER aggregate produces.
ALTER TABLE donor_metric_projections
	ADD COLUMN referred_link_count bigint NOT NULL DEFAULT 0,
	ADD COLUMN referred_irl_count bigint NOT NULL DEFAULT 0,
	ADD COLUMN referred_link_minor bigint NOT NULL DEFAULT 0,
	ADD COLUMN referred_irl_minor bigint NOT NULL DEFAULT 0;

-- Backfill every referrer's own row from its referred rows, computing exactly
-- what ReferralTotals produces today: counts filter on net_minor>0, the minor
-- sums are unfiltered by sign (net_minor is CHECK >= 0). A referrer with no
-- referred rows keeps its DEFAULT 0 values.
UPDATE donor_metric_projections r SET
	referred_link_count  = agg.link_count,
	referred_irl_count   = agg.irl_count,
	referred_link_minor  = agg.link_minor,
	referred_irl_minor   = agg.irl_minor
FROM (
	SELECT referrer_id,
		count(*) FILTER (WHERE referral_kind = 'LINK' AND net_minor > 0)::bigint AS link_count,
		count(*) FILTER (WHERE referral_kind = 'IRL'  AND net_minor > 0)::bigint AS irl_count,
		coalesce(sum(net_minor) FILTER (WHERE referral_kind = 'LINK'), 0)::bigint AS link_minor,
		coalesce(sum(net_minor) FILTER (WHERE referral_kind = 'IRL'),  0)::bigint AS irl_minor
	FROM donor_metric_projections
	WHERE referrer_id IS NOT NULL
	GROUP BY referrer_id
) agg
WHERE r.donor_id = agg.referrer_id;
