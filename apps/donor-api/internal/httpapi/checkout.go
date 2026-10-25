package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"

	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store"
)

type CheckoutSource interface {
	BeginCheckout(context.Context, payments.CheckoutRequest) (payments.CheckoutResult, error)
}

type checkoutBody struct {
	Email     *string         `json:"email"`
	Amount    *int64          `json:"amount_minor"`
	Frequency json.RawMessage `json:"frequency"`
}

func (a *api) beginCheckout(w http.ResponseWriter, r *http.Request) {
	const unavailable = "Checkout is temporarily unavailable; retry the same request"
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var body checkoutBody
	if !decodeJSON(w, r, &body) {
		return
	}
	frequency, ok := optional(body.Frequency, "MONTHLY")
	if !ok || frequency != "MONTHLY" || body.Email == nil || body.Amount == nil ||
		utf8.RuneCountInString(*body.Email) > maxEmailRunes || !store.ValidGiftAmount(*body.Amount) {
		invalidFields(w, r)
		return
	}
	if a.Checkout == nil {
		writeError(w, r, http.StatusServiceUnavailable, unavailable, nil)
		return
	}
	request := payments.CheckoutRequest{
		RequestID: key, Email: *body.Email, Amount: *body.Amount, Frequency: frequency, Company: cookieValue(r, companyCookie),
	}
	if visitor := cookieValue(r, referralCookie); visitor != "" {
		request.Visitor = &visitor
	}
	ctx, cancel := withTimeout(r, providerTimeout)
	defer cancel()
	result, err := a.Checkout.BeginCheckout(ctx, request)
	switch {
	case errors.Is(err, payments.ErrUnconfigured):
		paymentsOff.write(w)
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
