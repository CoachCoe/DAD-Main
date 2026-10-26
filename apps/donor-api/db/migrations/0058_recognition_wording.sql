-- Staff-edited wording for badges and streak milestones (PRD §8 Content). The
-- earning rules and thresholds stay in code; a row here only replaces the words
-- a donor sees. A BADGE row is keyed by badge code (title, rule shown on an
-- unearned badge); a MILESTONE row by its day count (push title and body).
CREATE TABLE donor_recognition_wording (
	kind TEXT NOT NULL CHECK (kind IN ('BADGE', 'MILESTONE')),
	key TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	actor_id UUID NOT NULL REFERENCES donor_staff (id),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
	PRIMARY KEY (kind, key)
);
