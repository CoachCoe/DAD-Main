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

func staffFixture(t *testing.T, pool *pgxpool.Pool, role string) (pgtype.UUID, string) {
	t.Helper()
	id, session := testID(t), testID(t)
	token := "staff-test-" + session.String()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "INSERT INTO donor_staff(id,email,name,role) VALUES($1,$2,'Test staff',$3)", id, id.String()+"@example.org", role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, sql := range []string{"DELETE FROM donor_staff_audit WHERE actor_id=$1", "DELETE FROM donor_staff_sessions WHERE staff_id=$1", "DELETE FROM donor_staff WHERE id=$1"} {
			if _, err := pool.Exec(ctx, sql, id); err != nil {
				t.Error(err)
			}
		}
	})
	if _, err := pool.Exec(ctx, "INSERT INTO donor_staff_sessions(id,staff_id,token_hash,expires_at) VALUES($1,$2,$3,$4)", session, id, HashToken(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return id, token
}

func TestStaffMutationHoldsActorLockAndRechecksRevocation(t *testing.T) {
	pool := database(t)
	for _, permission := range []string{"content.publish", "message.resend"} {
		t.Run(permission, func(t *testing.T) {
			id, token := staffFixture(t, pool, "ADMIN")
			s := &StaffService{DB: pool}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := staffTransaction(ctx, s, token, permission, func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (bool, error) {
				probe, err := pool.Begin(ctx)
				if err != nil {
					return false, err
				}
				defer probe.Rollback(ctx)
				var locked pgtype.UUID
				err = probe.QueryRow(ctx, "SELECT id FROM donor_staff WHERE id=$1 FOR UPDATE SKIP LOCKED", id).Scan(&locked)
				if !errors.Is(err, pgx.ErrNoRows) {
					return false, errors.New("staff mutation did not lock actor")
				}
				return true, auditStaff(ctx, queries.New(tx), actor.ID, "test.mutation", id.String(), nil, now)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SignOut(ctx, token); err != nil {
				t.Fatal(err)
			}
			_, err = staffTransaction(ctx, s, token, permission, func(pgx.Tx, queries.DonorStaff, time.Time) (bool, error) {
				t.Error("revoked session reached mutation")
				return true, nil
			})
			if !errors.Is(err, ErrStaffDenied) {
				t.Fatal("revoked session was not denied", err)
			}
		})
	}
}

func TestStaffPermissionMatrixAndExpiredSessions(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	for _, role := range []string{"SUPPORT", "FINANCE", "ADMIN"} {
		id, token := staffFixture(t, pool, role)
		for _, permission := range []string{"donor.read", "message.resend", "gift.refund", "gift.cancel", "data.export", "staff.manage", "content.publish", "audit.read"} {
			allowed := role == "ADMIN" || permission == "donor.read" || permission == "message.resend" || (role == "FINANCE" && (permission == "gift.refund" || permission == "gift.cancel" || permission == "data.export"))
			_, err := requireStaff(ctx, queries.New(pool), token, permission, time.Now())
			if (allowed && err != nil) || (!allowed && !errors.Is(err, ErrStaffForbidden)) {
				t.Fatalf("%s / %s: %v", role, permission, err)
			}
		}
		if _, err := requireStaff(ctx, queries.New(pool), token, "donor.read", time.Now().Add(2*time.Hour)); !errors.Is(err, ErrStaffDenied) {
			t.Fatal("expired session accepted", err)
		}
		if _, err := pool.Exec(ctx, "UPDATE donor_staff SET disabled=true WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		if _, err := requireStaff(ctx, queries.New(pool), token, "donor.read", time.Now()); !errors.Is(err, ErrStaffDenied) {
			t.Fatal("disabled staff accepted", err)
		}
	}
}
