package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

const sessionLifetime = 30 * 24 * time.Hour

type AccountService struct{ DB *pgxpool.Pool }

// ClaimAccount redeems a single-use sign-in proof for a new session token and
// revokes previousSession, the session the client held before, if any.
func (s AccountService) ClaimAccount(ctx context.Context, proof, previousSession string) (string, error) {
	var session string
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		token, donor, err := claim(ctx, tx, proof)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if _, err = refreshProjection(ctx, tx, donor.ID, now); err != nil {
			return err
		}
		session = token
		return queries.New(tx).RevokeSession(ctx, queries.RevokeSessionParams{TokenHash: HashToken(previousSession), RevokedAt: timestamp(now)})
	})
	return session, err
}

// claim consumes a sign-in proof and every other open link for its donor,
// marks the account claimed and inserts a hashed session. The session is
// usable only once tx commits.
func claim(ctx context.Context, tx pgx.Tx, proof string) (string, queries.Donor, error) {
	q := queries.New(tx)
	link, err := q.LoginLink(ctx, HashToken(proof))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", queries.Donor{}, ErrDenied
	}
	if err != nil {
		return "", queries.Donor{}, err
	}
	donor, err := q.LockDonor(ctx, link.DonorID)
	if err != nil {
		return "", queries.Donor{}, err
	}
	if donor.Disabled {
		return "", queries.Donor{}, ErrDenied
	}
	now := time.Now().UTC()
	if _, err = q.RedeemLoginLink(ctx, queries.RedeemLoginLinkParams{ID: link.ID, ConsumedAt: timestamp(now)}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrDenied
		}
		return "", queries.Donor{}, err
	}
	if err = q.ConsumeDonorLinks(ctx, queries.ConsumeDonorLinksParams{DonorID: donor.ID, ConsumedAt: timestamp(now)}); err != nil {
		return "", queries.Donor{}, err
	}
	if err = q.MarkClaimed(ctx, queries.MarkClaimedParams{ID: donor.ID, ClaimedAt: timestamp(now)}); err != nil {
		return "", queries.Donor{}, err
	}
	token, err := randomToken(32)
	if err != nil {
		return "", queries.Donor{}, err
	}
	id, err := NewID()
	if err != nil {
		return "", queries.Donor{}, err
	}
	err = q.InsertSession(ctx, queries.InsertSessionParams{ID: id, DonorID: donor.ID, TokenHash: HashToken(token), ExpiresAt: timestamp(now.Add(sessionLifetime))})
	return token, donor, err
}

func (s AccountService) SignOut(ctx context.Context, token string) error {
	return queries.New(s.DB).RevokeSession(ctx, queries.RevokeSessionParams{TokenHash: HashToken(token), RevokedAt: timestamp(time.Now())})
}

// QueueLogin queues a sign-in link for email's donor that returns them to
// next when ValidReturnPath accepts it; any other next returns them to the default page.
// An unknown address is not an error, so the response cannot reveal whether
// an account exists.
func (s AccountService) QueueLogin(ctx context.Context, email, next string) error {
	canonical, err := identity.NormalizeEmail(email)
	if err != nil {
		return err
	}
	if !ValidReturnPath(next) {
		next = ""
	}
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		return queueLoginEmail(ctx, queries.New(tx), canonical, next, time.Now().UTC())
	})
}
