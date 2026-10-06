-- A sign-in link may return the donor to one of a fixed set of account pages.
ALTER TABLE donor_login_emails ADD COLUMN return_path varchar(64)
CHECK (return_path IN ('/account/payment-method', '/account/dashboard'));
