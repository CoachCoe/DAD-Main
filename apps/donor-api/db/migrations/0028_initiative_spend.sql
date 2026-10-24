-- Initiative spend by category (PRD §8, A-3, P-12; §5.2 "no line items"). Phase 2.2.
--
-- Spend is reported as a TOTAL per category per initiative — never individual
-- transactions (PRD §5.2 is explicit that the app shows category totals, not a
-- line-item ledger). One row is the running total committed/spent in one
-- category of one initiative; the donor-facing and overall aggregations sum
-- these rows. amount_minor is integer minor units (cents), matching every other
-- money column.
--
-- A category is unique within an initiative (UNIQUE (initiative_id, category)):
-- staff set the total for a category, they do not append transactions. Spend
-- rows have no separate publish state — they are shown whenever their
-- initiative is published, so there is nothing to verify independently; the
-- initiative's own draft->publish gate controls visibility.
CREATE TABLE donor_initiative_spend (
	id UUID NOT NULL,
	initiative_id UUID NOT NULL,
	category VARCHAR(100) NOT NULL,
	amount_minor BIGINT NOT NULL DEFAULT 0,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (initiative_id, category),
	CONSTRAINT ck_donor_initiative_spend_amount CHECK (amount_minor >= 0),
	FOREIGN KEY (initiative_id) REFERENCES donor_initiatives (id) ON DELETE CASCADE
);

CREATE INDEX ix_donor_initiative_spend_initiative ON donor_initiative_spend (initiative_id, category);
