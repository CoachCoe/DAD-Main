package httpapi

import (
	"context"
	"errors"
	"net/http"
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
	ChangeAccess(context.Context, string, pgtype.UUID, string, string) (store.AccessChange, error)
}

const (
	staffSessionAge  = 8 * 60 * 60
	staffUnavailable = "Staff service is temporarily unavailable"
)

func staffStatus(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrStaffDenied):
		return http.StatusUnauthorized, "Credentials invalid, expired, or temporarily locked"
	case errors.Is(err, store.ErrStaffForbidden):
		return http.StatusForbidden, "Permission denied"
	case errors.Is(err, store.ErrStaffInput):
		return http.StatusUnprocessableEntity, "Invalid request fields"
	case errors.Is(err, store.ErrStaffConflict):
		return http.StatusConflict, "Staff access changed or conflicts with this request"
	case errors.Is(err, pgx.ErrNoRows):
		return http.StatusNotFound, "Record not found"
	default:
		return http.StatusServiceUnavailable, staffUnavailable
	}
}

func staffInvalid(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusUnprocessableEntity, "Invalid request fields", nil)
}

func (a *api) staffAvailable(w http.ResponseWriter, r *http.Request) bool {
	if a.Staff == nil {
		writeError(w, r, http.StatusServiceUnavailable, staffUnavailable, nil)
		return false
	}
	return true
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

func validPassword(password string, minimum int) bool {
	n := utf8.RuneCountInString(password)
	return n >= minimum && n <= 128
}

func validRole(role string) bool { return role == "ADMIN" || role == "FINANCE" || role == "SUPPORT" }

func (a *api) invitationURL(token string) string {
	return a.Origin + "/staff/activate#token=" + token
}

// staffCommand decodes and validates a staff request body, then runs it
// with the staff time budget.
func staffCommand[T requestBody](a *api, w http.ResponseWriter, r *http.Request, run func(context.Context, T) (any, error)) {
	if !a.staffAvailable(w, r) {
		return
	}
	var body T
	if !decodeJSON(w, r, &body) || !body.valid() {
		staffInvalid(w, r)
		return
	}
	ctx, cancel := withTimeout(r, staffTimeout)
	defer cancel()
	result, err := run(ctx, body)
	respond(w, r, result, err, staffStatus)
}

type signInBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (b signInBody) valid() bool {
	return utf8.RuneCountInString(b.Email) <= maxEmailRunes && validPassword(b.Password, 1) && validOTP(b.Code)
}

func (a *api) staffSignIn(w http.ResponseWriter, r *http.Request) {
	staffCommand(a, w, r, func(ctx context.Context, b signInBody) (any, error) {
		token, err := a.Staff.SignIn(ctx, b.Email, b.Password, b.Code)
		if err == nil {
			a.setCookie(w, staffCookie, token, staffSessionAge, http.SameSiteStrictMode)
		}
		return map[string]bool{"signed_in": true}, err
	})
}

type provisionBody struct {
	Token string `json:"token"`
}

func (b provisionBody) valid() bool { return validProof(b.Token) }

func (a *api) staffProvision(w http.ResponseWriter, r *http.Request) {
	staffCommand(a, w, r, func(ctx context.Context, b provisionBody) (any, error) {
		return a.Staff.Provision(ctx, b.Token)
	})
}

type activationBody struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (b activationBody) valid() bool {
	return validProof(b.Token) && validPassword(b.Password, 12) && validOTP(b.Code)
}

func (a *api) staffActivate(w http.ResponseWriter, r *http.Request) {
	staffCommand(a, w, r, func(ctx context.Context, b activationBody) (any, error) {
		return map[string]bool{"activated": true}, a.Staff.Activate(ctx, b.Token, b.Password, b.Code)
	})
}

type invitationBody struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

func (b invitationBody) valid() bool {
	n := utf8.RuneCountInString(b.Name)
	return utf8.RuneCountInString(b.Email) <= maxEmailRunes && n > 0 && n <= 100 && validRole(b.Role)
}

func (a *api) inviteStaff(w http.ResponseWriter, r *http.Request) {
	staffCommand(a, w, r, func(ctx context.Context, b invitationBody) (any, error) {
		token, err := a.Staff.Invite(ctx, cookieValue(r, staffCookie), b.Email, b.Name, b.Role)
		if err != nil {
			return nil, err
		}
		return map[string]string{"invitation_url": a.invitationURL(token)}, nil
	})
}

func (a *api) staffMe(w http.ResponseWriter, r *http.Request) {
	if !a.staffAvailable(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	body, err := a.Staff.Me(ctx, cookieValue(r, staffCookie))
	respond(w, r, body, err, staffStatus)
}

func (a *api) staffAccounts(w http.ResponseWriter, r *http.Request) {
	if !a.staffAvailable(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	body, err := a.Staff.Accounts(ctx, cookieValue(r, staffCookie))
	respond(w, r, body, err, staffStatus)
}

func (a *api) staffSignOut(w http.ResponseWriter, r *http.Request) {
	if !a.staffAvailable(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	err := a.Staff.SignOut(ctx, cookieValue(r, staffCookie))
	if err == nil || errors.Is(err, store.ErrStaffDenied) {
		a.setCookie(w, staffCookie, "", -1, http.SameSiteStrictMode)
		err = nil
	}
	respond(w, r, map[string]bool{"signed_out": true}, err, staffStatus)
}

func (a *api) changeStaffAccess(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.staffAvailable(w, r) {
			return
		}
		id, ok := pathUUID(r, "staff_id")
		if !ok {
			staffInvalid(w, r)
			return
		}
		var body struct {
			Role string `json:"role"`
		}
		if action == "role" && (!decodeJSON(w, r, &body) || !validRole(body.Role)) {
			staffInvalid(w, r)
			return
		}
		ctx, cancel := withTimeout(r, readTimeout)
		defer cancel()
		change, err := a.Staff.ChangeAccess(ctx, cookieValue(r, staffCookie), id, action, body.Role)
		if err != nil {
			respond(w, r, nil, err, staffStatus)
			return
		}
		switch action {
		case "recovery":
			writeJSON(w, http.StatusOK, map[string]string{"invitation_url": a.invitationURL(change.Token)})
		case "role":
			writeJSON(w, http.StatusOK, change.Staff)
		default:
			writeJSON(w, http.StatusOK, change)
		}
	}
}
