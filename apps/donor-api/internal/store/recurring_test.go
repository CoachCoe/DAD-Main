package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/payments"
	"impactgraph/donor-api/internal/store/queries"
)

// subscriptionGateway applies recurring mutations to the fixture's processor
// state the way Stripe would, and records each idempotency key.
type subscriptionGateway struct {
	*financialGateway
	t              *testing.T
	mu             sync.Mutex
	keys           []string
	fail           error
	retrievals     int
	beforeRetrieve func()
	refundRows     []payments.Object
	refundLists    int
	beforeList     func()
}

func (g *subscriptionGateway) RetrieveSubscription(ctx context.Context, id string) (payments.Object, error) {
	g.mu.Lock()
	g.retrievals++
	hook := g.beforeRetrieve
	g.beforeRetrieve = nil
	g.mu.Unlock()
	if hook != nil {
		hook()
	}
	return g.financialGateway.RetrieveSubscription(ctx, id)
}
func (g *subscriptionGateway) CreateCheckout(context.Context, []byte, string) (payments.RemoteCheckout, error) {
	return payments.RemoteCheckout{}, payments.ErrCheckoutUnavailable
}
func (g *subscriptionGateway) mutate(id, key string, edit func(map[string]any)) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.keys = append(g.keys, key)
	if err := g.fail; err != nil {
		g.fail = nil
		return err
	}
	raw, err := json.Marshal(g.subscriptions[id])
	if err != nil {
		return err
	}
	var remote map[string]any
	if err = json.Unmarshal(raw, &remote); err != nil {
		return err
	}
	edit(remote)
	g.subscriptions[id] = paymentObject(g.t, remote)
	return nil
}
func (g *subscriptionGateway) ScheduleCancellation(_ context.Context, id string, end int64, key string) error {
	return g.mutate(id, key, func(r map[string]any) { r["cancel_at"] = end })
}
func (g *subscriptionGateway) PauseCollection(_ context.Context, id string, resumes int64, key string) error {
	return g.mutate(id, key, func(r map[string]any) {
		r["pause_collection"] = map[string]any{"behavior": "void", "resumes_at": resumes}
	})
}
func (g *subscriptionGateway) ChangeMonthlyAmount(_ context.Context, id, item, product string, amount int64, key string) error {
	return g.mutate(id, key, func(r map[string]any) {
		line := subscriptionItem(r)
		line["id"] = item
		line["price"].(map[string]any)["product"] = product
		line["price"].(map[string]any)["unit_amount"] = amount
	})
}
func (g *subscriptionGateway) ListPaymentRefunds(context.Context, string) ([]payments.Object, error) {
	g.refundLists++
	if hook := g.beforeList; hook != nil {
		g.beforeList = nil
		hook()
	}
	return g.refundRows, nil
}
func (g *subscriptionGateway) CreateRefund(context.Context, string, int64, string) (payments.Object, error) {
	return nil, payments.ErrCheckoutUnavailable
}

type recurringFixture struct {
	*workerFixture
	checkout string
	sub      *subscriptionGateway
	service  RecurringService
	token    string
	attempt  pgtype.UUID
	subID    string
	start    time.Time
	end      time.Time
}

