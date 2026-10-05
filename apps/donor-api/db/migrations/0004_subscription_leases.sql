-- A recurring command holds its subscription's lease while its processor
-- request is outstanding, instead of holding the donor lock across the call.
CREATE TABLE donor_subscription_leases (
	attempt_id UUID NOT NULL,
	donor_id UUID NOT NULL,
	command_id UUID NOT NULL,
	expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
	PRIMARY KEY (attempt_id),
	FOREIGN KEY(attempt_id) REFERENCES donor_stripe_checkouts (id),
	FOREIGN KEY(donor_id) REFERENCES donors (id)
);

CREATE INDEX ix_donor_subscription_leases_donor_id ON donor_subscription_leases (donor_id);
