// Package store accesses the donor tables owned by the existing Alembic migrations.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

var ErrDenied = errors.New("donor access denied")

func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, errors.New("donor database URL is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 10
	config.MinConns = 0
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func NewID() (pgtype.UUID, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return pgtype.UUID{}, err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return pgtype.UUID{Bytes: bytes, Valid: true}, nil
}
func timestamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t.UTC(), Valid: true} }
func Authenticated(ctx context.Context, q *queries.Queries, token string, now time.Time) (queries.Donor, error) {
	donor, err := q.AuthenticatedDonor(ctx, queries.AuthenticatedDonorParams{TokenHash: HashToken(token), ExpiresAt: timestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return donor, ErrDenied
	}
	return donor, err
}

// ClaimInTx consumes email proof and inserts a hashed session. The caller must add
// projection refresh and old-browser-session revocation in this SAME transaction,
// commit it, and only then return the new cookie. No HTTP route exposes this yet.
func ClaimInTx(ctx context.Context, tx pgx.Tx, token string, now time.Time) (string, queries.Donor, error) {
	q := queries.New(tx)
	empty := queries.Donor{}
	link, err := q.LoginLink(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", empty, ErrDenied
	}
	if err != nil {
		return "", empty, err
	}
	donor, err := q.LockDonor(ctx, link.DonorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", empty, ErrDenied
	}
	if err != nil {
		return "", empty, err
	}
	if donor.Disabled {
		return "", empty, ErrDenied
	}
	if _, err = q.RedeemLoginLink(ctx, queries.RedeemLoginLinkParams{ID: link.ID, ConsumedAt: timestamp(now)}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrDenied
		}
		return "", empty, err
	}
	if err = q.ConsumeDonorLinks(ctx, queries.ConsumeDonorLinksParams{DonorID: donor.ID, ConsumedAt: timestamp(now)}); err != nil {
		return "", empty, err
	}
	if err = q.MarkClaimed(ctx, queries.MarkClaimedParams{ID: donor.ID, ClaimedAt: timestamp(now)}); err != nil {
		return "", empty, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		return "", empty, err
	}
	sessionToken := base64.RawURLEncoding.EncodeToString(secret)
	id, err := NewID()
	if err != nil {
		return "", empty, err
	}
	err = q.InsertSession(ctx, queries.InsertSessionParams{ID: id, DonorID: donor.ID, TokenHash: HashToken(sessionToken), ExpiresAt: timestamp(now.Add(30 * 24 * time.Hour))})
	if err != nil {
		return "", empty, err
	}
	if !donor.ClaimedAt.Valid {
		donor.ClaimedAt = timestamp(now)
	}
	return sessionToken, donor, nil
}
