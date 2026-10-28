-- Contact inquiries are not co-funders. Legacy imports may still reference one.
CREATE OR REPLACE FUNCTION donor_sync_primary_partner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.partner_signup_id IS NOT NULL AND EXISTS(SELECT 1 FROM donor_partner_signups WHERE id=NEW.partner_signup_id AND approved_at IS NOT NULL) THEN
  INSERT INTO donor_initiative_partners(initiative_id,partner_id,committed_minor,updated_at)
  VALUES(NEW.id,NEW.partner_signup_id,NEW.committed_minor,NEW.updated_at)
  ON CONFLICT(initiative_id,partner_id) DO NOTHING;
 END IF;
 RETURN NEW;
END $$;
DELETE FROM donor_initiative_partners ip USING donor_partner_signups p WHERE p.id=ip.partner_id AND p.approved_at IS NULL;
