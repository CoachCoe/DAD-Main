package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"impactgraph/donor-api/internal/dbroles"
)

func TestPublicContentUsesMigratedSchemaAndOmitsPrivateSources(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM donor_content_head WHERE id='r1'"); err != nil {
		t.Fatal(err)
	}
	reader := ContentReader{DB: tx}
	actAs(t, tx, dbroles.API)
	empty, err := reader.PublicContent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, "RESET ROLE")
	if empty.State != "unpublished" {
		t.Fatal("unpublished ledger must be explicit")
	}
	staffID, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO donor_staff (id,email,name,role) VALUES ($1,$2,'Fixture staff','ADMIN')", staffID, HashToken(string(staffID.Bytes[:]))+"@example.org"); err != nil {
		t.Fatal(err)
	}
	publicationID, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"initiatives":[{"slug":"first","title":"Plan","summary":"Planned","partner":"Partner","planned_start":"2027-04-22","board_approval_source_id":"private-approval","approved_on":"2026-01-01"}],"ledger":[{"metric":"Example","value":3000,"unit":"USD_CENTS","illustrative":true,"source_id":"private-source"}],"ledger_as_of":"2026-01-01","cadence":"monthly"}`
	if _, err = tx.Exec(ctx, "INSERT INTO donor_content_publications (id,actor_id,payload) VALUES ($1,$2,$3::json)", publicationID, staffID, payload); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO donor_content_head (id,publication_id) VALUES ('r1',$1)", publicationID); err != nil {
		t.Fatal(err)
	}
	actAs(t, tx, dbroles.API)
	result, err := reader.PublicContent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "published" || !strings.Contains(string(encoded), `"illustrative":true`) {
		t.Fatalf("wrong publication: %s", encoded)
	}
	for _, private := range []string{"private-approval", "private-source", "board_approval_source_id", "approved_on", "source_id"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("leaked %s", private)
		}
	}
}
