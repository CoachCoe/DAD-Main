package store

import (
	"context"
	"errors"
	"time"

	"dad/donor-api/internal/recognition"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5/pgtype"
)

func saveRetention(ctx context.Context, q *queries.Queries, donor pgtype.UUID, h history,
	observations []queries.RefundObservationsRow, stops []time.Time, zone *time.Location, now time.Time) error {
	var first time.Time
	for _, g := range h.Gifts {
		if g.Frequency == "MONTHLY" && !g.PaidAt.After(now) && (first.IsZero() || g.PaidAt.Before(first)) {
			first = g.PaidAt
		}
	}
	if first.IsZero() {
		return nil
	}
	for _, days := range []int32{30, 90, 180, 365} {
		checkpoint := first.In(zone).AddDate(0, 0, int(days)).UTC()
		var retained pgtype.Bool
		if !checkpoint.After(now) {
			var earlier []queries.RefundObservationsRow
			for _, o := range observations {
				if !o.ObservedAt.Time.After(checkpoint) {
					earlier = append(earlier, o)
				}
			}
			refunds, _ := refundsAsOf(h.Refunds, earlier, checkpoint)
			streak, err := recognition.GivingStreak(h.Gifts, refunds, h.Periods, donor.String(), checkpoint, 0, stops)
			if err != nil && !errors.Is(err, recognition.ErrIncomplete) {
				return err
			}
			if err == nil {
				retained = pgtype.Bool{Bool: streak.Since != nil && !streak.Since.After(first), Valid: true}
			}
		}
		if err := q.SaveRetentionMilestone(ctx, queries.SaveRetentionMilestoneParams{
			DonorID: donor, Days: days, CohortAt: timestamp(first), CheckpointAt: timestamp(checkpoint),
			Retained: retained, ComputedAt: timestamp(now),
		}); err != nil {
			return err
		}
	}
	return nil
}
