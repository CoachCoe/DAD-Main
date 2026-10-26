package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/push"
	"dad/donor-api/internal/recognition"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestReferralCorrectionsTransferTotalsAndAuditAtomically(t *testing.T) {
	pool := database(t)
	for _, c := range []struct{ name, oldOwner, oldKind, newOwner, newKind string }{
		{"different owners same channel", "alice", "LINK", "cara", "LINK"},
		{"different owners different channels", "alice", "LINK", "cara", "IRL"},
		{"same owner different channels", "alice", "LINK", "alice", "IRL"},
		{"earlier direct payment", "alice", "LINK", "", ""},
		{"earlier referred payment", "", "", "cara", "IRL"},
		{"same relationship earlier payment", "alice", "LINK", "alice", "LINK"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			h := newHistoryFixture(t, tx)
			ben, alice, cara := h.donor("ben"), h.donor("alice"), h.donor("cara")
			now := time.Now().UTC().Truncate(time.Microsecond)
			gift := func(id string, at time.Time, owner, kind string) {
				g := recognition.Gift{ID: id, DonorID: "ben", Amount: 3000, Currency: "USD", Frequency: "MONTHLY", PaidAt: at}
				if owner != "" {
					g.Candidate = &recognition.Candidate{ReferrerID: owner, Kind: kind, ExpiresAt: now.Add(time.Hour)}
				}
				h.gift(g)
			}
			gift("later", now.Add(-time.Hour), c.oldOwner, c.oldKind)
			actAs(t, tx, dbroles.ProjectionWorker)
			if _, err = refreshProjection(ctx, tx, ben, now); err != nil {
				t.Fatal(err)
			}
			mustExec(t, tx, "RESET ROLE")
			gift("earlier", now.Add(-24*time.Hour), c.newOwner, c.newKind)
			actAs(t, tx, dbroles.ProjectionWorker)
			for range 2 {
				got, err := refreshProjection(ctx, tx, ben, now)
				if err != nil {
					t.Fatal(err)
				}
				wantOwner := pgtype.UUID{}
				if c.newOwner != "" {
					wantOwner = h.donor(c.newOwner)
				}
				if got.ReferrerID != wantOwner || got.ReferralKind.String != c.newKind || got.FirstGiftID.String != h.objects["earlier"] ||
					got.NetMinor.Int64 != 6000 {
					t.Fatalf("corrected projection %+v", got)
				}
				for _, owner := range []pgtype.UUID{alice, cara} {
					got, err := h.q.Projection(ctx, owner)
					if err != nil {
						// An uncredited donor need not have a projection yet.
						if errors.Is(err, pgx.ErrNoRows) && owner != wantOwner && owner != h.donors[c.oldOwner] {
							continue
						}
						t.Fatal(err)
					}
					want := referralTotals{}
					if owner == wantOwner {
						if c.newKind == "LINK" {
							want.Link, want.LinkMinor = 1, 6000
						} else {
							want.IRL, want.IRLMinor = 1, 6000
						}
					}
					if got.ReferredLinkCount != want.Link || got.ReferredIrlCount != want.IRL || got.ReferredLinkMinor != want.LinkMinor ||
						got.ReferredIrlMinor != want.IRLMinor {
						t.Fatalf("owner %s: totals %+v, want %+v", owner.String(), got, want)
					}
				}
			}
			var oldGift, newGift string
			var oldOwner, newOwner pgtype.UUID
			var oldKind, newKind pgtype.Text
			var rules, count int
			if err = tx.QueryRow(ctx, "SELECT count(*) FROM donor_referral_corrections WHERE donor_id=$1", ben).Scan(&count); err != nil ||
				count != 1 {
				t.Fatalf("corrections %d: %v", count, err)
			}
			if err = tx.QueryRow(ctx, "SELECT old_first_gift_id,new_first_gift_id,old_referrer_id,new_referrer_id,old_kind,new_kind,rules_version FROM donor_referral_corrections WHERE donor_id=$1", ben).Scan(&oldGift, &newGift, &oldOwner, &newOwner, &oldKind, &newKind, &rules); err != nil {
				t.Fatal(err)
			}
			if oldGift != h.objects["later"] || newGift != h.objects["earlier"] || oldOwner != h.donors[c.oldOwner] ||
				newOwner != h.donors[c.newOwner] ||
				oldKind.String != c.oldKind ||
				newKind.String != c.newKind ||
				rules != projectionRules {
				t.Fatal("correction lost previous or current attribution")
			}
			if err = tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			if err = pool.QueryRow(ctx, "SELECT count(*) FROM donor_referral_corrections WHERE donor_id=$1", ben).Scan(&count); err != nil ||
				count != 0 {
				t.Fatal("correction survived rollback", err)
			}
		})
	}
}

