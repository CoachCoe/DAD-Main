package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type PaymentMethodStatus struct {
	CompletedAt pgtype.Timestamptz `json:"completed_at"`
	Mode        string             `json:"mode"`
	Status      string             `json:"status"`
}

// PaymentUpdate either redirects to hosted setup or reports an update that
// finished before a new one could begin.
type PaymentUpdate struct {
	CompletedAt *pgtype.Timestamptz `json:"completed_at,omitempty"`
	Status      string              `json:"status,omitempty"`
	URL         *string             `json:"url"`
}

func (s RecurringService) PaymentMethodStatus(ctx context.Context, token string) (PaymentMethodStatus, error) {
	return ownerTransaction(ctx, s.DB, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (PaymentMethodStatus, error) {
		row, err := queries.New(tx).LatestPaymentUpdate(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return PaymentMethodStatus{Mode: "test", Status: "NONE"}, nil
		}
		if err != nil {
			return PaymentMethodStatus{}, err
		}
		return PaymentMethodStatus{Mode: "test", Status: row.Status, CompletedAt: row.CompletedAt}, nil
	})
}

func (s RecurringService) BeginPaymentUpdate(ctx context.Context, token string, requestID pgtype.UUID) (PaymentUpdate, error) {
	if !ValidRequestID(requestID) {
		return PaymentUpdate{}, payments.ErrFacts
	}
	if s.Gateway == nil || payments.ValidateOrigin(s.Origin) != nil {
		return PaymentUpdate{}, payments.ErrCheckoutUnavailable
	}
	owner := donorOwner(token)
	var updateID pgtype.UUID
	ready, err := ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (*PaymentUpdate, error) {
		q := queries.New(tx)
		row, err := q.PaymentUpdateByID(ctx, requestID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		found := err == nil
		if found && (row.DonorID != d.ID || terminalPaymentUpdate(row.Status)) {
			return nil, ErrSubscriptionConflict
		}
		if !found {
			row, err = q.UnfinishedPaymentUpdate(ctx, d.ID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			found = err == nil
			if found && row.StripeSessionID.Valid {
				status, err := reconcilePaymentUpdate(ctx, tx, s.Gateway, row, "", now)
				if err != nil {
					return nil, err
				}
				if status == "APPLIED" || status == "CANCELLED" {
					row, err = q.PaymentUpdateByID(ctx, row.ID)
					if err != nil {
						return nil, err
					}
					return &PaymentUpdate{Status: row.Status, CompletedAt: &row.CompletedAt}, nil
				}
				found = status != "EXPIRED"
			}
		}
		if found {
			updateID = row.ID
			return nil, nil
		}
		attempt, err := q.LatestMonthlyAttempt(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !attempt.StripeSubscriptionID.Valid) {
			return nil, ErrSubscriptionConflict
		}
		if err != nil {
			return nil, err
		}
		remote, err := s.Gateway.RetrieveSubscription(ctx, attempt.StripeSubscriptionID.String)
		if err != nil {
			return nil, err
		}
		metadata, err := remote.Object("metadata")
		customer := remote.Text("customer")
		if err != nil || metadata.Text("dad_checkout_id") != attempt.CheckoutID.String() || remote.Text("id") != attempt.StripeSubscriptionID.String || !remote.BoolIs("livemode", false) || !strings.HasPrefix(customer, "cus_") {
			return nil, payments.ErrFacts
		}
		switch remote.Text("status") {
		case "active", "past_due", "unpaid":
		default:
			return nil, ErrSubscriptionConflict
		}
		marker := map[string]string{"dad_payment_update_id": requestID.String()}
		params, err := json.Marshal(map[string]any{"mode": "setup", "customer": customer, "allowed_payment_method_types": []string{"card"}, "metadata": marker, "setup_intent_data": map[string]any{"metadata": marker}, "success_url": s.Origin + "/account/payment-method", "cancel_url": s.Origin + "/account/payment-method", "expires_at": now.Add(time.Hour).Unix()})
		if err != nil {
			return nil, err
		}
		updateID = requestID
		return nil, q.InsertPaymentUpdate(ctx, queries.InsertPaymentUpdateParams{ID: requestID, DonorID: d.ID, AttemptID: attempt.ID, CustomerID: customer, RequestParams: params, CreatedAt: timestamp(now)})
	})
	if err != nil {
		return PaymentUpdate{}, err
	}
	if ready != nil {
		return *ready, nil
	}
	return ownerTransaction(ctx, s.DB, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (PaymentUpdate, error) {
		q := queries.New(tx)
		row, err := q.PaymentUpdateByID(ctx, updateID)
		if err != nil {
			return PaymentUpdate{}, err
		}
		if row.DonorID != d.ID || terminalPaymentUpdate(row.Status) {
			return PaymentUpdate{}, ErrSubscriptionConflict
		}
		if row.HostedUrl.Valid {
			return PaymentUpdate{URL: &row.HostedUrl.String}, nil
		}
		params, err := payments.DecodeObject(row.RequestParams)
		if err != nil {
			return PaymentUpdate{}, err
		}
		expires, err := params.Int("expires_at")
		if err != nil {
			return PaymentUpdate{}, err
		}
		if now.Unix() >= expires-1800 {
			return PaymentUpdate{}, ErrSubscriptionConflict
		}
		remote, err := s.Gateway.CreateCheckout(ctx, row.RequestParams, "dad-payment-setup-"+row.ID.String())
		if err != nil {
			return PaymentUpdate{}, err
		}
		if err = payments.ValidateRemoteCheckout(remote); err != nil {
			return PaymentUpdate{}, err
		}
		if row.StripeSessionID.Valid && row.StripeSessionID.String != remote.ID {
			return PaymentUpdate{}, payments.ErrFacts
		}
		err = q.SavePaymentUpdate(ctx, queries.SavePaymentUpdateParams{ID: row.ID, StripeSessionID: pgtype.Text{String: remote.ID, Valid: true}, Status: "OPEN", HostedUrl: pgtype.Text{String: remote.URL, Valid: true}, CompletedAt: row.CompletedAt})
		if err != nil {
			return PaymentUpdate{}, err
		}
		return PaymentUpdate{URL: &remote.URL}, nil
	})
}
