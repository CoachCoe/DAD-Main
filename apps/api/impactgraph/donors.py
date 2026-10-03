"""R1 donor identity and guest checkout foundation.

Callers own the transaction. These are internal services, not public endpoints: issuing
a login token is only for a trusted email sender, never for a browser response or log.
No function here claims a payment succeeded or awards a conversion, streak, or badge.
"""

from __future__ import annotations

import hashlib
import re
import secrets
from datetime import UTC, datetime, timedelta
from enum import StrEnum
from uuid import UUID

from sqlalchemy import select, update
from sqlalchemy.dialects.postgresql import insert as postgres_insert
from sqlalchemy.dialects.sqlite import insert as sqlite_insert
from sqlalchemy.orm import Session

from .persistence import (
    DonorCheckoutRecord,
    DonorLoginRecord,
    DonorRecord,
    DonorSessionRecord,
    ReferralTouchRecord,
)

REFERRAL_WINDOW = timedelta(days=30)
LOGIN_LIFETIME = timedelta(minutes=15)
SESSION_LIFETIME = timedelta(days=30)


class ReferralKind(StrEnum):
    LINK = "LINK"
    IRL = "IRL"


class GiftFrequency(StrEnum):
    MONTHLY = "MONTHLY"
    ONE_TIME = "ONE_TIME"


class DonorAccessDenied(ValueError):
    """The credential is invalid, expired, consumed, revoked, or disabled."""


def _hash(token: str) -> str:
    return hashlib.sha256(token.encode("utf-8")).hexdigest()


def _utc(value: datetime) -> datetime:
    # SQLite discards offsets on a database round trip; PostgreSQL retains them.
    return value.replace(tzinfo=UTC) if value.tzinfo is None else value.astimezone(UTC)


def _now(value: datetime | None) -> datetime:
    if value is not None and value.tzinfo is None:
        raise ValueError("An explicit clock must have a time zone")
    return _utc(value or datetime.now(UTC))


def normalize_email(email: str) -> str:
    """Case-insensitive matching without guessing aliases or removing dots/plus tags.

    R1 accepts ASCII mailbox names and IDNA domains. Validation is deliberately not
    treated as ownership verification; only redeeming an emailed token proves that.
    """
    email = email.strip()
    if email.count("@") != 1:
        raise ValueError("Enter a valid email address")
    local, domain = email.rsplit("@", 1)
    try:
        domain = domain.encode("idna").decode("ascii").lower()
    except UnicodeError as exc:
        raise ValueError("Enter a valid email address") from exc
    labels = domain.split(".")
    if (
        not re.fullmatch(r"[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]{1,64}", local)
        or local.startswith(".") or local.endswith(".") or ".." in local
        or len(domain) > 253 or len(labels) < 2
        or any(not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", p)
               for p in labels)
    ):
        raise ValueError("Enter a valid email address")
    return f"{local.lower()}@{domain}"


def guest_donor(session: Session, email: str) -> DonorRecord:
    """Matching an email finds the internal owner; it grants no read access."""
    email = normalize_email(email)
    donor = session.scalar(select(DonorRecord).where(DonorRecord.email == email))
    if donor is None:
        # The unique email constraint arbitrates concurrent first checkouts. A
        # conflict-safe insert keeps the caller's transaction intact and avoids
        # SQLite legacy transaction mode committing an outermost SAVEPOINT early.
        insert = {"postgresql": postgres_insert, "sqlite": sqlite_insert}[
            session.get_bind().dialect.name
        ]
        session.execute(insert(DonorRecord).values(email=email)
                        .on_conflict_do_nothing(index_elements=["email"]))
        donor = session.scalar(select(DonorRecord).where(DonorRecord.email == email))
    if donor.disabled:
        raise DonorAccessDenied("Donor unavailable")
    return donor


def issue_login_link(session: Session, email: str, *, now: datetime | None = None) -> str | None:
    """Return a secret for email delivery, or None; an HTTP wrapper must respond alike.

    Requesting another link does not invalidate a working one: otherwise anyone who
    knows a donor's email could repeatedly break their login.
    """
    now = _now(now)
    donor = session.scalar(
        select(DonorRecord).where(DonorRecord.email == normalize_email(email)).with_for_update()
        .execution_options(populate_existing=True)
    )
    if donor is None or donor.disabled:
        return None
    token = secrets.token_urlsafe(32)
    session.add(DonorLoginRecord(
        donor_id=donor.id, token_hash=_hash(token), expires_at=now + LOGIN_LIFETIME,
    ))
    session.flush()
    return token


def claim_account(session: Session, token: str, *, now: datetime | None = None) -> str:
    """Redeem one proof of email control and return a new, hashed-at-rest session."""
    now = _now(now)
    link = session.scalar(select(DonorLoginRecord).where(DonorLoginRecord.token_hash == _hash(token)))
    if link is None:
        raise DonorAccessDenied("Invalid or expired login link")
    donor = session.scalar(select(DonorRecord).where(DonorRecord.id == link.donor_id)
                           .with_for_update().execution_options(populate_existing=True))
    if donor is None or donor.disabled:
        raise DonorAccessDenied("Invalid or expired login link")
    # Compare-and-set, rather than a read followed by a write, prevents two requests
    # redeeming the same token. The account claim and new session commit atomically.
    redeemed = session.execute(
        update(DonorLoginRecord).where(
            DonorLoginRecord.id == link.id,
            DonorLoginRecord.consumed_at.is_(None),
            DonorLoginRecord.expires_at > now,
        ).values(consumed_at=now).returning(DonorLoginRecord.id)
        .execution_options(synchronize_session="fetch")
    ).scalar_one_or_none()
    if redeemed is None:
        raise DonorAccessDenied("Invalid or expired login link")
    session.execute(update(DonorLoginRecord).where(
        DonorLoginRecord.donor_id == donor.id, DonorLoginRecord.consumed_at.is_(None),
    ).values(consumed_at=now))
    if donor.claimed_at is None:
        donor.claimed_at = now
    secret = secrets.token_urlsafe(32)
    session.add(DonorSessionRecord(
        donor_id=donor.id, token_hash=_hash(secret), expires_at=now + SESSION_LIFETIME,
    ))
    session.flush()
    return secret


