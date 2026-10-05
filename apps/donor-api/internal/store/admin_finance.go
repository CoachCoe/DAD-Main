package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type StaffFinanceService struct {
	Staff   *StaffService
	Gateway payments.StaffFinanceGateway
	Origin  string
}

type StaffFinancialRequest struct {
	RequestID, DonorID, TargetID pgtype.UUID
	Reason                       string
	Amount                       int64
	PeriodEnd                    time.Time
}

func staffFinancialOwner(ctx context.Context, q *queries.Queries, token string, id pgtype.UUID, permission string, now time.Time) (queries.DonorStaff, queries.Donor, error) {
	actor, err := requireStaff(ctx, q, token, permission, now)
	var donor queries.Donor
	if err != nil {
		return actor, donor, err
	}
	if _, err = q.StaffByID(ctx, actor.ID); err != nil {
		return actor, donor, err
	}
	actor, err = requireStaff(ctx, q, token, permission, time.Now().UTC())
	if err != nil {
		return actor, donor, err
	}
	donor, err = q.LockDonor(ctx, id)
	if err == nil {
		actor, err = requireStaff(ctx, q, token, permission, time.Now().UTC())
	}
	return actor, donor, err
}

func staffGift(ctx context.Context, q *queries.Queries, donor, id pgtype.UUID) (queries.DonorGift, error) {
	gift, err := q.StaffGift(ctx, queries.StaffGiftParams{DonorID: donor, ID: id})
	if err == nil && !gift.StripePaymentIntentID.Valid {
		err = payments.ErrFacts
	}
	return gift, err
}

func (s StaffFinanceService) refunds(ctx context.Context, gift queries.DonorGift) ([]payments.Object, int64, error) {
	if s.Gateway == nil {
		return nil, 0, payments.ErrCheckoutUnavailable
	}
	rows, err := s.Gateway.ListPaymentRefunds(ctx, gift.StripePaymentIntentID.String)
	if err != nil {
		return nil, 0, err
	}
	ids := make(map[string]bool, len(rows))
	remaining := gift.AmountMinor
	for _, row := range rows {
		id := row.Text("id")
		amount, err := row.Int("amount")
		if err != nil || !strings.HasPrefix(id, "re_") || ids[id] || row.Text("payment_intent") != gift.StripePaymentIntentID.String || row.Text("currency") != "usd" || amount <= 0 {
			return nil, 0, payments.ErrFacts
		}
		ids[id] = true
		switch row.Text("status") {
		case "succeeded", "pending", "requires_action":
			if amount > remaining {
				return nil, 0, payments.ErrFacts
			}
			remaining -= amount
		case "failed", "canceled":
		default:
			return nil, 0, payments.ErrFacts
		}
	}
	return rows, remaining, nil
}

