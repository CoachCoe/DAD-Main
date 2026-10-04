"""Hosted card setup followed by a verified, idempotent subscription update."""

from datetime import UTC, datetime, timedelta
from urllib.parse import urlsplit
from uuid import UUID

from sqlalchemy import select

from .donor_subscriptions import SubscriptionConflict, _attempt, _owner
from .donors import _utc, authenticated_donor
from .persistence import DonorCheckoutRecord, DonorPaymentUpdateRecord, StripeCheckoutRecord

TERMINAL = {"APPLIED", "EXPIRED", "CANCELLED"}


def _subscription(session, attempt, gateway):
    if attempt is None or not attempt.stripe_subscription_id:
        raise SubscriptionConflict("No confirmed monthly gift was found")
    draft = session.get(DonorCheckoutRecord, attempt.checkout_id)
    remote = gateway.retrieve_subscription(attempt.stripe_subscription_id)
    if (remote.get("livemode") is not False or remote.get("id") != attempt.stripe_subscription_id
            or (remote.get("metadata") or {}).get("dad_checkout_id") != str(draft.id)
            or not isinstance(remote.get("customer"), str) or not remote["customer"].startswith("cus_")):
        raise ValueError("Subscription ownership mismatch")
    return remote


def payment_method_status(factory, token):
    with factory() as session:
        donor = authenticated_donor(session, token)
        row = session.scalar(select(DonorPaymentUpdateRecord).where(
            DonorPaymentUpdateRecord.donor_id == donor.id).order_by(
                DonorPaymentUpdateRecord.created_at.desc(), DonorPaymentUpdateRecord.id.desc()).limit(1))
        return {"mode": "test", "status": row.status if row else "NONE",
                "completed_at": row.completed_at if row else None}


def begin_payment_update(factory, gateway, settings, token, *, request_id):
    if request_id.version != 4:
        raise ValueError("Idempotency-Key must be a random UUID v4")
    with factory.begin() as session:
        donor = _owner(session, token)
        row = session.get(DonorPaymentUpdateRecord, request_id)
        if row and row.donor_id != donor.id:
            raise SubscriptionConflict("Payment update key already used")
        if row and row.status in TERMINAL:
            raise SubscriptionConflict("This payment update is finished. Refresh to start another")
        if row is None:
            # One unfinished update per donor. Two devices must not race to replace
            # the subscription default with different cards in webhook arrival order.
            row = session.scalar(select(DonorPaymentUpdateRecord).where(
                DonorPaymentUpdateRecord.donor_id == donor.id,
                DonorPaymentUpdateRecord.status.not_in(TERMINAL)).limit(1))
            if row and row.stripe_session_id:
                reconcile_payment_update(session, row, gateway)
                if row.status in {"APPLIED", "CANCELLED"}:
                    return {"url": None, "status": row.status, "completed_at": row.completed_at}
                if row.status == "EXPIRED":
                    row = None
        if row is None:
            attempt = _attempt(session, donor.id)
            remote = _subscription(session, attempt, gateway)
            if remote.get("status") not in {"active", "past_due", "unpaid"}:
                raise SubscriptionConflict("This monthly gift cannot accept a payment update")
            metadata = {"dad_payment_update_id": str(request_id)}
            params = {"mode": "setup", "customer": remote["customer"], "payment_method_types": ["card"],
                      "metadata": metadata, "setup_intent_data": {"metadata": metadata},
                      "success_url": settings.origin + "/account/payment-method",
                      "cancel_url": settings.origin + "/account/payment-method",
                      "expires_at": int((datetime.now(UTC) + timedelta(hours=1)).timestamp())}
            row = DonorPaymentUpdateRecord(id=request_id, donor_id=donor.id, attempt_id=attempt.id,
                                            customer_id=remote["customer"], request_params=params, status="PENDING",
                                            created_at=datetime.now(UTC))
            session.add(row)
        update_id = row.id
    with factory.begin() as session:
        _owner(session, token)
        row = session.get(DonorPaymentUpdateRecord, update_id)
        if row.status in TERMINAL:
            raise SubscriptionConflict("This payment update is finished. Refresh its status")
        if row.hosted_url:
            return {"url": row.hosted_url}
        # Stripe requires expiry at least 30 minutes ahead. Never recreate uncertain
        # sessions with changed parameters or an expired idempotency key.
        if datetime.now(UTC).timestamp() >= row.request_params["expires_at"] - 1800:
            raise SubscriptionConflict("Payment update requires reconciliation before retry")
        remote = gateway.create_checkout(row.request_params, "dad-payment-setup-" + str(row.id))
        parts = urlsplit(remote.get("url", ""))
        if (remote.get("livemode") is not False or not remote.get("id", "").startswith("cs_test_")
                or parts.scheme != "https" or parts.netloc != "checkout.stripe.com"):
            raise ValueError("Unexpected hosted setup response")
        if row.stripe_session_id and row.stripe_session_id != remote["id"]:
            raise ValueError("Setup session mismatch")
        row.stripe_session_id, row.hosted_url, row.status = remote["id"], remote["url"], "OPEN"
        return {"url": row.hosted_url}


