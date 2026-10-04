"""Staff financial commands: authorize durably, observe Stripe, then update money facts."""
from datetime import UTC, datetime, timedelta
from uuid import UUID

from sqlalchemy import select

from . import donor_staff as staff
from .donor_financial_facts import record_refund
from .donor_metrics import RefundStatus
from .donor_projections import refresh_projection
from .donor_subscriptions import _attempt, _view, cancel_authorized, reconcile_subscription
from .donors import _utc
from .persistence import (
    DonorCancellationRecord,
    DonorGiftRecord,
    DonorRecord,
    DonorRefundRecord,
    DonorStaffActionRecord,
)


def _owner(session, token, donor_id, permission):
    actor = staff.require_staff(session, token, permission)
    donor = session.scalar(select(DonorRecord).where(DonorRecord.id == donor_id).with_for_update())
    if donor is None:
        raise LookupError("Donor not found")
    return actor, donor


def _gift(session, donor_id, gift_id):
    gift = session.scalar(select(DonorGiftRecord).where(DonorGiftRecord.id == gift_id,
                            DonorGiftRecord.donor_id == donor_id).with_for_update())
    if gift is None:
        raise LookupError("Gift not found")
    if not gift.stripe_payment_intent_id:
        raise ValueError("Gift requires payment reconciliation")
    return gift


def _remote_refunds(gateway, gift):
    rows = gateway.list_payment_refunds(gift.stripe_payment_intent_id)
    ids = set()
    for row in rows:
        if (not isinstance(row.get("id"), str) or not row["id"].startswith("re_") or row["id"] in ids
                or row.get("payment_intent") != gift.stripe_payment_intent_id or row.get("currency") != "usd"
                or type(row.get("amount")) is not int or row["amount"] <= 0):
            raise ValueError("Refund history requires reconciliation")
        ids.add(row["id"])
        RefundStatus(row.get("status", "").upper())
    remaining = gift.amount_minor - sum(row["amount"] for row in rows if row["status"] in ("succeeded", "pending", "requires_action"))
    if remaining < 0:
        raise ValueError("Refunds exceed the gift")
    return rows, remaining


def refund_quote(factory, gateway, token, donor_id, gift_id):
    with factory.begin() as session:
        actor, _ = _owner(session, token, donor_id, "gift.refund")
        gift = _gift(session, donor_id, gift_id)
        refunds, remaining = _remote_refunds(gateway, gift)
        for current in refunds:
            command_id = (current.get("metadata") or {}).get("dad_staff_action")
            try:
                command = session.get(DonorStaffActionRecord, UUID(command_id)) if command_id else None
            except (ValueError, TypeError):
                command = None
            if command and command.kind == "REFUND" and command.target_id == gift.id and command.amount_minor == current["amount"]:
                previous = session.scalar(select(DonorRefundRecord).where(DonorRefundRecord.stripe_refund_id == current["id"]))
                if previous is None or previous.status != current["status"].upper():
                    observed = record_refund(session, gift, current, action_id=command.id)
                    command.stripe_refund_id, command.completed_at, command.status = observed.stripe_refund_id, datetime.now(UTC), "OBSERVED"
                    staff.audit(session, actor.id, "gift.refund_reconciled", command.id, status=observed.status)
        refresh_projection(session, donor_id)
        staff.audit(session, actor.id, "gift.refund_preview", gift.id)
        return {"gift_id": str(gift.id), "donor_id": str(donor_id), "amount_minor": gift.amount_minor,
                "available_minor": remaining, "currency": "USD", "mode": "test"}


def cancellation_quote(factory, gateway, token, donor_id):
    with factory.begin() as session:
        actor, donor = _owner(session, token, donor_id, "gift.cancel")
        attempt = _attempt(session, donor.id)
        if attempt is None or not attempt.stripe_subscription_id:
            return {"subscription": None, "mode": "test"}
        schedule = reconcile_subscription(session, attempt, gateway)
        staff.audit(session, actor.id, "gift.cancel_preview", attempt.id)
        return _view(session, attempt, schedule)


