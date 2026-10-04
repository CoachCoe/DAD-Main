"""Stripe boundary tests use real signature verification and a deterministic network seam."""

import hashlib
import hmac
import json
import time
from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
import stripe
from fastapi.testclient import TestClient
from sqlalchemy import func, select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import create_app
from impactgraph.donors import (
    GiftFrequency,
    ReferralKind,
    activate_sharing,
    capture_referral,
    claim_account,
    guest_donor,
    issue_login_link,
)
from impactgraph.persistence import (
    DonorCheckoutRecord,
    DonorGiftRecord,
    DonorRecurringReservationRecord,
    StripeCheckoutRecord,
    StripeEventRecord,
)
from impactgraph.stripe_payments import (
    API_VERSION,
    CheckoutConflict,
    PaymentSettings,
    PaymentUnavailable,
    StripeGateway,
    accept_event,
    begin_checkout,
    process_next_event,
    verify_event,
)

INVOICE_PERIOD_START = int(time.time())
SETTINGS = PaymentSettings("sk_test_example", "whsec_example", "http://localhost:3000")


class Gateway:
    def __init__(self):
        self.calls = []
        self.responses = {}
        self.fail_after_creation = False

    def create_checkout(self, params, key):
        self.calls.append((params, key))
        result = self.responses.setdefault(key, {
            "id": "cs_test_" + key, "url": "https://checkout.stripe.com/c/" + key, "livemode": False,
        })
        if self.fail_after_creation:
            self.fail_after_creation = False
            raise PaymentUnavailable("Connection lost after remote creation")
        return result


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


@pytest.fixture
def gateway():
    return Gateway()


def checkout(factory, gateway, *, frequency=GiftFrequency.ONE_TIME, email="ben@example.org", **kw):
    return begin_checkout(factory, gateway, SETTINGS, request_id=kw.pop("request_id", uuid4()),
                          email=email, amount_minor=3000, frequency=frequency, **kw)


def event_for(result, kind="payment_intent.succeeded", *, object_id=None, event_id=None):
    obj = {"id": object_id or "pi_" + uuid4().hex, "status": "succeeded", "currency": "usd",
           "amount_received": 3000, "metadata": {"dad_checkout_id": result["checkoutId"]}}
    if kind == "invoice.payment_succeeded":
        obj = {"id": object_id or "in_" + uuid4().hex, "status": "paid", "currency": "usd",
               "amount_paid": 3000, "amount_due": 3000, "total": 3000,
               "starting_balance": 0, "ending_balance": 0, "billing_reason": "subscription_create",
               "status_transitions": {"paid_at": int(time.time())},
               "parent": {"subscription_details": {"subscription": "sub_example",
                          "metadata": {"dad_checkout_id": result["checkoutId"]}}}}
    if kind == "invoice.payment_succeeded":
        start = INVOICE_PERIOD_START
        obj["lines"] = {"has_more": False, "data": [{
            "amount": 3000, "currency": "usd", "period": {"start": start, "end": start + 30 * 86400},
            "parent": {"type": "subscription_item_details", "subscription_item_details": {
                "subscription": "sub_example", "proration": False}},
        }]}
        obj["payments"] = {"has_more": False, "data": [{
            "status": "paid", "amount_paid": 3000, "currency": "usd", "invoice": obj["id"],
            "livemode": False, "payment": {"type": "payment_intent", "payment_intent": "pi_" + obj["id"]},
        }]}
    return {"id": event_id or "evt_" + uuid4().hex, "type": kind, "created": int(time.time()),
            "object": "event", "api_version": API_VERSION, "livemode": False,
            "data": {"object": obj}}


def signed(event, *, timestamp=None):
    body = json.dumps(event).encode()
    timestamp = int(time.time()) if timestamp is None else timestamp
    digest = hmac.new(SETTINGS.webhook_secret.encode(), str(timestamp).encode() + b"." + body,
                      hashlib.sha256).hexdigest()
    return body, f"t={timestamp},v1={digest}"


