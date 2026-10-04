from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donor_email import EmailSettings
from impactgraph.donor_messages import deliver_next, enrolment_template, queue_financial_messages
from impactgraph.donor_receipts import annual_summary, receipt, render_receipt
from impactgraph.donors import _utc, claim_account, guest_donor, issue_login_link
from impactgraph.persistence import DonorGiftRecord, DonorMessageRecord, DonorRefundRecord
from tests.test_donor_pauses import make_pause_setup
from tests.test_stripe_payments import SETTINGS

MAIL = EmailSettings(host="smtp.example.org", sender="donor@example.org", encryption_key="configured-for-fake-transport")


class Transport:
    def __init__(self):
        self.sent = []
        self.fail = False

    def send(self, message):
        self.sent.append(message)
        if self.fail:
            self.fail = False
            raise OSError("Provider acknowledgement lost")


@pytest.fixture
def account(engine):
    factory = sessionmaker(engine, expire_on_commit=False)
    gateway, token, _, _ = make_pause_setup(factory)
    return factory, gateway, token


def test_receipt_delivery_is_durable_deduplicated_and_not_a_tax_claim(account):
    factory, _, _ = account
    with factory.begin() as session:
        gift = session.scalar(select(DonorGiftRecord))
        queue_financial_messages(session, gift.donor_id)
        queue_financial_messages(session, gift.donor_id)
        assert len(session.scalars(select(DonorMessageRecord)).all()) == 1
        document = receipt(session, gift.donor_id, gift.id)
        assert document["gift"]["net_minor"] == 3000 and document["tax_receipt"] is False
        assert "NOT A TAX RECEIPT" in render_receipt(document)
    transport = Transport()
    transport.fail = True
    now = datetime.now(UTC)
    assert deliver_next(factory, MAIL, SETTINGS.origin, transport, now=now)
    assert not deliver_next(factory, MAIL, SETTINGS.origin, transport, now=now)
    assert deliver_next(factory, MAIL, SETTINGS.origin, transport, now=now + timedelta(minutes=3))
    assert len(transport.sent) == 2
    assert transport.sent[0]["Message-ID"] == transport.sent[1]["Message-ID"]
    assert transport.sent[0].get_content() == transport.sent[1].get_content()
    assert transport.sent[0]["To"] == "ben@example.org"
    with factory() as session:
        row = session.scalar(select(DonorMessageRecord))
        assert row.completed_at is not None and row.attempts == 2 and row.last_error is None


def test_summary_net_uses_successful_refunds_and_records_as_of(account):
    factory, _, _ = account
    with factory.begin() as session:
        gift = session.scalar(select(DonorGiftRecord))
        session.add_all([DonorRefundRecord(gift_id=gift.id, stripe_refund_id="re_ok", amount_minor=1000,
                        currency="USD", status="SUCCEEDED", observed_at=datetime.now(UTC)),
                        DonorRefundRecord(gift_id=gift.id, stripe_refund_id="re_pending", amount_minor=2000,
                        currency="USD", status="PENDING", observed_at=datetime.now(UTC))])
        session.flush()
        result = annual_summary(session, gift.donor_id, 2026)
        assert result["amount_minor"] == 3000 and result["net_minor"] == 2000 and result["refunded_minor"] == 1000
        assert result["tax_receipt"] is False and result["timezone"] == "America/New_York"
        assert len(result["gifts"][0]["refunds"]) == 2


def test_receipts_require_owner_and_do_not_leak_gift_existence(account):
    factory, gateway, token = account
    client = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway), base_url=SETTINGS.origin)
    with factory.begin() as session:
        gift_id = session.scalar(select(DonorGiftRecord.id))
        guest_donor(session, "other@example.org")
        other = claim_account(session, issue_login_link(session, "other@example.org"))
    path = f"/donor/me/receipts/{gift_id}"
    assert client.get(path).status_code == 401
    client.cookies.set(SESSION_COOKIE, other)
    assert client.get(path).status_code == 404
    assert client.get(f"/donor/me/receipts/{uuid4()}").status_code == 404
    client.cookies.set(SESSION_COOKIE, token)
    assert client.get(path).json()["gift"]["amount_minor"] == 3000
    assert client.get("/donor/me/annual-summary/2025").status_code == 409


