package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/store/queries"
)

func (s StaffFinanceService) RefundQuote(ctx context.Context, token string, donorID, giftID pgtype.UUID) (map[string]interface{}, error) {
	return staffTransaction(ctx, s.Staff, token, "gift.refund", func(tx pgx.Tx, _ queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		actor, _, err := staffFinancialOwner(ctx, q, token, donorID, "gift.refund", now)
		if err != nil {
			return nil, err
		}
		gift, err := staffGift(ctx, q, donorID, giftID)
		if err != nil {
			return nil, err
		}
		rows, remaining, err := s.refunds(ctx, gift)
		if err != nil {
			return nil, err
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
				return nil, err
			}
			amount, err := current.Int("amount")
			if err != nil {
				return nil, err
			}
			if command.Kind != "REFUND" || command.TargetID != gift.ID || command.DonorID != donorID || !command.AmountMinor.Valid || command.AmountMinor.Int64 != amount {
				continue
			}
			previous, err := q.RefundByStripeID(ctx, current.Text("id"))
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			if err == nil && previous.Status == strings.ToUpper(current.Text("status")) {
				continue
			}
			if err = RecordRefundInTx(ctx, q, gift, current, pgtype.UUID{}, command.ID, now); err != nil {
				return nil, err
			}
			if err = q.SaveStaffAction(ctx, queries.SaveStaffActionParams{ID: command.ID, StripeRefundID: pgtype.Text{String: current.Text("id"), Valid: true}, Status: "OBSERVED", CompletedAt: timestamp(now)}); err != nil {
				return nil, err
			}
			if err = auditStaff(ctx, q, actor.ID, "gift.refund_reconciled", command.ID.String(), map[string]string{"status": strings.ToUpper(current.Text("status"))}, now); err != nil {
				return nil, err
			}
		}
		if _, err = RefreshProjectionInTx(ctx, tx, donorID, now); err != nil {
			return nil, err
		}
		if err = auditStaff(ctx, q, actor.ID, "gift.refund_preview", gift.ID.String(), nil, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"gift_id": gift.ID, "donor_id": donorID, "amount_minor": gift.AmountMinor, "available_minor": remaining, "currency": "USD", "mode": "test"}, nil
	})
}

func (s StaffFinanceService) CancellationQuote(ctx context.Context, token string, donorID pgtype.UUID) (map[string]interface{}, error) {
	return staffTransaction(ctx, s.Staff, token, "gift.cancel", func(tx pgx.Tx, _ queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		actor, _, err := staffFinancialOwner(ctx, q, token, donorID, "gift.cancel", now)
		if err != nil {
			return nil, err
		}
		attempt, err := q.LatestMonthlyAttempt(ctx, donorID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !attempt.StripeSubscriptionID.Valid) {
			return map[string]interface{}{"subscription": nil, "mode": "test"}, nil
		}
		if err != nil {
			return nil, err
		}
		recurring := RecurringService{DB: s.Staff.DB, Gateway: s.Gateway, Origin: s.Origin}
		row, _, err := recurring.read(ctx, tx, attempt, now, "")
		if err != nil {
			return nil, err
		}
		if err = auditStaff(ctx, q, actor.ID, "gift.cancel_preview", attempt.ID.String(), nil, now); err != nil {
			return nil, err
		}
		return subscriptionView(ctx, q, attempt, row)
	})
}
