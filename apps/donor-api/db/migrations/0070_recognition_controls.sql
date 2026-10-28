CREATE TABLE donor_streak_resets (
 id UUID PRIMARY KEY, donor_id UUID NOT NULL REFERENCES donors(id), reset_at TIMESTAMPTZ NOT NULL,
 actor_id UUID NOT NULL REFERENCES donor_staff(id), reason TEXT NOT NULL CHECK(length(reason) BETWEEN 10 AND 1000)
);
CREATE INDEX ix_streak_resets_donor ON donor_streak_resets(donor_id,reset_at DESC);
CREATE TABLE donor_badge_rules (
 code VARCHAR(16) PRIMARY KEY, signal VARCHAR(40) NOT NULL, threshold BIGINT NOT NULL CHECK(threshold BETWEEN 1 AND 1000000),
 artwork TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL, actor_id UUID NOT NULL REFERENCES donor_staff(id)
);
CREATE TABLE donor_milestone_rules (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton), days BIGINT[] NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL, actor_id UUID NOT NULL REFERENCES donor_staff(id)
);
