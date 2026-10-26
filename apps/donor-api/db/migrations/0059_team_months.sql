-- Winning Team (Appendix A): each partner's team standings are recorded once
-- per calendar month (America/New_York) when the month closes, and the members
-- of the team at #1 earn the badge. Teams rank by active giving per member, so
-- a small team can win against a large one; teams below the minimum group size
-- are not ranked.
CREATE TABLE donor_team_months (
	partner_id UUID NOT NULL REFERENCES donor_partner_signups (id),
	month DATE NOT NULL CHECK (extract(day FROM month) = 1),
	winner_team_id UUID REFERENCES donor_partner_teams (id),
	closed_at TIMESTAMP WITH TIME ZONE NOT NULL,
	PRIMARY KEY (partner_id, month)
);

CREATE TABLE donor_team_month_standings (
	partner_id UUID NOT NULL,
	month DATE NOT NULL,
	team_id UUID NOT NULL REFERENCES donor_partner_teams (id),
	rank INTEGER NOT NULL CHECK (rank > 0),
	members INTEGER NOT NULL CHECK (members > 0),
	annualized_minor BIGINT NOT NULL CHECK (annualized_minor >= 0),
	per_member_minor BIGINT NOT NULL CHECK (per_member_minor >= 0),
	PRIMARY KEY (partner_id, month, team_id),
	FOREIGN KEY (partner_id, month) REFERENCES donor_team_months (partner_id, month)
);
