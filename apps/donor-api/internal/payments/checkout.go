package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stripe/stripe-go/v87"
)

var (
	ErrCheckoutConflict    = errors.New("checkout conflicts with an existing request")
	ErrCheckoutUnavailable = errors.New("checkout is temporarily unavailable; retry the same request")
	ErrCheckoutInput       = errors.New("invalid checkout fields")
)

type CheckoutRequest struct {
	RequestID pgtype.UUID
	Email     string
	Amount    int64
	Frequency string
	Visitor   *string
}
type CheckoutResult struct {
	CheckoutID string `json:"checkoutId"`
	URL        string `json:"url"`
}
type RemoteCheckout struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Livemode *bool  `json:"livemode"`
}
type CheckoutGateway interface {
	CreateCheckout(context.Context, []byte, string) (RemoteCheckout, error)
}

func ValidateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return errors.New("DONOR_PUBLIC_ORIGIN must be an HTTPS origin (HTTP localhost for tests)")
	}
	return nil
}

type StripeGateway struct{ client *stripe.Client }

type FinancialGateway interface {
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

func (g *StripeGateway) mutateSubscription(ctx context.Context, id string, values map[string]interface{}, key string) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return ErrCheckoutUnavailable
	}
	var params stripe.SubscriptionUpdateParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&params); err != nil {
		return ErrCheckoutUnavailable
	}
	params.SetIdempotencyKey(key)
	if _, err = g.client.V1Subscriptions.Update(ctx, id, &params); err != nil {
		return ErrCheckoutUnavailable
	}
	return nil
}
func (g *StripeGateway) ScheduleCancellation(ctx context.Context, id string, end int64, key string) error {
	return g.mutateSubscription(ctx, id, map[string]interface{}{"cancel_at": end, "proration_behavior": "none"}, key)
}
func (g *StripeGateway) PauseCollection(ctx context.Context, id string, resumes int64, key string) error {
	return g.mutateSubscription(ctx, id, map[string]interface{}{"pause_collection": map[string]interface{}{"behavior": "void", "resumes_at": resumes}, "proration_behavior": "none"}, key)
}
func (g *StripeGateway) ChangeMonthlyAmount(ctx context.Context, id, item, product string, amount int64, key string) error {
	return g.mutateSubscription(ctx, id, map[string]interface{}{"items": []interface{}{map[string]interface{}{"id": item, "quantity": 1, "price_data": map[string]interface{}{"currency": "usd", "product": product, "recurring": map[string]interface{}{"interval": "month"}, "unit_amount": amount}}}, "proration_behavior": "none"}, key)
}

func (g *StripeGateway) RetrieveCheckout(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1CheckoutSessions.Retrieve(ctx, id, nil)
	if err != nil || result == nil || result.LastResponse == nil {
		return nil, ErrCheckoutUnavailable
	}
	return DecodeObject(result.LastResponse.RawJSON)
}
func (g *StripeGateway) RetrieveSetupIntent(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1SetupIntents.Retrieve(ctx, id, nil)
	if err != nil || result == nil || result.LastResponse == nil {
		return nil, ErrCheckoutUnavailable
	}
	return DecodeObject(result.LastResponse.RawJSON)
}
func (g *StripeGateway) UpdateSubscriptionPaymentMethod(ctx context.Context, id, method, key string) error {
	params := &stripe.SubscriptionUpdateParams{DefaultPaymentMethod: stripe.String(method), ProrationBehavior: stripe.String("none")}
	params.SetIdempotencyKey(key)
	if _, err := g.client.V1Subscriptions.Update(ctx, id, params); err != nil {
		return ErrCheckoutUnavailable
	}
	return nil
}

func (g *StripeGateway) RetrieveRefund(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1Refunds.Retrieve(ctx, id, nil)
	if err != nil || result == nil || result.LastResponse == nil {
		return nil, ErrCheckoutUnavailable
	}
	return DecodeObject(result.LastResponse.RawJSON)
}
func (g *StripeGateway) RetrieveSubscription(ctx context.Context, id string) (Object, error) {
	result, err := g.client.V1Subscriptions.Retrieve(ctx, id, nil)
	if err != nil || result == nil || result.LastResponse == nil {
		return nil, ErrCheckoutUnavailable
	}
	return DecodeObject(result.LastResponse.RawJSON)
}

func NewStripeGateway(key string) (*StripeGateway, error) {
	if key == "" {
		return nil, ErrCheckoutUnavailable
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
		return RemoteCheckout{}, ErrCheckoutUnavailable
	}
	params.SetIdempotencyKey(key)
	session, err := g.client.V1CheckoutSessions.Create(ctx, &params)
	if err != nil {
		return RemoteCheckout{}, ErrCheckoutUnavailable
	}
	var result RemoteCheckout
	if session == nil || session.LastResponse == nil || json.Unmarshal(session.LastResponse.RawJSON, &result) != nil {
		return result, ErrCheckoutUnavailable
	}
	return result, nil
}
func ValidateRemoteCheckout(remote RemoteCheckout) error {
	u, err := url.Parse(remote.URL)
	if err != nil || !strings.HasPrefix(remote.ID, "cs_test_") || len(remote.ID) > 255 || remote.Livemode == nil || *remote.Livemode || u.Scheme != "https" || u.Hostname() != "checkout.stripe.com" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return ErrCheckoutUnavailable
	}
	return nil
}
