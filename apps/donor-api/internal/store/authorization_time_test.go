package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func TestProofExpiryIsCheckedAfterWaitingForOwnerLock(t *testing.T) {
	pool := database(t)
	for _, kind := range []string{"donor", "staff"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback(ctx)
			var redeem func(pgx.Tx) error
			var expire func()
			if kind == "donor" {
				id, _ := donor(t, pool)
				proof := link(t, pool, id, time.Now())
				if _, err := queries.New(lock).LockDonor(ctx, id); err != nil {
					t.Fatal(err)
				}
				redeem = func(tx pgx.Tx) error { _, _, err := ClaimInTx(ctx, tx, proof); return err }
				expire = func() {
					mustExec(t, lock, "UPDATE donor_login_links SET expires_at=$2 WHERE donor_id=$1", id, time.Now().UTC())
				}
			} else {
				id, _ := staffFixture(t, pool, "SUPPORT")
				t.Cleanup(func() {
					if _, err := pool.Exec(context.Background(), "DELETE FROM donor_staff_invites WHERE staff_id=$1", id); err != nil {
						t.Error(err)
					}
				})
				proof, err := createStaffInvite(ctx, queries.New(pool), id, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := queries.New(lock).StaffByID(ctx, id); err != nil {
					t.Fatal(err)
				}
				redeem = func(tx pgx.Tx) error { _, _, err := staffInvitation(ctx, queries.New(tx), proof); return err }
				expire = func() {
					mustExec(t, lock, "UPDATE donor_staff_invites SET expires_at=$2 WHERE staff_id=$1", id, time.Now().UTC())
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			pid := tx.Conn().PgConn().PID()
			done := make(chan error, 1)
			go func() { done <- redeem(tx) }()
			for {
				var blocked bool
				if err := pool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1))>0", pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatal("proof check did not wait for owner lock", err)
				case <-time.After(time.Millisecond):
				}
			}
			expire()
			if err := lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if !errors.Is(err, ErrDenied) && !errors.Is(err, ErrStaffDenied) {
				t.Fatal("expired proof accepted after lock wait", err)
			}
		})
	}
}
