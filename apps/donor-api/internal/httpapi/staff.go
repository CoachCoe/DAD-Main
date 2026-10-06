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
	case errors.Is(err, store.ErrStaffExists):
		return http.StatusConflict, "A staff account with this email already exists"
	case errors.Is(err, store.ErrStaffConflict):
		return http.StatusConflict, "Staff access changed or conflicts with this request"
	case errors.Is(err, pgx.ErrNoRows):
		return http.StatusNotFound, "Record not found"
	default:
		return http.StatusServiceUnavailable, staffUnavailable
	}
}

func (a *api) staff() service {
	return service{a.Staff != nil, staffUnavailable, readTimeout, staffStatus}
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

// staffCommand runs a validated staff request body with the staff time
// budget, which covers password hashing.
func staffCommand[T requestBody](a *api, w http.ResponseWriter, r *http.Request, run func(context.Context, T) (any, error)) {
	body, ok := decodeValid[T](w, r)
	if !ok {
		return
	}
	a.staff().within(staffTimeout).serve(w, r, func(ctx context.Context) (any, error) { return run(ctx, body) })
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

func (b provisionBody) valid() bool { return validToken(b.Token) }

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
	return validToken(b.Token) && validPassword(b.Password, 12) && validOTP(b.Code)
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

func (a *api) inviteStaff(w http.ResponseWriter, r *http.Request, session string) {
	staffCommand(a, w, r, func(ctx context.Context, b invitationBody) (any, error) {
		token, err := a.Staff.Invite(ctx, session, b.Email, b.Name, b.Role)
		if err != nil {
			return nil, err
		}
		return map[string]string{"invitation_url": a.invitationURL(token)}, nil
	})
}

func (a *api) staffMe(w http.ResponseWriter, r *http.Request, session string) {
	a.staff().serve(w, r, func(ctx context.Context) (any, error) { return a.Staff.Me(ctx, session) })
}

func (a *api) staffAccounts(w http.ResponseWriter, r *http.Request, session string) {
	a.staff().serve(w, r, func(ctx context.Context) (any, error) { return a.Staff.Accounts(ctx, session) })
}

// staffSignOut succeeds even without a valid session, so the browser can
// always discard its cookie.
func (a *api) staffSignOut(w http.ResponseWriter, r *http.Request) {
	a.staff().serve(w, r, func(ctx context.Context) (any, error) {
		err := a.Staff.SignOut(ctx, staffSession(r))
		if err != nil && !errors.Is(err, store.ErrStaffDenied) {
			return nil, err
		}
		a.setCookie(w, staffCookie, "", -1, http.SameSiteStrictMode)
		return map[string]bool{"signed_out": true}, nil
	})
}

type roleBody struct {
	Role string `json:"role"`
}

func (b roleBody) valid() bool { return validRole(b.Role) }

func (a *api) changeStaffAccess(action string) sessionHandler {
	return func(w http.ResponseWriter, r *http.Request, session string) {
		id, ok := pathID(w, r, "staff_id")
		if !ok {
			return
		}
		var body roleBody
		if action == "role" {
			if body, ok = decodeValid[roleBody](w, r); !ok {
				return
			}
		}
		a.staff().serve(w, r, func(ctx context.Context) (any, error) {
			change, err := a.Staff.ChangeAccess(ctx, session, id, action, body.Role)
			switch {
			case err != nil:
				return nil, err
			case action == "recovery":
				return map[string]string{"invitation_url": a.invitationURL(change.Token)}, nil
			case action == "role":
				return change.Staff, nil
			default:
				return change, nil
			}
		})
	}
}
