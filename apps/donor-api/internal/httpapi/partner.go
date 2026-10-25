package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"dad/donor-api/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

// PartnerApplySource is the public application entry point (P-1).
type PartnerApplySource interface {
	Apply(context.Context, pgtype.UUID, store.PartnerApplicationInput) error
}

// PartnerPortalSource is the authenticated partner portal surface (§7.2/§7.3).
type PartnerPortalSource interface {
	AcceptInvite(ctx context.Context, invite string) (string, error)
	SignOut(ctx context.Context, token string) error
	Users(ctx context.Context, token string) ([]store.PartnerUser, error)
	Invite(ctx context.Context, token, email, name, role string) (string, error)
	RemoveUser(ctx context.Context, token string, userID pgtype.UUID) error
	Overview(ctx context.Context, token string) (store.PartnerOverview, error)
	Initiatives(ctx context.Context, token string) ([]store.PartnerInitiative, error)
	Spend(ctx context.Context, token string) ([]store.PartnerSpendRow, error)
	Exports(ctx context.Context, token string) ([]store.PartnerReportRow, error)
	ExportReport(ctx context.Context, token string) ([]byte, error)
}

// StaffPartnerSource is the staff-side partner administration (§8, partner.manage).
type StaffPartnerSource interface {
	PendingApplications(ctx context.Context, token string, offset int64) ([]store.PartnerApplication, error)
	Approve(ctx context.Context, token string, appID pgtype.UUID) (pgtype.UUID, error)
	Reject(ctx context.Context, token string, appID pgtype.UUID, reason string) error
	LinkInitiative(ctx context.Context, token string, partnerID, initiativeID pgtype.UUID) error
	SetMatch(ctx context.Context, token string, partnerID pgtype.UUID, workforce, rateBP int32, capMinor int64) error
}

// partnerSessionMaxAge mirrors the 12h store session lifetime.
const partnerSessionMaxAge = 12 * time.Hour

// partnerSession reads the partner session: the first-party cookie the web
// portal carries, else the bearer token an API client sends. The portal is a browser app
// that stores the session token from AcceptInvite and sends it as a bearer —
// it is never a donor or staff cookie (P-5: a partner login is not a donor).
func partnerSession(r *http.Request) string {
	if cookie := cookieValue(r, partnerCookie); validToken(cookie) {
		return cookie
	}
	scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") || !validToken(token) {
		return ""
	}
	return token
}

func partnerStatusCode(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrPartnerDenied):
		return http.StatusUnauthorized, "Partner sign-in required"
	case errors.Is(err, store.ErrPartnerForbidden):
		return http.StatusForbidden, "You do not have permission for this action"
	case errors.Is(err, store.ErrPartnerInvite):
		return http.StatusUnprocessableEntity, "This invite link is invalid or has expired"
	case errors.Is(err, store.ErrPartnerApplication):
		return http.StatusUnprocessableEntity, "Check the highlighted fields and try again"
	case errors.Is(err, store.ErrPartnerDecision):
		return http.StatusConflict, "This application has already been decided"
	default:
		return http.StatusServiceUnavailable, "The partner portal is temporarily unavailable"
	}
}

func (a *api) partnerPortal() service {
	return service{a.PartnerPortal != nil, "The partner portal is temporarily unavailable", readTimeout, partnerStatusCode}
}

func (a *api) staffPartner() service {
	return service{a.StaffPartner != nil, "Partner administration is temporarily unavailable", staffTimeout, staffStatus}
}

// --- public application (P-1) ---

func (a *api) partnerApply(w http.ResponseWriter, r *http.Request) {
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body store.PartnerApplicationInput
	if !decodeJSON(w, r, &body) {
		return
	}
	svc := service{a.PartnerApply != nil, "Partner applications are temporarily unavailable", readTimeout, partnerStatusCode}
	svc.serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]any{"received": true}, a.PartnerApply.Apply(ctx, key, body)
	})
}

// --- partner portal (bearer session) ---

