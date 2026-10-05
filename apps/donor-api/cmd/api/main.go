package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/httpapi"
	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
	"impactgraph/donor-api/internal/wallet"
)

// shutdownTimeout lets in-flight processor calls finish within the server's
// write timeout instead of being cut off and left for reconciliation.
const shutdownTimeout = 35 * time.Second

// trustedProxies parses DONOR_TRUSTED_PROXY_CIDRS. Ranges broader than a
// private /8 (IPv4) or /32 (IPv6) are refused because trusting them would let
// arbitrary clients choose their own rate-limit identity.
func trustedProxies(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for field := range strings.SplitSeq(value, ",") {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(field)
		if err != nil {
			return nil, errors.New("DONOR_TRUSTED_PROXY_CIDRS must contain valid CIDR ranges")
		}
		if (prefix.Addr().Is4() && prefix.Bits() < 8) || (prefix.Addr().Is6() && prefix.Bits() < 32) {
			return nil, fmt.Errorf("DONOR_TRUSTED_PROXY_CIDRS range %s is too broad; list the proxy's own address or network", prefix)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func run() error {
	address := os.Getenv("DONOR_GO_LISTEN")
	if address == "" {
		address = "127.0.0.1:8002"
	}
	var services httpapi.Services
	var err error
	if services.TrustedProxies, err = trustedProxies(os.Getenv("DONOR_TRUSTED_PROXY_CIDRS")); err != nil {
		return err
	}
	email, err := mail.FromEnvironment(os.Getenv)
	if err != nil {
		return err
	}
	services.EmailConfigured = email != nil
	services.Origin = os.Getenv("DONOR_PUBLIC_ORIGIN")
	if services.Origin == "" {
		services.Origin = "http://localhost:3000"
	}
	if err := payments.ValidateOrigin(services.Origin); err != nil {
		return err
	}
	if services.Wallet, err = wallet.FromEnvironment(os.Getenv, services.Origin); err != nil {
		return err
	}
	stripeKey, webhookSecret := os.Getenv("STRIPE_SECRET_KEY"), os.Getenv("STRIPE_WEBHOOK_SECRET")
	if (stripeKey == "") != (webhookSecret == "") {
		return errors.New("set both STRIPE_SECRET_KEY and STRIPE_WEBHOOK_SECRET, or neither")
	}
	var gateway payments.StaffFinanceGateway
	if stripeKey != "" {
		if gateway, err = payments.NewStripeGateway(stripeKey); err != nil {
			return err
		}
	}
	staffKey, contentKey := os.Getenv("DONOR_STAFF_ENCRYPTION_KEY"), os.Getenv("DONOR_CONTENT_ENCRYPTION_KEY")
	if staffKey != "" && credentials.ValidateKey(staffKey) != nil {
		return errors.New("DONOR_STAFF_ENCRYPTION_KEY must be 32 bytes of URL-safe base64")
	}
	if contentKey != "" && credentials.ValidateKey(contentKey) != nil {
		return errors.New("DONOR_CONTENT_ENCRYPTION_KEY must be 32 bytes of URL-safe base64")
	}

	dsn := os.Getenv("DONOR_GO_DATABASE_URL")
	if dsn == "" {
		slog.Warn("DONOR_GO_DATABASE_URL is unset; only health and static responses are available")
	} else {
		startup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pool, err := store.Open(startup, dsn)
		cancel()
		if err != nil {
			return errors.New("donor database startup failed")
		}
		defer pool.Close()
		services.Ready = func(ctx context.Context) error { return migrations.Verify(ctx, pool) }
		check, finish := context.WithTimeout(context.Background(), 10*time.Second)
		err = services.Ready(check)
		finish()
		if err != nil {
			return errors.New("donor schema is not current; run migrations before starting the API")
		}
		services.Content = store.ContentReader{DB: pool}
		services.Partner = store.PartnerService{DB: pool}
		services.Dashboard = store.DashboardReader{DB: pool}
		services.Account = store.AccountService{DB: pool}
		services.Donor = store.DonorService{DB: pool, Origin: services.Origin}
		services.Checkout = store.CheckoutService{DB: pool, Gateway: gateway, Origin: services.Origin}
		services.Recurring = store.RecurringService{DB: pool, Gateway: gateway, Origin: services.Origin}
		if webhookSecret != "" {
			services.Webhook = payments.Receiver{Secret: webhookSecret, Inbox: store.StripeInbox{DB: pool}}
		}
		if email != nil {
			services.EmailChange = store.EmailChangeService{DB: pool, EncryptionKey: email.EncryptionKey}
		}
		if staffKey != "" {
			staff, err := store.NewStaffService(pool, staffKey)
			if err != nil {
				return errors.New("staff configuration is invalid")
			}
			services.Staff, services.Admin, services.Exports = staff, staff, staff
			services.StaffFinance = store.StaffFinanceService{Staff: staff, Gateway: gateway, Origin: services.Origin}
			services.ContentAdmin = store.ContentService{Staff: staff, EncryptionKey: contentKey}
		}
	}
	for feature, enabled := range map[string]bool{
		"payments (STRIPE_SECRET_KEY)":                    gateway != nil,
		"account email (SMTP settings)":                   email != nil,
		"staff back office (DONOR_STAFF_ENCRYPTION_KEY)":  services.Staff != nil,
		"source documents (DONOR_CONTENT_ENCRYPTION_KEY)": contentKey != "",
	} {
		if !enabled {
			slog.Info("feature disabled until configured", "feature", feature)
		}
	}

	server := httpapi.NewServer(address, services)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	slog.Info("donor API listening", "address", address)
	select {
	case err := <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
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
