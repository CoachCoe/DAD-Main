package store

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/hotp"
	"impactgraph/donor-api/internal/store/queries"
)

func TestStaffActivationSingleUseOTPAndLockout(t *testing.T) {
	pool := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, _ := staffFixture(t, pool, "SUPPORT")
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM donor_staff_invites WHERE staff_id=$1", id); err != nil {
			t.Error(err)
		}
		if _, err := pool.Exec(context.Background(), "DELETE FROM donor_staff_audit WHERE target=$1", id.String()); err != nil {
			t.Error(err)
		}
	})
	s, err := NewStaffService(pool, base64.URLEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	invite, err := createStaffInvite(ctx, queries.New(pool), id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	provisioned, err := s.Provision(ctx, invite)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Provision(ctx, invite)
	if err != nil || again["secret"] != provisioned["secret"] {
		t.Fatal("provisioning rotated pending secret", err)
	}
	const password = "test-only-long-password"
	step := uint64(time.Now().Unix() / 30)
	code, err := hotp.GenerateCode(provisioned["secret"], step)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(ctx, invite, password, code); err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(ctx, invite, password, code); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("invitation replay accepted", err)
	}
	if _, err = s.SignIn(ctx, provisioned["email"], password, code); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("activation OTP replay accepted", err)
	}
	code, err = hotp.GenerateCode(provisioned["secret"], step+1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	sessions := make(chan string, 2)
	for range 2 {
		wg.Go(func() {
			token, err := s.SignIn(ctx, provisioned["email"], password, code)
			if err == nil {
				sessions <- token
			}
			results <- err
		})
	}
	wg.Wait()
	close(results)
	close(sessions)
	successes, denied := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrStaffDenied) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || denied != 1 {
		t.Fatalf("OTP replay: %d successes, %d denied", successes, denied)
	}
	for token := range sessions {
		if _, err = s.Me(ctx, token); err != nil {
			t.Fatal(err)
		}
		if err = s.SignOut(ctx, token); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Me(ctx, token); !errors.Is(err, ErrStaffDenied) {
			t.Fatal("signed-out session accepted", err)
		}
	}
	for range 5 {
		if _, err = s.SignIn(ctx, provisioned["email"], "wrong password", code); !errors.Is(err, ErrStaffDenied) {
			t.Fatal("bad password accepted", err)
		}
	}
	staff, err := queries.New(pool).StaffByID(ctx, id)
	if err != nil || staff.Failures != 5 || !staff.LockedUntil.Valid || !staff.LockedUntil.Time.After(time.Now().Add(14*time.Minute)) {
		t.Fatal("lockout not persisted", err)
	}
	var activated int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM donor_staff_audit WHERE target=$1 AND action='staff.activated'", id.String()).Scan(&activated); err != nil || activated != 1 {
		t.Fatal("activation audit missing or duplicated", err)
	}
}
