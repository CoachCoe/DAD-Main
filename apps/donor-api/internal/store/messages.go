package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/recognition"
	"impactgraph/donor-api/internal/store/queries"
)

type MessageWorker struct {
	DB        *pgxpool.Pool
	Settings  mail.Settings
	Origin    string
	Transport mail.Transport
}

// messageSuppression returns why job should no longer be sent, if it should
// not: optional messages recheck consent and every message rechecks that its
// facts still hold.
func messageSuppression(ctx context.Context, q *queries.Queries, job queries.DonorMessage, donor queries.Donor, now time.Time) (string, error) {
	if donor.Disabled {
		return "donor_disabled", nil
	}
	data, err := payments.DecodeObject(job.Context)
	if err != nil {
		return "", err
	}
	switch job.Kind {
	case "REFERRAL":
		return referralSuppression(ctx, q, data, donor)
	case "RECOVERY":
		return recoverySuppression(ctx, q, data, donor, now)
	}
	return "", nil
}

// referralSuppression stops a referral notice the donor opted out of, or
// whose referred donor no longer counts.
func referralSuppression(ctx context.Context, q *queries.Queries, data payments.Object, donor queries.Donor) (string, error) {
	pref, err := q.DonorPreference(ctx, donor.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !pref.ReferralEmails) {
		return "opted_out", nil
	}
	if err != nil {
		return "", err
	}
	id, err := parseID(data.Text("referred_id"))
	if err != nil {
		return "", err
	}
	projection, err := q.Projection(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "referral_no_longer_qualifies", nil
	}
	if err != nil {
		return "", err
	}
	if projection.ReferrerID != donor.ID || projection.ReferralKind.String != data.Text("kind") || !projection.NetMinor.Valid || projection.NetMinor.Int64 <= 0 {
		return "referral_no_longer_qualifies", nil
	}
	return "", nil
}

// recoverySuppression stops a failed-payment reminder once the payment
// recovers, the gift is cancelled, grace ends, or a later reminder is due.
// Reminders go out on the failure day and days 7 and 13 of the 14-day grace.
func recoverySuppression(ctx context.Context, q *queries.Queries, data payments.Object, donor queries.Donor, now time.Time) (string, error) {
	id, err := parseID(data.Text("period_id"))
	if err != nil {
		return "", err
	}
	period, err := q.MessagePeriod(ctx, queries.MessagePeriodParams{ID: id, DonorID: donor.ID})
	if err != nil {
		return "", err
	}
	if period.Status != "FAILED" {
		return "payment_recovered", nil
	}
	sub, err := q.SubscriptionForCheckout(ctx, period.CheckoutID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if err == nil && (sub.StopsAt.Valid || terminalSubscription(sub.Status)) {
		return "subscription_cancelled", nil
	}
	elapsed := now.Sub(period.DueAt.Time)
	if elapsed >= recognition.Grace {
		return "grace_ended", nil
	}
	if elapsed < 0 {
		return "", payments.ErrFacts
	}
	latest := int64(0)
	if elapsed >= 13*24*time.Hour {
		latest = 13
	} else if elapsed >= 7*24*time.Hour {
		latest = 7
	}
	day, err := data.Int("day")
	if err != nil || (day != 0 && day != 7 && day != 13) {
		return "", payments.ErrFacts
	}
	if day < latest {
		return "newer_reminder_due", nil
	}
	return "", nil
}

func renderMessage(ctx context.Context, q *queries.Queries, job queries.DonorMessage, site publicSite, now time.Time) (string, string, error) {
	data, err := payments.DecodeObject(job.Context)
	if err != nil {
		return "", "", err
	}
	switch job.Kind {
	case "RECEIPT":
		id, err := parseID(data.Text("gift_id"))
		if err != nil {
			return "", "", err
		}
		receipt, err := readReceipt(ctx, q, job.DonorID, id, now)
		if err != nil {
			return "", "", err
		}
		return "Your DAD sandbox gift record", renderReceipt(receipt), nil
	case "ANNUAL":
		year, err := data.Int("year")
		if err != nil || year < 2026 || year > 9999 {
			return "", "", ErrReceipt
		}
		summary, err := readAnnualSummary(ctx, q, job.DonorID, int(year), now)
		if err != nil {
			return "", "", err
		}
		return "Your DAD sandbox annual summary", renderSummary(summary), nil
	case "REFERRAL":
		channel := "Link"
		if data.Text("kind") == "IRL" {
			channel = "IRL"
		} else if data.Text("kind") != "LINK" {
			return "", "", payments.ErrFacts
		}
		return "A " + channel + " Referral completed a first test gift", "Someone you invited through an " + channel + " Referral completed their first test gift.\nSee your circle's impact: " + site.dashboard() + "\n\nTurn off referral notifications: " + site.profile() + "\nThis is a sandbox notification. DAD launches in April 2027.\n", nil
	case "RECOVERY":
		id, err := parseID(data.Text("period_id"))
		if err != nil {
			return "", "", err
		}
		period, err := q.MessagePeriod(ctx, queries.MessagePeriodParams{ID: id, DonorID: job.DonorID})
		if err != nil {
			return "", "", err
		}
		return "Your DAD test payment needs attention", "Your monthly test payment was declined. Your giving streak is at risk.\nThe recovery grace period ends " + iso(period.DueAt.Time.Add(recognition.Grace)) + ".\nUpdate your payment method securely: " + site.paymentMethod() + "\nIf signed out, request a sign-in link for the email used to give, then return to payment methods.\nUpdating a card does not itself confirm payment recovery. Stripe's retry sequence handles collection.\nThis essential payment notice concerns sandbox giving, not a real charge.\n", nil
	default:
		return "", "", payments.ErrFacts
	}
}

// deliveryLease reserves a claimed job while its send is in flight. It outlasts
// a worker step, so a claim abandoned by a crashed worker is retried later.
const deliveryLease = 5 * time.Minute

// maxDeliveryAttempts parks a job for operator attention, matching the
// queries that select due work.
const maxDeliveryAttempts = 5

func messageRetryAt(now time.Time, attempts int32) pgtype.Timestamptz {
	return timestamp(now.Add(time.Duration(min(60, 1<<uint(attempts))) * time.Minute))
}
func failMessage(ctx context.Context, q *queries.Queries, job queries.DonorMessage, now time.Time, category string) error {
	if err := q.IncrementMessageAttempt(ctx, job.ID); err != nil {
		return err
	}
	return q.FailMessage(ctx, queries.FailMessageParams{ID: job.ID, LastError: text(category), AvailableAt: messageRetryAt(now, job.Attempts+1)})
}
func completeMessage(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time, reason string) error {
	return q.CompleteMessage(ctx, queries.CompleteMessageParams{ID: id, CompletedAt: timestamp(now), SuppressedReason: pgtype.Text{String: reason, Valid: reason != ""}})
}
func messageDataError(err error) bool {
	return errors.Is(err, payments.ErrFacts) || errors.Is(err, ErrReceipt) || errors.Is(err, pgx.ErrNoRows)
}

// prepare selects the next due message, suppressing it if it should no longer
// be sent and rendering its body on first use. It returns no job when the one
// found was finished or failed instead.
func (w MessageWorker) prepare(ctx context.Context, site publicSite, now time.Time) (pgtype.UUID, bool, error) {
	var id pgtype.UUID
	worked := false
	err := pgx.BeginFunc(ctx, w.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		job, err := q.NextMessage(ctx, timestamp(now))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		worked = true
		donor, err := q.LockDonor(ctx, job.DonorID)
		if err != nil {
			return err
		}
		reason, err := messageSuppression(ctx, q, job, donor, now)
		if err == nil && reason != "" {
			return completeMessage(ctx, q, job.ID, now, reason)
		}
		if err == nil && !job.Body.Valid {
			var subject, body string
			if subject, body, err = renderMessage(ctx, q, job, site, now); err == nil {
				err = q.RenderMessage(ctx, queries.RenderMessageParams{ID: job.ID, Subject: text(subject), Body: text(body)})
			}
		}
		if err != nil {
			if !messageDataError(err) {
				return err
			}
			return failMessage(ctx, q, job, now, "InvalidMessage")
		}
		id = job.ID
		return nil
	})
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	return id, worked, nil
}

