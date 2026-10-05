package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"impactgraph/donor-api/internal/store/queries"
)

// AmountOptions offers a new amount from the next renewal. An unavailable
// offer carries only its state.
type AmountOptions struct {
	AmountMinor    int64         `json:"amount_minor"`
	Change         *AmountChange `json:"change"`
	EffectiveAt    time.Time     `json:"effective_at"`
	State          string        `json:"state"`
	SubscriptionID string        `json:"subscription_id"`
}

func (o AmountOptions) MarshalJSON() ([]byte, error) {
	if o.State != "ready" {
		return json.Marshal(struct {
			State string `json:"state"`
		}{o.State})
	}
	type options AmountOptions
	return json.Marshal(options(o))
}

type PauseOption struct {
	Months    int32     `json:"months"`
	ResumesAt time.Time `json:"resumes_at"`
	StartsAt  time.Time `json:"starts_at"`
}
type PauseOptions struct {
	Options        []PauseOption `json:"options"`
	Pause          *PauseView    `json:"pause"`
	State          string        `json:"state"`
	SubscriptionID string        `json:"subscription_id,omitempty"`
}

// ResumeOptions is unavailable, a saved resumption, or an offer to resume at
// the next renewal; each state carries only its own fields.
type ResumeOptions struct {
	Confirmed      *bool      `json:"confirmed,omitempty"`
	NextRenewal    *time.Time `json:"next_renewal,omitempty"`
	OriginalResume *time.Time `json:"original_resume,omitempty"`
	PauseID        string     `json:"pause_id,omitempty"`
	RequestID      string     `json:"request_id,omitempty"`
	State          string     `json:"state"`
}

func (s RecurringService) AmountOptions(ctx context.Context, token string) (AmountOptions, error) {
	unavailable := AmountOptions{State: "unavailable"}
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return AmountOptions{}, err
	}
	return ownerTransaction(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (AmountOptions, error) {
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return AmountOptions{}, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return unavailable, nil
		}
		schedule, _, err := s.read(ctx, tx, attempt, now, "amount", snapshot)
		if err != nil {
			return AmountOptions{}, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid {
			return unavailable, nil
		}
		draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
		if err != nil {
			return AmountOptions{}, err
		}
		amount, err := nextAmount(ctx, q, draft)
		if err != nil {
			return AmountOptions{}, err
		}
		out := AmountOptions{State: "ready", SubscriptionID: attempt.ID.String(), AmountMinor: amount, EffectiveAt: schedule.PeriodEnd.Time}
		change, err := q.LatestAmountChange(ctx, draft.ID)
		switch {
		case err == nil:
			out.Change = amountView(change)
		case !errors.Is(err, pgx.ErrNoRows):
			return AmountOptions{}, err
		}
		return out, nil
	})
}
func (s RecurringService) PauseOptions(ctx context.Context, token string) (PauseOptions, error) {
	unavailable := PauseOptions{State: "unavailable", Options: []PauseOption{}}
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return PauseOptions{}, err
	}
	return ownerTransaction(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (PauseOptions, error) {
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return PauseOptions{}, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return unavailable, nil
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause", snapshot)
		if err != nil {
			return PauseOptions{}, err
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return PauseOptions{}, err
		}
		if schedule.StopsAt.Valid || terminalSubscription(schedule.Status) {
			return unavailable, nil
		}
		out := PauseOptions{State: "ready", SubscriptionID: attempt.ID.String(), Options: []PauseOption{}}
		pause, err := q.LatestPause(ctx, attempt.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return PauseOptions{}, err
		}
		active := false
		if err == nil {
			until, err := effectiveResume(ctx, q, pause)
			if err != nil {
				return PauseOptions{}, err
			}
			active = !pause.ConfirmedAt.Valid || now.Before(until)
		}
		if active {
			view, err := pauseView(ctx, q, pause)
			if err != nil {
				return PauseOptions{}, err
			}
			out.Pause = &view
		} else if schedule.Status == "active" {
			anchor, err := billingAnchor(remote)
			if err != nil {
				return PauseOptions{}, err
			}
			for months := int32(1); months <= 3; months++ {
				until, err := renewalAfter(schedule.PeriodEnd.Time, months, anchor)
				if err != nil {
					return PauseOptions{}, err
				}
				out.Options = append(out.Options, PauseOption{Months: months, StartsAt: schedule.PeriodEnd.Time, ResumesAt: until})
			}
		}
		return out, nil
	})
}
func (s RecurringService) ResumeOptions(ctx context.Context, token string) (ResumeOptions, error) {
	unavailable := ResumeOptions{State: "unavailable"}
	snapshot, err := s.donorSnapshot(ctx, token)
	if err != nil {
		return ResumeOptions{}, err
	}
	return ownerTransaction(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (ResumeOptions, error) {
		q := queries.New(tx)
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return ResumeOptions{}, err
		}
		if !attempt.StripeSubscriptionID.Valid {
			return unavailable, nil
		}
		schedule, remote, err := s.read(ctx, tx, attempt, now, "pause", snapshot)
		if err != nil {
			return ResumeOptions{}, err
		}
		if _, err = RefreshProjectionInTx(ctx, tx, d.ID, now); err != nil {
			return ResumeOptions{}, err
		}
		pause, err := q.LatestPause(ctx, attempt.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return unavailable, nil
		}
		if err != nil {
			return ResumeOptions{}, err
		}
		if !pause.ConfirmedAt.Valid || schedule.StopsAt.Valid || schedule.Status != "active" {
			return unavailable, nil
		}
		command, err := q.ResumptionForPause(ctx, pause.ID)
		if err == nil {
			saved := resumptionView(command)
			return ResumeOptions{State: "saved", Confirmed: &saved.Confirmed, NextRenewal: &saved.NextRenewal, PauseID: saved.PauseID, RequestID: saved.RequestID}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ResumeOptions{}, err
		}
		if absent(remote, "pause_collection") || !schedule.PeriodEnd.Time.Before(pause.ResumesAt.Time) || !now.Before(schedule.PeriodEnd.Time) {
			return unavailable, nil
		}
		return ResumeOptions{State: "ready", PauseID: pause.ID.String(), NextRenewal: &schedule.PeriodEnd.Time, OriginalResume: &pause.ResumesAt.Time}, nil
	})
}
