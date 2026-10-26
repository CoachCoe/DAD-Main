package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"dad/donor-api/internal/credentials"
	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/payments"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CheckoutService struct {
	DB      *pgxpool.Pool
	Gateway payments.CheckoutGateway
	Origin  string
}

// BeginCheckout returns a hosted checkout for a guest gift. The attempt and
// its frozen processor parameters commit before the processor is called, and
// a retry with the same request ID returns the same session.
func (s CheckoutService) BeginCheckout(ctx context.Context, request payments.CheckoutRequest) (payments.CheckoutResult, error) {
	empty := payments.CheckoutResult{}
	if s.Gateway == nil {
		return empty, payments.ErrUnconfigured
	}
	site, err := site(s.Origin)
	if err != nil {
		return empty, err
	}
	if !ValidRequestID(request.RequestID) || utf8.RuneCountInString(request.Email) > 320 || !ValidGiftAmount(request.Amount) {
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
	attempt, err := s.prepare(ctx, site, request, HashToken(preimage), time.Now().UTC())
	if err != nil {
		return empty, err
	}
	if attempt.HostedUrl.Valid {
		return payments.CheckoutResult{CheckoutID: attempt.CheckoutID.String(), URL: attempt.HostedUrl.String}, nil
	}
	remote, err := s.Gateway.CreateCheckout(ctx, attempt.RequestParams, "dad-checkout-"+request.RequestID.String())
	if err != nil {
		return empty, payments.ErrProcessorUnavailable
	}
	if err = payments.ValidateRemoteCheckout(remote); err != nil {
		return empty, err
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		current, err := q.LockStripeCheckout(ctx, request.RequestID)
		if err != nil {
			return err
		}
		if current.StripeSessionID.Valid && current.StripeSessionID.String != remote.ID {
			return payments.ErrCheckoutConflict
		}
		return q.AcknowledgeCheckout(
			ctx,
			queries.AcknowledgeCheckoutParams{ID: request.RequestID, StripeSessionID: text(remote.ID), HostedUrl: text(remote.URL)},
		)
	})
	if err != nil {
		return empty, err
	}
	return payments.CheckoutResult{CheckoutID: attempt.CheckoutID.String(), URL: remote.URL}, nil
}

// prepare records the guest donor, their checkout with any valid referral
// attribution, and the attempt's frozen processor parameters, or returns the
// attempt already recorded for request. A monthly gift reserves the donor's
// one recurring slot.
func (s CheckoutService) prepare(
	ctx context.Context,
	site publicSite,
	request payments.CheckoutRequest,
	digest string,
	now time.Time,
) (queries.DonorStripeCheckout, error) {
	return transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (queries.DonorStripeCheckout, error) {
		return prepareAttempt(ctx, queries.New(tx), site, request, digest, now)
	})
}

func prepareAttempt(
	ctx context.Context,
	q *queries.Queries,
	site publicSite,
	request payments.CheckoutRequest,
	digest string,
	now time.Time,
) (queries.DonorStripeCheckout, error) {
	empty := queries.DonorStripeCheckout{}
	donor, err := lockGuest(ctx, q, request.Email, now)
	if err != nil {
		return empty, err
	}
	attempt, err := q.StripeCheckout(ctx, request.RequestID)
	if err == nil {
		if attempt.RequestHash != digest || attempt.Status == "EXPIRED" || attempt.Status == "CANCELLED" || attempt.Status == "COMPLETE" {
			return empty, payments.ErrCheckoutConflict
		}
		if !attempt.HostedUrl.Valid && !now.Before(attempt.CreatedAt.Time.Add(commandRetryWindow)) {
			return empty, payments.ErrCheckoutConflict
		}
		return attempt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return empty, err
	}
	draft, err := checkoutDraft(ctx, q, donor, request, now)
	if err != nil {
		return empty, err
	}
	if err = q.InsertCheckout(ctx, draft); err != nil {
		return empty, err
	}
	raw, err := checkoutParams(site, donor.Email, draft, request)
	if err != nil {
		return empty, err
	}
	if err = q.InsertStripeCheckout(ctx, queries.InsertStripeCheckoutParams{ID: request.RequestID, CheckoutID: draft.ID, RequestHash: digest, RequestParams: raw, CreatedAt: timestamp(now)}); err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == uniqueViolation {
			return empty, payments.ErrCheckoutConflict
		}
		return empty, err
	}
	if request.Frequency == "MONTHLY" {
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
	return q.StripeCheckout(ctx, request.RequestID)
}

