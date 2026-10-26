package store

import (
	"context"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
)

func TestEventAlertsResumeInBoundedBatchesAndStopAfterCancellation(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	cleanEvents(t, pool)
	_, token := staffFixture(t, pool, "SUPER_ADMIN")
	events := EventService{Staff: &StaffService{DB: as(t, dbroles.API)}}
	id, err := events.Create(ctx, token, eventInput("bounded-alerts", "Fanout City", "", "COMMUNITY", 48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for range EventAlertBatch + 3 {
		d, _ := donor(t, pool)
		t.Cleanup(func() {
			cleanExec(t, "DELETE FROM donor_push_notifications WHERE donor_id=$1", d)
			cleanExec(t, "DELETE FROM donor_preferences WHERE donor_id=$1", d)
		})
		mustExec(t, pool, "UPDATE donors SET city='Fanout City' WHERE id=$1", d)
		mustExec(t, pool, `INSERT INTO donor_preferences(id,donor_id,version,referral_emails,marketing_emails,volunteer,push_event_nearby)
			VALUES($1,$2,1,false,false,true,true)`, testID(t), d)
	}
	if err := events.Publish(ctx, token, id); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	step := func() bool {
		t.Helper()
		worked, err := (EventAlertService{DB: as(t, dbroles.ProjectionWorker)}).QueueNext(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		return worked
	}
	assertProgress := func(want int, complete bool) {
		t.Helper()
		var count int
		var done bool
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM donor_push_notifications WHERE context->>'event_id'=$1`, id.String()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT completed_at IS NOT NULL FROM donor_event_alert_jobs WHERE event_id=$1`, id).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if count != want || done != complete {
			t.Fatalf("alerts=%d complete=%t, want %d %t", count, done, want, complete)
		}
	}
	if !step() {
		t.Fatal("first batch was not claimed")
	}
	assertProgress(EventAlertBatch, false)
	if !step() {
		t.Fatal("restarted worker did not resume")
	}
	assertProgress(EventAlertBatch+3, true)
	if err := events.Publish(ctx, token, id); err != nil {
		t.Fatal(err)
	}
	if step() {
		t.Fatal("republishing restarted a completed fanout")
	}
	assertProgress(EventAlertBatch+3, true)

	// A cancelled event must end a partially completed job without queueing more.
	mustExec(t, pool, "UPDATE donor_event_alert_jobs SET last_donor_id=NULL,completed_at=NULL WHERE event_id=$1", id)
	mustExec(t, pool, "UPDATE donor_events SET cancelled_at=now() WHERE id=$1", id)
	if !step() {
		t.Fatal("cancelled job was not retired")
	}
	assertProgress(EventAlertBatch+3, true)
}
