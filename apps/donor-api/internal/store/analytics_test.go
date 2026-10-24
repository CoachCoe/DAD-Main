package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
)

func TestAnalyticsRecord(t *testing.T) {
	s, token, id := donorServiceFixture(t)
	api := as(t, dbroles.API)
	svc := AnalyticsService{DB: api}
	ctx := context.Background()
	now := time.Now().UTC()
	t.Cleanup(func() {
		database(t).Exec(ctx, "DELETE FROM analytics_events WHERE donor_id=$1 OR donor_id IS NULL", id)
	})
	_ = s // the donor service established the account the token authenticates

	// Anonymous batch (no token): events are stored with a null donor.
	anon := []Event{
		{Name: "app_opened", OccurredAt: now},
		{Name: "attribution_captured", OccurredAt: now, Properties: map[string]string{"source": "company"}},
	}
	if err := svc.Record(ctx, "WEB", "", anon); err != nil {
		t.Fatalf("anonymous record: %v", err)
	}

	// Authenticated batch: events attribute to the donor.
	if err := svc.Record(ctx, "IOS", token, []Event{{Name: "gift_completed", OccurredAt: now}}); err != nil {
		t.Fatalf("authenticated record: %v", err)
	}
	pool := database(t)
	var attributed int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM analytics_events WHERE donor_id=$1 AND name='gift_completed'", id).Scan(&attributed); err != nil ||
		attributed != 1 {
		t.Fatalf("attributed events = %d, err %v; want 1", attributed, err)
	}
	var anonCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM analytics_events WHERE donor_id IS NULL").Scan(&anonCount); err != nil ||
		anonCount < 2 {
		t.Fatalf("anonymous events = %d, err %v; want >= 2", anonCount, err)
	}

	// An invalid token records anonymously rather than failing (pre-sign-in).
	if err := svc.Record(ctx, "WEB", "not-a-valid-session", []Event{{Name: "app_opened", OccurredAt: now}}); err != nil {
		t.Fatalf("invalid-token record should succeed anonymously: %v", err)
	}

	// Rejections: unknown name, bad property, bad source, empty/oversize batch,
	// missing time, future time — each rejects the WHOLE batch.
	for _, tc := range []struct {
		name   string
		source string
		events []Event
	}{
		{"unknown name", "WEB", []Event{{Name: "nope", OccurredAt: now}}},
		{"bad property", "WEB", []Event{{Name: "attribution_captured", OccurredAt: now, Properties: map[string]string{"source": "bogus"}}}},
		{"bad source", "DESKTOP", []Event{{Name: "app_opened", OccurredAt: now}}},
		{"empty batch", "WEB", []Event{}},
		{"missing time", "WEB", []Event{{Name: "app_opened"}}},
		{"future time", "WEB", []Event{{Name: "app_opened", OccurredAt: now.Add(time.Hour)}}},
		{"one bad in batch", "WEB", []Event{{Name: "app_opened", OccurredAt: now}, {Name: "nope", OccurredAt: now}}},
	} {
		if err := svc.Record(ctx, tc.source, "", tc.events); !errors.Is(err, ErrAnalytics) {
			t.Errorf("%s: error = %v; want ErrAnalytics", tc.name, err)
		}
	}

	// The "one bad in batch" case must not have stored the good event either.
	var leaked int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM analytics_events WHERE name='app_opened' AND source='WEB' AND donor_id IS NULL").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	// Two valid anonymous WEB app_opened were stored earlier (the anon batch's
	// app_opened and the invalid-token one); the rejected batch added none.
	if leaked != 2 {
		t.Errorf("anonymous WEB app_opened count = %d; want 2 (rejected batch stored nothing)", leaked)
	}
}
