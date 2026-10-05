package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store"
)

type CheckoutSource interface {
	BeginCheckout(context.Context, payments.CheckoutRequest) (payments.CheckoutResult, error)
}

const minAmountMinor = 100

type checkoutBody struct {
	Email     *string         `json:"email"`
	Amount    *int64          `json:"amount_minor"`
	Frequency json.RawMessage `json:"frequency"`
}

func (a *api) beginCheckout(w http.ResponseWriter, r *http.Request) {
	const unavailable = "Checkout is temporarily unavailable; retry the same request"
	if a.Checkout == nil {
		writeError(w, r, http.StatusServiceUnavailable, unavailable, nil)
		return
	}
	var body checkoutBody
	key, validKey := idempotencyKey(r)
	if !validKey || !decodeJSON(w, r, &body) || body.Email == nil || body.Amount == nil ||
		utf8.RuneCountInString(*body.Email) > 320 || *body.Amount < minAmountMinor || *body.Amount > maxAmountMinor {
		invalidFields(w, r)
		return
	}
	frequency, ok := optional(body.Frequency, "MONTHLY")
	if !ok || (frequency != "MONTHLY" && frequency != "ONE_TIME") {
		invalidFields(w, r)
		return
	}
	request := payments.CheckoutRequest{RequestID: key, Email: *body.Email, Amount: *body.Amount, Frequency: frequency}
	if visitor := cookieValue(r, referralCookie); visitor != "" {
		request.Visitor = &visitor
	}
	ctx, cancel := withTimeout(r, providerTimeout)
	defer cancel()
	result, err := a.Checkout.BeginCheckout(ctx, request)
	switch {
	case errors.Is(err, payments.ErrCheckoutConflict):
		writeError(w, r, http.StatusConflict, "Checkout conflicts with an existing request", nil)
	case errors.Is(err, store.ErrDenied):
		writeError(w, r, http.StatusForbidden, "Giving is unavailable for this account", nil)
	case errors.Is(err, payments.ErrCheckoutInput):
		invalidFields(w, r)
	case err != nil:
		writeError(w, r, http.StatusServiceUnavailable, unavailable, err)
	default:
		writeJSON(w, http.StatusCreated, result)
	}
}
