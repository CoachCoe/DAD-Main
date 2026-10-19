package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/payments"
)

// runAnchorPass calls the backfill until its pass ends, returning the errors
// the pass reported.
func runAnchorPass(t *testing.T, b *AnchorBackfill, now time.Time) []error {
	t.Helper()
	var failures []error
	for range 100 {
		worked, err := b.SyncNext(context.Background(), now)
		if err != nil {
			failures = append(failures, err)
		}
		if !worked {
			return failures
		}
	}
	t.Fatal("backfill pass did not end")
	return nil
}

func TestAnchorBackfillSyncsSubscriptionsSavedWithoutOne(t *testing.T) {
	r := newRecurringFixture(t)
	ctx := context.Background()
	stale := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := r.db.Exec(ctx, "UPDATE donor_subscriptions SET billing_anchor=NULL,observed_at=$2 WHERE attempt_id=$1", r.attempt, stale); err != nil {
		t.Fatal(err)
	}
	if pause, err := r.service.PauseOptions(ctx, r.token); err != nil || pause.State != "unavailable" {
		t.Fatal("pause dates offered without a billing anchor", pause, err)
	}

	backfill := &AnchorBackfill{DB: as(t, dbroles.StripeWorker), Gateway: r.sub}
	now := time.Now().UTC()
	if failures := runAnchorPass(t, backfill, now); len(failures) != 0 {
		t.Fatal(failures)
	}
	var observed time.Time
	if err := r.db.QueryRow(ctx, "SELECT observed_at FROM donor_subscriptions WHERE attempt_id=$1 AND billing_anchor=$2", r.attempt, r.start).Scan(&observed); err != nil ||
		!observed.After(stale) {
		t.Fatal("backfill did not sync the billing anchor", observed, err)
	}
	if pause, err := r.service.PauseOptions(ctx, r.token); err != nil || pause.State != "ready" || len(pause.Options) != 3 {
		t.Fatal("pause dates not offered after the backfill", pause, err)
	}

	reads := r.sub.retrievals
	if worked, err := backfill.SyncNext(ctx, now.Add(anchorBackfillInterval-time.Second)); worked || err != nil {
		t.Fatal("pass started before the interval elapsed", err)
	}
	runAnchorPass(t, backfill, now.Add(anchorBackfillInterval))
	if r.sub.retrievals != reads {
		t.Fatal("a synced subscription was read again")
	}
}

func TestAnchorBackfillPassesOverFailures(t *testing.T) {
	r := newRecurringFixture(t)
	ctx := context.Background()
	if _, err := r.db.Exec(ctx, "UPDATE donor_subscriptions SET billing_anchor=NULL WHERE attempt_id=$1", r.attempt); err != nil {
		t.Fatal(err)
	}
	remote := r.gateway.subscriptions[r.subID]
	delete(r.gateway.subscriptions, r.subID)
	backfill := &AnchorBackfill{DB: as(t, dbroles.StripeWorker), Gateway: r.sub}
	now := time.Now().UTC()
	failures := runAnchorPass(t, backfill, now)
	if len(failures) == 0 || !errors.Is(failures[len(failures)-1], payments.ErrProcessorUnavailable) {
		t.Fatal("unreachable processor not reported", failures)
	}
	r.gateway.subscriptions[r.subID] = remote
	runAnchorPass(t, backfill, now.Add(anchorBackfillInterval))
	var anchored bool
	if err := r.db.QueryRow(ctx, "SELECT billing_anchor IS NOT NULL FROM donor_subscriptions WHERE attempt_id=$1", r.attempt).Scan(&anchored); err != nil ||
		!anchored {
		t.Fatal("next pass did not retry the failed subscription", err)
	}
}
