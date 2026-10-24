-- Monthly giving is capped at $900/mo ($30/day), PRD §14.3. Tighten the donor
-- giving constraints to the ceiling. This bounds donor gifts only; staff manual
-- financial adjustments keep their own (higher) application-level limit.
ALTER TABLE donor_checkouts ADD CONSTRAINT ck_donor_checkout_maximum CHECK (amount_minor <= 90000);
ALTER TABLE donor_amount_changes DROP CONSTRAINT ck_donor_amount_change_minor;
ALTER TABLE donor_amount_changes ADD CONSTRAINT ck_donor_amount_change_minor CHECK (amount_minor >= 100 AND amount_minor <= 90000 AND previous_minor >= 100);
