package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store/queries"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
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
	"ADMIN": {
		"audit.read",
		"content.publish",
		"data.export",
		"donor.read",
		"gift.cancel",
		"gift.refund",
		"message.resend",
		"staff.manage",
	},
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

// staffView shows an account; it is active while it holds a passkey.
func staffView(s queries.DonorStaff, active bool) StaffView {
	return StaffView{
		ID:          s.ID.String(),
		Email:       s.Email,
		Name:        s.Name,
		Role:        s.Role,
		Disabled:    s.Disabled,
		Active:      active,
		Permissions: staffPermissions(s.Role),
	}
}

type StaffService struct {
	DB       *pgxpool.Pool
	WebAuthn *webauthn.WebAuthn
}

// NewStaffService verifies passkeys for the staff origin. Its host is the
// relying party ID, so the public site can never use a staff passkey.
func NewStaffService(db *pgxpool.Pool, origin string) (*StaffService, error) {
	if err := payments.ValidateOrigin(origin); err != nil {
		return nil, err
	}
	u, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	ceremony := webauthn.TimeoutConfig{Enforce: true, Timeout: ceremonyLifetime, TimeoutUVD: ceremonyLifetime}
	w, err := webauthn.New(&webauthn.Config{
		RPID:                  u.Hostname(),
		RPDisplayName:         "DAD staff",
		RPOrigins:             []string{origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: ceremony, Registration: ceremony},
	})
	if err != nil {
		return nil, err
	}
	return &StaffService{DB: db, WebAuthn: w}, nil
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
	return q.InsertStaffAudit(
		ctx,
		queries.InsertStaffAuditParams{ID: id, ActorID: actor, Action: action, Target: target, Details: raw, CreatedAt: timestamp(now)},
	)
}

func (s *StaffService) Me(ctx context.Context, token string) (StaffView, error) {
	staff, err := requireStaff(ctx, queries.New(s.DB), token, "", time.Now().UTC())
	if err != nil {
		return StaffView{}, err
	}
	// A session begins only with a passkey, and recovery revokes both together.
	return staffView(staff, true), nil
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