func (s StaffFinanceService) stage(ctx context.Context, token, kind string, r StaffFinancialRequest) error {
	r.Reason = identity.TrimSpace(r.Reason)
	if !ValidRequestID(r.RequestID) || utf8.RuneCountInString(r.Reason) < 5 || utf8.RuneCountInString(r.Reason) > 500 {
		return ErrStaffInput
	}
	permission := "gift.cancel"
	var amount pgtype.Int8
	var period pgtype.Timestamptz
	if kind == "REFUND" {
		permission = "gift.refund"
		if r.Amount < 1 || r.Amount > 99999999 {
			return ErrStaffInput
		}
		amount = pgtype.Int8{Int64: r.Amount, Valid: true}
	} else if kind == "CANCEL" && !r.PeriodEnd.IsZero() {
		period = timestamp(r.PeriodEnd)
	} else {
		return ErrStaffInput
	}
	_, err := staffTransaction(ctx, s.Staff, token, permission, func(tx pgx.Tx, _ queries.DonorStaff, now time.Time) (bool, error) {
		q := queries.New(tx)
		actor, _, err := staffFinancialOwner(ctx, q, token, r.DonorID, permission, now)
		if err != nil {
			return false, err
		}
		command, err := q.StaffActionByID(ctx, r.RequestID)
		if err == nil {
			if command.ActorID != actor.ID || command.DonorID != r.DonorID || command.Kind != kind || command.TargetID != r.TargetID || command.Reason != r.Reason || command.AmountMinor != amount || command.PeriodEnd.Valid != period.Valid || (period.Valid && !command.PeriodEnd.Time.Equal(period.Time)) {
				return false, ErrStaffConflict
			}
			return true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		if kind == "REFUND" {
			gift, err := staffGift(ctx, q, r.DonorID, r.TargetID)
			if err != nil {
				return false, err
			}
			if r.Amount > gift.AmountMinor {
				return false, ErrStaffConflict
			}
			pending, err := q.PendingStaffRefund(ctx, r.TargetID)
			if err != nil {
				return false, err
			}
			if pending {
				return false, ErrStaffConflict
			}
		} else {
			if _, err = ownedAttempt(ctx, q, r.DonorID, r.TargetID); err != nil {
				return false, err
			}
			if _, err = q.CancellationByID(ctx, r.RequestID); err == nil {
				return false, ErrStaffConflict
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return false, err
			}
		}
		if err = q.InsertStaffAction(ctx, queries.InsertStaffActionParams{ID: r.RequestID, ActorID: actor.ID, DonorID: r.DonorID, Kind: kind, TargetID: r.TargetID, Reason: r.Reason, AmountMinor: amount, PeriodEnd: period, CreatedAt: timestamp(now)}); err != nil {
			return false, err
		}
		return true, auditStaff(ctx, q, actor.ID, "gift.action_authorized", r.RequestID.String(), map[string]string{"kind": kind, "target_id": r.TargetID.String()}, now)
	})
	return err
}

func (s StaffFinanceService) Refund(ctx context.Context, token string, r StaffFinancialRequest) (map[string]interface{}, error) {
	if s.Gateway == nil {
		return nil, payments.ErrCheckoutUnavailable
	}
	if err := s.stage(ctx, token, "REFUND", r); err != nil {
		return nil, err
	}
	return staffTransaction(ctx, s.Staff, token, "gift.refund", func(tx pgx.Tx, _ queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		actor, _, err := staffFinancialOwner(ctx, q, token, r.DonorID, "gift.refund", now)
		if err != nil {
			return nil, err
		}
		command, err := q.StaffActionByID(ctx, r.RequestID)
		if err != nil {
			return nil, err
		}
		gift, err := staffGift(ctx, q, r.DonorID, r.TargetID)
		if err != nil {
			return nil, err
		}
		notSubmitted := map[string]interface{}{"request_id": command.ID, "status": "NOT_SUBMITTED", "mode": "test"}
		if command.Status == "ABANDONED" {
			return notSubmitted, nil
		}
		rows, remaining, err := s.refunds(ctx, gift)
		if err != nil {
			return nil, err
		}
		var current payments.Object
		for _, row := range rows {
			metadata, err := row.Object("metadata")
			if err == nil && metadata.Text("dad_staff_action") == command.ID.String() {
				if current != nil {
					return nil, payments.ErrFacts
				}
				current = row
			}
		}
		if current == nil {
			if command.StripeRefundID.Valid || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
				return nil, ErrStaffConflict
			}
			if r.Amount > remaining {
				if err = q.SaveStaffAction(ctx, queries.SaveStaffActionParams{ID: command.ID, Status: "ABANDONED", CompletedAt: timestamp(now)}); err != nil {
					return nil, err
				}
				if err = auditStaff(ctx, q, actor.ID, "gift.refund_not_submitted", command.ID.String(), map[string]string{"reason": "available_amount_changed"}, now); err != nil {
					return nil, err
				}
				return notSubmitted, nil
			}
			current, err = s.Gateway.CreateRefund(ctx, gift.StripePaymentIntentID.String, r.Amount, command.ID.String())
			if err != nil {
				return nil, err
			}
		}
		amount, err := current.Int("amount")
		metadata, metaErr := current.Object("metadata")
		if err != nil || metaErr != nil || amount != r.Amount || metadata.Text("dad_staff_action") != command.ID.String() || (command.StripeRefundID.Valid && command.StripeRefundID.String != current.Text("id")) {
			return nil, payments.ErrFacts
		}
		if err = RecordRefundInTx(ctx, q, gift, current, pgtype.UUID{}, command.ID, now); err != nil {
			return nil, err
		}
		refund, err := q.RefundByStripeID(ctx, current.Text("id"))
		if err != nil {
			return nil, err
		}
		if err = q.SaveStaffAction(ctx, queries.SaveStaffActionParams{ID: command.ID, StripeRefundID: pgtype.Text{String: refund.StripeRefundID, Valid: true}, Status: "OBSERVED", CompletedAt: timestamp(now)}); err != nil {
			return nil, err
		}
		if err = auditStaff(ctx, q, actor.ID, "gift.refund_observed", command.ID.String(), map[string]interface{}{"status": refund.Status, "amount_minor": refund.AmountMinor}, now); err != nil {
			return nil, err
		}
		if _, err = RefreshProjectionInTx(ctx, tx, r.DonorID, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"request_id": command.ID, "refund_id": refund.ID, "status": refund.Status, "amount_minor": refund.AmountMinor, "currency": "USD", "mode": "test"}, nil
	})
}

func (s StaffFinanceService) Cancel(ctx context.Context, token string, r StaffFinancialRequest) (map[string]interface{}, error) {
	if s.Gateway == nil {
		return nil, payments.ErrCheckoutUnavailable
	}
	if err := s.stage(ctx, token, "CANCEL", r); err != nil {
		return nil, err
	}
	owner := func(ctx context.Context, tx pgx.Tx, now time.Time) (queries.Donor, error) {
		q := queries.New(tx)
		_, donor, err := staffFinancialOwner(ctx, q, token, r.DonorID, "gift.cancel", now)
		if err != nil {
			return donor, err
		}
		command, err := q.StaffActionByID(ctx, r.RequestID)
		if err != nil {
			return donor, err
		}
		if command.Status == "ABANDONED" {
			return donor, ErrStaffConflict
		}
		return donor, nil
	}
	recurring := RecurringService{DB: s.Staff.DB, Gateway: s.Gateway, Origin: s.Origin}
	result, err := recurring.cancelAuthorized(ctx, owner, CancellationRequest{RequestID: r.RequestID, AttemptID: r.TargetID, PeriodEnd: r.PeriodEnd})
	if err != nil {
		return nil, err
	}
	_, err = staffTransaction(ctx, s.Staff, token, "gift.cancel", func(tx pgx.Tx, _ queries.DonorStaff, now time.Time) (bool, error) {
		q := queries.New(tx)
		actor, _, err := staffFinancialOwner(ctx, q, token, r.DonorID, "gift.cancel", now)
		if err != nil {
			return false, err
		}
		command, err := q.StaffActionByID(ctx, r.RequestID)
		if err != nil {
			return false, err
		}
		if command.CompletedAt.Valid {
			return true, nil
		}
		if err = q.SaveStaffAction(ctx, queries.SaveStaffActionParams{ID: command.ID, Status: "OBSERVED", CompletedAt: timestamp(now)}); err != nil {
			return false, err
		}
		return true, auditStaff(ctx, q, actor.ID, "gift.cancellation_confirmed", command.ID.String(), nil, now)
	})
	if err != nil {
		return nil, err
	}
	result["request_id"] = r.RequestID
	return result, nil
}
