ALTER TABLE donor_partner_applications
    ADD COLUMN receipt_hash varchar(64) UNIQUE,
    ADD COLUMN receipt_expires_at timestamptz,
    ADD CHECK ((receipt_hash IS NULL)=(receipt_expires_at IS NULL));
