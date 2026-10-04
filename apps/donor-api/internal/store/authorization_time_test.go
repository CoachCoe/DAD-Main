package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"impactgraph/donor-api/internal/store/queries"
)

func TestFinancialAuthorizationRechecksExpirationAfterLock(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	donorID, _ := donor(t, pool)
	token := "expiry-test-" + testID(t).String()
	started := time.Now().Add(-time.Hour)
	expired := started.Add(30 * time.Minute)
	if _, err := pool.Exec(ctx, "INSERT INTO donor_sessions(id,donor_id,token_hash,expires_at) VALUES($1,$2,$3,$4)", testID(t), donorID, HashToken(token), expired); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = donorOwner(token)(ctx, tx, started); !errors.Is(err, ErrDenied) {
		t.Fatal("stale pre-lock time authorized donor", err)
	}
	staffID, staffToken := staffFixture(t, pool, "FINANCE")
	if _, err = pool.Exec(ctx, "UPDATE donor_staff_sessions SET expires_at=$2 WHERE staff_id=$1", staffID, expired); err != nil {
		t.Fatal(err)
	}
	if _, _, err = staffFinancialOwner(ctx, queries.New(tx), staffToken, donorID, "gift.refund", started); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("stale pre-lock time authorized refund", err)
	}
}