// newRecurringFixture binds an active monthly subscription that renews on its
// billing anchor one calendar month after the current period started.
func newRecurringFixture(t *testing.T) *recurringFixture {
	t.Helper()
	f := newWorkerFixture(t, "MONTHLY")
	result := f.checkout(t)
	ctx := context.Background()
	pool := f.service.DB
	start := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	// Month-end anchors are covered by TestRenewalAfterKeepsMonthEndAnchors.
	if day := start.Day(); day > 28 {
		start = start.AddDate(0, 0, 28-day)
	}
	end := start.AddDate(0, 1, 0)
	subID := "sub_" + result.CheckoutID
	var attempt pgtype.UUID
	if err := pool.QueryRow(ctx, "UPDATE donor_stripe_checkouts SET stripe_subscription_id=$2,status='COMPLETE' WHERE checkout_id=$1 RETURNING id", result.CheckoutID, subID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			"DELETE FROM donor_refund_observations WHERE source_action_id IN (SELECT id FROM donor_staff_actions WHERE donor_id=$1)",
			"DELETE FROM donor_staff_actions WHERE donor_id=$1",
			"DELETE FROM donor_cancellations WHERE donor_id=$1",
		} {
			if _, err := pool.Exec(context.Background(), sql, f.donorID); err != nil {
				t.Error(err)
			}
		}
	})
	remote := subscription(result.CheckoutID, start)
	remote["billing_cycle_anchor"] = start.Unix()
	subscriptionItem(remote)["current_period_end"] = end.Unix()
	f.gateway.subscriptions[subID] = paymentObject(t, remote)
	token, err := (AccountService{DB: pool}).ClaimAccount(ctx, link(t, pool, f.donorID, time.Now()), "")
	if err != nil {
		t.Fatal(err)
	}
	gateway := &subscriptionGateway{financialGateway: f.gateway, t: t}
	return &recurringFixture{workerFixture: f, checkout: result.CheckoutID, sub: gateway, service: RecurringService{DB: pool, Gateway: gateway, Origin: "https://donor.example.org"}, token: token, attempt: attempt, subID: subID, start: start, end: end}
}

// jsonView decodes a service result the way the web client receives it.
func jsonView(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func jsonTime(t *testing.T, value any) time.Time {
	t.Helper()
	text, _ := value.(string)
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t.Fatalf("not a timestamp: %v", value)
	}
	return parsed
}
func assertKeys(t *testing.T, view map[string]any, keys ...string) {
	t.Helper()
	if len(view) != len(keys) {
		t.Fatalf("fields %v, want %v", view, keys)
	}
	for _, key := range keys {
		if _, ok := view[key]; !ok {
			t.Fatalf("missing field %s in %v", key, view)
		}
	}
}

func TestRenewalAfterKeepsMonthEndAnchors(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 10, 30, 0, 0, time.UTC) }
	for _, c := range []struct {
		name             string
		boundary, anchor time.Time
		months           int32
		want             time.Time
	}{
		{"31st clamps to February", at(2027, 1, 31), at(2026, 12, 31), 1, at(2027, 2, 28)},
		{"short month returns to 31st", at(2027, 2, 28), at(2026, 12, 31), 1, at(2027, 3, 31)},
		{"leap year February", at(2028, 1, 31), at(2027, 10, 31), 1, at(2028, 2, 29)},
		{"from leap day", at(2028, 2, 29), at(2027, 10, 31), 2, at(2028, 4, 30)},
		{"year boundary", at(2027, 12, 31), at(2027, 10, 31), 2, at(2028, 2, 29)},
		{"30th in February", at(2027, 2, 28), at(2027, 1, 30), 3, at(2027, 5, 30)},
		{"mid-month", at(2027, 3, 15), at(2027, 1, 15), 3, at(2027, 6, 15)},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := renewalAfter(c.boundary, c.months, c.anchor)
			if err != nil || !got.Equal(c.want) {
				t.Fatalf("got %s %v, want %s", got, err, c.want)
			}
		})
	}
	for _, c := range []struct {
		name             string
		boundary, anchor time.Time
		months           int32
	}{
		{"boundary off anchor day", at(2027, 3, 30), at(2027, 1, 31), 1},
		{"boundary off anchor time", at(2027, 3, 15).Add(time.Second), at(2027, 1, 15), 1},
		{"no months", at(2027, 3, 15), at(2027, 1, 15), 0},
		{"more than three months", at(2027, 3, 15), at(2027, 1, 15), 4},
	} {
		if _, err := renewalAfter(c.boundary, c.months, c.anchor); !errors.Is(err, payments.ErrFacts) {
			t.Fatalf("%s accepted: %v", c.name, err)
		}
	}
}

