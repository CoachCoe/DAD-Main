-- Events model (PRD §6.10 E-2, E-3). Phase 4.2.
--
-- An event is a volunteer, community, or invite-only gathering a donor can see
-- in the Events tab, reserve a spot at (4.3), and check in to at the venue
-- (4.5). It follows the same draft->publish discipline as donor_initiatives:
-- a row is DRAFT until staff publish it, and only PUBLISHED events are ever
-- visible to donors or returned by the near-city list.
--
-- Location is modelled the same coarse way as the rest of the system (PRD §10
-- geo philosophy: a good default over a multi-megabyte table). An event carries
-- a human city label and a ZIP; "in and near the donor's city" (E-2) is the
-- donor's city string OR the same three-digit ZIP region (SCF) as the donor --
-- no lat/long or distance table, consistent with internal/geo. venue/address
-- are free text shown on the detail page.
--
-- type is the event kind (E-3). INVITE_ONLY events are gated in 4.4 by a
-- qualifying badge or an admin guest list; the column exists now so the model
-- is complete, and the gating query lands with 4.4.
--
-- capacity is the number of spots; 0 means uncapped. duration_minutes is how
-- long the event runs and is what a check-in multiplies into volunteer hours
-- (4.5 / EM-8), so it is required and positive. starts_at/ends_at bound the
-- check-in window (E-8): check-in is accepted only between them.
CREATE TABLE donor_events (
	id UUID NOT NULL,
	slug VARCHAR(100) NOT NULL,
	title VARCHAR(150) NOT NULL,
	type VARCHAR(16) NOT NULL,
	description VARCHAR(4000) NOT NULL DEFAULT '',
	venue VARCHAR(200) NOT NULL DEFAULT '',
	address VARCHAR(300) NOT NULL DEFAULT '',
	city VARCHAR(80) NOT NULL DEFAULT '',
	zip VARCHAR(10) NOT NULL DEFAULT '',
	starts_at TIMESTAMP WITH TIME ZONE NOT NULL,
	ends_at TIMESTAMP WITH TIME ZONE NOT NULL,
	capacity INTEGER NOT NULL DEFAULT 0,
	publish_state VARCHAR(16) NOT NULL DEFAULT 'DRAFT',
	published_at TIMESTAMP WITH TIME ZONE,
	created_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	updated_at TIMESTAMP WITH TIME ZONE DEFAULT now() NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (slug),
	CONSTRAINT ck_donor_events_type CHECK (type IN ('VOLUNTEER', 'COMMUNITY', 'INVITE_ONLY')),
	CONSTRAINT ck_donor_events_publish_state CHECK (publish_state IN ('DRAFT', 'PUBLISHED')),
	CONSTRAINT ck_donor_events_capacity CHECK (capacity >= 0),
	-- ends_at must be after starts_at so the check-in window and duration are
	-- well-formed.
	CONSTRAINT ck_donor_events_window CHECK (ends_at > starts_at),
	-- A published event must carry its publish time; a draft must not.
	CONSTRAINT ck_donor_events_published_at CHECK (
		(publish_state = 'PUBLISHED') = (published_at IS NOT NULL)
	)
);

-- The donor-facing list asks for "published, upcoming events, soonest first".
-- A partial index on starts_at over published rows serves that selection as a
-- cheap ordered scan without touching drafts (PRD §13 risk #6: this stays a
-- lookup at scale). The near-city filter (city / ZIP-3) is applied on top; the
-- upcoming population at any moment is small, so a dedicated geo index is not
-- warranted.
CREATE INDEX ix_donor_events_upcoming ON donor_events (starts_at, id)
	WHERE publish_state = 'PUBLISHED';
