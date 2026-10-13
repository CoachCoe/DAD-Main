package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v87"
)

type FinancialGateway interface {
	RetrievePaymentIntent(context.Context, string) (Object, error)
	RetrieveRefund(context.Context, string) (Object, error)
	RetrieveSubscription(context.Context, string) (Object, error)
}

type PaymentMethodGateway interface {
	FinancialGateway
	RetrieveCheckout(context.Context, string) (Object, error)
	RetrieveSetupIntent(context.Context, string) (Object, error)
	UpdateSubscriptionPaymentMethod(context.Context, string, string, string) error
}

type SubscriptionGateway interface {
	PaymentMethodGateway
	CheckoutGateway
	ScheduleCancellation(context.Context, string, int64, string) error
	PauseCollection(context.Context, string, int64, string) error
	ChangeMonthlyAmount(context.Context, string, string, string, int64, string) error
}

type StaffFinanceGateway interface {
	SubscriptionGateway
	ListPaymentRefunds(context.Context, string) ([]Object, error)
	CreateRefund(context.Context, string, int64, string) (Object, error)
}

// ReconcileGateway lists the charges and refunds Stripe created in a window,
// read-only, so the daily reconciliation job can diff them against the
// database without any mutating call. A fake satisfies it in tests.
type ReconcileGateway interface {
	ListChargesByCreated(context.Context, time.Time, time.Time) ([]Object, error)
	ListRefundsByCreated(context.Context, time.Time, time.Time) ([]Object, error)
}

// StripeGateway calls Stripe in test mode. A failed call is reported as
// ErrProcessorUnavailable, never as the SDK error, which can carry donor inputs.
type StripeGateway struct{ client *stripe.Client }

func NewStripeGateway(key string) (*StripeGateway, error) {
	if key == "" {
		return nil, ErrProcessorUnavailable
	}
	if !strings.HasPrefix(key, "sk_test_") {
		return nil, errors.New("this release accepts only Stripe test keys")
	}
	backends := stripe.NewBackendsWithConfig(&stripe.BackendConfig{
		HTTPClient: &http.Client{Timeout: 15 * time.Second}, MaxNetworkRetries: stripe.Int64(2),
		// Provider errors may contain donor inputs. Application logs record operation
		// classes separately, never the SDK's raw request/error diagnostics.
		LeveledLogger: &stripe.LeveledLogger{Level: stripe.LevelNull},
	})
	return &StripeGateway{client: stripe.NewClient(key, stripe.WithBackends(backends))}, nil
}

func (g *StripeGateway) CreateCheckout(ctx context.Context, raw []byte, key string) (RemoteCheckout, error) {
	var params stripe.CheckoutSessionCreateParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	// Refuse an old/unknown saved parameter rather than silently dropping it and
	// changing an idempotent request. Such attempts require reconciliation.
	if err := decoder.Decode(&params); err != nil {
		return RemoteCheckout{}, ErrProcessorUnavailable
	}
	params.SetIdempotencyKey(key)
	session, err := g.client.V1CheckoutSessions.Create(ctx, &params)
	if err != nil {
		return RemoteCheckout{}, ErrProcessorUnavailable
	}
	var result RemoteCheckout
	if session.LastResponse == nil || json.Unmarshal(session.LastResponse.RawJSON, &result) != nil {
		return result, ErrProcessorUnavailable
	}
	return result, nil
}

