package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cleanInitiatives removes any initiatives (and cascaded metrics) created by a
// test, restoring the table to its pre-test state.
func cleanInitiatives(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	before := []string{}
	rows, err := pool.Query(ctx, "SELECT id::text FROM donor_initiatives")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		before = append(before, id)
	}
	rows.Close()
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DELETE FROM donor_initiatives WHERE id::text <> ALL($1)", before); err != nil {
			t.Error(err)
		}
	})
}

func validInitiative(slug string) InitiativeInput {
	return InitiativeInput{
		Slug: slug, Title: "Clean Water " + slug, Summary: "Wells for rural counties.",
		Type: "WATER", Location: "Northern region", Region: "North", Counties: []string{"Alpha", "Beta"},
		StartedOn: "2026-03-01", Status: "ACTIVE", CommittedMinor: 5_000_00,
	}
}

func TestInitiativeDraftPublishLifecycleAndMetricVerificationGate(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	cleanInitiatives(t, pool)
	_, adminToken := staffFixture(t, pool, "SUPER_ADMIN")
	_, support := staffFixture(t, pool, "OP_PARTNERSHIPS")
	s := InitiativeService{Staff: &StaffService{DB: api}}

	// A non-publisher role cannot create initiatives.
	if _, err := s.Create(ctx, support, validInitiative("water-north")); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("SUPPORT created an initiative: %v", err)
	}

	// Invalid input is refused.
	bad := validInitiative("Bad Slug!")
	if _, err := s.Create(ctx, adminToken, bad); !errors.Is(err, ErrStaffInput) {
		t.Fatalf("bad slug accepted: %v", err)
	}

	// Create a valid DRAFT initiative.
	id, err := s.Create(ctx, adminToken, validInitiative("water-north"))
	if err != nil {
		t.Fatal(err)
	}
	assertInitiativeState(t, pool, id, "DRAFT")

	// Duplicate slug is rejected by the unique constraint.
	if _, err := s.Create(ctx, adminToken, validInitiative("water-north")); err == nil {
		t.Fatal("duplicate slug accepted")
	}

	// Update edits fields while DRAFT.
	upd := validInitiative("water-north")
	upd.Status = "COMPLETE"
	upd.CommittedMinor = 7_500_00
	if err := s.Update(ctx, adminToken, id, upd); err != nil {
		t.Fatal(err)
	}

	// Stage a metric WITHOUT verification, then prove it cannot be published.
	if err := s.SetMetric(ctx, adminToken, id, MetricInput{Position: 0, Label: "People served", Value: 1200, Unit: "PEOPLE"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishMetric(ctx, adminToken, id, 0); !errors.Is(err, ErrStaffInput) {
		t.Fatalf("unverified metric was published: %v", err)
	}

	// Add the verification note + date, then publishing succeeds.
	if err := s.SetMetric(ctx, adminToken, id, MetricInput{
		Position: 0, Label: "People served", Value: 1200, Unit: "PEOPLE",
		VerificationNote: "Confirmed against partner field report", VerifiedOn: "2026-06-01",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishMetric(ctx, adminToken, id, 0); err != nil {
		t.Fatalf("verified metric failed to publish: %v", err)
	}
	assertMetricState(t, pool, id, 0, "PUBLISHED")

	// Position out of range is refused.
	if err := s.SetMetric(ctx, adminToken, id, MetricInput{Position: 6, Label: "x", Value: 1, Unit: "PEOPLE"}); !errors.Is(
		err,
		ErrStaffInput,
	) {
		t.Fatalf("position 6 accepted (max is 0-5): %v", err)
	}

	// Publish the initiative; it becomes donor-visible with a published_at.
	if err := s.Publish(ctx, adminToken, id); err != nil {
		t.Fatal(err)
	}
	assertInitiativeState(t, pool, id, "PUBLISHED")

	// Delete the metric.
	if err := s.DeleteMetric(ctx, adminToken, id, 0); err != nil {
		t.Fatal(err)
	}
	var metrics int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_initiative_metrics WHERE initiative_id=$1", id).Scan(&metrics); err != nil ||
		metrics != 0 {
		t.Fatalf("metric not deleted: count=%d err=%v", metrics, err)
	}
}

func assertInitiativeState(t *testing.T, pool *pgxpool.Pool, id pgtype.UUID, want string) {
	t.Helper()
	var state string
	var publishedAt pgtype.Timestamptz
	if err := pool.QueryRow(context.Background(), "SELECT publish_state, published_at FROM donor_initiatives WHERE id=$1", id).Scan(&state, &publishedAt); err != nil {
		t.Fatal(err)
	}
	if state != want {
		t.Fatalf("initiative state = %q, want %q", state, want)
	}
	if (state == "PUBLISHED") != publishedAt.Valid {
		t.Fatalf("published_at validity %v disagrees with state %q", publishedAt.Valid, state)
	}
}

func assertMetricState(t *testing.T, pool *pgxpool.Pool, id pgtype.UUID, position int32, want string) {
	t.Helper()
	var state string
	if err := pool.QueryRow(context.Background(), "SELECT publish_state FROM donor_initiative_metrics WHERE initiative_id=$1 AND position=$2", id, position).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != want {
		t.Fatalf("metric state = %q, want %q", state, want)
	}
}

func TestInitiativeSpendByCategoryTotalsAndPublishedOnlyRead(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	cleanInitiatives(t, pool)
	_, adminToken := staffFixture(t, pool, "SUPER_ADMIN")
	s := InitiativeService{Staff: &StaffService{DB: api}}
	reader := InitiativeReader{DB: pool}

	id, err := s.Create(ctx, adminToken, validInitiative("spend-demo"))
	if err != nil {
		t.Fatal(err)
	}

	// Set two spend categories; upsert the first to prove it replaces, not appends.
	for _, in := range []SpendInput{
		{Category: "Direct aid", AmountMinor: 100_00},
		{Category: "Logistics", AmountMinor: 40_00},
		{Category: "Direct aid", AmountMinor: 150_00}, // upsert replaces 100_00
	} {
		if err := s.SetSpend(ctx, adminToken, id, in); err != nil {
			t.Fatal(err)
		}
	}

	// While DRAFT, the donor read returns 404-equivalent (not observable).
	if _, err := reader.Spend(ctx, id); !errors.Is(err, ErrInitiativeNotPublished) {
		t.Fatalf("draft initiative spend was readable: %v", err)
	}

	// Publish, then the read returns category totals + overall total.
	if err := s.Publish(ctx, adminToken, id); err != nil {
		t.Fatal(err)
	}
	got, err := reader.Spend(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalMinor != 190_00 {
		t.Fatalf("total = %d, want 19000 (150+40)", got.TotalMinor)
	}
	if len(got.Categories) != 2 {
		t.Fatalf("categories = %d, want 2 (upsert must not append)", len(got.Categories))
	}
	// Categories come back sorted; Direct aid should reflect the upserted 15000.
	if got.Categories[0].Category != "Direct aid" || got.Categories[0].AmountMinor != 150_00 {
		t.Fatalf("category[0] = %+v, want Direct aid=15000", got.Categories[0])
	}

	// Delete a category; total drops.
	if err := s.DeleteSpend(ctx, adminToken, id, "Logistics"); err != nil {
		t.Fatal(err)
	}
	got, err = reader.Spend(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalMinor != 150_00 || len(got.Categories) != 1 {
		t.Fatalf("after delete total=%d cats=%d, want 15000/1", got.TotalMinor, len(got.Categories))
	}

	// An unknown id reads as not-published, never 500.
	var missing pgtype.UUID
	_ = missing.Scan("00000000-0000-0000-0000-000000000000")
	if _, err := reader.Spend(ctx, missing); !errors.Is(err, ErrInitiativeNotPublished) {
		t.Fatalf("unknown id did not map to not-published: %v", err)
	}
}

// signedInDonor creates a donor and returns its id and a live session token.
func signedInDonor(t *testing.T, pool *pgxpool.Pool) (pgtype.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id, _ := donor(t, pool)
	token := link(t, pool, id, time.Now().UTC())
	var access string
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		tokens, _, err := claim(ctx, tx, token, Client{})
		if err != nil {
			return err
		}
		access = tokens.Access
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, access
}

func TestDailyRotationDeterministicAndExposureLedger(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	cleanInitiatives(t, pool)
	_, adminToken := staffFixture(t, pool, "SUPER_ADMIN")
	s := InitiativeService{Staff: &StaffService{DB: api}}
	rotation := DonorInitiativeService{DB: pool}

	donorID, donorToken := signedInDonor(t, pool)

	// No published initiatives -> clean not-found.
	if _, err := rotation.Today(ctx, donorToken); !errors.Is(err, ErrNoInitiativeToday) {
		t.Fatalf("empty rotation did not report no-initiative: %v", err)
	}

	// Publish three active initiatives, each with a verified headline metric.
	for i, slug := range []string{"rot-a", "rot-b", "rot-c"} {
		id, err := s.Create(ctx, adminToken, validInitiative(slug))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetMetric(ctx, adminToken, id, MetricInput{
			Position: 0, Label: "Served", Value: int64(100 * (i + 1)), Unit: "PEOPLE",
			VerificationNote: "field report", VerifiedOn: "2026-06-01",
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.PublishMetric(ctx, adminToken, id, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.Publish(ctx, adminToken, id); err != nil {
			t.Fatal(err)
		}
	}

	// Same donor, same day: deterministic, stable pick.
	first, err := rotation.Today(ctx, donorToken)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		again, err := rotation.Today(ctx, donorToken)
		if err != nil {
			t.Fatal(err)
		}
		if again.InitiativeID != first.InitiativeID {
			t.Fatalf("rotation not stable within a day: %s vs %s", again.InitiativeID, first.InitiativeID)
		}
	}

	// The chosen initiative carries its shown verified metric.
	if len(first.Metrics) != 1 || first.Metrics[0].Unit != "PEOPLE" {
		t.Fatalf("metrics not surfaced: %+v", first.Metrics)
	}

	// Repeat views are one distinct exposure (idempotent upsert).
	if first.DistinctShown != 1 {
		t.Fatalf("distinct_shown = %d, want 1", first.DistinctShown)
	}
	var rowCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_initiative_exposures WHERE donor_id=$1", donorID).Scan(&rowCount); err != nil ||
		rowCount != 1 {
		t.Fatalf("exposure rows = %d want 1 (upsert must not append): %v", rowCount, err)
	}

	// Across several donors the rotation is not constant.
	seen := map[string]bool{first.InitiativeID: true}
	for i := 0; i < 8; i++ {
		_, tok := signedInDonor(t, pool)
		got, err := rotation.Today(ctx, tok)
		if err != nil {
			t.Fatal(err)
		}
		seen[got.InitiativeID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("rotation appears constant across donors: only %d distinct", len(seen))
	}
}
