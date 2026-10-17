// Package dbrolestest runs code under test with exactly a process's database
// privileges, so a privilege the process lacks fails the test as it would
// fail in production.
package dbrolestest

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
	"github.com/jackc/pgx/v5/pgxpool"
)

// URL returns the test database's URL acting as role. The test user, a
// superuser, assumes the role as the session starts, so every statement is
// checked against the role's privileges alone.
func URL(t testing.TB, role dbroles.Role) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if location, err := url.Parse(dsn); err != nil || (location.Path != "/donor_go" && !strings.HasPrefix(location.Path, "/donor_go_")) {
		t.Fatal("TEST_DATABASE_URL must name a migrated donor_go test database")
	}
	return As(t, dsn, role)
}

// As returns the database URL dsn acting as role.
func As(t testing.TB, dsn string, role dbroles.Role) string {
	t.Helper()
	location, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	query.Set("role", role.Name)
	location.RawQuery = query.Encode()
	return location.String()
}

// Open opens a pool of role's size acting as role, closed when the test ends.
func Open(t testing.TB, role dbroles.Role) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(URL(t, role))
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = role.Pool
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
