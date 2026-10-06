package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/store/queries"
)

var (
	ErrStaffDenied    = errors.New("staff sign-in required")
	ErrStaffForbidden = errors.New("staff permission denied")
	ErrStaffInput     = errors.New("invalid staff request")
	ErrStaffConflict  = errors.New("staff account change conflicts with current access")
	// ErrStaffExists is an ErrStaffConflict for an invitation to an email that
	// already has a staff account.
	ErrStaffExists = fmt.Errorf("%w: a staff account with this email already exists", ErrStaffConflict)
)

// rolePermissions lists each role's named permissions, sorted. Each role adds
// to the one before it.
var rolePermissions = map[string][]string{
	"SUPPORT": {"donor.read", "message.resend"},
	"FINANCE": {"data.export", "donor.read", "gift.cancel", "gift.refund", "message.resend"},
	"ADMIN":   {"audit.read", "content.publish", "data.export", "donor.read", "gift.cancel", "gift.refund", "message.resend", "staff.manage"},
}

func staffPermissions(role string) []string { return rolePermissions[role] }

type StaffView struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Disabled    bool     `json:"disabled"`
	Active      bool     `json:"active"`
	Permissions []string `json:"permissions"`
}

func staffView(s queries.DonorStaff) StaffView {
	return StaffView{ID: s.ID.String(), Email: s.Email, Name: s.Name, Role: s.Role, Disabled: s.Disabled, Active: s.PasswordHash.Valid, Permissions: staffPermissions(s.Role)}
}

type StaffService struct {
	DB            *pgxpool.Pool
	EncryptionKey string
	dummyHash     string
	passwordSlots chan struct{}
}

func NewStaffService(db *pgxpool.Pool, key string) (*StaffService, error) {
	if err := credentials.ValidateKey(key); err != nil {
		return nil, err
	}
	secret, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	hash, err := credentials.HashPassword(secret)
	if err != nil {
		return nil, err
	}
	return &StaffService{DB: db, EncryptionKey: key, dummyHash: hash, passwordSlots: make(chan struct{}, 2)}, nil
}

func (s *StaffService) passwordSlot(ctx context.Context) (func(), error) {
	select {
	case s.passwordSlots <- struct{}{}:
		return func() { <-s.passwordSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func requireStaff(ctx context.Context, q *queries.Queries, token, permission string, now time.Time) (queries.DonorStaff, error) {
	staff, err := q.AuthenticatedStaff(ctx, queries.AuthenticatedStaffParams{TokenHash: HashToken(token), ExpiresAt: timestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return staff, ErrStaffDenied
	}
	if err != nil {
		return staff, err
	}
	if permission != "" && !slices.Contains(staffPermissions(staff.Role), permission) {
		return staff, ErrStaffForbidden
	}
	return staff, nil
}

func auditStaff(ctx context.Context, q *queries.Queries, actor pgtype.UUID, action, target string, details any, now time.Time) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return q.InsertStaffAudit(ctx, queries.InsertStaffAuditParams{ID: id, ActorID: actor, Action: action, Target: target, Details: raw, CreatedAt: timestamp(now)})
}

func (s *StaffService) Me(ctx context.Context, token string) (StaffView, error) {
	staff, err := requireStaff(ctx, queries.New(s.DB), token, "", time.Now().UTC())
	if err != nil {
		return StaffView{}, err
	}
	return staffView(staff), nil
}

func (s *StaffService) SignOut(ctx context.Context, token string) error {
	_, err := staffWrite(ctx, s, token, "", func(tx pgx.Tx, staff queries.DonorStaff, now time.Time) (struct{}, error) {
		q := queries.New(tx)
		if err := q.RevokeStaffSession(ctx, queries.RevokeStaffSessionParams{TokenHash: HashToken(token), RevokedAt: timestamp(now)}); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, auditStaff(ctx, q, staff.ID, "staff.signed_out", staff.ID.String(), nil, now)
	})
	return err
}
