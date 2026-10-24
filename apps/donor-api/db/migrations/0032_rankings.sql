-- Rankings rollup + opt-out (PRD §6.4 K-1..K-3, S-4). Phase 3.3.
--
-- National percentile and city rank by current streak length. At 1M donors this
-- CANNOT be computed per request (PRD §13 risk #6), so a periodic job computes
-- the whole population's ranks in one set-based pass and writes one row per
-- ranked donor here; the donor read is then a single-row lookup. Ties break on
-- the earlier streak start (S-4 / the ranking rule): a longer-standing identical
-- streak ranks higher.
--
-- Only a donor's OWN row is ever returned to them (never a names list, K-3), and
-- a donor who opted out of rankings is excluded from the computed population
-- entirely, so opting out removes them from everyone's denominator too.
--
-- national_percentile is the top-percentile the donor falls in (1 = top 1%),
-- so the "Top 2%" badge is national_percentile <= 2. city_rank is 1-based
-- within donor.city; it is NULL when the donor has no city.
CREATE TABLE donor_rankings (
	donor_id UUID NOT NULL,
	national_percentile INTEGER NOT NULL,
	city VARCHAR(80),
	city_rank INTEGER,
	computed_at TIMESTAMP WITH TIME ZONE NOT NULL,
	PRIMARY KEY (donor_id),
	CONSTRAINT ck_donor_rankings_percentile CHECK (national_percentile >= 1 AND national_percentile <= 100),
	CONSTRAINT ck_donor_rankings_city_rank CHECK (city_rank IS NULL OR city_rank >= 1),
	FOREIGN KEY (donor_id) REFERENCES donors (id) ON DELETE CASCADE
);

CREATE INDEX ix_donor_rankings_city ON donor_rankings (city, city_rank);

-- Rankings are on by default (PRD: default-on opt-out). A donor who opts out is
-- excluded from the ranking population at the next refresh.
ALTER TABLE donor_preferences ADD COLUMN rankings_opted_out BOOLEAN NOT NULL DEFAULT false;
