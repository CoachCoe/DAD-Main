package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/mail"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type LoginEmailWorker struct {
	DB        *pgxpool.Pool
	Settings  mail.Settings
	Origin    string
	Transport mail.Transport
}

func (w LoginEmailWorker) DeliverNext(ctx context.Context, now time.Time) (bool, error) {
	if err := w.Settings.Validate(); err != nil {
		return false, err
	}
	if err := payments.ValidateOrigin(w.Origin); err != nil {
		return false, err
	}
	if w.Transport == nil {
		return false, mail.ErrConfiguration
	}
	// Validate the encryption key before selecting work. A misconfigured process
	// must not consume the retry budget of every queued account message.
	if _, err := credentials.Encrypt(nil, w.Settings.EncryptionKey); err != nil {
		return false, err
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
	job, err := q.LoginEmailByID(ctx, id)
	if err != nil {
		return false, err
	}
	if job.CompletedAt.Valid || job.AvailableAt.Time.After(now) || job.Attempts >= 5 {
		return true, tx.Commit(ctx)
	}
	donor, err := q.LockDonor(ctx, job.DonorID)
	if err != nil {
		return false, err
	}
	if donor.Disabled || !job.ExpiresAt.Valid || !job.ExpiresAt.Time.After(now) {
		if err = q.CompleteLoginEmail(ctx, queries.CompleteLoginEmailParams{ID: id, CompletedAt: timestamp(now)}); err != nil {
			return false, err
		}
		return true, tx.Commit(ctx)
	}
	if err = q.IncrementLoginEmailAttempt(ctx, id); err != nil {
		return false, err
	}
	sendErr := func() error {
		token, err := credentials.Decrypt(job.EncryptedToken, w.Settings.EncryptionKey)
		if err != nil {
			return err
		}
		// Old Python tokens are exactly 32 random bytes encoded as URL-safe text.
		raw, err := base64.RawURLEncoding.Strict().DecodeString(string(token))
		if err != nil || len(raw) != 32 {
			return credentials.ErrInvalid
		}
		messageID, err := mail.MessageID(w.Settings.Sender, "login", id.String())
		if err != nil {
			return err
		}
		return w.Transport.Send(ctx, mail.Message{From: w.Settings.Sender, To: donor.Email, Subject: "Sign in to your DAD sandbox account", ID: messageID, Body: "Use this link to claim or sign in to your DAD sandbox donor account.\n\n" + w.Origin + "/account/claim#token=" + string(token) + "\n\n" +
			"Confirm on the page to continue. This single-use link expires in 15 minutes.\n" +
			"If you did not request it, ignore this email. No account access is granted until you confirm. This is an account access message, not a tax receipt.\n"})
	}()
	if sendErr != nil {
		category := "DeliveryError"
		if errors.Is(sendErr, credentials.ErrInvalid) {
			category = "InvalidToken"
		}
		err = q.FailLoginEmail(ctx, queries.FailLoginEmailParams{ID: id, LastError: pgtype.Text{String: category, Valid: true}, AvailableAt: timestamp(now.Add(time.Minute))})
	} else {
		err = q.CompleteLoginEmail(ctx, queries.CompleteLoginEmailParams{ID: id, CompletedAt: timestamp(now)})
	}
	if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (w LoginEmailWorker) prepare(ctx context.Context, now time.Time) (pgtype.UUID, bool, error) {
	empty := pgtype.UUID{}
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return empty, false, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	job, err := q.NextLoginEmail(ctx, timestamp(now))
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
	if donor.Disabled {
		if err = q.CompleteLoginEmail(ctx, queries.CompleteLoginEmailParams{ID: job.ID, CompletedAt: timestamp(now)}); err != nil {
			return empty, false, err
		}
		return empty, true, tx.Commit(ctx)
	}
	if job.EncryptedToken == nil {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return empty, false, err
		}
		token := base64.RawURLEncoding.EncodeToString(secret)
		ciphertext, err := credentials.Encrypt([]byte(token), w.Settings.EncryptionKey)
		if err != nil {
			return empty, false, err
		}
		id, err := NewID()
		if err != nil {
			return empty, false, err
		}
		expiry := timestamp(now.Add(15 * time.Minute))
		if err = q.InsertLoginLink(ctx, queries.InsertLoginLinkParams{ID: id, DonorID: donor.ID, TokenHash: HashToken(token), ExpiresAt: expiry}); err != nil {
			return empty, false, err
		}
		if err = q.PrepareLoginEmail(ctx, queries.PrepareLoginEmailParams{ID: job.ID, EncryptedToken: ciphertext, ExpiresAt: expiry}); err != nil {
			return empty, false, err
		}
	}
	// The proof must commit before any SMTP side effect. The second transaction
	// rechecks eligibility under the job lock, serializing competing workers.
	return job.ID, true, tx.Commit(ctx)
}
