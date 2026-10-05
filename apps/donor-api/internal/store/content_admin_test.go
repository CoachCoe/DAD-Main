package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSourceIntegrityAndAtomicPublication(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "ADMIN")
	_, support := staffFixture(t, pool, "SUPPORT")
	var previous pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT publication_id FROM donor_content_head WHERE id='r1'").Scan(&previous); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if previous.Valid {
			if _, err := pool.Exec(ctx, "UPDATE donor_content_head SET publication_id=$1 WHERE id='r1'", previous); err != nil {
				t.Error(err)
			}
		} else if _, err := pool.Exec(ctx, "DELETE FROM donor_content_head WHERE publication_id IN (SELECT id FROM donor_content_publications WHERE actor_id=$1)", actor); err != nil {
			t.Error(err)
		}
		for _, sql := range []string{"DELETE FROM donor_content_publications WHERE actor_id=$1", "DELETE FROM donor_content_sources WHERE actor_id=$1"} {
			if _, err := pool.Exec(ctx, sql, actor); err != nil {
				t.Error(err)
			}
		}
	})
	s := ContentService{Staff: &StaffService{DB: pool}, EncryptionKey: base64.URLEncoding.EncodeToString(make([]byte, 32))}
	data := []byte("Private signed source record")
	if _, err := s.StoreSource(ctx, support, "source.txt", data); !errors.Is(err, ErrStaffForbidden) {
		t.Fatal("support uploaded source", err)
	}
	source, err := s.StoreSource(ctx, token, "source.txt", data)
	if err != nil {
		t.Fatal(err)
	}
	id := source.ID
	var ciphertext []byte
	if err = pool.QueryRow(ctx, "SELECT encrypted_data FROM donor_content_sources WHERE id=$1", id).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, data) {
		t.Fatal("source not encrypted", err)
	}
	document, err := s.ReadSource(ctx, token, id)
	if err != nil || !bytes.Equal(document.Data, data) {
		t.Fatal("source roundtrip failed", err)
	}
	r := PublicationRequest{AsOf: "2026-01-01", Cadence: "Monthly", InitiativesCSV: fmt.Sprintf("slug,title,summary,partner,planned_start,board_approval_source_id,approved_on\nfirst,First,Planned initiative,Partner,2027-04-22,%s,2026-01-01\n", id.String()), LedgerCSV: fmt.Sprintf("metric,value,unit,source_id,illustrative\nCollected,3000,USD_CENTS,%s,false\n", id.String())}
	if _, err = s.Publish(ctx, token, r); err != nil {
		t.Fatal(err)
	}
	var published pgtype.UUID
	if err = pool.QueryRow(ctx, "SELECT publication_id FROM donor_content_head WHERE id='r1'").Scan(&published); err != nil {
		t.Fatal(err)
	}
	r.LedgerCSV = fmt.Sprintf("metric,value,unit,source_id,illustrative\nCollected,3000,USD_CENTS,%s,false\n", testID(t).String())
	if _, err = s.Publish(ctx, token, r); !errors.Is(err, ErrStaffInput) {
		t.Fatal("unbacked figure published", err)
	}
	var after pgtype.UUID
	if err = pool.QueryRow(ctx, "SELECT publication_id FROM donor_content_head WHERE id='r1'").Scan(&after); err != nil || after != published {
		t.Fatal("failed publication changed public snapshot", err)
	}
	if _, err = pool.Exec(ctx, "UPDATE donor_content_sources SET sha256=repeat('0',64) WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadSource(ctx, token, id); !errors.Is(err, ErrStaffConflict) {
		t.Fatal("corrupt source returned", err)
	}
}
