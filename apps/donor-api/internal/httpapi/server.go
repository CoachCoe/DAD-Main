// Package httpapi owns the donor HTTP transport.
package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
	"impactgraph/donor-api/internal/wallet"
)

type ContentSource interface {
	PublicContent(context.Context) (store.PublicContent, error)
}
type WebhookReceiver interface {
	Receive(context.Context, []byte, string) error
}

// Services are the optional dependencies behind each route group; a nil
// service makes its routes answer 503 rather than fail at startup.
type Services struct {
	Wallet          *wallet.Issuer
	Ready           func(context.Context) error
	Content         ContentSource
	Dashboard       DashboardSource
	Webhook         WebhookReceiver
	Checkout        CheckoutSource
	Origin          string
	TrustedProxies  []netip.Prefix
	Account         AccountSource
	Donor           DonorSource
	Recurring       RecurringSource
	Staff           StaffSource
	Admin           AdminSource
	StaffFinance    StaffFinanceSource
	Exports         ExportSource
	ContentAdmin    ContentAdminSource
	Partner         PartnerSource
	EmailConfigured bool
	EmailChange     EmailChangeSource
}

type api struct {
	Services
	guard  *browserGuard
	secure bool
}

// WriteTimeout exceeds providerTimeout so a slow processor call can still answer.
const writeTimeout = 30 * time.Second

