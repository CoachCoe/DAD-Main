package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

func pageOffsetValid(offset int64) bool { return offset >= 0 && offset <= math.MaxInt64-50 }
func page[T any](rows []T, offset int64) ([]T, interface{}) {
	if len(rows) > 50 {
		return rows[:50], offset + 50
	}
	return rows, nil
}

func (s *StaffService) SearchDonors(ctx context.Context, token, term string, offset int64) (map[string]interface{}, error) {
	if !pageOffsetValid(offset) || utf8.RuneCountInString(term) > 320 {
		return nil, ErrStaffInput
	}
	return staffTransaction(ctx, s, token, "donor.read", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		pattern := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(identity.TrimSpace(term))
		rows, err := q.StaffDonorSearch(ctx, queries.StaffDonorSearchParams{Term: "%" + pattern + "%", PageOffset: offset})
		if err != nil {
			return nil, err
		}
		rows, next := page(rows, offset)
		result := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			result = append(result, map[string]interface{}{"id": row.ID, "email": row.Email, "public_name": row.PublicName, "claimed": row.ClaimedAt.Valid, "disabled": row.Disabled})
		}
		if err = auditStaff(ctx, q, actor.ID, "donor.search", "donors", map[string]interface{}{"offset": offset, "result_count": len(rows)}, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"donors": result, "next_offset": next}, nil
	})
}

func (s *StaffService) DonorDetail(ctx context.Context, token string, id pgtype.UUID, giftsOffset, referralsOffset int64) (map[string]interface{}, error) {
	if !pageOffsetValid(giftsOffset) || !pageOffsetValid(referralsOffset) {
		return nil, ErrStaffInput
	}
	return staffTransaction(ctx, s, token, "donor.read", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		donor, err := q.StaffDonorByID(ctx, id)
		if err != nil {
			return nil, err
		}
		projection, err := q.Projection(ctx, id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var streak, referrer interface{}
		if err == nil {
			streak = map[string]interface{}{"current": projection.CurrentStreak, "best": projection.BestStreak, "at_risk": projection.AtRisk, "computed_at": projection.ComputedAt}
			if projection.ReferrerID.Valid {
				row, err := q.StaffDonorByID(ctx, projection.ReferrerID)
				if err != nil {
					return nil, err
				}
				referrer = map[string]interface{}{"id": row.ID, "email": row.Email, "type": projection.ReferralKind}
			}
		}
		gifts, err := q.DonorGiftsPage(ctx, queries.DonorGiftsPageParams{DonorID: id, PageOffset: giftsOffset})
		if err != nil {
			return nil, err
		}
		gifts, nextGifts := page(gifts, giftsOffset)
		referrals, err := q.StaffReferredPage(ctx, queries.StaffReferredPageParams{ReferrerID: id, PageOffset: referralsOffset})
		if err != nil {
			return nil, err
		}
		referrals, nextReferrals := page(referrals, referralsOffset)
		referred := make([]map[string]interface{}, 0, len(referrals))
		for _, row := range referrals {
			referred = append(referred, map[string]interface{}{"id": row.ID, "email": row.Email, "type": row.ReferralKind, "net_minor": row.NetMinor})
		}
		if err = auditStaff(ctx, q, actor.ID, "donor.view", id.String(), nil, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": donor.ID, "email": donor.Email, "public_name": donor.PublicName, "streak": streak, "referrer": referrer, "gifts": gifts, "referred": referred, "next_gifts_offset": nextGifts, "next_referrals_offset": nextReferrals, "as_of": now}, nil
	})
}

func (s *StaffService) AuditEvents(ctx context.Context, token string, offset int64) (map[string]interface{}, error) {
	if !pageOffsetValid(offset) {
		return nil, ErrStaffInput
	}
	return staffTransaction(ctx, s, token, "audit.read", func(tx pgx.Tx, _ queries.DonorStaff, _ time.Time) (map[string]interface{}, error) {
		rows, err := queries.New(tx).StaffAuditPage(ctx, offset)
		if err != nil {
			return nil, err
		}
		rows, next := page(rows, offset)
		events := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			events = append(events, map[string]interface{}{"id": row.ID, "actor_id": row.ActorID, "action": row.Action, "target": row.Target, "at": row.CreatedAt, "details": json.RawMessage(row.Details)})
		}
		return map[string]interface{}{"events": events, "next_offset": next}, nil
	})
}

func (s *StaffService) DonorMessages(ctx context.Context, token string, id pgtype.UUID, offset int64) (map[string]interface{}, error) {
	if !pageOffsetValid(offset) {
		return nil, ErrStaffInput
	}
	return staffTransaction(ctx, s, token, "donor.read", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		if _, err := q.StaffDonorByID(ctx, id); err != nil {
			return nil, err
		}
		rows, err := q.StaffMessagePage(ctx, queries.StaffMessagePageParams{DonorID: id, PageOffset: offset})
		if err != nil {
			return nil, err
		}
		rows, next := page(rows, offset)
		if err = auditStaff(ctx, q, actor.ID, "message.list", id.String(), nil, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"messages": rows, "next_offset": next}, nil
	})
}

func (s *StaffService) DonorActions(ctx context.Context, token string, id pgtype.UUID) ([]queries.StaffActionPageRow, error) {
	return staffTransaction(ctx, s, token, "donor.read", func(tx pgx.Tx, _ queries.DonorStaff, _ time.Time) ([]queries.StaffActionPageRow, error) {
		return queries.New(tx).StaffActionPage(ctx, id)
	})
}

func (s *StaffService) Enrolments(ctx context.Context, token string) (map[string]interface{}, error) {
	if _, err := requireStaff(ctx, queries.New(s.DB), token, "data.export", time.Now().UTC()); err != nil {
		return nil, err
	}
	return map[string]interface{}{"state": "not_active_until_r2", "enrolments": []interface{}{}}, nil
}
