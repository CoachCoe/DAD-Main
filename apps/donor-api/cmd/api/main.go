package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"impactgraph/donor-api/internal/httpapi"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

func run() error {
	address := os.Getenv("DONOR_GO_LISTEN")
	if address == "" {
		address = "127.0.0.1:8002"
	}
	var services httpapi.Services
	services.EmailConfigured = os.Getenv("DONOR_SMTP_HOST") != "" && os.Getenv("DONOR_EMAIL_FROM") != "" && os.Getenv("DONOR_EMAIL_ENCRYPTION_KEY") != ""
	services.Origin = os.Getenv("DONOR_PUBLIC_ORIGIN")
	if services.Origin == "" {
		services.Origin = "http://localhost:3000"
	}
	if err := payments.ValidateOrigin(services.Origin); err != nil {
		return err
	}
	var gateway payments.CheckoutGateway
	if key := os.Getenv("STRIPE_SECRET_KEY"); key != "" {
		configured, err := payments.NewStripeGateway(key)
		if err != nil {
			return err
		}
		gateway = configured
	}
	if dsn := os.Getenv("DONOR_GO_DATABASE_URL"); dsn != "" {
		startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pool, err := store.Open(startup, dsn)
		cancel()
		if err != nil {
			return errors.New("donor database startup failed")
		}
		defer pool.Close()
		services.Content = store.ContentReader{DB: pool}
		services.Dashboard = store.DashboardReader{DB: pool}
		services.Webhook = payments.Receiver{Secret: os.Getenv("STRIPE_WEBHOOK_SECRET"), Inbox: store.StripeInbox{DB: pool}}
		services.Checkout = store.CheckoutService{DB: pool, Gateway: gateway, Origin: services.Origin}
		services.Account = store.AccountService{DB: pool}
	}
	server := httpapi.NewServer(address, services)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	select {
	case err := <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("donor service stopped", "error", err)
		os.Exit(1)
	}
}
