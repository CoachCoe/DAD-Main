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
		return refundSnapshot{}, payments.ErrUnconfigured
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
	// ObservedAt and Stale mark a preview from stored refund states because
	// the processor could not be read.
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	Stale      bool       `json:"stale,omitempty"`
}

// RefundQuote previews how much of a gift can still be refunded, after
// reconciling the processor's refunds of it.
func (s StaffFinanceService) RefundQuote(ctx context.Context, token string, donorID, giftID pgtype.UUID) (RefundQuote, error) {
	snapshot, live, err := liveRead(ctx, func(ctx context.Context) (refundSnapshot, error) {
		return s.prefetchRefunds(ctx, token, donorID, giftID)
	})
	if err != nil {
		return RefundQuote{}, err
	}
	if !live {
		return staffRead(ctx, s.Staff, token, "gift.refund", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (RefundQuote, error) {
			q := queries.New(tx)
			gift, err := q.StaffGiftSnapshot(ctx, queries.StaffGiftSnapshotParams{DonorID: donorID, ID: giftID})
			if err != nil {
				return RefundQuote{}, err
			}
			quote, err := storedRefundQuote(ctx, q, gift)
			if err != nil {
				return RefundQuote{}, err
			}
			return quote, auditStaff(ctx, q, actor.ID, "gift.refund_preview", gift.ID.String(), map[string]bool{"stale": true}, now)
		})
	}
	return staffDonorWrite(ctx, s.Staff, token, "gift.refund", donorID, func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (RefundQuote, error) {
		q := queries.New(tx)
		gift, err := staffGift(ctx, q, donorID, giftID)
		if err != nil {
			return RefundQuote{}, err
		}
		rows, remaining, err := s.currentRefunds(ctx, q, gift, snapshot)
		if err != nil {
			return RefundQuote{}, err
		}
		if err = reconcileStaffRefunds(ctx, q, actor.ID, donorID, gift, rows, now); err != nil {
			return RefundQuote{}, err
		}
		if _, err = refreshProjection(ctx, tx, donorID, now); err != nil {
			return RefundQuote{}, err
		}
		if err = auditStaff(ctx, q, actor.ID, "gift.refund_preview", gift.ID.String(), nil, now); err != nil {
			return RefundQuote{}, err
		}
		return RefundQuote{GiftID: gift.ID, DonorID: donorID, AmountMinor: gift.AmountMinor, AvailableMinor: remaining, Currency: "USD", Mode: "test"}, nil
	})
}

// reconcileStaffRefunds records the current state of each processor refund
// a staff command created for gift, completing commands whose outcome was
// lost, so the preview reflects every refund DAD asked for.
func reconcileStaffRefunds(ctx context.Context, q *queries.Queries, actor, donorID pgtype.UUID, gift queries.DonorGift, rows []payments.Object, now time.Time) error {
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
			return err
		}
		amount, err := current.Int("amount")
		if err != nil {
			return err
		}
		if command.Kind != "REFUND" || command.TargetID != gift.ID || command.DonorID != donorID || !command.AmountMinor.Valid || command.AmountMinor.Int64 != amount {
			continue
		}
		status := strings.ToUpper(current.Text("status"))
		previous, err := q.RefundByStripeID(ctx, current.Text("id"))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && previous.Status == status {
			continue
		}
		if err = recordRefund(ctx, q, gift, current, pgtype.UUID{}, command.ID, now); err != nil {
			return err
		}
		if err = q.SaveStaffAction(ctx, queries.SaveStaffActionParams{ID: command.ID, StripeRefundID: text(current.Text("id")), Status: "OBSERVED", CompletedAt: timestamp(now)}); err != nil {
			return err
		}
		if err = auditStaff(ctx, q, actor, "gift.refund_reconciled", command.ID.String(), map[string]string{"status": status}, now); err != nil {
			return err
		}
	}
	return nil
}

// CancellationQuote previews the donor's monthly gift for a staff
// cancellation, from stored state if the processor cannot be read.
func (s StaffFinanceService) CancellationQuote(ctx context.Context, token string, donorID pgtype.UUID) (RecurringDetails, error) {
	if _, err := requireStaff(ctx, queries.New(s.Staff.DB), token, "gift.cancel", time.Now().UTC()); err != nil {
		return RecurringDetails{}, err
	}
	recurring := s.recurring()
	snapshot, live, err := liveRead(ctx, func(ctx context.Context) (*subscriptionSnapshot, error) {
		return recurring.prefetchSubscription(ctx, donorID)
	})
	if err != nil {
		return RecurringDetails{}, err
	}
	if !live {
		return staffRead(ctx, s.Staff, token, "gift.cancel", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (RecurringDetails, error) {
			q := queries.New(tx)
			view, err := storedDetails(ctx, q, donorID)
			if err != nil {
				return RecurringDetails{}, err
			}
			if view.Subscription == nil {
				view = RecurringDetails{Mode: "test", Stale: view.Stale}
			}
			return view, auditStaff(ctx, q, actor.ID, "gift.cancel_preview", donorID.String(), map[string]bool{"stale": true}, now)
		})
	}
	return staffDonorWrite(ctx, s.Staff, token, "gift.cancel", donorID, func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (RecurringDetails, error) {
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, donorID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !attempt.StripeSubscriptionID.Valid) {
			return RecurringDetails{Mode: "test"}, nil
		}
		if err != nil {
			return RecurringDetails{}, err
		}
		row, _, err := recurring.read(ctx, tx, attempt, now, viewOnly, snapshot)
		if err != nil {
			return RecurringDetails{}, err
		}
		if err = auditStaff(ctx, q, actor.ID, "gift.cancel_preview", attempt.ID.String(), nil, now); err != nil {
			return RecurringDetails{}, err
		}
		return subscriptionView(ctx, q, attempt, row)
	})
}