func TestRecurringDetailsReadsProcessorOutsideDonorLock(t *testing.T) {
	for _, reconciledMeanwhile := range []bool{false, true} {
		name := "snapshot used"
		if reconciledMeanwhile {
			name = "stale snapshot refetched"
		}
		t.Run(name, func(t *testing.T) {
			r := newRecurringFixture(t)
			ctx := context.Background()
			pool := r.service.DB
			if _, err := r.service.Details(ctx, r.token); err != nil {
				t.Fatal(err)
			}
			r.sub.retrievals = 0
			reading, release := make(chan struct{}), make(chan struct{})
			r.sub.beforeRetrieve = func() { close(reading); <-release }
			type outcome struct {
				result RecurringDetails
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := r.service.Details(ctx, r.token)
				done <- outcome{result, err}
			}()
			<-reading
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, lock, "SET LOCAL lock_timeout='2s'")
			if _, err = queries.New(lock).LockDonor(ctx, r.donorID); err != nil {
				t.Fatal("processor read held the donor lock", err)
			}
			if reconciledMeanwhile {
				mustExec(t, lock, "UPDATE donor_subscriptions SET observed_at=observed_at+interval '1 millisecond' WHERE attempt_id=$1", r.attempt)
			}
			if err = lock.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			close(release)
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			want := 1
			if reconciledMeanwhile {
				want = 2
			}
			if r.sub.retrievals != want {
				t.Fatalf("processor reads=%d want %d", r.sub.retrievals, want)
			}
			view := jsonView(t, got.result)
			assertKeys(t, view, "mode", "state", "subscription")
			sub := view["subscription"].(map[string]any)
			assertKeys(t, sub, "id", "amount_minor", "currency", "status", "period_end", "stops_at")
			if view["mode"] != "test" || view["state"] != "ready" || sub["id"] != r.attempt.String() || sub["amount_minor"] != float64(3000) || sub["currency"] != "USD" || sub["status"] != "active" || sub["stops_at"] != nil || !jsonTime(t, sub["period_end"]).Equal(r.end) {
				t.Fatalf("details: %v", view)
			}
		})
	}
}

func TestRecurringDetailsWithoutSubscription(t *testing.T) {
	r := newRecurringFixture(t)
	ctx := context.Background()
	if _, err := r.service.DB.Exec(ctx, "UPDATE donor_stripe_checkouts SET stripe_subscription_id=NULL WHERE id=$1", r.attempt); err != nil {
		t.Fatal(err)
	}
	result, err := r.service.Details(ctx, r.token)
	if err != nil {
		t.Fatal(err)
	}
	if view := jsonView(t, result); view["state"] != "preparing" || view["subscription"] != nil || view["mode"] != "test" {
		t.Fatalf("unbound subscription: %v", view)
	}
	if _, err = r.service.Details(ctx, "not-a-session"); !errors.Is(err, ErrDenied) {
		t.Fatal("anonymous details", err)
	}
}

