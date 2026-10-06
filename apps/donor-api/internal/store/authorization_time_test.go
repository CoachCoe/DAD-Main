package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

// TestOwnerSessionIsRecheckedAfterLock expires the session while its owner
// waits for the donor lock; the expiry must be honoured once the lock is held.
func TestOwnerSessionIsRecheckedAfterLock(t *testing.T) {
	pool := database(t)
	for _, kind := range []string{"donor", "staff"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			donorID, _ := donor(t, pool)
			var resolve func(pgx.Tx) error
			var expire string
			var subject pgtype.UUID
			if kind == "donor" {
				token := "owner-" + testID(t).String()
				if _, err := pool.Exec(ctx, "UPDATE donors SET claimed_at=now() WHERE id=$1", donorID); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, "INSERT INTO donor_sessions(id,donor_id,token_hash,expires_at) VALUES($1,$2,$3,$4)", testID(t), donorID, HashToken(token), time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				resolve = func(tx pgx.Tx) error { _, err := donorOwner(token)(ctx, tx); return err }
				expire, subject = "UPDATE donor_sessions SET expires_at=now() WHERE donor_id=$1", donorID
			} else {
				staffID, staffToken := staffFixture(t, pool, "FINANCE")
				resolve = func(tx pgx.Tx) error {
					_, _, err := lockStaffDonor(ctx, queries.New(tx), staffToken, "gift.refund", donorID)
					return err
				}
				expire, subject = "UPDATE donor_staff_sessions SET expires_at=now() WHERE staff_id=$1", staffID
			}
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback(ctx)
			if _, err = queries.New(lock).LockDonor(ctx, donorID); err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			done := make(chan error, 1)
			go func() { done <- resolve(tx) }()
			awaitBlocked(ctx, t, pool, tx, done)
			mustExec(t, lock, expire, subject)
			if err = lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, ErrDenied) && !errors.Is(err, ErrStaffDenied) {
				t.Fatalf("session expired during lock wait was accepted: %v", err)
			}
		})
	}
}

// awaitBlocked waits until tx's connection is blocked on another
// transaction's lock, failing if the work in progress finishes first.
func awaitBlocked(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tx pgx.Tx, done <-chan error) {
	t.Helper()
	pid := tx.Conn().PgConn().PID()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "SELECT cardinality(pg_blocking_pids($1))>0", pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("work did not wait for the lock: %v", err)
		case <-time.After(time.Millisecond):
		}
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
				redeem = func(tx pgx.Tx) error { _, _, err := claim(ctx, tx, proof); return err }
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
			done := make(chan error, 1)
			go func() { done <- redeem(tx) }()
			awaitBlocked(ctx, t, pool, tx, done)
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