def deliver(factory, event, *, gateway=None):
    body, signature = signed(event)
    accept_event(factory, verify_event(body, signature, SETTINGS))
    assert process_next_event(factory, gateway=gateway)


def test_checkout_uses_hosted_stripe_and_monthly_metadata(factory, gateway):
    result = checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    params, key = gateway.calls[0]
    assert params["mode"] == "subscription"
    assert params["line_items"][0]["price_data"]["recurring"] == {"interval": "month"}
    assert params["line_items"][0]["price_data"]["unit_amount"] == 3000
    assert params["subscription_data"]["metadata"]["dad_checkout_id"] == result["checkoutId"]
    assert "customer" not in params  # An unverified email must not expose saved payment methods.
    assert params["customer_email"] == "ben@example.org"
    assert key.startswith("dad-checkout-")
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 0


def test_duplicate_checkout_and_changed_request(factory, gateway):
    request_id = uuid4()
    result = checkout(factory, gateway, request_id=request_id)
    assert checkout(factory, gateway, request_id=request_id) == result
    assert len(gateway.calls) == 1
    with pytest.raises(CheckoutConflict):
        checkout(factory, gateway, request_id=request_id, email="other@example.org")


def test_network_failure_retries_identical_stripe_key(factory, gateway):
    request_id = uuid4()
    gateway.fail_after_creation = True
    with pytest.raises(PaymentUnavailable):
        checkout(factory, gateway, request_id=request_id)
    checkout(factory, gateway, request_id=request_id)
    assert gateway.calls[0] == gateway.calls[1]
    assert len(gateway.responses) == 1


def test_retry_preserves_original_return_urls_after_configuration_change(factory, gateway):
    request_id = uuid4()
    gateway.fail_after_creation = True
    with pytest.raises(PaymentUnavailable):
        checkout(factory, gateway, request_id=request_id)
    changed = PaymentSettings("sk_test_example", "whsec_example", "https://new.example.org")
    begin_checkout(factory, gateway, changed, request_id=request_id, email="ben@example.org",
                   amount_minor=3000, frequency=GiftFrequency.ONE_TIME)
    assert gateway.calls[0] == gateway.calls[1]


def test_uncertain_attempt_cannot_be_recreated_after_stripe_idempotency_window(factory, gateway):
    request_id = uuid4()
    gateway.fail_after_creation = True
    with pytest.raises(PaymentUnavailable):
        checkout(factory, gateway, request_id=request_id)
    with factory.begin() as session:
        session.get(StripeCheckoutRecord, request_id).created_at = datetime.now(UTC) - timedelta(days=2)
    with pytest.raises(CheckoutConflict, match="reconciliation"):
        checkout(factory, gateway, request_id=request_id)
    assert len(gateway.calls) == 1


def test_monthly_reservation_prevents_second_subscription_but_allows_one_time(factory, gateway):
    checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    with pytest.raises(CheckoutConflict, match="Monthly"):
        checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    checkout(factory, gateway)
    assert len(gateway.calls) == 2


def test_signature_tampering_expiry_and_wrong_mode_are_rejected():
    event = event_for({"checkoutId": str(uuid4())})
    body, signature = signed(event)
    assert verify_event(body, signature, SETTINGS)["id"] == event["id"]
    with pytest.raises(stripe.SignatureVerificationError):
        verify_event(body + b" ", signature, SETTINGS)
    expired, old_signature = signed(event, timestamp=int(time.time()) - 301)
    with pytest.raises(stripe.SignatureVerificationError):
        verify_event(expired, old_signature, SETTINGS)
    event["livemode"] = True
    live, live_signature = signed(event)
    with pytest.raises(ValueError, match="test events"):
        verify_event(live, live_signature, SETTINGS)
    with pytest.raises(ValueError, match="test keys"):
        PaymentSettings("sk_live_never_use", "whsec_example")


def test_webhook_version_and_missing_configuration_fail_closed():
    event = event_for({"checkoutId": str(uuid4())})
    event["api_version"] = "2020-08-27"
    body, signature = signed(event)
    with pytest.raises(ValueError, match="version"):
        verify_event(body, signature, SETTINGS)
    with pytest.raises(PaymentUnavailable):
        verify_event(body, signature, PaymentSettings())


