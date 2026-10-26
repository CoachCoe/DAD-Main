package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"dad/donor-api/internal/dbroles"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// restoreContent removes actor's sources and publications and restores the
// public snapshot that was current before the test.
func restoreContent(t *testing.T, pool *pgxpool.Pool, actor pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	var previous pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT publication_id FROM donor_content_head WHERE id='r1'").Scan(&previous); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
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
}

func TestSourceUploadsRequireEncryptionKeyButPublishingDoesNot(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "SUPER_ADMIN")
	_, support := staffFixture(t, pool, "OP_PARTNERSHIPS")
	restoreContent(t, pool, actor)
	s := ContentService{Staff: &StaffService{DB: api}}
	if err := s.AuthorizeSourceUpload(ctx, support); !errors.Is(err, ErrStaffForbidden) {
		t.Fatal("unauthorized caller learned storage configuration", err)
	}
	if err := s.AuthorizeSourceUpload(ctx, token); !errors.Is(err, ErrSourceStorageUnconfigured) {
		t.Fatal("upload authorized without an encryption key", err)
	}
	if _, err := s.StoreSource(ctx, token, "source.txt", []byte("Private signed source record")); !errors.Is(
		err,
		ErrSourceStorageUnconfigured,
	) {
		t.Fatal("source stored without an encryption key", err)
	}
	var stored int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_content_sources WHERE actor_id=$1", actor).Scan(&stored); err != nil ||
		stored != 0 {
		t.Fatal("unencrypted source persisted", stored, err)
	}
	if err := s.Authorize(ctx, token); err != nil {
		t.Fatal("publishing authorization needs the source key", err)
	}
	source := testID(t)
	if err := queries.New(pool).InsertContentSource(ctx, queries.InsertContentSourceParams{ID: source, ActorID: actor, Name: "approval.pdf", Sha256: strings.Repeat("0", 64), EncryptedData: []byte("ciphertext")}); err != nil {
		t.Fatal(err)
	}
	r := PublicationRequest{
		AsOf:      "2026-01-01",
		Cadence:   "Monthly",
		LedgerCSV: fmt.Sprintf("metric,value,unit,source_id,illustrative\nCollected,3000,USD_CENTS,%s,false\n", source.String()),
	}
	if _, err := s.Publish(ctx, token, r); err != nil {
		t.Fatal("publishing failed without the source key", err)
	}
}

func TestSourceStoredBeforeAKeyRotationStaysReadable(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "SUPER_ADMIN")
	restoreContent(t, pool, actor)
	old := base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	s := ContentService{Staff: &StaffService{DB: api}, EncryptionKeys: keyring(t, old)}
	data := []byte("Private signed source record")
	source, err := s.StoreSource(ctx, token, "source.txt", data)
	if err != nil {
		t.Fatal(err)
	}
	s.EncryptionKeys = keyring(t, base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))+","+old)
	if document, err := s.ReadSource(ctx, token, source.ID); err != nil || !bytes.Equal(document.Data, data) {
		t.Fatal("source unreadable after prepending a key", err)
	}
}

func TestSourceIntegrityAndAtomicPublication(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "SUPER_ADMIN")
	_, support := staffFixture(t, pool, "OP_PARTNERSHIPS")
	restoreContent(t, pool, actor)
	s := ContentService{Staff: &StaffService{DB: api}, EncryptionKeys: keyring(t, base64.URLEncoding.EncodeToString(make([]byte, 32)))}
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
	if err = pool.QueryRow(ctx, "SELECT encrypted_data FROM donor_content_sources WHERE id=$1", id).Scan(&ciphertext); err != nil ||
		bytes.Contains(ciphertext, data) {
		t.Fatal("source not encrypted", err)
	}
	document, err := s.ReadSource(ctx, token, id)
	if err != nil || !bytes.Equal(document.Data, data) {
		t.Fatal("source roundtrip failed", err)
	}
	r := PublicationRequest{
		AsOf:      "2026-01-01",
		Cadence:   "Monthly",
		LedgerCSV: fmt.Sprintf("metric,value,unit,source_id,illustrative\nCollected,3000,USD_CENTS,%s,false\n", id.String()),
	}
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
	if err = pool.QueryRow(ctx, "SELECT publication_id FROM donor_content_head WHERE id='r1'").Scan(&after); err != nil ||
		after != published {
		t.Fatal("failed publication changed public snapshot", err)
	}
	if _, err = pool.Exec(ctx, "UPDATE donor_content_sources SET sha256=repeat('0',64) WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadSource(ctx, token, id); !errors.Is(err, ErrStaffConflict) {
		t.Fatal("corrupt source returned", err)
	}
}

func TestPublicationErrorsLocateEachProblem(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "SUPER_ADMIN")
	restoreContent(t, pool, actor)
	s := ContentService{Staff: &StaffService{DB: api}}
	source := testID(t)
	if err := queries.New(pool).InsertContentSource(ctx, queries.InsertContentSourceParams{ID: source, ActorID: actor, Name: "approval.pdf", Sha256: strings.Repeat("0", 64), EncryptedData: []byte("ciphertext")}); err != nil {
		t.Fatal(err)
	}
	const ledgerHeader = "metric,value,unit,source_id,illustrative\n"
	for _, c := range []struct {
		name string
		rows string
		want PublicationProblem
	}{
		{"bad header", "metric,value\n", PublicationProblem{Sheet: "ledger", Row: 1, Reason: "invalid_header"}},
		{"bad unit", ledgerHeader + "Collected,3000,EUROS," + source.String() + ",false\n", PublicationProblem{Sheet: "ledger", Row: 2, Column: "unit", Reason: "invalid_unit"}},
		{"unknown source", ledgerHeader + "Collected,3000,USD_CENTS," + testID(t).String() + ",false\n", PublicationProblem{Sheet: "ledger", Row: 2, Column: "source_id", Reason: "unknown_source"}},
		{"NUL byte", ledgerHeader + "Collected\x00,3000,USD_CENTS," + source.String() + ",false\n", PublicationProblem{Sheet: "ledger", Reason: "invalid_text"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Publish(
				ctx,
				token,
				PublicationRequest{AsOf: "2026-01-01", Cadence: "Monthly", LedgerCSV: c.rows},
			)
			if !errors.Is(err, ErrStaffInput) {
				t.Fatalf("got %v, want an ErrStaffInput", err)
			}
			problems, ok := errors.AsType[*PublicationError](err)
			if !ok || len(problems.Problems) != 1 || problems.Problems[0] != c.want {
				t.Fatalf("got %+v, want only %+v", problems, c.want)
			}
		})
	}
}
