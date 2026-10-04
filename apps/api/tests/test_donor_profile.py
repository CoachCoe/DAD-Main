import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donors import claim_account, guest_donor, issue_login_link
from impactgraph.persistence import DonorPreferenceEventRecord
from tests.test_stripe_payments import SETTINGS


@pytest.fixture
def account(engine):
    factory = sessionmaker(engine, expire_on_commit=False)
    with factory.begin() as session:
        guest_donor(session, "person@example.org")
        token = claim_account(session, issue_login_link(session, "person@example.org"))
    client = TestClient(create_app(factory=factory, settings=SETTINGS), base_url=SETTINGS.origin)
    client.cookies.set(SESSION_COOKIE, token)
    return factory, client


def test_preferences_are_opt_in_audited_and_reject_stale_updates(account):
    factory, client = account
    initial = client.get("/donor/me/profile").json()
    assert initial["version"] == 0 and not initial["referral_emails"] and not initial["marketing_emails"]
    body = {"version": 0, "referral_emails": True, "marketing_emails": False}
    assert client.post("/donor/me/preferences", json=body).status_code == 403
    result = client.post("/donor/me/preferences", json=body, headers={"Origin": SETTINGS.origin})
    assert result.json()["version"] == 1 and result.json()["referral_emails"]
    assert client.post("/donor/me/preferences", json=body, headers={"Origin": SETTINGS.origin}).status_code == 409
    result = client.post("/donor/me/preferences", json={**body, "version": 1, "referral_emails": False},
                         headers={"Origin": SETTINGS.origin})
    assert result.json()["version"] == 2 and not result.json()["referral_emails"]
    with factory() as session:
        events = session.scalars(select(DonorPreferenceEventRecord).order_by(DonorPreferenceEventRecord.version)).all()
        assert [(event.version, event.referral_emails, event.marketing_emails) for event in events] == [(1, True, False), (2, False, False)]
        assert all(event.consent_version == "r1-notifications-v1" and event.source == "account" for event in events)


def test_cannot_inject_another_identity_or_coerce_consent(account):
    _, client = account
    body = {"version": 0, "referral_emails": "true", "marketing_emails": False}
    assert client.post("/donor/me/preferences", json=body, headers={"Origin": SETTINGS.origin}).status_code == 422
    body.update(referral_emails=True, email="someone@example.org")
    assert client.post("/donor/me/preferences", json=body, headers={"Origin": SETTINGS.origin}).status_code == 422
    client.cookies.clear()
    assert client.get("/donor/me/profile").status_code == 401


def test_logout_everywhere_revokes_all_owned_sessions_only(account):
    factory, client = account
    with factory.begin() as session:
        second = claim_account(session, issue_login_link(session, "person@example.org"))
        guest_donor(session, "other@example.org")
        other = claim_account(session, issue_login_link(session, "other@example.org"))
    assert client.post("/donor/logout-all").status_code == 403
    assert client.post("/donor/logout-all", headers={"Origin": SETTINGS.origin}).status_code == 200
    assert client.get("/donor/me/profile").status_code == 401
    client.cookies.set(SESSION_COOKIE, second)
    assert client.get("/donor/me/profile").status_code == 401
    client.cookies.set(SESSION_COOKIE, other)
    assert client.get("/donor/me/profile").status_code == 200
