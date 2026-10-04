from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import create_app
from impactgraph.donor_projections import dashboard, refresh_next, refresh_projection
from impactgraph.donors import guest_donor, issue_login_link, prepare_checkout
from impactgraph.persistence import (
    DonorBillingPeriodRecord,
    DonorGiftRecord,
    DonorMetricProjectionRecord,
    DonorRefundRecord,
)
from impactgraph.stripe_payments import PaymentSettings

JAN = datetime(2027, 1, 1, tzinfo=UTC)
FEB = datetime(2027, 2, 1, tzinfo=UTC)
MAR = datetime(2027, 3, 1, tzinfo=UTC)


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


def gift(session, email, *, paid_at=JAN, referrer=None, kind="LINK", monthly=False, end=FEB):
    draft = prepare_checkout(session, email, 3000, frequency="MONTHLY" if monthly else "ONE_TIME")
    if referrer:
        draft.referrer_id, draft.referral_kind, draft.referral_expires_at = referrer.id, kind, MAR
    period = None
    if monthly:
        period = DonorBillingPeriodRecord(donor_id=draft.donor_id, checkout_id=draft.id,
                                         stripe_invoice_id="in_" + uuid4().hex,
                                         stripe_subscription_id="sub_test", due_at=paid_at,
                                         ends_at=end, status="PAID")
        session.add(period)
        session.flush()
    row = DonorGiftRecord(donor_id=draft.donor_id, checkout_id=draft.id,
                         stripe_object_id="pi_" + uuid4().hex, amount_minor=3000,
                         currency="USD", frequency=draft.frequency, paid_at=paid_at,
                         period_id=period.id if period else None)
    session.add(row)
    session.flush()
    return row


def refund(session, row, when, amount=3000):
    session.add(DonorRefundRecord(gift_id=row.id, stripe_refund_id="re_" + uuid4().hex,
                                 amount_minor=amount, currency="USD", status="SUCCEEDED",
                                 observed_at=when))
    session.flush()


def test_referral_totals_are_direct_separate_and_refund_adjusted(factory):
    with factory.begin() as session:
        alice = guest_donor(session, "alice@example.org")
        ben = gift(session, "ben@example.org", referrer=alice)
        gift(session, "ben@example.org", paid_at=JAN + timedelta(hours=1))
        cara = gift(session, "cara@example.org", referrer=alice, kind="IRL")
        gift(session, "dee@example.org", referrer=guest_donor(session, "ben@example.org"))
        for donor_id in [alice.id, ben.donor_id, cara.donor_id]:
            refresh_projection(session, donor_id, now=JAN + timedelta(hours=2))
        result = dashboard(session, alice.id, now=JAN + timedelta(hours=2))
        assert result["referrals"] == {"link": 1, "irl": 1, "total_minor": 9000,
                                       "link_minor": 6000, "irl_minor": 3000, "currency": "USD"}
        refund(session, cara, JAN + timedelta(hours=3))
        refresh_projection(session, cara.donor_id, now=JAN + timedelta(hours=3))
        assert dashboard(session, alice.id, now=JAN + timedelta(hours=3))["referrals"]["irl"] == 0
        assert dashboard(session, alice.id, now=JAN + timedelta(hours=3))["referrals"]["total_minor"] == 6000


def test_award_and_best_survive_refunds_and_repeated_refresh(factory):
    with factory.begin() as session:
        first = gift(session, "ben@example.org", monthly=True)
        second = gift(session, "ben@example.org", monthly=True, paid_at=FEB, end=MAR)
        projected = refresh_projection(session, first.donor_id, now=FEB)
        award = projected.founding_awarded_at
        assert projected.best_streak == projected.current_streak == 2
        refund(session, first, FEB + timedelta(hours=1))
        refund(session, second, FEB + timedelta(hours=1))
        for _ in range(2):
            projected = refresh_projection(session, first.donor_id, now=FEB + timedelta(hours=2))
            assert projected.current_streak == 0 and projected.best_streak == 2
            assert projected.founding_awarded_at == award


def test_backfill_replays_refunds_before_later_gifts_without_inventing_best(factory):
    with factory.begin() as session:
        first = gift(session, "ben@example.org", monthly=True)
        refund(session, first, JAN + timedelta(days=1))
        gift(session, "ben@example.org", monthly=True, paid_at=FEB, end=MAR)
        row = refresh_projection(session, first.donor_id, now=FEB)
        assert row.best_streak == row.current_streak == 1


