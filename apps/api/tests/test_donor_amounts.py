from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_amounts import amount_options, change_amount
from impactgraph.donor_projections import refresh_projection
from impactgraph.donor_subscriptions import SubscriptionConflict, recurring_details
from impactgraph.persistence import DonorAmountChangeRecord, DonorCheckoutRecord, DonorGiftRecord
from impactgraph.stripe_payments import PaymentUnavailable, accept_event, process_next_event
from tests.test_donor_pauses import make_pause_setup, renewal_after, uuid_from
from tests.test_stripe_payments import event_for


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


@pytest.fixture
def setup(factory):
    result = make_pause_setup(factory)
    gateway = result[0]
    item = gateway.remote["items"]["data"][0]
    item["id"], item["price"]["product"] = "si_owned", "prod_owned"
    gateway.amount_changes = []
    gateway.lose_amount_response = False

    def update(subscription, item_id, product, amount, key):
        assert (subscription, item_id, product) == ("sub_example", "si_owned", "prod_owned")
        gateway.amount_changes.append((amount, key))
        item["price"]["unit_amount"] = amount
        if gateway.lose_amount_response:
            gateway.lose_amount_response = False
            raise PaymentUnavailable("Applied, response lost")
    gateway.change_monthly_amount = update
    return result


def change(factory, setup, amount=9000, *, key=None, chosen=None):
    gateway, token, _, _ = setup
    chosen = chosen or amount_options(factory, gateway, token)
    return change_amount(factory, gateway, token, request_id=key or uuid4(),
                         attempt_id=uuid_from(chosen["subscription_id"]), amount_minor=amount,
                         previous_minor=chosen["amount_minor"], effective_at=chosen["effective_at"])


def invoice(factory, setup, start, amount, *, object_id=None):
    gateway, _, result, anchor = setup
    event = event_for(result, "invoice.payment_succeeded", object_id=object_id)
    obj = event["data"]["object"]
    obj.update(amount_paid=amount, amount_due=amount, total=amount, billing_reason="subscription_cycle")
    obj["status_transitions"]["paid_at"] = int(start.timestamp())
    obj["lines"]["data"][0].update(amount=amount, period={"start": int(start.timestamp()), "end": int(renewal_after(start, 1, anchor).timestamp())})
    obj["payments"]["data"][0]["amount_paid"] = amount
    accept_event(factory, event)
    assert process_next_event(factory, gateway=gateway)
    return event


def test_change_preserves_original_gift_and_values_next_period_correctly(factory, setup):
    gateway, token, _, anchor = setup
    result = change(factory, setup)
    assert result["confirmed"]
    assert recurring_details(factory, gateway, token)["subscription"]["amount_minor"] == 9000
    with factory() as session:
        assert session.scalar(select(DonorCheckoutRecord)).amount_minor == 3000
        original = session.scalar(select(DonorGiftRecord))
        assert original.amount_minor == 3000
        original_invoice = original.stripe_object_id
    event = invoice(factory, setup, result["effective_at"], 9000)
    with factory.begin() as session:
        gifts = session.scalars(select(DonorGiftRecord).order_by(DonorGiftRecord.paid_at)).all()
        assert [gift.amount_minor for gift in gifts] == [3000, 9000]
        assert gifts[1].stripe_object_id == event["data"]["object"]["id"]
        metrics = refresh_projection(session, gifts[0].donor_id, now=result["effective_at"])
        assert metrics.current_streak == 2 and metrics.net_minor == 12000
    # A delayed duplicate original invoice still validates at the original price.
    invoice(factory, setup, anchor, 3000, object_id=original_invoice)
    with factory() as session:
        assert len(session.scalars(select(DonorGiftRecord)).all()) == 2


def test_lost_response_reconciles_without_duplicate_change(factory, setup):
    gateway, token, _, _ = setup
    chosen, key = amount_options(factory, gateway, token), uuid4()
    gateway.lose_amount_response = True
    with pytest.raises(PaymentUnavailable):
        change(factory, setup, key=key, chosen=chosen)
    assert change(factory, setup, key=key, chosen=chosen)["confirmed"]
    assert change(factory, setup, key=key, chosen=chosen)["confirmed"]
    assert len(gateway.amount_changes) == 1


