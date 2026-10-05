package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/store"
)

const (
	readTimeout     = 5 * time.Second
	staffTimeout    = 15 * time.Second
	providerTimeout = 25 * time.Second
	maxJSONBody     = 256 << 10
	maxAmountMinor  = 99_999_999
)

const (
	donorCookie    = "dad_donor_session"
	staffCookie    = "dad_staff_session"
	referralCookie = "dad_referral"
)

// classifier maps a service error to a public status and message.
type classifier func(error) (int, string)

func writeError(w http.ResponseWriter, r *http.Request, status int, detail string, cause error) {
	if status >= http.StatusInternalServerError && cause != nil {
		slog.LogAttrs(r.Context(), slog.LevelError, "request failed",
			slog.String("route", r.Pattern), slog.Int("status", status), slog.String("cause", errorClass(cause)))
	}
	writeJSON(w, status, problem{detail})
}

func invalidFields(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusUnprocessableEntity, "Invalid request fields", nil)
}

func respond(w http.ResponseWriter, r *http.Request, body any, err error, classify classifier) {
	if err != nil {
		status, detail := classify(err)
		writeError(w, r, status, detail, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// errorClass names a failure without its message, which may carry donor data.
func errorClass(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	if pg, ok := errors.AsType[*pgconn.PgError](err); ok {
		return "postgres " + pg.Code
	}
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return fmt.Sprintf("%T", err)
}

func pathUUID(r *http.Request, name string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	return id, id.Scan(r.PathValue(name)) == nil && id.Valid
}

func idempotencyKey(r *http.Request) (pgtype.UUID, bool) {
	var key pgtype.UUID
	return key, key.Scan(r.Header.Get("Idempotency-Key")) == nil && store.ValidRequestID(key)
}

func offsetParam(r *http.Request, name string) (int64, bool) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil && n >= 0 && n <= math.MaxInt64-50
}

func cookieValue(r *http.Request, name string) string {
	if cookie, err := r.Cookie(name); err == nil {
		return cookie.Value
	}
	return ""
}

func (a *api) setCookie(w http.ResponseWriter, name, value string, maxAge int, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, MaxAge: maxAge, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: sameSite})
}

func withTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
