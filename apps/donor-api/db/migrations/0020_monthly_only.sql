-- One-time gifts are out of scope (PRD §5.1): giving is monthly recurring only.
-- Tighten the checkout frequency constraint so no new ONE_TIME checkout can be
-- recorded. Historical donor_gifts rows keep their recorded frequency (that
-- table carries no frequency CHECK), so past one-time gifts remain valid facts.
ALTER TABLE donor_checkouts DROP CONSTRAINT ck_donor_frequency;
ALTER TABLE donor_checkouts ADD CONSTRAINT ck_donor_frequency CHECK (frequency = 'MONTHLY');
