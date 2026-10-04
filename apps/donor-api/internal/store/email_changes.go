package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

var ErrEmailChangeConflict = errors.New("this address cannot replace the account email")

type EmailChangeService struct {
	DB            *pgxpool.Pool
	EncryptionKey string
}

func emailChangeProof(key string) (string, []byte, error) {
	proof, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	encrypted, err := credentials.Encrypt([]byte(proof), key)
	return HashToken(proof), encrypted, err
}

func identityEvent(ctx context.Context, q *queries.Queries, donor, change pgtype.UUID, action string) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	return q.RecordIdentityEvent(ctx, queries.RecordIdentityEventParams{ID: id, DonorID: donor, EmailChangeID: change, Action: action})
}

func (s EmailChangeService) Request(ctx context.Context, session, email string) error {
	email, err := identity.NormalizeEmail(email)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	donor, err := donorOwner(session)(ctx, tx, time.Now().UTC())
	if err != nil {
		return err
	}
	if donor.Email == email {
		return ErrEmailChangeConflict
	}
	hash, encrypted, err := emailChangeProof(s.EncryptionKey)
	if err != nil {
		return err
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	q := queries.New(tx)
	if err = q.CancelEmailChanges(ctx, donor.ID); err != nil {
		return err
	}
	if err = q.InsertEmailChange(ctx, queries.InsertEmailChangeParams{ID: id, DonorID: donor.ID, OldEmail: donor.Email, NewEmail: email, TokenHash: hash, EncryptedToken: encrypted, ExpiresAt: timestamp(time.Now().Add(15 * time.Minute))}); err != nil {
		return err
	}
	if err = identityEvent(ctx, q, donor.ID, id, "REQUESTED"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s EmailChangeService) Confirm(ctx context.Context, proof string) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	owner, err := q.EmailChangeOwner(ctx, HashToken(proof))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDenied
	}
	if err != nil {
		return "", err
	}
	donor, err := q.LockDonor(ctx, owner)
	if err != nil {
		return "", err
	}
	change, err := q.EmailChangeByToken(ctx, HashToken(proof))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDenied
	}
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if donor.Disabled || donor.Email != change.OldEmail || !change.ExpiresAt.Time.After(now) || (change.Stage != "CURRENT" && change.Stage != "NEW") {
		return "", ErrDenied
	}
	stage := "NEW"
	if change.Stage == "CURRENT" {
		hash, encrypted, err := emailChangeProof(s.EncryptionKey)
		if err != nil {
			return "", err
		}
		if err = q.AdvanceEmailChange(ctx, queries.AdvanceEmailChangeParams{ID: change.ID, TokenHash: hash, EncryptedToken: encrypted, ExpiresAt: timestamp(now.Add(15 * time.Minute))}); err != nil {
			return "", err
		}
		if err = identityEvent(ctx, q, owner, change.ID, "CURRENT_VERIFIED"); err != nil {
			return "", err
		}
	} else {
		if err = q.SetDonorEmail(ctx, queries.SetDonorEmailParams{ID: owner, Email: change.NewEmail}); err != nil {
			var constraint *pgconn.PgError
			if errors.As(err, &constraint) && constraint.Code == "23505" {
				return "", ErrEmailChangeConflict
			}
			return "", err
		}
		if err = q.RevokeAllSessions(ctx, queries.RevokeAllSessionsParams{DonorID: owner, RevokedAt: timestamp(now)}); err != nil {
			return "", err
		}
		if err = q.ConsumeDonorLinks(ctx, queries.ConsumeDonorLinksParams{DonorID: owner, ConsumedAt: timestamp(now)}); err != nil {
			return "", err
		}
		if err = q.CompleteEmailChange(ctx, queries.CompleteEmailChangeParams{ID: change.ID, CompletedAt: timestamp(now)}); err != nil {
			return "", err
		}
		if err = identityEvent(ctx, q, owner, change.ID, "CHANGED"); err != nil {
			return "", err
		}
		stage = "COMPLETED"
	}
	return stage, tx.Commit(ctx)
}
