import base64
from datetime import UTC, datetime, timedelta

import pytest
from cryptography.fernet import Fernet
from cryptography.hazmat.primitives.hashes import SHA1
from cryptography.hazmat.primitives.twofactor.totp import TOTP
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph import donor_staff as staff
from impactgraph.donor_api import create_app
from impactgraph.persistence import DonorStaffAuditRecord, DonorStaffRecord, DonorStaffSessionRecord
from tests.test_stripe_payments import SETTINGS

PASSWORD = "a-test-only-staff-password"


@pytest.fixture
def factory(engine, monkeypatch):
    monkeypatch.setenv("DONOR_STAFF_ENCRYPTION_KEY", Fernet.generate_key().decode())
    return sessionmaker(engine, expire_on_commit=False)


def account(factory, role="ADMIN", email="staff@example.org"):
    now = datetime.now(UTC)
    earlier = now - timedelta(seconds=60)
    with factory.begin() as session:
        token = staff.invite_staff(session, email=email, name="Staff member", role=role, now=earlier)
        provisioned = staff.provision(session, token, now=earlier)
    otp = TOTP(base64.b32decode(provisioned["secret"]), 6, SHA1(), 30)
    staff.activate(factory, token, PASSWORD, otp.generate(int(earlier.timestamp())).decode(), now=earlier)
    session_token = staff.sign_in(factory, email, PASSWORD, otp.generate(int(now.timestamp())).decode(), now=now)
    return session_token, otp, now


def test_individual_mfa_account_session_and_replay_protection(factory):
    token, otp, now = account(factory)
    with factory() as session:
        actor = staff.require_staff(session, token, "staff.manage", now=now)
        assert actor.role == "ADMIN"
        row = session.scalar(select(DonorStaffSessionRecord))
        assert row.token_hash != token and row.expires_at is not None
        assert actor.totp_secret.startswith(b"gAAAA")
        assert actor.password_hash.startswith("$argon2id$")
    with pytest.raises(staff.StaffDenied):
        staff.sign_in(factory, "staff@example.org", PASSWORD, otp.generate(int(now.timestamp())).decode(), now=now)
    with factory() as session, pytest.raises(staff.StaffDenied):
        staff.require_staff(session, token, now=now + timedelta(hours=8))


def test_failed_attempts_persist_and_lock_across_requests(factory):
    _, otp, now = account(factory)
    future = now + timedelta(seconds=60)
    for _ in range(5):
        with pytest.raises(staff.StaffDenied):
            staff.sign_in(factory, "staff@example.org", "incorrect", "000000", now=future)
    with factory() as session:
        row = session.scalar(select(DonorStaffRecord))
        assert row.failures == 5 and row.locked_until is not None
        assert len(session.scalars(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "staff.sign_in_failed")).all()) == 5
    with pytest.raises(staff.StaffDenied):
        staff.sign_in(factory, "staff@example.org", PASSWORD, otp.generate(int(future.timestamp())).decode(), now=future)
    later = future + timedelta(minutes=16)
    assert staff.sign_in(factory, "staff@example.org", PASSWORD, otp.generate(int(later.timestamp())).decode(), now=later)


def test_invitation_expires_and_is_single_use(factory):
    now = datetime.now(UTC)
    with factory.begin() as session:
        token = staff.invite_staff(session, email="new@example.org", name="New staff", role="SUPPORT", now=now)
        details = staff.provision(session, token, now=now)
        assert staff.provision(session, token, now=now) == details
    otp = TOTP(base64.b32decode(details["secret"]), 6, SHA1(), 30)
    staff.activate(factory, token, PASSWORD, otp.generate(int(now.timestamp())).decode(), now=now)
    with factory() as session, pytest.raises(staff.StaffDenied):
        staff.provision(session, token, now=now)
    with factory.begin() as session:
        expired = staff.invite_staff(session, email="expired@example.org", name="Expired", role="SUPPORT", now=now - timedelta(days=2))
    with factory() as session, pytest.raises(staff.StaffDenied):
        staff.provision(session, expired, now=now)


def test_support_cannot_manage_staff_or_export_and_headers_cannot_elevate(factory):
    token, _, _ = account(factory, "SUPPORT")
    client = TestClient(create_app(factory=factory, settings=SETTINGS), base_url=SETTINGS.origin)
    assert client.get("/donor/staff/donors", headers={"X-Role": "ADMIN"}).status_code == 401
    client.cookies.set(staff.COOKIE, token)
    assert client.get("/donor/staff/donors").status_code == 200
    assert client.get("/donor/staff/accounts", headers={"X-Role": "ADMIN"}).status_code == 403
    assert client.get("/donor/staff/audit").status_code == 403
    with factory() as session, pytest.raises(staff.StaffForbidden):
        staff.require_staff(session, token, "data.export")
    body = client.get("/donor/staff/me").json()
    assert "password_hash" not in body and "totp_secret" not in body


def test_admin_disable_revokes_access_and_keeps_an_active_admin(factory):
    admin, _, _ = account(factory)
    support, _, _ = account(factory, "SUPPORT", "support@example.org")
    client = TestClient(create_app(factory=factory, settings=SETTINGS), base_url=SETTINGS.origin)
    client.cookies.set(staff.COOKIE, admin)
    with factory() as session:
        actor = staff.require_staff(session, admin)
        support_id = staff.require_staff(session, support).id
    path = f"/donor/staff/accounts/{support_id}/disable"
    assert client.post(path).status_code == 403
    assert client.post(path, headers={"Origin": SETTINGS.origin}).status_code == 200
    assert client.post(f"/donor/staff/accounts/{actor.id}/disable", headers={"Origin": SETTINGS.origin}).status_code == 409
    with factory() as session, pytest.raises(staff.StaffDenied):
        staff.require_staff(session, support)
    with factory() as session:
        record = session.scalar(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "staff.disabled"))
        assert record.actor_id == actor.id and record.target == str(support_id)


def test_staff_can_invite_without_a_developer_but_not_supply_an_actor(factory):
    token, _, _ = account(factory)
    client = TestClient(create_app(factory=factory, settings=SETTINGS), base_url=SETTINGS.origin)
    client.cookies.set(staff.COOKIE, token)
    body = {"email": "finance@example.org", "name": "Finance person", "role": "FINANCE"}
    assert client.post("/donor/staff/accounts", json=body).status_code == 403
    response = client.post("/donor/staff/accounts", json=body, headers={"Origin": SETTINGS.origin})
    assert response.status_code == 200 and "/staff/activate#token=" in response.json()["invitation_url"]
    assert client.post("/donor/staff/accounts", json={**body, "actor_id": "someone-else"}, headers={"Origin": SETTINGS.origin}).status_code == 422
