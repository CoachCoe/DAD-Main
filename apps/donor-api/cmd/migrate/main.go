package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/store"
)

func run(args []string, getenv func(string) string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	adopt := flags.Bool("adopt-existing", false, "Validate and adopt the existing R1 donor schema without changing its data")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid migrate arguments")
	}
	dsn := getenv("DONOR_GO_DATABASE_URL")
	if dsn == "" {
		return errors.New("DONOR_GO_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := store.Open(ctx, dsn)
	if err != nil {
		return errors.New("migration database unavailable")
	}
	defer pool.Close()
	return migrations.Apply(ctx, pool, *adopt)
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(os.Args[1:], os.Getenv); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("database migrations complete")
}
