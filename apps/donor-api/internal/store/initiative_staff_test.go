package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5"
)

func TestStaffInitiativeReadsAndRotationExclusion(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	cleanInitiatives(t, pool)
	_, adminToken := staffFixture(t, pool, "SUPER_ADMIN")
	_, growth := staffFixture(t, pool, "OP_GROWTH")
	s := InitiativeService{Staff: &StaffService{DB: as(t, dbroles.API)}}
	rotation := DonorInitiativeService{DB: pool}
	_, donorToken := signedInDonor(t, pool)

	shown, err := s.Create(ctx, adminToken, withBoardApproval(t, pool, validInitiative("rot-shown")))
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := s.Create(ctx, adminToken, withBoardApproval(t, pool, validInitiative("rot-excluded")))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMetric(ctx, adminToken, excluded, MetricInput{Position: 2, Label: "Wells", Value: 4, Unit: "INITIATIVES"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSpend(ctx, adminToken, excluded, SpendInput{Category: "Drilling", AmountMinor: 900_00}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.List(ctx, growth, 0); !errors.Is(err, ErrStaffForbidden) {
		t.Fatalf("OP_GROWTH listed initiatives: %v", err)
	}
	page, err := s.List(ctx, adminToken, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(
		page.Initiatives,
		func(i StaffInitiative) bool { return i.ID == excluded.String() && i.PublishState == "DRAFT" },
	) {
		t.Fatalf("draft initiative not listed: %+v", page.Initiatives)
	}
	detail, err := s.Detail(ctx, adminToken, excluded)
	if err != nil {
		t.Fatal(err)
	}
	if detail.StartedOn != "2026-03-01" || !detail.InRotation || len(detail.Metrics) != 1 || detail.Metrics[0].PublishState != "DRAFT" ||
		len(detail.Spend) != 1 || detail.Spend[0].AmountMinor != 900_00 {
		t.Fatalf("detail: %+v", detail)
	}
	if _, err := s.Detail(ctx, adminToken, mustNewID(t)); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unknown initiative: %v", err)
	}

	if err := s.Publish(ctx, adminToken, shown); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, adminToken, excluded); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRotation(ctx, adminToken, excluded, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRotation(ctx, adminToken, mustNewID(t), false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rotation of an unknown initiative: %v", err)
	}
	for range 6 {
		_, token := signedInDonor(t, pool)
		today, err := rotation.Today(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if today.InitiativeID != shown.String() {
			t.Fatalf("rotation showed %s, an excluded initiative", today.InitiativeID)
		}
	}
	today, err := rotation.Today(ctx, donorToken)
	if err != nil {
		t.Fatal(err)
	}
	if today.Location != "Northern region" || today.Type != "WATER" || today.StartedOn != "2026-03-01" {
		t.Fatalf("today's card lacks A-2 fields: %+v", today)
	}
}
