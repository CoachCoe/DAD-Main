package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/sharing"
	"impactgraph/donor-api/internal/store"
)

type DonorSource interface {
	Profile(context.Context, string) (store.Profile, error)
	SavePreferences(context.Context, string, store.Preferences) (store.Profile, error)
	SignOutEverywhere(context.Context, string) error
	Sharing(context.Context, string) (store.Sharing, error)
	SaveSharing(context.Context, string, string, bool) (store.Sharing, error)
	CaptureReferral(context.Context, string, string, string, time.Time) (store.ReferralCapture, error)
	Gifts(context.Context, string, int64) (store.GiftsPage, error)
	Receipt(context.Context, string, pgtype.UUID) (store.Receipt, error)
	AnnualSummary(context.Context, string, int) (store.AnnualSummary, error)
}

func donorReply(w http.ResponseWriter, reply replyFunc, body interface{}, err error) {
	status, detail := 200, ""
	switch {
	case errors.Is(err, store.ErrDenied):
		status, detail = 401, "Sign in required"
	case errors.Is(err, store.ErrPreferenceConflict):
		status, detail = 409, "Preferences changed. Refresh before saving again"
	case errors.Is(err, store.ErrPublicName):
		status, detail = 422, "Choose a public first name or nickname of 1 to 80 characters"
	case errors.Is(err, pgx.ErrNoRows):
		status, detail = 404, "Gift not found"
	case errors.Is(err, store.ErrReceipt):
		status, detail = 409, "Gift records are unavailable for this request"
	case errors.Is(err, payments.ErrFacts):
		status, detail = 422, "Invalid request fields"
	case err != nil:
		status, detail = 503, "Donor account is temporarily unavailable"
	}
	if err != nil {
		reply(w, status, map[string]string{"detail": detail})
	} else {
		reply(w, 200, body)
	}
}
func installDonorRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	source := services.Donor
	ready := func(w http.ResponseWriter) bool {
		if source == nil {
			reply(w, 503, map[string]string{"detail": "Donor account is temporarily unavailable"})
			return false
		}
		return true
	}
	invalid := func(w http.ResponseWriter) { reply(w, 422, map[string]string{"detail": "Invalid request fields"}) }
	mux.HandleFunc("GET /donor/me/profile", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		body, err := source.Profile(ctx, sessionToken(r))
		donorReply(w, reply, body, err)
	})
	mux.HandleFunc("POST /donor/me/preferences", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var body struct {
			Version   *int32 `json:"version"`
			Referral  *bool  `json:"referral_emails"`
			Marketing *bool  `json:"marketing_emails"`
		}
		if !decodeBody(w, r, &body) || body.Version == nil || *body.Version < 0 || body.Referral == nil || body.Marketing == nil {
			invalid(w)
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := source.SavePreferences(ctx, sessionToken(r), store.Preferences{Version: *body.Version, ReferralEmails: *body.Referral, MarketingEmails: *body.Marketing})
		donorReply(w, reply, result, err)
	})
	mux.HandleFunc("POST /donor/logout-all", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) || !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := source.SignOutEverywhere(ctx, sessionToken(r))
		if err == nil {
			http.SetCookie(w, &http.Cookie{Name: "dad_donor_session", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(guard.origin, "https:"), SameSite: http.SameSiteLaxMode, Path: "/"})
		}
		donorReply(w, reply, map[string]bool{"signed_out": true}, err)
	})
	mux.HandleFunc("GET /donor/me/sharing", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		body, err := source.Sharing(ctx, sessionToken(r))
		donorReply(w, reply, body, err)
	})
	mux.HandleFunc("POST /donor/me/sharing", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var body struct {
			Name    *string         `json:"public_name"`
			Replace json.RawMessage `json:"replace_code"`
		}
		replace := false
		if !decodeBody(w, r, &body) || body.Name == nil || utf8.RuneCountInString(*body.Name) < 1 || utf8.RuneCountInString(*body.Name) > 80 {
			invalid(w)
			return
		}
		if len(body.Replace) > 0 {
			if string(body.Replace) == "null" || json.Unmarshal(body.Replace, &replace) != nil {
				invalid(w)
				return
			}
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := source.SaveSharing(ctx, sessionToken(r), *body.Name, replace)
		donorReply(w, reply, result, err)
	})
	mux.HandleFunc("GET /donor/me/referral-code.svg", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		details, err := source.Sharing(ctx, sessionToken(r))
		if err != nil {
			donorReply(w, reply, nil, err)
			return
		}
		if details.IRLLink == nil {
			reply(w, 409, map[string]string{"detail": "Choose a public nickname before showing your referral code"})
			return
		}
		image, err := sharing.SVG(*details.IRLLink)
		if err != nil {
			donorReply(w, reply, nil, err)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		_, _ = w.Write(image)
	})
	mux.HandleFunc("POST /donor/referrals/{code}", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var body struct {
			Kind json.RawMessage `json:"kind"`
		}
		kind := "LINK"
		if !decodeBody(w, r, &body) {
			invalid(w)
			return
		}
		if len(body.Kind) > 0 {
			if string(body.Kind) == "null" || json.Unmarshal(body.Kind, &kind) != nil {
				invalid(w)
				return
			}
		}
		if kind != "LINK" && kind != "IRL" {
			invalid(w)
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		visitor := ""
		if cookie, err := r.Cookie("dad_referral"); err == nil {
			visitor = cookie.Value
		}
		result, err := source.CaptureReferral(ctx, r.PathValue("code"), kind, visitor, time.Now().UTC())
		if err != nil {
			donorReply(w, reply, nil, err)
			return
		}
		age := 30 * 86400
		if result.Token == "" {
			age = -1
		}
		http.SetCookie(w, &http.Cookie{Name: "dad_referral", Value: result.Token, MaxAge: age, HttpOnly: true, Secure: strings.HasPrefix(guard.origin, "https:"), SameSite: http.SameSiteLaxMode, Path: "/"})
		reply(w, 200, map[string]interface{}{"captured": result.Token != "", "public_name": result.PublicName})
	})
	mux.HandleFunc("GET /donor/me/gifts", func(w http.ResponseWriter, r *http.Request) {
		offset := int64(0)
		if value, exists := r.URL.Query()["offset"]; exists {
			var err error
			offset, err = strconv.ParseInt(value[len(value)-1], 10, 64)
			if err != nil || offset < 0 || offset > math.MaxInt64-50 {
				invalid(w)
				return
			}
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := source.Gifts(ctx, sessionToken(r), offset)
		donorReply(w, reply, result, err)
	})
	mux.HandleFunc("GET /donor/me/receipts/{gift_id}", func(w http.ResponseWriter, r *http.Request) {
		var id pgtype.UUID
		if err := id.Scan(r.PathValue("gift_id")); err != nil || !id.Valid {
			invalid(w)
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := source.Receipt(ctx, sessionToken(r), id)
		donorReply(w, reply, result, err)
	})
	mux.HandleFunc("GET /donor/me/annual-summary/{year}", func(w http.ResponseWriter, r *http.Request) {
		year, err := strconv.Atoi(r.PathValue("year"))
		if err != nil {
			invalid(w)
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		result, err := source.AnnualSummary(ctx, sessionToken(r), year)
		donorReply(w, reply, result, err)
	})
}
