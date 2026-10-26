-- The R1 contact form is gone: partners now apply through
-- donor_partner_applications. Its leads, the donor_partner_signups rows never
-- approved, held contact details no longer needed, so they are deleted. An
-- initiative cannot be run by a lead, so a link to one is cleared. A lead still
-- referenced elsewhere would be an anomaly and is kept for review.
UPDATE donor_initiatives i SET partner_signup_id = NULL
FROM donor_partner_signups s
WHERE s.id = i.partner_signup_id AND s.approved_at IS NULL;

DELETE FROM donor_partner_signups s
WHERE s.approved_at IS NULL
AND NOT EXISTS (SELECT 1 FROM donor_partner_applications a WHERE a.approved_partner_id = s.id)
AND NOT EXISTS (SELECT 1 FROM donor_partner_users u WHERE u.partner_id = s.id)
AND NOT EXISTS (SELECT 1 FROM donor_partner_reports r WHERE r.partner_id = s.id)
AND NOT EXISTS (SELECT 1 FROM donor_partner_teams t WHERE t.partner_id = s.id)
AND NOT EXISTS (SELECT 1 FROM donor_partner_employees e WHERE e.partner_id = s.id)
AND NOT EXISTS (SELECT 1 FROM donor_partner_match_payments m WHERE m.partner_id = s.id)
AND NOT EXISTS (SELECT 1 FROM donor_team_months m WHERE m.partner_id = s.id);
