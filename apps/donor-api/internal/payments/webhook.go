// Package payments implements the sandbox-only Stripe boundary. A receipt from
// the inbox means durable delivery, never that a gift has been recognized.
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stripe/stripe-go/v87"
)

const APIVersion = "2026-09-30.endive"
const MaxWebhookBytes = 256 * 1024

var (
	ErrUnavailable  = errors.New("webhook verification unavailable")
	ErrInvalidEvent = errors.New("invalid Stripe event")
	ErrEventShape   = errors.New("invalid Stripe event shape")
)

type VerifiedEvent struct {
	id, kind string
	occurred time.Time
	data     []byte
}

func (e VerifiedEvent) ID() string            { return e.id }
func (e VerifiedEvent) Kind() string          { return e.kind }
func (e VerifiedEvent) OccurredAt() time.Time { return e.occurred }
func (e VerifiedEvent) Data() []byte          { return append([]byte(nil), e.data...) }

type Inbox interface {
	SaveVerifiedEvent(context.Context, VerifiedEvent) error
}
type Receiver struct {
	Secret string
	Inbox  Inbox
}

func (r Receiver) Receive(ctx context.Context, body []byte, signature string) error {
	event, err := VerifyEvent(body, signature, r.Secret)
	if err != nil {
		return err
	}
	if event == nil {
		return nil
	}
	if r.Inbox == nil {
		return ErrUnavailable
	}
	return r.Inbox.SaveVerifiedEvent(ctx, *event)
}

func VerifyEvent(body []byte, signature, secret string) (*VerifiedEvent, error) {
	if secret == "" {
		return nil, ErrUnavailable
	}
	if len(body) > MaxWebhookBytes {
		return nil, ErrInvalidEvent
	}
	event, err := stripe.ConstructEvent(body, signature, secret)
	if err != nil {
		return nil, ErrInvalidEvent
	}
	// The SDK permits compatible release-train versions; our stored fact contract
	// requires this exact version and an explicitly false live-mode flag.
	var envelope struct {
		Livemode *bool  `json:"livemode"`
		Created  *int64 `json:"created"`
	}
	if err = json.Unmarshal(body, &envelope); err != nil || envelope.Livemode == nil || *envelope.Livemode || event.Account != "" || event.APIVersion != APIVersion {
		return nil, ErrInvalidEvent
	}
	switch string(event.Type) {
	case "payment_intent.succeeded", "invoice.payment_succeeded", "invoice.payment_failed", "refund.created", "refund.updated", "refund.failed", "checkout.session.completed", "checkout.session.expired", "customer.subscription.deleted", "customer.subscription.updated":
	default:
		return nil, nil
	}
	if envelope.Created == nil || *envelope.Created < 0 || *envelope.Created > 253402300799 || event.ID == "" || len(event.ID) > 255 || event.Data == nil {
		return nil, ErrEventShape
	}
	var obj map[string]interface{}
	decoder := json.NewDecoder(bytes.NewReader(event.Data.Raw))
	decoder.UseNumber()
	if err = decoder.Decode(&obj); err != nil || obj == nil {
		return nil, ErrEventShape
	}
	data, keep, err := minimalObject(string(event.Type), obj)
	if err != nil {
		return nil, err
	}
	if !keep {
		return nil, nil
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, ErrEventShape
	}
	return &VerifiedEvent{id: event.ID, kind: string(event.Type), occurred: time.Unix(*envelope.Created, 0).UTC(), data: encoded}, nil
}

