package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"dad/donor-api/internal/store"
)

// EventSource is the staff (Impact-Ops) surface for events (Phase 4.2, E-3).
type EventSource interface {
	Create(context.Context, string, store.EventInput) (pgtype.UUID, error)
	Update(context.Context, string, pgtype.UUID, store.EventInput) error
	Publish(context.Context, string, pgtype.UUID) error
}

// EventReadSource is the donor-facing read of published events (E-2/E-3).
type EventReadSource interface {
	NearDonor(context.Context, string) ([]store.EventView, error)
	Detail(context.Context, pgtype.UUID) (store.EventView, error)
}

// ReservationSource books, waitlists, and cancels event reservations (E-4/E-9).
type ReservationSource interface {
	Reserve(context.Context, string, pgtype.UUID) (store.ReservationResult, error)
	Cancel(context.Context, string, pgtype.UUID) (store.ReservationResult, error)
}

func (a *api) eventsAdmin() service {
	return service{
		a.Events != nil,
		"Event administration is temporarily unavailable",
		staffTimeout,
		staffStatus,
	}
}

func (a *api) createEvent(w http.ResponseWriter, r *http.Request, session string) {
	var body store.EventInput
	if !decodeJSON(w, r, &body) {
		return
	}
	a.eventsAdmin().serve(w, r, func(ctx context.Context) (any, error) {
		id, err := a.Events.Create(ctx, session, body)
		if err != nil {
			return nil, err
		}
		return map[string]string{"id": id.String()}, nil
	})
}

func (a *api) updateEvent(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "event_id")
	if !ok {
		return
	}
	var body store.EventInput
	if !decodeJSON(w, r, &body) {
		return
	}
	a.eventsAdmin().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]bool{"updated": true}, a.Events.Update(ctx, session, id, body)
	})
}

func (a *api) publishEvent(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "event_id")
	if !ok {
		return
	}
	a.eventsAdmin().serve(w, r, func(ctx context.Context) (any, error) {
		return map[string]bool{"published": true}, a.Events.Publish(ctx, session, id)
	})
}

// events lists the signed-in donor's upcoming events in and near their city
// (E-2), soonest first.
func (a *api) events(w http.ResponseWriter, r *http.Request, session string) {
	svc := service{a.EventRead != nil, donorUnavailable, readTimeout, donorStatus}
	svc.serve(w, r, func(ctx context.Context) (any, error) {
		views, err := a.EventRead.NearDonor(ctx, session)
		if err != nil {
			return nil, err
		}
		return map[string]any{"events": views}, nil
	})
}

// eventDetail is the donor-facing read of one published event (E-3). A draft or
// unknown id answers 404.
func (a *api) eventDetail(w http.ResponseWriter, r *http.Request) {
	if a.EventRead == nil {
		writeError(w, r, http.StatusServiceUnavailable, donorUnavailable, nil)
		return
	}
	id, ok := pathID(w, r, "event_id")
	if !ok {
		return
	}
	ctx, cancel := withTimeout(r, 3*time.Second)
	defer cancel()
	body, err := a.EventRead.Detail(ctx, id)
	switch {
	case errors.Is(err, store.ErrEventNotFound):
		writeError(w, r, http.StatusNotFound, "Record not found", nil)
	case err != nil:
		writeError(w, r, http.StatusServiceUnavailable, donorUnavailable, err)
	default:
		w.Header().Set("Cache-Control", "public, max-age=60")
		writeJSON(w, http.StatusOK, body)
	}
}

// reserveEvent books (or waitlists) the signed-in donor at an event (E-4).
func (a *api) reserveEvent(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "event_id")
	if !ok {
		return
	}
	svc := service{a.Reservations != nil, donorUnavailable, writeTimeout, donorStatus}
	svc.serve(w, r, func(ctx context.Context) (any, error) {
		return a.Reservations.Reserve(ctx, session, id)
	})
}

// cancelEvent cancels the signed-in donor's reservation, promoting the waitlist
// head if a seat freed (E-4/E-9).
func (a *api) cancelEvent(w http.ResponseWriter, r *http.Request, session string) {
	id, ok := pathID(w, r, "event_id")
	if !ok {
		return
	}
	svc := service{a.Reservations != nil, donorUnavailable, writeTimeout, donorStatus}
	svc.serve(w, r, func(ctx context.Context) (any, error) {
		return a.Reservations.Cancel(ctx, session, id)
	})
}
