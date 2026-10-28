ALTER TABLE donors ADD COLUMN full_name VARCHAR(150);
CREATE TABLE donor_passwords (
 donor_id UUID PRIMARY KEY REFERENCES donors(id) ON DELETE CASCADE,
 password_hash TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL
);
