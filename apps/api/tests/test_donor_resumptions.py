from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph import donor_pauses, donor_projections, donor_resumptions
from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donor_pauses import pause_options, reconcile_pause, renewal_after
from impactgraph.donor_projections import refresh_projection
from impactgraph.donor_resumptions import resume_options, resume_recurring
from impactgraph.donor_subscriptions import SubscriptionConflict, cancel_recurring
from impactgraph.donors import claim_account, guest_donor, issue_login_link
from impactgraph.persistence import (
    DonorGiftRecord,
    DonorPauseRecord,
    DonorResumeRecord,
    StripeCheckoutRecord,
)
from impactgraph.stripe_payments import PaymentUnavailable
from tests.test_donor_pauses import make_pause_setup, pause, uuid_from
from tests.test_stripe_payments import SETTINGS, deliver, event_for


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


@pytest.fixture
def setup(factory):
    return make_pause_setup(factory)


def resume(factory, setup, *, key=None, dates=None):
    gateway, token, _, _ = setup
    options = dates or resume_options(factory, gateway, token)
    return resume_recurring(factory, gateway, token, request_id=key or uuid4(),
                            pause_id=uuid_from(options["pause_id"]), next_renewal=options["next_renewal"])


def freeze(monkeypatch, when):
    class Clock(datetime):
        @classmethod
        def now(cls, tz=None):
            return when.astimezone(tz) if tz else when.replace(tzinfo=None)
    monkeypatch.setattr(donor_pauses, "datetime", Clock)
    monkeypatch.setattr(donor_resumptions, "datetime", Clock)
    monkeypatch.setattr(donor_projections, "datetime", Clock)


def renewal(factory, setup, when):
    _, _, result, anchor = setup
    paid = event_for(result, "invoice.payment_succeeded")
    obj = paid["data"]["object"]
    obj["status_transitions"]["paid_at"] = int(when.timestamp())
    obj["billing_reason"] = "subscription_cycle"
    obj["lines"]["data"][0]["period"] = {"start": int(when.timestamp()),
                                            "end": int(renewal_after(when, 1, anchor).timestamp())}
    deliver(factory, paid)


def test_resume_before_first_skip_preserves_continuity_after_successful_charge(factory, setup):
    gateway, token, _, _ = setup
    original = pause(factory, setup, months=3)["pause"]
    key = uuid4()
    result = resume(factory, setup, key=key)
    assert result["confirmed"] and result["next_renewal"] == original["starts_at"]
    assert gateway.pauses[-1] == ("sub_example", int(original["starts_at"].timestamp()), "dad-resume-" + str(key))
    assert gateway.remote["pause_collection"]["behavior"] == "void"
    assert pause_options(factory, gateway, token)["pause"]["resumes_at"] == original["starts_at"]
    with factory.begin() as session:
        row = session.scalar(select(DonorResumeRecord))
        donor_id = row.donor_id
        assert len(session.scalars(select(DonorGiftRecord)).all()) == 1
        assert refresh_projection(session, donor_id, now=original["starts_at"] - timedelta(seconds=1)).current_streak == 1
        assert refresh_projection(session, donor_id, now=original["starts_at"]).current_streak is None
    renewal(factory, setup, original["starts_at"])
    with factory.begin() as session:
        projection = refresh_projection(session, donor_id, now=original["starts_at"])
        assert projection.current_streak == 2 and projection.founding_awarded_at is not None


def test_resume_after_a_skipped_month_restarts_at_one(factory, setup, monkeypatch):
    gateway, _token, _, anchor = setup
    original = pause(factory, setup, months=3)["pause"]
    first, next_date = original["starts_at"], renewal_after(original["starts_at"], 1, anchor)
    freeze(monkeypatch, first + timedelta(days=1))
    gateway.remote["items"]["data"][0].update(current_period_start=int(first.timestamp()),
                                               current_period_end=int(next_date.timestamp()))
    result = resume(factory, setup)
    assert result["next_renewal"] == next_date
    with factory.begin() as session:
        donor_id = session.scalar(select(DonorResumeRecord.donor_id))
        assert refresh_projection(session, donor_id, now=next_date - timedelta(seconds=1)).current_streak == 0
    renewal(factory, setup, next_date)
    with factory.begin() as session:
        assert refresh_projection(session, donor_id, now=next_date).current_streak == 1
        row = session.scalar(select(DonorPauseRecord))
        attempt = session.get(StripeCheckoutRecord, row.attempt_id)
        reconcile_pause(session, attempt, {**gateway.remote, "pause_collection": None}, now=next_date)


def test_lost_response_and_duplicate_retry_do_not_move_date_again(factory, setup):
    gateway, token, _, _ = setup
    pause(factory, setup, months=3)
    chosen = resume_options(factory, gateway, token)
    key = uuid4()
    gateway.lose_pause_response = True
    with pytest.raises(PaymentUnavailable):
        resume(factory, setup, key=key, dates=chosen)
    with factory() as session:
        assert session.get(DonorResumeRecord, key).confirmed_at is None
    assert resume(factory, setup, key=key, dates=chosen)["confirmed"]
    assert resume(factory, setup, key=key, dates=chosen)["confirmed"]
    assert len(gateway.pauses) == 2  # Original pause, then one shortened schedule.


