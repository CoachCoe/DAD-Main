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

// Options are offered from the subscription as last synced; each command
// checks the processor's current state before it changes anything.

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

// latestSubscription returns the donor's latest monthly attempt, locked, and
// whether it has a subscription to act on yet.
func latestSubscription(ctx context.Context, q *queries.Queries, donor pgtype.UUID) (queries.DonorStripeCheckout, bool, error) {
	attempt, err := q.LatestMonthlyAttempt(ctx, donor)
	if errors.Is(err, pgx.ErrNoRows) {
		return attempt, false, nil
	}
	return attempt, err == nil && attempt.StripeSubscriptionID.Valid, err
}

// voiding reports whether the processor is voiding attempt's renewals: a sync
// confirms a pause or resumption only once the processor shows it, and the
// voiding lasts until its resumption date.
func voiding(ctx context.Context, q *queries.Queries, attempt pgtype.UUID, now time.Time) (bool, error) {
	pause, err := q.LatestPause(ctx, attempt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !pause.ConfirmedAt.Valid) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	until, err := effectiveResume(ctx, q, pause)
	return now.Before(until), err
}

// AmountOptions offers the donor's next-renewal amount change.
func (s RecurringService) AmountOptions(ctx context.Context, token string) (AmountOptions, error) {
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, now time.Time) (AmountOptions, error) {
		unavailable := AmountOptions{State: "unavailable"}
		attempt, schedule, err := syncedSubscription(ctx, q, d.ID)
		if err != nil || schedule == nil {
			return unavailable, err
		}
		paused, err := voiding(ctx, q, attempt.ID, now)
		if err != nil {
			return AmountOptions{}, err
		}
		if schedule.Status != "active" || schedule.StopsAt.Valid || schedule.ExternalChange || paused {
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

// PauseOptions offers pauses of 1 to 3 renewals with their exact resumption
// dates, or describes the pause in effect.
func (s RecurringService) PauseOptions(ctx context.Context, token string) (PauseOptions, error) {
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, now time.Time) (PauseOptions, error) {
		unavailable := PauseOptions{State: "unavailable", Options: []PauseOption{}}
		attempt, schedule, err := syncedSubscription(ctx, q, d.ID)
		if err != nil || schedule == nil {
			return unavailable, err
		}
		if schedule.StopsAt.Valid || terminalSubscription(schedule.Status) || schedule.ExternalChange {
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
			if !schedule.BillingAnchor.Valid {
				return unavailable, nil
			}
			for months := int32(1); months <= 3; months++ {
				until, err := renewalAfter(schedule.PeriodEnd.Time, months, schedule.BillingAnchor.Time)
				if err != nil {
					return PauseOptions{}, err
				}
				out.Options = append(out.Options, PauseOption{Months: months, StartsAt: schedule.PeriodEnd.Time, ResumesAt: until})
			}
		}
		return out, nil
	})
}

// ResumeOptions offers to end a confirmed pause at the next renewal, or
// describes the resumption already requested.
func (s RecurringService) ResumeOptions(ctx context.Context, token string) (ResumeOptions, error) {
	return donorRead(ctx, s.DB, token, func(q *queries.Queries, d queries.Donor, now time.Time) (ResumeOptions, error) {
		unavailable := ResumeOptions{State: "unavailable"}
		attempt, schedule, err := syncedSubscription(ctx, q, d.ID)
		if err != nil || schedule == nil {
			return unavailable, err
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
		// A confirmed pause still runs until its resumption date, so the
		// processor is voiding the renewal offered here.
		if schedule.ExternalChange || !schedule.PeriodEnd.Time.Before(pause.ResumesAt.Time) || !now.Before(schedule.PeriodEnd.Time) {
			return unavailable, nil
		}
		return ResumeOptions{State: "ready", PauseID: pause.ID.String(), NextRenewal: &schedule.PeriodEnd.Time, OriginalResume: &pause.ResumesAt.Time}, nil
	})
}
