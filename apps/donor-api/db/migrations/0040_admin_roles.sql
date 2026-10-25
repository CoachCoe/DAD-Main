-- Admin role remodel (PRD §8). Phase 5.4.
--
-- §8 replaces the additive SUPPORT ⊂ FINANCE ⊂ ADMIN ladder with:
--   SUPER_ADMIN   — everything, incl. refunds, manual badge grants, and admin
--                   management (donors & money and badge grants are super-admin
--                   only).
--   OP_PARTNERSHIPS — partner approvals, links, matching, partner users.
--   OP_GROWTH       — events and check-ins.
--   OP_IMPACT       — initiative metrics and spend (draft → publish).
-- Operators can do everything in their area EXCEPT refunds, badge grants and
-- admin management, which stay with a super admin.
--
-- Migration of existing rows preserves access rather than silently dropping
-- powers: ADMIN and FINANCE both become SUPER_ADMIN (FINANCE held refunds,
-- which are now super-admin only, so they must become super admin to keep
-- them), and SUPPORT becomes OP_PARTNERSHIPS (the read-mostly operator area —
-- it carries donor.read, the SUPPORT capability). Operators are then
-- re-assigned to their real areas by hand from the admin console.
--
-- Done as widen-CHECK → migrate rows → narrow-CHECK so no row ever violates the
-- constraint mid-migration.

ALTER TABLE donor_staff DROP CONSTRAINT ck_donor_staff_role;
ALTER TABLE donor_staff ADD CONSTRAINT ck_donor_staff_role CHECK (
	role IN ('SUPPORT', 'FINANCE', 'ADMIN', 'SUPER_ADMIN', 'OP_PARTNERSHIPS', 'OP_GROWTH', 'OP_IMPACT')
);

UPDATE donor_staff SET role = 'SUPER_ADMIN' WHERE role IN ('ADMIN', 'FINANCE');
UPDATE donor_staff SET role = 'OP_PARTNERSHIPS' WHERE role = 'SUPPORT';

ALTER TABLE donor_staff DROP CONSTRAINT ck_donor_staff_role;
ALTER TABLE donor_staff ADD CONSTRAINT ck_donor_staff_role CHECK (
	role IN ('SUPER_ADMIN', 'OP_PARTNERSHIPS', 'OP_GROWTH', 'OP_IMPACT')
);
