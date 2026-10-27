package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RecomputeRecognition repairs derived state after a documented system error.
// The recorded payment history remains the authority for continuity and awards.
func (s *StaffService) RecomputeRecognition(ctx context.Context, token string, donor pgtype.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 10 || n > 1000 || !plainText(reason, true) {
		return ErrStaffInput
	}
	_, err := staffDonorWrite(
		ctx,
		s,
		token,
		"donor.recompute",
		donor,
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (struct{}, error) {
			q := queries.New(tx)
			before, err := q.Projection(ctx, donor)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, err
			}
			after, err := refreshProjection(ctx, tx, donor, now)
			if err != nil {
				return struct{}{}, err
			}
			return struct{}{}, auditStaff(ctx, q, actor.ID, "donor.recognition_recomputed", donor.String(), map[string]any{
				"reason": reason, "before_current": before.CurrentStreak, "after_current": after.CurrentStreak,
				"before_best": before.BestStreak, "after_best": after.BestStreak, "rules_version": projectionRules,
			}, now)
		},
	)
	return err
}