def _stage(factory, token, donor_id, request_id, kind, target_id, reason, *, amount_minor=None, period_end=None):
    if request_id.version != 4 or not 5 <= len(reason.strip()) <= 500:
        raise ValueError("Confirm a reason and a UUID v4 request key")
    if kind == "REFUND" and (type(amount_minor) is not int or not 1 <= amount_minor <= 99_999_999):
        raise ValueError("Refund requires positive integer cents")
    if kind == "CANCEL" and (period_end is None or period_end.tzinfo is None):
        raise ValueError("Cancellation requires an exact renewal date")
    with factory.begin() as session:
        actor, donor = _owner(session, token, donor_id, "gift.refund" if kind == "REFUND" else "gift.cancel")
        row = session.get(DonorStaffActionRecord, request_id)
        if row:
            if (row.actor_id != actor.id or row.donor_id != donor_id or row.kind != kind or row.target_id != target_id
                    or row.reason != reason.strip() or row.amount_minor != amount_minor
                    or (_utc(row.period_end) if row.period_end else None) != (_utc(period_end) if period_end else None)):
                raise ValueError("Financial action key already used")
            return
        if kind == "REFUND":
            gift = _gift(session, donor_id, target_id)
            if amount_minor > gift.amount_minor:
                raise ValueError("Refund exceeds the original gift")
            for pending in session.scalars(select(DonorStaffActionRecord).where(
                    DonorStaffActionRecord.target_id == target_id, DonorStaffActionRecord.kind == "REFUND")):
                refund = session.scalar(select(DonorRefundRecord).where(DonorRefundRecord.stripe_refund_id == pending.stripe_refund_id)) if pending.stripe_refund_id else None
                if pending.status != "ABANDONED" and (refund is None or refund.status in ("PENDING", "REQUIRES_ACTION")):
                    raise ValueError("Another refund requires reconciliation before a new request")
        else:
            attempt = _attempt(session, donor.id, target_id)
            if attempt is None or not attempt.stripe_subscription_id:
                raise LookupError("Subscription not found")
            if session.get(DonorCancellationRecord, request_id):
                raise ValueError("Cancellation key already used")
        session.add(DonorStaffActionRecord(id=request_id, actor_id=actor.id, donor_id=donor_id, kind=kind,
            target_id=target_id, reason=reason.strip(), amount_minor=amount_minor, period_end=period_end,
            created_at=datetime.now(UTC)))
        staff.audit(session, actor.id, "gift.action_authorized", request_id, kind=kind, target_id=str(target_id))


def refund_gift(factory, gateway, token, *, donor_id, gift_id, request_id, amount_minor, reason):
    _stage(factory, token, donor_id, request_id, "REFUND", gift_id, reason, amount_minor=amount_minor)
    with factory.begin() as session:
        actor, donor = _owner(session, token, donor_id, "gift.refund")
        command = session.get(DonorStaffActionRecord, request_id)
        gift = _gift(session, donor_id, gift_id)
        if command.status == "ABANDONED":
            return {"request_id": str(command.id), "status": "NOT_SUBMITTED", "mode": "test"}
        refunds, remaining = _remote_refunds(gateway, gift)
        matching = [row for row in refunds if (row.get("metadata") or {}).get("dad_staff_action") == str(command.id)]
        if len(matching) > 1:
            raise ValueError("Duplicate processor refunds require reconciliation")
        if matching:
            current = matching[0]
        else:
            if command.stripe_refund_id or datetime.now(UTC) - _utc(command.created_at) >= timedelta(hours=23):
                raise ValueError("Refund requires reconciliation before retry")
            if amount_minor > remaining:
                command.status, command.completed_at = "ABANDONED", datetime.now(UTC)
                staff.audit(session, actor.id, "gift.refund_not_submitted", command.id, reason="available_amount_changed")
                return {"request_id": str(command.id), "status": "NOT_SUBMITTED", "mode": "test"}
            current = gateway.create_refund(gift.stripe_payment_intent_id, amount_minor, str(command.id))
        if (current.get("amount") != amount_minor or (current.get("metadata") or {}).get("dad_staff_action") != str(command.id)
                or (command.stripe_refund_id and command.stripe_refund_id != current.get("id"))):
            raise ValueError("Processor refund does not match the authorized command")
        refund = record_refund(session, gift, current, action_id=command.id)
        command.stripe_refund_id, command.completed_at = refund.stripe_refund_id, datetime.now(UTC)
        command.status = "OBSERVED"
        staff.audit(session, actor.id, "gift.refund_observed", command.id, status=refund.status, amount_minor=refund.amount_minor)
        refresh_projection(session, donor.id)
        return {"request_id": str(command.id), "refund_id": str(refund.id), "status": refund.status,
                "amount_minor": refund.amount_minor, "currency": "USD", "mode": "test"}


def cancel_giving(factory, gateway, token, *, donor_id, attempt_id, request_id, period_end, reason):
    _stage(factory, token, donor_id, request_id, "CANCEL", attempt_id, reason, period_end=period_end)
    def resolve_owner(session):
        _, donor = _owner(session, token, donor_id, "gift.cancel")
        command = session.get(DonorStaffActionRecord, request_id)
        if command.status == "ABANDONED":
            raise ValueError("This command was abandoned; confirm a new request")
        return donor
    result = cancel_authorized(factory, gateway, resolve_owner, request_id=request_id,
                               attempt_id=attempt_id, period_end=period_end)
    with factory.begin() as session:
        actor, _ = _owner(session, token, donor_id, "gift.cancel")
        command = session.get(DonorStaffActionRecord, request_id)
        if result["subscription"]["stops_at"] is None:
            raise ValueError("Cancellation requires reconciliation")
        if command.completed_at is None:
            command.completed_at, command.status = datetime.now(UTC), "OBSERVED"
            staff.audit(session, actor.id, "gift.cancellation_confirmed", command.id)
    return {"request_id": str(request_id), **result}
