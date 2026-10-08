-- Donor views read the subscription as last synced rather than asking Stripe,
-- so the sync keeps what they need: the billing anchor that dates pause
-- renewals, and whether Stripe holds a schedule or pending update DAD did not
-- make, during which no change is offered. Rows synced before this migration
-- have no anchor until their next sync.
ALTER TABLE donor_subscriptions ADD COLUMN billing_anchor TIMESTAMP WITH TIME ZONE;
ALTER TABLE donor_subscriptions ADD COLUMN external_change BOOLEAN NOT NULL DEFAULT false;
