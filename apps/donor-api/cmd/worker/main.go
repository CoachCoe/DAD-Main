package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/mail"
	"dad/donor-api/internal/migrations"
	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/push"
	"dad/donor-api/internal/store"
	"dad/donor-api/internal/store/queries"
	"dad/donor-api/internal/workers"
)

// configError messages are written by this program and are safe to log.
type configError string

func (e configError) Error() string { return string(e) }

func run(ctx context.Context, args []string, getenv func(string) string) error {
	flags := flag.NewFlagSet("donor-worker", flag.ContinueOnError)
	kinds := strings.Join(slices.Sorted(maps.Keys(dbroles.Workers)), ", ")
	kind := flags.String("kind", "", kinds)
	once := flags.Bool("once", false, "process at most one item")
	retry := flags.String("retry-event", "", "reset an unprocessed Stripe event's attempt budget")
	if err := flags.Parse(args); err != nil {
		return configError("invalid worker arguments")
	}
	role, known := dbroles.Workers[*kind]
	if flags.NArg() != 0 || !known {
		return configError("--kind must be one of " + kinds)
	}
	if *retry != "" && (*kind != "stripe" || !strings.HasPrefix(*retry, "evt_")) {
		return configError("--retry-event requires --kind stripe and an evt_ identifier")
	}
	if getenv("DONOR_GO_WORKERS_ENABLED") != "true" {
		return configError("workers require DONOR_GO_WORKERS_ENABLED=true")
	}
	dsn := role.URL(getenv)
	if dsn == "" {
		return configError(role.Variable() + " is required")
	}
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := store.Open(startup, dsn, role.Pool)
	cancel()
	if err != nil {
		return configError("worker database is unreachable")
	}
	defer pool.Close()
	check, finish := context.WithTimeout(ctx, 10*time.Second)
	defer finish()
	if err = migrations.Verify(check, pool); err != nil {
		return configError("donor schema is not current; run migrations before starting workers")
	}
	if err = role.Check(check, pool, getenv(dbroles.AllowOwnerVariable) == "true"); err != nil {
		return configError(err.Error())
	}
	if *retry != "" {
		retryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		count, err := queries.New(pool).RetryStripeEvent(retryCtx, *retry)
		cancel()
		if err != nil || count != 1 {
			return configError("expected an unprocessed Stripe event")
		}
	}
	step, err := stepFor(*kind, pool, getenv)
	if err != nil {
		return err
	}
	budget := workers.StepBudget
	if *kind == "stripe" {
		budget = workers.ProcessorStepBudget
	}
	return workers.Run(ctx, *once, 2*time.Second, budget, step)
}

type deliverer interface {
	DeliverNext(context.Context, time.Time) (bool, error)
}

func stepFor(kind string, pool *pgxpool.Pool, getenv func(string) string) (func(context.Context) (bool, error), error) {
	switch kind {
	case "stripe":
		gateway, err := payments.NewStripeGateway(getenv("STRIPE_SECRET_KEY"))
		if err != nil {
			return nil, configError("STRIPE_SECRET_KEY must be a Stripe test-mode secret key")
		}
		events := store.EventWorker{DB: pool, Gateway: gateway}
		backfill := &store.AnchorBackfill{DB: pool, Gateway: gateway}
		return func(ctx context.Context) (bool, error) {
			worked, err := events.ProcessNext(ctx)
			if worked || err != nil {
				return worked, err
			}
			return backfill.SyncNext(ctx, time.Now().UTC())
		}, nil
	case "projections":
		retention := &store.TouchRetention{DB: pool}
		return func(ctx context.Context) (bool, error) {
			now := time.Now().UTC()
			purged, err := retention.PurgeNext(ctx, now)
			if err != nil {
				return false, err
			}
			processed, err := store.RefreshDueProjections(ctx, pool, now, store.ProjectionBatch)
			return purged || processed > 0, err
		}, nil
	case "push":
		config, err := push.FromEnvironment(getenv)
		if err != nil {
			return nil, configError(err.Error())
		}
		// Without a provider the worker still runs, so a deployment can start
		// every worker: it completes notifications for unconfigured platforms
		// as suppressed instead of letting them wait.
		providers := config.Providers()
		for platform, feature := range map[string]string{push.IOS: "iOS app notifications (DONOR_PUSH_APNS_*)", push.Android: "Android app notifications (DONOR_PUSH_FCM_KEY_FILE)"} {
			if providers[platform] == nil {
				slog.Info("feature disabled until configured", "feature", feature)
			}
		}
		worker := store.PushWorker{DB: pool, Providers: providers}
		return func(ctx context.Context) (bool, error) { return worker.DeliverNext(ctx, time.Now().UTC()) }, nil
	}
	settings, err := mail.FromEnvironment(getenv)
	if err != nil || settings == nil {
		return nil, configError("SMTP settings are missing or invalid")
	}
	origin := getenv("DONOR_PUBLIC_ORIGIN")
	if err := payments.ValidateOrigin(origin); err != nil {
		return nil, configError("DONOR_PUBLIC_ORIGIN, the origin of email links, " + err.Error())
	}
	roots, err := settings.RootCAs()
	if err != nil {
		return nil, configError("DONOR_SMTP_CA could not be loaded")
	}
	transport := mail.SMTPTransport{Settings: *settings, RootCAs: roots}
	var worker deliverer
	switch kind {
	case "email-changes":
		worker = store.EmailChangeWorker{DB: pool, Settings: *settings, Origin: origin, Transport: transport}
	case "login-email":
		worker = store.LoginEmailWorker{DB: pool, Settings: *settings, Origin: origin, Transport: transport}
	default:
		worker = store.MessageWorker{DB: pool, Settings: *settings, Origin: origin, Transport: transport}
	}
	return func(ctx context.Context) (bool, error) { return worker.DeliverNext(ctx, time.Now().UTC()) }, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := run(ctx, os.Args[1:], os.Getenv)
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	if reason, ok := errors.AsType[configError](err); ok {
		slog.Error("donor worker stopped", "reason", string(reason))
	} else {
		// Database errors can carry row values. Keep service logs free of donor data.
		slog.Error("donor worker stopped; inspect configuration and inbox state")
	}
	os.Exit(1)
}
