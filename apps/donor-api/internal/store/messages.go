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
	"impactgraph/donor-api/internal/store/queries"
)

type MessageWorker struct {
	DB        *pgxpool.Pool
	Settings  mail.Settings
	Origin    string
	Transport mail.Transport
}

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
	case "RECOVERY":
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
		if elapsed >= 14*24*time.Hour {
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
	}
	return "", nil
}
func renderMessage(ctx context.Context, q *queries.Queries, job queries.DonorMessage, origin string, now time.Time) (string, string, error) {
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
		receipt, err := ReadReceipt(ctx, q, job.DonorID, id, now)
		if err != nil {
			return "", "", err
		}
		return "Your DAD sandbox gift record", RenderReceipt(receipt), nil
	case "ANNUAL":
		year, err := data.Int("year")
		if err != nil || year < 2026 || year > 9999 {
			return "", "", ErrReceipt
		}
		summary, err := ReadAnnualSummary(ctx, q, job.DonorID, int(year), now)
		if err != nil {
			return "", "", err
		}
		return "Your DAD sandbox annual summary", RenderSummary(summary), nil
	case "REFERRAL":
		channel := "Link"
		if data.Text("kind") == "IRL" {
			channel = "IRL"
		} else if data.Text("kind") != "LINK" {
			return "", "", payments.ErrFacts
		}
		return "A " + channel + " Referral completed a first test gift", "Someone you invited through an " + channel + " Referral completed their first test gift.\nSee your circle's impact: " + origin + "/account/dashboard\n\nTurn off referral notifications: " + origin + "/account/profile\nThis is a sandbox notification. DAD launches in April 2027.\n", nil
	case "RECOVERY":
		id, err := parseID(data.Text("period_id"))
		if err != nil {
			return "", "", err
		}
		period, err := q.MessagePeriod(ctx, queries.MessagePeriodParams{ID: id, DonorID: job.DonorID})
		if err != nil {
			return "", "", err
		}
		return "Your DAD test payment needs attention", "Your monthly test payment was declined. Your giving streak is at risk.\nThe recovery grace period ends " + iso(period.DueAt.Time.Add(14*24*time.Hour)) + ".\nUpdate your payment method securely: " + origin + "/account/payment-method\nIf signed out, request a sign-in link for the email used to give, then return to payment methods.\nUpdating a card does not itself confirm payment recovery. Stripe's retry sequence handles collection.\nThis essential payment notice concerns sandbox giving, not a real charge.\n", nil
	default:
		return "", "", payments.ErrFacts
	}
}
func failMessage(ctx context.Context, q *queries.Queries, job queries.DonorMessage, now time.Time, category string) error {
	if err := q.IncrementMessageAttempt(ctx, job.ID); err != nil {
		return err
	}
	minutes := min(60, 1<<uint(job.Attempts+1))
	return q.FailMessage(ctx, queries.FailMessageParams{ID: job.ID, LastError: pgtype.Text{String: category, Valid: true}, AvailableAt: timestamp(now.Add(time.Duration(minutes) * time.Minute))})
}
func completeMessage(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time, reason string) error {
	return q.CompleteMessage(ctx, queries.CompleteMessageParams{ID: id, CompletedAt: timestamp(now), SuppressedReason: pgtype.Text{String: reason, Valid: reason != ""}})
}
func messageDataError(err error) bool {
	return errors.Is(err, payments.ErrFacts) || errors.Is(err, ErrReceipt) || errors.Is(err, pgx.ErrNoRows)
}

func (w MessageWorker) prepare(ctx context.Context, now time.Time) (pgtype.UUID, bool, error) {
	empty := pgtype.UUID{}
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	job, err := q.NextMessage(ctx, timestamp(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, false, nil
	}
	if err != nil {
		return empty, false, err
	}
	donor, err := q.LockDonor(ctx, job.DonorID)
	if err != nil {
		return empty, false, err
	}
	reason, err := messageSuppression(ctx, q, job, donor, now)
	if err == nil && reason != "" {
		if err = completeMessage(ctx, q, job.ID, now, reason); err != nil {
			return empty, false, err
		}
		return empty, true, tx.Commit(ctx)
	}
	if err == nil && !job.Body.Valid {
		var subject, body string
		subject, body, err = renderMessage(ctx, q, job, w.Origin, now)
		if err == nil {
			err = q.RenderMessage(ctx, queries.RenderMessageParams{ID: job.ID, Subject: pgtype.Text{String: subject, Valid: true}, Body: pgtype.Text{String: body, Valid: true}})
		}
	}
	if err != nil {
		if !messageDataError(err) {
			return empty, false, err
		}
		if err = failMessage(ctx, q, job, now, "ValueError"); err != nil {
			return empty, false, err
		}
		return empty, true, tx.Commit(ctx)
	}
	return job.ID, true, tx.Commit(ctx)
}
func (w MessageWorker) DeliverNext(ctx context.Context, now time.Time) (bool, error) {
	if err := w.Settings.Validate(); err != nil {
		return false, err
	}
	if err := payments.ValidateOrigin(w.Origin); err != nil {
		return false, err
	}
	if w.Transport == nil {
		return false, mail.ErrConfiguration
	}
	id, worked, err := w.prepare(ctx, now)
	if err != nil || !worked || !id.Valid {
		return worked, err
	}
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	job, err := q.MessageByID(ctx, id)
	if err != nil {
		return false, err
	}
	if job.CompletedAt.Valid || job.Attempts >= 5 || job.AvailableAt.Time.After(now) {
		return true, tx.Commit(ctx)
	}
	donor, err := q.LockDonor(ctx, job.DonorID)
	if err != nil {
		return false, err
	}
	reason, err := messageSuppression(ctx, q, job, donor, now)
	if err != nil {
		if !messageDataError(err) {
			return false, err
		}
		if err = failMessage(ctx, q, job, now, "ValueError"); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if reason != "" {
		if err = completeMessage(ctx, q, id, now, reason); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	messageID, err := mail.MessageID(w.Settings.Sender, "message", id.String())
	if err != nil {
		return false, err
	}
	sendErr := w.Transport.Send(ctx, mail.Message{From: w.Settings.Sender, To: donor.Email, Subject: job.Subject.String, Body: job.Body.String, ID: messageID})
	if sendErr != nil {
		err = failMessage(ctx, q, job, now, "DeliveryError")
	} else {
		if err = q.IncrementMessageAttempt(ctx, id); err == nil {
			err = completeMessage(ctx, q, id, now, "")
		}
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