// DeliverNext renders and sends the next due financial or referral message.
// It reports whether it found work.
func (w MessageWorker) DeliverNext(ctx context.Context, now time.Time) (bool, error) {
	site, err := mailSite(w.Settings, w.Origin, w.Transport)
	if err != nil {
		return false, err
	}
	id, worked, err := w.prepare(ctx, site, now)
	if err != nil || !worked || !id.Valid {
		return worked, err
	}
	message, attempts, err := w.claim(ctx, id, now)
	if err != nil || message == nil {
		return err == nil, err
	}
	// The donor lock is released before SMTP; the claim keeps other workers off
	// the job, and a crash before completion resends with the same Message-ID.
	sendErr := w.Transport.Send(ctx, *message)
	q := queries.New(w.DB)
	if sendErr != nil {
		err = q.FailMessage(ctx, queries.FailMessageParams{ID: id, LastError: text("DeliveryError"), AvailableAt: messageRetryAt(now, attempts)})
	} else {
		err = completeMessage(ctx, q, id, now, "")
	}
	return err == nil, err
}

// claim rechecks eligibility under the donor lock and reserves the job for one
// send. It returns no message when the job was completed, suppressed or failed
// instead, and otherwise the attempt the send will be.
func (w MessageWorker) claim(ctx context.Context, id pgtype.UUID, now time.Time) (*mail.Message, int32, error) {
	var message *mail.Message
	var attempt int32
	err := pgx.BeginFunc(ctx, w.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		job, err := q.MessageByID(ctx, id)
		if err != nil {
			return err
		}
		if job.CompletedAt.Valid || job.Attempts >= maxDeliveryAttempts || job.AvailableAt.Time.After(now) {
			return nil
		}
		donor, err := q.LockDonor(ctx, job.DonorID)
		if err != nil {
			return err
		}
		reason, err := messageSuppression(ctx, q, job, donor, now)
		if err != nil {
			if !messageDataError(err) {
				return err
			}
			return failMessage(ctx, q, job, now, "InvalidMessage")
		}
		if reason != "" {
			return completeMessage(ctx, q, id, now, reason)
		}
		messageID, err := mail.MessageID(w.Settings.Sender, "message", id.String())
		if err != nil {
			return err
		}
		if err = q.ClaimMessage(ctx, queries.ClaimMessageParams{ID: id, AvailableAt: timestamp(now.Add(deliveryLease))}); err != nil {
			return err
		}
		message = &mail.Message{From: w.Settings.Sender, To: donor.Email, Subject: job.Subject.String, Body: job.Body.String, ID: messageID}
		attempt = job.Attempts + 1
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return message, attempt, nil
}
