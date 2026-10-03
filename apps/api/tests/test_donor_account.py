from datetime import UTC, datetime, timedelta

import pytest
from cryptography.fernet import Fernet
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donor_email import EmailSettings, deliver_next, queue_login_email
from impactgraph.donors import guest_donor, issue_login_link, prepare_checkout
from impactgraph.persistence import DonorGiftRecord, DonorLoginEmailRecord, DonorLoginRecord
from impactgraph.stripe_payments import PaymentSettings

ORIGIN = "https://dad.example"


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


@pytest.fixture
def email_settings():
    return EmailSettings(host="smtp.example", sender="donors@example.org",
                         encryption_key=Fernet.generate_key().decode())


def client(factory, email_settings):
    return TestClient(create_app(factory=factory, settings=PaymentSettings(origin=ORIGIN),
                                 email_settings=email_settings), base_url=ORIGIN)


class Mailbox:
    def __init__(self, fail=False):
        self.messages = []
        self.fail = fail

    def send(self, message):
        self.messages.append(message)
        if self.fail:
            raise ConnectionError("Do not expose SMTP details or token")


def test_queued_login_uniform_throttled_and_single_use(factory, email_settings):
    with factory.begin() as session:
        guest_donor(session, "donor@example.org")
    browser = client(factory, email_settings)
    headers = {"Origin": ORIGIN}
    known = browser.post("/donor/login", json={"email": "donor@example.org"}, headers=headers)
    unknown = browser.post("/donor/login", json={"email": "unknown@example.org"}, headers=headers)
    assert known.status_code == unknown.status_code == 202
    assert known.json() == unknown.json()
    browser.post("/donor/login", json={"email": "DONOR@example.org"}, headers=headers)
    with factory() as session:
        assert len(session.scalars(select(DonorLoginEmailRecord)).all()) == 1
    mailbox = Mailbox()
    assert deliver_next(factory, email_settings, ORIGIN, mailbox)
    message = mailbox.messages[0]
    token = message.get_content().split("#token=")[1].split()[0]
    assert message["To"] == "donor@example.org"
    assert token not in known.text
    with factory() as session:
        assert session.scalar(select(DonorLoginEmailRecord)).encrypted_token is None
        assert session.scalar(select(DonorLoginRecord)).token_hash != token
    assert browser.get("/donor/claim", params={"token": token}).status_code == 405
    assert browser.post("/donor/claim", json={"token": token}).status_code == 403
    result = browser.post("/donor/claim", json={"token": token}, headers=headers)
    assert result.status_code == 200 and token not in result.text
    cookie = result.headers["set-cookie"]
    assert "HttpOnly" in cookie and "Secure" in cookie and "SameSite=lax" in cookie
    assert browser.get("/donor/me/gifts").json()["gifts"] == []
    assert browser.post("/donor/claim", json={"token": token}, headers=headers).status_code == 401
    saved = browser.cookies.get(SESSION_COOKIE)
    assert browser.post("/donor/logout", headers=headers).status_code == 200
    browser.cookies.set(SESSION_COOKIE, saved)
    assert browser.get("/donor/me/gifts").status_code == 401


def test_retry_reuses_committed_credential_and_expires(factory, email_settings):
    now = datetime.now(UTC)
    with factory.begin() as session:
        guest_donor(session, "donor@example.org")
        queue_login_email(session, "donor@example.org", now=now)
    mailbox = Mailbox(fail=True)
    assert deliver_next(factory, email_settings, ORIGIN, mailbox, now=now)
    token = mailbox.messages[0].get_content().split("#token=")[1].split()[0]
    with factory() as session:
        job = session.scalar(select(DonorLoginEmailRecord))
        assert job.last_error == "ConnectionError"
        assert token.encode() not in job.encrypted_token
        assert session.scalar(select(DonorLoginRecord)).consumed_at is None
    assert not deliver_next(factory, email_settings, ORIGIN, mailbox, now=now)
    assert deliver_next(factory, email_settings, ORIGIN, mailbox, now=now + timedelta(minutes=1))
    assert mailbox.messages[0].get_content() == mailbox.messages[1].get_content()
    assert mailbox.messages[0]["Message-ID"] == mailbox.messages[1]["Message-ID"]
    assert deliver_next(factory, email_settings, ORIGIN, mailbox, now=now + timedelta(minutes=16))
    assert len(mailbox.messages) == 2
    with factory() as session:
        assert session.scalar(select(DonorLoginEmailRecord)).encrypted_token is None


