ALTER TABLE analytics_events ADD COLUMN journey_id UUID;
ALTER TABLE analytics_events ADD COLUMN dedup_key TEXT UNIQUE;
ALTER TABLE donor_checkouts ADD COLUMN journey_id UUID;
CREATE INDEX ix_analytics_journey ON analytics_events(journey_id,name,occurred_at) WHERE journey_id IS NOT NULL;
ALTER TABLE analytics_events DROP CONSTRAINT ck_analytics_source;
ALTER TABLE analytics_events ADD CONSTRAINT ck_analytics_source CHECK(source IN ('WEB','IOS','ANDROID','SERVER'));
ALTER TABLE analytics_events DROP CONSTRAINT analytics_events_donor_id_fkey;
ALTER TABLE analytics_events ADD FOREIGN KEY(donor_id) REFERENCES donors(id) ON DELETE SET NULL;
