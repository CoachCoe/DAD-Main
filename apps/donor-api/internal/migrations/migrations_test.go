package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/db"
)

func isolatedDatabase(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil || (config.ConnConfig.Database != "donor_go" && !strings.HasPrefix(config.ConnConfig.Database, "donor_go_")) {
		t.Fatal("TEST_DATABASE_URL must name the dedicated donor_go test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var suffix [8]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "donor_migration_test_" + hex.EncodeToString(suffix[:])
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, finish := context.WithTimeout(context.Background(), 10*time.Second)
		defer finish()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+identifier+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	config = config.Copy()
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func TestManifestRejectsMisnamedMigrations(t *testing.T) {
	if _, err := manifest(db.Migrations); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"1_r1.sql", "0003_Mixed.sql", "0003-dash.sql", "0003_.sql", "00003_r1.sql", "0003 r1.sql"} {
		fsys := fstest.MapFS{"migrations/0001_r1.sql": {}, "migrations/" + name: {}}
		if _, err := manifest(fsys); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}

func TestFreshMigrationIdempotenceAndHistoryIntegrity(t *testing.T) {
	pool, ctx := isolatedDatabase(t)
	if err := Verify(ctx, pool); err == nil {
		t.Fatal("empty database reported ready")
	}
	for range 2 {
		if err := Apply(ctx, pool, false); err != nil {
			t.Fatal(err)
		}
		if err := Verify(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE donor_schema_migrations SET checksum='tampered'"); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, pool, false); err == nil {
		t.Fatal("tampered checksum accepted")
	}
	if err := Verify(ctx, pool); err == nil {
		t.Fatal("tampered history reported ready")
	}
}

func TestAdoptionPreservesExistingRecordsAndRejectsDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "drifted"}[drift], func(t *testing.T) {
			pool, ctx := isolatedDatabase(t)
			files, err := manifest(db.Migrations)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, files[0].sql); err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, "CREATE TABLE alembic_version(version_num text); INSERT INTO alembic_version VALUES('0030_donor_public_content'); CREATE TABLE legacy_fixture(value text); INSERT INTO legacy_fixture VALUES('preserve'); INSERT INTO donors(id,email) VALUES('00000000-0000-4000-8000-000000000001','preserve@example.org')"); err != nil {
				t.Fatal(err)
			}
			if err = Apply(ctx, pool, false); !errors.Is(err, ErrAdoptionRequired) {
				t.Fatalf("implicit adoption: %v", err)
			}
			if drift {
				if _, err = pool.Exec(ctx, "ALTER TABLE donors ADD COLUMN unexpected text"); err != nil {
					t.Fatal(err)
				}
				if err = Apply(ctx, pool, true); !errors.Is(err, ErrSchemaMismatch) {
					t.Fatalf("schema drift accepted: %v", err)
				}
				if err = Verify(ctx, pool); err == nil {
					t.Fatal("rejected adoption reported ready")
				}
			} else {
				if err = Apply(ctx, pool, true); err != nil {
					t.Fatal(err)
				}
				if err = Verify(ctx, pool); err != nil {
					t.Fatal(err)
				}
			}
			var email, legacy string
			if err = pool.QueryRow(ctx, "SELECT email FROM donors").Scan(&email); err != nil || email != "preserve@example.org" {
				t.Fatal("donor changed", err)
			}
			if err = pool.QueryRow(ctx, "SELECT value FROM legacy_fixture").Scan(&legacy); err != nil || legacy != "preserve" {
				t.Fatal("legacy data changed", err)
			}
			var leaked bool
			if err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='dad_baseline_validation')").Scan(&leaked); err != nil || leaked {
				t.Fatal("validation schema leaked", err)
			}
		})
	}
}
