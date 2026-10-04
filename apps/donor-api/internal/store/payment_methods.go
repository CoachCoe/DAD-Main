package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

func terminalPaymentUpdate(status string) bool {
	return status == "APPLIED" || status == "EXPIRED" || status == "CANCELLED"
}

// A SetupIntent verifies a card for future use; it is never recorded as a gift.
// The durable command ID survives a lost provider acknowledgement or DB rollback.
func reconcilePaymentUpdate(ctx context.Context, tx pgx.Tx, gateway payments.PaymentMethodGateway, row queries.DonorPaymentUpdate, sessionID string, now time.Time) (string, error) {
	if terminalPaymentUpdate(row.Status) {
		return row.Status, nil
	}
	if gateway == nil {
		return "", payments.ErrCheckoutUnavailable
	}
	if sessionID == "" {
		sessionID = row.StripeSessionID.String
	}
	if !strings.HasPrefix(sessionID, "cs_test_") || (row.StripeSessionID.Valid && row.StripeSessionID.String != sessionID) {
		return "", payments.ErrFacts
	}
	remote, err := gateway.RetrieveCheckout(ctx, sessionID)
	if err != nil {
		return "", err
	}
	metadata, err := remote.Object("metadata")
	if err != nil || metadata.Text("dad_payment_update_id") != row.ID.String() || remote.Text("id") != sessionID || !remote.BoolIs("livemode", false) || remote.Text("mode") != "setup" || remote.Text("customer") != row.CustomerID {
		return "", payments.ErrFacts
	}
	q := queries.New(tx)
	save := func(status string, complete pgtype.Timestamptz) (string, error) {
		url := row.HostedUrl
		if terminalPaymentUpdate(status) {
			url = pgtype.Text{}
		}
		err := q.SavePaymentUpdate(ctx, queries.SavePaymentUpdateParams{ID: row.ID, StripeSessionID: pgtype.Text{String: sessionID, Valid: true}, Status: status, HostedUrl: url, CompletedAt: complete})
		return status, err
	}
	switch remote.Text("status") {
	case "expired":
		return save("EXPIRED", row.CompletedAt)
	case "open":
		return save(row.Status, row.CompletedAt)
	case "complete":
	default:
		return "", payments.ErrFacts
	}
	setupID := remote.Text("setup_intent")
	if !strings.HasPrefix(setupID, "seti_") {
		return "", payments.ErrFacts
	}
	setup, err := gateway.RetrieveSetupIntent(ctx, setupID)
	if err != nil {
		return "", err
	}
	metadata, err = setup.Object("metadata")
	var types []string
	typesErr := json.Unmarshal(setup["payment_method_types"], &types)
	method := setup.Text("payment_method")
	if err != nil || metadata.Text("dad_payment_update_id") != row.ID.String() || setup.Text("id") != setupID || !setup.BoolIs("livemode", false) || setup.Text("customer") != row.CustomerID || setup.Text("status") != "succeeded" || setup.Text("usage") != "off_session" || typesErr != nil || len(types) != 1 || types[0] != "card" || !strings.HasPrefix(method, "pm_") {
		return "", payments.ErrFacts
	}
	attempt, err := q.AttemptByID(ctx, row.AttemptID)
	if err != nil {
		return "", err
	}
	draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
	if err != nil {
		return "", err
	}
	if draft.DonorID != row.DonorID || draft.Frequency != "MONTHLY" || !attempt.StripeSubscriptionID.Valid {
		return "", payments.ErrFacts
	}
	read := func() (payments.Object, error) {
		sub, err := gateway.RetrieveSubscription(ctx, attempt.StripeSubscriptionID.String)
		if err != nil {
			return nil, err
		}
		metadata, err := sub.Object("metadata")
		if err != nil || metadata.Text("dad_checkout_id") != draft.ID.String() || sub.Text("id") != attempt.StripeSubscriptionID.String || !sub.BoolIs("livemode", false) || !strings.HasPrefix(sub.Text("customer"), "cus_") || sub.Text("customer") != row.CustomerID {
			return nil, payments.ErrFacts
		}
		return sub, nil
	}
	sub, err := read()
	if err != nil {
		return "", err
	}
	if terminalSubscription(sub.Text("status")) {
		return save("CANCELLED", row.CompletedAt)
	}
	switch sub.Text("status") {
	case "active", "past_due", "unpaid":
	default:
		return "", payments.ErrFacts
	}
	if sub.Text("default_payment_method") != method {
		if now.Sub(row.CreatedAt.Time) >= 23*time.Hour {
			return "", payments.ErrFacts
		}
		if err = gateway.UpdateSubscriptionPaymentMethod(ctx, attempt.StripeSubscriptionID.String, method, "dad-payment-apply-"+row.ID.String()); err != nil {
			return "", err
		}
		confirmed, err := read()
		if err != nil {
			return "", err
		}
		if confirmed.Text("default_payment_method") != method {
			return "", payments.ErrFacts
		}
	}
	return save("APPLIED", timestamp(now))
}
