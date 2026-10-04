-- name: AuthenticatedStaff :one
SELECT s.* FROM donor_staff s JOIN donor_staff_sessions x ON x.staff_id=s.id
WHERE x.token_hash=$1 AND x.revoked_at IS NULL AND x.expires_at>$2 AND NOT s.disabled;

-- name: StaffByEmail :one
SELECT * FROM donor_staff WHERE email=$1 FOR UPDATE;

-- name: StaffByID :one
SELECT * FROM donor_staff WHERE id=$1 FOR UPDATE;

-- name: LockStaffAccounts :many
SELECT * FROM donor_staff ORDER BY id FOR UPDATE;

-- name: InsertStaff :one
INSERT INTO donor_staff(id,email,name,role) VALUES($1,$2,$3,$4) RETURNING *;

-- name: SaveStaffCredentials :exec
UPDATE donor_staff SET password_hash=$2,totp_secret=$3,totp_counter=$4,failures=$5,locked_until=$6 WHERE id=$1;

-- name: InsertStaffAudit :exec
INSERT INTO donor_staff_audit(id,actor_id,action,target,details,created_at) VALUES($1,$2,$3,$4,$5,$6);

-- name: InsertStaffSession :exec
INSERT INTO donor_staff_sessions(id,staff_id,token_hash,expires_at) VALUES($1,$2,$3,$4);

-- name: RevokeStaffSession :exec
UPDATE donor_staff_sessions SET revoked_at=$2 WHERE token_hash=$1 AND revoked_at IS NULL;

-- name: RevokeStaffSessions :exec
UPDATE donor_staff_sessions SET revoked_at=$2 WHERE staff_id=$1 AND revoked_at IS NULL;

-- name: InsertStaffInvite :exec
INSERT INTO donor_staff_invites(id,staff_id,token_hash,expires_at) VALUES($1,$2,$3,$4);

-- name: StaffInviteByToken :one
SELECT * FROM donor_staff_invites WHERE token_hash=$1 FOR UPDATE;

-- name: StaffInviteOwner :one
SELECT staff_id FROM donor_staff_invites WHERE token_hash=$1;

-- name: SaveStaffInvite :exec
UPDATE donor_staff_invites SET attempts=$2,pending_secret=$3,consumed_at=$4 WHERE id=$1;

-- name: InvalidateStaffInvites :exec
UPDATE donor_staff_invites SET consumed_at=$2,pending_secret=NULL WHERE staff_id=$1 AND consumed_at IS NULL;

-- name: SetStaffAccess :exec
UPDATE donor_staff SET disabled=$2,role=$3 WHERE id=$1;
