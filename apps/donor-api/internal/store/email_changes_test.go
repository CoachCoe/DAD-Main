package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/store/queries"
)

func changeFixture(t *testing.T) (EmailChangeService, EmailChangeWorker, pgtype.UUID, string, string, *recordingMail) {
	t.Helper()
	pool := database(t)
	id, email := donor(t, pool)
	accountCleanup(t, pool, id)
	session := claimProof(t, pool, link(t, pool, id, time.Now()))
	t.Cleanup(func() {
		for _, sql := range []string{"DELETE FROM donor_identity_events WHERE donor_id=$1", "DELETE FROM donor_email_changes WHERE donor_id=$1"} {
			if _, err := pool.Exec(context.Background(), sql, id); err != nil {
				t.Error(err)
			}
		}
	})
	transport := &recordingMail{}
	w := EmailChangeWorker{DB: pool, Settings: mail.Settings{Host: "smtp.fixture", Port: 587, Sender: "sender@example.org", EncryptionKey: emailTestKey}, Origin: "https://donor.example.org", Transport: transport}
	return EmailChangeService{DB: pool, EncryptionKey: emailTestKey}, w, id, email, session, transport
}

func TestEmailChangeRequiresBothAddressesAndRevokesAccess(t *testing.T) {
	s, w, id, old, session, transport := changeFixture(t)
	ctx := context.Background()
	newEmail := "new-" + id.String() + "@example.org"
	unusedLogin := link(t, s.DB, id, time.Now())
	if err := s.Request(ctx, session, newEmail); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.DeliverNext(ctx, time.Now()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if len(transport.messages) != 1 || transport.messages[0].To != old {
		t.Fatal("first proof was not sent to current address")
	}
	first := tokenFromMail(t, transport.messages[0])
	if stage, err := s.Confirm(ctx, first); err != nil || stage != "NEW" {
		t.Fatal(stage, err)
	}
	if _, err := s.Confirm(ctx, first); !errors.Is(err, ErrDenied) {
		t.Fatal("current proof replay accepted", err)
	}
	donor, err := Authenticated(ctx, queries.New(s.DB), session, time.Now())
	if err != nil || donor.Email != old {
		t.Fatal("identity changed before second confirmation", err)
	}
	if worked, err := w.DeliverNext(ctx, time.Now()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if len(transport.messages) != 2 || transport.messages[1].To != newEmail {
		t.Fatal("second proof sent to wrong address")
	}
	second := tokenFromMail(t, transport.messages[1])
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := s.Confirm(ctx, second); results <- err })
	}
	wg.Wait()
	close(results)
	succeeded, denied := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrDenied) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || denied != 1 {
		t.Fatal("confirmation was not single use")
	}
	if _, err = Authenticated(ctx, queries.New(s.DB), session, time.Now()); !errors.Is(err, ErrDenied) {
		t.Fatal("old session survived", err)
	}
	login, err := queries.New(s.DB).LoginLink(ctx, HashToken(unusedLogin))
	if err != nil || !login.ConsumedAt.Valid {
		t.Fatal("old sign-in proof survived", err)
	}
	donor, err = queries.New(s.DB).DonorByEmail(ctx, newEmail)
	if err != nil || donor.ID != id {
		t.Fatal("identity not preserved", err)
	}
	var events int
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM donor_identity_events WHERE donor_id=$1", id).Scan(&events); err != nil || events != 3 {
		t.Fatal("identity audit incomplete", err)
	}
}

func TestEmailChangeConflictsExpiryReplacementAndDeliveryRetry(t *testing.T) {
	s, w, id, old, session, transport := changeFixture(t)
	ctx := context.Background()
	_, occupied := donor(t, s.DB)
	if err := s.Request(ctx, "invalid", occupied); !errors.Is(err, ErrDenied) {
		t.Fatal("anonymous change allowed", err)
	}
	if err := s.Request(ctx, session, occupied); err != nil {
		t.Fatal("address ownership leaked before verification", err)
	}
	transport.failure = errors.New("lost acknowledgement")
	if _, err := w.DeliverNext(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	first := tokenFromMail(t, transport.messages[0])
	transport.failure = nil
	if _, err := w.DeliverNext(ctx, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(transport.messages) != 2 || tokenFromMail(t, transport.messages[1]) != first || transport.messages[0].ID != transport.messages[1].ID {
		t.Fatal("delivery retry changed proof or message ID")
	}
	if _, err := s.Confirm(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := w.DeliverNext(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Confirm(ctx, tokenFromMail(t, transport.messages[2])); !errors.Is(err, ErrEmailChangeConflict) {
		t.Fatal("existing donor was merged", err)
	}
	d, err := Authenticated(ctx, queries.New(s.DB), session, time.Now())
	if err != nil || d.Email != old {
		t.Fatal("conflict changed identity", err)
	}
	if err = s.Request(ctx, session, "fresh-"+id.String()+"@example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, tokenFromMail(t, transport.messages[2])); !errors.Is(err, ErrDenied) {
		t.Fatal("superseded request remained active", err)
	}
	if _, err = w.DeliverNext(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	last := tokenFromMail(t, transport.messages[len(transport.messages)-1])
	if _, err = s.DB.Exec(ctx, "UPDATE donor_email_changes SET expires_at=now()-interval '1 second' WHERE donor_id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, last); !errors.Is(err, ErrDenied) {
		t.Fatal("expired proof accepted", err)
	}
}
