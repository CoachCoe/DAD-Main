from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donor_pauses import pause_options, pause_recurring, reconcile_pause, renewal_after
from impactgraph.donor_projections import refresh_projection
from impactgraph.donor_subscriptions import SubscriptionConflict, cancel_recurring
from impactgraph.donors import GiftFrequency, claim_account, guest_donor, issue_login_link
from impactgraph.persistence import DonorGiftRecord, DonorPauseRecord, StripeCheckoutRecord
from impactgraph.stripe_payments import PaymentUnavailable
from tests.test_donor_subscriptions import BillingGateway, subscription
from tests.test_stripe_payments import INVOICE_PERIOD_START, SETTINGS, checkout, deliver, event_for


class PauseGateway(BillingGateway):
    def __init__(self):
        super().__init__()
        self.pauses = []
        self.lose_pause_response = False

    def pause_collection(self, subscription_id, resumes_at, key):
        self.pauses.append((subscription_id, resumes_at, key))
        self.remote["pause_collection"] = {"behavior": "void", "resumes_at": resumes_at}
        if self.lose_pause_response:
            self.lose_pause_response = False
            raise PaymentUnavailable("Remote success, response lost")


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


@pytest.fixture
def setup(factory):
    gateway = PauseGateway()
    result = checkout(factory, gateway, frequency=GiftFrequency.MONTHLY)
    anchor = datetime.fromtimestamp(INVOICE_PERIOD_START, UTC)
    first = renewal_after(anchor, 1, anchor)
    gateway.remote = subscription(result)
    gateway.remote["billing_cycle_anchor"] = INVOICE_PERIOD_START
    gateway.remote["items"]["data"][0]["current_period_end"] = int(first.timestamp())
    paid = event_for(result, "invoice.payment_succeeded")
    paid["data"]["object"]["lines"]["data"][0]["period"]["end"] = int(first.timestamp())
    deliver(factory, paid)
    with factory.begin() as session:
        token = claim_account(session, issue_login_link(session, "ben@example.org"))
    return gateway, token, result, anchor


def pause(factory, setup, months=1, *, request_id=None, dates=None):
    gateway, token, _, _ = setup
    options = pause_options(factory, gateway, token)
    chosen = dates or options["options"][months - 1]
    return pause_recurring(factory, gateway, token, request_id=request_id or uuid4(),
                           attempt_id=uuid_from(options["subscription_id"]), **chosen)


def uuid_from(value):
    from uuid import UUID
    return UUID(value)


@pytest.mark.parametrize("year,expected", [(2027, 28), (2028, 29)])
def test_month_end_anchor_survives_february(year, expected):
    anchor = datetime(year, 1, 31, 12, 34, 56, tzinfo=UTC)
    february = renewal_after(anchor, 1, anchor)
    assert february == datetime(year, 2, expected, 12, 34, 56, tzinfo=UTC)
    assert renewal_after(february, 1, anchor) == datetime(year, 3, 31, 12, 34, 56, tzinfo=UTC)
    assert renewal_after(february, 3, anchor) == datetime(year, 5, 31, 12, 34, 56, tzinfo=UTC)


@pytest.mark.parametrize("months", [0, 4, True])
def test_only_one_to_three_renewals_are_allowed(months):
    with pytest.raises(ValueError):
        renewal_after(datetime.now(UTC), months, datetime.now(UTC))


@pytest.mark.parametrize("months", [1, 2, 3])
def test_pause_dates_and_streak_boundaries(factory, setup, months):
    gateway, token, _, _ = setup
    result = pause(factory, setup, months)["pause"]
    start, end = result["starts_at"], result["resumes_at"]
    assert result["confirmed"]
    assert gateway.pauses[0][1] == int(end.timestamp())
    with factory.begin() as session:
        row = session.scalar(select(DonorPauseRecord))
        assert row.confirmed_at is not None
        before = refresh_projection(session, row.donor_id, now=start - timedelta(seconds=1))
        assert before.current_streak == 1
        during = refresh_projection(session, row.donor_id, now=start)
        assert during.current_streak == 0 and during.best_streak == 1 and not during.at_risk
        assert during.founding_awarded_at is not None
        assert refresh_projection(session, row.donor_id, now=end - timedelta(seconds=1)).current_streak == 0
        assert refresh_projection(session, row.donor_id, now=end).current_streak is None
        assert len(session.scalars(select(DonorGiftRecord)).all()) == 1
    assert pause_options(factory, gateway, token)["pause"]["confirmed"]
    assert pause_options(factory, gateway, token)["options"] == []


def test_lost_response_reconciles_with_same_confirmation(factory, setup):
    gateway, token, _, _ = setup
    options = pause_options(factory, gateway, token)
    chosen = options["options"][1]
    key = uuid4()
    gateway.lose_pause_response = True
    with pytest.raises(PaymentUnavailable):
        pause(factory, setup, request_id=key, dates=chosen)
    with factory() as session:
        assert session.get(DonorPauseRecord, key).confirmed_at is None
    result = pause(factory, setup, request_id=key, dates=chosen)
    assert result["pause"]["confirmed"]
    assert len(gateway.pauses) == 1


def test_stale_dates_and_overlapping_pause_are_rejected(factory, setup):
    gateway, token, _, _ = setup
    chosen = pause_options(factory, gateway, token)["options"][0]
    with pytest.raises(SubscriptionConflict, match="dates changed"):
        pause(factory, setup, dates={**chosen, "resumes_at": chosen["resumes_at"] + timedelta(days=1)})
    pause(factory, setup, dates=chosen)
    with pytest.raises(SubscriptionConflict, match="already exists"):
        pause(factory, setup, dates=chosen)
    assert len(gateway.pauses) == 1