def test_prior_year_summary_is_queued_once_at_calendar_rollover(account):
    factory, _, _ = account
    with factory.begin() as session:
        donor_id = session.scalar(select(DonorGiftRecord.donor_id))
        # New York still belongs to the prior year at 00:00 UTC.
        queue_financial_messages(session, donor_id, now=datetime(2027, 1, 1, 0, tzinfo=UTC))
        assert not session.scalar(select(DonorMessageRecord).where(DonorMessageRecord.kind == "ANNUAL"))
        for _ in range(2):
            queue_financial_messages(session, donor_id, now=datetime(2027, 1, 1, 5, tzinfo=UTC))
        rows = session.scalars(select(DonorMessageRecord).where(DonorMessageRecord.kind == "ANNUAL")).all()
        assert len(rows) == 1 and rows[0].context == {"year": 2026}
        assert _utc(rows[0].available_at) == datetime(2027, 1, 1, 5, tzinfo=UTC)


def test_enrolment_template_does_not_promise_aid():
    subject, body = enrolment_template("Example program")
    assert subject == "Enrolment confirmation"
    assert "not a promise of funds or aid" in body and "Example program" in body


@pytest.mark.parametrize("opt_out", [False, True])
def test_referral_delivery_rechecks_consent_and_contains_no_referred_identity(account, opt_out):
    from impactgraph.persistence import DonorMetricProjectionRecord, DonorPreferenceRecord
    factory, _, _ = account
    with factory.begin() as session:
        donor_id = session.scalar(select(DonorGiftRecord.donor_id))
        referrer = guest_donor(session, "inviter@example.org")
        projection = session.scalar(select(DonorMetricProjectionRecord).where(DonorMetricProjectionRecord.donor_id == donor_id))
        projection.referrer_id, projection.referral_kind = referrer.id, "IRL"
        preference = DonorPreferenceRecord(donor_id=referrer.id, version=1, referral_emails=True, marketing_emails=False)
        session.add(preference)
        session.flush()
        queue_financial_messages(session, donor_id)
        preference.referral_emails = not opt_out
    transport = Transport()
    while deliver_next(factory, MAIL, SETTINGS.origin, transport):
        pass
    referrals = [message for message in transport.sent if message["To"] == "inviter@example.org"]
    assert len(referrals) == (0 if opt_out else 1)
    if referrals:
        assert "IRL Referral" in referrals[0]["Subject"]
        body = referrals[0].get_content()
        assert "ben@example.org" not in body and "$30" not in body
        assert "/account/profile" in body
    with factory() as session:
        job = session.scalar(select(DonorMessageRecord).where(DonorMessageRecord.kind == "REFERRAL"))
        assert job.completed_at and job.suppressed_reason == ("opted_out" if opt_out else None)


def test_recovery_sequence_stops_when_the_invoice_is_paid(account):
    from impactgraph.persistence import DonorBillingPeriodRecord
    factory, _, _ = account
    with factory.begin() as session:
        original = session.scalar(select(DonorBillingPeriodRecord))
        due = _utc(original.ends_at)
        failed = DonorBillingPeriodRecord(donor_id=original.donor_id, checkout_id=original.checkout_id,
            stripe_invoice_id="in_failed", stripe_subscription_id=original.stripe_subscription_id,
            due_at=due, ends_at=due + timedelta(days=30), status="FAILED")
        session.add(failed)
        session.flush()
        failed_id = failed.id
        queue_financial_messages(session, failed.donor_id, now=due)
    transport = Transport()
    while deliver_next(factory, MAIL, SETTINGS.origin, transport, now=due):
        pass
    assert len([message for message in transport.sent if "needs attention" in message["Subject"]]) == 1
    with factory.begin() as session:
        session.get(DonorBillingPeriodRecord, failed_id).status = "PAID"
    while deliver_next(factory, MAIL, SETTINGS.origin, transport, now=due + timedelta(days=13)):
        pass
    assert len([message for message in transport.sent if "needs attention" in message["Subject"]]) == 1
    with factory() as session:
        jobs = session.scalars(select(DonorMessageRecord).where(DonorMessageRecord.kind == "RECOVERY")).all()
        assert sum(job.suppressed_reason == "payment_recovered" for job in jobs) == 2
