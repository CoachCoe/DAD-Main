package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

func terminalPaymentUpdate(status string) bool {
	return status == "APPLIED" || status == "EXPIRED" || status == "CANCELLED"
}

// paymentUpdateFacts is a hosted card setup's processor state, fetched without
// holding the donor lock. An empty status leaves the update's status unchanged.
type paymentUpdateFacts struct {
	update  pgtype.UUID
	session string
	status  string
}

// fetchPaymentUpdate reads an unfinished card update from the processor without
// holding the donor lock. A SetupIntent verifies a card for future use; it is
// never recorded as a gift. Once verified, the card becomes the subscription's
// default under the update's stable idempotency key, so the durable command
// survives a lost provider acknowledgement or DB rollback.
func fetchPaymentUpdate(ctx context.Context, q *queries.Queries, gateway payments.PaymentMethodGateway, row queries.DonorPaymentUpdate, sessionID string, now time.Time) (paymentUpdateFacts, error) {
	if sessionID == "" {
		sessionID = row.StripeSessionID.String
	}
	facts := paymentUpdateFacts{update: row.ID, session: sessionID}
	if terminalPaymentUpdate(row.Status) {
		facts.status = row.Status
		return facts, nil
	}
	if gateway == nil {
		return facts, payments.ErrCheckoutUnavailable
	}
	if !strings.HasPrefix(sessionID, "cs_test_") || (row.StripeSessionID.Valid && row.StripeSessionID.String != sessionID) {
		return facts, payments.ErrFacts
	}
	remote, err := gateway.RetrieveCheckout(ctx, sessionID)
	if err != nil {
		return facts, err
	}
	metadata, err := remote.Object("metadata")
	if err != nil || metadata.Text("dad_payment_update_id") != row.ID.String() || remote.Text("id") != sessionID || !remote.BoolIs("livemode", false) || remote.Text("mode") != "setup" || remote.Text("customer") != row.CustomerID {
		return facts, payments.ErrFacts
	}
	switch remote.Text("status") {
	case "expired":
		facts.status = "EXPIRED"
		return facts, nil
	case "open":
		return facts, nil
	case "complete":
	default:
		return facts, payments.ErrFacts
	}
	setupID := remote.Text("setup_intent")
	if !strings.HasPrefix(setupID, "seti_") {
		return facts, payments.ErrFacts
	}
	setup, err := gateway.RetrieveSetupIntent(ctx, setupID)
	if err != nil {
		return facts, err
	}
	metadata, err = setup.Object("metadata")
	var types []string
	typesErr := json.Unmarshal(setup["payment_method_types"], &types)
	method := setup.Text("payment_method")
	if err != nil || metadata.Text("dad_payment_update_id") != row.ID.String() || setup.Text("id") != setupID || !setup.BoolIs("livemode", false) || setup.Text("customer") != row.CustomerID || setup.Text("status") != "succeeded" || setup.Text("usage") != "off_session" || typesErr != nil || len(types) != 1 || types[0] != "card" || !strings.HasPrefix(method, "pm_") {
		return facts, payments.ErrFacts
	}
	attempt, err := q.AttemptSnapshotByID(ctx, row.AttemptID)
	if err != nil {
		return facts, err
	}
	draft, err := q.CheckoutDraft(ctx, attempt.CheckoutID)
	if err != nil {
		return facts, err
	}
	if draft.DonorID != row.DonorID || draft.Frequency != "MONTHLY" || !attempt.StripeSubscriptionID.Valid {
		return facts, payments.ErrFacts
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
		return facts, err
	}
	if terminalSubscription(sub.Text("status")) {
		facts.status = "CANCELLED"
		return facts, nil
	}
	switch sub.Text("status") {
	case "active", "past_due", "unpaid":
	default:
		return facts, payments.ErrFacts
	}
	if sub.Text("default_payment_method") != method {
		if now.Sub(row.CreatedAt.Time) >= 23*time.Hour {
			return facts, payments.ErrFacts
		}
		if err = gateway.UpdateSubscriptionPaymentMethod(ctx, attempt.StripeSubscriptionID.String, method, "dad-payment-apply-"+row.ID.String()); err != nil {
			return facts, err
		}
		confirmed, err := read()
		if err != nil {
			return facts, err
		}
		if confirmed.Text("default_payment_method") != method {
			return facts, payments.ErrFacts
		}
	}
	facts.status = "APPLIED"
	return facts, nil
}

// applyPaymentUpdate records fetched facts on the update, which the caller has
// locked along with its donor.
func applyPaymentUpdate(ctx context.Context, q *queries.Queries, row queries.DonorPaymentUpdate, facts paymentUpdateFacts, now time.Time) (string, error) {
	if terminalPaymentUpdate(row.Status) {
		return row.Status, nil
	}
	if facts.update != row.ID || (row.StripeSessionID.Valid && row.StripeSessionID.String != facts.session) {
		return "", payments.ErrFacts
	}
	status, url, completed := facts.status, row.HostedUrl, row.CompletedAt
	if status == "" {
		status = row.Status
	}
	if terminalPaymentUpdate(status) {
		url = pgtype.Text{}
	}
	if status == "APPLIED" {
		completed = timestamp(now)
	}
	err := q.SavePaymentUpdate(ctx, queries.SavePaymentUpdateParams{ID: row.ID, StripeSessionID: pgtype.Text{String: facts.session, Valid: true}, Status: status, HostedUrl: url, CompletedAt: completed})
	return status, err
}
