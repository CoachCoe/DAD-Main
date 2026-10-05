package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

func (s RecurringService) ChangeAmount(ctx context.Context, token string, r AmountRequest) (AmountChange, error) {
	if !ValidRequestID(r.RequestID) || r.Amount < 100 || r.Amount > 99999999 || r.Previous < 100 || r.Previous > 99999999 || r.EffectiveAt.IsZero() {
		return AmountChange{}, payments.ErrFacts
	}
	owner := donorOwner(token)
	ready, err := ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (*AmountChange, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return nil, err
		}
		command, err := q.AmountChangeByID(ctx, r.RequestID)
		if err == nil {
			if command.DonorID != d.ID || command.CheckoutID != attempt.CheckoutID || command.AmountMinor != r.Amount || command.PreviousMinor != r.Previous || !command.EffectiveAt.Time.Equal(r.EffectiveAt) {
				return nil, ErrSubscriptionConflict
			}
			if command.ConfirmedAt.Valid {
				return amountView(command), nil
			}
			return nil, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "amount", nil)
		if err != nil {
			return nil, err
		}
		draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
		if err != nil {
			return nil, err
		}
		prior, err := q.LatestAmountChange(ctx, draft.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil && !prior.ConfirmedAt.Valid {
			return nil, ErrSubscriptionConflict
		}
		previous, err := nextAmount(ctx, q, draft)
		if err != nil {
			return nil, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid || !schedule.PeriodEnd.Time.Equal(r.EffectiveAt) || previous != r.Previous || r.Amount == r.Previous || !now.Before(r.EffectiveAt.Add(-time.Minute)) {
			return nil, ErrSubscriptionConflict
		}
		itemID, product, err := amountChangeItem(remote)
		if err != nil {
			return nil, err
		}
		return nil, q.InsertAmountChange(ctx, queries.InsertAmountChangeParams{ID: r.RequestID, DonorID: d.ID, CheckoutID: draft.ID, PreviousMinor: r.Previous, AmountMinor: r.Amount, EffectiveAt: timestamp(r.EffectiveAt), ItemID: itemID, ProductID: product, CreatedAt: timestamp(now)})
	})
	if err != nil {
		return AmountChange{}, err
	}
	if ready != nil {
		return *ready, nil
	}
	return ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (AmountChange, error) {
		q := queries.New(tx)
		attempt, err := ownedAttempt(ctx, q, d.ID, r.AttemptID)
		if err != nil {
			return AmountChange{}, err
		}
		schedule, _, err := s.read(ctx, tx, attempt, now, "amount", nil)
		if err != nil {
			return AmountChange{}, err
		}
		command, err := q.AmountChangeByID(ctx, r.RequestID)
		if err != nil {
			return AmountChange{}, err
		}
		if !command.ConfirmedAt.Valid {
			if schedule.Status != "active" || schedule.StopsAt.Valid || !schedule.PeriodEnd.Time.Equal(command.EffectiveAt.Time) || !now.Before(command.EffectiveAt.Time.Add(-time.Minute)) || now.Sub(command.CreatedAt.Time) >= 23*time.Hour {
				return AmountChange{}, ErrSubscriptionConflict
			}
			if err = s.Gateway.ChangeMonthlyAmount(ctx, attempt.StripeSubscriptionID.String, command.ItemID, command.ProductID, command.AmountMinor, "dad-amount-"+command.ID.String()); err != nil {
				return AmountChange{}, err
			}
			if _, _, err = s.read(ctx, tx, attempt, now, "amount", nil); err != nil {
				return AmountChange{}, err
			}
			command, err = q.AmountChangeByID(ctx, r.RequestID)
			if err != nil {
				return AmountChange{}, err
			}
			if !command.ConfirmedAt.Valid {
				return AmountChange{}, ErrSubscriptionConflict
			}
		}
		return *amountView(command), nil
	})
}

// amountChangeItem returns the single monthly item and its product, which an
// amount change replaces in place.
func amountChangeItem(remote payments.Object) (string, string, error) {
	items, err := remote.Object("items")
	if err != nil {
		return "", "", err
	}
	lines, err := items.Array("data")
	if err != nil || len(lines) != 1 {
		return "", "", payments.ErrFacts
	}
	price, err := lines[0].Object("price")
	if err != nil {
		return "", "", err
	}
	itemID, product := lines[0].Text("id"), price.Text("product")
	if !strings.HasPrefix(product, "prod_") || !strings.HasPrefix(itemID, "si_") {
		return "", "", payments.ErrFacts
	}
	return itemID, product, nil
}
