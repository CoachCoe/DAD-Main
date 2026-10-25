package store

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	partnerInviteLifetime  = 7 * 24 * time.Hour
	partnerSessionLifetime = 12 * time.Hour
)

var (
	// ErrPartnerDenied means no live partner session backs the request.
	ErrPartnerDenied = errors.New("partner sign-in required")
	// ErrPartnerForbidden means the partner user lacks the required role.
	ErrPartnerForbidden = errors.New("partner permission denied")
	// ErrPartnerInvite is an invalid or expired invite.
	ErrPartnerInvite = errors.New("invalid or expired partner invite")
)

// PartnerActor is the authenticated partner user behind a request.
type PartnerActor struct {
	UserID       pgtype.UUID
	PartnerID    pgtype.UUID
	Email        string
	Role         string // ADMIN or VIEWER
	Organization string
}

// PartnerService... (the portal surface). It reuses the donor DB pool; partner
// auth is wholly separate from donor and staff sessions (P-5: a partner login
// is not a donor and cannot give).
type PartnerPortalService struct{ DB *pgxpool.Pool }

// authenticatePartner resolves a partner bearer token to its actor, rejecting
// expired/revoked sessions and disabled users.
func authenticatePartner(ctx context.Context, q *queries.Queries, token string, now time.Time) (PartnerActor, error) {
	row, err := q.AuthenticatedPartnerUser(ctx, queries.AuthenticatedPartnerUserParams{
		TokenHash: HashToken(token), AsOf: timestamp(now),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return PartnerActor{}, ErrPartnerDenied
	}
	if err != nil {
		return PartnerActor{}, err
	}
	return PartnerActor{
		UserID: row.ID, PartnerID: row.PartnerID, Email: row.Email,
		Role: row.Role, Organization: row.Organization,
	}, nil
}

// partnerRead runs fn for an authenticated partner (any role).
func partnerRead[T any](
	ctx context.Context,
	db *pgxpool.Pool,
	token string,
	fn func(*queries.Queries, PartnerActor) (T, error),
) (T, error) {
	var zero T
	q := queries.New(db)
	actor, err := authenticatePartner(ctx, q, token, time.Now().UTC())
	if err != nil {
		return zero, err
	}
	return fn(q, actor)
}

// partnerAdminWrite runs fn in a transaction for an authenticated partner ADMIN
// (§7.2: invite/remove users, edit org, export are admin-only).
func partnerAdminWrite[T any](
	ctx context.Context,
	db *pgxpool.Pool,
	token string,
	fn func(pgx.Tx, PartnerActor, time.Time) (T, error),
) (T, error) {
	return transact(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) (T, error) {
		var zero T
		q := queries.New(tx)
		now := time.Now().UTC()
		actor, err := authenticatePartner(ctx, q, token, now)
		if err != nil {
			return zero, err
		}
		if actor.Role != "ADMIN" {
			return zero, ErrPartnerForbidden
		}
		return fn(tx, actor, now)
	})
}

// Invite adds a partner user by email and emails them a sign-in link (P-4).
// Admin-only. Re-inviting refreshes the role and re-enables a removed user.
func (s PartnerPortalService) Invite(ctx context.Context, token, email, name, role string) error {
	if role != "ADMIN" && role != "VIEWER" {
		return ErrPartnerApplication
	}
	email, err := identity.NormalizeEmail(email)
	if err != nil {
		return ErrPartnerApplication
	}
	name = identity.TrimSpace(name)
	if utf8.RuneCountInString(name) > 100 || !plainText(name, false) {
		return ErrPartnerApplication
	}
	_, err = partnerAdminWrite(ctx, s.DB, token, func(tx pgx.Tx, actor PartnerActor, now time.Time) (struct{}, error) {
		q := queries.New(tx)
		id, err := NewID()
		if err != nil {
			return struct{}{}, err
		}
		user, err := q.InvitePartnerUser(ctx, queries.InvitePartnerUserParams{
			ID: id, PartnerID: actor.PartnerID, Email: email, Name: name, Role: role,
			InvitedBy: actor.UserID, CreatedAt: timestamp(now),
		})
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, queuePartnerLogin(ctx, q, user, partnerLinkInvited, now)
	})
	return err
}