def reconcile_payment_update(session, row, gateway, *, session_id=None):
    """Caller holds the donor lock. A completion URL is never evidence of success."""
    if row.status in TERMINAL:
        return
    identifier = session_id or row.stripe_session_id
    if not identifier or (row.stripe_session_id and row.stripe_session_id != identifier):
        raise ValueError("Unknown setup session")
    remote = gateway.retrieve_checkout(identifier)
    if (remote.get("id") != identifier or remote.get("livemode") is not False
            or remote.get("mode") != "setup" or remote.get("customer") != row.customer_id
            or (remote.get("metadata") or {}).get("dad_payment_update_id") != str(row.id)):
        raise ValueError("Setup session ownership mismatch")
    row.stripe_session_id = identifier
    if remote.get("status") == "expired":
        row.status, row.hosted_url = "EXPIRED", None
        return
    if remote.get("status") == "open":
        return
    if remote.get("status") != "complete":
        raise ValueError("Setup session has not completed")
    setup_id = remote.get("setup_intent")
    if not isinstance(setup_id, str) or not setup_id.startswith("seti_"):
        raise ValueError("Missing SetupIntent")
    setup = gateway.retrieve_setup_intent(setup_id)
    payment_method = setup.get("payment_method")
    if (setup.get("id") != setup_id or setup.get("livemode") is not False
            or setup.get("customer") != row.customer_id or setup.get("status") != "succeeded"
            or setup.get("usage") != "off_session" or setup.get("payment_method_types") != ["card"]
            or (setup.get("metadata") or {}).get("dad_payment_update_id") != str(row.id)
            or not isinstance(payment_method, str) or not payment_method.startswith("pm_")):
        raise ValueError("SetupIntent is not a verified card setup for this donor")
    attempt = session.get(StripeCheckoutRecord, row.attempt_id)
    subscription = _subscription(session, attempt, gateway)
    if subscription["customer"] != row.customer_id:
        raise ValueError("Subscription customer changed")
    if subscription.get("status") in {"canceled", "incomplete_expired"}:
        row.status, row.hosted_url = "CANCELLED", None
        return
    if subscription.get("status") not in {"active", "past_due", "unpaid"}:
        raise SubscriptionConflict("Subscription cannot accept the update yet")
    if subscription.get("default_payment_method") != payment_method:
        if datetime.now(UTC) - _utc(row.created_at) >= timedelta(hours=23):
            raise SubscriptionConflict("Payment update requires reconciliation before retry")
        gateway.update_subscription_payment_method(attempt.stripe_subscription_id, payment_method,
                                                   "dad-payment-apply-" + str(row.id))
        confirmed = _subscription(session, attempt, gateway)
        if confirmed.get("default_payment_method") != payment_method:
            raise SubscriptionConflict("Payment method change has not been confirmed")
    row.status, row.completed_at, row.hosted_url = "APPLIED", datetime.now(UTC), None


def apply_payment_update_event(session, event, gateway):
    row = session.get(DonorPaymentUpdateRecord, UUID(event.data["payment_update_id"]))
    if row is None:
        raise ValueError("Unknown payment update")
    reconcile_payment_update(session, row, gateway, session_id=event.data["id"])
    if row.status not in TERMINAL:
        raise SubscriptionConflict("Stripe setup is not ready; retain the event for retry")
