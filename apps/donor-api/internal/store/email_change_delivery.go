package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type EmailChangeWorker LoginEmailWorker

type emailChangeSend struct {
	id      pgtype.UUID
	stage   string
	message mail.Message
}

func (w EmailChangeWorker) DeliverNext(ctx context.Context, now time.Time) (bool, error) {
	if err := w.Settings.Validate(); err != nil {
		return false, err
	}
	if err := payments.ValidateOrigin(w.Origin); err != nil {
		return false, err
	}
	if w.Transport == nil {
		return false, mail.ErrConfiguration
	}
	if _, err := credentials.Encrypt(nil, w.Settings.EncryptionKey); err != nil {
		return false, err
	}
	send, worked, err := w.claim(ctx, now)
	if err != nil || send == nil {
		return worked, err
	}
	q := queries.New(w.DB)
	if err = w.Transport.Send(ctx, send.message); err != nil {
		err = q.RetryEmailChangeDelivery(ctx, queries.RetryEmailChangeDeliveryParams{ID: send.id, AvailableAt: timestamp(now.Add(time.Minute)), Stage: send.stage})
	} else {
		err = q.EmailChangeDelivered(ctx, queries.EmailChangeDeliveredParams{ID: send.id, SentAt: timestamp(now), Stage: send.stage})
	}
	return err == nil, err
}

// claim selects the next confirmation under its donor's lock and reserves it
// for one send, which happens after the lock is released. Completion matches
// the claimed stage, so a confirmation that advanced meanwhile is not marked sent.
func (w EmailChangeWorker) claim(ctx context.Context, now time.Time) (*emailChangeSend, bool, error) {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	owner, err := q.EmailChangeDeliveryOwner(ctx, timestamp(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	donor, err := q.LockDonor(ctx, owner)
	if err != nil {
		return nil, false, err
	}
	job, err := q.NextEmailChangeDelivery(ctx, queries.NextEmailChangeDeliveryParams{DonorID: owner, AvailableAt: timestamp(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if donor.Disabled || donor.Email != job.OldEmail || !job.ExpiresAt.Time.After(now) {
		if err = q.CancelEmailChanges(ctx, owner); err != nil {
			return nil, false, err
		}
		return nil, true, tx.Commit(ctx)
	}
	proof, err := credentials.Decrypt(job.EncryptedToken, w.Settings.EncryptionKey)
	if err != nil {
		if err = q.EmailChangeDeliveryFailed(ctx, queries.EmailChangeDeliveryFailedParams{ID: job.ID, AvailableAt: timestamp(now.Add(time.Minute))}); err != nil {
			return nil, false, err
		}
		return nil, true, tx.Commit(ctx)
	}
	messageID, err := mail.MessageID(w.Settings.Sender, "email-change-"+job.Stage, job.ID.String())
	if err != nil {
		return nil, false, err
	}
	recipient := job.OldEmail
	body := "Someone signed in to your DAD account requested changing its email to " + job.NewEmail + ". Confirm only if you requested this change."
	if job.Stage == "NEW" {
		recipient = job.NewEmail
		body = "Confirm this address to finish changing your DAD account email. This will sign out every existing session."
	}
	if err = q.ClaimEmailChangeDelivery(ctx, queries.ClaimEmailChangeDeliveryParams{ID: job.ID, AvailableAt: timestamp(now.Add(deliveryLease))}); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	message := mail.Message{From: w.Settings.Sender, To: recipient, ID: messageID, Subject: "Confirm your DAD account email change", Body: body + "\n\n" + w.Origin + "/account/email-change#token=" + string(proof) + "\n\nConfirm on the page. This link is single-use and expires 15 minutes after it was requested. If you did not request this, ignore this message. No change will be made without both confirmations.\n"}
	return &emailChangeSend{id: job.ID, stage: job.Stage, message: message}, true, nil
}
