"""Sandbox Stripe boundary: hosted checkout, verified durable inbox, individual gifts.

The HTTP receiver only verifies and persists. A separately runnable worker processes
committed events. Stripe/DB retries share stable identifiers; a redirect never pays a gift.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import time
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from typing import Protocol
from urllib.parse import urlsplit
from uuid import UUID

import stripe
from sqlalchemy import delete, select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.dialects.sqlite import insert as sqlite_insert

from .database import create_session_factory
from .donor_email import queue_login_email
from .donor_financial_facts import (
    invoice_fields,
    invoice_payment_intent,
    reconcile_refund,
    record_period,
)
from .donor_projections import event_donor_id, refresh_projection
from .donors import GiftFrequency, normalize_email, prepare_checkout
from .persistence import (
    DonorCheckoutRecord,
    DonorGiftRecord,
    DonorRecord,
    DonorRecurringReservationRecord,
    StripeCheckoutRecord,
    StripeEventRecord,
)

API_VERSION = "2026-09-30.endive"
MAX_EVENT_ATTEMPTS = 10
EVENT_TYPES = {
    "payment_intent.succeeded", "invoice.payment_succeeded", "invoice.payment_failed",
    "refund.created", "refund.updated", "refund.failed",
    "checkout.session.completed", "checkout.session.expired", "customer.subscription.deleted",
    "customer.subscription.updated",
}


class CheckoutConflict(ValueError):
    pass


class PaymentUnavailable(RuntimeError):
    pass


@dataclass(frozen=True)
class PaymentSettings:
    secret_key: str = field(default="", repr=False)
    webhook_secret: str = field(default="", repr=False)
    origin: str = "http://localhost:3000"

    def __post_init__(self):
        # Live giving is deliberately unavailable until receipts, operational controls,
        # organizational verification, and the production acceptance gate are complete.
        if self.secret_key and not self.secret_key.startswith("sk_test_"):
            raise ValueError("This release accepts only Stripe test keys")
        parts = urlsplit(self.origin)
        if (parts.scheme not in {"http", "https"} or not parts.netloc or parts.username
                or parts.password or parts.path or parts.query or parts.fragment
                or (parts.scheme == "http" and parts.hostname not in {"localhost", "127.0.0.1"})):
            raise ValueError("DONOR_PUBLIC_ORIGIN must be an HTTPS origin (HTTP localhost for tests)")

    @classmethod
    def from_env(cls):
        return cls(os.getenv("STRIPE_SECRET_KEY", ""), os.getenv("STRIPE_WEBHOOK_SECRET", ""),
                   os.getenv("DONOR_PUBLIC_ORIGIN", "http://localhost:3000"))


class CheckoutGateway(Protocol):
    def create_checkout(self, params: dict, key: str) -> dict: ...


class StripeGateway:
    def __init__(self, settings: PaymentSettings):
        if not settings.secret_key:
            raise PaymentUnavailable("Stripe test credentials are not configured")
        self.client = stripe.StripeClient(
            settings.secret_key, stripe_version=API_VERSION, max_network_retries=2,
            http_client=stripe.RequestsClient(timeout=15),
        )

    def retrieve_refund(self, refund_id: str) -> dict:
        return self.client.v1.refunds.retrieve(refund_id).to_dict()

    def retrieve_subscription(self, subscription_id: str) -> dict:
        return self.client.v1.subscriptions.retrieve(subscription_id).to_dict()

    def schedule_cancellation(self, subscription_id: str, cancel_at: int, key: str) -> dict:
        return self.client.v1.subscriptions.update(
            subscription_id, {"cancel_at": cancel_at, "proration_behavior": "none"},
            options={"idempotency_key": key},
        ).to_dict()

    def create_checkout(self, params: dict, key: str) -> dict:
        return self.client.v1.checkout.sessions.create(
            params, options={"idempotency_key": key},
        ).to_dict()


def _insert(session, model):
    return {"postgresql": pg_insert, "sqlite": sqlite_insert}[session.get_bind().dialect.name](model)


def _utc(value):
    return value.replace(tzinfo=UTC) if value.tzinfo is None else value.astimezone(UTC)


def _request_hash(email, amount, frequency, visitor):
    data = [normalize_email(email), amount, GiftFrequency(frequency).value, visitor]
    return hashlib.sha256(json.dumps(data, separators=(",", ":")).encode()).hexdigest()


def begin_checkout(factory, gateway: CheckoutGateway, settings: PaymentSettings, *,
                   request_id: UUID, email: str, amount_minor: int,
                   frequency: GiftFrequency = GiftFrequency.MONTHLY,
                   visitor_token: str | None = None) -> dict:
    """Commit an attempt before contacting Stripe; retry its exact same parameters/key.

    The request UUID is an unguessable capability. A repeat must carry the same input.
    We never attach an existing Stripe customer based only on an unverified email,
    which would expose that customer's saved payment methods to a stranger.
    """
    if request_id.version != 4:
        raise ValueError("Idempotency-Key must be a random UUID v4")
    digest = _request_hash(email, amount_minor, frequency, visitor_token)
    with factory.begin() as session:
        # Serialize requests for the same donor; this also arbitrates simultaneous
        # recurring attempts before either can create a subscription at Stripe.
        from .donors import guest_donor
        donor = guest_donor(session, email)
        session.execute(select(DonorRecord).where(DonorRecord.id == donor.id).with_for_update())
        attempt = session.get(StripeCheckoutRecord, request_id)
        if attempt:
            if attempt.request_hash != digest:
                raise CheckoutConflict("Idempotency key was already used for another request")
            if attempt.status in {"EXPIRED", "CANCELLED", "COMPLETE"}:
                raise CheckoutConflict("This checkout is no longer open")
            if attempt.hosted_url:
                return {"checkoutId": str(attempt.checkout_id), "url": attempt.hosted_url}
            if datetime.now(UTC) - _utc(attempt.created_at) >= timedelta(hours=23):
                raise CheckoutConflict("Checkout requires reconciliation before retry")
        else:
            draft = prepare_checkout(session, email, amount_minor, frequency=frequency,
                                     visitor_token=visitor_token)
            attempt = StripeCheckoutRecord(id=request_id, checkout_id=draft.id,
                                            request_hash=digest, status="PENDING")
            session.add(attempt)
            session.flush()
            if draft.frequency == "MONTHLY":
                held = session.execute(_insert(session, DonorRecurringReservationRecord).values(
                    donor_id=donor.id, attempt_id=attempt.id,
                ).on_conflict_do_nothing(index_elements=["donor_id"])
                    .returning(DonorRecurringReservationRecord.id)).scalar_one_or_none()
                if held is None:
                    raise CheckoutConflict("Monthly giving already has an active or pending checkout")
        draft = session.get(DonorCheckoutRecord, attempt.checkout_id)
        # Everything used for a retry comes from immutable draft records, not newly
        # supplied browser fields or changed attribution cookies.
        metadata = {"dad_checkout_id": str(draft.id)}
        if draft.referrer_id:
            metadata.update(dad_referrer_id=str(draft.referrer_id), dad_referral_kind=draft.referral_kind)
        price = {"currency": "usd", "unit_amount": draft.amount_minor,
                 "product_data": {"name": "DAD monthly giving" if draft.frequency == "MONTHLY"
                                  else "DAD one-time gift"}}
        params = {
            "mode": "subscription" if draft.frequency == "MONTHLY" else "payment",
            "customer_email": donor.email,
            "client_reference_id": str(draft.id), "metadata": metadata,
            "line_items": [{"price_data": price, "quantity": 1}],
            "payment_method_types": ["card"],
            "success_url": settings.origin + "/donate/thanks?session_id={CHECKOUT_SESSION_ID}",
            "cancel_url": settings.origin + "/donate",
        }
        if draft.frequency == "MONTHLY":
            price["recurring"] = {"interval": "month"}
            params["subscription_data"] = {"metadata": metadata}
        else:
            params["payment_intent_data"] = {"metadata": metadata}
        if attempt.request_params:
            params = attempt.request_params
        else:
            attempt.request_params = params
        checkout_id = draft.id
    remote = gateway.create_checkout(params, f"dad-checkout-{request_id}")
    url = urlsplit(remote.get("url", ""))
    if (not str(remote.get("id", "")).startswith("cs_test_") or remote.get("livemode") is not False
            or url.scheme != "https" or url.hostname != "checkout.stripe.com"):
        raise PaymentUnavailable("Stripe returned an unexpected checkout response")
    with factory.begin() as session:
        attempt = session.scalar(select(StripeCheckoutRecord).where(
            StripeCheckoutRecord.id == request_id).with_for_update())
        if attempt.stripe_session_id and attempt.stripe_session_id != remote["id"]:
            raise CheckoutConflict("Stripe session differs from the persisted attempt")
        attempt.stripe_session_id = remote["id"]
        attempt.hosted_url = remote["url"]
        if attempt.status == "PENDING":
            attempt.status = "OPEN"
    return {"checkoutId": str(checkout_id), "url": remote["url"]}


def verify_event(body: bytes, signature: str, settings: PaymentSettings) -> dict:
    if not settings.webhook_secret:
        raise PaymentUnavailable("Stripe webhook verification is not configured")
    event = stripe.Webhook.construct_event(body, signature, settings.webhook_secret, tolerance=300)
    value = event.to_dict()
    if value.get("livemode") is not False or value.get("account"):
        raise ValueError("Only direct-account Stripe test events are accepted")
    if value.get("api_version") != API_VERSION:
        raise ValueError("Stripe webhook API version does not match the configured integration")
    return value


def accept_event(factory, event: dict) -> bool:
    """Only call with a verified event. Persist minimal fields, then acknowledge Stripe."""
    kind = event["type"]
    if kind not in EVENT_TYPES:
        return False
    obj = event["data"]["object"]
    metadata = obj.get("metadata") or {}
    subscription = obj.get("subscription")
    if kind.startswith("invoice."):
        details = (obj.get("parent") or {}).get("subscription_details") or {}
        metadata = details.get("metadata") or {}
        subscription = details.get("subscription")
    checkout_id = metadata.get("dad_checkout_id")
    if not checkout_id and not kind.startswith("refund."):
        return False  # A pre-existing Stripe account can contain unrelated payments.
    if checkout_id:
        UUID(checkout_id)
    data = {"id": obj["id"], "checkout_id": checkout_id, "subscription": subscription}
    for key in ("status", "currency", "amount_received", "amount_paid", "amount_due", "total",
                "billing_reason", "starting_balance", "ending_balance"):
        if key in obj:
            data[key] = obj[key]
    if kind.startswith("invoice."):
        data.update(invoice_fields(obj))
        data["paid_at"] = (obj.get("status_transitions") or {}).get("paid_at")
    if kind.startswith("refund."):
        data = {key: obj.get(key) for key in ("id", "payment_intent", "amount", "currency", "status")}
    with factory.begin() as session:
        session.execute(_insert(session, StripeEventRecord).values(
            stripe_event_id=event["id"], event_type=kind,
            occurred_at=datetime.fromtimestamp(event["created"], UTC), data=data,
        ).on_conflict_do_nothing(index_elements=["stripe_event_id"]))
    return True


def _apply_event(session, event, gateway=None):
    data = event.data
    if event.event_type.startswith("refund."):
        reconcile_refund(session, event, gateway or StripeGateway(PaymentSettings.from_env()))
        return
    attempt = session.scalar(select(StripeCheckoutRecord).where(
        StripeCheckoutRecord.checkout_id == UUID(data["checkout_id"])).with_for_update())
    if attempt is None:
        raise ValueError("Unknown checkout")
    draft = session.get(DonorCheckoutRecord, attempt.checkout_id)
    kind = event.event_type
    if kind.startswith("checkout.session."):
        if attempt.stripe_session_id and attempt.stripe_session_id != data["id"]:
            raise ValueError("Checkout session mismatch")
        attempt.stripe_session_id = data["id"]
        if kind == "checkout.session.expired":
            if data.get("status") != "expired":
                raise ValueError("Expected an expired session")
            if attempt.status not in {"COMPLETE", "CANCELLED"}:
                attempt.status = "EXPIRED"
                session.execute(delete(DonorRecurringReservationRecord).where(
                    DonorRecurringReservationRecord.attempt_id == attempt.id))
        else:
            if data.get("status") != "complete":
                raise ValueError("Expected a completed session")
            if attempt.status != "CANCELLED":
                attempt.status = "COMPLETE"
            if data.get("subscription"):
                _bind_subscription(attempt, data["subscription"])
        return  # Completion/redirect never records a gift.
    if kind.startswith("customer.subscription."):
        from .donor_subscriptions import reconcile_subscription

        _bind_subscription(attempt, data["id"])
        reconcile_subscription(session, attempt, gateway or StripeGateway(PaymentSettings.from_env()))
        return
    if kind == "invoice.payment_failed":
        if (draft.frequency != "MONTHLY" or data.get("status") != "open"
                or data.get("currency") != "usd" or data.get("amount_due") != draft.amount_minor
                or data.get("billing_reason") not in {"subscription_create", "subscription_cycle"}):
            raise ValueError("Unexpected failed recurring invoice")
        _bind_subscription(attempt, data.get("subscription"))
        record_period(session, draft, data, paid=False)
        return
    period = None
    if kind == "payment_intent.succeeded":
        if draft.frequency != "ONE_TIME" or data.get("status") != "succeeded":
            raise ValueError("Unexpected PaymentIntent")
        amount = data.get("amount_received")
        paid_at = event.occurred_at
        payment_intent = data["id"]
    else:
        if (draft.frequency != "MONTHLY" or data.get("status") != "paid"
                or data.get("billing_reason") not in {"subscription_create", "subscription_cycle"}
                or data.get("starting_balance", 0) != 0 or data.get("ending_balance", 0) != 0
                or data.get("amount_paid") != data.get("total")
                or data.get("amount_paid") != data.get("amount_due")):
            raise ValueError("Invoice is not an unadjusted paid recurring donation")
        _bind_subscription(attempt, data.get("subscription"))
        amount = data.get("amount_paid")
        paid_at = datetime.fromtimestamp(data["paid_at"], UTC)
        payment_intent = invoice_payment_intent(data, amount)
        period = record_period(session, draft, data, paid=True)
    if (type(amount) is not int or amount != draft.amount_minor or data.get("currency") != "usd"):
        raise ValueError("Payment amount or currency differs from checkout")
    donor = session.scalar(select(DonorRecord).where(DonorRecord.id == draft.donor_id)
                           .with_for_update().execution_options(populate_existing=True))
    had_gift = session.scalar(select(DonorGiftRecord.id).where(
        DonorGiftRecord.donor_id == donor.id).limit(1)) is not None
    session.execute(_insert(session, DonorGiftRecord).values(
        donor_id=draft.donor_id, checkout_id=draft.id, stripe_object_id=data["id"],
        amount_minor=amount, currency="USD", frequency=draft.frequency, paid_at=paid_at,
        stripe_payment_intent_id=payment_intent, period_id=period.id if period else None,
    ).on_conflict_do_nothing(index_elements=["stripe_object_id"]))
    gift = session.scalar(select(DonorGiftRecord).where(
        DonorGiftRecord.stripe_object_id == data["id"]).with_for_update())
    if (gift.donor_id != draft.donor_id or gift.checkout_id != draft.id
            or gift.amount_minor != amount or gift.frequency != draft.frequency
            or gift.stripe_payment_intent_id not in {None, payment_intent}
            or gift.period_id not in {None, period.id if period else None}):
        raise ValueError("Payment conflicts with an existing gift")
    # Re-delivery with complete verified invoice fields can enrich legacy sandbox rows.
    gift.stripe_payment_intent_id = payment_intent
    gift.period_id = period.id if period else None
    if not had_gift and donor.claimed_at is None:
        queue_login_email(session, donor.email)


def _bind_subscription(attempt, subscription):
    if not isinstance(subscription, str) or not subscription.startswith("sub_"):
        raise ValueError("Missing subscription")
    if attempt.stripe_subscription_id and attempt.stripe_subscription_id != subscription:
        raise ValueError("Subscription mismatch")
    attempt.stripe_subscription_id = subscription


def process_next_event(factory, *, gateway=None) -> bool:
    with factory.begin() as session:
        event = session.scalar(select(StripeEventRecord).where(
            StripeEventRecord.processed_at.is_(None), StripeEventRecord.attempts < MAX_EVENT_ATTEMPTS,
        ).order_by(StripeEventRecord.created_at, StripeEventRecord.id)
            .with_for_update(skip_locked=True).limit(1))
        if event is None:
            return False
        event.attempts += 1
        try:
            with session.begin_nested():
                donor_id = event_donor_id(session, event)
                if donor_id is not None:
                    session.execute(select(DonorRecord).where(
                        DonorRecord.id == donor_id).with_for_update())
                _apply_event(session, event, gateway)
                session.flush()
                if donor_id is not None:
                    refresh_projection(session, donor_id)
        except Exception as exc:  # noqa: BLE001 -- preserve failed inbox work for explicit replay
            event.last_error = type(exc).__name__
        else:
            event.processed_at = datetime.now(UTC)
            event.last_error = None
    return True


def main():
    parser = argparse.ArgumentParser(description="Process the donor Stripe inbox")
    parser.add_argument("--once", action="store_true")
    parser.add_argument("--retry-event", help="Reset the attempt budget of a failed event")
    args = parser.parse_args()
    PaymentSettings.from_env()  # Refuse accidental live credentials here too.
    factory = create_session_factory()
    if args.retry_event:
        with factory.begin() as session:
            event = session.scalar(select(StripeEventRecord).where(
                StripeEventRecord.stripe_event_id == args.retry_event).with_for_update())
            if event is None or event.processed_at is not None:
                raise ValueError("Expected an unprocessed event")
            event.attempts = 0
            event.last_error = None
    while True:
        worked = process_next_event(factory)
        if args.once:
            break
        if not worked:
            time.sleep(2)


if __name__ == "__main__":
    main()
