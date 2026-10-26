-- An initiative records the board approval behind it: a private source
-- document and the date of approval. Publishing requires one; initiatives
-- published before this rule keep their place until staff edit them. The
-- approvals given in the content spreadsheet are carried over by slug.
ALTER TABLE donor_initiatives
	ADD COLUMN board_approval_source_id UUID REFERENCES donor_content_sources (id),
	ADD COLUMN board_approved_on DATE,
	ADD CONSTRAINT ck_donor_initiatives_board_approval CHECK (
		(board_approval_source_id IS NULL) = (board_approved_on IS NULL)
	);

UPDATE donor_initiatives d
SET board_approval_source_id = s.id, board_approved_on = (i->>'approved_on')::date
FROM donor_content_head h
JOIN donor_content_publications p ON p.id = h.publication_id
CROSS JOIN json_array_elements(p.payload -> 'initiatives') AS i
JOIN donor_content_sources s ON s.id::text = i->>'board_approval_source_id'
WHERE d.slug = i->>'slug' AND d.board_approval_source_id IS NULL;
