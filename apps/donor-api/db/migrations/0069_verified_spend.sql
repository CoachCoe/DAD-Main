-- Draft totals never overwrite the published snapshot until staff verify and publish.
CREATE TABLE donor_initiative_spend_drafts (
 initiative_id UUID NOT NULL REFERENCES donor_initiatives(id) ON DELETE CASCADE,
 category VARCHAR(100) NOT NULL,
 amount_minor BIGINT NOT NULL CHECK(amount_minor>=0),
 verification_note TEXT NOT NULL DEFAULT '',
 verified_on DATE,
 updated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(initiative_id,category)
);