func TestDonorCancellationStopsAtPeriodEndOnce(t *testing.T) {
	r := newRecurringFixture(t)
	ctx := context.Background()
	request := CancellationRequest{RequestID: testID(t), AttemptID: r.attempt, PeriodEnd: r.end}
	if _, err := r.service.Cancel(ctx, r.token, CancellationRequest{RequestID: testID(t), AttemptID: r.attempt, PeriodEnd: r.end.Add(time.Hour)}); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatal("cancellation at a different boundary", err)
	}
	if len(r.sub.keys) != 0 {
		t.Fatal("rejected cancellation reached processor")
	}
	result, err := r.service.Cancel(ctx, r.token, request)
	if err != nil {
		t.Fatal(err)
	}
	sub := jsonView(t, result)["subscription"].(map[string]any)
	if !jsonTime(t, sub["stops_at"]).Equal(r.end) || sub["status"] != "active" {
		t.Fatalf("cancellation view: %v", sub)
	}
	if _, err = r.service.Cancel(ctx, r.token, request); err != nil {
		t.Fatal("retry", err)
	}
	if len(r.sub.keys) != 1 || r.sub.keys[0] != "dad-cancel-"+request.RequestID.String() {
		t.Fatalf("processor mutations: %v", r.sub.keys)
	}
	var completed bool
	if err = r.service.DB.QueryRow(ctx, "SELECT completed_at IS NOT NULL FROM donor_cancellations WHERE id=$1", request.RequestID).Scan(&completed); err != nil || !completed {
		t.Fatal("cancellation not completed", err)
	}
	changed := request
	changed.PeriodEnd = r.end.Add(-time.Hour)
	if _, err = r.service.Cancel(ctx, r.token, changed); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatal("idempotency key reused for another boundary", err)
	}
	invalid := request
	invalid.RequestID.Bytes[6] = 0x10
	if _, err = r.service.Cancel(ctx, r.token, invalid); !errors.Is(err, payments.ErrFacts) {
		t.Fatal("non-v4 request id accepted", err)
	}
}

func TestPauseOptionsPauseAndEarlyResume(t *testing.T) {
	r := newRecurringFixture(t)
	ctx := context.Background()
	options, err := r.service.PauseOptions(ctx, r.token)
	if err != nil {
		t.Fatal(err)
	}
	view := jsonView(t, options)
	assertKeys(t, view, "state", "subscription_id", "pause", "options")
	choices := view["options"].([]any)
	if view["state"] != "ready" || view["pause"] != nil || view["subscription_id"] != r.attempt.String() || len(choices) != 3 {
		t.Fatalf("pause options: %v", view)
	}
	for i, raw := range choices {
		choice := raw.(map[string]any)
		assertKeys(t, choice, "months", "starts_at", "resumes_at")
		want := r.end.AddDate(0, i+1, 0)
		if choice["months"] != float64(i+1) || !jsonTime(t, choice["starts_at"]).Equal(r.end) || !jsonTime(t, choice["resumes_at"]).Equal(want) {
			t.Fatalf("option %d: %v", i, choice)
		}
	}
	two := choices[1].(map[string]any)
	request := PauseRequest{RequestID: testID(t), AttemptID: r.attempt, Months: 2, StartsAt: r.end, ResumesAt: jsonTime(t, two["resumes_at"])}
	wrong := request
	wrong.RequestID, wrong.ResumesAt = testID(t), wrong.ResumesAt.Add(24*time.Hour)
	if _, err = r.service.Pause(ctx, r.token, wrong); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatal("unshown resume date accepted", err)
	}
	paused, err := r.service.Pause(ctx, r.token, request)
	if err != nil {
		t.Fatal(err)
	}
	pause := jsonView(t, paused)["pause"].(map[string]any)
	assertKeys(t, pause, "request_id", "months", "starts_at", "resumes_at", "confirmed")
	if pause["confirmed"] != true || pause["months"] != float64(2) || !jsonTime(t, pause["resumes_at"]).Equal(request.ResumesAt) {
		t.Fatalf("pause: %v", pause)
	}
	if _, err = r.service.Pause(ctx, r.token, request); err != nil {
		t.Fatal("pause retry", err)
	}
	if len(r.sub.keys) != 1 || r.sub.keys[0] != "dad-pause-"+request.RequestID.String() {
		t.Fatalf("processor mutations: %v", r.sub.keys)
	}
	if again, err := r.service.PauseOptions(ctx, r.token); err != nil || len(jsonView(t, again)["options"].([]any)) != 0 || jsonView(t, again)["pause"] == nil {
		t.Fatal("active pause still offered new pauses", again, err)
	}
	resumable, err := r.service.ResumeOptions(ctx, r.token)
	if err != nil {
		t.Fatal(err)
	}
	offer := jsonView(t, resumable)
	assertKeys(t, offer, "state", "pause_id", "next_renewal", "original_resume")
	if offer["state"] != "ready" || offer["pause_id"] != request.RequestID.String() || !jsonTime(t, offer["next_renewal"]).Equal(r.end) || !jsonTime(t, offer["original_resume"]).Equal(request.ResumesAt) {
		t.Fatalf("resume options: %v", offer)
	}
	resume := ResumptionRequest{RequestID: testID(t), PauseID: request.RequestID, NextRenewal: r.end}
	resumed, err := r.service.Resume(ctx, r.token, resume)
	if err != nil {
		t.Fatal(err)
	}
	if view := jsonView(t, resumed); view["confirmed"] != true || view["pause_id"] != request.RequestID.String() || !jsonTime(t, view["next_renewal"]).Equal(r.end) {
		t.Fatalf("resumption: %v", view)
	}
	if len(r.sub.keys) != 2 || r.sub.keys[1] != "dad-resume-"+resume.RequestID.String() {
		t.Fatalf("processor mutations: %v", r.sub.keys)
	}
	saved, err := r.service.ResumeOptions(ctx, r.token)
	if err != nil || jsonView(t, saved)["state"] != "saved" {
		t.Fatal("saved resumption not reported", saved, err)
	}
}

