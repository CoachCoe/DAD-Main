package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

// Donor work runs in one of two transactions. donorRead serves a page from a
// read-only snapshot, so figures assembled from several queries agree even
// while a worker rewrites the projections behind them. donorWrite holds the
// donor lock, which serializes everything that changes one donor's facts:
// requests, workers and staff actions alike.

// donorRead runs fn as the signed-in donor holding token.
func donorRead[T any](ctx context.Context, db *pgxpool.Pool, token string, fn func(*queries.Queries, queries.Donor, time.Time) (T, error)) (T, error) {
	var result T
	err := pgx.BeginTxFunc(ctx, db, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := queries.New(tx)
		now := time.Now().UTC()
		donor, err := authenticate(ctx, q, token, now)
		if err != nil {
			return err
		}
		result, err = fn(q, donor, now)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

// ownerResolver authenticates and locks the donor a write acts for: the
// signed-in donor, or the donor a staff member acts on.
type ownerResolver func(context.Context, pgx.Tx) (queries.Donor, error)

// donorOwner resolves the donor signed in with token. The session is checked
// again once the lock is held, so a sign-out that committed while the lock
// was awaited is honoured.
func donorOwner(token string) ownerResolver {
	return func(ctx context.Context, tx pgx.Tx) (queries.Donor, error) {
		q := queries.New(tx)
		d, err := authenticate(ctx, q, token, time.Now().UTC())
		if err != nil {
			return d, err
		}
		if _, err = q.LockDonor(ctx, d.ID); err != nil {
			return d, err
		}
		return authenticate(ctx, q, token, time.Now().UTC())
	}
}

// donorWrite runs fn holding the lock of the donor owner resolves.
func donorWrite[T any](ctx context.Context, db *pgxpool.Pool, owner ownerResolver, fn func(pgx.Tx, queries.Donor, time.Time) (T, error)) (T, error) {
	var result T
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		d, err := owner(ctx, tx)
		if err != nil {
			return err
		}
		result, err = fn(tx, d, time.Now().UTC())
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

// Staff work is authorized against current database state in the transaction
// that performs it. staffRead only authorizes. staffWrite also locks the
// actor's account and authorizes again, so a revocation that committed while
// the lock was awaited is honoured and none can commit until the write does.
// Access management instead locks every account, in ID order, itself.

func staffRead[T any](ctx context.Context, s *StaffService, token, permission string, fn func(pgx.Tx, queries.DonorStaff, time.Time) (T, error)) (T, error) {
	var result T
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		actor, err := requireStaff(ctx, queries.New(tx), token, permission, now)
		if err != nil {
			return err
		}
		result, err = fn(tx, actor, now)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

func staffWrite[T any](ctx context.Context, s *StaffService, token, permission string, fn func(pgx.Tx, queries.DonorStaff, time.Time) (T, error)) (T, error) {
	var result T
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, err := lockStaff(ctx, queries.New(tx), token, permission)
		if err != nil {
			return err
		}
		result, err = fn(tx, actor, time.Now().UTC())
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

// lockStaff authorizes token, locks the actor's account and authorizes again.
func lockStaff(ctx context.Context, q *queries.Queries, token, permission string) (queries.DonorStaff, error) {
	actor, err := requireStaff(ctx, q, token, permission, time.Now().UTC())
	if err != nil {
		return actor, err
	}
	if _, err = q.StaffByID(ctx, actor.ID); err != nil {
		return actor, err
	}
	return requireStaff(ctx, q, token, permission, time.Now().UTC())
}

// staffDonorWrite is staffWrite for work on one donor's facts. The donor lock
// is taken after the actor's, the order every staff writer uses.
func staffDonorWrite[T any](ctx context.Context, s *StaffService, token, permission string, donor pgtype.UUID, fn func(pgx.Tx, queries.DonorStaff, time.Time) (T, error)) (T, error) {
	var result T
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		actor, _, err := lockStaffDonor(ctx, queries.New(tx), token, permission, donor)
		if err != nil {
			return err
		}
		result, err = fn(tx, actor, time.Now().UTC())
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

func lockStaffDonor(ctx context.Context, q *queries.Queries, token, permission string, id pgtype.UUID) (queries.DonorStaff, queries.Donor, error) {
	if _, err := lockStaff(ctx, q, token, permission); err != nil {
		return queries.DonorStaff{}, queries.Donor{}, err
	}
	donor, err := q.LockDonor(ctx, id)
	if err != nil {
		return queries.DonorStaff{}, donor, err
	}
	actor, err := requireStaff(ctx, q, token, permission, time.Now().UTC())
	return actor, donor, err
}
