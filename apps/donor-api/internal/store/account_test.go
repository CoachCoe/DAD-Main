package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/store/queries"
)

func accountCleanup(t *testing.T, pool *pgxpool.Pool, id pgtype.UUID) {
	t.Helper()
	t.Cleanup(func() {
		for _, sql := range []string{"DELETE FROM donor_messages WHERE donor_id=$1", "DELETE FROM donor_metric_projections WHERE donor_id=$1", "DELETE FROM donor_login_emails WHERE donor_id=$1", "DELETE FROM donor_gifts WHERE donor_id=$1", "DELETE FROM donor_checkouts WHERE donor_id=$1"} {
			if _, err := pool.Exec(context.Background(), sql, id); err != nil {
				t.Error(err)
			}
		}
	})
}
func claimProof(t *testing.T, pool *pgxpool.Pool, proof string) string {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	token, _, err := ClaimInTx(ctx, tx, proof)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return token
}
func TestClaimRefreshesAndReplacesOnlyThisBrowser(t *testing.T) {
	pool := database(t)
	id, _ := donor(t, pool)
	accountCleanup(t, pool, id)
	ctx := context.Background()
	now := time.Now().UTC()
	old := claimProof(t, pool, link(t, pool, id, now))
	other := claimProof(t, pool, link(t, pool, id, now))
	proof := link(t, pool, id, now)
	service := AccountService{DB: pool}
	token, err := service.ClaimAccount(ctx, proof, old)
	if err != nil {
		t.Fatal(err)
	}
	q := queries.New(pool)
	for _, session := range []string{token, other} {
		if _, err = Authenticated(ctx, q, session, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = Authenticated(ctx, q, old, time.Now()); !errors.Is(err, ErrDenied) {
		t.Fatal("old browser session survived")
	}
	projection, err := q.Projection(ctx, id)
	if err != nil || !projection.CurrentStreak.Valid || projection.CurrentStreak.Int32 != 0 || projection.NetMinor.Int64 != 0 {
		t.Fatal("claim did not atomically initialize recognition")
	}
	if _, err = service.ClaimAccount(ctx, proof, token); !errors.Is(err, ErrDenied) {
		t.Fatal("proof reused")
	}
}
func TestClaimProjectionFailureRollsBackProofAndSession(t *testing.T) {
	pool := database(t)
	id, _ := donor(t, pool)
	accountCleanup(t, pool, id)
	ctx := context.Background()
	now := time.Now().UTC()
	proof := link(t, pool, id, now)
	checkout, gift := testID(t), testID(t)
	if _, err := pool.Exec(ctx, `INSERT INTO donor_checkouts (id,donor_id,amount_minor,currency,frequency) VALUES ($1,$2,3000,'USD','ONE_TIME')`, checkout, id); err != nil {
		t.Fatal(err)
	}
	// Corrupt historical facts must not leave a half-claimed account behind.
	if _, err := pool.Exec(ctx, `INSERT INTO donor_gifts (id,donor_id,checkout_id,stripe_object_id,amount_minor,currency,frequency,paid_at) VALUES ($1,$2,$3,$4,3000,'USD','BROKEN',$5)`, gift, id, checkout, "pi_"+gift.String(), now); err != nil {
		t.Fatal(err)
	}
	service := AccountService{DB: pool}
	if token, err := service.ClaimAccount(ctx, proof, ""); err == nil || token != "" {
		t.Fatal("failed projection exposed session")
	}
	var claimed *time.Time
	var count int
	if err := pool.QueryRow(ctx, "SELECT claimed_at FROM donors WHERE id=$1", id).Scan(&claimed); err != nil || claimed != nil {
		t.Fatal("failed claim persisted")
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_sessions WHERE donor_id=$1", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed claim inserted session")
	}
	if _, err := pool.Exec(ctx, "UPDATE donor_gifts SET frequency='ONE_TIME' WHERE id=$1", gift); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimAccount(ctx, proof, ""); err != nil {
		t.Fatalf("rolled-back proof unusable: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_messages WHERE donor_id=$1 AND kind='RECEIPT'", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("gift receipt not queued with claim")
	}
}
func TestLoginQueueDeduplicatesConcurrentRequests(t *testing.T) {
	pool := database(t)
	id, email := donor(t, pool)
	accountCleanup(t, pool, id)
	service := AccountService{DB: pool}
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- service.QueueLogin(ctx, email) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_login_emails WHERE donor_id=$1", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("email queue was not throttled under donor lock")
	}
	if err := service.QueueLogin(ctx, "unknown-"+id.String()+"@example.org"); err != nil {
		t.Fatal("unknown account distinguishable")
	}
	if _, err := pool.Exec(ctx, "UPDATE donors SET disabled=true WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := service.QueueLogin(ctx, email); err != nil {
		t.Fatal("disabled account distinguishable")
	}
}