func TestAmountChangeAppliesAtNextRenewal(t *testing.T) {
	r := newRecurringFixture(t)
	ctx := context.Background()
	options, err := r.service.AmountOptions(ctx, r.token)
	if err != nil {
		t.Fatal(err)
	}
	view := jsonView(t, options)
	assertKeys(t, view, "state", "subscription_id", "amount_minor", "effective_at", "change")
	if view["state"] != "ready" || view["amount_minor"] != float64(3000) || view["change"] != nil || !jsonTime(t, view["effective_at"]).Equal(r.end) {
		t.Fatalf("amount options: %v", view)
	}
	request := AmountRequest{RequestID: testID(t), AttemptID: r.attempt, Amount: 9000, Previous: 3000, EffectiveAt: r.end}
	if _, err = r.service.ChangeAmount(ctx, r.token, AmountRequest{RequestID: testID(t), AttemptID: r.attempt, Amount: 9000, Previous: 3000, EffectiveAt: r.start}); !errors.Is(err, ErrSubscriptionConflict) {
		t.Fatal("mid-period amount change accepted", err)
	}
	changed, err := r.service.ChangeAmount(ctx, r.token, request)
	if err != nil {
		t.Fatal(err)
	}
	change := jsonView(t, changed)
	assertKeys(t, change, "request_id", "amount_minor", "previous_minor", "effective_at", "confirmed")
	if change["confirmed"] != true || change["amount_minor"] != float64(9000) || change["previous_minor"] != float64(3000) || !jsonTime(t, change["effective_at"]).Equal(r.end) {
		t.Fatalf("amount change: %v", change)
	}
	if _, err = r.service.ChangeAmount(ctx, r.token, request); err != nil {
		t.Fatal("retry", err)
	}
	if len(r.sub.keys) != 1 || r.sub.keys[0] != "dad-amount-"+request.RequestID.String() {
		t.Fatalf("processor mutations: %v", r.sub.keys)
	}
	// The current period keeps the old amount: neither prorated nor repriced.
	repriced := invoice(r.checkout, "in_repriced_"+r.donorID.String(), r.start, true)
	for _, key := range []string{"amount_paid", "amount_due", "total"} {
		repriced[key] = 9000
	}
	repriced["lines"].(map[string]any)["data"].([]any)[0].(map[string]any)["amount"] = 9000
	repriced["payments"].(map[string]any)["data"].([]any)[0].(map[string]any)["amount_paid"] = 9000
	rejected := r.receive(t, "invoice.payment_succeeded", repriced)
	r.process(t)
	assertEventProcessed(t, r.workerFixture, rejected, false)
	accepted := r.receive(t, "invoice.payment_succeeded", invoice(r.checkout, "in_current_"+r.donorID.String(), r.start, true))
	r.process(t)
	assertEventProcessed(t, r.workerFixture, accepted, true)
	details, err := r.service.Details(ctx, r.token)
	if err != nil || jsonView(t, details)["subscription"].(map[string]any)["amount_minor"] != float64(9000) {
		t.Fatal("next renewal amount not shown", details, err)
	}
}

