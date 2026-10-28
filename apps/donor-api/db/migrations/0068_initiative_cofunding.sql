-- Each initiative can have several co-funders, each with its own commitment.
CREATE TABLE donor_initiative_partners (
 initiative_id UUID NOT NULL REFERENCES donor_initiatives(id) ON DELETE CASCADE,
 partner_id UUID NOT NULL REFERENCES donor_partner_signups(id),
 committed_minor BIGINT NOT NULL CHECK (committed_minor >= 0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (initiative_id, partner_id)
);
CREATE INDEX ix_initiative_partners_partner ON donor_initiative_partners(partner_id, initiative_id);
INSERT INTO donor_initiative_partners(initiative_id,partner_id,committed_minor)
SELECT id,partner_signup_id,committed_minor FROM donor_initiatives WHERE partner_signup_id IS NOT NULL;
-- Preserve the older optional primary-partner input for installed clients. Adding a
-- primary partner never removes other co-funders or changes their commitments.
CREATE FUNCTION donor_sync_primary_partner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.partner_signup_id IS NOT NULL THEN
  INSERT INTO donor_initiative_partners(initiative_id,partner_id,committed_minor,updated_at)
  VALUES(NEW.id,NEW.partner_signup_id,NEW.committed_minor,NEW.updated_at)
  ON CONFLICT(initiative_id,partner_id) DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER donor_primary_partner AFTER INSERT OR UPDATE OF partner_signup_id ON donor_initiatives
FOR EACH ROW EXECUTE FUNCTION donor_sync_primary_partner();
