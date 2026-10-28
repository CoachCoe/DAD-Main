-- Analytics event taxonomy (PRD §9, §4 "from day one"). Phase 1.5.
--
-- One shared, append-only event stream that every platform (web now, mobile
-- later) writes to, so the §4 success metrics are computed from one source of
-- truth. This is a hot write path at scale, so the table is INSERT-only (no
-- updates, no per-row reads on the write path), its id is client-independent,
-- and the single index serves the reporting reads (name + time) without slowing
-- inserts with wide indexing.
--
-- occurred_at is the client's event time; received_at is when the server
-- accepted it (clock-skew and offline-replay are visible as the gap). donor_id
-- is nullable: an event before sign-in (app_opened, signup_started) is
-- anonymous, and the FK keeps a named donor's events joinable to their account.
-- properties holds the event's typed payload (e.g. {"source":"company"} for
-- attribution_captured); the application validates names and property shapes
-- before insert, so the column is a checked payload, not free-form.
--
-- Partition-ready: at 1M users this table is the first candidate for monthly
-- RANGE partitioning on occurred_at. It is left unpartitioned now (premature at
-- current volume) but carries no design that blocks it later — the primary key
-- is a standalone id and every query is time-bounded.
CREATE TABLE analytics_events (
	id UUID NOT NULL,
	name VARCHAR(40) NOT NULL,
	donor_id UUID,
	source VARCHAR(16) NOT NULL,
	properties JSONB NOT NULL DEFAULT '{}'::jsonb,
	occurred_at TIMESTAMP WITH TIME ZONE NOT NULL,
	received_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	CONSTRAINT ck_analytics_source CHECK (source IN ('WEB', 'IOS', 'ANDROID')),
	FOREIGN KEY (donor_id) REFERENCES donors (id)
);

-- Reporting reads are "events of name N over a time range"; this index serves
-- them. Inserts touch only this one index.
CREATE INDEX ix_analytics_events_name_occurred ON analytics_events (name, occurred_at);
