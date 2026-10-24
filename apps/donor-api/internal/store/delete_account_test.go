package store

import (
	"context"
	"strings"
	"testing"

	"dad/donor-api/internal/store/queries"
)

func TestDeleteAccountErasesPersonalDataAndKeepsLedger(t *testing.T) {
	s, token, id := donorServiceFixture(t)
	pool := database(t)
	ctx := context.Background()

	// Give the donor identity fields, an OIDC identity, and a retained gift.
	if _, err := pool.Exec(ctx,
		"UPDATE donors SET public_name='Dana', zip='94103', city='SF', timezone='America/Los_Angeles', phone='+14155550123', referral_code='dana', company_code='ACME' WHERE id=$1", id,
	); err != nil {
		t.Fatal(err)
	}
	oidcID, _ := NewID()
	if _, err := pool.Exec(ctx,
		"INSERT INTO donor_oidc_identities (id, donor_id, provider, subject) VALUES ($1,$2,'GOOGLE','sub-del')", oidcID, id,
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM donor_oidc_identities WHERE donor_id=$1", id) })
	// A gift is the retained legal record.
	checkoutID, _ := NewID()
	giftID, _ := NewID()
	if _, err := pool.Exec(ctx,
		"INSERT INTO donor_checkouts(id,donor_id,amount_minor,frequency) VALUES($1,$2,3000,'MONTHLY')", checkoutID, id,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO donor_gifts(id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at) VALUES($1,$2,$3,$4,3000,'USD','MONTHLY',now())",
		giftID, id, checkoutID, "pi_"+giftID.String(),
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM donor_gifts WHERE donor_id=$1", id)
		pool.Exec(ctx, "DELETE FROM donor_checkouts WHERE donor_id=$1", id)
		pool.Exec(ctx, "DELETE FROM donor_deletions WHERE donor_id=$1", id)
	})

	if err := s.DeleteAccount(ctx, token); err != nil {
		t.Fatal(err)
	}

	// Personal data scrubbed; email is a non-routable placeholder; disabled.
	var email string
	var publicName, phone, zip, city, tz, refCode, companyCode *string
	var disabled bool
	if err := pool.QueryRow(ctx,
		"SELECT email, public_name, phone, zip, city, timezone, referral_code, company_code, disabled FROM donors WHERE id=$1", id,
	).Scan(&email, &publicName, &phone, &zip, &city, &tz, &refCode, &companyCode, &disabled); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(email, "@deleted.invalid") {
		t.Errorf("email = %q; want a @deleted.invalid placeholder", email)
	}
	for name, v := range map[string]*string{
		"public_name": publicName, "phone": phone, "zip": zip, "city": city,
		"timezone": tz, "referral_code": refCode, "company_code": companyCode,
	} {
		if v != nil {
			t.Errorf("%s = %q; want cleared", name, *v)
		}
	}
	if !disabled {
		t.Error("account not disabled after deletion")
	}

	// OIDC identity removed.
	var oidcCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_oidc_identities WHERE donor_id=$1", id).Scan(&oidcCount); err != nil ||
		oidcCount != 0 {
		t.Errorf("oidc identities = %d, err %v; want 0", oidcCount, err)
	}

	// The gift ledger is retained.
	var giftCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_gifts WHERE donor_id=$1", id).Scan(&giftCount); err != nil || giftCount != 1 {
		t.Errorf("gifts = %d, err %v; want 1 (ledger retained)", giftCount, err)
	}

	// The deletion is audited.
	var auditCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_deletions WHERE donor_id=$1", id).Scan(&auditCount); err != nil ||
		auditCount != 1 {
		t.Errorf("deletion audit rows = %d, err %v; want 1", auditCount, err)
	}

	// The session no longer authenticates.
	if _, err := s.Profile(ctx, token); err == nil {
		t.Error("session still valid after deletion; want denied")
	}

	// A disabled anonymised donor still reads back by id for the ledger's FK.
	if _, err := queries.New(pool).LockDonor(ctx, id); err != nil {
		t.Errorf("anonymised donor row unreadable: %v", err)
	}
}
