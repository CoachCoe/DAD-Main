"""Audited administrator recovery and role changes without a database edit."""
import secrets
from datetime import UTC, datetime, timedelta

from sqlalchemy import select, update

from . import donor_staff as staff
from .donors import _hash
from .persistence import DonorStaffInviteRecord, DonorStaffRecord, DonorStaffSessionRecord


def _target(session, actor_id, target_id):
    rows = session.scalars(select(DonorStaffRecord).order_by(DonorStaffRecord.id).with_for_update()
                           .execution_options(populate_existing=True)).all()
    actor = next((row for row in rows if row.id == actor_id), None)
    target = next((row for row in rows if row.id == target_id), None)
    if not actor or actor.disabled or not actor.password_hash or actor.role != "ADMIN":
        raise staff.StaffForbidden("Administrator access required")
    if target is None:
        raise LookupError("Staff account not found")
    if actor.id == target.id:
        raise ValueError("Another administrator must change your own access")
    return target


def recovery_invitation(session, actor_id, target_id, *, now=None):
    now = now or datetime.now(UTC)
    staff.cipher()
    target = _target(session, actor_id, target_id)
    staff.audit(session, actor_id, "staff.recovery_invited", target.id,
                previously_disabled=target.disabled, previously_active=target.password_hash is not None)
    target.password_hash, target.totp_secret, target.disabled = None, None, False
    target.totp_counter, target.failures, target.locked_until = -1, 0, None
    session.execute(update(DonorStaffSessionRecord).where(DonorStaffSessionRecord.staff_id == target.id)
                    .values(revoked_at=now))
    session.execute(update(DonorStaffInviteRecord).where(DonorStaffInviteRecord.staff_id == target.id,
        DonorStaffInviteRecord.consumed_at.is_(None)).values(consumed_at=now, pending_secret=None))
    token = secrets.token_urlsafe(32)
    session.add(DonorStaffInviteRecord(staff_id=target.id, token_hash=_hash(token), expires_at=now + timedelta(hours=24)))
    return token


def change_role(session, actor_id, target_id, role):
    if role not in staff.PERMISSIONS:
        raise ValueError("Unknown staff role")
    target = _target(session, actor_id, target_id)
    previous = target.role
    if role != previous:
        target.role = role
        session.execute(update(DonorStaffSessionRecord).where(DonorStaffSessionRecord.staff_id == target.id)
                        .values(revoked_at=datetime.now(UTC)))
        staff.audit(session, actor_id, "staff.role_changed", target.id, previous=previous, role=role)
    return staff.view(target)
