package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
)

func TestInitiativeApprovalIsRecheckedAfterConcurrentChanges(t *testing.T) {
	for _, action := range []string{"publish", "remove approval"} {
		t.Run(action, func(t *testing.T) {
			pool := database(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cleanInitiatives(t, pool)
			_, token := staffFixture(t, pool, "SUPER_ADMIN")
			service := InitiativeService{Staff: &StaffService{DB: as(t, dbroles.API)}}
			input := withBoardApproval(t, pool, validInitiative("approval-race"))
			id, err := service.Create(ctx, token, input)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pending.Rollback(ctx)
			if action == "publish" {
				mustExec(t, pending, "UPDATE donor_initiatives SET board_approval_source_id=NULL,board_approved_on=NULL WHERE id=$1", id)
			} else {
				mustExec(t, pending, "UPDATE donor_initiatives SET publish_state='PUBLISHED',published_at=now() WHERE id=$1", id)
			}
			result := make(chan error, 1)
			go func() {
				if action == "publish" {
					result <- service.Publish(ctx, token, id)
					return
				}
				input.BoardApprovalSourceID, input.BoardApprovedOn = "", ""
				result <- service.Update(ctx, token, id, input)
			}()
			for {
				var blocked bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
					WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0
					AND query LIKE '%donor_initiatives%')`).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("initiative change bypassed the pending write: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			if err := pending.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, ErrBoardApprovalRequired) {
				t.Fatalf("%s after concurrent change = %v, want board approval required", action, err)
			}
			var unapprovedPublication bool
			if err := pool.QueryRow(ctx, `SELECT publish_state='PUBLISHED' AND board_approval_source_id IS NULL
				FROM donor_initiatives WHERE id=$1`, id).Scan(&unapprovedPublication); err != nil || unapprovedPublication {
				t.Fatalf("published without approval=%t: %v", unapprovedPublication, err)
			}
		})
	}
}
