package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func cleanEvents(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM donor_events")
	})
}

// eventInput builds a valid staff event input starting at +days days, in the
// given city/zip, of the given type.
func eventInput(slug, city, zip, kind string, startIn time.Duration) EventInput {
	start := time.Now().UTC().Add(startIn)
	return EventInput{
		Slug: slug, Title: "Event " + slug, Type: kind,
		Description: "A gathering.", Venue: "Community Hall", Address: "1 Main St",
		City: city, Zip: zip,
		StartsAt: start.Format(time.RFC3339),
		EndsAt:   start.Add(2 * time.Hour).Format(time.RFC3339),
		Capacity: 20,
	}
}

func TestEventDraftPublishAndValidation(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	cleanEvents(t, pool)
	_, adminToken := staffFixture(t, pool, "ADMIN")
	_, support := staffFixture(t, pool, "SUPPORT")
	s := EventService{Staff: &StaffService{DB: api}}

	// A non-publisher role cannot create events.
	if _, err := s.Create(ctx, support, eventInput("water-cleanup", "SF", "94103", "VOLUNTEER", 48*time.Hour)); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("SUPPORT created an event: %v", err)
	}

	// Bad slug, bad type, and ends-before-starts are each refused.
	for name, in := range map[string]EventInput{
		"bad slug": eventInput("Bad Slug!", "SF", "94103", "VOLUNTEER", 48*time.Hour),
		"bad type": eventInput("x", "SF", "94103", "PARTY", 48*time.Hour),
	} {
		if _, err := s.Create(ctx, adminToken, in); !errors.Is(err, ErrStaffInput) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	backwards := eventInput("backwards", "SF", "94103", "VOLUNTEER", 48*time.Hour)
	backwards.EndsAt = backwards.StartsAt
	if _, err := s.Create(ctx, adminToken, backwards); !errors.Is(err, ErrStaffInput) {
		t.Fatalf("ends==starts accepted: %v", err)
	}

	// Create a DRAFT, confirm it is not donor-visible, then publish it.
	id, err := s.Create(ctx, adminToken, eventInput("water-cleanup", "SF", "94103", "VOLUNTEER", 48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	reader := EventReader{DB: pool}
	if _, err := reader.Detail(ctx, id); !errors.Is(err, ErrEventNotFound) {
		t.Fatalf("draft event was donor-visible: %v", err)
	}
	if err := s.Publish(ctx, adminToken, id); err != nil {
		t.Fatal(err)
	}
	view, err := reader.Detail(ctx, id)
	if err != nil || view.Title != "Event water-cleanup" || view.Capacity != 20 {
		t.Fatalf("published detail: %+v %v", view, err)
	}
}

func TestUpcomingEventsNearDonor(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	cleanEvents(t, pool)
	_, adminToken := staffFixture(t, pool, "ADMIN")
	s := EventService{Staff: &StaffService{DB: api}}
	reader := EventReader{DB: pool}

	// A donor in San Francisco, ZIP 94103 (SCF 941).
	donorID, donorToken := signedInDonor(t, pool)
	if _, err := pool.Exec(ctx, "UPDATE donors SET city=$2, zip=$3 WHERE id=$1", donorID, "San Francisco", "94103"); err != nil {
		t.Fatal(err)
	}

	publish := func(in EventInput) pgtype.UUID {
		id, err := s.Create(ctx, adminToken, in)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Publish(ctx, adminToken, id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Matches by city label (different ZIP region).
	cityMatch := publish(eventInput("city-match", "San Francisco", "90001", "VOLUNTEER", 24*time.Hour))
	// Matches by ZIP-3 region 941 (different/empty city).
	zipMatch := publish(eventInput("zip-match", "Daly City", "94105", "COMMUNITY", 72*time.Hour))
	// Far away: different city AND different ZIP region -> excluded.
	publish(eventInput("far", "Chicago", "60601", "VOLUNTEER", 24*time.Hour))
	// INVITE_ONLY -> never in the open near-city list even when local.
	publish(eventInput("invite", "San Francisco", "94103", "INVITE_ONLY", 24*time.Hour))
	// In the past -> excluded (upcoming only).
	publish(eventInput("past", "San Francisco", "94103", "VOLUNTEER", -24*time.Hour))
	// A DRAFT local event (created, not published) -> excluded.
	if _, err := s.Create(ctx, adminToken, eventInput("draft-local", "San Francisco", "94103", "VOLUNTEER", 24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	views, err := reader.NearDonor(ctx, donorToken)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, v := range views {
		got[v.ID] = true
	}
	if len(views) != 2 || !got[cityMatch.String()] || !got[zipMatch.String()] {
		t.Fatalf("near-donor list = %d events %+v; want city-match + zip-match only", len(views), views)
	}
	// Soonest first: city-match (+24h) before zip-match (+72h).
	if views[0].ID != cityMatch.String() {
		t.Fatalf("not soonest-first: %+v", views)
	}

	// A donor with neither city nor ZIP matches nothing.
	otherID, otherToken := signedInDonor(t, pool)
	if _, err := pool.Exec(ctx, "UPDATE donors SET city=NULL, zip=NULL WHERE id=$1", otherID); err != nil {
		t.Fatal(err)
	}
	empty, err := reader.NearDonor(ctx, otherToken)
	if err != nil || len(empty) != 0 {
		t.Fatalf("cityless/zipless donor saw %d events: %v", len(empty), err)
	}
}
