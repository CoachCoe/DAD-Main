package payments

import (
	"context"

	"github.com/stripe/stripe-go/v87"
)

type StaffFinanceGateway interface {
	SubscriptionGateway
	ListPaymentRefunds(context.Context, string) ([]Object, error)
	CreateRefund(context.Context, string, int64, string) (Object, error)
}

func (g *StripeGateway) CreateRefund(ctx context.Context, payment string, amount int64, command string) (Object, error) {
	params := &stripe.RefundCreateParams{PaymentIntent: stripe.String(payment), Amount: stripe.Int64(amount), Reason: stripe.String("requested_by_customer"), Metadata: map[string]string{"dad_staff_action": command}}
	params.SetIdempotencyKey("dad-staff-refund-" + command)
	result, err := g.client.V1Refunds.Create(ctx, params)
	if err != nil || result == nil || result.LastResponse == nil {
		return nil, ErrCheckoutUnavailable
	}
	return DecodeObject(result.LastResponse.RawJSON)
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
		if page.Err() != nil || page.LastResponse() == nil {
			return nil, ErrCheckoutUnavailable
		}
		raw, err := DecodeObject(page.LastResponse().RawJSON)
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
	return nil, ErrCheckoutUnavailable
}
