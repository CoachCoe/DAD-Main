from copy import deepcopy
from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donor_projections import refresh_projection
from impactgraph.donor_subscriptions import (
    SubscriptionConflict,
    cancel_recurring,
    recurring_details,
)
from impactgraph.donors import GiftFrequency, claim_account, guest_donor, issue_login_link
from impactgraph.persistence import (
    DonorCancellationRecord,
    DonorGiftRecord,
    DonorSubscriptionRecord,
    StripeCheckoutRecord,
    StripeEventRecord,
)
from impactgraph.stripe_payments import PaymentUnavailable, accept_event, process_next_event
from tests.test_stripe_payments import (
    INVOICE_PERIOD_START,
    SETTINGS,
    Gateway,
    checkout,
    deliver,
    event_for,
)


def subscription(result, *, status="active"):
    return {"id": "sub_example", "livemode": False, "status": status,
            "metadata": {"dad_checkout_id": result["checkoutId"]},
            "cancel_at_period_end": False, "cancel_at": None,
            "items": {"has_more": False, "data": [{
                "quantity": 1, "current_period_start": INVOICE_PERIOD_START,
                "current_period_end": INVOICE_PERIOD_START + 30 * 86400,
                "price": {"currency": "usd", "unit_amount": 3000,
                          "recurring": {"interval": "month", "interval_count": 1, "usage_type": "licensed"}},
            }]}}


class BillingGateway(Gateway):
    def __init__(self):
        super().__init__()
        self.remote = None
        self.cancellations = []
        self.lose_response = False

    def retrieve_subscription(self, subscription_id):
        assert subscription_id == self.remote["id"]
        return deepcopy(self.remote)

    def schedule_cancellation(self, subscription_id, cancel_at, key):
        self.cancellations.append((subscription_id, key))
        assert cancel_at == self.remote["items"]["data"][0]["current_period_end"]
        self.remote["cancel_at"] = cancel_at
        if self.lose_response:
            self.lose_response = False
            raise PaymentUnavailable("Remote success, response lost")
        return deepcopy(self.remote)


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


@pytest.fixture
def setup(factory):
    provider = BillingGateway()
    result = checkout(factory, provider, frequency=GiftFrequency.MONTHLY)
    provider.remote = subscription(result)
    deliver(factory, event_for(result, "invoice.payment_succeeded"))
    with factory.begin() as session:
        token = claim_account(session, issue_login_link(session, "ben@example.org"))
        attempt = session.scalar(select(StripeCheckoutRecord))
        attempt_id = attempt.id
    end = datetime.fromtimestamp(INVOICE_PERIOD_START + 30 * 86400, UTC)
    return provider, token, attempt_id, end, result


def cancel(factory, setup, *, request_id=None, **kw):
    provider, token, attempt_id, end, _ = setup
    return cancel_recurring(factory, provider, token, request_id=request_id or uuid4(),
                            attempt_id=kw.get("attempt_id", attempt_id), period_end=kw.get("period_end", end))


def test_cancellation_preserves_gifts_badge_and_streak_until_skipped_renewal(factory, setup):
    provider, token, attempt_id, end, _ = setup
    before = recurring_details(factory, provider, token)
    assert before["subscription"]["stops_at"] is None
    key = uuid4()
    after = cancel(factory, setup, request_id=key)
    assert after["subscription"]["stops_at"] == end
    cancel(factory, setup, request_id=key)
    assert provider.cancellations == [("sub_example", "dad-cancel-" + str(key))]
    with factory.begin() as session:
        action = session.get(DonorCancellationRecord, key)
        assert action.completed_at is not None and action.attempt_id == attempt_id
        assert len(session.scalars(select(DonorGiftRecord)).all()) == 1
        before_end = refresh_projection(session, action.donor_id, now=end - timedelta(seconds=1))
        assert before_end.current_streak == 1
        assert before_end.founding_awarded_at is not None
        at_end = refresh_projection(session, action.donor_id, now=end)
        assert at_end.current_streak == 0 and at_end.best_streak == 1
        assert not at_end.at_risk and at_end.founding_awarded_at is not None


def test_uncertain_remote_success_is_reconciled_without_second_mutation(factory, setup):
    provider = setup[0]
    provider.lose_response = True
    key = uuid4()
    with pytest.raises(PaymentUnavailable):
        cancel(factory, setup, request_id=key)
    with factory() as session:
        assert session.get(DonorCancellationRecord, key).completed_at is None
    assert cancel(factory, setup, request_id=key)["subscription"]["stops_at"] == setup[3]
    assert len(provider.cancellations) == 1


