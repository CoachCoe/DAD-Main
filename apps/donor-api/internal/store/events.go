package store

import (
	"context"
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
// event is uncapped.
type EventView struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	Venue       string    `json:"venue"`
	Address     string    `json:"address"`
	City        string    `json:"city"`
	StartsAt    time.Time `json:"starts_at"`
	EndsAt      time.Time `json:"ends_at"`
	Capacity    int32     `json:"capacity"`
}

func eventView(e queries.DonorEvent) EventView {
	return EventView{
		ID: e.ID.String(), Slug: e.Slug, Title: e.Title, Type: e.Type, Description: e.Description,
		Venue: e.Venue, Address: e.Address, City: e.City,
		StartsAt: e.StartsAt.Time, EndsAt: e.EndsAt.Time, Capacity: e.Capacity,
	}
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
		out := make([]EventView, 0, len(rows))
		for _, e := range rows {
			out = append(out, eventView(e))
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
	return eventView(e), nil
}
