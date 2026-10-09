package store

import (
	"context"
	"encoding/binary"
	"errors"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/recognition"
	"impactgraph/donor-api/internal/store/queries"
)

type history struct {
	Gifts   []recognition.Gift
	Refunds []recognition.Refund
	Periods []recognition.Period
}

func loadHistory(ctx context.Context, q *queries.Queries, donorID pgtype.UUID) (history, error) {
	var result history
	gifts, err := q.GiftHistory(ctx, donorID)
	if err != nil {
		return result, err
	}
	for _, g := range gifts {
		gift := recognition.Gift{ID: g.StripeObjectID, DonorID: g.DonorID.String(), Amount: g.AmountMinor, Currency: g.Currency, Frequency: g.Frequency, PaidAt: g.PaidAt.Time}
		if g.PeriodID.Valid {
			gift.PeriodID = g.PeriodID.String()
		}
		if g.ReferrerID.Valid {
			if !g.ReferralKind.Valid || !g.ReferralExpiresAt.Valid {
				return result, recognition.ErrInvalid
			}
			gift.Candidate = &recognition.Candidate{ReferrerID: g.ReferrerID.String(), Kind: g.ReferralKind.String, ExpiresAt: g.ReferralExpiresAt.Time}
		}
		result.Gifts = append(result.Gifts, gift)
	}
	refunds, err := q.RefundHistory(ctx, donorID)
	if err != nil {
		return result, err
	}
	for _, r := range refunds {
		result.Refunds = append(result.Refunds, recognition.Refund{ID: r.StripeRefundID, GiftID: r.StripeObjectID, Amount: r.AmountMinor, Currency: r.Currency, Status: r.Status, UpdatedAt: r.ObservedAt.Time})
	}
	periods, err := q.PeriodHistory(ctx, donorID)
	if err != nil {
		return result, err
	}
	for _, p := range periods {
		status := "EXPECTED"
		if p.Status == "FAILED" {
			status = "FAILED"
		}
		result.Periods = append(result.Periods, recognition.Period{ID: p.ID.String(), DonorID: p.DonorID.String(), DueAt: p.DueAt.Time, EndsAt: p.EndsAt.Time, Status: status})
	}
	return result, nil
}

// historicalBest replays refund observations made by asOf so a full refund
// cannot erase a best streak the donor had already attained.
func historicalBest(ctx context.Context, id pgtype.UUID, h history, observations []queries.RefundObservationsRow, now time.Time, previous int64) (int64, error) {
	byID := map[string]recognition.Refund{}
	for _, r := range h.Refunds {
		byID[r.ID] = r
	}
	observed := map[string]bool{}
	history := []recognition.Refund{}
	for _, o := range observations {
		r, ok := byID[o.StripeRefundID]
		if !ok {
			return 0, recognition.ErrInvalid
		}
		r.Status = o.Status
		r.UpdatedAt = o.ObservedAt.Time
		observed[r.ID] = true
		history = append(history, r)
	}
	for _, r := range h.Refunds {
		if !observed[r.ID] && !r.UpdatedAt.After(now) {
			history = append(history, r)
		}
	}
	return replayBest(ctx, id.String(), h, history, now, previous)
}

// refundsAsOf returns each refund's state as of asOf. A refund last updated
// after asOf, as another process with a later clock may record, takes its
// latest earlier observation; the later update is returned as a refresh boundary.
func refundsAsOf(current []recognition.Refund, observations []queries.RefundObservationsRow, asOf time.Time) ([]recognition.Refund, []time.Time) {
	latest := map[string]queries.RefundObservationsRow{}
	for _, o := range observations {
		latest[o.StripeRefundID] = o
	}
	result := make([]recognition.Refund, 0, len(current))
	var later []time.Time
	for _, r := range current {
		if r.UpdatedAt.After(asOf) {
			later = append(later, r.UpdatedAt)
			o, ok := latest[r.ID]
			if !ok {
				continue
			}
			r.Status, r.UpdatedAt = o.Status, o.ObservedAt.Time
		}
		result = append(result, r)
	}
	return result, later
}