def test_multiple_changes_before_same_renewal_use_latest_confirmed_amount(factory, setup):
    first = change(factory, setup)
    second = change(factory, setup, 5000)
    assert first["effective_at"] == second["effective_at"]
    invoice(factory, setup, second["effective_at"], 5000)
    with factory() as session:
        assert sorted(g.amount_minor for g in session.scalars(select(DonorGiftRecord))) == [3000, 5000]


def test_changed_confirmation_or_old_request_cannot_change_billing(factory, setup):
    gateway, token, _, _ = setup
    chosen = amount_options(factory, gateway, token)
    with pytest.raises(SubscriptionConflict, match="changed"):
        change(factory, setup, chosen={**chosen, "effective_at": chosen["effective_at"] + timedelta(days=1)})
    gateway.lose_amount_response = True
    key = uuid4()
    with pytest.raises(PaymentUnavailable):
        change(factory, setup, key=key, chosen=chosen)
    # Model a command whose remote operation has not applied and whose key aged out.
    gateway.remote["items"]["data"][0]["price"]["unit_amount"] = 3000
    with factory.begin() as session:
        session.get(DonorAmountChangeRecord, key).created_at = datetime.now(UTC) - timedelta(days=2)
    with pytest.raises(SubscriptionConflict, match="reconciliation"):
        change(factory, setup, key=key, chosen=chosen)
    assert len(gateway.amount_changes) == 1


@pytest.mark.parametrize("amount", [0, -1, True, 1.5, 100000000])
def test_invalid_minor_units_are_rejected(factory, setup, amount):
    with pytest.raises(ValueError):
        change(factory, setup, amount)
    assert setup[0].amount_changes == []


def test_auth_origin_and_subscription_ownership_are_required(factory, setup):
    from fastapi.testclient import TestClient

    from impactgraph.donor_api import SESSION_COOKIE, create_app
    from impactgraph.donors import claim_account, guest_donor, issue_login_link
    from tests.test_stripe_payments import SETTINGS

    gateway, token, _, _ = setup
    chosen = amount_options(factory, gateway, token)
    body = {"subscription_id": chosen["subscription_id"], "amount_minor": 9000,
            "previous_minor": 3000, "effective_at": chosen["effective_at"].isoformat()}
    headers = {"Origin": SETTINGS.origin, "Idempotency-Key": str(uuid4())}
    browser = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway), base_url=SETTINGS.origin)
    assert browser.get("/donor/me/recurring/amount").status_code == 401
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/recurring/amount", json=body, headers={"Idempotency-Key": str(uuid4())}).status_code == 403
    assert browser.post("/donor/me/recurring/amount", json={**body, "prorate": True}, headers=headers).status_code == 422
    with factory.begin() as session:
        guest_donor(session, "stranger@example.org")
        other = claim_account(session, issue_login_link(session, "stranger@example.org"))
    browser.cookies.set(SESSION_COOKIE, other)
    assert browser.post("/donor/me/recurring/amount", json=body, headers=headers).status_code == 409
    assert gateway.amount_changes == []
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/recurring/amount", json=body, headers=headers).json()["confirmed"]


def test_unapproved_remote_price_is_not_accepted(factory, setup):
    gateway, token, _, _ = setup
    gateway.remote["items"]["data"][0]["price"]["unit_amount"] = 5000
    with pytest.raises(ValueError, match="Unexpected recurring amount"):
        amount_options(factory, gateway, token)
    assert gateway.amount_changes == []


def test_adapter_preserves_anchor_and_disables_proration():
    from types import SimpleNamespace
    from unittest.mock import Mock

    from impactgraph.stripe_payments import StripeGateway
    gateway = object.__new__(StripeGateway)
    update = Mock(return_value=SimpleNamespace(to_dict=dict))
    gateway.client = SimpleNamespace(v1=SimpleNamespace(subscriptions=SimpleNamespace(update=update)))
    gateway.change_monthly_amount("sub_owned", "si_owned", "prod_owned", 9000, "stable-key")
    update.assert_called_once_with("sub_owned", {
        "items": [{"id": "si_owned", "quantity": 1, "price_data": {"currency": "usd", "product": "prod_owned",
                   "recurring": {"interval": "month"}, "unit_amount": 9000}}], "proration_behavior": "none",
    }, options={"idempotency_key": "stable-key"})
