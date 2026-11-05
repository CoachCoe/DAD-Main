package store

import (
	"context"
	"errors"
	"testing"

	"dad/donor-api/internal/dbroles"
)

// A device may hold a donor session and a partner session at once. They live
// in separate tables, so neither token authenticates the other surface, and
// ending one leaves the other signed in.
func TestPartnerAndDonorSessionsCoexistOnOneDevice(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	api := as(t, dbroles.API)
	partner := approvedPartner(t, pool, "Coexist partner", "COEXISTPARTNER", 0, 0)
	_, partnerToken := partnerPushSession(t, pool, partner, true)
	_, donorToken := signedInDonor(t, pool)
	portal := PartnerPortalService{DB: api}
	donors := DonorService{DB: api}

	var app bool
	if err := pool.QueryRow(ctx, "SELECT app FROM donor_partner_sessions WHERE token_hash=$1", HashToken(partnerToken)).
		Scan(&app); err != nil || !app {
		t.Fatalf("native partner session not recorded as an app session: %v %v", app, err)
	}
	if _, err := portal.Users(ctx, partnerToken); err != nil {
		t.Fatalf("partner bearer: %v", err)
	}
	if _, err := portal.Users(ctx, donorToken); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("donor token opened the partner portal: %v", err)
	}
	if _, err := donors.Profile(ctx, partnerToken); !errors.Is(err, ErrDenied) {
		t.Fatalf("partner token opened the donor account: %v", err)
	}

	if err := portal.SignOut(ctx, partnerToken); err != nil {
		t.Fatal(err)
	}
	if _, err := portal.Users(ctx, partnerToken); !errors.Is(err, ErrPartnerDenied) {
		t.Fatalf("partner session survived sign-out: %v", err)
	}
	if _, err := donors.Profile(ctx, donorToken); err != nil {
		t.Fatalf("partner sign-out ended the donor session: %v", err)
	}
}
