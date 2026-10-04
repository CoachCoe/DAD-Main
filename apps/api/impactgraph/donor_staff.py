"""Individual staff accounts, password + replay-resistant TOTP, and named permissions."""
import argparse
import base64
import os
import secrets
from datetime import UTC, datetime, timedelta

from argon2 import PasswordHasher
from argon2.exceptions import InvalidHashError, VerificationError
from cryptography.fernet import Fernet
from cryptography.hazmat.primitives.hashes import SHA1
from cryptography.hazmat.primitives.twofactor import InvalidToken
from cryptography.hazmat.primitives.twofactor.totp import TOTP
from sqlalchemy import select, update

from .donors import _hash, _utc, normalize_email
from .persistence import (
    DonorStaffAuditRecord,
    DonorStaffInviteRecord,
    DonorStaffRecord,
    DonorStaffSessionRecord,
)

COOKIE = "dad_staff_session"
PERMISSIONS = {
    "SUPPORT": frozenset({"donor.read", "message.resend"}),
    "FINANCE": frozenset({"donor.read", "message.resend", "gift.refund", "gift.cancel", "data.export"}),
    "ADMIN": frozenset({"donor.read", "message.resend", "gift.refund", "gift.cancel", "data.export", "staff.manage", "content.publish", "audit.read"}),
}
PASSWORDS = PasswordHasher()
DUMMY_HASH = PASSWORDS.hash(secrets.token_urlsafe(32))


class StaffDenied(Exception):
    pass


class StaffForbidden(Exception):
    pass


def cipher():
    key = os.getenv("DONOR_STAFF_ENCRYPTION_KEY", "")
    if not key:
        raise ValueError("Staff encryption is not configured")
    return Fernet(key.encode())


def audit(session, actor_id, action, target, **details):
    session.add(DonorStaffAuditRecord(actor_id=actor_id, action=action, target=str(target), details=details))


def view(staff):
    return {"id": str(staff.id), "email": staff.email, "name": staff.name, "role": staff.role,
            "disabled": staff.disabled, "active": staff.password_hash is not None,
            "permissions": sorted(PERMISSIONS[staff.role])}


def require_staff(session, token, permission=None, *, now=None):
    now = now or datetime.now(UTC)
    staff = session.scalar(select(DonorStaffRecord).join(DonorStaffSessionRecord,
        DonorStaffSessionRecord.staff_id == DonorStaffRecord.id).where(
        DonorStaffSessionRecord.token_hash == _hash(token or ""), DonorStaffSessionRecord.revoked_at.is_(None),
        DonorStaffSessionRecord.expires_at > now, DonorStaffRecord.disabled.is_(False)))
    if staff is None:
        raise StaffDenied("Staff sign-in required")
    if permission and permission not in PERMISSIONS[staff.role]:
        raise StaffForbidden("Permission denied")
    return staff


def invite_staff(session, *, email, name, role, actor_id=None, now=None):
    now = now or datetime.now(UTC)
    cipher()  # Fail before creating an unusable invitation.
    if role not in PERMISSIONS or not name.strip() or len(name) > 100:
        raise ValueError("Invalid staff identity")
    email = normalize_email(email)
    if session.scalar(select(DonorStaffRecord.id).where(DonorStaffRecord.email == email)):
        raise ValueError("A staff identity already exists for this email")
    staff = DonorStaffRecord(email=email, name=name.strip(), role=role)
    session.add(staff)
    session.flush()
    token = secrets.token_urlsafe(32)
    session.add(DonorStaffInviteRecord(staff_id=staff.id, token_hash=_hash(token), expires_at=now + timedelta(hours=24)))
    audit(session, actor_id, "staff.invited", staff.id, role=role)
    return token


def _invitation(session, token, now):
    row = session.scalar(select(DonorStaffInviteRecord).where(DonorStaffInviteRecord.token_hash == _hash(token))
                         .with_for_update())
    if row is None or row.consumed_at or _utc(row.expires_at) <= now or row.attempts >= 5:
        raise StaffDenied("Invitation invalid or expired")
    staff = session.get(DonorStaffRecord, row.staff_id)
    if staff.disabled or staff.password_hash:
        raise StaffDenied("Invitation invalid or expired")
    return row, staff


def provision(session, token, *, now=None):
    row, staff = _invitation(session, token, now or datetime.now(UTC))
    encryption = cipher()
    if row.pending_secret is None:
        row.pending_secret = encryption.encrypt(secrets.token_bytes(20))
    secret = encryption.decrypt(row.pending_secret)
    return {"email": staff.email, "secret": base64.b32encode(secret).decode(),
            "uri": TOTP(secret, 6, SHA1(), 30).get_provisioning_uri(staff.email, "DAD Staff")}