def test_inbox_excludes_card_details_and_records_gift_once(factory, gateway):
    result = checkout(factory, gateway)
    event = event_for(result)
    event["data"]["object"]["payment_method_details"] = {"card": {"last4": "4242"}}
    body, signature = signed(event)
    verified = verify_event(body, signature, SETTINGS)
    accept_event(factory, verified)
    accept_event(factory, verified)
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(StripeEventRecord)) == 1
        assert "4242" not in json.dumps(session.scalar(select(StripeEventRecord)).data)
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 0
    assert process_next_event(factory)
    assert not process_next_event(factory)
    # A different event ID for the same payment must also be harmless.
    verified["id"] = "evt_second_delivery"
    accept_event(factory, verified)
    assert process_next_event(factory)
    with factory() as session:
        gifts = list(session.scalars(select(DonorGiftRecord)))
        assert len(gifts) == 1 and gifts[0].amount_minor == 3000


def test_paid_invoice_before_checkout_completion_and_renewal(factory, gateway):
    result = checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    first = event_for(result, "invoice.payment_succeeded")
    deliver(factory, first)
    complete = event_for(result, "checkout.session.completed")
    complete["data"]["object"].update(id="cs_test_" + gateway.calls[0][1],
                                       status="complete", subscription="sub_example")
    deliver(factory, complete)
    renewal = event_for(result, "invoice.payment_succeeded")
    renewal["data"]["object"]["billing_reason"] = "subscription_cycle"
    deliver(factory, renewal)
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 2
        assert session.scalar(select(StripeCheckoutRecord)).status == "COMPLETE"
        assert session.scalar(select(StripeCheckoutRecord)).stripe_subscription_id == "sub_example"


def test_guest_payment_keeps_checkout_attribution(factory, gateway):
    with factory.begin() as session:
        alice = guest_donor(session, "alice@example.org")
        login = claim_account(session, issue_login_link(session, alice.email))
        code = activate_sharing(session, login, "Alice")
        visitor = capture_referral(session, code, ReferralKind.IRL)
        referrer_id = alice.id
    result = checkout(factory, gateway, visitor_token=visitor)
    deliver(factory, event_for(result))
    with factory() as session:
        gift = session.scalar(select(DonorGiftRecord))
        draft = session.get(DonorCheckoutRecord, gift.checkout_id)
        assert draft.referrer_id == referrer_id and draft.referral_kind == "IRL"
    assert gateway.calls[0][0]["payment_intent_data"]["metadata"]["dad_referral_kind"] == "IRL"


@pytest.mark.parametrize("changes", [{"amount_received": 3100}, {"currency": "eur"},
                                    {"status": "processing"}])
def test_invalid_payment_is_retained_for_reconciliation_not_credited(factory, gateway, changes):
    result = checkout(factory, gateway)
    event = event_for(result)
    event["data"]["object"].update(changes)
    deliver(factory, event)
    with factory() as session:
        record = session.scalar(select(StripeEventRecord))
        assert record.processed_at is None and record.last_error == "ValueError"
        assert record.attempts == 1
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 0


def test_expired_checkout_releases_monthly_reservation(factory, gateway):
    result = checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    event = event_for(result, "checkout.session.expired")
    event["data"]["object"].update(id="cs_test_" + gateway.calls[0][1], status="expired")
    deliver(factory, event)
    checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    assert len(gateway.calls) == 2


def test_cancellation_before_completion_is_not_undone(factory, gateway):
    result = checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    event = event_for(result, "customer.subscription.deleted")
    event["data"]["object"].update(id="sub_example", status="canceled")
    from tests.test_donor_subscriptions import BillingGateway, subscription
    billing = BillingGateway()
    billing.remote = subscription(result, status="canceled")
    deliver(factory, event, gateway=billing)
    complete = event_for(result, "checkout.session.completed")
    complete["data"]["object"].update(id="cs_test_" + gateway.calls[0][1], status="complete",
                                       subscription="sub_example")
    deliver(factory, complete)
    with factory() as session:
        assert session.scalar(select(StripeCheckoutRecord)).status == "CANCELLED"
        assert session.scalar(select(func.count()).select_from(DonorRecurringReservationRecord)) == 0
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 0


