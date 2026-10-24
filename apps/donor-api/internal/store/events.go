package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"dad/donor-api/internal/geo"
	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EventService is the staff surface for events (PRD §6.10 E-3, Phase 4.2). It
// follows the same draft->publish discipline as InitiativeService: an event is
// editable while DRAFT and only becomes donor-visible once published. It reuses
// the content.publish staff permission — events are the same Impact-Ops
// publishing concern.
type EventService struct{ Staff *StaffService }

const eventPermission = "content.publish"

// eventTypes are the allowed event kinds (E-3).
var eventTypes = map[string]bool{"VOLUNTEER": true, "COMMUNITY": true, "INVITE_ONLY": true}

// EventInput is a staff create/update of an event's editable fields. Times are
// RFC3339; StartsAt must precede EndsAt (the DB enforces it too). Capacity 0
// means uncapped.
type EventInput struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Venue       string `json:"venue"`
	Address     string `json:"address"`
	City        string `json:"city"`
	Zip         string `json:"zip"`
	StartsAt    string `json:"starts_at"`
	EndsAt      string `json:"ends_at"`
	Capacity    int32  `json:"capacity"`
}

// validate checks an event input, returning ErrStaffInput on any problem. The
// slug and plain-text rules mirror InitiativeInput.validate so the two staff
// surfaces agree on what valid authored content looks like.
func (in EventInput) validate(now time.Time) (queries.InsertEventParams, error) {
	var params queries.InsertEventParams
	if len(in.Slug) > 100 || !initiativeSlug.MatchString(in.Slug) {
		return params, ErrStaffInput
	}
	if identity.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 150 || !plainText(in.Title, false) {
		return params, ErrStaffInput
	}
	if !eventTypes[in.Type] {
		return params, ErrStaffInput
	}
	// Optional long/free-text fields: bounded and plain, but may be empty.
	for _, f := range []struct {
		value   string
		maximum int
		multi   bool
	}{{in.Description, 4000, true}, {in.Venue, 200, false}, {in.Address, 300, false}, {in.City, 80, false}} {
		if utf8.RuneCountInString(f.value) > f.maximum || !plainText(f.value, f.multi) {
			return params, ErrStaffInput
		}
	}
	if in.Zip != "" {
		normalized, err := geo.NormalizeZip(in.Zip)
		if err != nil {
			return params, ErrStaffInput
		}
		in.Zip = normalized
	}
	if in.Capacity < 0 {
		return params, ErrStaffInput
	}
	starts, err := time.Parse(time.RFC3339, in.StartsAt)
	if err != nil {
		return params, ErrStaffInput
	}
	ends, err := time.Parse(time.RFC3339, in.EndsAt)
	if err != nil {
		return params, ErrStaffInput
	}
	if !ends.After(starts) {
		return params, ErrStaffInput
	}
	params = queries.InsertEventParams{
		Slug: in.Slug, Title: in.Title, Type: in.Type, Description: in.Description,
		Venue: in.Venue, Address: in.Address, City: in.City, Zip: in.Zip,
		StartsAt: timestamp(starts.UTC()), EndsAt: timestamp(ends.UTC()),
		Capacity: in.Capacity, CreatedAt: timestamp(now),
	}
	return params, nil
}

// Create inserts a new DRAFT event and returns its id.
func (s EventService) Create(ctx context.Context, token string, in EventInput) (pgtype.UUID, error) {
	return staffWrite(ctx, s.Staff, token, eventPermission,
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (pgtype.UUID, error) {
			params, err := in.validate(now)
			if err != nil {
				return pgtype.UUID{}, err
			}
			id, err := NewID()
			if err != nil {
				return pgtype.UUID{}, err
			}
			params.ID = id
			q := queries.New(tx)
			if err = q.InsertEvent(ctx, params); err != nil {
				return pgtype.UUID{}, err
			}
			return id, auditStaff(ctx, q, actor.ID, "event.created", id.String(), map[string]string{"slug": in.Slug}, now)
		})
}

