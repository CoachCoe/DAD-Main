package store

import (
	"context"
	"errors"
	"testing"

	"dad/donor-api/internal/dbroles"
)

func TestApplicationReceiptIsPrivateIdempotentAndScoped(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	api := as(t, dbroles.API)
	service := PartnerApplicationService{DB: api, Staff: &StaffService{DB: api}}
	_, staff := staffFixture(t, pool, "SUPER_ADMIN")
	cleanPartners(t, pool)
	id := testID(t)
	in := validApplication("Receipt Org", "receipt@org.test")
	receipt, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := service.ApplyWithReceipt(ctx, id, in, receipt); err != nil {
			t.Fatal(err)
		}
	}
	var hash string
	if err := pool.QueryRow(ctx, "SELECT receipt_hash FROM donor_partner_applications WHERE id=$1", id).Scan(&hash); err != nil ||
		hash != HashToken(receipt) {
		t.Fatalf("receipt was not hashed: %v", err)
	}
	status, err := service.ApplicationStatus(ctx, receipt)
	if err != nil || status.Status != "PENDING" || status.Organization != in.Organization {
		t.Fatalf("pending receipt: %+v %v", status, err)
	}
	other, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplicationStatus(ctx, other); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("foreign receipt accepted: %v", err)
	}
	if err := service.ApplyWithReceipt(ctx, id, in, other); !errors.Is(err, ErrPartnerApplication) {
		t.Fatalf("receipt replaced by a retry: %v", err)
	}
	changed := in
	changed.Organization = "Changed"
	if err := service.ApplyWithReceipt(ctx, id, changed, receipt); !errors.Is(err, ErrPartnerApplication) {
		t.Fatalf("application changed by a retry: %v", err)
	}
	otherID := testID(t)
	if err := service.ApplyWithReceipt(ctx, otherID, validApplication("Other", "other@org.test"), receipt); !errors.Is(
		err,
		ErrPartnerApplication,
	) {
		t.Fatalf("receipt shared by two applications: %v", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM donor_partner_applications WHERE id=$1)", otherID).Scan(&exists); err != nil ||
		exists {
		t.Fatalf("failed receipt left application behind: %v %v", exists, err)
	}
	if _, err := service.Approve(ctx, staff, id); err != nil {
		t.Fatal(err)
	}
	status, err = service.ApplicationStatus(ctx, receipt)
	if err != nil || status.Status != "APPROVED" {
		t.Fatalf("approval not reflected: %+v %v", status, err)
	}
	if _, err := (PartnerPortalService{DB: api}).Users(ctx, receipt); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("receipt granted portal access: %v", err)
	}
	if _, err := (DonorService{DB: api}).Profile(ctx, receipt); !errors.Is(err, ErrDenied) {
		t.Fatalf("receipt granted donor access: %v", err)
	}
	mustExec(t, pool, "UPDATE donor_partner_applications SET receipt_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", id)
	if _, err := service.ApplicationStatus(ctx, receipt); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("expired receipt accepted: %v", err)
	}
}
