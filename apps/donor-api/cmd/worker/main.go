package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
	"impactgraph/donor-api/internal/store/queries"
	"impactgraph/donor-api/internal/workers"
)

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("donor-worker", flag.ContinueOnError)
	kind := flags.String("kind", "", "stripe or projections")
	once := flags.Bool("once", false, "process at most one item")
	retry := flags.String("retry-event", "", "reset an unprocessed Stripe event's attempt budget")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*kind != "stripe" && *kind != "projections") || (*retry != "" && (*kind != "stripe" || !strings.HasPrefix(*retry, "evt_"))) {
		return errors.New("invalid worker arguments")
	}
	// Cutover is explicit. Never start a second side-effecting runtime merely
	// because a deployment inherited the Python service's environment.
	if os.Getenv("DONOR_GO_WORKERS_ENABLED") != "true" {
		return errors.New("Go workers require explicit activation after Python worker handoff")
	}
	gateway, err := payments.NewStripeGateway(os.Getenv("STRIPE_SECRET_KEY"))
	if err != nil {
		return err
	}
	dsn := os.Getenv("DONOR_GO_DATABASE_URL")
	if dsn == "" {
		return errors.New("Go worker database is required")
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := store.Open(startup, dsn)
	cancel()
	if err != nil {
		return errors.New("donor worker database startup failed")
	}
	defer pool.Close()
	if *retry != "" {
		retryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		count, err := queries.New(pool).RetryStripeEvent(retryCtx, *retry)
		cancel()
		if err != nil || count != 1 {
			return errors.New("expected an unprocessed Stripe event")
		}
	}
	var step func(context.Context) (bool, error)
	if *kind == "stripe" {
		step = store.EventWorker{DB: pool, Gateway: gateway}.ProcessNext
	} else {
		step = func(ctx context.Context) (bool, error) {
			return store.RefreshNextProjection(ctx, pool, time.Now().UTC())
		}
	}
	return workers.Run(ctx, *once, 2*time.Second, step)
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		// Database errors can carry row values. Keep service logs free of donor data.
		slog.Error("donor worker stopped; inspect configuration and inbox state")
		os.Exit(1)
	}
}