// Recurring changes never prorate: a new amount, pause, or cancellation takes
// effect at a renewal boundary.
func (g *StripeGateway) mutateSubscription(ctx context.Context, id string, params *stripe.SubscriptionUpdateParams, key string) error {
	params.ProrationBehavior = stripe.String("none")
	params.SetIdempotencyKey(key)
	if _, err := g.client.V1Subscriptions.Update(ctx, id, params); err != nil {
		return ErrProcessorUnavailable
	}
	return nil
}
func (g *StripeGateway) ScheduleCancellation(ctx context.Context, id string, end int64, key string) error {
	return g.mutateSubscription(ctx, id, &stripe.SubscriptionUpdateParams{CancelAt: stripe.Int64(end)}, key)
}
func (g *StripeGateway) PauseCollection(ctx context.Context, id string, resumes int64, key string) error {
	return g.mutateSubscription(ctx, id, &stripe.SubscriptionUpdateParams{PauseCollection: &stripe.SubscriptionUpdatePauseCollectionParams{Behavior: stripe.String("void"), ResumesAt: stripe.Int64(resumes)}}, key)
}
func (g *StripeGateway) ChangeMonthlyAmount(ctx context.Context, id, item, product string, amount int64, key string) error {
	price := &stripe.SubscriptionUpdateItemPriceDataParams{Currency: stripe.String("usd"), Product: stripe.String(product), Recurring: &stripe.SubscriptionUpdateItemPriceDataRecurringParams{Interval: stripe.String("month")}, UnitAmount: stripe.Int64(amount)}
	return g.mutateSubscription(ctx, id, &stripe.SubscriptionUpdateParams{Items: []*stripe.SubscriptionUpdateItemParams{{ID: stripe.String(item), Quantity: stripe.Int64(1), PriceData: price}}}, key)
}
func (g *StripeGateway) UpdateSubscriptionPaymentMethod(ctx context.Context, id, method, key string) error {
	return g.mutateSubscription(ctx, id, &stripe.SubscriptionUpdateParams{DefaultPaymentMethod: stripe.String(method)}, key)
}

// rawObject returns the JSON the processor sent for a call, so callers verify
// the processor's own fields rather than the SDK's typed view of them.
func rawObject(response *stripe.APIResponse) (Object, error) {
	if response == nil {
		return nil, ErrProcessorUnavailable
	}
	return DecodeObject(response.RawJSON)
}

func (g *StripeGateway) RetrieveCheckout(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1CheckoutSessions.Retrieve(ctx, id, nil)
	if err != nil {
		return nil, ErrProcessorUnavailable
	}
	return rawObject(result.LastResponse)
}
func (g *StripeGateway) RetrieveSetupIntent(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1SetupIntents.Retrieve(ctx, id, nil)
	if err != nil {
		return nil, ErrProcessorUnavailable
	}
	return rawObject(result.LastResponse)
}

// RetrievePaymentIntent expands the latest charge, whose creation is when a
// card payment succeeded.
func (g *StripeGateway) RetrievePaymentIntent(ctx context.Context, id string) (Object, error) {
	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")
	result, err := g.client.V1PaymentIntents.Retrieve(ctx, id, params)
	if err != nil {
		return nil, ErrProcessorUnavailable
	}
	return rawObject(result.LastResponse)
}
func (g *StripeGateway) RetrieveRefund(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1Refunds.Retrieve(ctx, id, nil)
	if err != nil {
		return nil, ErrProcessorUnavailable
	}
	return rawObject(result.LastResponse)
}
func (g *StripeGateway) RetrieveSubscription(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1Subscriptions.Retrieve(ctx, id, nil)
	if err != nil {
		return nil, ErrProcessorUnavailable
	}
	return rawObject(result.LastResponse)
}

func (g *StripeGateway) CreateRefund(ctx context.Context, payment string, amount int64, command string) (Object, error) {
	params := &stripe.RefundCreateParams{PaymentIntent: stripe.String(payment), Amount: stripe.Int64(amount), Reason: stripe.String("requested_by_customer"), Metadata: map[string]string{"dad_staff_action": command}}
	params.SetIdempotencyKey("dad-staff-refund-" + command)
	result, err := g.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return nil, ErrProcessorUnavailable
	}
	return rawObject(result.LastResponse)
}

