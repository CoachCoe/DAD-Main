-- PRD §8 Content: staff can exclude a published, active initiative from the
-- daily "Today your $1 funded" rotation without unpublishing it.
ALTER TABLE donor_initiatives ADD COLUMN in_rotation BOOLEAN NOT NULL DEFAULT true;

DROP INDEX ix_donor_initiatives_rotatable;
CREATE INDEX ix_donor_initiatives_rotatable ON donor_initiatives (started_on, id)
	WHERE publish_state = 'PUBLISHED' AND status = 'ACTIVE' AND in_rotation;