// lockGuest returns the donor for email, created as an unclaimed guest if
// new, locked and with a projection row for the refresh worker to find.
func lockGuest(ctx context.Context, q *queries.Queries, email string, now time.Time) (queries.Donor, error) {
	id, err := NewID()
	if err != nil {
		return queries.Donor{}, err
	}
	if err = reserveEmailIdentity(ctx, q, email, identityDonor, pgtype.UUID{}); err != nil {
		return queries.Donor{}, err
	}
	if err = q.InsertGuest(ctx, queries.InsertGuestParams{ID: id, Email: email}); err != nil {
		return queries.Donor{}, err
	}
	donor, err := q.DonorByEmail(ctx, email)
	if err != nil {
		return queries.Donor{}, err
	}
	if donor, err = q.LockDonor(ctx, donor.ID); err != nil {
		return queries.Donor{}, err
	}
	if donor.Disabled {
		return queries.Donor{}, ErrDenied
	}
	projection, err := NewID()
	if err != nil {
		return queries.Donor{}, err
	}
	return donor, q.SeedProjection(ctx, queries.SeedProjectionParams{ID: projection, DonorID: donor.ID, ComputedAt: timestamp(now)})
}

// checkoutDraft is a new checkout carrying the visitor's referral, if its
// window is still open and the referrer is not the donor.
func checkoutDraft(
	ctx context.Context,
	q *queries.Queries,
	donor queries.Donor,
	request payments.CheckoutRequest,
	now time.Time,
) (queries.InsertCheckoutParams, error) {
	id, err := NewID()
	if err != nil {
		return queries.InsertCheckoutParams{}, err
	}
	draft := queries.InsertCheckoutParams{
		ID:          id,
		DonorID:     donor.ID,
		AmountMinor: request.Amount,
		Frequency:   request.Frequency,
		CreatedAt:   timestamp(now),
	}
	if request.Visitor == nil || *request.Visitor == "" {
		return draft, nil
	}
	touch, err := q.ValidReferralTouch(
		ctx,
		queries.ValidReferralTouchParams{TokenHash: HashToken(*request.Visitor), ExpiresAt: timestamp(now), ID: donor.ID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return draft, nil
	}
	if err != nil {
		return queries.InsertCheckoutParams{}, err
	}
	draft.ReferrerID, draft.ReferralKind, draft.ReferralExpiresAt = touch.ReferrerID, text(touch.Kind), touch.ExpiresAt
	return draft, nil
}

// checkoutLifetime is how long a hosted checkout stays open. An unpaid monthly
// checkout holds the donor's recurring reservation until it expires, so it is
// kept short; Stripe accepts no less than 30 minutes, and the margin lets a
// retried request still meet that.
const checkoutLifetime = time.Hour

// checkoutParams are the hosted checkout parameters frozen with an attempt,
// so a retried request sends the processor exactly the same thing.
func checkoutParams(site publicSite, email string, draft queries.InsertCheckoutParams, request payments.CheckoutRequest) ([]byte, error) {
	checkoutID := draft.ID
	metadata := map[string]string{"dad_checkout_id": checkoutID.String()}
	if draft.ReferrerID.Valid {
		metadata["dad_referrer_id"] = draft.ReferrerID.String()
		metadata["dad_referral_kind"] = draft.ReferralKind.String
	}
	price := map[string]any{
		"currency":     "usd",
		"unit_amount":  request.Amount,
		"product_data": map[string]string{"name": "DAD monthly giving"},
		"recurring":    map[string]string{"interval": "month"},
	}
	params := map[string]any{
		"mode":                         "subscription",
		"customer_email":               email,
		"client_reference_id":          checkoutID.String(),
		"metadata":                     metadata,
		"line_items":                   []any{map[string]any{"price_data": price, "quantity": 1}},
		"allowed_payment_method_types": []string{"card"},
		"success_url":                  site.checkoutReturn(request.Amount, request.Frequency),
		"cancel_url":                   site.donate(),
		"expires_at":                   draft.CreatedAt.Time.Add(checkoutLifetime).Unix(),
		"subscription_data":            map[string]any{"metadata": metadata},
	}
	return json.Marshal(params)
}
