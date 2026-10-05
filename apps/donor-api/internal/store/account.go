package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

type AccountService struct{ DB *pgxpool.Pool }

func (s AccountService) ClaimAccount(ctx context.Context, proof, oldSession string) (string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	token, donor, err := ClaimInTx(ctx, tx, proof)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if _, err = RefreshProjectionInTx(ctx, tx, donor.ID, now); err != nil {
		return "", err
	}
	if err = queries.New(tx).RevokeSession(ctx, queries.RevokeSessionParams{TokenHash: HashToken(oldSession), RevokedAt: timestamp(now)}); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}
func (s AccountService) SignOut(ctx context.Context, token string) error {
	return queries.New(s.DB).RevokeSession(ctx, queries.RevokeSessionParams{TokenHash: HashToken(token), RevokedAt: timestamp(time.Now())})
}
func (s AccountService) QueueLogin(ctx context.Context, email string) error {
	canonical, err := identity.NormalizeEmail(email)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = QueueLoginEmailInTx(ctx, tx, canonical, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