func TestStaffCancellationIsAuditedWithReasonAndOutcome(t *testing.T) {
	pool := database(t)
	// Staff outlive the fixture's staff actions, so they are created first.
	_, finance := staffFixture(t, pool, "FINANCE")
	_, support := staffFixture(t, pool, "SUPPORT")
	r := newRecurringFixture(t)
	ctx := context.Background()
	s := StaffFinanceService{Staff: &StaffService{DB: pool}, Gateway: r.sub, Origin: "https://donor.example.org"}
	request := StaffFinancialRequest{RequestID: testID(t), DonorID: r.donorID, TargetID: r.attempt, Reason: "Donor asked by phone to stop", PeriodEnd: r.end}
	if _, err := s.Cancel(ctx, support, request); !errors.Is(err, ErrStaffForbidden) {
		t.Fatal("support cancelled a gift", err)
	}
	short := request
	short.Reason = "no"
	if _, err := s.Cancel(ctx, finance, short); !errors.Is(err, ErrStaffInput) {
		t.Fatal("cancellation without a reason", err)
	}
	quote, err := s.CancellationQuote(ctx, finance, r.donorID)
	if err != nil || !jsonTime(t, jsonView(t, quote)["subscription"].(map[string]any)["period_end"]).Equal(r.end) {
		t.Fatal("cancellation quote", quote, err)
	}
	r.sub.fail = payments.ErrCheckoutUnavailable
	if _, err = s.Cancel(ctx, finance, request); !errors.Is(err, payments.ErrCheckoutUnavailable) {
		t.Fatal("processor failure hidden", err)
	}
	result, err := s.Cancel(ctx, finance, request)
	if err != nil {
		t.Fatal(err)
	}
	view := jsonView(t, result)
	if view["request_id"] != request.RequestID.String() || !jsonTime(t, view["subscription"].(map[string]any)["stops_at"]).Equal(r.end) {
		t.Fatalf("staff cancellation: %v", view)
	}
	if len(r.sub.keys) != 2 || r.sub.keys[0] != r.sub.keys[1] {
		t.Fatal("retry changed the processor idempotency key", r.sub.keys)
	}
	var status string
	if err = pool.QueryRow(ctx, "SELECT status FROM donor_staff_actions WHERE id=$1", request.RequestID).Scan(&status); err != nil || status != "OBSERVED" {
		t.Fatal("staff action not completed", status, err)
	}
	rows, err := pool.Query(ctx, "SELECT action,details::text FROM donor_staff_audit WHERE target=$1 ORDER BY created_at,action", request.RequestID.String())
	if err != nil {
		t.Fatal(err)
	}
	audit := map[string]map[string]string{}
	for rows.Next() {
		var action, raw string
		if err = rows.Scan(&action, &raw); err != nil {
			t.Fatal(err)
		}
		details := map[string]string{}
		if err = json.Unmarshal([]byte(raw), &details); err != nil {
			t.Fatal(err)
		}
		audit[action] = details
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"gift.action_authorized", "gift.cancellation_failed", "gift.cancellation_confirmed"} {
		if audit[action]["reason"] != request.Reason {
			t.Fatalf("%s audit lacks reason: %v", action, audit)
		}
	}
	if audit["gift.cancellation_failed"]["outcome"] != "processor_unavailable" {
		t.Fatalf("failure outcome not audited: %v", audit)
	}
}

