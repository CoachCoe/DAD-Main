"""Audited monthly amount changes and period-specific invoice validation."""

from datetime import UTC, datetime, timedelta

from sqlalchemy import select

from .donor_subscriptions import SubscriptionConflict, _attempt, _owner
from .donors import _utc
from .persistence import DonorAmountChangeRecord, DonorCheckoutRecord


def latest_change(session, checkout_id):
    return session.scalar(select(DonorAmountChangeRecord).where(
        DonorAmountChangeRecord.checkout_id == checkout_id).order_by(DonorAmountChangeRecord.created_at.desc()).limit(1))


def next_amount(session, draft):
    change = session.scalar(select(DonorAmountChangeRecord).where(
        DonorAmountChangeRecord.checkout_id == draft.id, DonorAmountChangeRecord.confirmed_at.is_not(None)
    ).order_by(DonorAmountChangeRecord.created_at.desc()).limit(1))
    return change.amount_minor if change else draft.amount_minor


def invoice_amount(session, draft, data):
    lines = data.get("lines", [])
    if not data.get("lines_complete") or len(lines) != 1:
        raise ValueError("Expected a complete monthly invoice")
    start = (lines[0].get("period") or {}).get("start")
    if type(start) is not int:
        raise ValueError("Invoice requires a billing start")
    change = session.scalar(select(DonorAmountChangeRecord).where(
        DonorAmountChangeRecord.checkout_id == draft.id, DonorAmountChangeRecord.confirmed_at.is_not(None),
        DonorAmountChangeRecord.effective_at <= datetime.fromtimestamp(start, UTC),
    ).order_by(DonorAmountChangeRecord.effective_at.desc(), DonorAmountChangeRecord.created_at.desc()).limit(1))
    return change.amount_minor if change else draft.amount_minor


def reconcile_amount(session, draft, item):
    change = latest_change(session, draft.id)
    actual = (item.get("price") or {}).get("unit_amount")
    if change:
        if (item.get("id") != change.item_id or (item.get("price") or {}).get("product") != change.product_id):
            raise ValueError("Recurring item changed")
        if actual == change.amount_minor:
            if change.confirmed_at is None and item.get("current_period_end") != int(_utc(change.effective_at).timestamp()):
                raise ValueError("Amount change crossed a renewal; reconcile the first affected invoice")
            change.confirmed_at = change.confirmed_at or datetime.now(UTC)
            return
        if change.confirmed_at is None and actual == change.previous_minor:
            return
        raise ValueError("Unexpected recurring amount")
    if actual != draft.amount_minor:
        raise ValueError("Unexpected recurring amount")


def _read(session, attempt, gateway):
    from .donor_subscriptions import reconcile_subscription
    remote = gateway.retrieve_subscription(attempt.stripe_subscription_id)
    schedule = reconcile_subscription(session, attempt, gateway, remote=remote)
    if remote.get("schedule") or remote.get("pending_update") or remote.get("pause_collection"):
        raise SubscriptionConflict("Resolve scheduled changes or finish the pause before changing the amount")
    return schedule, remote["items"]["data"][0]


def _view(row):
    return {"request_id": str(row.id), "amount_minor": row.amount_minor, "previous_minor": row.previous_minor,
            "effective_at": _utc(row.effective_at), "confirmed": row.confirmed_at is not None}


def amount_options(factory, gateway, token):
    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id)
        if attempt is None or not attempt.stripe_subscription_id:
            return {"state": "unavailable"}
        schedule, _item = _read(session, attempt, gateway)
        if schedule.status != "active" or schedule.stops_at:
            return {"state": "unavailable"}
        draft = session.get(DonorCheckoutRecord, attempt.checkout_id)
        change = latest_change(session, draft.id)
        return {"state": "ready", "subscription_id": str(attempt.id), "amount_minor": next_amount(session, draft),
                "effective_at": _utc(schedule.period_end), "change": _view(change) if change else None}


def change_amount(factory, gateway, token, *, request_id, attempt_id, amount_minor, previous_minor, effective_at):
    if (request_id.version != 4 or type(amount_minor) is not int or not 100 <= amount_minor <= 99_999_999
            or effective_at.tzinfo is None):
        raise ValueError("Invalid amount confirmation")
    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id, attempt_id)
        if attempt is None or not attempt.stripe_subscription_id:
            raise SubscriptionConflict("No owned monthly gift was found")
        row = session.get(DonorAmountChangeRecord, request_id)
        if row:
            if (row.donor_id != donor.id or row.checkout_id != attempt.checkout_id or row.amount_minor != amount_minor
                    or row.previous_minor != previous_minor or _utc(row.effective_at) != _utc(effective_at)):
                raise SubscriptionConflict("Amount-change key already used")
            if row.confirmed_at:
                return _view(row)
        else:
            schedule, item = _read(session, attempt, gateway)
            draft = session.get(DonorCheckoutRecord, attempt.checkout_id)
            prior = latest_change(session, draft.id)
            if prior and prior.confirmed_at is None:
                raise SubscriptionConflict("An amount change is pending. Refresh its status")
            if (schedule.status != "active" or schedule.stops_at or _utc(schedule.period_end) != _utc(effective_at)
                    or next_amount(session, draft) != previous_minor or amount_minor == previous_minor
                    or datetime.now(UTC) >= _utc(effective_at) - timedelta(minutes=1)):
                raise SubscriptionConflict("Amount or renewal date changed. Refresh and confirm again")
            product, item_id = item["price"].get("product"), item.get("id")
            if not isinstance(product, str) or not product.startswith("prod_") or not isinstance(item_id, str) or not item_id.startswith("si_"):
                raise ValueError("Missing recurring product or item")
            session.add(DonorAmountChangeRecord(id=request_id, donor_id=donor.id, checkout_id=draft.id,
                        amount_minor=amount_minor, previous_minor=previous_minor, effective_at=effective_at,
                        item_id=item_id, product_id=product, created_at=datetime.now(UTC)))
    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id, attempt_id)
        row = session.get(DonorAmountChangeRecord, request_id)
        schedule, _item = _read(session, attempt, gateway)
        if row.confirmed_at is None:
            if (schedule.status != "active" or schedule.stops_at or _utc(schedule.period_end) != _utc(row.effective_at)
                    or datetime.now(UTC) >= _utc(row.effective_at) - timedelta(minutes=1)):
                raise SubscriptionConflict("Renewal changed. Reconcile before retry")
            if datetime.now(UTC) - _utc(row.created_at) >= timedelta(hours=23):
                raise SubscriptionConflict("Amount change requires reconciliation before retry")
            gateway.change_monthly_amount(attempt.stripe_subscription_id, row.item_id, row.product_id,
                                           row.amount_minor, "dad-amount-" + str(row.id))
            _read(session, attempt, gateway)
            if row.confirmed_at is None:
                raise SubscriptionConflict("Amount change is not confirmed. Retry to check its status")
        return _view(row)
