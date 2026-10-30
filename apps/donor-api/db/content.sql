-- name: ContentSources :many
SELECT id,name,sha256 FROM donor_content_sources ORDER BY created_at DESC,id LIMIT 500;

-- name: ContentSourceByID :one
SELECT * FROM donor_content_sources WHERE id=$1;

-- name: ContentSourceIDs :many
SELECT id FROM donor_content_sources WHERE id=ANY($1::uuid[]);

-- name: InsertContentSource :exec
INSERT INTO donor_content_sources(id,actor_id,name,sha256,encrypted_data) VALUES($1,$2,$3,$4,$5);