def test_expired_command_and_changed_confirmation_cannot_cancel_new_period(factory, setup):
    with pytest.raises(SubscriptionConflict, match="Billing date"):
        cancel(factory, setup, period_end=setup[3] - timedelta(days=1))
    key = uuid4()
    with factory.begin() as session:
        donor_id = session.scalar(select(DonorGiftRecord.donor_id))
        session.add(DonorCancellationRecord(id=key, donor_id=donor_id, attempt_id=setup[2],
                    confirmed_period_end=setup[3], created_at=datetime.now(UTC) - timedelta(days=2)))
    with pytest.raises(SubscriptionConflict, match="reconciliation"):
        cancel(factory, setup, request_id=key)
    assert setup[0].cancellations == []


@pytest.mark.parametrize("change", [
    {"livemode": True}, {"metadata": {"dad_checkout_id": str(uuid4())}},
    {"items": {"has_more": True, "data": []}}, {"pause_collection": {"behavior": "void"}},
    {"cancel_at": 1234},
])
def test_unexpected_processor_state_fails_closed(factory, setup, change):
    setup[0].remote.update(change)
    with pytest.raises(ValueError):
        cancel(factory, setup)
    assert setup[0].cancellations == []


def test_origin_auth_ownership_and_confirmation_are_required(factory, setup):
    provider, token, attempt_id, end, _ = setup
    browser = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=provider),
                         base_url=SETTINGS.origin)
    body = {"subscription_id": str(attempt_id), "period_end": end.isoformat()}
    headers = {"Origin": SETTINGS.origin, "Idempotency-Key": str(uuid4())}
    assert browser.get("/donor/me/recurring").status_code == 401
    assert browser.post("/donor/me/recurring/cancel", json=body, headers=headers).status_code == 401
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/recurring/cancel", json=body).status_code in {403, 422}
    assert browser.post("/donor/me/recurring/cancel", json={**body, "refund": True}, headers=headers).status_code == 422
    with factory.begin() as session:
        guest_donor(session, "other@example.org")
        other = claim_account(session, issue_login_link(session, "other@example.org"))
    browser.cookies.set(SESSION_COOKIE, other)
    assert browser.get("/donor/me/recurring").json()["subscription"] is None
    assert browser.post("/donor/me/recurring/cancel", json=body, headers=headers).status_code == 409
    assert provider.cancellations == []
    browser.cookies.set(SESSION_COOKIE, token)
    response = browser.post("/donor/me/recurring/cancel", json=body, headers=headers)
    assert response.status_code == 200 and response.json()["subscription"]["stops_at"]
    assert "no-store" in response.headers["cache-control"]


def test_reordered_webhook_reads_latest_schedule_and_can_clear_cancellation(factory, setup):
    provider, token, _, _, result = setup
    cancel(factory, setup)
    provider.remote["cancel_at"] = None  # Authorized change at processor.
    event = event_for(result, "customer.subscription.updated", object_id="sub_example")
    event["data"]["object"].update(cancel_at_period_end=True)  # Stale event payload.
    accept_event(factory, event)
    assert process_next_event(factory, gateway=provider)
    with factory() as session:
        assert session.scalar(select(DonorSubscriptionRecord)).stops_at is None
        assert session.scalar(select(StripeEventRecord).where(
            StripeEventRecord.stripe_event_id == event["id"])).processed_at is not None
    assert recurring_details(factory, provider, token)["subscription"]["stops_at"] is None


def test_reusing_key_for_another_confirmation_is_rejected(factory, setup):
    key = uuid4()
    cancel(factory, setup, request_id=key)
    with pytest.raises(SubscriptionConflict, match="key"):
        cancel(factory, setup, request_id=key, period_end=setup[3] + timedelta(days=30))
    assert len(setup[0].cancellations) == 1


def test_stripe_adapter_sends_only_fixed_cancellation_and_no_proration(monkeypatch):
    from types import SimpleNamespace

    from impactgraph.stripe_payments import StripeGateway

    calls = []

    def update(subscription_id, params, *, options):
        calls.append((subscription_id, params, options))
        return SimpleNamespace(to_dict=lambda: {"id": subscription_id})

    provider = StripeGateway(SETTINGS)
    monkeypatch.setattr(provider.client.v1.subscriptions, "update", update)
    provider.schedule_cancellation("sub_owned", 1800000000, "dad-cancel-command")
    assert calls == [("sub_owned", {"cancel_at": 1800000000, "proration_behavior": "none"},
                      {"idempotency_key": "dad-cancel-command"})]


def test_scheduling_cancellation_does_not_allow_second_monthly_subscription(factory, setup):
    from impactgraph.stripe_payments import CheckoutConflict

    cancel(factory, setup)
    with pytest.raises(CheckoutConflict):
        checkout(factory, setup[0], frequency=GiftFrequency.MONTHLY)


def test_unknown_current_renewal_stays_unknown_without_verified_stop(factory, setup):
    provider, token, _, end, _ = setup
    recurring_details(factory, provider, token)
    with factory.begin() as session:
        donor_id = session.scalar(select(DonorGiftRecord.donor_id))
        projection = refresh_projection(session, donor_id, now=end)
        assert projection.current_streak is None
        assert projection.best_streak == 1