def test_stale_confirmation_and_reused_key_are_rejected(factory, setup):
    gateway, token, _, _ = setup
    pause(factory, setup, months=3)
    chosen = resume_options(factory, gateway, token)
    with pytest.raises(SubscriptionConflict, match="date changed"):
        resume(factory, setup, dates={**chosen, "next_renewal": chosen["next_renewal"] + timedelta(days=1)})
    key = uuid4()
    resume(factory, setup, key=key, dates=chosen)
    with pytest.raises(SubscriptionConflict, match="key already used"):
        resume(factory, setup, key=key, dates={**chosen, "next_renewal": chosen["next_renewal"] + timedelta(days=1)})
    with pytest.raises(SubscriptionConflict, match="already exists"):
        resume(factory, setup, dates=chosen)
    assert len(gateway.pauses) == 2


def test_cancellation_wins_over_a_pending_or_completed_resume(factory, setup):
    gateway, token, _, _ = setup
    original = pause(factory, setup, months=3)["pause"]
    chosen = resume_options(factory, gateway, token)
    key = uuid4()
    resume(factory, setup, key=key, dates=chosen)
    with factory() as session:
        attempt_id = session.scalar(select(DonorPauseRecord.attempt_id))
    cancel_recurring(factory, gateway, token, request_id=uuid4(), attempt_id=attempt_id,
                     period_end=original["starts_at"])
    assert resume_options(factory, gateway, token)["state"] == "unavailable"
    with pytest.raises(SubscriptionConflict, match="changed"):
        resume(factory, setup, key=key, dates=chosen)
    assert len(gateway.pauses) == 2


def test_unrecognized_reversion_to_original_pause_is_not_accepted(factory, setup):
    gateway, _, _, _ = setup
    original = pause(factory, setup, months=3)["pause"]
    resume(factory, setup)
    with factory.begin() as session:
        row = session.scalar(select(DonorPauseRecord))
        attempt = session.get(StripeCheckoutRecord, row.attempt_id)
        remote = {**gateway.remote, "pause_collection": {"behavior": "void", "resumes_at": int(original["resumes_at"].timestamp())}}
        with pytest.raises(ValueError, match="Unrecognized"):
            reconcile_pause(session, attempt, remote)


def test_auth_origin_and_pause_ownership_are_required(factory, setup):
    gateway, token, _, _ = setup
    pause(factory, setup, months=3)
    chosen = resume_options(factory, gateway, token)
    body = {"pause_id": chosen["pause_id"], "next_renewal": chosen["next_renewal"].isoformat()}
    headers = {"Origin": SETTINGS.origin, "Idempotency-Key": str(uuid4())}
    browser = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway), base_url=SETTINGS.origin)
    assert browser.get("/donor/me/recurring/resume").status_code == 401
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/recurring/resume", json=body, headers={"Idempotency-Key": str(uuid4())}).status_code == 403
    assert browser.post("/donor/me/recurring/resume", json={**body, "charge_now": True}, headers=headers).status_code == 422
    with factory.begin() as session:
        guest_donor(session, "stranger@example.org")
        other = claim_account(session, issue_login_link(session, "stranger@example.org"))
    browser.cookies.set(SESSION_COOKIE, other)
    assert browser.post("/donor/me/recurring/resume", json=body, headers=headers).status_code == 409
    assert len(gateway.pauses) == 1
    browser.cookies.set(SESSION_COOKIE, token)
    assert browser.post("/donor/me/recurring/resume", json=body, headers=headers).json()["confirmed"]


def test_old_unconfirmed_request_cannot_be_reissued(factory, setup):
    gateway, token, _, _ = setup
    pause(factory, setup, months=3)
    chosen = resume_options(factory, gateway, token)
    key = uuid4()
    with factory.begin() as session:
        row = session.get(DonorPauseRecord, uuid_from(chosen["pause_id"]))
        session.add(DonorResumeRecord(id=key, donor_id=row.donor_id, pause_id=row.id,
                                      next_renewal=chosen["next_renewal"], created_at=datetime.now(UTC) - timedelta(days=2)))
    with pytest.raises(SubscriptionConflict, match="reconciliation"):
        resume(factory, setup, key=key, dates=chosen)
    assert len(gateway.pauses) == 1


def test_account_read_reconciles_lost_response_and_refreshes_projection(factory, setup, monkeypatch):
    gateway, token, _, anchor = setup
    original = pause(factory, setup, months=3)["pause"]
    first, next_date = original["starts_at"], renewal_after(original["starts_at"], 1, anchor)
    freeze(monkeypatch, next_date - timedelta(hours=1))
    gateway.remote["items"]["data"][0].update(current_period_start=int(first.timestamp()),
                                               current_period_end=int(next_date.timestamp()))
    chosen = resume_options(factory, gateway, token)
    gateway.lose_pause_response = True
    with pytest.raises(PaymentUnavailable):
        resume(factory, setup, dates=chosen)
    assert resume_options(factory, gateway, token)["confirmed"]
    with factory.begin() as session:
        from impactgraph.donors import _utc
        from impactgraph.persistence import DonorMetricProjectionRecord
        row = session.scalar(select(DonorMetricProjectionRecord))
        assert _utc(row.next_refresh_at) == next_date
