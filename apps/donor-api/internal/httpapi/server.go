// Package httpapi owns the independent donor HTTP transport.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

type ContentSource interface {
	PublicContent(context.Context) (map[string]interface{}, error)
}
type DashboardSource interface {
	DonorDashboard(context.Context, string) (map[string]interface{}, error)
}
type WebhookReceiver interface {
	Receive(context.Context, []byte, string) error
}
type CheckoutSource interface {
	BeginCheckout(context.Context, payments.CheckoutRequest) (payments.CheckoutResult, error)
}
type Services struct {
	Content         ContentSource
	Dashboard       DashboardSource
	Webhook         WebhookReceiver
	Checkout        CheckoutSource
	Origin          string
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
}

// NewServer keeps the incomplete rewrite off the existing Python service port.
// Readiness must remain unavailable until the complete donor path is implemented.
func NewServer(address string, services Services) *http.Server {
	content, dashboard := services.Content, services.Dashboard
	origin := services.Origin
	if origin == "" {
		origin = "http://localhost:3000"
	}
	guard := &browserGuard{origin: origin}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, body interface{}) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	installAccountRoutes(mux, services, guard, reply)
	installDonorRoutes(mux, services, guard, reply)
	installRecurringRoutes(mux, services, guard, reply)
	installStaffRoutes(mux, services, guard, reply)
	installAdminRoutes(mux, services, guard, reply)
	installStaffFinanceRoutes(mux, services, guard, reply)
	installExportRoutes(mux, services, reply)
	installContentRoutes(mux, services, guard, reply)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 503, map[string]string{"detail": "Go donor rewrite is not ready for traffic"})
	})
	mux.HandleFunc("GET /donor/public/content", func(w http.ResponseWriter, r *http.Request) {
		if content == nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		body, err := content.PublicContent(ctx)
		if err != nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /donor/me/dashboard", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("dad_donor_session")
		if err != nil || cookie.Value == "" {
			reply(w, 401, map[string]string{"detail": "Sign in required"})
			return
		}
		if dashboard == nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		body, err := dashboard.DonorDashboard(ctx, cookie.Value)
		if errors.Is(err, store.ErrDenied) {
			reply(w, 401, map[string]string{"detail": "Sign in required"})
			return
		}
		if err != nil {
			reply(w, 503, map[string]string{"detail": "Donor database unavailable"})
			return
		}
		reply(w, 200, body)
	})
	mux.HandleFunc("POST /donor/stripe/webhook", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, payments.MaxWebhookBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				reply(w, 413, map[string]string{"detail": "Webhook payload is too large"})
			} else {
				reply(w, 400, map[string]string{"detail": "Invalid Stripe event"})
			}
			return
		}
		if services.Webhook == nil {
			reply(w, 503, map[string]string{"detail": "Webhook verification unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err = services.Webhook.Receive(ctx, body, r.Header.Get("Stripe-Signature"))
		switch {
		case errors.Is(err, payments.ErrInvalidEvent):
			reply(w, 400, map[string]string{"detail": "Invalid Stripe event"})
		case errors.Is(err, payments.ErrEventShape):
			reply(w, 400, map[string]string{"detail": "Invalid Stripe event shape"})
		case err != nil:
			reply(w, 503, map[string]string{"detail": "Webhook verification unavailable"})
		default:
			reply(w, 200, map[string]bool{"received": true})
		}
	})
	mux.HandleFunc("POST /donor/checkouts", func(w http.ResponseWriter, r *http.Request) {
		if status := guard.allow(r, time.Now()); status != 0 {
			detail := "Request origin is not allowed"
			if status == 429 {
				detail = "Too many requests"
				w.Header().Set("Retry-After", "60")
			}
			reply(w, status, map[string]string{"detail": detail})
			return
		}
		var body struct {
			Email     *string         `json:"email"`
			Amount    *int64          `json:"amount_minor"`
			Frequency json.RawMessage `json:"frequency"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, payments.MaxWebhookBytes))
		decoder.DisallowUnknownFields()
		var extra interface{}
		invalid := decoder.Decode(&body) != nil
		if contentType := r.Header.Get("Content-Type"); contentType != "" {
			media, _, err := mime.ParseMediaType(contentType)
			if err != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
				invalid = true
			}
		}
		if !invalid {
			invalid = decoder.Decode(&extra) != io.EOF
		}
		frequency := "MONTHLY"
		if len(body.Frequency) > 0 {
			frequency = ""
			if err := json.Unmarshal(body.Frequency, &frequency); err != nil {
				invalid = true
			}
		}
		var requestID pgtype.UUID
		if err := requestID.Scan(r.Header.Get("Idempotency-Key")); err != nil {
			invalid = true
		}
		if invalid || body.Email == nil || body.Amount == nil || utf8.RuneCountInString(*body.Email) > 320 || *body.Amount < 100 || *body.Amount > 99999999 || (frequency != "MONTHLY" && frequency != "ONE_TIME") || !requestID.Valid || requestID.Bytes[6]>>4 != 4 || requestID.Bytes[8]&0xc0 != 0x80 {
			reply(w, 422, map[string]string{"detail": "Invalid request fields"})
			return
		}
		if services.Checkout == nil {
			reply(w, 503, map[string]string{"detail": "Checkout is temporarily unavailable; retry the same request"})
			return
		}
		var visitor *string
		if cookie, err := r.Cookie("dad_referral"); err == nil {
			visitor = &cookie.Value
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		result, err := services.Checkout.BeginCheckout(ctx, payments.CheckoutRequest{RequestID: requestID, Email: *body.Email, Amount: *body.Amount, Frequency: frequency, Visitor: visitor})
		switch {
		case errors.Is(err, payments.ErrCheckoutConflict):
			reply(w, 409, map[string]string{"detail": "Checkout conflicts with an existing request"})
		case errors.Is(err, store.ErrDenied):
			reply(w, 403, map[string]string{"detail": "Giving is unavailable for this account"})
		case errors.Is(err, payments.ErrCheckoutInput):
			reply(w, 422, map[string]string{"detail": "Invalid request fields"})
		case err != nil:
			reply(w, 503, map[string]string{"detail": "Checkout is temporarily unavailable; retry the same request"})
		default:
			reply(w, 201, result)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { reply(w, 404, map[string]string{"detail": "Not Found"}) })
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
}