// Update edits an existing event's fields. Publishing is separate.
func (s EventService) Update(ctx context.Context, token string, id pgtype.UUID, in EventInput) error {
	_, err := staffWrite(ctx, s.Staff, token, eventPermission,
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (struct{}, error) {
			params, err := in.validate(now)
			if err != nil {
				return struct{}{}, err
			}
			q := queries.New(tx)
			if _, err = q.EventByID(ctx, id); err != nil {
				return struct{}{}, err
			}
			if err = q.UpdateEvent(ctx, queries.UpdateEventParams{
				ID: id, Title: params.Title, Type: params.Type, Description: params.Description,
				Venue: params.Venue, Address: params.Address, City: params.City, Zip: params.Zip,
				StartsAt: params.StartsAt, EndsAt: params.EndsAt, Capacity: params.Capacity,
				UpdatedAt: timestamp(now),
			}); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, auditStaff(ctx, q, actor.ID, "event.updated", id.String(), nil, now)
		})
	return err
}

// Publish makes a DRAFT event donor-visible.
func (s EventService) Publish(ctx context.Context, token string, id pgtype.UUID) error {
	_, err := staffWrite(ctx, s.Staff, token, eventPermission,
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (struct{}, error) {
			q := queries.New(tx)
			if _, err := q.EventByID(ctx, id); err != nil {
				return struct{}{}, err
			}
			if err := q.PublishEvent(ctx, queries.PublishEventParams{ID: id, PublishedAt: timestamp(now)}); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, auditStaff(ctx, q, actor.ID, "event.published", id.String(), nil, now)
		})
	return err
}

// EventReader is the donor-facing read of published events (Phase 4.2). It only
// ever returns PUBLISHED events; a draft or unknown id reads as "not found", so
// the draft side is never observable to donors.
type EventReader struct{ DB *pgxpool.Pool }

// ErrEventNotFound is returned when an event does not exist or is not published;
// both map to 404 so the draft state is not observable.
var ErrEventNotFound = errors.New("event not found")

// EventView is one event as a donor sees it (E-3). SpotsLeft is nil when the
// event is uncapped. ReservationStatus is the signed-in donor's own status for
// this event (RESERVED/WAITLISTED), or nil on an anonymous read or when they
// hold no active reservation — it drives the Reserve / Join waitlist / Cancel
// button (E-4).
type EventView struct {
	ID                string    `json:"id"`
	Slug              string    `json:"slug"`
	Title             string    `json:"title"`
	Type              string    `json:"type"`
	Description       string    `json:"description"`
	Venue             string    `json:"venue"`
	Address           string    `json:"address"`
	City              string    `json:"city"`
	StartsAt          time.Time `json:"starts_at"`
	EndsAt            time.Time `json:"ends_at"`
	Capacity          int32     `json:"capacity"`
	SpotsLeft         *int32    `json:"spots_left"`
	ReservationStatus *string   `json:"reservation_status"`
}

func eventView(e queries.DonorEvent) EventView {
	return EventView{
		ID: e.ID.String(), Slug: e.Slug, Title: e.Title, Type: e.Type, Description: e.Description,
		Venue: e.Venue, Address: e.Address, City: e.City,
		StartsAt: e.StartsAt.Time, EndsAt: e.EndsAt.Time, Capacity: e.Capacity,
	}
}

// spotsLeft is capacity minus RESERVED count, floored at 0; nil when uncapped.
func spotsLeft(capacity int32, reserved int64) *int32 {
	if capacity <= 0 {
		return nil
	}
	left := capacity - int32(reserved)
	if left < 0 {
		left = 0
	}
	return &left
}

