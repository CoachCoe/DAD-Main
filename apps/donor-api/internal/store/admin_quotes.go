package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

// refundSnapshot is the processor's refund list fetched without holding the
// donor lock, with the count of local refund observations it was read after.
type refundSnapshot struct {
	gift         queries.DonorGift
	observations int64
	rows         []payments.Object
}

func (s StaffFinanceService) prefetchRefunds(ctx context.Context, token string, donorID, giftID pgtype.UUID) (refundSnapshot, error) {
	q := queries.New(s.Staff.DB)
	if _, err := requireStaff(ctx, q, token, "gift.refund", time.Now().UTC()); err != nil {
		return refundSnapshot{}, err
	}
	gift, err := q.StaffGiftSnapshot(ctx, queries.StaffGiftSnapshotParams{DonorID: donorID, ID: giftID})
	if err != nil {
		return refundSnapshot{}, err
	}
	if !gift.StripePaymentIntentID.Valid {
		return refundSnapshot{}, payments.ErrFacts
	}
	observations, err := q.GiftRefundObservations(ctx, gift.ID)
	if err != nil {
		return refundSnapshot{}, err
	}
	if s.Gateway == nil {
		return refundSnapshot{}, payments.ErrCheckoutUnavailable
	}
	rows, err := s.Gateway.ListPaymentRefunds(ctx, gift.StripePaymentIntentID.String)
	if err != nil {
		return refundSnapshot{}, err
	}
	return refundSnapshot{gift: gift, observations: observations, rows: rows}, nil
}

// currentRefunds returns the processor's refunds for the locked gift. The
// snapshot is used only if no refund state was recorded after it was read.
func (s StaffFinanceService) currentRefunds(ctx context.Context, q *queries.Queries, gift queries.DonorGift, snapshot refundSnapshot) ([]payments.Object, int64, error) {
	observations, err := q.GiftRefundObservations(ctx, gift.ID)
	if err != nil {
		return nil, 0, err
	}
	if observations != snapshot.observations || gift.ID != snapshot.gift.ID || gift.StripePaymentIntentID != snapshot.gift.StripePaymentIntentID {
		return s.refunds(ctx, gift)
	}
	return refundBalance(gift, snapshot.rows)
}

type RefundQuote struct {
	AmountMinor    int64       `json:"amount_minor"`
	AvailableMinor int64       `json:"available_minor"`
	Currency       string      `json:"currency"`
	DonorID        pgtype.UUID `json:"donor_id"`
	GiftID         pgtype.UUID `json:"gift_id"`
	Mode           string      `json:"mode"`
}

func (s StaffFinanceService) RefundQuote(ctx context.Context, token string, donorID, giftID pgtype.UUID) (RefundQuote, error) {
	snapshot, err := s.prefetchRefunds(ctx, token, donorID, giftID)
	if err != nil {
		return RefundQuote{}, err
	}
	return staffTransaction(ctx, s.Staff, token, "gift.refund", func(tx pgx.Tx, _ queries.DonorStaff, now time.Time) (RefundQuote, error) {
		q := queries.New(tx)
		actor, _, err := staffFinancialOwner(ctx, q, token, donorID, "gift.refund", now)
		if err != nil {
			return RefundQuote{}, err
		}
		gift, err := staffGift(ctx, q, donorID, giftID)
		if err != nil {
			return RefundQuote{}, err
		}
		rows, remaining, err := s.currentRefunds(ctx, q, gift, snapshot)
		if err != nil {
			return RefundQuote{}, err
		}
		for _, current := range rows {
			metadata, err := current.Object("metadata")
			if err != nil {
				continue
			}
			var id pgtype.UUID
			if id.Scan(metadata.Text("dad_staff_action")) != nil || !id.Valid {
				continue
			}
			command, err := q.StaffActionByID(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return RefundQuote{}, err
			}
			amount, err := current.Int("amount")
			if err != nil {
				return RefundQuote{}, err
			}
			if command.Kind != "REFUND" || command.TargetID != gift.ID || command.DonorID != donorID || !command.AmountMinor.Valid || command.AmountMinor.Int64 != amount {
				continue
			}
			previous, err := q.RefundByStripeID(ctx, current.Text("id"))
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return RefundQuote{}, err
			}
			if err == nil && previous.Status == strings.ToUpper(current.Text("status")) {
				continue
			}
			if err = RecordRefundInTx(ctx, q, gift, current, pgtype.UUID{}, command.ID, now); err != nil {
				return RefundQuote{}, err
			}
			if err = q.SaveStaffAction(ctx, queries.SaveStaffActionParams{ID: command.ID, StripeRefundID: pgtype.Text{String: current.Text("id"), Valid: true}, Status: "OBSERVED", CompletedAt: timestamp(now)}); err != nil {
				return RefundQuote{}, err
			}
			if err = auditStaff(ctx, q, actor.ID, "gift.refund_reconciled", command.ID.String(), map[string]string{"status": strings.ToUpper(current.Text("status"))}, now); err != nil {
				return RefundQuote{}, err
			}
		}
		if _, err = RefreshProjectionInTx(ctx, tx, donorID, now); err != nil {
			return RefundQuote{}, err
		}
		if err = auditStaff(ctx, q, actor.ID, "gift.refund_preview", gift.ID.String(), nil, now); err != nil {
			return RefundQuote{}, err
		}
		return RefundQuote{GiftID: gift.ID, DonorID: donorID, AmountMinor: gift.AmountMinor, AvailableMinor: remaining, Currency: "USD", Mode: "test"}, nil
	})
}

func (s StaffFinanceService) CancellationQuote(ctx context.Context, token string, donorID pgtype.UUID) (RecurringDetails, error) {
	if _, err := requireStaff(ctx, queries.New(s.Staff.DB), token, "gift.cancel", time.Now().UTC()); err != nil {
		return RecurringDetails{}, err
	}
	recurring := RecurringService{DB: s.Staff.DB, Gateway: s.Gateway, Origin: s.Origin}
	snapshot, err := recurring.prefetchSubscription(ctx, donorID)
	if err != nil {
		return RecurringDetails{}, err
	}
	return staffTransaction(ctx, s.Staff, token, "gift.cancel", func(tx pgx.Tx, _ queries.DonorStaff, now time.Time) (RecurringDetails, error) {
		q := queries.New(tx)
		actor, _, err := staffFinancialOwner(ctx, q, token, donorID, "gift.cancel", now)
		if err != nil {
			return RecurringDetails{}, err
		}
		attempt, err := q.LatestMonthlyAttempt(ctx, donorID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !attempt.StripeSubscriptionID.Valid) {
			return RecurringDetails{Mode: "test"}, nil
		}
		if err != nil {
			return RecurringDetails{}, err
		}
		row, _, err := recurring.read(ctx, tx, attempt, now, "", snapshot)
		if err != nil {
			return RecurringDetails{}, err
		}
		if err = auditStaff(ctx, q, actor.ID, "gift.cancel_preview", attempt.ID.String(), nil, now); err != nil {
			return RecurringDetails{}, err
		}
		return subscriptionView(ctx, q, attempt, row)
	})
}
