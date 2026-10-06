package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

type refundGateway struct {
	payments.StaffFinanceGateway
	refund              payments.Object
	creates             int
	loseAcknowledgement bool
	beforeCreate        func()
	t                   *testing.T
}

func (g *refundGateway) ListPaymentRefunds(context.Context, string) ([]payments.Object, error) {
	if g.refund == nil {
		return nil, nil
	}
	return []payments.Object{g.refund}, nil
}
func (g *refundGateway) CreateRefund(_ context.Context, payment string, amount int64, command string) (payments.Object, error) {
	if g.beforeCreate != nil {
		g.beforeCreate()
	}
	g.creates++
	g.refund = paymentObject(g.t, map[string]any{"id": "re_" + command, "payment_intent": payment, "currency": "usd", "amount": amount, "status": "succeeded", "metadata": map[string]string{"dad_staff_action": command}})
	if g.loseAcknowledgement {
		g.loseAcknowledgement = false
		return nil, payments.ErrProcessorUnavailable
	}
	return g.refund, nil
}

func TestStaffRefundLostAcknowledgementPermissionsAndAudit(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	_, finance := staffFixture(t, pool, "FINANCE")
	_, support := staffFixture(t, pool, "SUPPORT")
	f := newWorkerFixture(t, "MONTHLY")
	checkout := f.checkout(t)
	invoiceID := "in_finance_" + f.donorID.String()
	f.receive(t, "invoice.payment_succeeded", invoice(checkout.CheckoutID, invoiceID, time.Now().Add(-48*time.Hour), true))
	f.process(t)
	var giftID pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM donor_gifts WHERE donor_id=$1", f.donorID).Scan(&giftID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			"DELETE FROM donor_refund_observations WHERE source_action_id IN (SELECT id FROM donor_staff_actions WHERE donor_id=$1)",
			"DELETE FROM donor_staff_actions WHERE donor_id=$1",
		} {
			if _, err := pool.Exec(context.Background(), sql, f.donorID); err != nil {
				t.Error(err)
			}
		}
	})
	gateway := &refundGateway{t: t, loseAcknowledgement: true}
	s := StaffFinanceService{Staff: &StaffService{DB: pool}, Gateway: gateway, Origin: "https://example.org"}
	r := StaffFinancialRequest{RequestID: testID(t), DonorID: f.donorID, TargetID: giftID, Amount: 1000, Reason: "Donor requested partial refund"}
	if _, err := s.Refund(ctx, support, r); !errors.Is(err, ErrStaffForbidden) {
		t.Fatal("support permitted refund", err)
	}
	if gateway.creates != 0 {
		t.Fatal("denied action reached processor")
	}
	if _, err := s.Refund(ctx, finance, r); !errors.Is(err, payments.ErrProcessorUnavailable) {
		t.Fatal("expected lost acknowledgement", err)
	}
	for range 2 {
		result, err := s.Refund(ctx, finance, r)
		if err != nil || result.Status != "SUCCEEDED" {
			t.Fatal("refund reconciliation failed", result, err)
		}
	}
	if gateway.creates != 1 {
		t.Fatal("retry duplicated processor refund")
	}
	projection, err := queries.New(pool).Projection(ctx, f.donorID)
	if err != nil || projection.NetMinor.Int64 != 2000 || projection.CurrentStreak.Int32 != 1 || !projection.FoundingAwardedAt.Valid {
		t.Fatal("partial refund changed recognition incorrectly", err)
	}
	var authorizations, observations int
	if err = pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE action='gift.action_authorized'),count(*) FILTER (WHERE action='gift.refund_observed') FROM donor_staff_audit WHERE target=$1", r.RequestID.String()).Scan(&authorizations, &observations); err != nil || authorizations != 1 || observations < 1 {
		t.Fatal("refund audit incomplete", err)
	}
	r.Amount = 2000
	if _, err = s.Refund(ctx, finance, r); !errors.Is(err, ErrStaffConflict) {
		t.Fatal("idempotency payload changed", err)
	}
}

func TestStaffRefundCallsProcessorOutsideDonorLock(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	_, finance := staffFixture(t, pool, "FINANCE")
	f := newWorkerFixture(t, "MONTHLY")
	checkout := f.checkout(t)
	f.receive(t, "invoice.payment_succeeded", invoice(checkout.CheckoutID, "in_lock_"+f.donorID.String(), time.Now().Add(-48*time.Hour), true))
	f.process(t)
	var giftID pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM donor_gifts WHERE donor_id=$1", f.donorID).Scan(&giftID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			"DELETE FROM donor_refund_observations WHERE source_action_id IN (SELECT id FROM donor_staff_actions WHERE donor_id=$1)",
			"DELETE FROM donor_staff_actions WHERE donor_id=$1",
		} {
			if _, err := pool.Exec(context.Background(), sql, f.donorID); err != nil {
				t.Error(err)
			}
		}
	})
	stalled := newGate(t)
	gateway := &refundGateway{t: t, beforeCreate: stalled.wait}
	s := StaffFinanceService{Staff: &StaffService{DB: pool}, Gateway: gateway, Origin: "https://example.org"}
	r := StaffFinancialRequest{RequestID: testID(t), DonorID: f.donorID, TargetID: giftID, Amount: 1000, Reason: "Donor requested partial refund"}
	var result StaffRefund
	err := whileStalled(t, stalled, func() error {
		var err error
		result, err = s.Refund(ctx, finance, r)
		return err
	}, func() { assertDonorUnlocked(t, pool, f.donorID) })
	if err != nil || result.Status != "SUCCEEDED" || gateway.creates != 1 {
		t.Fatal("refund not recorded", result, err)
	}
	var status string
	if err = pool.QueryRow(ctx, "SELECT status FROM donor_staff_actions WHERE id=$1", r.RequestID).Scan(&status); err != nil || status != "OBSERVED" {
		t.Fatal("staff action not completed", status, err)
	}
}