// NearDonor lists published, upcoming events in and near the signed-in donor's
// city (E-2): the same city label or the same three-digit ZIP region, soonest
// first. INVITE_ONLY events are never in this list. A donor with neither a city
// nor a ZIP gets an empty list rather than everything.
func (r EventReader) NearDonor(ctx context.Context, token string) ([]EventView, error) {
	return donorRead(ctx, r.DB, token, func(q *queries.Queries, d queries.Donor, now time.Time) ([]EventView, error) {
		city := ""
		if d.City.Valid {
			city = d.City.String
		}
		zip3 := ""
		if d.Zip.Valid && len(d.Zip.String) >= 3 {
			zip3 = d.Zip.String[:3]
		}
		rows, err := q.UpcomingEventsNearDonor(ctx, queries.UpcomingEventsNearDonorParams{
			Now: timestamp(now), City: city, Zip3: zip3,
		})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return []EventView{}, nil
		}
		ids := make([]pgtype.UUID, 0, len(rows))
		for _, e := range rows {
			ids = append(ids, e.ID)
		}
		reservedCounts, err := q.ReservedCountsForEvents(ctx, ids)
		if err != nil {
			return nil, err
		}
		reserved := make(map[string]int64, len(reservedCounts))
		for _, rc := range reservedCounts {
			reserved[rc.EventID.String()] = rc.Reserved
		}
		mine, err := q.DonorReservationsForEvents(ctx, queries.DonorReservationsForEventsParams{DonorID: d.ID, EventIds: ids})
		if err != nil {
			return nil, err
		}
		status := make(map[string]string, len(mine))
		for _, m := range mine {
			status[m.EventID.String()] = m.Status
		}
		out := make([]EventView, 0, len(rows))
		for _, e := range rows {
			v := eventView(e)
			v.SpotsLeft = spotsLeft(e.Capacity, reserved[v.ID])
			if s, ok := status[v.ID]; ok {
				v.ReservationStatus = &s
			}
			out = append(out, v)
		}
		return out, nil
	})
}

// Detail returns one published event by id for the detail page (E-3). A draft
// or unknown id is ErrEventNotFound.
func (r EventReader) Detail(ctx context.Context, id pgtype.UUID) (EventView, error) {
	q := queries.New(r.DB)
	e, err := q.EventByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return EventView{}, ErrEventNotFound
	}
	if err != nil {
		return EventView{}, err
	}
	if e.PublishState != "PUBLISHED" {
		return EventView{}, ErrEventNotFound
	}
	v := eventView(e)
	if e.Capacity > 0 {
		reserved, err := q.ReservedCount(ctx, id)
		if err != nil {
			return EventView{}, err
		}
		v.SpotsLeft = spotsLeft(e.Capacity, reserved)
	}
	return v, nil
}

// Reservation statuses (also the DB check constraint).
const (
	reservationReserved   = "RESERVED"
	reservationWaitlisted = "WAITLISTED"
	reservationCancelled  = "CANCELLED"
)

// eventPromotedCategory is the push category for a waitlist promotion (E-4). It
// is gated at send time on the donor's volunteer opt-in, the same switch that
// turns on event alerts (E-1).
const eventPromotedCategory = "EVENT_PROMOTED"

var (
	// ErrEventFull is never returned to the donor as an error — reserving a
	// full event waitlists them instead — but is used internally to branch.
	// ErrReservationClosed is returned when a donor tries to reserve or cancel
	// after the event has started (E-9: cancel is allowed only up to start).
	ErrReservationClosed = errors.New("event has already started")
)

// ReservationResult is the outcome of a reserve/cancel, so the caller can tell
// the donor whether they got a seat or a waitlist place.
type ReservationResult struct {
	Status string `json:"status"`
}

// ReservationService books, waitlists, and cancels event reservations under
// the event row lock so capacity is enforced race-free, and promotes the
// waitlist head synchronously on cancel (PRD §6.10 E-4, E-9).
type ReservationService struct{ DB *pgxpool.Pool }

