package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/dbroles"
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

func TestStaffWriteHoldsActorLockAndRechecksRevocation(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	id, token := staffFixture(t, pool, "ADMIN")
	s := &StaffService{DB: api}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := staffWrite(ctx, s, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (bool, error) {
		probe, err := pool.Begin(ctx)
		if err != nil {
			return false, err
		}
		defer probe.Rollback(ctx)
		var locked pgtype.UUID
		err = probe.QueryRow(ctx, "SELECT id FROM donor_staff WHERE id=$1 FOR UPDATE SKIP LOCKED", id).Scan(&locked)
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, errors.New("staff write did not lock its actor")
		}
		return true, auditStaff(ctx, queries.New(tx), actor.ID, "test.mutation", id.String(), nil, now)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SignOut(ctx, token); err != nil {
		t.Fatal(err)
	}
	_, err = staffWrite(ctx, s, token, "content.publish", func(pgx.Tx, queries.DonorStaff, time.Time) (bool, error) {
		t.Error("revoked session reached the write")
		return true, nil
	})
	if !errors.Is(err, ErrStaffDenied) {
		t.Fatalf("revoked session: got %v, want ErrStaffDenied", err)
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

func TestRecoveryReplacesPasskeysAndRevokesSessions(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	s := staffService(t, dbroles.API)
	_, adminKey := activatedStaff(t, pool, s, "ADMIN")
	admin, err := signIn(t, s, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	target, targetKey := activatedStaff(t, pool, s, "ADMIN")
	session, err := signIn(t, s, targetKey)
	if err != nil {
		t.Fatal(err)
	}
	change, err := s.ChangeAccess(ctx, admin, target, "recovery", "")
	if err != nil || change.Token == "" {
		t.Fatal("recovery invitation not issued", err)
	}
	if _, err = s.Me(ctx, session); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("recovery left a session usable", err)
	}
	if _, err = signIn(t, s, targetKey); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("recovery left a passkey usable", err)
	}
	accounts, err := s.Accounts(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.ID == target.String() && account.Active {
			t.Fatal("recovered account still reported active")
		}
	}
	var details string
	if err = pool.QueryRow(ctx, "SELECT details::text FROM donor_staff_audit WHERE target=$1 AND action='staff.recovery_invited'", target.String()).Scan(&details); err != nil || details != `{"previously_active":true,"previously_disabled":false}` {
		t.Fatal("recovery audit missing", details, err)
	}
	invitation, err := s.BeginActivation(ctx, change.Token)
	if err != nil {
		t.Fatal(err)
	}
	replacement := newAuthenticator(t, staffTestOrigin, staffTestRPID)
	if _, err = s.Activate(ctx, invitation.ID, replacement.register(t, invitation.Options)); err != nil {
		t.Fatal(err)
	}
	if _, err = signIn(t, s, replacement); err != nil {
		t.Fatal("replacement passkey rejected", err)
	}
}

func TestAdministratorInvitesStaffWhoAreInactiveUntilTheyEnrol(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	_, admin := staffFixture(t, pool, "ADMIN")
	email := testID(t).String() + "@example.org"
	t.Cleanup(func() {
		for _, sql := range []string{
			"DELETE FROM donor_staff_audit WHERE target IN (SELECT id::text FROM donor_staff WHERE email=$1)",
			"DELETE FROM donor_staff_ceremonies WHERE invite_id IN (SELECT i.id FROM donor_staff_invites i JOIN donor_staff s ON s.id=i.staff_id WHERE s.email=$1)",
			"DELETE FROM donor_staff_invites WHERE staff_id IN (SELECT id FROM donor_staff WHERE email=$1)",
			"DELETE FROM donor_staff WHERE email=$1",
		} {
			mustExec(t, pool, sql, email)
		}
	})
	s := staffService(t, dbroles.API)
	token, err := s.Invite(ctx, admin, email, "New Person", "SUPPORT")
	if err != nil || token == "" {
		t.Fatalf("invitation: %v", err)
	}
	accounts, err := s.Accounts(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	invited := slices.IndexFunc(accounts, func(a StaffView) bool { return a.Email == email })
	if invited < 0 || accounts[invited].Active || accounts[invited].Role != "SUPPORT" {
		t.Fatalf("invited account missing or active: %+v", accounts)
	}
	if _, err = s.BeginActivation(ctx, token); err != nil {
		t.Fatalf("the invitation does not start enrolment: %v", err)
	}
}

func TestBreakGlassRecoveryReplacesOnlyAnEnabledAdministratorsPasskeys(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	s := staffService(t, dbroles.API)
	operator := staffService(t, dbroles.StaffCLI)
	admin, key := activatedStaff(t, pool, s, "ADMIN")
	session, err := signIn(t, s, key)
	if err != nil {
		t.Fatal(err)
	}
	support, _ := invitedStaff(t, pool, "SUPPORT")
	disabled, _ := activatedStaff(t, pool, s, "ADMIN")
	mustExec(t, pool, "UPDATE donor_staff SET disabled=true WHERE id=$1", disabled)
	for _, other := range []string{support.String() + "@example.org", disabled.String() + "@example.org", "nobody@example.org"} {
		if _, err := operator.Recover(ctx, other); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("break-glass recovery of %s: got %v, want no such administrator", other, err)
		}
	}
	token, err := operator.Recover(ctx, admin.String()+"@example.org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Me(ctx, session); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("break-glass recovery left a session usable", err)
	}
	if _, err = signIn(t, s, key); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("break-glass recovery left a passkey usable", err)
	}
	var actor pgtype.UUID
	if err = pool.QueryRow(ctx, "SELECT actor_id FROM donor_staff_audit WHERE target=$1 AND action='staff.recovery_invited'", admin.String()).Scan(&actor); err != nil || actor.Valid {
		t.Fatal("break-glass recovery not audited as an operator action", err)
	}
	invitation, err := s.BeginActivation(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Activate(ctx, invitation.ID, key.register(t, invitation.Options)); err != nil {
		t.Fatal(err)
	}
}
