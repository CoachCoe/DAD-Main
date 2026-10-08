package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/store/queries"
)

func notificationWorker(f *workerFixture, transport *recordingMail) MessageWorker {
	return MessageWorker{DB: f.service.DB, Settings: mail.Settings{Host: "smtp.fixture", Port: 587, Sender: "sender@example.org", EncryptionKeys: emailTestKeys}, Origin: "https://donor.example.org", Transport: transport}
}
func TestReceiptMessagePersistsBodyBeforeSendAndRetriesUnchanged(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	ctx := context.Background()
	f.receive(t, "invoice.payment_succeeded", invoice(result.CheckoutID, "in_"+result.CheckoutID, time.Now().Add(-24*time.Hour), true))
	f.process(t)
	transport := &recordingMail{failure: errors.New("lost receipt acknowledgement")}
	w := notificationWorker(f, transport)
	now := time.Now().Add(time.Second)
	transport.check = func(m mail.Message) {
		var saved string
		if err := w.DB.QueryRow(ctx, "SELECT body FROM donor_messages WHERE donor_id=$1 AND kind='RECEIPT'", f.donorID).Scan(&saved); err != nil || saved != m.Body {
			t.Fatal("uncommitted rendering sent", err)
		}
	}
	if worked, err := w.DeliverNext(ctx, now); err != nil || !worked {
		t.Fatal(worked, err)
	}
	// A later refund changes an on-demand receipt, but not a retry of the already
	// issued message. A lost SMTP acknowledgement must preserve message identity.
	refund := testID(t)
	if _, err := w.DB.Exec(ctx, `INSERT INTO donor_refunds(id,gift_id,stripe_refund_id,amount_minor,currency,status,observed_at) SELECT $1,id,$2,3000,'USD','SUCCEEDED',$3 FROM donor_gifts WHERE donor_id=$4`, refund, "re_"+refund.String(), now, f.donorID); err != nil {
		t.Fatal(err)
	}
	transport.failure = nil
	if worked, err := w.DeliverNext(ctx, now.Add(time.Minute)); err != nil || worked {
		t.Fatal("backoff ignored", worked, err)
	}
	if worked, err := w.DeliverNext(ctx, now.Add(2*time.Minute)); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if len(transport.messages) != 2 || transport.messages[0] != transport.messages[1] || !strings.Contains(transport.messages[0].Body, "Net test amount: $30.00") {
		t.Fatal("message changed across retry")
	}
}
func TestRecoveryMessagesSuppressObsoleteAndRecoveredReminders(t *testing.T) {
	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	ctx := context.Background()
	now := time.Now().UTC()
	due := now.Add(-8 * 24 * time.Hour)
	event := f.receive(t, "invoice.payment_failed", invoice(result.CheckoutID, "in_"+result.CheckoutID, due, false))
	f.process(t)
	assertEventProcessed(t, f, event, true)
	transport := &recordingMail{}
	w := notificationWorker(f, transport)
	for i := 0; i < 2; i++ {
		if worked, err := w.DeliverNext(ctx, now.Add(time.Second)); err != nil || !worked {
			t.Fatal(worked, err)
		}
	}
	if len(transport.messages) != 1 || !strings.Contains(transport.messages[0].Body, "streak is at risk") {
		t.Fatal("wrong reminder delivery")
	}
	var reason string
	if err := w.DB.QueryRow(ctx, "SELECT suppressed_reason FROM donor_messages WHERE donor_id=$1 AND context->>'day'='0'", f.donorID).Scan(&reason); err != nil || reason != "newer_reminder_due" {
		t.Fatal(reason, err)
	}
	if _, err := w.DB.Exec(ctx, "UPDATE donor_billing_periods SET status='PAID' WHERE donor_id=$1", f.donorID); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.DeliverNext(ctx, due.Add(13*24*time.Hour+time.Second)); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if len(transport.messages) != 1 {
		t.Fatal("recovered gift received another warning")
	}
	if err := w.DB.QueryRow(ctx, "SELECT suppressed_reason FROM donor_messages WHERE donor_id=$1 AND context->>'day'='13'", f.donorID).Scan(&reason); err != nil || reason != "payment_recovered" {
		t.Fatal(reason, err)
	}
}
func TestReferralSuppressionRechecksPreferencesAfterRendering(t *testing.T) {
	f := newWorkerFixture(t, "ONE_TIME")
	ctx := context.Background()
	now := time.Now()
	referred, _ := donor(t, f.service.DB)
	t.Cleanup(func() {
		_, err := f.service.DB.Exec(ctx, "DELETE FROM donor_metric_projections WHERE donor_id=$1", referred)
		if err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		_, err := f.service.DB.Exec(ctx, "DELETE FROM donor_preferences WHERE donor_id=$1", f.donorID)
		if err != nil {
			t.Error(err)
		}
	})
	if _, err := f.service.DB.Exec(ctx, "INSERT INTO donor_preferences(id,donor_id,version,referral_emails,marketing_emails) VALUES($1,$2,1,true,false)", testID(t), f.donorID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.service.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := queries.New(tx)
	if _, err = tx.Exec(ctx, `INSERT INTO donor_metric_projections(id,donor_id,referrer_id,referral_kind,net_minor,computed_at,next_refresh_at) VALUES($1,$2,$3,'IRL',3000,$4,$5)`, testID(t), referred, f.donorID, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = queueMessage(ctx, q, f.donorID, "REFERRAL", "referral:"+referred.String(), map[string]any{"referred_id": referred.String(), "kind": "IRL"}, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	transport := &recordingMail{failure: errors.New("retry")}
	w := notificationWorker(f, transport)
	if _, err = w.DeliverNext(ctx, now); err != nil {
		t.Fatal(err)
	}
	if len(transport.messages) != 1 || !strings.Contains(transport.messages[0].Subject, "IRL Referral") {
		t.Fatal("lost referral channel")
	}
	if _, err = w.DB.Exec(ctx, "UPDATE donor_preferences SET referral_emails=false WHERE donor_id=$1", f.donorID); err != nil {
		t.Fatal(err)
	}
	if _, err = w.DeliverNext(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(transport.messages) != 1 {
		t.Fatal("opt-out ignored on retry")
	}
	var reason string
	if err = w.DB.QueryRow(ctx, "SELECT suppressed_reason FROM donor_messages WHERE donor_id=$1", f.donorID).Scan(&reason); err != nil || reason != "opted_out" {
		t.Fatal(reason, err)
	}
}
