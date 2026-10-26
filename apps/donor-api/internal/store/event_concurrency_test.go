package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"dad/donor-api/internal/dbroles"
)

func TestCheckInSerializesWithEventChanges(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "QR"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			pool := database(t)
			api := as(t, dbroles.API)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cleanEvents(t, pool)
			_, staff := staffFixture(t, pool, "SUPER_ADMIN")
			events := EventService{Staff: &StaffService{DB: api}}
			eventID, err := events.Create(ctx, staff, eventInput("concurrent-checkin", "SF", "94103", "VOLUNTEER", -time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if err := events.Publish(ctx, staff, eventID); err != nil {
				t.Fatal(err)
			}
			donorID, token := signedInDonor(t, pool)
			t.Cleanup(func() { cleanExec(t, "DELETE FROM donor_badges WHERE donor_id=$1", donorID) })
			mustExec(t, pool, `INSERT INTO donor_event_reservations(id,event_id,donor_id,status,reserved_at,created_at,updated_at)
				VALUES(gen_random_uuid(),$1,$2,'RESERVED',now(),now(),now())`, eventID, donorID)
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback(ctx)
			mustExec(t, lock, "UPDATE donor_events SET cancelled_at=now() WHERE id=$1", eventID)
			result := make(chan error, 1)
			go func() {
				if manual {
					result <- events.CheckInAttendee(ctx, staff, eventID, donorID)
					return
				}
				_, err := (ReservationService{DB: api, CheckInSecret: "fixture"}).CheckIn(ctx, token, checkInToken(eventID, "fixture"))
				result <- err
			}()
			for {
				select {
				case err := <-result:
					t.Fatalf("check-in bypassed the pending event change: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				default:
				}
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
					WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if err := lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-result
			if !errors.Is(err, ErrCheckIn) && !(manual && errors.Is(err, ErrEventCancelled)) {
				t.Fatalf("cancelled event check-in = %v", err)
			}
			var attended bool
			if err := pool.QueryRow(ctx, "SELECT checked_in_at IS NOT NULL FROM donor_event_reservations WHERE event_id=$1 AND donor_id=$2", eventID, donorID).Scan(&attended); err != nil ||
				attended {
				t.Fatalf("cancelled event recorded attendance=%t: %v", attended, err)
			}
		})
	}
}

func TestJoinTeamDuringConcurrentCreation(t *testing.T) {
	pool := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	partner := approvedPartner(t, pool, "Team Race", "TEAM-RACE", 0, 0)
	donor, token := signedInDonor(t, pool)
	mustExec(t, pool, "INSERT INTO donor_partner_employees(donor_id,partner_id,enrolled_at) VALUES($1,$2,now())", donor, partner)
	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	team := mustNewID(t)
	mustExec(t, lock, "INSERT INTO donor_partner_teams(id,partner_id,name,created_at) VALUES($1,$2,'Community',now())", team, partner)
	type outcome struct {
		id  string
		err error
	}
	result := make(chan outcome, 1)
	api := as(t, dbroles.API)
	go func() {
		id, err := (EmployeeService{DB: api}).JoinTeam(ctx, token, "Community")
		result <- outcome{id, err}
	}()
	awaitPartnerWaiters(t, pool, 1)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-result
	if got.err != nil || got.id != team.String() {
		t.Fatalf("concurrent team join = %q, %v; want %s", got.id, got.err, team.String())
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_partner_teams WHERE partner_id=$1", partner).Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("team count=%d: %v", count, err)
	}
}
