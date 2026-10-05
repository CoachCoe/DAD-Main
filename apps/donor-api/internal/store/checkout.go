package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type CheckoutService struct {
	DB      *pgxpool.Pool
	Gateway payments.CheckoutGateway
	Origin  string
}

func (s CheckoutService) BeginCheckout(ctx context.Context, request payments.CheckoutRequest) (payments.CheckoutResult, error) {
	empty := payments.CheckoutResult{}
	if s.Gateway == nil {
		return empty, payments.ErrCheckoutUnavailable
	}
	if err := payments.ValidateOrigin(s.Origin); err != nil {
		return empty, err
	}
	if !request.RequestID.Valid || request.RequestID.Bytes[6]>>4 != 4 || request.RequestID.Bytes[8]&0xc0 != 0x80 || utf8.RuneCountInString(request.Email) > 320 || request.Amount < 100 || request.Amount > 99999999 {
		return empty, payments.ErrCheckoutInput
	}
	email, err := identity.NormalizeEmail(request.Email)
	if err != nil {
		return empty, payments.ErrCheckoutInput
	}
	request.Email = email
	preimage, err := credentials.CheckoutPreimage(email, request.Amount, request.Frequency, request.Visitor)
	if err != nil {
		return empty, payments.ErrCheckoutInput
	}
	attempt, err := s.prepare(ctx, request, HashToken(preimage), time.Now().UTC())
	if err != nil {
		return empty, err
	}
	if attempt.HostedUrl.Valid {
		return payments.CheckoutResult{CheckoutID: attempt.CheckoutID.String(), URL: attempt.HostedUrl.String}, nil
	}
	remote, err := s.Gateway.CreateCheckout(ctx, attempt.RequestParams, "dad-checkout-"+request.RequestID.String())
	if err != nil {
		return empty, payments.ErrCheckoutUnavailable
	}
	if err = payments.ValidateRemoteCheckout(remote); err != nil {
		return empty, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	current, err := q.LockStripeCheckout(ctx, request.RequestID)
	if err != nil {
		return empty, err
	}
	if current.StripeSessionID.Valid && current.StripeSessionID.String != remote.ID {
		return empty, payments.ErrCheckoutConflict
	}
	if err = q.AcknowledgeCheckout(ctx, queries.AcknowledgeCheckoutParams{ID: request.RequestID, StripeSessionID: pgtype.Text{String: remote.ID, Valid: true}, HostedUrl: pgtype.Text{String: remote.URL, Valid: true}}); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return payments.CheckoutResult{CheckoutID: attempt.CheckoutID.String(), URL: remote.URL}, nil
}

func (s CheckoutService) prepare(ctx context.Context, request payments.CheckoutRequest, digest string, now time.Time) (queries.DonorStripeCheckout, error) {
	empty := queries.DonorStripeCheckout{}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	id, err := NewID()
	if err != nil {
		return empty, err
	}
	if err = q.InsertGuest(ctx, queries.InsertGuestParams{ID: id, Email: request.Email}); err != nil {
		return empty, err
	}
	donor, err := q.DonorByEmail(ctx, request.Email)
	if err != nil {
		return empty, err
	}
	donor, err = q.LockDonor(ctx, donor.ID)
	if err != nil {
		return empty, err
	}
	if donor.Disabled {
		return empty, ErrDenied
	}
	projection, err := NewID()
	if err != nil {
		return empty, err
	}
	if err = q.SeedProjection(ctx, queries.SeedProjectionParams{ID: projection, DonorID: donor.ID, ComputedAt: timestamp(now)}); err != nil {
		return empty, err
	}
	attempt, err := q.StripeCheckout(ctx, request.RequestID)
	if err == nil {
		if attempt.RequestHash != digest || attempt.Status == "EXPIRED" || attempt.Status == "CANCELLED" || attempt.Status == "COMPLETE" {
			return empty, payments.ErrCheckoutConflict
		}
		if !attempt.HostedUrl.Valid && !now.Before(attempt.CreatedAt.Time.Add(23*time.Hour)) {
			return empty, payments.ErrCheckoutConflict
		}
		return attempt, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	checkoutID, err := NewID()
	if err != nil {
		return empty, err
	}
	draft := queries.InsertCheckoutParams{ID: checkoutID, DonorID: donor.ID, AmountMinor: request.Amount, Frequency: request.Frequency, CreatedAt: timestamp(now)}
	if request.Visitor != nil && *request.Visitor != "" {
		touch, err := q.ValidReferralTouch(ctx, queries.ValidReferralTouchParams{TokenHash: HashToken(*request.Visitor), ExpiresAt: timestamp(now), ID: donor.ID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return empty, err
		}
		if err == nil {
			draft.ReferrerID = touch.ReferrerID
			draft.ReferralKind = pgtype.Text{String: touch.Kind, Valid: true}
			draft.ReferralExpiresAt = touch.ExpiresAt
		}
	}
	if err = q.InsertCheckout(ctx, draft); err != nil {
		return empty, err
	}
	metadata := map[string]string{"dad_checkout_id": checkoutID.String()}
	if draft.ReferrerID.Valid {
		metadata["dad_referrer_id"] = draft.ReferrerID.String()
		metadata["dad_referral_kind"] = draft.ReferralKind.String
	}
	monthly := request.Frequency == "MONTHLY"
	mode, name := "payment", "DAD one-time gift"
	if monthly {
		mode, name = "subscription", "DAD monthly giving"
	}
	price := map[string]any{"currency": "usd", "unit_amount": request.Amount, "product_data": map[string]string{"name": name}}
	params := map[string]any{"mode": mode, "customer_email": donor.Email, "client_reference_id": checkoutID.String(), "metadata": metadata,
		"line_items": []any{map[string]any{"price_data": price, "quantity": 1}}, "allowed_payment_method_types": []string{"card"},
		"success_url": fmt.Sprintf("%s/donate/thanks?session_id={CHECKOUT_SESSION_ID}&amount_minor=%d&frequency=%s", s.Origin, request.Amount, request.Frequency), "cancel_url": s.Origin + "/donate"}
	if monthly {
		price["recurring"] = map[string]string{"interval": "month"}
		params["subscription_data"] = map[string]any{"metadata": metadata}
	} else {
		params["payment_intent_data"] = map[string]any{"metadata": metadata}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return empty, err
	}
	if err = q.InsertStripeCheckout(ctx, queries.InsertStripeCheckoutParams{ID: request.RequestID, CheckoutID: checkoutID, RequestHash: digest, RequestParams: raw, CreatedAt: timestamp(now)}); err != nil {
		var violation *pgconn.PgError
		if errors.As(err, &violation) && violation.Code == "23505" {
			return empty, payments.ErrCheckoutConflict
		}
		return empty, err
	}
	if monthly {
		reservation, err := NewID()
		if err != nil {
			return empty, err
		}
		if _, err = q.ReserveRecurring(ctx, queries.ReserveRecurringParams{ID: reservation, DonorID: donor.ID, AttemptID: request.RequestID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return empty, payments.ErrCheckoutConflict
			}
			return empty, err
		}
	}
	attempt, err = q.StripeCheckout(ctx, request.RequestID)
	if err != nil {
		return empty, err
	}
	return attempt, tx.Commit(ctx)
}