def _counter(secret, code, now, previous=-1):
    otp = TOTP(secret, 6, SHA1(), 30)
    step = int(now.timestamp()) // 30
    for counter in (step, step - 1, step + 1):
        if counter <= previous:
            continue
        try:
            otp.verify(code.encode(), counter * 30)
            return counter
        except InvalidToken:
            continue
    return None


def activate(factory, token, password, code, *, now=None):
    now = now or datetime.now(UTC)
    if not 12 <= len(password) <= 128:
        raise ValueError("Use a password of 12 to 128 characters")
    success = False
    with factory.begin() as session:
        row, staff = _invitation(session, token, now)
        row.attempts += 1
        counter = _counter(cipher().decrypt(row.pending_secret), code, now) if row.pending_secret else None
        if counter is not None:
            staff.password_hash, staff.totp_secret = PASSWORDS.hash(password), row.pending_secret
            staff.totp_counter = counter
            row.consumed_at, row.pending_secret = now, None
            audit(session, staff.id, "staff.activated", staff.id)
            success = True
    if not success:
        raise StaffDenied("Authenticator code invalid or expired")


def sign_in(factory, email, password, code, *, now=None):
    now = now or datetime.now(UTC)
    token = None
    with factory.begin() as session:
        staff = session.scalar(select(DonorStaffRecord).where(DonorStaffRecord.email == normalize_email(email)).with_for_update())
        try:
            verified = PASSWORDS.verify(staff.password_hash if staff and staff.password_hash else DUMMY_HASH, password)
        except (VerificationError, InvalidHashError):
            verified = False
        locked = bool(staff and staff.locked_until and _utc(staff.locked_until) > now)
        counter = _counter(cipher().decrypt(staff.totp_secret), code, now, staff.totp_counter) if (
            verified and staff and staff.totp_secret and not staff.disabled and not locked) else None
        if counter is not None:
            staff.totp_counter, staff.failures, staff.locked_until = counter, 0, None
            token = secrets.token_urlsafe(48)
            session.add(DonorStaffSessionRecord(staff_id=staff.id, token_hash=_hash(token), expires_at=now + timedelta(hours=8)))
            audit(session, staff.id, "staff.signed_in", staff.id)
        elif staff:
            if not locked:
                if staff.locked_until:
                    staff.failures, staff.locked_until = 0, None
                staff.failures += 1
                if staff.failures >= 5:
                    staff.locked_until = now + timedelta(minutes=15)
            audit(session, None, "staff.sign_in_failed", staff.id)
    if token is None:
        raise StaffDenied("Credentials invalid, expired, or temporarily locked")
    return token


def sign_out(session, token):
    staff = require_staff(session, token)
    session.execute(update(DonorStaffSessionRecord).where(DonorStaffSessionRecord.token_hash == _hash(token))
                    .values(revoked_at=datetime.now(UTC)))
    audit(session, staff.id, "staff.signed_out", staff.id)


def disable_staff(session, actor, staff_id):
    # Serializing administrator changes prevents two admins disabling one another.
    rows = session.scalars(select(DonorStaffRecord).order_by(DonorStaffRecord.id).with_for_update()
                           .execution_options(populate_existing=True)).all()
    target = next((row for row in rows if row.id == staff_id), None)
    if target is None:
        raise LookupError("Staff account not found")
    if actor.disabled or actor.role != "ADMIN":
        raise StaffForbidden("Administrator access required")
    if target.id == actor.id:
        raise ValueError("You cannot disable your own staff account")
    if target.role == "ADMIN" and sum(not row.disabled and row.password_hash is not None and row.role == "ADMIN"
                                       for row in rows if row.id != target.id) < 1:
        raise ValueError("Keep an active administrator")
    target.disabled = True
    session.execute(update(DonorStaffSessionRecord).where(DonorStaffSessionRecord.staff_id == target.id)
                    .values(revoked_at=datetime.now(UTC)))
    audit(session, actor.id, "staff.disabled", target.id)


def main():
    from .database import create_session_factory
    from .stripe_payments import PaymentSettings
    parser = argparse.ArgumentParser(description="Bootstrap the first named DAD staff administrator")
    parser.add_argument("--email", required=True)
    parser.add_argument("--name", required=True)
    args = parser.parse_args()
    factory = create_session_factory()
    with factory.begin() as session:
        if session.scalar(select(DonorStaffRecord.id).limit(1)):
            raise SystemExit("Staff already exists; use an administrator account")
        token = invite_staff(session, email=args.email, name=args.name, role="ADMIN")
    print(f"One-use private invitation: {PaymentSettings.from_env().origin}/staff/activate#token={token}")


if __name__ == "__main__":
    main()
