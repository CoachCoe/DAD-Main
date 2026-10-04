"""Durable sandbox notification outbox; SMTP delivery is at least once."""
import argparse
import smtplib
import time
from datetime import UTC, datetime, timedelta
from email.message import EmailMessage
from uuid import UUID

from sqlalchemy import select

from .database import create_session_factory
from .donor_email import EmailSettings, SMTPTransport
from .donor_receipts import YEAR_ZONE, annual_summary, receipt, render_receipt, render_summary
from .donors import _utc
from .persistence import (
    DonorBillingPeriodRecord,
    DonorGiftRecord,
    DonorMessageRecord,
    DonorMetricProjectionRecord,
    DonorPreferenceRecord,
    DonorRecord,
    DonorSubscriptionRecord,
    StripeCheckoutRecord,
)


def _enqueue(session, donor_id, kind, key, context, available_at):
    if session.scalar(select(DonorMessageRecord.id).where(DonorMessageRecord.dedup_key == key)) is None:
        session.add(DonorMessageRecord(donor_id=donor_id, kind=kind, dedup_key=key,
                                      context=context, available_at=available_at))
        session.flush()


def queue_financial_messages(session, donor_id, *, now=None):
    """Caller holds the donor lock; fact processing and delivery intents commit together."""
    now = now or datetime.now(UTC)
    gifts = session.scalars(select(DonorGiftRecord).where(DonorGiftRecord.donor_id == donor_id,
                                                         DonorGiftRecord.paid_at <= now)).all()
    for gift in gifts:
        _enqueue(session, donor_id, "RECEIPT", f"receipt:{gift.id}", {"gift_id": str(gift.id)}, now)
    year = now.astimezone(YEAR_ZONE).year
    for gift_year in sorted({_utc(gift.paid_at).astimezone(YEAR_ZONE).year for gift in gifts}):
        if 2026 <= gift_year < year:
            _enqueue(session, donor_id, "ANNUAL", f"annual:{donor_id}:{gift_year}", {"year": gift_year}, now)
    projection = session.scalar(select(DonorMetricProjectionRecord).where(DonorMetricProjectionRecord.donor_id == donor_id))
    if projection and projection.referrer_id and projection.net_minor and projection.net_minor > 0:
        _enqueue(session, projection.referrer_id, "REFERRAL", f"referral:{donor_id}",
                 {"referred_id": str(donor_id), "kind": projection.referral_kind}, now)
    for period in session.scalars(select(DonorBillingPeriodRecord).where(
            DonorBillingPeriodRecord.donor_id == donor_id, DonorBillingPeriodRecord.status == "FAILED")):
        for day in (0, 7, 13):
            _enqueue(session, donor_id, "RECOVERY", f"recovery:{period.id}:{day}",
                     {"period_id": str(period.id), "day": day}, _utc(period.due_at) + timedelta(days=day))


def suppression(session, job, now):
    donor = session.get(DonorRecord, job.donor_id)
    if donor.disabled:
        return "donor_disabled"
    if job.kind == "REFERRAL":
        preference = session.scalar(select(DonorPreferenceRecord).where(DonorPreferenceRecord.donor_id == donor.id))
        if not preference or not preference.referral_emails:
            return "opted_out"
        projection = session.scalar(select(DonorMetricProjectionRecord).where(
            DonorMetricProjectionRecord.donor_id == UUID(job.context["referred_id"])))
        if (not projection or projection.referrer_id != donor.id or projection.referral_kind != job.context["kind"]
                or projection.net_minor is None or projection.net_minor <= 0):
            return "referral_no_longer_qualifies"
    if job.kind == "RECOVERY":
        period = session.get(DonorBillingPeriodRecord, UUID(job.context["period_id"]))
        if period.status != "FAILED":
            return "payment_recovered"
        subscription = session.scalar(select(DonorSubscriptionRecord).join(StripeCheckoutRecord,
            StripeCheckoutRecord.id == DonorSubscriptionRecord.attempt_id).where(
            StripeCheckoutRecord.checkout_id == period.checkout_id))
        if subscription and (subscription.stops_at or subscription.status in ("canceled", "incomplete_expired")):
            return "subscription_cancelled"
        elapsed = now - _utc(period.due_at)
        if elapsed >= timedelta(days=14):
            return "grace_ended"
        latest = max(day for day in (0, 7, 13) if elapsed >= timedelta(days=day))
        if job.context["day"] < latest:
            return "newer_reminder_due"
    return None


