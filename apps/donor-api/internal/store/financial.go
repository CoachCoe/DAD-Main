package store

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/recognition"
	"impactgraph/donor-api/internal/store/queries"
)

type History struct {
	Gifts   []recognition.Gift
	Refunds []recognition.Refund
	Periods []recognition.Period
}

func LoadHistory(ctx context.Context, q *queries.Queries, donorID pgtype.UUID) (History, error) {
	var result History
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

func historicalBest(ctx context.Context, q *queries.Queries, id pgtype.UUID, h History, now time.Time, previous int64) (int64, error) {
	observations, err := q.RefundObservations(ctx, queries.RefundObservationsParams{DonorID: id, ObservedAt: timestamp(now)})
	if err != nil {
		return 0, err
	}
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
	sort.SliceStable(history, func(i, j int) bool { return history[i].UpdatedAt.Before(history[j].UpdatedAt) })
	points := map[time.Time]bool{}
	for _, g := range h.Gifts {
		if !g.PaidAt.After(now) {
			points[g.PaidAt.UTC()] = true
		}
	}
	for _, r := range history {
		points[r.UpdatedAt.UTC()] = true
	}
	ordered := make([]time.Time, 0, len(points))
	for point := range points {
		ordered = append(ordered, point)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Before(ordered[j]) })
	best := previous
	for _, point := range ordered {
		latest := map[string]recognition.Refund{}
		for _, r := range history {
			if !r.UpdatedAt.After(point) {
				latest[r.ID] = r
			}
		}
		refunds := make([]recognition.Refund, 0, len(latest))
		for _, r := range latest {
			refunds = append(refunds, r)
		}
		value, err := recognition.GivingStreak(h.Gifts, refunds, h.Periods, id.String(), point, best, nil, nil)
		if err == nil && value.Best > best {
			best = value.Best
		}
	}
	return best, nil
}

// RefreshProjectionFactsInTx writes only recognition facts. The orchestration layer
// must queue financial messages in this transaction before it commits. No worker or
// public mutation route calls this until the message outbox migration is complete.
func RefreshProjectionFactsInTx(ctx context.Context, tx pgx.Tx, id pgtype.UUID, now time.Time) (queries.SaveProjectionParams, error) {
	q := queries.New(tx)
	out := queries.SaveProjectionParams{DonorID: id, ComputedAt: timestamp(now), NextRefreshAt: timestamp(now.Add(24 * time.Hour))}
	if _, err := q.LockDonor(ctx, id); err != nil {
		return out, err
	}
	old, err := q.Projection(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		out.ID, err = NewID()
		if err != nil {
			return out, err
		}
	} else {
		out.ID = old.ID
		out.FoundingAwardedAt = old.FoundingAwardedAt
		out.BestStreak = old.BestStreak
	}
	h, err := LoadHistory(ctx, q, id)
	if err != nil {
		return out, err
	}
	relations, err := recognition.Relationships(h.Gifts, now)
	if err != nil {
		return out, err
	}
	if relation, ok := relations[id.String()]; ok {
		out.FirstGiftID = pgtype.Text{String: relation.FirstGiftID, Valid: true}
		if relation.ReferrerID != nil {
			if err = out.ReferrerID.Scan(*relation.ReferrerID); err != nil {
				return out, err
			}
			out.ReferralKind = pgtype.Text{String: *relation.Kind, Valid: true}
		}
	}
	founded, err := recognition.Founding(h.Gifts, id.String(), now, out.FoundingAwardedAt.Valid)
	if err != nil {
		return out, err
	}
	if founded && !out.FoundingAwardedAt.Valid {
		out.FoundingAwardedAt = timestamp(now)
	}
	net, netErr := recognition.NetGifts(h.Gifts, h.Refunds, now)
	if netErr == nil {
		total := int64(0)
		for _, amount := range net {
			if amount > math.MaxInt64-total {
				netErr = recognition.ErrInvalid
				break
			}
			total += amount
		}
		if netErr == nil {
			out.NetMinor = pgtype.Int8{Int64: total, Valid: true}
		}
	}
	best, err := historicalBest(ctx, q, id, h, now, int64(out.BestStreak))
	if err != nil {
		return out, err
	}
	if best > math.MaxInt32 {
		return out, recognition.ErrInvalid
	}
	out.BestStreak = int32(best)
	windows, err := q.PauseWindows(ctx, id)
	if err != nil {
		return out, err
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
		return out, err
	}
	stops := make([]time.Time, 0, len(ended))
	for _, v := range ended {
		stops = append(stops, v.Time)
	}
	streak, err := recognition.GivingStreak(h.Gifts, h.Refunds, h.Periods, id.String(), now, best, stops, pauses)
	if err != nil {
		out.NextRefreshAt = timestamp(now.Add(5 * time.Minute))
	} else {
		if streak.Current > math.MaxInt32 || streak.Best > math.MaxInt32 {
			return out, recognition.ErrInvalid
		}
		out.CurrentStreak = pgtype.Int4{Int32: int32(streak.Current), Valid: true}
		out.BestStreak = int32(streak.Best)
		out.AtRisk = streak.AtRisk
		if streak.GraceEndsAt != nil {
			out.GraceEndsAt = timestamp(*streak.GraceEndsAt)
		}
	}
	boundaries := append([]time.Time{}, stops...)
	for _, p := range h.Periods {
		boundaries = append(boundaries, p.DueAt, p.EndsAt)
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
	for _, b := range boundaries {
		if b.After(now) && b.Before(out.NextRefreshAt.Time) {
			out.NextRefreshAt = timestamp(b)
		}
	}
	return out, q.SaveProjection(ctx, out)
}
