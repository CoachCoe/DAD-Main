-- Waitlist claims (D-6) are now keyed: email_hash is an HMAC-SHA256 under
-- DONOR_FOUNDING_CLAIM_KEY, which only the API holds, so hashing guessed
-- addresses cannot reverse them. A claim that matches a donor becomes a grant,
-- recorded when the claims are imported, when a donor first gives, or when a
-- donor moves to the claimed address; the projection reads the grant. Claims
-- imported unkeyed are matched to current donors once here, then deleted:
-- re-import the file to honour donors who have not yet given.
CREATE TABLE donor_founding_grants (
	donor_id UUID NOT NULL PRIMARY KEY REFERENCES donors (id) ON DELETE CASCADE,
	granted_at TIMESTAMP WITH TIME ZONE NOT NULL
);

INSERT INTO donor_founding_grants (donor_id, granted_at)
SELECT d.id, now()
FROM donors d
JOIN donor_founding_claims c ON c.email_hash = encode(sha256(convert_to(lower(d.email), 'UTF8')), 'hex')
WHERE c.expires_at > now()
ON CONFLICT (donor_id) DO NOTHING;

DELETE FROM donor_founding_claims;
