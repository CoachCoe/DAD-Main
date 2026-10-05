package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/migrations"
	"impactgraph/donor-api/internal/store/queries"
)

func database(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is required; use the migrated donor_go test database")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	if config.ConnConfig.Database != "donor_go" {
		t.Fatal("integration tests require the dedicated donor_go database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err = migrations.Verify(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
func donor(t *testing.T, pool *pgxpool.Pool) (pgtype.UUID, string) {
	t.Helper()
	ctx := context.Background()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	email := HashToken(string(id.Bytes[:])) + "@example.org"
	if err = queries.New(pool).InsertGuest(ctx, queries.InsertGuestParams{ID: id, Email: email}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{"DELETE FROM donor_sessions WHERE donor_id=$1", "DELETE FROM donor_login_links WHERE donor_id=$1", "DELETE FROM donor_metric_projections WHERE donor_id=$1", "DELETE FROM donors WHERE id=$1"} {
			if _, err := pool.Exec(ctx, query, id); err != nil {
				t.Error(err)
			}
		}
	})
	return id, email
}
func link(t *testing.T, pool *pgxpool.Pool, id pgtype.UUID, now time.Time) string {
	t.Helper()
	key, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	token := HashToken(string(key.Bytes[:]))
	_, err = pool.Exec(context.Background(), "INSERT INTO donor_login_links (id,donor_id,token_hash,expires_at) VALUES ($1,$2,$3,$4)", key, id, HashToken(token), now.Add(15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func TestClaimIsSingleUseUnderConcurrency(t *testing.T) {
	pool := database(t)
	id, _ := donor(t, pool)
	now := time.Now().UTC()
	token := link(t, pool, id, now)
	sibling := link(t, pool, id, now)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	sessions := make(chan string, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ctx := context.Background()
			tx, err := pool.Begin(ctx)
			if err != nil {
				results <- err
				return
			}
			defer tx.Rollback(ctx)
			session, _, err := ClaimInTx(ctx, tx, token, now)
			if err == nil {
				err = tx.Commit(ctx)
				if err == nil {
					sessions <- session
				}
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(sessions)
	successes, denied := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrDenied) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || denied != 1 {
		t.Fatalf("success=%d denied=%d", successes, denied)
	}
	for session := range sessions {
		got, err := Authenticated(context.Background(), queries.New(pool), session, now)
		if err != nil || got.ID != id {
			t.Fatalf("authentication: %v", err)
		}
		if len(session) != 43 {
			t.Fatalf("token encoding changed")
		}
		if _, err = Authenticated(context.Background(), queries.New(pool), session, now.Add(30*24*time.Hour)); !errors.Is(err, ErrDenied) {
			t.Fatal("session expiry boundary")
		}
	}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, _, err = ClaimInTx(context.Background(), tx, sibling, now); !errors.Is(err, ErrDenied) {
		t.Fatalf("sibling remains usable: %v", err)
	}
}
func TestClaimRollbackLeavesProofUsable(t *testing.T) {
	pool := database(t)
	id, _ := donor(t, pool)
	now := time.Now().UTC()
	token := link(t, pool, id, now)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := ClaimInTx(ctx, tx, token, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = Authenticated(ctx, queries.New(pool), session, now); !errors.Is(err, ErrDenied) {
		t.Fatalf("rolled back session: %v", err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, _, err = ClaimInTx(ctx, tx, token, now); err != nil {
		t.Fatal(err)
	}
}
func TestExistingTokenHashEncoding(t *testing.T) {
	if HashToken("test") != "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08" {
		t.Fatal("invalid token hash")
	}
}
func TestDisabledDonorCannotClaim(t *testing.T) {
	pool := database(t)
	id, _ := donor(t, pool)
	now := time.Now().UTC()
	token := link(t, pool, id, now)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE donors SET disabled=true WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, _, err = ClaimInTx(ctx, tx, token, now); !errors.Is(err, ErrDenied) {
		t.Fatalf("disabled donor claimed: %v", err)
	}
}
func TestExpiredProofCannotCreateSession(t *testing.T) {
	pool := database(t)
	id, _ := donor(t, pool)
	now := time.Now().UTC()
	token := link(t, pool, id, now.Add(-15*time.Minute))
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, _, err = ClaimInTx(ctx, tx, token, now); !errors.Is(err, ErrDenied) {
		t.Fatalf("expiry boundary: %v", err)
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM donor_sessions WHERE donor_id=$1", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("expired proof created a session")
	}
}
