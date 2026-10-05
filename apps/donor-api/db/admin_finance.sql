-- name: StaffGift :one
SELECT * FROM donor_gifts WHERE donor_id=$1 AND id=$2 FOR UPDATE;

-- name: StaffGiftSnapshot :one
SELECT * FROM donor_gifts WHERE donor_id=$1 AND id=$2;

-- name: GiftRefundObservations :one
SELECT count(*)::bigint FROM donor_refund_observations o JOIN donor_refunds r ON r.id=o.refund_id WHERE r.gift_id=$1;

-- name: StaffActionByID :one
SELECT * FROM donor_staff_actions WHERE id=$1;

-- name: PendingStaffRefund :one
SELECT EXISTS(SELECT 1 FROM donor_staff_actions a LEFT JOIN donor_refunds r ON r.stripe_refund_id=a.stripe_refund_id
WHERE a.target_id=$1 AND a.kind='REFUND' AND a.status<>'ABANDONED'
AND (r.id IS NULL OR r.status IN ('PENDING','REQUIRES_ACTION')));

-- name: InsertStaffAction :exec
INSERT INTO donor_staff_actions(id,actor_id,donor_id,kind,target_id,reason,amount_minor,period_end,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: SaveStaffAction :exec
UPDATE donor_staff_actions SET stripe_refund_id=$2,status=$3,completed_at=$4 WHERE id=$1;