def test_backfill_recovers_best_before_late_refund(factory):
    with factory.begin() as session:
        first = gift(session, "ben@example.org", monthly=True)
        gift(session, "ben@example.org", monthly=True, paid_at=FEB, end=MAR)
        refund(session, first, FEB + timedelta(days=1))
        row = refresh_projection(session, first.donor_id, now=FEB + timedelta(days=2))
        assert row.best_streak == 2 and row.current_streak == 1


def test_grace_expiry_requires_refresh_and_missing_period_is_not_zero(factory):
    with factory.begin() as session:
        first = gift(session, "ben@example.org", monthly=True)
        session.add(DonorBillingPeriodRecord(donor_id=first.donor_id, checkout_id=first.checkout_id,
                                            stripe_invoice_id="in_failed", stripe_subscription_id="sub_test",
                                            due_at=FEB, ends_at=MAR, status="FAILED"))
        session.flush()
        row = refresh_projection(session, first.donor_id, now=FEB + timedelta(days=13))
        assert row.current_streak == 1 and row.at_risk
        assert dashboard(session, first.donor_id, now=FEB + timedelta(days=14))["streak"] is None
        row = refresh_projection(session, first.donor_id, now=FEB + timedelta(days=14))
        assert row.current_streak == 0 and not row.at_risk
        row = refresh_projection(session, first.donor_id, now=MAR)
        assert row.current_streak is None
        assert dashboard(session, first.donor_id, now=MAR)["streak"] is None


def test_earlier_payment_corrects_relationship_and_expiry_does_not_reattribute(factory):
    with factory.begin() as session:
        alice, cara = guest_donor(session, "alice@example.org"), guest_donor(session, "cara@example.org")
        later = gift(session, "ben@example.org", paid_at=JAN + timedelta(days=2), referrer=alice)
        refresh_projection(session, later.donor_id, now=JAN + timedelta(days=3))
        earlier = gift(session, "ben@example.org", paid_at=JAN, referrer=cara, kind="IRL")
        row = refresh_projection(session, earlier.donor_id, now=JAN + timedelta(days=3))
        assert row.referrer_id == cara.id and row.referral_kind == "IRL"
        assert row.net_minor == 6000
        row = refresh_projection(session, earlier.donor_id, now=MAR)
        assert row.referrer_id == cara.id


def test_missing_referral_projection_is_not_zero(factory):
    with factory.begin() as session:
        alice = guest_donor(session, "alice@example.org")
        gift(session, "ben@example.org", referrer=alice)
        refresh_projection(session, alice.id, now=JAN)
        assert dashboard(session, alice.id, now=JAN)["referrals"] is None


def test_worker_backfills_then_waits_until_due(factory):
    with factory.begin() as session:
        donor = guest_donor(session, "empty@example.org")
        donor_id = donor.id
    assert refresh_next(factory, now=JAN)
    assert not refresh_next(factory, now=JAN)
    with factory() as session:
        result = dashboard(session, donor_id, now=JAN)
        assert result["referrals"]["total_minor"] == 0
        assert result["streak"]["current"] == 0
        assert not result["founding_donor"]
    assert refresh_next(factory, now=JAN + timedelta(days=1))
    with factory() as session:
        assert len(session.scalars(select(DonorMetricProjectionRecord)).all()) == 1


def test_dashboard_authentication_and_no_other_donor_data(factory):
    origin = "https://dad.example"
    with factory.begin() as session:
        owner = guest_donor(session, "owner@example.org")
        other = guest_donor(session, "other@example.org")
        secret = issue_login_link(session, owner.email)
        other_id = other.id
    browser = TestClient(create_app(factory=factory, settings=PaymentSettings(origin=origin)), base_url=origin)
    assert browser.get("/donor/me/dashboard").status_code == 401
    assert browser.post("/donor/claim", json={"token": secret}, headers={"Origin": origin}).status_code == 200
    result = browser.get(f"/donor/me/dashboard?donor_id={other_id}")
    assert result.status_code == 200 and result.headers["cache-control"] == "no-store"
    assert "other@example.org" not in result.text and str(other_id) not in result.text
    assert result.json()["mode"] == "test" and result.json()["streak"]["current"] == 0
