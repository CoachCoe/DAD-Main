-- name: DonorPasskeys :many
SELECT * FROM donor_passkeys WHERE donor_id=$1 ORDER BY id;

-- name: DonorPasskeyOwner :one
SELECT donor_id FROM donor_passkeys WHERE credential_id=$1;

-- name: InsertDonorPasskey :exec
INSERT INTO donor_passkeys(id,donor_id,credential_id,public_key,sign_count,transports,
    aaguid,attestation_format,user_verified,backup_eligible,backup_state,name)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12);

-- name: RecordDonorPasskeyUse :exec
UPDATE donor_passkeys
SET sign_count=$2,user_verified=$3,backup_state=$4,last_used_at=$5
WHERE credential_id=$1;

-- name: DeleteDonorPasskey :execrows
DELETE FROM donor_passkeys WHERE id=$1 AND donor_id=$2;

-- name: DeleteAllDonorPasskeys :execrows
DELETE FROM donor_passkeys WHERE donor_id=$1;

-- name: InsertDonorCeremony :exec
INSERT INTO donor_ceremonies(token_hash,kind,donor_id,session,expires_at)
VALUES($1,$2,$3,$4,$5);

-- name: TakeDonorCeremony :one
DELETE FROM donor_ceremonies
WHERE token_hash=$1 AND kind=$2
RETURNING donor_id,session,expires_at;

-- name: DeleteExpiredDonorCeremonies :exec
DELETE FROM donor_ceremonies WHERE expires_at<=$1;
