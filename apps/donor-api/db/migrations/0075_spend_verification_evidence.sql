-- Keep verification evidence with each newly published total.
ALTER TABLE donor_initiative_spend ADD COLUMN verification_note TEXT NOT NULL DEFAULT '';
ALTER TABLE donor_initiative_spend ADD COLUMN verified_on DATE;
