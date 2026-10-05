package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

type StaffFinanceSource interface {
	RefundQuote(context.Context, string, pgtype.UUID, pgtype.UUID) (map[string]any, error)
	CancellationQuote(context.Context, string, pgtype.UUID) (map[string]any, error)
	Refund(context.Context, string, store.StaffFinancialRequest) (map[string]any, error)
	Cancel(context.Context, string, store.StaffFinancialRequest) (map[string]any, error)
}

func financeStatus(err error) (int, string) {
	if errors.Is(err, payments.ErrFacts) || errors.Is(err, store.ErrSubscriptionConflict) {
		return http.StatusConflict, "Financial records require reconciliation. Refresh before confirming"
	}
	return staffStatus(err)
}

func validReason(reason string) bool {
	n := utf8.RuneCountInString(reason)
	return n >= 5 && n <= 500
}

// finance runs a staff financial request with the processor time budget.
func (a *api) finance(w http.ResponseWriter, r *http.Request, run func(context.Context, string) (map[string]any, error)) {
	if a.StaffFinance == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Financial actions are temporarily unavailable", nil)
		return
	}
	ctx, cancel := withTimeout(r, providerTimeout)
	defer cancel()
	body, err := run(ctx, cookieValue(r, staffCookie))
	respond(w, r, body, err, financeStatus)
}

func (a *api) refundQuote(w http.ResponseWriter, r *http.Request) {
	donorID, okDonor := pathUUID(r, "donor_id")
	giftID, okGift := pathUUID(r, "gift_id")
	if !okDonor || !okGift {
		staffInvalid(w, r)
		return
	}
	a.finance(w, r, func(ctx context.Context, session string) (map[string]any, error) {
		return a.StaffFinance.RefundQuote(ctx, session, donorID, giftID)
	})
}

func (a *api) cancellationQuote(w http.ResponseWriter, r *http.Request) {
	donorID, ok := pathUUID(r, "donor_id")
	if !ok {
		staffInvalid(w, r)
		return
	}
	a.finance(w, r, func(ctx context.Context, session string) (map[string]any, error) {
		return a.StaffFinance.CancellationQuote(ctx, session, donorID)
	})
}

func (a *api) refund(w http.ResponseWriter, r *http.Request) {
	donorID, okDonor := pathUUID(r, "donor_id")
	giftID, okGift := pathUUID(r, "gift_id")
	key, okKey := idempotencyKey(r)
	var body struct {
		Amount int64  `json:"amount_minor"`
		Reason string `json:"reason"`
	}
	if !okDonor || !okGift || !okKey || !decodeJSON(w, r, &body) || body.Amount < 1 || body.Amount > maxAmountMinor || !validReason(body.Reason) {
		staffInvalid(w, r)
		return
	}
	request := store.StaffFinancialRequest{RequestID: key, DonorID: donorID, TargetID: giftID, Amount: body.Amount, Reason: body.Reason}
	a.finance(w, r, func(ctx context.Context, session string) (map[string]any, error) {
		return a.StaffFinance.Refund(ctx, session, request)
	})
}

func (a *api) cancel(w http.ResponseWriter, r *http.Request) {
	donorID, okDonor := pathUUID(r, "donor_id")
	key, okKey := idempotencyKey(r)
	var body struct {
		ID     pgtype.UUID `json:"subscription_id"`
		End    time.Time   `json:"period_end"`
		Reason string      `json:"reason"`
	}
	if !okDonor || !okKey || !decodeJSON(w, r, &body) || !body.ID.Valid || body.End.IsZero() || !validReason(body.Reason) {
		staffInvalid(w, r)
		return
	}
	request := store.StaffFinancialRequest{RequestID: key, DonorID: donorID, TargetID: body.ID, PeriodEnd: body.End.Truncate(time.Microsecond), Reason: body.Reason}
	a.finance(w, r, func(ctx context.Context, session string) (map[string]any, error) {
		return a.StaffFinance.Cancel(ctx, session, request)
	})
}
