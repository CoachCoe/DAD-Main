-- name: ContentSources :many
SELECT id,name,sha256 FROM donor_content_sources ORDER BY created_at DESC,id LIMIT 500;

-- name: ContentPublications :many
SELECT id,created_at FROM donor_content_publications ORDER BY created_at DESC,id LIMIT 20;

-- name: ContentSourceByID :one
SELECT * FROM donor_content_sources WHERE id=$1;

-- name: ContentSourceIDs :many
SELECT id FROM donor_content_sources WHERE id=ANY($1::uuid[]);

-- name: InsertContentSource :exec
INSERT INTO donor_content_sources(id,actor_id,name,sha256,encrypted_data) VALUES($1,$2,$3,$4,$5);

-- name: InsertPublication :exec
INSERT INTO donor_content_publications(id,actor_id,payload,created_at) VALUES($1,$2,$3,$4);

-- name: PublishContentHead :exec
INSERT INTO donor_content_head(id,publication_id) VALUES('r1',$1)
ON CONFLICT(id) DO UPDATE SET publication_id=EXCLUDED.publication_id;

-- name: InsertPartnerSignup :exec
INSERT INTO donor_partner_signups(id,organization,name,email,website,message,request_hash,consent_version)
VALUES($1,$2,$3,$4,$5,$6,$7,'r1-partner-contact-v1') ON CONFLICT(id) DO NOTHING;

-- name: PartnerSignupByID :one
SELECT * FROM donor_partner_signups WHERE id=$1;

-- name: PartnerSignupsPage :many
SELECT id,organization,name,email,website,message,created_at FROM donor_partner_signups
ORDER BY created_at DESC,id LIMIT 51 OFFSET sqlc.arg(page_offset)::bigint;
