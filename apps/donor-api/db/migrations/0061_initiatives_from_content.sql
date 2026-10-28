-- Initiatives have one home: donor_initiatives, edited in the staff
-- initiative editor. Initiatives published through the content spreadsheet are
-- copied in once, published, so the public pages keep showing them. They are
-- planned, so they stay out of the daily rotation until staff add them. Their
-- partner is linked when an approved partner has the same name; the type,
-- location and region the spreadsheet never had read "To be announced". The
-- spreadsheet's board-approval sources stay in its publication history.
INSERT INTO donor_initiatives (id, slug, title, summary, type, location, region, started_on, status,
	partner_signup_id, publish_state, published_at, in_rotation, created_at, updated_at)
SELECT gen_random_uuid(), i->>'slug', i->>'title', i->>'summary', 'Planned', 'To be announced', 'To be announced',
	(i->>'planned_start')::date, 'ACTIVE',
	(SELECT s.id FROM donor_partner_signups s
	 WHERE s.approved_at IS NOT NULL AND lower(s.organization) = lower(i->>'partner')
	 ORDER BY s.approved_at, s.id LIMIT 1),
	'PUBLISHED', p.created_at, false, p.created_at, p.created_at
FROM donor_content_head h
JOIN donor_content_publications p ON p.id = h.publication_id
CROSS JOIN json_array_elements(p.payload -> 'initiatives') AS i
ON CONFLICT (slug) DO NOTHING;