def test_resume_requires_successful_renewal_to_restart_streak(factory, setup):
    gateway, _, result, anchor = setup
    ends = pause(factory, setup, months=2)["pause"]["resumes_at"]
    next_end = renewal_after(ends, 1, anchor)
    paid = event_for(result, "invoice.payment_succeeded")
    obj = paid["data"]["object"]
    obj["status_transitions"]["paid_at"] = int(ends.timestamp())
    obj["billing_reason"] = "subscription_cycle"
    obj["lines"]["data"][0]["period"] = {"start": int(ends.timestamp()), "end": int(next_end.timestamp())}
    deliver(factory, paid)
    with factory.begin() as session:
        row = session.scalar(select(DonorPauseRecord))
        projection = refresh_projection(session, row.donor_id, now=ends)
        assert projection.current_streak == 1 and projection.best_streak == 1
        remote = {**gateway.remote, "pause_collection": None}
        attempt = session.get(StripeCheckoutRecord, row.attempt_id)
        assert reconcile_pause(session, attempt, remote, now=ends).confirmed_at is not None
        with pytest.raises(ValueError, match="earlier"):
            reconcile_pause(session, attempt, remote, now=ends - timedelta(seconds=1))


def test_cancellation_during_pause_does_not_resume_giving_or_restore_streak(factory, setup):
    gateway, token, _, _ = setup
    paused = pause(factory, setup, months=3)["pause"]
    with factory() as session:
        row = session.scalar(select(DonorPauseRecord))
        attempt_id, donor_id = row.attempt_id, row.donor_id
    cancelled = cancel_recurring(factory, gateway, token, request_id=uuid4(), attempt_id=attempt_id,
                                 period_end=paused["starts_at"])
    assert cancelled["subscription"]["stops_at"] == paused["starts_at"]
    assert pause_options(factory, gateway, token)["state"] == "unavailable"
    with factory.begin() as session:
        projection = refresh_projection(session, donor_id, now=paused["resumes_at"] + timedelta(days=1))
        assert projection.current_streak == 0 and projection.founding_awarded_at is not None


def test_auth_origin_and_owned_subscription_are_required(factory, setup):
    gateway, token, _, _ = setup
    options = pause_options(factory, gateway, token)
    selected = options["options"][0]
    body = {"subscription_id": options["subscription_id"], "months": selected["months"],
            "starts_at": selected["starts_at"].isoformat(), "resumes_at": selected["resumes_at"].isoformat()}
    browser = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway), base_url=SETTINGS.origin)
    headers = {"Origin": SETTINGS.origin, "Idempotency-Key": str(uuid4())}
    assert browser.get("/donor/me/recurring/pause").status_code == 401
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/recurring/pause", json=body, headers={"Idempotency-Key": str(uuid4())}).status_code == 403
    assert browser.post("/donor/me/recurring/pause", json={**body, "months": True}, headers=headers).status_code == 422
    with factory.begin() as session:
        guest_donor(session, "stranger@example.org")
        other = claim_account(session, issue_login_link(session, "stranger@example.org"))
    browser.cookies.set(SESSION_COOKIE, other)
    assert browser.post("/donor/me/recurring/pause", json=body, headers=headers).status_code == 409
    assert gateway.pauses == []
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/recurring/pause", json=body, headers=headers).json()["pause"]["confirmed"]


def test_adapter_voids_skipped_invoices_and_schedules_resume(monkeypatch):
    from types import SimpleNamespace

    from impactgraph.stripe_payments import StripeGateway

    calls = []

    def update(subscription_id, params, *, options):
        calls.append((subscription_id, params, options))
        return SimpleNamespace(to_dict=lambda: {"id": subscription_id})

    provider = StripeGateway(SETTINGS)
    monkeypatch.setattr(provider.client.v1.subscriptions, "update", update)
    provider.pause_collection("sub_owned", 1800000000, "dad-pause-key")
    assert calls == [("sub_owned", {"pause_collection": {"behavior": "void", "resumes_at": 1800000000},
                                   "proration_behavior": "none"}, {"idempotency_key": "dad-pause-key"})]


def test_expired_unconfirmed_command_does_not_create_a_new_remote_pause(factory, setup):
    gateway, token, _, _ = setup
    options = pause_options(factory, gateway, token)
    chosen = options["options"][0]
    key = uuid4()
    with factory.begin() as session:
        donor_id = session.scalar(select(DonorGiftRecord.donor_id))
        session.add(DonorPauseRecord(id=key, donor_id=donor_id,
                    attempt_id=uuid_from(options["subscription_id"]), **chosen,
                    created_at=datetime.now(UTC) - timedelta(days=2)))
    with pytest.raises(SubscriptionConflict, match="reconciliation"):
        pause(factory, setup, request_id=key, dates=chosen)
    assert gateway.pauses == []


def test_completed_pause_key_cannot_authorize_different_dates(factory, setup):
    gateway, token, _, _ = setup
    chosen = pause_options(factory, gateway, token)["options"][0]
    key = uuid4()
    pause(factory, setup, request_id=key, dates=chosen)
    with pytest.raises(SubscriptionConflict, match="key already used"):
        pause(factory, setup, request_id=key, dates={**chosen, "resumes_at": chosen["resumes_at"] + timedelta(days=1)})
    assert len(gateway.pauses) == 1
