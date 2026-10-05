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

	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
	"impactgraph/donor-api/internal/store/queries"
	"impactgraph/donor-api/internal/workers"
)

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("donor-worker", flag.ContinueOnError)
	kind := flags.String("kind", "", "stripe, projections, login-email, email-changes, or messages")
	once := flags.Bool("once", false, "process at most one item")
	retry := flags.String("retry-event", "", "reset an unprocessed Stripe event's attempt budget")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*kind != "stripe" && *kind != "projections" && *kind != "login-email" && *kind != "email-changes" && *kind != "messages") || (*retry != "" && (*kind != "stripe" || !strings.HasPrefix(*retry, "evt_"))) {
		return errors.New("invalid worker arguments")
	}
	if os.Getenv("DONOR_GO_WORKERS_ENABLED") != "true" {
		return errors.New("workers require DONOR_GO_WORKERS_ENABLED=true")
	}
	dsn := os.Getenv("DONOR_GO_DATABASE_URL")
	if dsn == "" {
		return errors.New("worker database is required")
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := store.Open(startup, dsn)
	cancel()
	if err != nil {
		return errors.New("donor worker database startup failed")
	}
	defer pool.Close()
	check, finish := context.WithTimeout(ctx, 10*time.Second)
	err = migrations.Verify(check, pool)
	finish()
	if err != nil {
		return errors.New("donor schema is not current; run migrations before starting workers")
	}
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
		gateway, err := payments.NewStripeGateway(os.Getenv("STRIPE_SECRET_KEY"))
		if err != nil {
			return err
		}
		step = store.EventWorker{DB: pool, Gateway: gateway}.ProcessNext
	} else if *kind == "projections" {
		step = func(ctx context.Context) (bool, error) {
			return store.RefreshNextProjection(ctx, pool, time.Now().UTC())
		}
	} else {
		configured, err := mail.FromEnvironment(os.Getenv)
		if err != nil || configured == nil {
			return mail.ErrConfiguration
		}
		settings := *configured
		origin := os.Getenv("DONOR_PUBLIC_ORIGIN")
		if origin == "" {
			origin = "http://localhost:3000"
		}
		transport := mail.SMTPTransport{Settings: settings}
		if *kind == "email-changes" {
			worker := store.EmailChangeWorker{DB: pool, Settings: settings, Origin: origin, Transport: transport}
			step = func(ctx context.Context) (bool, error) { return worker.DeliverNext(ctx, time.Now().UTC()) }
		} else if *kind == "login-email" {
			worker := store.LoginEmailWorker{DB: pool, Settings: settings, Origin: origin, Transport: transport}
			step = func(ctx context.Context) (bool, error) { return worker.DeliverNext(ctx, time.Now().UTC()) }
		} else {
			worker := store.MessageWorker{DB: pool, Settings: settings, Origin: origin, Transport: transport}
			step = func(ctx context.Context) (bool, error) { return worker.DeliverNext(ctx, time.Now().UTC()) }
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
