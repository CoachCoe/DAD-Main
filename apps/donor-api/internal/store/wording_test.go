package store

import (
	"context"
	"errors"
	"testing"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/push"
)

// TestStaffRewordsBadgesAndMilestones covers PRD §8 Content: staff change the
// words on a badge and a milestone notification, and can restore the default.
func TestStaffRewordsBadgesAndMilestones(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	staff := &StaffService{DB: as(t, dbroles.API)}
	_, content := staffFixture(t, pool, "SUPER_ADMIN")
	_, growth := staffFixture(t, pool, "OP_GROWTH")
	wording := WordingService{Staff: staff}
	t.Cleanup(func() { mustExec(t, pool, "DELETE FROM donor_recognition_wording") })

	badge := WordingChange{Kind: WordingBadge, Key: "WT", Title: "Top Team", Text: "Your team wins the month"}
	if err := wording.SetWording(ctx, growth, badge); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("growth operator rewording a badge: %v; want ErrStaffForbidden", err)
	}
	for name, change := range map[string]WordingChange{
		"unknown badge":     {Kind: WordingBadge, Key: "NOPE", Title: "A", Text: "B"},
		"yearly milestone":  {Kind: WordingMilestone, Key: "1825", Title: "A", Text: "B"},
		"padded key":        {Kind: WordingMilestone, Key: "030", Title: "A", Text: "B"},
		"title only":        {Kind: WordingBadge, Key: "WT", Title: "A"},
		"control character": {Kind: WordingBadge, Key: "WT", Title: "A\nB", Text: "B"},
	} {
		if err := wording.SetWording(ctx, content, change); !errors.Is(err, ErrStaffInput) {
			t.Errorf("%s: %v; want ErrStaffInput", name, err)
		}
	}
	if err := wording.SetWording(ctx, content, badge); err != nil {
		t.Fatal(err)
	}
	milestone := WordingChange{Kind: WordingMilestone, Key: "30", Title: "A month of giving", Text: "Thirty days of showing up."}
	if err := wording.SetWording(ctx, content, milestone); err != nil {
		t.Fatal(err)
	}

	_, donorToken := signedInDonor(t, pool)
	views, err := BadgeService{DB: pool, Staff: staff}.Badges(ctx, donorToken)
	if err != nil {
		t.Fatal(err)
	}
	if last := views[len(views)-1]; last.Code != "WT" || last.Title != badge.Title || last.Rule != badge.Text {
		t.Fatalf("donor sees %+v, want the staff wording", last)
	}

	f := newPushFixture(t)
	appDevice(t, pool, f.donor, push.IOS, iosToken, f.now.Add(1<<40))
	apns := &fakeProvider{}
	f.deliver(t, PushWorker{DB: as(t, dbroles.PushWorker), Providers: map[string]push.Provider{push.IOS: apns}}, f.now)
	if len(apns.notes) != 1 || apns.notes[0].Title != milestone.Title || apns.notes[0].Body != milestone.Text {
		t.Fatalf("milestone notification %+v, want the staff wording", apns.notes)
	}

	if err := wording.SetWording(ctx, content, WordingChange{Kind: WordingBadge, Key: "WT"}); err != nil {
		t.Fatal(err)
	}
	all, err := wording.Wording(ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	wt, month := all.Badges[len(all.Badges)-1], all.Milestones[1]
	if wt.Custom || wt.Title != "Winning Team" || !month.Custom || month.Title != milestone.Title ||
		month.DefaultTitle != "Your 30-day giving streak" {
		t.Fatalf("after reset: badge %+v, milestone %+v", wt, month)
	}
	var audited int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_staff_audit WHERE action='content.wording_changed' AND target IN ('BADGE:WT','MILESTONE:30')").
		Scan(&audited); err != nil ||
		audited < 3 {
		t.Fatalf("%d wording changes audited, err %v; want 3", audited, err)
	}
}
