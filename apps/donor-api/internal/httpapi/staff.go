package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store"
)

type StaffSource interface {
	SignIn(context.Context, string, string, string) (string, error)
	SignOut(context.Context, string) error
	Me(context.Context, string) (store.StaffView, error)
	Provision(context.Context, string) (map[string]string, error)
	Activate(context.Context, string, string, string) error
	Accounts(context.Context, string) ([]store.StaffView, error)
	Invite(context.Context, string, string, string, string) (string, error)
	ChangeAccess(context.Context, string, pgtype.UUID, string, string) (map[string]interface{}, error)
}

func staffToken(r *http.Request) string {
	cookie, err := r.Cookie("dad_staff_session")
	if err != nil {
		return ""
	}
	return cookie.Value
}

func staffReply(w http.ResponseWriter, reply replyFunc, body interface{}, err error) {
	status, detail := 503, "Staff service is temporarily unavailable"
	switch {
	case err == nil:
		reply(w, 200, body)
		return
	case errors.Is(err, store.ErrStaffDenied):
		status, detail = 401, "Credentials invalid, expired, or temporarily locked"
	case errors.Is(err, store.ErrStaffForbidden):
		status, detail = 403, "Permission denied"
	case errors.Is(err, store.ErrStaffInput):
		status, detail = 422, "Invalid request fields"
	case errors.Is(err, store.ErrStaffConflict):
		status, detail = 409, "Staff access changed or conflicts with this request"
	case errors.Is(err, pgx.ErrNoRows):
		status, detail = 404, "Record not found"
	}
	reply(w, status, map[string]string{"detail": detail})
}

func validOTP(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func staffPost[T any](mux *http.ServeMux, path string, guard *browserGuard, reply replyFunc, ready func(http.ResponseWriter) bool, valid func(T) bool, run func(context.Context, http.ResponseWriter, *http.Request, T) (interface{}, error)) {
	mux.HandleFunc("POST /donor/staff/"+path, func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) {
			return
		}
		var body T
		if !decodeBody(w, r, &body) || !valid(body) {
			staffReply(w, reply, nil, store.ErrStaffInput)
			return
		}
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		result, err := run(ctx, w, r, body)
		staffReply(w, reply, result, err)
	})
}

func installStaffRoutes(mux *http.ServeMux, services Services, guard *browserGuard, reply replyFunc) {
	source := services.Staff
	ready := func(w http.ResponseWriter) bool {
		if source != nil {
			return true
		}
		staffReply(w, reply, nil, errors.New("unconfigured"))
		return false
	}
	setCookie := func(w http.ResponseWriter, token string, age int) {
		http.SetCookie(w, &http.Cookie{Name: "dad_staff_session", Value: token, MaxAge: age, HttpOnly: true, Secure: strings.HasPrefix(guard.origin, "https:"), SameSite: http.SameSiteStrictMode, Path: "/"})
	}
	type login struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	staffPost(mux, "sign-in", guard, reply, ready, func(b login) bool {
		return utf8.RuneCountInString(b.Email) <= 320 && len(b.Password) > 0 && utf8.RuneCountInString(b.Password) <= 128 && validOTP(b.Code)
	}, func(ctx context.Context, w http.ResponseWriter, r *http.Request, b login) (interface{}, error) {
		token, err := source.SignIn(ctx, b.Email, b.Password, b.Code)
		if err == nil {
			setCookie(w, token, 8*3600)
		}
		return map[string]bool{"signed_in": true}, err
	})
	type provision struct {
		Token string `json:"token"`
	}
	validToken := func(t string) bool { return len(t) >= 32 && len(t) <= 128 }
	staffPost(mux, "provision", guard, reply, ready, func(b provision) bool { return validToken(b.Token) }, func(ctx context.Context, w http.ResponseWriter, r *http.Request, b provision) (interface{}, error) {
		return source.Provision(ctx, b.Token)
	})
	type activation struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	staffPost(mux, "activate", guard, reply, ready, func(b activation) bool {
		return validToken(b.Token) && utf8.RuneCountInString(b.Password) >= 12 && utf8.RuneCountInString(b.Password) <= 128 && validOTP(b.Code)
	}, func(ctx context.Context, w http.ResponseWriter, r *http.Request, b activation) (interface{}, error) {
		return map[string]bool{"activated": true}, source.Activate(ctx, b.Token, b.Password, b.Code)
	})
	for _, path := range []string{"me", "accounts"} {
		mux.HandleFunc("GET /donor/staff/"+path, func(w http.ResponseWriter, r *http.Request) {
			if !ready(w) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var body interface{}
			var err error
			if path == "me" {
				body, err = source.Me(ctx, staffToken(r))
			} else {
				body, err = source.Accounts(ctx, staffToken(r))
			}
			staffReply(w, reply, body, err)
		})
	}
	mux.HandleFunc("POST /donor/staff/sign-out", func(w http.ResponseWriter, r *http.Request) {
		if !guard.check(w, r, reply) || !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := source.SignOut(ctx, staffToken(r))
		if err == nil {
			setCookie(w, "", -1)
		}
		staffReply(w, reply, map[string]bool{"signed_out": true}, err)
	})
	type invitation struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	validRole := func(role string) bool { return role == "ADMIN" || role == "FINANCE" || role == "SUPPORT" }
	staffPost(mux, "accounts", guard, reply, ready, func(b invitation) bool {
		return utf8.RuneCountInString(b.Email) <= 320 && len(b.Name) > 0 && utf8.RuneCountInString(b.Name) <= 100 && validRole(b.Role)
	}, func(ctx context.Context, w http.ResponseWriter, r *http.Request, b invitation) (interface{}, error) {
		token, err := source.Invite(ctx, staffToken(r), b.Email, b.Name, b.Role)
		if err != nil {
			return nil, err
		}
		return map[string]string{"invitation_url": guard.origin + "/staff/activate#token=" + token}, nil
	})
	for _, action := range []string{"disable", "recovery", "role"} {
		mux.HandleFunc("POST /donor/staff/accounts/{staff_id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			if !guard.check(w, r, reply) {
				return
			}
			var id pgtype.UUID
			if id.Scan(r.PathValue("staff_id")) != nil || !id.Valid {
				staffReply(w, reply, nil, store.ErrStaffInput)
				return
			}
			var body struct {
				Role string `json:"role"`
			}
			if action == "role" && (!decodeBody(w, r, &body) || !validRole(body.Role)) {
				staffReply(w, reply, nil, store.ErrStaffInput)
				return
			}
			if !ready(w) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			result, err := source.ChangeAccess(ctx, staffToken(r), id, action, body.Role)
			var response interface{} = result
			if err == nil && action == "recovery" {
				response = map[string]string{"invitation_url": guard.origin + "/staff/activate#token=" + result["token"].(string)}
			}
			if err == nil && action == "role" {
				response = result["staff"]
			}
			staffReply(w, reply, response, err)
		})
	}
}
