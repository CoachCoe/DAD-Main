package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store/queries"
)

func (s *StaffService) RequestClaim(ctx context.Context, token string, id pgtype.UUID) error {
	_, err := staffTransaction(ctx, s, token, "message.resend", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (bool, error) {
		q := queries.New(tx)
		donor, err := q.StaffDonorByID(ctx, id)
		if err != nil {
			return false, err
		}
		if err = QueueLoginEmailInTx(ctx, tx, donor.Email, now); err != nil {
			return false, err
		}
		return true, auditStaff(ctx, q, actor.ID, "message.claim_requested", id.String(), nil, now)
	})
	return err
}

type QueuedMessage struct {
	ID     pgtype.UUID `json:"id"`
	Queued bool        `json:"queued"`
}

func (s *StaffService) ResendMessage(ctx context.Context, token string, messageID, requestID pgtype.UUID) (QueuedMessage, error) {
	if !ValidRequestID(requestID) {
		return QueuedMessage{}, ErrStaffInput
	}
	return staffTransaction(ctx, s, token, "message.resend", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (QueuedMessage, error) {
		q := queries.New(tx)
		source, err := q.StaffMessageByID(ctx, messageID)
		if err != nil {
			return QueuedMessage{}, err
		}
		if _, err = q.LockDonor(ctx, source.DonorID); err != nil {
			return QueuedMessage{}, err
		}
		source, err = q.StaffMessageByID(ctx, messageID)
		if err != nil {
			return QueuedMessage{}, err
		}
		key := "resend:" + requestID.String()
		existing, err := q.MessageByDedupKey(ctx, key)
		if err == nil {
			var metadata map[string]any
			if err = json.Unmarshal(existing.Context, &metadata); err != nil {
				return QueuedMessage{}, err
			}
			if metadata["resend_source"] != messageID.String() || metadata["resend_actor"] != actor.ID.String() {
				return QueuedMessage{}, ErrStaffConflict
			}
			return QueuedMessage{Queued: true, ID: existing.ID}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return QueuedMessage{}, err
		}
		if (source.Kind != "RECEIPT" && source.Kind != "ANNUAL") || source.SuppressedReason.Valid || (!source.CompletedAt.Valid && source.Attempts < 5) {
			return QueuedMessage{}, ErrStaffConflict
		}
		recent, err := q.RecentDonorMessage(ctx, queries.RecentDonorMessageParams{DonorID: source.DonorID, Kind: source.Kind, CreatedAt: timestamp(now.Add(-time.Minute))})
		if err != nil {
			return QueuedMessage{}, err
		}
		if recent {
			return QueuedMessage{}, ErrStaffConflict
		}
		var metadata map[string]any
		if err = json.Unmarshal(source.Context, &metadata); err != nil {
			return QueuedMessage{}, err
		}
		if metadata == nil {
			return QueuedMessage{}, ErrStaffConflict
		}
		metadata["resend_source"], metadata["resend_actor"] = messageID.String(), actor.ID.String()
		if err = queueMessage(ctx, q, source.DonorID, source.Kind, key, metadata, now); err != nil {
			return QueuedMessage{}, err
		}
		job, err := q.MessageByDedupKey(ctx, key)
		if err != nil {
			return QueuedMessage{}, err
		}
		// An idempotency key may also have raced on a different donor lock.
		var saved map[string]any
		if err = json.Unmarshal(job.Context, &saved); err != nil {
			return QueuedMessage{}, err
		}
		if saved["resend_source"] != messageID.String() || saved["resend_actor"] != actor.ID.String() {
			return QueuedMessage{}, ErrStaffConflict
		}
		if err = auditStaff(ctx, q, actor.ID, "message.resend_requested", job.ID.String(), map[string]string{"source_id": messageID.String()}, now); err != nil {
			return QueuedMessage{}, err
		}
		return QueuedMessage{Queued: true, ID: job.ID}, nil
	})
}