def render_message(session, job, origin, now):
    if job.kind == "RECEIPT":
        return "Your DAD sandbox gift record", render_receipt(receipt(session, job.donor_id, UUID(job.context["gift_id"]), now=now))
    if job.kind == "ANNUAL":
        return "Your DAD sandbox annual summary", render_summary(annual_summary(session, job.donor_id, job.context["year"], now=now))
    if job.kind == "REFERRAL":
        channel = "IRL" if job.context["kind"] == "IRL" else "Link"
        return f"A {channel} Referral completed a first test gift", (
            f"Someone you invited through an {channel} Referral completed their first test gift.\n"
            f"See your circle's impact: {origin}/account/dashboard\n\n"
            f"Turn off referral notifications: {origin}/account/profile\n"
            "This is a sandbox notification. DAD launches in April 2027.\n")
    if job.kind == "RECOVERY":
        period = session.get(DonorBillingPeriodRecord, UUID(job.context["period_id"]))
        deadline = _utc(period.due_at) + timedelta(days=14)
        return "Your DAD test payment needs attention", (
            "Your monthly test payment was declined. Your giving streak is at risk.\n"
            f"The recovery grace period ends {deadline.isoformat()}.\n"
            f"Update your payment method securely: {origin}/account/payment-method\n"
            "If signed out, request a sign-in link for the email used to give, then return to payment methods.\n"
            "Updating a card does not itself confirm payment recovery. Stripe's retry sequence handles collection.\n"
            "This essential payment notice concerns sandbox giving, not a real charge.\n")
    raise ValueError("Unknown notification type")


def enrolment_template(program_name):
    """Prepared for R2 only; no R1 enrolment endpoint or automatic trigger."""
    return "Enrolment confirmation", (f"Your enrolment in {program_name} has been received.\n"
                                      "This confirms enrolment only. It is not a promise of funds or aid.\n")


def deliver_next(factory, settings, origin, transport, *, now=None):
    if not settings.configured:
        raise ValueError("Donor email delivery is not configured")
    now = now or datetime.now(UTC)
    # Persist the rendered message before external delivery; retries keep its content
    # and Message-ID stable. Recheck suppression immediately before each delivery.
    with factory.begin() as session:
        job = session.scalar(select(DonorMessageRecord).where(DonorMessageRecord.completed_at.is_(None),
            DonorMessageRecord.attempts < 5, DonorMessageRecord.available_at <= now)
            .order_by(DonorMessageRecord.available_at, DonorMessageRecord.id).with_for_update(skip_locked=True).limit(1))
        if job is None:
            return False
        reason = suppression(session, job, now)
        if reason:
            job.completed_at, job.suppressed_reason = now, reason
            return True
        if job.body is None:
            try:
                job.subject, job.body = render_message(session, job, origin, now)
            except (ValueError, LookupError, TypeError) as exc:
                job.attempts += 1
                job.last_error = type(exc).__name__
                job.available_at = now + timedelta(minutes=min(60, 2 ** job.attempts))
                return True
        job_id = job.id
    with factory.begin() as session:
        job = session.scalar(select(DonorMessageRecord).where(DonorMessageRecord.id == job_id).with_for_update())
        if job.completed_at or job.attempts >= 5 or _utc(job.available_at) > now:
            return True
        reason = suppression(session, job, now)
        if reason:
            job.completed_at, job.suppressed_reason = now, reason
            return True
        donor = session.get(DonorRecord, job.donor_id)
        job.attempts += 1
        message = EmailMessage()
        message["From"], message["To"], message["Subject"] = settings.sender, donor.email, job.subject
        message["Message-ID"] = f"<dad-message-{job.id}@{settings.sender.rsplit('@', 1)[-1]}>"
        message.set_content(job.body)
        try:
            transport.send(message)
        except (OSError, smtplib.SMTPException, ValueError) as exc:
            job.last_error = type(exc).__name__
            job.available_at = now + timedelta(minutes=min(60, 2 ** job.attempts))
        else:
            job.completed_at, job.last_error = now, None
    return True


def main():
    from .stripe_payments import PaymentSettings
    parser = argparse.ArgumentParser(description="Deliver queued sandbox donor notifications")
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    settings, factory = EmailSettings.from_env(), create_session_factory()
    origin, transport = PaymentSettings.from_env().origin, SMTPTransport(settings)
    while True:
        worked = deliver_next(factory, settings, origin, transport)
        if args.once:
            return
        if not worked:
            time.sleep(2)


if __name__ == "__main__":
    main()
