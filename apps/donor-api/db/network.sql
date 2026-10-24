-- Network queries (PRD §6.5 I-2, §6.7 N-1..N-3). Phase 3.4.

-- name: NetworkTotals :one
-- N-1 / I-2: the count of still-active referrals and their COMBINED ANNUALIZED
-- giving (current monthly pledge x 12). Lapsed referrals are excluded from both
-- -- "your network funds $X a year" is a present-tense statement about who is
-- giving now, not a lifetime sum. Served by ix_..._active_referrer.
SELECT
 count(*)::bigint AS active_count,
 (coalesce(sum(current_monthly_minor),0) * 12)::bigint AS annualized_minor
FROM donor_metric_projections
WHERE referrer_id = $1 AND active;

-- name: NetworkInviteeList :many
-- N-3: people the donor brought in who OPTED IN to being shown, newest first.
-- A referral who did not opt in is never returned here; the caller reports the
-- remaining (active_count - len(list)) as an anonymous tail. public_name is the
-- donor's chosen display name (first name + last initial); active is their live
-- streak state. Only converted referrals (first_gift_id present) appear.
SELECT d.id, d.public_name, p.active
FROM donor_metric_projections p
JOIN donors d ON d.id = p.donor_id
JOIN donor_preferences pref ON pref.donor_id = p.donor_id
WHERE p.referrer_id = $1
 AND p.first_gift_id IS NOT NULL
 AND pref.network_visible
 AND d.public_name IS NOT NULL
ORDER BY p.created_at DESC;
