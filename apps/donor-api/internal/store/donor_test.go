package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5/pgtype"
)

func donorServiceFixture(t *testing.T) (DonorService, string, pgtype.UUID) {
	t.Helper()
	pool := database(t)
	api := as(t, dbroles.API)
	id, _ := donor(t, pool)
	accountCleanup(t, pool, id)
	t.Cleanup(func() {
		for _, sql := range []string{"DELETE FROM donor_referral_touches WHERE referrer_id=$1", "DELETE FROM donor_preference_events WHERE donor_id=$1", "DELETE FROM donor_preferences WHERE donor_id=$1"} {
			if _, err := pool.Exec(context.Background(), sql, id); err != nil {
				t.Error(err)
			}
		}
	})
	proof := link(t, pool, id, time.Now())
	tokens, err := (AccountService{DB: api}).ClaimAccount(context.Background(), proof, "", Client{})
	if err != nil {
		t.Fatal(err)
	}
	return DonorService{DB: api, Origin: "https://donor.example.org"}, tokens.Access, id
}

func TestPreferenceConcurrencyAndConsentAudit(t *testing.T) {
	s, token, id := donorServiceFixture(t)
	pool := database(t)
	ctx := context.Background()
	profile, err := s.Profile(ctx, token)
	if err != nil || profile.Version != 0 || profile.ReferralEmails || profile.MarketingEmails || profile.ConsentVersion != consentVersion {
		t.Fatal(profile, err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := s.SavePreferences(ctx, token, Preferences{Version: 0, ReferralEmails: true})
			results <- err
		}()
	}
	close(start)
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrPreferenceConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("lost update", success, conflict)
	}
	var count int
	var version int
	var consent, source string
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_preference_events WHERE donor_id=$1", id).Scan(&count); err != nil ||
		count != 1 {
		t.Fatal("duplicate/missing consent event", err)
	}
	if err := pool.QueryRow(ctx, "SELECT version,consent_version,source FROM donor_preference_events WHERE donor_id=$1", id).Scan(&version, &consent, &source); err != nil ||
		version != 1 ||
		consent != consentVersion ||
		source != "account" {
		t.Fatal("audit mismatch", err)
	}
	if err = s.SignOutEverywhere(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Profile(ctx, token); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked session accepted", err)
	}
}

func TestSharingRotationAppliesLastTouchAndExpiry(t *testing.T) {
	first, token, id := donorServiceFixture(t)
	pool := database(t)
	second, other, secondID := donorServiceFixture(t)
	ctx := context.Background()
	now := time.Now()
	before, err := first.Sharing(ctx, token)
	if err != nil || before.PublicName != nil || before.Link != nil || before.IRLLink != nil {
		t.Fatal(before, err)
	}
	for _, name := range []string{" ", "email@example.org", "name\ncontrol", strings.Repeat("x", 81)} {
		if _, err := first.SaveSharing(ctx, token, name, false); !errors.Is(err, ErrPublicName) {
			t.Fatal("invalid public name", err)
		}
	}
	a, err := first.SaveSharing(ctx, token, "  First  ", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.SaveSharing(ctx, other, "Second", false)
	if err != nil {
		t.Fatal(err)
	}
	if *a.PublicName != "First" || *a.IRLLink != *a.Link+"?via=irl" {
		t.Fatal("wrong share links")
	}
	code := strings.TrimPrefix(*a.Link, first.Origin+"/r/")
	touch, err := first.CaptureReferral(ctx, code, "LINK", "", now)
	if err != nil || touch.Token == "" || *touch.PublicName != "First" {
		t.Fatal(touch, err)
	}
	later, err := first.CaptureReferral(ctx, strings.TrimPrefix(*b.Link, second.Origin+"/r/"), "IRL", touch.Token, now.Add(10*24*time.Hour))
	if err != nil || later.Token != touch.Token || *later.PublicName != "Second" {
		t.Fatal("last touch not applied", later, err)
	}
	saved, err := queries.New(pool).ReferralTouchByToken(ctx, HashToken(touch.Token))
	if err != nil || saved.Kind != "IRL" || !saved.ExpiresAt.Time.Equal(now.Add(10*24*time.Hour+30*24*time.Hour).Truncate(time.Microsecond)) {
		t.Fatal("last-touch attribution type/expiry not updated", err)
	}
	rotated, err := first.SaveSharing(ctx, token, "First", true)
	if err != nil || *rotated.Link == *a.Link {
		t.Fatal("code not rotated", err)
	}
	fresh, err := first.CaptureReferral(ctx, code, "IRL", "", now)
	if err != nil || fresh.Token != "" {
		t.Fatal("retired code captured new visitor", err)
	}
	retained, err := first.CaptureReferral(ctx, code, "IRL", touch.Token, now)
	if err != nil || retained.Token != touch.Token {
		t.Fatal("rotation invalidated captured touch", err)
	}
	renewed, err := first.CaptureReferral(
		ctx,
		strings.TrimPrefix(*rotated.Link, first.Origin+"/r/"),
		"IRL",
		touch.Token,
		now.Add(41*24*time.Hour),
	)
	if err != nil || renewed.Token == touch.Token || renewed.Token == "" {
		t.Fatal("expired token repurposed", err)
	}
	if _, err = pool.Exec(ctx, "UPDATE donors SET disabled=true WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err = first.Sharing(ctx, token); !errors.Is(err, ErrDenied) {
		t.Fatal("disabled donor can share", err)
	}
	// The token now points at Second via last-touch; a disabled referrer's name
	// must not be disclosed on a subsequent capture.
	if _, err = pool.Exec(ctx, "UPDATE donors SET disabled=true WHERE id=$1", secondID); err != nil {
		t.Fatal(err)
	}
	existing, err := first.CaptureReferral(ctx, code, "LINK", touch.Token, now)
	if err != nil || existing.PublicName != nil {
		t.Fatal("disabled donor name disclosed", err)
	}
}

func TestConcurrentSharingKeepsOnePermanentCode(t *testing.T) {
	s, token, _ := donorServiceFixture(t)
	results := make(chan Sharing, 2)
	failures := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			value, err := s.SaveSharing(context.Background(), token, "Sam", false)
			results <- value
			failures <- err
		}()
	}
	close(start)
	a, b := <-results, <-results
	for i := 0; i < 2; i++ {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if a.Link == nil || b.Link == nil || *a.Link != *b.Link {
		t.Fatal("concurrent sharing returned different codes")
	}
}