// AcceptInvite exchanges an emailed sign-in link, whether an invitation or a
// requested sign-in, for a partner session token. Each link works once.
func (s PartnerPortalService) AcceptInvite(ctx context.Context, invite string) (string, error) {
	return transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (string, error) {
		q := queries.New(tx)
		now := time.Now().UTC()
		user, err := q.PartnerUserByInvite(ctx, pgtype.Text{String: HashToken(invite), Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrPartnerInvite
		}
		if err != nil {
			return "", err
		}
		if user.Disabled || !user.InviteExpiresAt.Valid || now.After(user.InviteExpiresAt.Time) {
			return "", ErrPartnerInvite
		}
		if err = q.AcceptPartnerInvite(ctx, queries.AcceptPartnerInviteParams{ID: user.ID, AcceptedAt: timestamp(now)}); err != nil {
			return "", err
		}
		return s.issueSession(ctx, q, user.ID, now)
	})
}

// issueSession mints a partner session token for a user.
func (s PartnerPortalService) issueSession(ctx context.Context, q *queries.Queries, userID pgtype.UUID, now time.Time) (string, error) {
	sessionID, err := NewID()
	if err != nil {
		return "", err
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	if err = q.InsertPartnerSession(ctx, queries.InsertPartnerSessionParams{
		ID: sessionID, PartnerUserID: userID, TokenHash: HashToken(token),
		ExpiresAt: timestamp(now.Add(partnerSessionLifetime)), CreatedAt: timestamp(now),
	}); err != nil {
		return "", err
	}
	return token, nil
}

// SignOut revokes the calling partner session.
func (s PartnerPortalService) SignOut(ctx context.Context, token string) error {
	return q(s.DB).RevokePartnerSession(ctx, queries.RevokePartnerSessionParams{
		TokenHash: HashToken(token), RevokedAt: timestamp(time.Now().UTC()),
	})
}

// PartnerUser is a portal user row for the §7.2 users list.
type PartnerUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

// Users lists the partner org's active users (any partner role may view).
func (s PartnerPortalService) Users(ctx context.Context, token string) ([]PartnerUser, error) {
	return partnerRead(ctx, s.DB, token, func(q *queries.Queries, actor PartnerActor) ([]PartnerUser, error) {
		rows, err := q.PartnerUsersForPartner(ctx, actor.PartnerID)
		if err != nil {
			return nil, err
		}
		out := make([]PartnerUser, 0, len(rows))
		for _, r := range rows {
			out = append(out, PartnerUser{ID: r.ID.String(), Email: r.Email, Name: r.Name, Role: r.Role})
		}
		return out, nil
	})
}

// CompanyCode returns the calling partner's company code (EM-1), which any
// partner role may view (§7.2).
func (s PartnerPortalService) CompanyCode(ctx context.Context, token string) (string, error) {
	return partnerRead(ctx, s.DB, token, func(q *queries.Queries, actor PartnerActor) (string, error) {
		code, err := q.PartnerCompanyCode(ctx, actor.PartnerID)
		return code.String, err
	})
}

// RemoveUser disables a partner user (P-4, admin-only). An admin cannot remove
// themselves (avoids orphaning the org).
func (s PartnerPortalService) RemoveUser(ctx context.Context, token string, userID pgtype.UUID) error {
	_, err := partnerAdminWrite(ctx, s.DB, token, func(tx pgx.Tx, actor PartnerActor, _ time.Time) (struct{}, error) {
		if userID == actor.UserID {
			return struct{}{}, ErrPartnerForbidden
		}
		return struct{}{}, queries.New(tx).DisablePartnerUser(ctx, queries.DisablePartnerUserParams{PartnerID: actor.PartnerID, ID: userID})
	})
	return err
}

// q is a small helper to run a one-shot query off the pool.
func q(db *pgxpool.Pool) *queries.Queries { return queries.New(db) }
