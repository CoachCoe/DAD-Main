package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"dad/donor-api/internal/dbroles"
)

func TestPartnerInquiryIsIdempotentAndShownToStaff(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	id := testID(t)
	_, admin := staffFixture(t, pool, "ADMIN")
	t.Cleanup(func() { mustExec(t, pool, "DELETE FROM donor_partner_signups WHERE id=$1", id) })
	inquiry := PartnerRequest{Organization: "Org", Name: "Name", Email: "partner-" + id.String() + "@example.org", Website: "https://partner.example.org", Message: "Hello", Consent: true}
	for range 2 {
		if err := (PartnerService{DB: api}).Signup(ctx, id, inquiry); err != nil {
			t.Fatalf("signup or its retry: %v", err)
		}
	}
	inquiry.Message = "Changed"
	if err := (PartnerService{DB: api}).Signup(ctx, id, inquiry); !errors.Is(err, ErrStaffConflict) {
		t.Fatalf("changed retry: got %v, want ErrStaffConflict", err)
	}
	page, err := (ContentService{Staff: &StaffService{DB: api}}).Partners(ctx, admin, 0)
	if err != nil || !slices.ContainsFunc(page.Signups, func(s PartnerSignup) bool { return s.ID == id && s.Message == "Hello" }) {
		t.Fatalf("staff list: %v", err)
	}
}
