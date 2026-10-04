from copy import deepcopy
from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donor_payment_methods import begin_payment_update, payment_method_status
from impactgraph.donor_subscriptions import SubscriptionConflict
from impactgraph.donors import GiftFrequency, claim_account, guest_donor, issue_login_link
from impactgraph.persistence import DonorGiftRecord, DonorPaymentUpdateRecord, StripeEventRecord
from impactgraph.stripe_payments import PaymentUnavailable, accept_event, process_next_event
from tests.test_donor_subscriptions import BillingGateway, subscription
from tests.test_stripe_payments import SETTINGS, checkout, deliver, event_for


class CardGateway(BillingGateway):
    def __init__(self):
        super().__init__()
        self.sessions = {}
        self.setups = {}
        self.updates = []
        self.lose_apply_response = False

    def create_checkout(self, params, key):
        result = super().create_checkout(params, key)
        if params.get("mode") == "setup":
            self.sessions.setdefault(result["id"], {**result, "status": "open", "mode": "setup",
                                                   "customer": params["customer"], "metadata": params["metadata"]})
        return result

    def retrieve_checkout(self, identifier):
        return deepcopy(self.sessions[identifier])

    def retrieve_setup_intent(self, identifier):
        return deepcopy(self.setups[identifier])

    def update_subscription_payment_method(self, subscription_id, payment_method, key):
        assert subscription_id == self.remote["id"]
        self.updates.append((subscription_id, payment_method, key))
        self.remote["default_payment_method"] = payment_method
        if self.lose_apply_response:
            self.lose_apply_response = False
            raise PaymentUnavailable("Applied, response lost")

    def complete(self, identifier, payment_method="pm_new"):
        remote = self.sessions[identifier]
        remote.update(status="complete", setup_intent="seti_" + identifier)
        self.setups[remote["setup_intent"]] = {"id": remote["setup_intent"], "livemode": False,
            "customer": remote["customer"], "metadata": remote["metadata"], "status": "succeeded",
            "usage": "off_session", "payment_method_types": ["card"], "payment_method": payment_method}
        return {"id": "evt_" + uuid4().hex, "type": "checkout.session.completed",
                "created": int(datetime.now(UTC).timestamp()), "data": {"object": deepcopy(remote)}}


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


@pytest.fixture
def setup(factory):
    gateway = CardGateway()
    result = checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    gateway.remote = {**subscription(result), "customer": "cus_owner", "default_payment_method": "pm_old"}
    deliver(factory, event_for(result, "invoice.payment_succeeded"))
    with factory.begin() as session:
        token = claim_account(session, issue_login_link(session, "ben@example.org"))
    return gateway, token


def start(factory, setup, request_id=None):
    gateway, token = setup
    result = begin_payment_update(factory, gateway, SETTINGS, token, request_id=request_id or uuid4())
    identifier = next(key for key, value in gateway.sessions.items() if value["url"] == result["url"])
    return result, identifier


def process(factory, gateway, event):
    accept_event(factory, event)
    assert process_next_event(factory, gateway=gateway)


def test_hosted_setup_uses_verified_customer_and_confirms_only_after_apply(factory, setup):
    gateway, token = setup
    result, identifier = start(factory, setup)
    params, key = gateway.calls[-1]
    assert params["mode"] == "setup" and params["customer"] == "cus_owner"
    assert "customer_email" not in params and "line_items" not in params
    assert params["payment_method_types"] == ["card"]
    assert params["success_url"] == SETTINGS.origin + "/account/payment-method"
    assert params["setup_intent_data"]["metadata"] == params["metadata"]
    assert key.startswith("dad-payment-setup-")
    assert result["url"].startswith("https://checkout.stripe.com/")
    assert payment_method_status(factory, token)["status"] == "OPEN"
    event = gateway.complete(identifier)
    process(factory, gateway, event)
    assert payment_method_status(factory, token)["status"] == "APPLIED"
    assert gateway.remote["default_payment_method"] == "pm_new"
    assert len(gateway.updates) == 1
    with factory() as session:
        assert len(session.scalars(select(DonorGiftRecord)).all()) == 1
        saved = session.scalar(select(StripeEventRecord).where(StripeEventRecord.stripe_event_id == event["id"]))
        assert set(saved.data) == {"id", "payment_update_id"}
        assert saved.processed_at is not None


def test_two_devices_share_pending_setup_and_old_replay_cannot_overwrite_new_card(factory, setup):
    gateway, token = setup
    first, identifier = start(factory, setup)
    second, same_identifier = start(factory, setup)
    assert first == second and identifier == same_identifier
    old = gateway.complete(identifier, "pm_first")
    process(factory, gateway, old)
    _, new_identifier = start(factory, setup)
    assert new_identifier != identifier
    process(factory, gateway, gateway.complete(new_identifier, "pm_second"))
    old["id"] = "evt_" + uuid4().hex
    process(factory, gateway, old)
    assert gateway.remote["default_payment_method"] == "pm_second"
    assert len(gateway.updates) == 2
    assert payment_method_status(factory, token)["status"] == "APPLIED"


def test_lost_apply_response_reconciles_without_duplicate_mutation(factory, setup):
    gateway, token = setup
    _, identifier = start(factory, setup)
    gateway.lose_apply_response = True
    process(factory, gateway, gateway.complete(identifier))
    assert payment_method_status(factory, token)["status"] == "OPEN"
    assert process_next_event(factory, gateway=gateway)
    assert payment_method_status(factory, token)["status"] == "APPLIED"
    assert len(gateway.updates) == 1


