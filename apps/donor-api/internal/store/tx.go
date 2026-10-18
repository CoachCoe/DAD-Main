package store

import (
	"context"
	"time"

	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Donor work runs in one of two transactions. donorRead serves a page from a
// read-only snapshot, so figures assembled from several queries agree even
// while a worker rewrites the projections behind them. donorWrite holds the
// donor lock, which serializes everything that changes one donor's facts:
// requests, workers and staff actions alike.

// readSnapshot is a consistent, read-only view across several queries.
var readSnapshot = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// transact runs fn in a transaction and returns its result once committed,
// or the zero value with the error that rolled it back.
func transact[T any](ctx context.Context, db *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) (T, error)) (T, error) {
	var result T
	err := pgx.BeginTxFunc(ctx, db, opts, func(tx pgx.Tx) (err error) {
		result, err = fn(tx)
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

// donorRead runs fn as the signed-in donor holding token.
func donorRead[T any](ctx context.Context, db *pgxpool.Pool, token string, fn func(*queries.Queries, queries.Donor, time.Time) (T, error)) (T, error) {
	var user signedIn
	now := time.Now().UTC()
	result, err := transact(ctx, db, readSnapshot, func(tx pgx.Tx) (T, error) {
		q := queries.New(tx)
		var err error
		if user, err = authenticate(ctx, q, token, now); err != nil {
			var zero T
			return zero, err
		}
		return fn(q, user.Donor, now)
	})
	if err != nil {
		return result, err
	}
	// The snapshot is read-only, so the session's use is recorded after it.
	// It is advisory, and failing to record it must not fail the read.
	_ = user.touch(ctx, queries.New(db), now)
	return result, nil
}

// ownerResolver authenticates and locks the donor a write acts for: the
// signed-in donor, or the donor a staff member acts on.
type ownerResolver func(context.Context, pgx.Tx) (queries.Donor, error)

// donorOwner resolves the donor signed in with token.
func donorOwner(token string) ownerResolver {
	return func(ctx context.Context, tx pgx.Tx) (queries.Donor, error) {
		user, err := lockSignedIn(ctx, queries.New(tx), token)
		return user.Donor, err
	}
}

// lockSignedIn authenticates token and locks its donor. The session is
// checked again once the lock is held, so a sign-out that committed while the
// lock was awaited is honoured.
func lockSignedIn(ctx context.Context, q *queries.Queries, token string) (signedIn, error) {
	user, err := authenticate(ctx, q, token, time.Now().UTC())
	if err != nil {
		return user, err
	}
	if _, err = q.LockDonor(ctx, user.ID); err != nil {
		return user, err
	}
	now := time.Now().UTC()
	if user, err = authenticate(ctx, q, token, now); err != nil {
		return user, err
	}
	return user, user.touch(ctx, q, now)
}

// donorWrite runs fn holding the lock of the donor owner resolves.
func donorWrite[T any](ctx context.Context, db *pgxpool.Pool, owner ownerResolver, fn func(pgx.Tx, queries.Donor, time.Time) (T, error)) (T, error) {
	return transact(ctx, db, pgx.TxOptions{}, func(tx pgx.Tx) (T, error) {
		d, err := owner(ctx, tx)
		if err != nil {
			var zero T
			return zero, err
		}
		return fn(tx, d, time.Now().UTC())
	})
}

// Staff work is authorized against current database state in the transaction
// that performs it. staffRead only authorizes. staffWrite also locks the
// actor's account and authorizes again, so a revocation that committed while
// the lock was awaited is honoured and none can commit until the write does.
// Access management instead locks every account, in ID order, itself.

func staffRead[T any](ctx context.Context, s *StaffService, token, permission string, fn func(pgx.Tx, queries.DonorStaff, time.Time) (T, error)) (T, error) {
	return transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (T, error) {
		now := time.Now().UTC()
		actor, err := requireStaff(ctx, queries.New(tx), token, permission, now)
		if err != nil {
			var zero T
			return zero, err
		}
		return fn(tx, actor, now)
	})
}

func staffWrite[T any](ctx context.Context, s *StaffService, token, permission string, fn func(pgx.Tx, queries.DonorStaff, time.Time) (T, error)) (T, error) {
	return transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (T, error) {
		actor, err := lockStaff(ctx, queries.New(tx), token, permission)
		if err != nil {
			var zero T
			return zero, err
		}
		return fn(tx, actor, time.Now().UTC())
	})
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
	return transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (T, error) {
		actor, _, err := lockStaffDonor(ctx, queries.New(tx), token, permission, donor)
		if err != nil {
			var zero T
			return zero, err
		}
		return fn(tx, actor, time.Now().UTC())
	})
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
