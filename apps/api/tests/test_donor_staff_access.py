import base64
from datetime import UTC, datetime, timedelta

import pytest
from cryptography.fernet import Fernet
from cryptography.hazmat.primitives.hashes import SHA1
from cryptography.hazmat.primitives.twofactor.totp import TOTP
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph import donor_staff as staff
from impactgraph.donor_staff_access import change_role, recovery_invitation
from impactgraph.persistence import DonorStaffAuditRecord
from tests.test_donor_staff import PASSWORD, account


@pytest.fixture
def setup(engine, monkeypatch):
    monkeypatch.setenv("DONOR_STAFF_ENCRYPTION_KEY", Fernet.generate_key().decode())
    factory = sessionmaker(engine, expire_on_commit=False)
    admin, _, _ = account(factory)
    support, _, _ = account(factory, "SUPPORT", "support@example.org")
    with factory() as session:
        actor_id = staff.require_staff(session, admin).id
        target_id = staff.require_staff(session, support).id
    return factory, admin, support, actor_id, target_id


def test_recovery_revokes_old_access_and_requires_new_mfa(setup):
    factory, _, old_token, actor_id, target_id = setup
    now = datetime.now(UTC)
    with factory.begin() as session:
        token = recovery_invitation(session, actor_id, target_id, now=now)
        provisioned = staff.provision(session, token, now=now)
    with factory() as session, pytest.raises(staff.StaffDenied):
        staff.require_staff(session, old_token)
    otp = TOTP(base64.b32decode(provisioned["secret"]), 6, SHA1(), 30)
    staff.activate(factory, token, PASSWORD, otp.generate(int(now.timestamp())).decode(), now=now)
    later = now + timedelta(seconds=60)
    new_token = staff.sign_in(factory, "support@example.org", PASSWORD, otp.generate(int(later.timestamp())).decode(), now=later)
    with factory() as session:
        assert staff.require_staff(session, new_token).id == target_id
        event = session.scalar(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "staff.recovery_invited"))
        assert event.actor_id == actor_id and event.target == str(target_id)


def test_replacement_invitation_invalidates_the_previous_one(setup):
    factory, _, _, actor_id, target_id = setup
    with factory.begin() as session:
        first = recovery_invitation(session, actor_id, target_id)
        staff.provision(session, first)
        second = recovery_invitation(session, actor_id, target_id)
    with factory() as session, pytest.raises(staff.StaffDenied):
        staff.provision(session, first)
    with factory.begin() as session:
        assert staff.provision(session, second)["email"] == "support@example.org"


def test_only_another_current_administrator_may_replace_access(setup):
    factory, _, _, actor_id, target_id = setup
    with factory.begin() as session, pytest.raises(staff.StaffForbidden):
        recovery_invitation(session, target_id, actor_id)
    with factory.begin() as session, pytest.raises(ValueError, match="Another administrator"):
        recovery_invitation(session, actor_id, actor_id)
    with factory.begin() as session, pytest.raises(ValueError, match="Another administrator"):
        change_role(session, actor_id, actor_id, "SUPPORT")


def test_role_change_revokes_old_session_and_records_before_and_after(setup):
    factory, _, old_token, actor_id, target_id = setup
    with factory.begin() as session:
        assert change_role(session, actor_id, target_id, "FINANCE")["role"] == "FINANCE"
    with factory() as session, pytest.raises(staff.StaffDenied):
        staff.require_staff(session, old_token)
    with factory() as session:
        event = session.scalar(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "staff.role_changed"))
        assert event.details == {"previous": "SUPPORT", "role": "FINANCE"}
