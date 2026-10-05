package store

import (
	"context"
	"testing"
	"time"

	"impactgraph/donor-api/internal/store/queries"
)

func TestQueueHealthCountsParkedAndIgnoresFutureAndCompleted(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// An isolated historical cutoff excludes other tests' committed queue rows.
	now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	id := testID(t)
	mustExec(t, tx, "INSERT INTO donors(id,email,created_at) VALUES($1,$2,$3)", id, id.String()+"@example.org", now)
	mustExec(t, tx, "INSERT INTO donor_metric_projections(id,donor_id,computed_at,next_refresh_at) VALUES($1,$2,$3,$3)", testID(t), id, now)
	for i, delta := range []time.Duration{-time.Hour, -time.Minute, time.Hour, -time.Hour} {
		job := testID(t)
		mustExec(t, tx, `INSERT INTO donor_messages(id,donor_id,kind,dedup_key,context,available_at,attempts,completed_at)
  VALUES($1,$2,'RECEIPT',$3,'{}',$4,$5,CASE WHEN $6 THEN $4::timestamptz ELSE NULL END)`, job, id, job.String(), now.Add(delta), i*5, i == 3)
	}
	rows, err := queries.New(tx).QueueHealth(ctx, timestamp(now))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if row.Queue == "messages" {
			if row.Due != 2 || row.Parked != 1 || row.OldestSeconds != 3600 {
				t.Fatal(row)
			}
		}
		if row.Queue == "projections" {
			if row.Due != 1 {
				t.Fatal(row)
			}
		}
	}
}