// Reserve books the signed-in donor a spot at an event, or waitlists them when
// it is full (E-4). Reserving is refused once the event has started. A donor
// who already holds an active reservation keeps it (idempotent).
func (s ReservationService) Reserve(ctx context.Context, token string, eventID pgtype.UUID) (ReservationResult, error) {
	return donorWrite(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (ReservationResult, error) {
		q := queries.New(tx)
		event, err := q.LockEvent(ctx, eventID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ReservationResult{}, ErrEventNotFound
		}
		if err != nil {
			return ReservationResult{}, err
		}
		if event.PublishState != "PUBLISHED" {
			return ReservationResult{}, ErrEventNotFound
		}
		if !now.Before(event.StartsAt.Time) {
			return ReservationResult{}, ErrReservationClosed
		}
		// An existing active reservation is returned unchanged (idempotent).
		existing, err := q.ReservationFor(ctx, queries.ReservationForParams{EventID: eventID, DonorID: d.ID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ReservationResult{}, err
		}
		if err == nil && existing.Status != reservationCancelled {
			return ReservationResult{Status: existing.Status}, nil
		}
		// Capacity 0 means uncapped. Otherwise a seat is free when RESERVED
		// count is below capacity.
		status := reservationReserved
		if event.Capacity > 0 {
			reserved, err := q.ReservedCount(ctx, eventID)
			if err != nil {
				return ReservationResult{}, err
			}
			if reserved >= int64(event.Capacity) {
				status = reservationWaitlisted
			}
		}
		id, err := NewID()
		if err != nil {
			return ReservationResult{}, err
		}
		if err = q.UpsertReservation(ctx, queries.UpsertReservationParams{
			ID: id, EventID: eventID, DonorID: d.ID, Status: status,
			ReservedAt: timestamp(now), CreatedAt: timestamp(now),
		}); err != nil {
			return ReservationResult{}, err
		}
		return ReservationResult{Status: status}, nil
	})
}

// Cancel releases the signed-in donor's reservation (E-9, up to event start)
// and, if that freed a seat, promotes the head of the waitlist to RESERVED and
// queues a push to them (E-4) — all in the same transaction under the event
// lock, so the promotion is atomic with the cancellation.
func (s ReservationService) Cancel(ctx context.Context, token string, eventID pgtype.UUID) (ReservationResult, error) {
	return donorWrite(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (ReservationResult, error) {
		q := queries.New(tx)
		event, err := q.LockEvent(ctx, eventID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ReservationResult{}, ErrEventNotFound
		}
		if err != nil {
			return ReservationResult{}, err
		}
		if !now.Before(event.StartsAt.Time) {
			return ReservationResult{}, ErrReservationClosed
		}
		existing, err := q.ReservationFor(ctx, queries.ReservationForParams{EventID: eventID, DonorID: d.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ReservationResult{}, ErrEventNotFound
		}
		if err != nil {
			return ReservationResult{}, err
		}
		if existing.Status == reservationCancelled {
			return ReservationResult{Status: reservationCancelled}, nil
		}
		wasReserved := existing.Status == reservationReserved
		if err = q.SetReservationStatus(ctx, queries.SetReservationStatusParams{
			EventID: eventID, DonorID: d.ID, Status: reservationCancelled, UpdatedAt: timestamp(now),
		}); err != nil {
			return ReservationResult{}, err
		}
		// Cancelling a RESERVED seat frees a place; promote the waitlist head.
		// Cancelling a WAITLISTED spot frees nothing, so no promotion.
		if wasReserved {
			if err = s.promoteWaitlistHead(ctx, q, event, now); err != nil {
				return ReservationResult{}, err
			}
		}
		return ReservationResult{Status: reservationCancelled}, nil
	})
}

// promoteWaitlistHead promotes the earliest-queued waitlisted donor (if any) to
// RESERVED and queues a promotion push to them. Called under the event lock.
func (s ReservationService) promoteWaitlistHead(ctx context.Context, q *queries.Queries, event queries.DonorEvent, now time.Time) error {
	head, err := q.WaitlistHead(ctx, event.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // no one waiting
	}
	if err != nil {
		return err
	}
	if err = q.PromoteReservation(ctx, queries.PromoteReservationParams{ID: head.ID, UpdatedAt: timestamp(now)}); err != nil {
		return err
	}
	pushID, err := NewID()
	if err != nil {
		return err
	}
	context, err := json.Marshal(map[string]string{"event_id": event.ID.String()})
	if err != nil {
		return err
	}
	// dedup_key is per (promotion, donor, event) so a re-promotion after a
	// later cancel/re-waitlist is a distinct, deliverable notification.
	dedup := "push:event-promoted:" + event.ID.String() + ":" + head.DonorID.String() + ":" + head.ReservedAt.Time.UTC().Format(time.RFC3339Nano)
	return q.QueueEventPush(ctx, queries.QueueEventPushParams{
		ID: pushID, DonorID: head.DonorID, Category: eventPromotedCategory, DedupKey: dedup,
		Context: context, AvailableAt: timestamp(now), ExpiresAt: event.StartsAt,
	})
}