func (g *StripeGateway) ListPaymentRefunds(ctx context.Context, payment string) ([]Object, error) {
	result := []Object{}
	after := ""
	for range 10 {
		params := &stripe.RefundListParams{PaymentIntent: stripe.String(payment), ListParams: stripe.ListParams{Limit: stripe.Int64(100)}}
		if after != "" {
			params.StartingAfter = stripe.String(after)
		}
		page := g.client.V1Refunds.List(ctx, params)
		if page.Err() != nil {
			return nil, ErrProcessorUnavailable
		}
		raw, err := rawObject(page.LastResponse())
		if err != nil {
			return nil, err
		}
		rows, err := raw.Array("data")
		if err != nil || len(rows) > 100 {
			return nil, ErrFacts
		}
		result = append(result, rows...)
		if raw.BoolIs("has_more", false) {
			return result, nil
		}
		if !raw.BoolIs("has_more", true) || len(rows) == 0 {
			return nil, ErrFacts
		}
		last := rows[len(rows)-1].Text("id")
		if last == "" || last == after {
			return nil, ErrFacts
		}
		after = last
	}
	return nil, ErrProcessorUnavailable
}

// ListChargesByCreated lists every charge Stripe created in [start, end],
// read-only, for the daily reconciliation job. It pages exactly as
// ListPaymentRefunds does and rejects any live-mode object, so this test-only
// build never reconciles a live fact.
func (g *StripeGateway) ListChargesByCreated(ctx context.Context, start, end time.Time) ([]Object, error) {
	return g.listByCreated(ctx, start, end, func(ctx context.Context, created *stripe.RangeQueryParams, after string) listPage {
		params := &stripe.ChargeListParams{CreatedRange: created, ListParams: stripe.ListParams{Limit: stripe.Int64(100)}}
		if after != "" {
			params.StartingAfter = stripe.String(after)
		}
		return g.client.V1Charges.List(ctx, params)
	})
}

// ListRefundsByCreated lists every refund Stripe created in [start, end],
// read-only, with the same discipline as ListChargesByCreated.
func (g *StripeGateway) ListRefundsByCreated(ctx context.Context, start, end time.Time) ([]Object, error) {
	return g.listByCreated(ctx, start, end, func(ctx context.Context, created *stripe.RangeQueryParams, after string) listPage {
		params := &stripe.RefundListParams{CreatedRange: created, ListParams: stripe.ListParams{Limit: stripe.Int64(100)}}
		if after != "" {
			params.StartingAfter = stripe.String(after)
		}
		return g.client.V1Refunds.List(ctx, params)
	})
}

// listPage is the subset of the SDK's V1List iterator the paging loop reads,
// satisfied by both V1List[*Charge] and V1List[*Refund].
type listPage interface {
	Err() error
	LastResponse() *stripe.APIResponse
}

// listByCreated runs the shared bounded paging loop ListPaymentRefunds proved:
// Limit 100, a created range, starting_after, has_more termination, and the
// non-advancing-cursor guard. Each returned object must be livemode:false.
func (g *StripeGateway) listByCreated(ctx context.Context, start, end time.Time, list func(context.Context, *stripe.RangeQueryParams, string) listPage) ([]Object, error) {
	created := &stripe.RangeQueryParams{GreaterThanOrEqual: start.Unix(), LesserThanOrEqual: end.Unix()}
	result := []Object{}
	after := ""
	for range 100 {
		page := list(ctx, created, after)
		if page.Err() != nil {
			return nil, ErrProcessorUnavailable
		}
		raw, err := rawObject(page.LastResponse())
		if err != nil {
			return nil, err
		}
		rows, err := raw.Array("data")
		if err != nil || len(rows) > 100 {
			return nil, ErrFacts
		}
		for _, row := range rows {
			if !row.BoolIs("livemode", false) {
				return nil, ErrFacts
			}
		}
		result = append(result, rows...)
		if raw.BoolIs("has_more", false) {
			return result, nil
		}
		if !raw.BoolIs("has_more", true) || len(rows) == 0 {
			return nil, ErrFacts
		}
		last := rows[len(rows)-1].Text("id")
		if last == "" || last == after {
			return nil, ErrFacts
		}
		after = last
	}
	return nil, ErrProcessorUnavailable
}