func object(value interface{}) (map[string]interface{}, error) {
	if value == nil {
		return map[string]interface{}{}, nil
	}
	result, ok := value.(map[string]interface{})
	if !ok {
		return nil, ErrEventShape
	}
	return result, nil
}
func pick(obj map[string]interface{}, names ...string) map[string]interface{} {
	result := map[string]interface{}{}
	for _, key := range names {
		result[key] = obj[key]
	}
	return result
}
func uuid(value interface{}) error {
	if value == nil || value == "" {
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return ErrEventShape
	}
	var id pgtype.UUID
	if err := id.Scan(text); err != nil || !id.Valid {
		return ErrEventShape
	}
	return nil
}
func minimalObject(kind string, obj map[string]interface{}) (map[string]interface{}, bool, error) {
	metadata, err := object(obj["metadata"])
	if err != nil {
		return nil, false, err
	}
	subscription := obj["subscription"]
	if strings.HasPrefix(kind, "invoice.") {
		parent, err := object(obj["parent"])
		if err != nil {
			return nil, false, err
		}
		details, err := object(parent["subscription_details"])
		if err != nil {
			return nil, false, err
		}
		metadata, err = object(details["metadata"])
		if err != nil {
			return nil, false, err
		}
		subscription = details["subscription"]
	}
	checkout := metadata["dad_checkout_id"]
	var update interface{}
	if strings.HasPrefix(kind, "checkout.session.") {
		update = metadata["dad_payment_update_id"]
	}
	if checkout == nil || checkout == "" {
		checkout = nil
	}
	if update == nil || update == "" {
		update = nil
	}
	if checkout == nil && update == nil && !strings.HasPrefix(kind, "refund.") {
		return nil, false, nil
	}
	if uuid(checkout) != nil || uuid(update) != nil {
		return nil, false, ErrEventShape
	}
	id, ok := obj["id"].(string)
	if !ok || id == "" || len(id) > 255 {
		return nil, false, ErrEventShape
	}
	data := map[string]interface{}{"id": id, "checkout_id": checkout, "subscription": subscription}
	for _, key := range []string{"status", "currency", "amount_received", "amount_paid", "amount_due", "total", "billing_reason", "starting_balance", "ending_balance"} {
		if value, present := obj[key]; present {
			data[key] = value
		}
	}
	if strings.HasPrefix(kind, "invoice.") {
		lines, err := invoiceLines(obj["lines"])
		if err != nil {
			return nil, false, err
		}
		payments, err := invoicePayments(obj["payments"])
		if err != nil {
			return nil, false, err
		}
		for key, value := range lines {
			data[key] = value
		}
		for key, value := range payments {
			data[key] = value
		}
		transitions, err := object(obj["status_transitions"])
		if err != nil {
			return nil, false, err
		}
		data["paid_at"] = transitions["paid_at"]
	}
	if strings.HasPrefix(kind, "refund.") {
		data = pick(obj, "id", "payment_intent", "amount", "currency", "status")
	}
	if update != nil {
		data = map[string]interface{}{"id": id, "payment_update_id": update}
	}
	return data, true, nil
}

func page(value interface{}) ([]interface{}, bool, error) {
	obj, err := object(value)
	if err != nil {
		return nil, false, err
	}
	complete, ok := obj["has_more"].(bool)
	complete = ok && !complete
	if obj["data"] == nil {
		return []interface{}{}, complete, nil
	}
	rows, ok := obj["data"].([]interface{})
	if !ok {
		return nil, false, ErrEventShape
	}
	return rows, complete, nil
}
func invoiceLines(value interface{}) (map[string]interface{}, error) {
	input, complete, err := page(value)
	if err != nil {
		return nil, err
	}
	rows := make([]interface{}, 0, len(input))
	for _, raw := range input {
		line, err := object(raw)
		if err != nil {
			return nil, err
		}
		parent, err := object(line["parent"])
		if err != nil {
			return nil, err
		}
		details, err := object(parent["subscription_item_details"])
		if err != nil {
			return nil, err
		}
		period, err := object(line["period"])
		if err != nil {
			return nil, err
		}
		row := pick(line, "amount", "currency")
		row["period"] = pick(period, "start", "end")
		row["parent_type"] = parent["type"]
		row["subscription"] = details["subscription"]
		row["proration"] = details["proration"]
		rows = append(rows, row)
	}
	return map[string]interface{}{"lines_complete": complete, "lines": rows}, nil
}
func invoicePayments(value interface{}) (map[string]interface{}, error) {
	input, complete, err := page(value)
	if err != nil {
		return nil, err
	}
	rows := make([]interface{}, 0, len(input))
	for _, raw := range input {
		p, err := object(raw)
		if err != nil {
			return nil, err
		}
		payment, err := object(p["payment"])
		if err != nil {
			return nil, err
		}
		row := pick(p, "status", "amount_paid", "currency", "invoice", "livemode")
		row["type"] = payment["type"]
		row["payment_intent"] = payment["payment_intent"]
		rows = append(rows, row)
	}
	return map[string]interface{}{"payments_complete": complete, "payments": rows}, nil
}
