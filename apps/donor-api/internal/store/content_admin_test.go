package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cleanSources removes actor's sources after the test.
func cleanSources(t *testing.T, pool *pgxpool.Pool, actor pgtype.UUID) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM donor_content_sources WHERE actor_id=$1", actor); err != nil {
			t.Error(err)
		}
	})
}

func TestSourceUploadsRequireEncryptionKey(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "SUPER_ADMIN")
	_, support := staffFixture(t, pool, "OP_PARTNERSHIPS")
	cleanSources(t, pool, actor)
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
}

func TestSourceStoredBeforeAKeyRotationStaysReadable(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "SUPER_ADMIN")
	cleanSources(t, pool, actor)
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

func TestSourceIntegrity(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, token := staffFixture(t, pool, "SUPER_ADMIN")
	_, support := staffFixture(t, pool, "OP_PARTNERSHIPS")
	cleanSources(t, pool, actor)
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
	if _, err = pool.Exec(ctx, "UPDATE donor_content_sources SET sha256=repeat('0',64) WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadSource(ctx, token, id); !errors.Is(err, ErrStaffConflict) {
		t.Fatal("corrupt source returned", err)
	}
}
