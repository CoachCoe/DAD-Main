"""Sandbox donor login email outbox and vendor-neutral TLS SMTP transport.

No credentials in responses or logs. SMTP delivery is at least once: a lost acknowledgement
can cause a duplicate message, with the same Message-ID and the same single-use link.
"""

from __future__ import annotations

import os
import smtplib
import ssl
import time
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from email.message import EmailMessage

from cryptography.fernet import Fernet, InvalidToken
from sqlalchemy import select

from .database import create_session_factory
from .donors import LOGIN_LIFETIME, _utc, issue_login_link, normalize_email
from .persistence import DonorLoginEmailRecord, DonorRecord


@dataclass(frozen=True)
class EmailSettings:
    host: str = ""
    port: int = 587
    username: str = ""
    password: str = field(default="", repr=False)
    sender: str = ""
    encryption_key: str = field(default="", repr=False)

    @property
    def configured(self):
        return bool(self.host and self.sender and self.encryption_key)

    @classmethod
    def from_env(cls):
        return cls(host=os.getenv("DONOR_SMTP_HOST", ""),
                   port=int(os.getenv("DONOR_SMTP_PORT", "587")),
                   username=os.getenv("DONOR_SMTP_USERNAME", ""),
                   password=os.getenv("DONOR_SMTP_PASSWORD", ""),
                   sender=os.getenv("DONOR_EMAIL_FROM", ""),
                   encryption_key=os.getenv("DONOR_EMAIL_ENCRYPTION_KEY", ""))


class SMTPTransport:
    def __init__(self, settings):
        self.settings = settings

    def send(self, message):
        settings = self.settings
        with smtplib.SMTP(settings.host, settings.port, timeout=15) as client:
            client.starttls(context=ssl.create_default_context())
            if settings.username:
                client.login(settings.username, settings.password)
            client.send_message(message)


def queue_login_email(session, email, *, now=None):
    now = now or datetime.now(UTC)
    donor = session.scalar(select(DonorRecord).where(
        DonorRecord.email == normalize_email(email), DonorRecord.disabled.is_(False),
    ).with_for_update())
    if donor is None:
        return
    # The donor lock serializes requests across API processes, independently of the
    # per-address limiter. Requesting another email never revokes an existing link.
    recent = session.scalar(select(DonorLoginEmailRecord.id).where(
        DonorLoginEmailRecord.donor_id == donor.id,
        DonorLoginEmailRecord.created_at > now - timedelta(minutes=1),
    ).limit(1))
    if recent is None:
        session.add(DonorLoginEmailRecord(donor_id=donor.id, available_at=now))


def deliver_next(factory, settings, origin, transport, *, now=None):
    if not settings.configured:
        raise ValueError("Donor email delivery is not configured")
    cipher = Fernet(settings.encryption_key.encode())
    now = now or datetime.now(UTC)
    # Commit the credential before sending. A crash retries the same encrypted token;
    # no email ever contains a link whose creation still depends on a later commit.
    with factory.begin() as session:
        job = session.scalar(select(DonorLoginEmailRecord).where(
            DonorLoginEmailRecord.completed_at.is_(None),
            DonorLoginEmailRecord.attempts < 5,
            DonorLoginEmailRecord.available_at <= now,
        ).order_by(DonorLoginEmailRecord.available_at).with_for_update(skip_locked=True).limit(1))
        if job is None:
            return False
        job_id = job.id
        donor = session.get(DonorRecord, job.donor_id)
        if donor.disabled:
            job.completed_at, job.encrypted_token = now, None
            return True
        if job.encrypted_token is None:
            token = issue_login_link(session, donor.email, now=now)
            if token is None:
                job.completed_at = now
                return True
            job.encrypted_token = cipher.encrypt(token.encode())
            job.expires_at = now + LOGIN_LIFETIME
    with factory.begin() as session:
        job = session.scalar(select(DonorLoginEmailRecord).where(
            DonorLoginEmailRecord.id == job_id,
        ).with_for_update())
        if job.completed_at or _utc(job.available_at) > now or job.attempts >= 5:
            return True
        donor = session.get(DonorRecord, job.donor_id)
        if donor.disabled or _utc(job.expires_at) <= now:
            job.completed_at, job.encrypted_token = now, None
            return True
        job.attempts += 1
        try:
            token = cipher.decrypt(job.encrypted_token).decode()
            message = EmailMessage()
            message["From"] = settings.sender
            message["To"] = donor.email
            message["Subject"] = "Sign in to your DAD sandbox account"
            message["Message-ID"] = f"<dad-login-{job.id}@{settings.sender.rsplit('@', 1)[-1]}>"
            message.set_content(
                "Use this link to claim or sign in to your DAD sandbox donor account.\n\n"
                f"{origin}/account/claim#token={token}\n\n"
                "Confirm on the page to continue. This single-use link expires in 15 minutes.\n"
                "If you did not request it, ignore this email. No account access is granted "
                "until you confirm. This is an account access message, not a tax receipt.\n"
            )
            transport.send(message)
        except (OSError, smtplib.SMTPException, InvalidToken, ValueError) as exc:
            job.last_error = type(exc).__name__
            job.available_at = now + timedelta(minutes=1)
        else:
            job.completed_at, job.encrypted_token, job.last_error = now, None, None
    return True


def main():
    from .stripe_payments import PaymentSettings

    settings = EmailSettings.from_env()
    if not settings.configured:
        raise SystemExit("Configure donor email before starting the worker")
    origin = PaymentSettings.from_env().origin
    factory = create_session_factory()
    transport = SMTPTransport(settings)
    while True:
        if not deliver_next(factory, settings, origin, transport):
            time.sleep(2)


if __name__ == "__main__":
    main()
