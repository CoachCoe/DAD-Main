"""Donor-owned recurring cancellation. Card details and refunds are outside this boundary."""

from datetime import UTC, datetime, timedelta

from sqlalchemy import delete, select

from .donors import _utc, authenticated_donor
from .persistence import (
    DonorCancellationRecord,
    DonorCheckoutRecord,
    DonorRecord,
    DonorRecurringReservationRecord,
    DonorSubscriptionRecord,
    StripeCheckoutRecord,
)


class SubscriptionConflict(ValueError):
    pass


def reconcile_subscription(session, attempt, gateway, *, remote=None):
    """Read current Stripe state under the donor lock; event arrival order is irrelevant."""
    draft = session.get(DonorCheckoutRecord, attempt.checkout_id)
    remote = remote if remote is not None else gateway.retrieve_subscription(attempt.stripe_subscription_id)
    items = remote.get("items") or {}
    lines = items.get("data") or []
    if (draft.frequency != "MONTHLY" or remote.get("id") != attempt.stripe_subscription_id
            or remote.get("livemode") is not False
            or (remote.get("metadata") or {}).get("dad_checkout_id") != str(draft.id)
            or items.get("has_more") is not False or len(lines) != 1):
        raise ValueError("Subscription ownership or shape mismatch")
    item = lines[0]
    price = item.get("price") or {}
    if (price.get("currency") != "usd" or price.get("unit_amount") != draft.amount_minor
            or (price.get("recurring") or {}).get("interval") != "month"
            or (price.get("recurring") or {}).get("interval_count") != 1
            or item.get("quantity") != 1):
        raise ValueError("Unexpected recurring price")
    start, end = item.get("current_period_start"), item.get("current_period_end")
    if (type(start) is not int or type(end) is not int or not 27 * 86400 <= end - start <= 32 * 86400):
        raise ValueError("Missing monthly subscription schedule")
    status = remote.get("status")
    if status not in {"active", "past_due", "unpaid", "canceled", "incomplete", "incomplete_expired"}:
        raise ValueError("Unsupported subscription state")
    cancel = remote.get("cancel_at_period_end")
    if type(cancel) is not bool:
        raise ValueError("Unsupported subscription schedule")
    if remote.get("cancel_at") not in {None, end}:
        raise ValueError("Cancellation must occur at the monthly boundary")
    from .donor_pauses import reconcile_pause
    reconcile_pause(session, attempt, remote)
    row = session.scalar(select(DonorSubscriptionRecord).where(
        DonorSubscriptionRecord.attempt_id == attempt.id))
    if row is None:
        row = DonorSubscriptionRecord(attempt_id=attempt.id, donor_id=draft.donor_id)
        session.add(row)
    row.status = status
    row.period_end = datetime.fromtimestamp(end, UTC)
    # An immediate processor cancellation does not erase a paid month either.
    row.stops_at = row.period_end if cancel or remote.get("cancel_at") == end or status in {"canceled", "incomplete_expired"} else None
    row.observed_at = datetime.now(UTC)
    if status in {"canceled", "incomplete_expired"}:
        attempt.status = "CANCELLED"
        session.execute(delete(DonorRecurringReservationRecord).where(
            DonorRecurringReservationRecord.attempt_id == attempt.id))
    session.flush()
    return row


def _owner(session, token):
    donor = authenticated_donor(session, token)
    session.execute(select(DonorRecord).where(DonorRecord.id == donor.id).with_for_update())
    return donor


def _attempt(session, donor_id, attempt_id=None):
    query = select(StripeCheckoutRecord).join(
        DonorCheckoutRecord, DonorCheckoutRecord.id == StripeCheckoutRecord.checkout_id,
    ).where(DonorCheckoutRecord.donor_id == donor_id, DonorCheckoutRecord.frequency == "MONTHLY")
    if attempt_id:
        query = query.where(StripeCheckoutRecord.id == attempt_id)
    else:
        query = query.where(StripeCheckoutRecord.status != "EXPIRED").order_by(
            StripeCheckoutRecord.created_at.desc(), StripeCheckoutRecord.id.desc())
    return session.scalar(query.limit(1).with_for_update(of=StripeCheckoutRecord))


def _view(session, attempt, row):
    draft = session.get(DonorCheckoutRecord, attempt.checkout_id)
    return {"mode": "test", "state": "ready", "subscription": {
        "id": str(attempt.id), "amount_minor": draft.amount_minor, "currency": "USD",
        "status": row.status, "period_end": row.period_end, "stops_at": row.stops_at,
    }}


def recurring_details(factory, gateway, token):
    from .donor_projections import refresh_projection

    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id)
        if attempt is None:
            return {"mode": "test", "state": "ready", "subscription": None}
        if not attempt.stripe_subscription_id:
            return {"mode": "test", "state": "preparing", "subscription": None}
        row = reconcile_subscription(session, attempt, gateway)
        refresh_projection(session, donor.id)
        return _view(session, attempt, row)


def cancel_recurring(factory, gateway, token, *, request_id, attempt_id, period_end):
    from .donor_projections import refresh_projection

    if request_id.version != 4:
        raise ValueError("Idempotency-Key must be a random UUID v4")
    if period_end.tzinfo is None:
        raise ValueError("Confirmation requires a time zone")
    # Commit authorization before any external mutation. A crash leaves a retryable,
    # auditable command rather than an unexplained change at the processor.
    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id, attempt_id)
        if attempt is None or not attempt.stripe_subscription_id:
            raise SubscriptionConflict("No confirmed monthly gift was found")
        action = session.get(DonorCancellationRecord, request_id)
        if action:
            if (action.donor_id != donor.id or action.attempt_id != attempt.id
                    or _utc(action.confirmed_period_end) != _utc(period_end)):
                raise SubscriptionConflict("Cancellation key already used")
        else:
            session.add(DonorCancellationRecord(id=request_id, donor_id=donor.id,
                                                attempt_id=attempt.id, confirmed_period_end=period_end))
    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id, attempt_id)
        action = session.get(DonorCancellationRecord, request_id)
        row = reconcile_subscription(session, attempt, gateway)
        if action.completed_at is None:
            if row.stops_at is None:
                if (_utc(row.period_end) != _utc(period_end)
                        or datetime.now(UTC) >= _utc(period_end)):
                    raise SubscriptionConflict("Billing date changed. Refresh and confirm again")
                if datetime.now(UTC) - _utc(action.created_at) >= timedelta(hours=23):
                    raise SubscriptionConflict("Cancellation requires reconciliation before retry")
                if row.status not in {"active", "past_due", "unpaid"}:
                    raise SubscriptionConflict("This monthly gift cannot be cancelled here yet")
                gateway.schedule_cancellation(attempt.stripe_subscription_id, int(_utc(period_end).timestamp()),
                                              "dad-cancel-" + str(action.id))
                row = reconcile_subscription(session, attempt, gateway)
                if row.stops_at is None:
                    raise SubscriptionConflict("Cancellation is not confirmed. Retry to check its status")
            action.completed_at = datetime.now(UTC)
        refresh_projection(session, donor.id)
        return _view(session, attempt, row)