func replayBest(ctx context.Context, donorID string, h history, history []recognition.Refund, now time.Time, previous int64) (int64, error) {
	slices.SortStableFunc(history, func(a, b recognition.Refund) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	gifts := slices.Clone(h.Gifts)
	slices.SortStableFunc(gifts, func(a, b recognition.Gift) int { return a.PaidAt.Compare(b.PaidAt) })
	points := map[time.Time]bool{}
	for _, g := range h.Gifts {
		if !g.PaidAt.After(now) {
			points[g.PaidAt.UTC()] = true
		}
	}
	for _, r := range history {
		points[r.UpdatedAt.UTC()] = true
	}
	ordered := slices.SortedFunc(maps.Keys(points), time.Time.Compare)
	best := previous
	latest := map[string]recognition.Refund{}
	paidPeriods := map[string]bool{}
	refundIndex, giftIndex := 0, 0
	for _, point := range ordered {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		for refundIndex < len(history) && !history[refundIndex].UpdatedAt.After(point) {
			r := history[refundIndex]
			latest[r.ID] = r
			refundIndex++
		}
		for giftIndex < len(gifts) && !gifts[giftIndex].PaidAt.After(point) {
			g := gifts[giftIndex]
			if g.DonorID == donorID && g.Frequency == "MONTHLY" {
				paidPeriods[g.PeriodID] = true
			}
			giftIndex++
		}
		// No history at this point can exceed its number of paid recurring periods.
		if int64(len(paidPeriods)) <= best {
			continue
		}
		refunds := slices.Collect(maps.Values(latest))
		value, err := recognition.GivingStreak(h.Gifts, refunds, h.Periods, donorID, point, best, nil, nil)
		if err == nil && value.Best > best {
			best = value.Best
		}
	}
	return best, nil
}

// projection is a donor's saved recognition with the start of the current
// streak run, which identifies the run but is not stored.
type projection struct {
	queries.SaveProjectionParams
	streakSince *time.Time
}

// projectionRules versions the rules a projection follows: recognition and
// the messages and push intents queued with it. Raise it with any change to
// them, so every saved projection is recomputed under the new rules.
const projectionRules = 1

// A projection waits at most refreshCap for its next time boundary. Facts that
// change a projection refresh it when they arrive, so this is a backstop, not a
// schedule.
const (
	minRefreshInterval = 25 * 24 * time.Hour
	maxRefreshInterval = 35 * 24 * time.Hour
)

// refreshCap is the donor's own wait between minRefreshInterval and
// maxRefreshInterval, taken from the random part of their ID, so projections
// computed together, as after a rules version bump, fall due over ten days
// rather than at once.
func refreshCap(donor pgtype.UUID) time.Duration {
	spread := uint64((maxRefreshInterval - minRefreshInterval) / time.Second)
	return minRefreshInterval + time.Duration(binary.BigEndian.Uint64(donor.Bytes[8:])%spread)*time.Second
}

// projectFacts recomputes and saves the donor's recognition projection
// under the donor lock.
func projectFacts(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time) (projection, error) {
	out := queries.SaveProjectionParams{DonorID: id, ComputedAt: timestamp(now), NextRefreshAt: timestamp(now.Add(refreshCap(id))), RulesVersion: projectionRules}
	if _, err := q.LockDonor(ctx, id); err != nil {
		return projection{SaveProjectionParams: out}, err
	}
	old, err := q.Projection(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if out.ID, err = NewID(); err != nil {
			return projection{SaveProjectionParams: out}, err
		}
	case err != nil:
		return projection{SaveProjectionParams: out}, err
	default:
		out.ID = old.ID
		out.FoundingAwardedAt = old.FoundingAwardedAt
		out.BestStreak = old.BestStreak
		out.FirstGiftID, out.ReferrerID, out.ReferralKind = old.FirstGiftID, old.ReferrerID, old.ReferralKind
	}
	h, err := loadHistory(ctx, q, id)
	if err != nil {
		return projection{SaveProjectionParams: out}, err
	}
	observations, err := q.RefundObservations(ctx, queries.RefundObservationsParams{DonorID: id, ObservedAt: timestamp(now)})
	if err != nil {
		return projection{SaveProjectionParams: out}, err
	}
	refunds, refundBoundaries := refundsAsOf(h.Refunds, observations, now)
	if err = projectConversion(&out, h, id, now); err != nil {
		return projection{SaveProjectionParams: out}, err
	}
	if out.NetMinor, err = netMinor(h.Gifts, refunds, now); err != nil {
		return projection{SaveProjectionParams: out}, err
	}
	best, err := historicalBest(ctx, id, h, observations, now, int64(out.BestStreak))
	if err != nil {
		return projection{SaveProjectionParams: out}, err
	}
	if best > math.MaxInt32 {
		return projection{SaveProjectionParams: out}, recognition.ErrInvalid
	}
	out.BestStreak = int32(best)
	stops, pauses, err := renewalStops(ctx, q, id, now)
	if err != nil {
		return projection{SaveProjectionParams: out}, err
	}
	streak, err := recognition.GivingStreak(h.Gifts, refunds, h.Periods, id.String(), now, best, stops, pauses)
	switch {
	case errors.Is(err, recognition.ErrIncomplete):
		out.NextRefreshAt = timestamp(now.Add(5 * time.Minute))
	case err != nil:
		return projection{SaveProjectionParams: out}, err
	default:
		if streak.Current > math.MaxInt32 || streak.Best > math.MaxInt32 {
			return projection{SaveProjectionParams: out}, recognition.ErrInvalid
		}
		out.CurrentStreak = pgtype.Int4{Int32: int32(streak.Current), Valid: true}
		out.BestStreak = int32(streak.Best)
		out.AtRisk = streak.AtRisk
		if streak.GraceEndsAt != nil {
			out.GraceEndsAt = timestamp(*streak.GraceEndsAt)
		}
	}
	out.NextRefreshAt = nextRefresh(out, h, refundBoundaries, stops, pauses, now)
	return projection{out, streak.Since}, q.SaveProjection(ctx, out)
}

// nextRefresh is the earliest moment after now at which the projection can
// change without new facts arriving, and at most its default refresh time.
func nextRefresh(out queries.SaveProjectionParams, h history, refundBoundaries, stops []time.Time, pauses []recognition.Pause, now time.Time) pgtype.Timestamptz {
	boundaries := slices.Concat(refundBoundaries, stops)
	for _, p := range h.Periods {
		boundaries = append(boundaries, p.DueAt, p.EndsAt, p.EndsAt.Add(recognition.Grace))
	}
	for _, g := range h.Gifts {
		boundaries = append(boundaries, g.PaidAt)
	}
	for _, p := range pauses {
		boundaries = append(boundaries, p.Start, p.End)
	}
	if out.GraceEndsAt.Valid {
		boundaries = append(boundaries, out.GraceEndsAt.Time)
	}
	// A year's summary is queued once that year ends in New York.
	if len(h.Gifts) > 0 {
		boundaries = append(boundaries, time.Date(now.In(recognition.NewYork).Year()+1, 1, 1, 0, 0, 0, 0, recognition.NewYork))
	}
	next := out.NextRefreshAt
	for _, b := range boundaries {
		if b.After(now) && b.Before(next.Time) {
			next = timestamp(b)
		}
	}
	return next
}

// projectConversion records the donor's first gift, its referral and the
// Founding Donor award. Both are permanent once set, so a gift processed late
// cannot move a relationship whose referral notice may already have been sent.
func projectConversion(out *queries.SaveProjectionParams, h history, id pgtype.UUID, now time.Time) error {
	if !out.FirstGiftID.Valid {
		relations, err := recognition.Relationships(h.Gifts, now)
		if err != nil {
			return err
		}
		if relation, ok := relations[id.String()]; ok {
			out.FirstGiftID = text(relation.FirstGiftID)
			if relation.ReferrerID != nil {
				if err = out.ReferrerID.Scan(*relation.ReferrerID); err != nil {
					return err
				}
				out.ReferralKind = text(*relation.Kind)
			}
		}
	}
	if out.FoundingAwardedAt.Valid {
		return nil
	}
	at, qualified, err := recognition.FoundingQualifiedAt(h.Gifts, id.String(), now)
	if qualified {
		out.FoundingAwardedAt = timestamp(at)
	}
	return err
}

func netMinor(gifts []recognition.Gift, refunds []recognition.Refund, now time.Time) (pgtype.Int8, error) {
	net, err := recognition.NetGifts(gifts, refunds, now)
	if err != nil {
		return pgtype.Int8{}, err
	}
	total := int64(0)
	for _, amount := range net {
		if amount > math.MaxInt64-total {
			return pgtype.Int8{}, recognition.ErrInvalid
		}
		total += amount
	}
	return pgtype.Int8{Int64: total, Valid: true}, nil
}

// renewalStops returns the renewal boundaries ended by cancellation and the
// pause windows that skip renewals.
func renewalStops(ctx context.Context, q *queries.Queries, id pgtype.UUID, now time.Time) ([]time.Time, []recognition.Pause, error) {
	windows, err := q.PauseWindows(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	pauses := make([]recognition.Pause, 0, len(windows))
	for _, w := range windows {
		end := w.ResumesAt.Time
		if w.NextRenewal.Valid {
			end = w.NextRenewal.Time
		}
		if w.StopsAt.Valid && !w.StopsAt.Time.After(now) {
			end = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
		}
		pauses = append(pauses, recognition.Pause{Start: w.StartsAt.Time, End: end})
	}
	ended, err := q.StoppedPeriods(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	stops := make([]time.Time, 0, len(ended))
	for _, v := range ended {
		stops = append(stops, v.Time)
	}
	return stops, pauses, nil
}
