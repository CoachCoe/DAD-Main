package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/store"
)

func main() {
	adopt := flag.Bool("adopt-existing", false, "Validate and adopt the existing R1 donor schema without changing its data")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := store.Open(ctx, os.Getenv("DONOR_GO_DATABASE_URL"))
	if err != nil {
		slog.Error("migration database unavailable")
		os.Exit(1)
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool, *adopt); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
	slog.Info("database migrations complete")
}