@pytest.mark.parametrize("change", [
    {"livemode": True}, {"customer": "cus_stranger"}, {"status": "requires_action"},
    {"metadata": {}}, {"usage": "on_session"}, {"payment_method_types": ["us_bank_account"]},
])
def test_wrong_or_unfinished_setup_never_updates_subscription(factory, setup, change):
    gateway, token = setup
    _, identifier = start(factory, setup)
    event = gateway.complete(identifier)
    gateway.setups[gateway.sessions[identifier]["setup_intent"]].update(change)
    process(factory, gateway, event)
    assert gateway.updates == []
    assert payment_method_status(factory, token)["status"] == "OPEN"
    with factory() as session:
        row = session.scalar(select(StripeEventRecord).where(StripeEventRecord.stripe_event_id == event["id"]))
        assert row.last_error == "ValueError" and row.processed_at is None


def test_expired_setup_can_restart_and_cancelled_subscription_is_not_reactivated(factory, setup):
    gateway, token = setup
    _, identifier = start(factory, setup)
    gateway.sessions[identifier]["status"] = "expired"
    _, replacement = start(factory, setup)
    assert replacement != identifier
    gateway.remote["status"] = "canceled"
    process(factory, gateway, gateway.complete(replacement))
    assert payment_method_status(factory, token)["status"] == "CANCELLED"
    assert gateway.updates == []


def test_uncertain_creation_retries_frozen_parameters_and_key(factory, setup):
    gateway, _ = setup
    key = uuid4()
    gateway.fail_after_creation = True
    with pytest.raises(PaymentUnavailable):
        start(factory, setup, key)
    first = gateway.calls[-1]
    start(factory, setup, key)
    assert first == gateway.calls[-1]


def test_old_uncertain_update_is_parked_instead_of_reapplying(factory, setup):
    gateway, token = setup
    _, identifier = start(factory, setup)
    with factory.begin() as session:
        session.scalar(select(DonorPaymentUpdateRecord)).created_at = datetime.now(UTC) - timedelta(days=2)
    process(factory, gateway, gateway.complete(identifier))
    assert gateway.updates == []
    assert payment_method_status(factory, token)["status"] == "OPEN"


def test_auth_origin_and_customer_ownership_cannot_be_supplied_by_browser(factory, setup):
    gateway, token = setup
    browser = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway), base_url=SETTINGS.origin)
    headers = {"Origin": SETTINGS.origin, "Idempotency-Key": str(uuid4())}
    assert browser.get("/donor/me/payment-method").status_code == 401
    assert browser.post("/donor/me/payment-method/setup", headers=headers).status_code == 401
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/payment-method/setup", headers={"Idempotency-Key": str(uuid4())}).status_code == 403
    assert browser.post("/donor/me/payment-method/setup", headers=headers).status_code == 200
    with factory.begin() as session:
        guest_donor(session, "stranger@example.org")
        stranger = claim_account(session, issue_login_link(session, "stranger@example.org"))
    browser.cookies.set(SESSION_COOKIE, stranger)
    assert browser.get("/donor/me/payment-method").json()["status"] == "NONE"
    assert browser.post("/donor/me/payment-method/setup", headers=headers).status_code == 409
    with pytest.raises(SubscriptionConflict):
        begin_payment_update(factory, gateway, SETTINGS, stranger, request_id=uuid4())


def test_completed_setup_can_be_reconciled_on_retry_without_creating_another(factory, setup):
    gateway, token = setup
    _, identifier = start(factory, setup)
    gateway.complete(identifier)
    before = len(gateway.calls)
    result = begin_payment_update(factory, gateway, SETTINGS, token, request_id=uuid4())
    assert result["url"] is None and result["status"] == "APPLIED"
    assert len(gateway.calls) == before
    assert payment_method_status(factory, token)["status"] == "APPLIED"


def test_completed_setup_webhook_can_arrive_before_creation_response_is_saved(factory, setup):
    gateway, token = setup
    _, identifier = start(factory, setup)
    with factory.begin() as session:
        row = session.scalar(select(DonorPaymentUpdateRecord))
        row.stripe_session_id, row.hosted_url, row.status = None, None, "PENDING"
    process(factory, gateway, gateway.complete(identifier))
    assert payment_method_status(factory, token)["status"] == "APPLIED"
    assert len(gateway.updates) == 1


def test_stripe_adapter_sets_only_subscription_default_and_no_proration(monkeypatch):
    from types import SimpleNamespace

    from impactgraph.stripe_payments import StripeGateway

    calls = []

    def update(subscription_id, params, *, options):
        calls.append((subscription_id, params, options))
        return SimpleNamespace(to_dict=lambda: {"id": subscription_id})

    gateway = StripeGateway(SETTINGS)
    monkeypatch.setattr(gateway.client.v1.subscriptions, "update", update)
    gateway.update_subscription_payment_method("sub_owned", "pm_new", "dad-payment-apply-key")
    assert calls == [("sub_owned", {"default_payment_method": "pm_new", "proration_behavior": "none"},
                      {"idempotency_key": "dad-payment-apply-key"})]


def test_event_is_retained_when_stripe_retrieval_has_not_caught_up(factory, setup):
    gateway, token = setup
    _, identifier = start(factory, setup)
    event = gateway.complete(identifier)
    gateway.sessions[identifier]["status"] = "open"
    process(factory, gateway, event)
    with factory() as session:
        saved = session.scalar(select(StripeEventRecord).where(StripeEventRecord.stripe_event_id == event["id"]))
        assert saved.processed_at is None
    assert gateway.updates == []
    gateway.sessions[identifier]["status"] = "complete"
    assert process_next_event(factory, gateway=gateway)
    assert payment_method_status(factory, token)["status"] == "APPLIED"