def authenticated_donor(session: Session, token: str, *, now: datetime | None = None) -> DonorRecord:
    now = _now(now)
    donor = session.scalar(
        select(DonorRecord).join(DonorSessionRecord, DonorSessionRecord.donor_id == DonorRecord.id)
        .where(
            DonorSessionRecord.token_hash == _hash(token),
            DonorSessionRecord.revoked_at.is_(None), DonorSessionRecord.expires_at > now,
            DonorRecord.disabled.is_(False), DonorRecord.claimed_at.is_not(None),
        )
    )
    if donor is None:
        raise DonorAccessDenied("Sign in required")
    return donor


def sign_out(session: Session, token: str, *, now: datetime | None = None) -> None:
    session.execute(update(DonorSessionRecord).where(
        DonorSessionRecord.token_hash == _hash(token), DonorSessionRecord.revoked_at.is_(None),
    ).values(revoked_at=_now(now)))


def activate_sharing(
    session: Session, session_token: str, public_name: str, *,
    replace_code: bool = False, now: datetime | None = None,
) -> str:
    donor = authenticated_donor(session, session_token, now=now)
    # Serialize code creation/replacement so simultaneous requests cannot return a
    # supposedly permanent code that another request immediately overwrites.
    donor = session.scalar(select(DonorRecord).where(DonorRecord.id == donor.id)
                           .with_for_update().execution_options(populate_existing=True))
    name = public_name.strip()
    if not name or len(name) > 80 or "@" in name or any(ord(c) < 32 for c in name):
        raise ValueError("Choose a public first name or nickname of 1 to 80 characters")
    donor.public_name = name
    if donor.referral_code is None or replace_code:
        donor.referral_code = secrets.token_urlsafe(24)
    session.flush()
    return donor.referral_code


def capture_referral(
    session: Session, referral_code: str, kind: ReferralKind, *,
    visitor_token: str | None = None, now: datetime | None = None,
) -> str | None:
    """Retain the first valid touch for 30 days; returning never extends its expiry."""
    now = _now(now)
    kind = ReferralKind(kind)
    touch = session.scalar(select(ReferralTouchRecord).where(
        ReferralTouchRecord.token_hash == _hash(visitor_token),
    ).with_for_update()) if visitor_token else None
    if touch is not None and _utc(touch.expires_at) > now:
        return visitor_token
    referrer = session.scalar(select(DonorRecord).where(
        DonorRecord.referral_code == referral_code, DonorRecord.claimed_at.is_not(None),
        DonorRecord.public_name.is_not(None), DonorRecord.disabled.is_(False),
    ))
    if referrer is None:
        return None
    # Expired tokens are replaced, not repurposed; old checkout snapshots are immutable.
    token = secrets.token_urlsafe(32)
    session.add(ReferralTouchRecord(
        token_hash=_hash(token), referrer_id=referrer.id, kind=kind.value,
        touched_at=now, expires_at=now + REFERRAL_WINDOW,
    ))
    session.flush()
    return token


def prepare_checkout(
    session: Session, email: str, amount_minor: int, *,
    frequency: GiftFrequency = GiftFrequency.MONTHLY,
    visitor_token: str | None = None, now: datetime | None = None,
) -> DonorCheckoutRecord:
    """Create a USD draft. This neither charges money nor credits a referral.

    A payment adapter must enforce expiry at successful payment time, store permanent
    donor attribution on the first gift, and reuse that relationship for later gifts.
    """
    now = _now(now)
    if type(amount_minor) is not int or not 100 <= amount_minor <= 2**63 - 1:
        raise ValueError("Gift must be an integer number of cents, at least 100")
    frequency = GiftFrequency(frequency)
    donor = guest_donor(session, email)
    touch = session.scalar(select(ReferralTouchRecord).where(
        ReferralTouchRecord.token_hash == _hash(visitor_token), ReferralTouchRecord.expires_at > now,
    )) if visitor_token else None
    if touch is not None:
        referrer = session.get(DonorRecord, touch.referrer_id)
        if referrer is None or referrer.disabled or referrer.id == donor.id:
            touch = None
    checkout = DonorCheckoutRecord(
        donor_id=donor.id, amount_minor=amount_minor, currency="USD", frequency=frequency.value,
        referrer_id=touch.referrer_id if touch else None,
        referral_kind=touch.kind if touch else None,
        referral_expires_at=touch.expires_at if touch else None,
    )
    session.add(checkout)
    session.flush()
    return checkout


def owned_checkout(
    session: Session, session_token: str, checkout_id: UUID, *, now: datetime | None = None,
) -> DonorCheckoutRecord:
    donor = authenticated_donor(session, session_token, now=now)
    checkout = session.scalar(select(DonorCheckoutRecord).where(
        DonorCheckoutRecord.id == checkout_id, DonorCheckoutRecord.donor_id == donor.id,
    ))
    if checkout is None:
        raise LookupError("Checkout not found")
    return checkout
