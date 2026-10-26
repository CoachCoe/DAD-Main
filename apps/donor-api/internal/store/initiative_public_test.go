package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"dad/donor-api/internal/dbroles"
)

// TestPublicInitiativesShowOnlyPublished covers the public initiative pages:
// they list published initiatives with their approved partner's name, and a
// draft reads as not found.
func TestPublicInitiativesShowOnlyPublished(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	_, admin := staffFixture(t, pool, "SUPER_ADMIN")
	s := InitiativeService{Staff: &StaffService{DB: as(t, dbroles.API)}}
	partner := approvedPartner(t, pool, "Public Wells", "PUBWELLS", 0, 0)
	cleanInitiatives(t, pool)
	in := validInitiative("public-wells")
	in.PartnerSignupID = partner.String()
	published, err := s.Create(ctx, admin, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(ctx, admin, published); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, admin, validInitiative("draft-wells")); err != nil {
		t.Fatal(err)
	}

	reader := InitiativeReader{DB: as(t, dbroles.API)}
	all, err := reader.Published(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *PublicInitiative
	for i := range all {
		if all[i].Slug == "draft-wells" {
			t.Fatal("a draft initiative is listed publicly")
		}
		if all[i].Slug == "public-wells" {
			found = &all[i]
		}
	}
	if found == nil || found.ID != published.String() || found.Partner == nil || *found.Partner != "Public Wells" ||
		found.StartedOn != "2026-03-01" {
		t.Fatalf("published initiative %+v", found)
	}
	if one, err := reader.PublishedBySlug(ctx, "public-wells"); err != nil || one.ID != found.ID || one.Partner == nil ||
		*one.Partner != *found.Partner {
		t.Fatalf("by slug: %+v, %v", one, err)
	}
	if _, err := reader.PublishedBySlug(ctx, "draft-wells"); !errors.Is(err, ErrInitiativeNotPublished) {
		t.Fatalf("draft by slug: %v; want ErrInitiativeNotPublished", err)
	}
}

// TestContentInitiativesMoveToTheEditor runs the migration that copies the
// spreadsheet's published initiatives into donor_initiatives, inside a
// transaction it rolls back.
func TestContentInitiativesMoveToTheEditor(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	migration, err := os.ReadFile("../../db/migrations/0061_initiatives_from_content.sql")
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := staffFixture(t, pool, "SUPER_ADMIN")
	approvedPartner(t, pool, "Sheet Partner", "SHEETPART", 0, 0)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	publication := testID(t)
	payload := `{"initiatives":[` +
		`{"slug":"sheet-wells","title":"Sheet Wells","summary":"Wells.","partner":"sheet partner","planned_start":"2027-04-01"},` +
		`{"slug":"sheet-farms","title":"Sheet Farms","summary":"Farms.","partner":"Nobody Yet","planned_start":"2027-05-01"}` +
		`],"ledger":[],"ledger_as_of":"2026-10-01","cadence":"monthly"}`
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO donor_content_publications(id,actor_id,payload,created_at) VALUES($1,$2,$3,now())", []any{publication, actor, payload}},
		{"INSERT INTO donor_content_head(id,publication_id) VALUES('r1',$1) ON CONFLICT(id) DO UPDATE SET publication_id=EXCLUDED.publication_id", []any{publication}},
		{"DELETE FROM donor_initiatives WHERE slug IN ('sheet-wells','sheet-farms')", nil},
	} {
		if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	reader := InitiativeReader{DB: tx}
	wells, err := reader.PublishedBySlug(ctx, "sheet-wells")
	if err != nil || wells.Partner == nil || *wells.Partner != "Sheet Partner" || wells.StartedOn != "2027-04-01" ||
		wells.Location != "To be announced" {
		t.Fatalf("copied initiative %+v, %v", wells, err)
	}
	farms, err := reader.PublishedBySlug(ctx, "sheet-farms")
	if err != nil || farms.Partner != nil {
		t.Fatalf("an initiative whose partner is not approved: %+v, %v", farms, err)
	}
	var rotating int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM donor_initiatives WHERE slug IN ('sheet-wells','sheet-farms') AND in_rotation").Scan(&rotating); err != nil ||
		rotating != 0 {
		t.Fatalf("%d planned initiatives joined the daily rotation, err %v", rotating, err)
	}
}