func TestCorrectedReferralNoticesRespectLegacyDedupAndCurrentRelationship(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	h := newHistoryFixture(t, tx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ben, alice, cara := h.donor("ben"), h.donor("alice"), h.donor("cara")
	appDevice(t, tx, alice, push.IOS, iosToken, now.Add(24*time.Hour))
	appDevice(t, tx, cara, push.Android, androidToken, now.Add(24*time.Hour))
	h.gift(
		recognition.Gift{
			ID:        "later",
			DonorID:   "ben",
			Amount:    3000,
			Currency:  "USD",
			Frequency: "MONTHLY",
			PaidAt:    now.Add(-time.Hour),
			Candidate: &recognition.Candidate{ReferrerID: "alice", Kind: "LINK", ExpiresAt: now.Add(time.Hour)},
		},
	)
	if _, err = projectFacts(ctx, h.q, ben, now); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"referred_id": ben.String(), "kind": "LINK"}
	if err = queueMessage(ctx, h.q, alice, "REFERRAL", "referral:"+ben.String(), payload, now); err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		tx,
		`INSERT INTO donor_push_notifications(id,donor_id,category,dedup_key,context,available_at,expires_at) VALUES ($1,$2,'REFERRAL_CONVERTED',$3,json_build_object('referred_id',$4::text,'kind','LINK'),$5,$6)`,
		testID(t),
		alice,
		"push:referral:"+ben.String(),
		ben.String(),
		now,
		now.Add(pushLifetime),
	)
	actAs(t, tx, dbroles.ProjectionWorker)
	if _, err = refreshProjection(ctx, tx, ben, now); err != nil {
		t.Fatal(err)
	}
	assertCount := func(owner pgtype.UUID, want int) {
		t.Helper()
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM donor_messages WHERE donor_id=$1 AND kind='REFERRAL'", owner).Scan(&count); err != nil ||
			count != want {
			t.Fatalf("email count %d, want %d: %v", count, want, err)
		}
		if got := len(intentsOf(t, tx, owner, "REFERRAL_CONVERTED")); got != want {
			t.Fatalf("referral push count %d, want %d", got, want)
		}
	}
	assertCount(alice, 1)
	mustExec(t, tx, "RESET ROLE")
	h.gift(
		recognition.Gift{
			ID:        "earlier",
			DonorID:   "ben",
			Amount:    3000,
			Currency:  "USD",
			Frequency: "MONTHLY",
			PaidAt:    now.Add(-24 * time.Hour),
			Candidate: &recognition.Candidate{ReferrerID: "cara", Kind: "IRL", ExpiresAt: now.Add(time.Hour)},
		},
	)
	actAs(t, tx, dbroles.ProjectionWorker)
	for range 2 {
		if _, err = refreshProjection(ctx, tx, ben, now); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(alice, 1)
	assertCount(cara, 1)
	// Delivery rechecks eligibility even for an intent prepared before correction.
	reason, err := referralLapsed(
		ctx,
		h.q,
		paymentObject(t, map[string]any{"referred_id": ben.String(), "kind": "LINK"}),
		queries.Donor{ID: alice},
	)
	if err != nil || reason == "" {
		t.Fatalf("obsolete notice remains deliverable: %q %v", reason, err)
	}
	reason, err = referralLapsed(
		ctx,
		h.q,
		paymentObject(t, map[string]any{"referred_id": ben.String(), "kind": "IRL"}),
		queries.Donor{ID: cara},
	)
	if err != nil || reason != "" {
		t.Fatalf("correct notice suppressed: %q %v", reason, err)
	}
}

func TestRefundDoesNotReplaceTheEarliestPaymentWhenCorrectingAttribution(t *testing.T) {
	ctx := context.Background()
	tx, err := database(t).Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	h := newHistoryFixture(t, tx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ben, alice, cara := h.donor("ben"), h.donor("alice"), h.donor("cara")
	h.gift(
		recognition.Gift{
			ID:        "later",
			DonorID:   "ben",
			Amount:    3000,
			Currency:  "USD",
			Frequency: "MONTHLY",
			PaidAt:    now.Add(-time.Hour),
			Candidate: &recognition.Candidate{ReferrerID: "alice", Kind: "LINK", ExpiresAt: now.Add(time.Hour)},
		},
	)
	if _, err = projectFacts(ctx, h.q, ben, now); err != nil {
		t.Fatal(err)
	}
	h.gift(
		recognition.Gift{
			ID:        "earlier",
			DonorID:   "ben",
			Amount:    3000,
			Currency:  "USD",
			Frequency: "MONTHLY",
			PaidAt:    now.Add(-24 * time.Hour),
			Candidate: &recognition.Candidate{ReferrerID: "cara", Kind: "IRL", ExpiresAt: now.Add(time.Hour)},
		},
	)
	h.refund(recognition.Refund{GiftID: "earlier", Amount: 3000, Status: "SUCCEEDED", UpdatedAt: now.Add(-time.Minute)})
	actAs(t, tx, dbroles.ProjectionWorker)
	p, err := projectFacts(ctx, h.q, ben, now)
	if err != nil || p.ReferrerID != cara || p.FirstGiftID.String != h.objects["earlier"] || p.NetMinor.Int64 != 3000 {
		t.Fatalf("refunded first payment lost credit: %+v %v", p, err)
	}
	old, err := h.q.Projection(ctx, alice)
	if err != nil || old.ReferredLinkCount != 0 || old.ReferredLinkMinor != 0 {
		t.Fatalf("old totals %+v: %v", old, err)
	}
	current, err := h.q.Projection(ctx, cara)
	if err != nil || current.ReferredIrlCount != 1 || current.ReferredIrlMinor != 3000 {
		t.Fatalf("corrected totals %+v: %v", current, err)
	}
	mustExec(t, tx, "RESET ROLE")
	h.refund(recognition.Refund{GiftID: "later", Amount: 3000, Status: "SUCCEEDED", UpdatedAt: now})
	actAs(t, tx, dbroles.ProjectionWorker)
	p, err = projectFacts(ctx, h.q, ben, now)
	if err != nil || p.ReferrerID != cara || p.FirstGiftID.String != h.objects["earlier"] || p.NetMinor.Int64 != 0 {
		t.Fatalf("fully refunded relationship %+v: %v", p, err)
	}
	current, err = h.q.Projection(ctx, cara)
	if err != nil || current.ReferredIrlCount != 0 || current.ReferredIrlMinor != 0 {
		t.Fatalf("full refund totals %+v: %v", current, err)
	}
}
