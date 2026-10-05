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

func (s RecurringService) PaymentMethodStatus(ctx context.Context, token string) (map[string]interface{}, error) {
	return s.transaction(ctx, donorOwner(token), func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		row, err := queries.New(tx).LatestPaymentUpdate(ctx, d.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]interface{}{"mode": "test", "status": "NONE", "completed_at": nil}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"mode": "test", "status": row.Status, "completed_at": row.CompletedAt}, nil
	})
}

func (s RecurringService) BeginPaymentUpdate(ctx context.Context, token string, requestID pgtype.UUID) (map[string]interface{}, error) {
	if !ValidRequestID(requestID) {
		return nil, payments.ErrFacts
	}
	if s.Gateway == nil || payments.ValidateOrigin(s.Origin) != nil {
		return nil, payments.ErrCheckoutUnavailable
	}
	owner := donorOwner(token)
	var updateID pgtype.UUID
	ready, err := s.transaction(ctx, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
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
					return map[string]interface{}{"url": nil, "status": row.Status, "completed_at": row.CompletedAt}, nil
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
		params, err := json.Marshal(map[string]interface{}{"mode": "setup", "customer": customer, "allowed_payment_method_types": []string{"card"}, "metadata": marker, "setup_intent_data": map[string]interface{}{"metadata": marker}, "success_url": s.Origin + "/account/payment-method", "cancel_url": s.Origin + "/account/payment-method", "expires_at": now.Add(time.Hour).Unix()})
		if err != nil {
			return nil, err
		}
		updateID = requestID
		return nil, q.InsertPaymentUpdate(ctx, queries.InsertPaymentUpdateParams{ID: requestID, DonorID: d.ID, AttemptID: attempt.ID, CustomerID: customer, RequestParams: params, CreatedAt: timestamp(now)})
	})
	if err != nil || ready != nil {
		return ready, err
	}
	return s.transaction(ctx, owner, func(tx pgx.Tx, d queries.Donor, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		row, err := q.PaymentUpdateByID(ctx, updateID)
		if err != nil {
			return nil, err
		}
		if row.DonorID != d.ID || terminalPaymentUpdate(row.Status) {
			return nil, ErrSubscriptionConflict
		}
		if row.HostedUrl.Valid {
			return map[string]interface{}{"url": row.HostedUrl.String}, nil
		}
		params, err := payments.DecodeObject(row.RequestParams)
		if err != nil {
			return nil, err
		}
		expires, err := params.Int("expires_at")
		if err != nil {
			return nil, err
		}
		if now.Unix() >= expires-1800 {
			return nil, ErrSubscriptionConflict
		}
		remote, err := s.Gateway.CreateCheckout(ctx, row.RequestParams, "dad-payment-setup-"+row.ID.String())
		if err != nil {
			return nil, err
		}
		if err = payments.ValidateRemoteCheckout(remote); err != nil {
			return nil, err
		}
		if row.StripeSessionID.Valid && row.StripeSessionID.String != remote.ID {
			return nil, payments.ErrFacts
		}
		err = q.SavePaymentUpdate(ctx, queries.SavePaymentUpdateParams{ID: row.ID, StripeSessionID: pgtype.Text{String: remote.ID, Valid: true}, Status: "OPEN", HostedUrl: pgtype.Text{String: remote.URL, Valid: true}, CompletedAt: row.CompletedAt})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"url": remote.URL}, nil
	})
}