func (a *api) partnerAccept(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Invite string `json:"invite"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		token, err := a.PartnerPortal.AcceptInvite(ctx, body.Invite)
		if err != nil {
			return nil, err
		}
		a.setCookie(w, partnerCookie, token, int(partnerSessionMaxAge.Seconds()), http.SameSiteLaxMode)
		return map[string]any{"session": token}, nil
	})
}

func (a *api) partnerSignOut(w http.ResponseWriter, r *http.Request, session string) {
	a.setCookie(w, partnerCookie, "", -1, http.SameSiteLaxMode)
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]any{"signed_out": true}, a.PartnerPortal.SignOut(ctx, session)
	})
}

func (a *api) partnerOverview(w http.ResponseWriter, r *http.Request, session string) {
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) { return a.PartnerPortal.Overview(ctx, session) })
}

func (a *api) partnerInitiatives(w http.ResponseWriter, r *http.Request, session string) {
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		rows, err := a.PartnerPortal.Initiatives(ctx, session)
		return map[string]any{"initiatives": rows}, err
	})
}

func (a *api) partnerSpend(w http.ResponseWriter, r *http.Request, session string) {
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		rows, err := a.PartnerPortal.Spend(ctx, session)
		return map[string]any{"categories": rows}, err
	})
}

func (a *api) partnerUsers(w http.ResponseWriter, r *http.Request, session string) {
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		rows, err := a.PartnerPortal.Users(ctx, session)
		return map[string]any{"users": rows}, err
	})
}

func (a *api) partnerInviteUser(w http.ResponseWriter, r *http.Request, session string) {
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		invite, err := a.PartnerPortal.Invite(ctx, session, body.Email, body.Name, body.Role)
		if err != nil {
			return nil, err
		}
		return map[string]any{"invite": invite}, nil
	})
}

func (a *api) partnerRemoveUser(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]any{"removed": true}, a.PartnerPortal.RemoveUser(ctx, session, id)
	})
}

func (a *api) partnerExports(w http.ResponseWriter, r *http.Request, session string) {
	a.partnerPortal().serve(w, r, func(ctx context.Context) (any, error) {
		rows, err := a.PartnerPortal.Exports(ctx, session)
		return map[string]any{"exports": rows}, err
	})
}

// partnerReport streams the branded PDF (P-14). Not JSON — a file download.
func (a *api) partnerReport(w http.ResponseWriter, r *http.Request, session string) {
	if !a.partnerPortal().available(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, staffTimeout)
	defer cancel()
	pdf, err := a.PartnerPortal.ExportReport(ctx, session)
	if err != nil {
		respond(w, r, nil, err, partnerStatusCode)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="dad-impact-report.pdf"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pdf)
}

// --- staff partner administration (partner.manage) ---

func (a *api) staffPartnerPending(w http.ResponseWriter, r *http.Request, session string) {
	a.staffPartner().serve(w, r, func(ctx context.Context) (any, error) {
		rows, err := a.StaffPartner.PendingApplications(ctx, session, 0)
		return map[string]any{"applications": rows}, err
	})
}

func (a *api) staffPartnerApprove(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "appID")
	if !ok {
		return
	}
	a.staffPartner().serve(w, r, func(ctx context.Context) (any, error) {
		partnerID, err := a.StaffPartner.Approve(ctx, session, id)
		if err != nil {
			return nil, err
		}
		return map[string]any{"partner_id": partnerID.String()}, nil
	})
}

func (a *api) staffPartnerReject(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "appID")
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.staffPartner().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]any{"rejected": true}, a.StaffPartner.Reject(ctx, session, id, body.Reason)
	})
}

func (a *api) staffPartnerLink(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "partnerID")
	if !ok {
		return
	}
	var body struct {
		InitiativeID string `json:"initiative_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var initiativeID pgtype.UUID
	if initiativeID.Scan(body.InitiativeID) != nil || !initiativeID.Valid {
		invalidFields(w, r)
		return
	}
	a.staffPartner().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]any{"linked": true}, a.StaffPartner.LinkInitiative(ctx, session, id, initiativeID)
	})
}

func (a *api) staffPartnerMatch(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "partnerID")
	if !ok {
		return
	}
	var body struct {
		RateBP    int32 `json:"match_rate_bp"`
		CapMinor  int64 `json:"match_cap_minor"`
		Workforce int32 `json:"workforce_size"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	a.staffPartner().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]any{"saved": true}, a.StaffPartner.SetMatch(ctx, session, id, body.Workforce, body.RateBP, body.CapMinor)
	})
}