def test_api_origin_validation_strict_input_and_signed_inbox(factory, gateway):
    client = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway))
    body = {"email": "ben@example.org", "amount_minor": 3000, "frequency": "ONE_TIME"}
    headers = {"Idempotency-Key": str(uuid4())}
    assert client.post("/donor/checkouts", json=body, headers=headers).status_code == 403
    headers["Origin"] = SETTINGS.origin
    assert client.post("/donor/checkouts", json={**body, "card": "never accept"},
                       headers=headers).status_code == 422
    response = client.post("/donor/checkouts", json=body, headers=headers)
    assert response.status_code == 201
    payload, signature = signed(event_for(response.json()))
    assert client.post("/donor/stripe/webhook", content=payload).status_code == 400
    assert client.post("/donor/stripe/webhook", content=payload,
                       headers={"Stripe-Signature": signature}).status_code == 200
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 0
    assert process_next_event(factory)
    assert client.get("/health/ready").json()["status"] == "ready"


def test_completion_alone_is_not_payment(factory, gateway):
    result = checkout(factory, gateway)
    event = event_for(result, "checkout.session.completed")
    event["data"]["object"].update(id="cs_test_" + gateway.calls[0][1], status="complete")
    deliver(factory, event)
    with factory() as session:
        assert session.scalar(select(StripeEventRecord)).processed_at is not None
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 0


def test_failed_inbox_commit_is_not_acknowledged(factory, gateway, monkeypatch):
    client = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway),
                        raise_server_exceptions=False)
    event = event_for({"checkoutId": str(uuid4())})
    body, signature = signed(event)

    def fail(*args):
        raise RuntimeError("database unavailable")

    monkeypatch.setattr("impactgraph.donor_api.accept_event", fail)
    response = client.post("/donor/stripe/webhook", content=body,
                           headers={"Stripe-Signature": signature})
    assert response.status_code == 500


def test_failed_event_parks_after_attempt_budget_without_blocking_other_gifts(factory, gateway):
    result = checkout(factory, gateway)
    bad = event_for(result)
    bad["data"]["object"]["currency"] = "eur"
    accept_event(factory, bad)
    for _ in range(10):
        assert process_next_event(factory)
    assert not process_next_event(factory)
    deliver(factory, event_for(result))
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 1
        parked = session.scalar(select(StripeEventRecord).where(StripeEventRecord.stripe_event_id == bad["id"]))
        assert parked.attempts == 10 and parked.processed_at is None


def test_sdk_adapter_uses_pinned_version_without_a_global_api_key(monkeypatch):
    calls = []

    class Remote:
        def to_dict(self):
            return {"id": "cs_test_example"}

    class Client:
        def __init__(self, key, **kwargs):
            calls.append((key, kwargs))
            self.v1 = self.checkout = self.sessions = self

        def create(self, params, options):
            calls.append((params, options))
            return Remote()

    monkeypatch.setattr(stripe, "StripeClient", Client)
    assert StripeGateway(SETTINGS).create_checkout({"mode": "payment"}, "stable")["id"]
    assert calls[0][1]["stripe_version"] == API_VERSION
    assert calls[1][1] == {"idempotency_key": "stable"}


def test_first_gift_queues_one_claim_email_not_renewals_or_replays(factory, gateway):
    from impactgraph.persistence import DonorLoginEmailRecord

    result = checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    first = event_for(result, "invoice.payment_succeeded", object_id="in_first")
    deliver(factory, first)
    deliver(factory, event_for(result, "invoice.payment_succeeded", object_id="in_first"))
    renewal = event_for(result, "invoice.payment_succeeded", object_id="in_renewal")
    renewal["data"]["object"]["billing_reason"] = "subscription_cycle"
    deliver(factory, renewal)
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 2
        assert session.scalar(select(func.count()).select_from(DonorLoginEmailRecord)) == 1