func NewServer(address string, services Services) *http.Server {
	if services.Origin == "" {
		services.Origin = "http://localhost:3000"
	}
	a := &api{
		Services: services,
		guard:    newBrowserGuard(services.Origin, services.TrustedProxies),
		secure:   strings.HasPrefix(services.Origin, "https:"),
	}
	mux := http.NewServeMux()
	a.routes(mux)
	return &http.Server{
		Addr:              address,
		Handler:           secureHeaders(jsonFallback(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
}

func (a *api) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/live", a.live)
	mux.HandleFunc("GET /health/ready", a.ready)
	mux.HandleFunc("GET /donor/public/content", a.publicContent)
	mux.HandleFunc("POST /donor/stripe/webhook", a.stripeWebhook)
	mux.HandleFunc("POST /donor/checkouts", a.browser(visitorLimit, a.beginCheckout))
	mux.HandleFunc("GET /donor/referrals/{code}", a.referrer)
	mux.HandleFunc("POST /donor/referrals/{code}", a.browser(visitorLimit, a.captureReferral))
	mux.HandleFunc("POST /donor/public/partner-signup", a.browser(visitorLimit, a.partnerSignup))

	mux.HandleFunc("POST /donor/login", a.browser(authLimit, a.login))
	mux.HandleFunc("POST /donor/claim", a.browser(authLimit, a.claim))
	mux.HandleFunc("POST /donor/logout", a.browser(accountLimit, a.logout))
	mux.HandleFunc("POST /donor/logout-all", a.browser(accountLimit, a.signedIn(a.logoutAll)))
	mux.HandleFunc("POST /donor/me/email-change", a.browser(accountLimit, a.signedIn(a.requestEmailChange)))
	mux.HandleFunc("POST /donor/email-change/confirm", a.browser(authLimit, a.confirmEmailChange))

	mux.HandleFunc("GET /donor/me/dashboard", a.signedIn(a.dashboard))
	mux.HandleFunc("GET /donor/me/profile", a.signedIn(a.profile))
	mux.HandleFunc("POST /donor/me/preferences", a.browser(accountLimit, a.signedIn(a.savePreferences)))
	mux.HandleFunc("GET /donor/me/sharing", a.signedIn(a.sharing))
	mux.HandleFunc("POST /donor/me/sharing", a.browser(accountLimit, a.signedIn(a.saveSharing)))
	mux.HandleFunc("GET /donor/me/referral-code.svg", a.signedIn(a.referralCode))
	mux.HandleFunc("GET /donor/me/gifts", a.signedIn(a.gifts))
	mux.HandleFunc("GET /donor/me/receipts/{gift_id}", a.signedIn(a.receipt))
	mux.HandleFunc("GET /donor/me/annual-summary/{year}", a.signedIn(a.annualSummary))
	mux.HandleFunc("GET /donor/me/wallet", a.signedIn(a.walletProviders))
	mux.HandleFunc("POST /donor/me/wallet/apple", a.browser(accountLimit, a.signedIn(a.applePass)))
	mux.HandleFunc("POST /donor/me/wallet/google", a.browser(accountLimit, a.signedIn(a.googlePass)))

	mux.HandleFunc("GET /donor/me/recurring", a.signedIn(recurringRead(a, RecurringSource.Details)))
	mux.HandleFunc("GET /donor/me/recurring/amount", a.signedIn(recurringRead(a, RecurringSource.AmountOptions)))
	mux.HandleFunc("GET /donor/me/recurring/pause", a.signedIn(recurringRead(a, RecurringSource.PauseOptions)))
	mux.HandleFunc("GET /donor/me/recurring/resume", a.signedIn(recurringRead(a, RecurringSource.ResumeOptions)))
	mux.HandleFunc("GET /donor/me/payment-method", a.signedIn(recurringRead(a, RecurringSource.PaymentMethodStatus)))
	mux.HandleFunc("POST /donor/me/recurring/cancel", a.browser(accountLimit, a.signedIn(recurringCommand(a, cancelRecurring))))
	mux.HandleFunc("POST /donor/me/recurring/pause", a.browser(accountLimit, a.signedIn(recurringCommand(a, pauseRecurring))))
	mux.HandleFunc("POST /donor/me/recurring/resume", a.browser(accountLimit, a.signedIn(recurringCommand(a, resumeRecurring))))
	mux.HandleFunc("POST /donor/me/recurring/amount", a.browser(accountLimit, a.signedIn(recurringCommand(a, changeAmount))))
	mux.HandleFunc("POST /donor/me/payment-method/setup", a.browser(accountLimit, a.signedIn(a.beginPaymentUpdate)))

	mux.HandleFunc("POST /donor/staff/sign-in", a.browser(authLimit, a.staffSignIn))
	mux.HandleFunc("POST /donor/staff/provision", a.browser(authLimit, a.staffProvision))
	mux.HandleFunc("POST /donor/staff/activate", a.browser(authLimit, a.staffActivate))
	mux.HandleFunc("POST /donor/staff/sign-out", a.browser(staffLimit, a.staffSignOut))
	mux.HandleFunc("GET /donor/staff/me", a.staffMe)
	mux.HandleFunc("GET /donor/staff/accounts", a.staffAccounts)
	mux.HandleFunc("POST /donor/staff/accounts", a.browser(staffLimit, a.inviteStaff))
	mux.HandleFunc("POST /donor/staff/accounts/{staff_id}/disable", a.browser(staffLimit, a.changeStaffAccess("disable")))
	mux.HandleFunc("POST /donor/staff/accounts/{staff_id}/recovery", a.browser(staffLimit, a.changeStaffAccess("recovery")))
	mux.HandleFunc("POST /donor/staff/accounts/{staff_id}/role", a.browser(staffLimit, a.changeStaffAccess("role")))

	mux.HandleFunc("GET /donor/staff/donors", a.searchDonors)
	mux.HandleFunc("GET /donor/staff/donors/{donor_id}", a.donorDetail)
	mux.HandleFunc("GET /donor/staff/donors/{donor_id}/messages", a.donorMessages)
	mux.HandleFunc("GET /donor/staff/donors/{donor_id}/actions", a.donorActions)
	mux.HandleFunc("GET /donor/staff/audit", a.auditEvents)
	mux.HandleFunc("GET /donor/staff/enrolments", a.enrolments)
	mux.HandleFunc("POST /donor/staff/donors/{donor_id}/claim-email", a.browser(staffLimit, a.requestClaim))
	mux.HandleFunc("POST /donor/staff/messages/{message_id}/resend", a.browser(staffLimit, a.resendMessage))

	mux.HandleFunc("GET /donor/staff/donors/{donor_id}/gifts/{gift_id}/refund", a.refundQuote)
	mux.HandleFunc("POST /donor/staff/donors/{donor_id}/gifts/{gift_id}/refund", a.browser(staffLimit, a.refund))
	mux.HandleFunc("GET /donor/staff/donors/{donor_id}/cancellation", a.cancellationQuote)
	mux.HandleFunc("POST /donor/staff/donors/{donor_id}/cancellation", a.browser(staffLimit, a.cancel))

	mux.HandleFunc("GET /donor/staff/exports", a.exportDatasets)
	mux.HandleFunc("GET /donor/staff/exports/{file}", a.export)

	mux.HandleFunc("GET /donor/staff/content", a.contentInventory)
	mux.HandleFunc("GET /donor/staff/partners", a.partnerInquiries)
	mux.HandleFunc("POST /donor/staff/content/sources", a.browser(staffLimit, a.uploadSource))
	mux.HandleFunc("GET /donor/staff/content/sources/{source_id}", a.downloadSource)
	mux.HandleFunc("POST /donor/staff/content/publish", a.browser(staffLimit, a.publish))
}

// secureHeaders applies the API's response defaults; individual handlers may
// relax caching for public data.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// jsonFallback keeps ServeMux's 404 and 405 semantics, including Allow, while
// answering in the API's JSON error shape.
func jsonFallback(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		capture := &statusCapture{header: http.Header{}}
		mux.ServeHTTP(capture, r)
		if allow := capture.header.Get("Allow"); allow != "" {
			w.Header().Set("Allow", allow)
		}
		writeJSON(w, capture.status, problem{http.StatusText(capture.status)})
	})
}

