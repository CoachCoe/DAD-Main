-- Employee giving queries (PRD §7.4). Phase 6.
-- Every partner-facing aggregate applies k-anonymity: a group with fewer than 5
-- linked employees is suppressed (EM-3). The threshold is inlined as 5.

-- name: PartnerByCompanyCode :one
-- Resolve an uppercase company code to its APPROVED partner org (EM-1).
SELECT id FROM donor_partner_signups
WHERE company_code = $1 AND approved_at IS NOT NULL;

-- name: UpsertEmployeeLink :exec
-- Link a donor to a partner (EM-2). Idempotent: re-running keeps the first
-- enrolment time and does not change an existing partner (one employer).
INSERT INTO donor_partner_employees (donor_id, partner_id, enrolled_at)
VALUES ($1, $2, $3)
ON CONFLICT (donor_id) DO NOTHING;

-- name: EmployeeLink :one
SELECT donor_id, partner_id, team_id, enrolled_at FROM donor_partner_employees WHERE donor_id = $1;

-- name: SetEmployeeTeam :exec
-- Place an employee on a team (EM-4). The service checks the team belongs to
-- the employee's partner before calling this.
UPDATE donor_partner_employees SET team_id = $2 WHERE donor_id = $1;

-- name: InsertTeam :exec
INSERT INTO donor_partner_teams (id, partner_id, name, created_at) VALUES ($1,$2,$3,$4);

-- name: TeamByID :one
SELECT id, partner_id, name FROM donor_partner_teams WHERE id = $1;

-- name: EmployeesEnrolledCount :one
-- The count of linked employees for a partner (EM-2 roll-up).
SELECT count(*)::bigint AS enrolled FROM donor_partner_employees WHERE partner_id = $1;

-- name: PartnerOurPeople :one
-- Our People aggregate (EM-3): enrolled count, total employee giving, and
-- average current streak across linked employees — SUPPRESSED (all zero,
-- suppressed=true) when fewer than 5 employees are linked. Individuals are
-- never returned.
WITH linked AS (
  SELECT e.donor_id FROM donor_partner_employees e WHERE e.partner_id = $1
), agg AS (
  SELECT
    (SELECT count(*) FROM linked)::bigint AS enrolled,
    coalesce((SELECT sum(g.amount_minor) FROM donor_gifts g WHERE g.donor_id IN (SELECT donor_id FROM linked)), 0)::bigint AS given_minor,
    coalesce((SELECT avg(m.current_streak) FROM donor_metric_projections m WHERE m.donor_id IN (SELECT donor_id FROM linked) AND m.current_streak IS NOT NULL), 0)::numeric AS avg_streak
)
SELECT
  (CASE WHEN enrolled < 5 THEN 0 ELSE enrolled END)::bigint AS enrolled,
  (CASE WHEN enrolled < 5 THEN 0 ELSE given_minor END)::bigint AS given_minor,
  (CASE WHEN enrolled < 5 THEN 0 ELSE round(avg_streak) END)::bigint AS avg_streak,
  (enrolled < 5) AS suppressed
FROM agg;

-- name: TeamLeaderboard :many
-- Team leaderboard (EM-5): per team, active givers, average current streak and
-- total volunteer minutes. Teams with fewer than 5 members are SUPPRESSED
-- (omitted). A team's "active givers" is members with at least one gift.
WITH members AS (
  SELECT e.team_id, e.donor_id
  FROM donor_partner_employees e
  WHERE e.partner_id = $1 AND e.team_id IS NOT NULL
), per_team AS (
  SELECT
    t.id AS team_id, t.name,
    count(mem.donor_id)::bigint AS members,
    count(mem.donor_id) FILTER (WHERE EXISTS (SELECT 1 FROM donor_gifts g WHERE g.donor_id = mem.donor_id))::bigint AS active_givers,
    coalesce(round(avg(m.current_streak)), 0)::bigint AS avg_streak,
    coalesce(sum((SELECT coalesce(sum(EXTRACT(EPOCH FROM (ev.ends_at - ev.starts_at)) / 60), 0)
                  FROM donor_event_reservations r JOIN donor_events ev ON ev.id = r.event_id
                  WHERE r.donor_id = mem.donor_id AND r.checked_in_at IS NOT NULL)), 0)::bigint AS volunteer_minutes
  FROM donor_partner_teams t
  JOIN members mem ON mem.team_id = t.id
  LEFT JOIN donor_metric_projections m ON m.donor_id = mem.donor_id
  WHERE t.partner_id = $1
  GROUP BY t.id, t.name
)
SELECT team_id, name, active_givers, avg_streak, volunteer_minutes
FROM per_team
WHERE members >= 5
ORDER BY active_givers DESC, name;

-- name: PartnerMatchTerms :one
SELECT match_rate_bp, match_cap_minor FROM donor_partner_signups WHERE id = $1;

-- name: LinkedEmployeeGiftsInYear :one
-- Total of all linked employees' gifts in a calendar year (EM-6). Counts gifts
-- made BEFORE the link too (no enrolled_at filter) — the link attributes the
-- whole giving history to the employer for matching.
SELECT coalesce(sum(g.amount_minor), 0)::bigint AS gifts_minor
FROM donor_gifts g
JOIN donor_partner_employees e ON e.donor_id = g.donor_id
WHERE e.partner_id = $1
  AND g.paid_at >= sqlc.arg(year_start)::timestamptz
  AND g.paid_at <  sqlc.arg(year_end)::timestamptz;

-- name: InsertMatchPayment :exec
-- Record an offline match payment received (EM-7). No money moves in-app.
INSERT INTO donor_partner_match_payments (id, partner_id, year, amount_minor, received_at, recorded_by)
VALUES ($1,$2,$3,$4,$5,$6);

-- name: MatchPaymentsInYear :one
SELECT coalesce(sum(amount_minor), 0)::bigint AS paid_minor
FROM donor_partner_match_payments WHERE partner_id = $1 AND year = $2;

-- name: SetPartnerWorkforce :exec
-- EM-9: workforce size, admin-set and partner-updatable.
UPDATE donor_partner_signups SET workforce_size = $2 WHERE id = $1;

-- name: PartnerOrganizationByID :one
SELECT organization FROM donor_partner_signups WHERE id = $1;

-- name: TeamIDByName :one
SELECT id FROM donor_partner_teams WHERE partner_id = $1 AND name = $2;