def test_history_is_owner_scoped_and_no_credential_echo(factory, email_settings):
    with factory.begin() as session:
        checkout = prepare_checkout(session, "owner@example.org", 3000)
        session.add(DonorGiftRecord(donor_id=checkout.donor_id, checkout_id=checkout.id,
                                   stripe_object_id="pi_owner", amount_minor=3000,
                                   currency="USD", frequency="MONTHLY", paid_at=datetime.now(UTC)))
        guest_donor(session, "other@example.org")
        other = issue_login_link(session, "other@example.org")
        owner = issue_login_link(session, "owner@example.org")
    browser = client(factory, email_settings)
    assert browser.get("/donor/me/gifts?email=owner@example.org").status_code == 401
    browser.post("/donor/claim", json={"token": other}, headers={"Origin": ORIGIN})
    assert browser.get("/donor/me/gifts").json()["gifts"] == []
    browser.post("/donor/claim", json={"token": owner}, headers={"Origin": ORIGIN})
    result = browser.get("/donor/me/gifts")
    assert result.headers["cache-control"] == "no-store"
    assert result.json()["gifts"][0]["amount_minor"] == 3000
    assert "pi_owner" not in result.text
    assert browser.get("/donor/me/gifts?offset=-1").status_code == 422
    invalid = browser.post("/donor/claim", json={"token": "short-secret"}, headers={"Origin": ORIGIN})
    assert invalid.status_code == 422 and "short-secret" not in invalid.text


def test_email_disabled_fails_closed(factory):
    browser = client(factory, EmailSettings())
    assert browser.post("/donor/login", json={"email": "a@example.org"},
                        headers={"Origin": ORIGIN}).status_code == 503
    with factory() as session:
        assert session.scalar(select(DonorLoginEmailRecord)) is None


def test_delivery_parks_after_five_failures(factory, email_settings):
    now = datetime.now(UTC)
    with factory.begin() as session:
        guest_donor(session, "donor@example.org")
        queue_login_email(session, "donor@example.org", now=now)
    mailbox = Mailbox(fail=True)
    for minute in range(5):
        assert deliver_next(factory, email_settings, ORIGIN, mailbox,
                            now=now + timedelta(minutes=minute))
    assert not deliver_next(factory, email_settings, ORIGIN, mailbox,
                            now=now + timedelta(minutes=6))
    assert len(mailbox.messages) == 5
    with factory() as session:
        job = session.scalar(select(DonorLoginEmailRecord))
        assert job.attempts == 5 and job.completed_at is None


def test_disabled_donor_receives_no_queued_mail(factory, email_settings):
    with factory.begin() as session:
        donor = guest_donor(session, "donor@example.org")
        queue_login_email(session, donor.email)
        donor.disabled = True
    mailbox = Mailbox()
    assert deliver_next(factory, email_settings, ORIGIN, mailbox)
    assert mailbox.messages == []


def test_smtp_requires_validated_tls_before_sending(email_settings, monkeypatch):
    import ssl

    from impactgraph.donor_email import SMTPTransport

    calls = []

    class SMTP:
        def __init__(self, host, port, timeout):
            assert (host, port, timeout) == ("smtp.example", 587, 15)

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def starttls(self, context):
            assert context.verify_mode == ssl.CERT_REQUIRED and context.check_hostname
            calls.append("tls")

        def send_message(self, message):
            assert calls == ["tls"]
            calls.append("send")

    monkeypatch.setattr("impactgraph.donor_email.smtplib.SMTP", SMTP)
    SMTPTransport(email_settings).send("test")
    assert calls == ["tls", "send"]