func TestRefundQuoteReconcilesStaffRefunds(t *testing.T) {
	for _, recordedMeanwhile := range []bool{false, true} {
		name := "snapshot used"
		if recordedMeanwhile {
			name = "stale snapshot refetched"
		}
		t.Run(name, func(t *testing.T) {
			pool := database(t)
			actor, finance := staffFixture(t, pool, "FINANCE")
			r := newRecurringFixture(t)
			ctx := context.Background()
			r.receive(t, "invoice.payment_succeeded", invoice(r.checkout, "in_quote_"+r.donorID.String(), r.start, true))
			r.process(t)
			var gift queries.DonorGift
			if err := pool.QueryRow(ctx, "SELECT id,stripe_payment_intent_id FROM donor_gifts WHERE donor_id=$1", r.donorID).Scan(&gift.ID, &gift.StripePaymentIntentID); err != nil {
				t.Fatal(err)
			}
			action := testID(t)
			if _, err := pool.Exec(ctx, "INSERT INTO donor_staff_actions(id,actor_id,donor_id,kind,target_id,amount_minor,reason) VALUES($1,$2,$3,'REFUND',$4,1000,'Duplicate gift reported')", action, actor, r.donorID, gift.ID); err != nil {
				t.Fatal(err)
			}
			refund := paymentObject(t, map[string]any{"id": "re_" + action.String(), "payment_intent": gift.StripePaymentIntentID.String, "amount": 1000, "currency": "usd", "status": "succeeded", "metadata": map[string]string{"dad_staff_action": action.String()}})
			r.sub.refundRows = []payments.Object{refund}
			if recordedMeanwhile {
				// A webhook records the refund between the quote's processor read and its lock.
				r.sub.beforeList = func() {
					event := r.receive(t, "refund.updated", map[string]any{"id": "re_" + action.String(), "payment_intent": gift.StripePaymentIntentID.String, "status": "pending"})
					r.gateway.refunds["re_"+action.String()] = paymentObject(t, map[string]any{"id": "re_" + action.String(), "payment_intent": gift.StripePaymentIntentID.String, "amount": 1000, "currency": "usd", "status": "pending"})
					r.process(t)
					assertEventProcessed(t, r.workerFixture, event, true)
				}
			}
			s := StaffFinanceService{Staff: &StaffService{DB: pool}, Gateway: r.sub, Origin: "https://donor.example.org"}
			quote, err := s.RefundQuote(ctx, finance, r.donorID, gift.ID)
			if err != nil {
				t.Fatal(err)
			}
			view := jsonView(t, quote)
			assertKeys(t, view, "gift_id", "donor_id", "amount_minor", "available_minor", "currency", "mode")
			if view["available_minor"] != float64(2000) || view["amount_minor"] != float64(3000) || view["gift_id"] != gift.ID.String() {
				t.Fatalf("quote: %v", view)
			}
			lists := 1
			if recordedMeanwhile {
				lists = 2
			}
			if r.sub.refundLists != lists {
				t.Fatalf("processor lists=%d want %d", r.sub.refundLists, lists)
			}
			var status, refundStatus string
			if err = pool.QueryRow(ctx, "SELECT a.status,r.status FROM donor_staff_actions a JOIN donor_refunds r ON r.stripe_refund_id=a.stripe_refund_id WHERE a.id=$1", action).Scan(&status, &refundStatus); err != nil || status != "OBSERVED" || refundStatus != "SUCCEEDED" {
				t.Fatal("staff refund not reconciled", status, refundStatus, err)
			}
			var reconciled, previews int
			if err = pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE action='gift.refund_reconciled' AND target=$2),count(*) FILTER (WHERE action='gift.refund_preview' AND target=$3) FROM donor_staff_audit WHERE actor_id=$1", actor, action.String(), gift.ID.String()).Scan(&reconciled, &previews); err != nil || reconciled != 1 || previews != 1 {
				t.Fatal("quote not audited", reconciled, previews, err)
			}
			p, err := queries.New(pool).Projection(ctx, r.donorID)
			if err != nil || p.NetMinor.Int64 != 2000 {
				t.Fatal("reconciled refund not projected", err)
			}
		})
	}
}
