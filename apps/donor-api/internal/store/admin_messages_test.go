package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/store/queries"
)

func TestStaffResendIsIdempotentLimitedAndAudited(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	support, supportToken := staffFixture(t, pool, "SUPPORT")
	_, adminToken := staffFixture(t, pool, "ADMIN")
	id, _ := donor(t, pool)
	accountCleanup(t, pool, id)
	s := &StaffService{DB: pool}
	message := func(kind string, completed bool, suppressed string) pgtype.UUID {
		t.Helper()
		job := testID(t)
		if _, err := pool.Exec(ctx, `INSERT INTO donor_messages(id,donor_id,kind,dedup_key,context,available_at,completed_at,suppressed_reason,attempts,created_at)
VALUES($1,$2,$3,$4,'{"gift_id":"g"}',now()-interval '2 minutes',CASE WHEN $5 THEN now()-interval '2 minutes' END,NULLIF($6,''),1,now()-interval '2 minutes')`, job, id, kind, "test:"+job.String(), completed, suppressed); err != nil {
			t.Fatal(err)
		}
		return job
	}
	source := message("RECEIPT", true, "")
	for name, candidate := range map[string]pgtype.UUID{
		"pending":    message("RECEIPT", false, ""),
		"suppressed": message("RECEIPT", true, "donor_disabled"),
		"optional":   message("REFERRAL", true, ""),
	} {
		if _, err := s.ResendMessage(ctx, supportToken, candidate, testID(t)); !errors.Is(err, ErrStaffConflict) {
			t.Fatalf("%s message resent: %v", name, err)
		}
	}
	invalid := testID(t)
	invalid.Bytes[6] = 0x10
	if _, err := s.ResendMessage(ctx, supportToken, source, invalid); !errors.Is(err, ErrStaffInput) {
		t.Fatal("non-v4 request id accepted", err)
	}
	if _, err := s.ResendMessage(ctx, "not-a-session", source, testID(t)); !errors.Is(err, ErrStaffDenied) {
		t.Fatal("anonymous resend", err)
	}
	request := testID(t)
	first, err := s.ResendMessage(ctx, supportToken, source, request)
	if err != nil {
		t.Fatal(err)
	}
	view := jsonView(t, first)
	assertKeys(t, view, "queued", "id")
	again, err := s.ResendMessage(ctx, supportToken, source, request)
	if err != nil || jsonView(t, again)["id"] != view["id"] || view["queued"] != true {
		t.Fatal("retry queued another message", again, err)
	}
	if _, err = s.ResendMessage(ctx, adminToken, source, request); !errors.Is(err, ErrStaffConflict) {
		t.Fatal("request id reused by another actor", err)
	}
	if _, err = s.ResendMessage(ctx, supportToken, source, testID(t)); !errors.Is(err, ErrStaffConflict) {
		t.Fatal("resend not rate limited", err)
	}
	var raw string
	var resends int
	if err = pool.QueryRow(ctx, "SELECT context::text,(SELECT count(*) FROM donor_messages WHERE donor_id=$1 AND dedup_key LIKE 'resend:%') FROM donor_messages WHERE dedup_key=$2", id, "resend:"+request.String()).Scan(&raw, &resends); err != nil {
		t.Fatal(err)
	}
	var saved map[string]string
	if err = json.Unmarshal([]byte(raw), &saved); err != nil || saved["gift_id"] != "g" || saved["resend_source"] != source.String() || saved["resend_actor"] != support.String() || resends != 1 {
		t.Fatal("resend context", raw, resends, err)
	}
	var audited int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM donor_staff_audit WHERE actor_id=$1 AND action='message.resend_requested' AND target=$2 AND details->>'source_id'=$3", support, view["id"], source.String()).Scan(&audited); err != nil || audited != 1 {
		t.Fatal("resend not audited", audited, err)
	}
}

func TestStaffClaimRequestQueuesLoginEmail(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	support, token := staffFixture(t, pool, "SUPPORT")
	id, _ := donor(t, pool)
	accountCleanup(t, pool, id)
	s := &StaffService{DB: pool}
	if err := s.RequestClaim(ctx, token, testID(t)); err == nil {
		t.Fatal("claim requested for unknown donor")
	}
	if err := s.RequestClaim(ctx, token, id); err != nil {
		t.Fatal(err)
	}
	var queued, audited int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM donor_login_emails WHERE donor_id=$1 AND created_at>$3),(SELECT count(*) FROM donor_staff_audit WHERE actor_id=$2 AND action='message.claim_requested' AND target=$4)", id, support, time.Now().Add(-time.Minute), id.String()).Scan(&queued, &audited); err != nil || queued != 1 || audited != 1 {
		t.Fatal("claim request", queued, audited, err)
	}
}

func TestStaffDonorDetailShowsUncomputedStreakAsUpdating(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	_, token := staffFixture(t, pool, "SUPPORT")
	id, _ := donor(t, pool)
	q := queries.New(pool)
	now := time.Now().UTC()
	if err := q.SeedProjection(ctx, queries.SeedProjectionParams{ID: testID(t), DonorID: id, ComputedAt: timestamp(now)}); err != nil {
		t.Fatal(err)
	}
	s := &StaffService{DB: pool}
	detail, err := s.DonorDetail(ctx, token, id, 0, 0)
	if err != nil || detail.Streak != nil {
		t.Fatalf("seeded projection reported a streak: %+v, %v", detail.Streak, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RefreshProjectionInTx(ctx, tx, id, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	detail, err = s.DonorDetail(ctx, token, id, 0, 0)
	if err != nil || detail.Streak == nil || detail.Streak.Best != 0 {
		t.Fatalf("computed projection missing from detail: %+v, %v", detail.Streak, err)
	}
}
