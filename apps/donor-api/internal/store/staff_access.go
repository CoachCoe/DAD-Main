package store

import (
	"context"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

func staffTransaction[T any](ctx context.Context, s *StaffService, token, permission string, run func(pgx.Tx, queries.DonorStaff, time.Time) (T, error)) (T, error) {
	var empty T
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	now := time.Now().UTC()
	actor, err := requireStaff(ctx, queries.New(tx), token, permission, now)
	if err != nil {
		return empty, err
	}
	result, err := run(tx, actor, now)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return result, nil
}

func createStaffInvite(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	inviteID, err := NewID()
	if err != nil {
		return "", err
	}
	if err = q.InsertStaffInvite(ctx, queries.InsertStaffInviteParams{ID: inviteID, StaffID: id, TokenHash: HashToken(token), ExpiresAt: timestamp(now.Add(24 * time.Hour))}); err != nil {
		return "", err
	}
	return token, nil
}

func inviteStaff(ctx context.Context, q *queries.Queries, actor pgtype.UUID, email, name, role string, now time.Time) (string, error) {
	canonical, err := identity.NormalizeEmail(email)
	if err != nil || identity.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 100 || len(staffPermissions(role)) == 0 {
		return "", ErrStaffInput
	}
	id, err := NewID()
	if err != nil {
		return "", err
	}
	_, err = q.InsertStaff(ctx, queries.InsertStaffParams{ID: id, Email: canonical, Name: identity.TrimSpace(name), Role: role})
	if err != nil {
		var constraint *pgconn.PgError
		if errors.As(err, &constraint) && constraint.Code == "23505" {
			return "", ErrStaffConflict
		}
		return "", err
	}
	token, err := createStaffInvite(ctx, q, id, now)
	if err != nil {
		return "", err
	}
	if err = auditStaff(ctx, q, actor, "staff.invited", id.String(), map[string]string{"role": role}, now); err != nil {
		return "", err
	}
	return token, nil
}

func (s *StaffService) Bootstrap(ctx context.Context, email, name string) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "LOCK TABLE donor_staff IN EXCLUSIVE MODE"); err != nil {
		return "", err
	}
	q := queries.New(tx)
	rows, err := q.LockStaffAccounts(ctx)
	if err != nil {
		return "", err
	}
	if len(rows) > 0 {
		return "", ErrStaffConflict
	}
	token, err := inviteStaff(ctx, q, pgtype.UUID{}, email, name, "ADMIN", time.Now().UTC())
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}

func (s *StaffService) Invite(ctx context.Context, token, email, name, role string) (string, error) {
	return staffTransaction(ctx, s, token, "staff.manage", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (string, error) {
		q := queries.New(tx)
		// Recheck after locking so a concurrent administrator cannot revoke this action.
		if _, err := q.StaffByID(ctx, actor.ID); err != nil {
			return "", err
		}
		if _, err := requireStaff(ctx, q, token, "staff.manage", now); err != nil {
			return "", err
		}
		return inviteStaff(ctx, q, actor.ID, email, name, role, now)
	})
}

func (s *StaffService) Accounts(ctx context.Context, token string) ([]StaffView, error) {
	return staffTransaction(ctx, s, token, "staff.manage", func(tx pgx.Tx, _ queries.DonorStaff, _ time.Time) ([]StaffView, error) {
		rows, err := queries.New(tx).LockStaffAccounts(ctx)
		if err != nil {
			return nil, err
		}
		slices.SortFunc(rows, func(a, b queries.DonorStaff) int {
			if a.Email < b.Email {
				return -1
			}
			if a.Email > b.Email {
				return 1
			}
			return 0
		})
		result := make([]StaffView, 0, len(rows))
		for _, row := range rows {
			result = append(result, staffView(row))
		}
		return result, nil
	})
}

func (s *StaffService) ChangeAccess(ctx context.Context, token string, targetID pgtype.UUID, action, role string) (map[string]interface{}, error) {
	return staffTransaction(ctx, s, token, "staff.manage", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		rows, err := q.LockStaffAccounts(ctx)
		if err != nil {
			return nil, err
		}
		if _, err = requireStaff(ctx, q, token, "staff.manage", now); err != nil {
			return nil, err
		}
		if targetID == actor.ID {
			return nil, ErrStaffConflict
		}
		var target queries.DonorStaff
		otherAdmins := 0
		for _, row := range rows {
			if row.ID == targetID {
				target = row
			} else if row.Role == "ADMIN" && !row.Disabled && row.PasswordHash.Valid {
				otherAdmins++
			}
		}
		if !target.ID.Valid {
			return nil, pgx.ErrNoRows
		}
		if otherAdmins == 0 {
			return nil, ErrStaffConflict
		}
		result := map[string]interface{}{}
		var details interface{}
		var event string
		switch action {
		case "disable":
			target.Disabled = true
			event = "staff.disabled"
			result["disabled"] = true
		case "role":
			if len(staffPermissions(role)) == 0 {
				return nil, ErrStaffInput
			}
			if target.Role == role {
				return map[string]interface{}{"staff": staffView(target)}, nil
			}
			details = map[string]string{"previous": target.Role, "role": role}
			target.Role = role
			event = "staff.role_changed"
			result["staff"] = staffView(target)
		case "recovery":
			details = map[string]bool{"previously_disabled": target.Disabled, "previously_active": target.PasswordHash.Valid}
			target.PasswordHash, target.TotpSecret, target.Disabled = pgtype.Text{}, nil, false
			target.TotpCounter, target.Failures, target.LockedUntil = -1, 0, pgtype.Timestamptz{}
			if err = saveStaffCredentials(ctx, q, target); err != nil {
				return nil, err
			}
			if err = q.InvalidateStaffInvites(ctx, queries.InvalidateStaffInvitesParams{StaffID: target.ID, ConsumedAt: timestamp(now)}); err != nil {
				return nil, err
			}
			invitation, err := createStaffInvite(ctx, q, target.ID, now)
			if err != nil {
				return nil, err
			}
			result["token"] = invitation
			event = "staff.recovery_invited"
		default:
			return nil, ErrStaffInput
		}
		if err = q.SetStaffAccess(ctx, queries.SetStaffAccessParams{ID: target.ID, Disabled: target.Disabled, Role: target.Role}); err != nil {
			return nil, err
		}
		if err = q.RevokeStaffSessions(ctx, queries.RevokeStaffSessionsParams{StaffID: target.ID, RevokedAt: timestamp(now)}); err != nil {
			return nil, err
		}
		if err = auditStaff(ctx, q, actor.ID, event, target.ID.String(), details, now); err != nil {
			return nil, err
		}
		return result, nil
	})
}