type statusCapture struct {
	header http.Header
	status int
}

func (c *statusCapture) Header() http.Header         { return c.header }
func (c *statusCapture) Write(b []byte) (int, error) { return len(b), nil }
func (c *statusCapture) WriteHeader(status int)      { c.status = status }

func (a *api) live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (a *api) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r, 3*time.Second)
	defer cancel()
	if a.Ready == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Donor database unavailable or migrations pending", nil)
		return
	}
	if err := a.Ready(ctx); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "Donor database unavailable or migrations pending", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "payments": "test_mode_only"})
}

func (a *api) publicContent(w http.ResponseWriter, r *http.Request) {
	const unavailable = "Donor database unavailable"
	if a.Content == nil {
		writeError(w, r, http.StatusServiceUnavailable, unavailable, nil)
		return
	}
	ctx, cancel := withTimeout(r, 3*time.Second)
	defer cancel()
	body, err := a.Content.PublicContent(ctx)
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, unavailable, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, body)
}

func (a *api) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	const unavailable = "Webhook verification unavailable"
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, payments.MaxWebhookBytes))
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		writeError(w, r, http.StatusRequestEntityTooLarge, "Webhook payload is too large", nil)
		return
	}
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "Invalid Stripe event", nil)
		return
	}
	if a.Webhook == nil {
		writeError(w, r, http.StatusServiceUnavailable, unavailable, nil)
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	switch err := a.Webhook.Receive(ctx, body, r.Header.Get("Stripe-Signature")); {
	case errors.Is(err, payments.ErrInvalidEvent):
		writeError(w, r, http.StatusBadRequest, "Invalid Stripe event", nil)
	case errors.Is(err, payments.ErrEventShape):
		writeError(w, r, http.StatusBadRequest, "Invalid Stripe event shape", nil)
	case err != nil:
		writeError(w, r, http.StatusServiceUnavailable, unavailable, err)
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"received": true})
	}
}
