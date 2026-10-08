// Command seed fills a disposable database with a synthetic donor population
// for load testing: giving histories, referral trees and visits written as
// the checkout and webhook paths store them, with projections derived by the
// projection worker's own code.
//
// It refuses any database that might hold real donors. The URL must be given
// as a flag, never taken from the environment, and -confirm must repeat the
// database's name. A database holding any donor without a seeded address is
// refused outright; one already seeded is refused unless -append is set.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"impactgraph/donor-api/internal/migrations"
)

// fixtureCodes is how many referral visits the fixture describes.
const fixtureCodes = 5000

type options struct {
	databaseURL, confirm, fixture string
	donors, sessions, workers     int
	seed                          uint64
	appendPopulation              bool
}

func parse(args []string) (options, error) {
	var o options
	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	flags.StringVar(&o.databaseURL, "database-url", "", "disposable database to fill (required; the environment is never read)")
	flags.StringVar(&o.confirm, "confirm", "", "the database's name, repeated to confirm it is disposable (required)")
	flags.IntVar(&o.donors, "donors", 50000, "donors to create (1–1,000,000)")
	flags.Uint64Var(&o.seed, "seed", 1, "random seed; the same seed and size give the same population")
	flags.BoolVar(&o.appendPopulation, "append", false, "add to a database that already holds a seeded population")
	flags.IntVar(&o.sessions, "sessions", 2000, "donor sessions to create for authenticated load (0 for none)")
	flags.StringVar(&o.fixture, "fixture", "", "new file, outside any repository, for session tokens and referral codes (mode 0600)")
	flags.IntVar(&o.workers, "workers", 8, "concurrent projection refreshes (1–64)")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	switch {
	case flags.NArg() != 0:
		return o, errors.New("unexpected arguments")
	case o.databaseURL == "" || o.confirm == "":
		return o, errors.New("-database-url and -confirm are required")
	case o.donors < 1 || o.donors > 1_000_000 || o.sessions < 0 || o.workers < 1 || o.workers > 64:
		return o, errors.New("invalid population arguments")
	case o.fixture == "" && o.sessions > 0:
		return o, errors.New("-fixture is required to hand out sessions")
	}
	config, err := pgxpool.ParseConfig(o.databaseURL)
	if err != nil {
		return o, errors.New("invalid -database-url")
	}
	if config.ConnConfig.Database == "" || config.ConnConfig.Database != o.confirm {
		return o, errors.New("-confirm must repeat the database name in -database-url")
	}
	return o, nil
}

// insideRepository reports whether path would land in a git working tree,
// where a fixture of live session tokens could be committed.
func insideRepository(path string) (bool, error) {
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		dir = parent
	}
}

// createFixture opens a new private file for session tokens, refusing an
// existing file or one inside a working tree.
func createFixture(path string) (*os.File, error) {
	if inside, err := insideRepository(path); err != nil || inside {
		return nil, errors.New("-fixture must be outside any git working tree")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create fixture: %w", err)
	}
	return file, nil
}

// guard returns how many seeded donors the database already holds, refusing
// a database with any other donor, or a seeded one unless appending.
func guard(ctx context.Context, pool *pgxpool.Pool, appendPopulation bool) (int, error) {
	var real, seeded int
	err := pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE email NOT LIKE '%@"+emailDomain+"'), count(*) FILTER (WHERE email LIKE '%@"+emailDomain+"') FROM donors").Scan(&real, &seeded)
	switch {
	case err != nil:
		return 0, err
	case real > 0:
		return 0, fmt.Errorf("refusing: the database holds %d donors that were not seeded", real)
	case seeded > 0 && !appendPopulation:
		return 0, fmt.Errorf("refusing: the database already holds %d seeded donors; pass -append to add more", seeded)
	}
	return seeded, nil
}

type report struct {
	Donors             int              `json:"donors"`
	Offset             int              `json:"offset"`
	Seed               uint64           `json:"seed"`
	Shape              shape            `json:"shape"`
	Rows               map[string]int64 `json:"rows"`
	Sessions           int              `json:"sessions"`
	SuppressedMessages int64            `json:"suppressed_messages"`
	GenerateSeconds    float64          `json:"generate_seconds"`
	CopySeconds        float64          `json:"copy_seconds"`
	ProjectionSeconds  float64          `json:"projection_seconds"`
	TotalSeconds       float64          `json:"total_seconds"`
}

// seed writes a population generated as of now into pool and derives its
// projections, after checking the database may receive it.
func seed(ctx context.Context, pool *pgxpool.Pool, o options, now time.Time) (*population, report, error) {
	r := report{Donors: o.donors, Seed: o.seed}
	if err := migrations.Verify(ctx, pool); err != nil {
		return nil, r, errors.New("seed database schema is not current; run migrations first")
	}
	offset, err := guard(ctx, pool, o.appendPopulation)
	if err != nil {
		return nil, r, err
	}
	r.Offset = offset
	pop := generate(o.donors, offset, o.seed, now)
	r.Shape = pop.shape()
	tables, err := pop.tables()
	if err != nil {
		return nil, r, err
	}
	r.GenerateSeconds = time.Since(now).Seconds()
	copied := time.Now()
	if r.Rows, err = copyAll(ctx, pool, tables); err != nil {
		return nil, r, err
	}
	r.CopySeconds = time.Since(copied).Seconds()
	projected := time.Now()
	if err = refreshProjections(ctx, pool, o.workers); err != nil {
		return nil, r, err
	}
	if r.SuppressedMessages, err = suppressHistory(ctx, pool); err != nil {
		return nil, r, err
	}
	r.ProjectionSeconds = time.Since(projected).Seconds()
	return pop, r, nil
}

func run(ctx context.Context, args []string, out io.Writer) (err error) {
	o, err := parse(args)
	if err != nil {
		return err
	}
	var fixtureFile *os.File
	if o.fixture != "" {
		if fixtureFile, err = createFixture(o.fixture); err != nil {
			return err
		}
		defer func() {
			if closeErr := fixtureFile.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				_ = os.Remove(o.fixture)
			}
		}()
	}
	config, err := pgxpool.ParseConfig(o.databaseURL)
	if err != nil {
		return err
	}
	config.MaxConns = int32(o.workers + 2)
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("seed database unavailable")
	}
	defer pool.Close()
	started := time.Now()
	pop, r, err := seed(ctx, pool, o, started)
	if err != nil {
		return err
	}
	if fixtureFile != nil {
		if r.Sessions, err = pop.writeFixture(ctx, pool, o.sessions, fixtureFile); err != nil {
			return err
		}
	}
	r.TotalSeconds = time.Since(started).Seconds()
	return json.NewEncoder(out).Encode(r)
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
