package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"impactgraph/donor-api/internal/store/queries"
)

func (s RecurringService) AmountOptions(ctx context.Context, token string) (map[string]interface{}, error) {
	return s.transaction(ctx, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		unavailable := map[string]interface{}{"state": "unavailable"}
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return nil, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return unavailable, nil
		}
		schedule, _, err := s.read(ctx, tx, attempt, now, "amount")
		if err != nil {
			return nil, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid {
			return unavailable, nil
		}
		draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
		if err != nil {
			return nil, err
		}
		amount, err := nextAmount(ctx, q, draft)
		if err != nil {
			return nil, err
		}
		change, err := q.LatestAmountChange(ctx, draft.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var view interface{}
		if err == nil {
			view = amountView(change)
		}
		return map[string]interface{}{"state": "ready", "subscription_id": attempt.ID.String(), "amount_minor": amount, "effective_at": schedule.PeriodEnd.Time, "change": view}, nil
	})
}
func (s RecurringService) PauseOptions(ctx context.Context, token string) (map[string]interface{}, error) {
	return s.transaction(ctx, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		options := []map[string]interface{}{}
		unavailable := map[string]interface{}{"state": "unavailable", "pause": nil, "options": options}
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return nil, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return unavailable, nil
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause")
		if err != nil {
			return nil, err
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return nil, err
		}
		if schedule.StopsAt.Valid || terminalSubscription(schedule.Status) {
			return unavailable, nil
		}
		pause, err := q.LatestPause(ctx, attempt.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		active := false
		if err == nil {
			until, err := effectiveResume(ctx, q, pause)
			if err != nil {
				return nil, err
			}
			active = !pause.ConfirmedAt.Valid || now.Before(until)
		}
		var view interface{}
		if active {
			view, err = pauseView(ctx, q, pause)
			if err != nil {
				return nil, err
			}
		} else if schedule.Status == "active" {
			epoch, _ := remote.Int("billing_cycle_anchor")
			anchor := time.Unix(epoch, 0).UTC()
			for months := int32(1); months <= 3; months++ {
				until, err := renewalAfter(schedule.PeriodEnd.Time, months, anchor)
				if err != nil {
					return nil, err
				}
				options = append(options, map[string]interface{}{"months": months, "starts_at": schedule.PeriodEnd.Time, "resumes_at": until})
			}
		}
		return map[string]interface{}{"state": "ready", "subscription_id": attempt.ID.String(), "pause": view, "options": options}, nil
	})
}
func (s RecurringService) ResumeOptions(ctx context.Context, token string) (map[string]interface{}, error) {
	return s.transaction(ctx, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		unavailable := map[string]interface{}{"state": "unavailable"}
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return nil, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return unavailable, nil
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause")
		if err != nil {
			return nil, err
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return nil, err
		}
		pause, err := q.LatestPause(ctx, attempt.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return nil, err
		}
		if !pause.ConfirmedAt.Valid || schedule.StopsAt.Valid || schedule.Status != "active" {
			return unavailable, nil
		}
		command, err := q.ResumptionForPause(ctx, pause.ID)
		if err == nil {
			view := resumptionView(command)
			view["state"] = "saved"
			return view, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if absent(remote, "pause_collection") || !schedule.PeriodEnd.Time.Before(pause.ResumesAt.Time) || !now.Before(schedule.PeriodEnd.Time) {
			return unavailable, nil
		}
		return map[string]interface{}{"state": "ready", "pause_id": pause.ID.String(), "next_renewal": schedule.PeriodEnd.Time, "original_resume": pause.ResumesAt.Time}, nil
	})
}
