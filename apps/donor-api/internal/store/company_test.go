package store

import (
	"context"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
)

func TestCompanyCodeCaptureAndBind(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	s := DonorService{DB: api, Origin: "https://donor.example.org"}
	now := time.Now().UTC()

	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM donor_company_touches") })

	// A malformed code with no prior touch captures nothing.
	if cap, err := s.CaptureCompanyCode(ctx, "!!bad!!", "", now); err != nil || cap.Token != "" {
		t.Fatalf("malformed capture = %+v, %v; want empty", cap, err)
	}

	// A valid code mints a visitor token and is normalised to upper case.
	first, err := s.CaptureCompanyCode(ctx, "acme-corp", "", now)
	if err != nil || first.Token == "" {
		t.Fatalf("first capture = %+v, %v", first, err)
	}
	var stored string
	if err := pool.QueryRow(ctx, "SELECT company_code FROM donor_company_touches WHERE token_hash=$1", HashToken(first.Token)).Scan(&stored); err != nil ||
		stored != "ACME-CORP" {
		t.Fatalf("stored code = %q, err %v; want ACME-CORP", stored, err)
	}

	// Last touch wins: a new code on the same token overwrites it, same token.
	second, err := s.CaptureCompanyCode(ctx, "GLOBEX", first.Token, now.Add(time.Minute))
	if err != nil || second.Token != first.Token {
		t.Fatalf("second capture = %+v, %v; want same token %q", second, err, first.Token)
	}
	if err := pool.QueryRow(ctx, "SELECT company_code FROM donor_company_touches WHERE token_hash=$1", HashToken(first.Token)).Scan(&stored); err != nil ||
		stored != "GLOBEX" {
		t.Fatalf("after last-touch = %q; want GLOBEX", stored)
	}

	// Bind at first gift records the captured code on the donor, once.
	id, _ := donor(t, pool)
	t.Cleanup(func() { pool.Exec(ctx, "UPDATE donors SET company_code=NULL WHERE id=$1", id) })
	bound, err := s.BindCapturedCompanyCode(ctx, id, first.Token, now.Add(2*time.Minute))
	if err != nil || bound != "GLOBEX" {
		t.Fatalf("bind = %q, %v; want GLOBEX", bound, err)
	}
	var onDonor *string
	if err := pool.QueryRow(ctx, "SELECT company_code FROM donors WHERE id=$1", id).Scan(&onDonor); err != nil || onDonor == nil ||
		*onDonor != "GLOBEX" {
		t.Fatalf("donor company_code = %v; want GLOBEX", onDonor)
	}

	// Binding is first-write-wins: a second bind with a different code is a no-op.
	other, err := s.CaptureCompanyCode(ctx, "INITECH", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.BindCapturedCompanyCode(ctx, id, other.Token, now.Add(3*time.Minute)); err != nil || again != "INITECH" {
		// BindCapturedCompanyCode reports the touch's code, but the UPDATE is a
		// no-op because company_code is already set; verify the donor is unchanged.
		t.Fatalf("second bind returned = %q, %v", again, err)
	}
	if err := pool.QueryRow(ctx, "SELECT company_code FROM donors WHERE id=$1", id).Scan(&onDonor); err != nil || onDonor == nil ||
		*onDonor != "GLOBEX" {
		t.Fatalf("donor code changed on second bind: %v; want still GLOBEX", onDonor)
	}

	// An expired touch does not bind.
	expired, err := s.CaptureCompanyCode(ctx, "OSCORP", "", now.Add(-2*referralWindow))
	if err != nil {
		t.Fatal(err)
	}
	id2, _ := donor(t, pool)
	t.Cleanup(func() { pool.Exec(ctx, "UPDATE donors SET company_code=NULL WHERE id=$1", id2) })
	if bound, err := s.BindCapturedCompanyCode(ctx, id2, expired.Token, now); err != nil || bound != "" {
		t.Fatalf("expired bind = %q, %v; want empty", bound, err)
	}
}
