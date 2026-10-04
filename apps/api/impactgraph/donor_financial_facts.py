"""Translate verified Stripe facts to persistent, auditable donor-domain inputs."""

from datetime import UTC, datetime

from sqlalchemy import func, select

from .donor_metrics import BillingPeriod, Gift, ReferralCandidate, Refund, RefundStatus
from .donors import _utc
from .persistence import (
    DonorBillingPeriodRecord,
    DonorCheckoutRecord,
    DonorGiftRecord,
    DonorRefundObservationRecord,
    DonorRefundRecord,
)


def invoice_fields(obj):
    """Allowlist only; no customer, card, address, free-form description, or metadata."""
    lines = obj.get("lines") or {}
    payments = obj.get("payments") or {}
    return {
        "lines_complete": lines.get("has_more") is False,
        "lines": [{"amount": line.get("amount"), "currency": line.get("currency"),
                   "period": {key: (line.get("period") or {}).get(key) for key in ("start", "end")},
                   "parent_type": (line.get("parent") or {}).get("type"),
                   "subscription": ((line.get("parent") or {}).get("subscription_item_details")
                                    or {}).get("subscription"),
                   "proration": ((line.get("parent") or {}).get("subscription_item_details")
                                 or {}).get("proration")}
                  for line in lines.get("data", [])],
        "payments_complete": payments.get("has_more") is False,
        "payments": [{"status": p.get("status"), "amount_paid": p.get("amount_paid"),
                      "currency": p.get("currency"), "invoice": p.get("invoice"),
                      "livemode": p.get("livemode"),
                      "type": (p.get("payment") or {}).get("type"),
                      "payment_intent": (p.get("payment") or {}).get("payment_intent")}
                     for p in payments.get("data", [])],
    }


def record_period(session, draft, data, *, paid):
    from .donor_amounts import invoice_amount
    expected_amount = invoice_amount(session, draft, data)
    if not data.get("lines_complete") or len(data.get("lines", [])) != 1:
        raise ValueError("Expected the complete single-line monthly invoice")
    line = data["lines"][0]
    if (line["parent_type"] != "subscription_item_details" or line["proration"] is not False
            or line["subscription"] != data["subscription"] or line["currency"] != "usd"
            or type(line["amount"]) is not int or line["amount"] != expected_amount):
        raise ValueError("Invoice line does not match the recurring gift")
    bounds = line["period"] or {}
    if type(bounds.get("start")) is not int or type(bounds.get("end")) is not int:
        raise ValueError("Missing billing-period bounds")
    due = datetime.fromtimestamp(bounds["start"], UTC)
    end = datetime.fromtimestamp(bounds["end"], UTC)
    BillingPeriod(data["id"], str(draft.donor_id), due, end)  # Monthly-duration validation.
    period = session.scalar(select(DonorBillingPeriodRecord).where(
        DonorBillingPeriodRecord.stripe_invoice_id == data["id"]))
    if period is None:
        period = DonorBillingPeriodRecord(donor_id=draft.donor_id, checkout_id=draft.id,
                                         stripe_invoice_id=data["id"],
                                         stripe_subscription_id=data["subscription"],
                                         due_at=due, ends_at=end, status="FAILED")
        session.add(period)
    elif (period.checkout_id != draft.id or period.stripe_subscription_id != data["subscription"]
          or _utc(period.due_at) != due or _utc(period.ends_at) != end):
        raise ValueError("Invoice changed its billing period or owner")
    # A delayed failure must not undo a confirmed payment.
    if paid:
        period.status = "PAID"
    session.flush()
    return period


def invoice_payment_intent(data, amount):
    if not data.get("payments_complete") or len(data.get("payments", [])) != 1:
        raise ValueError("Expected one complete invoice payment; reconcile split/offline payments")
    payment = data["payments"][0]
    if (payment["status"] != "paid" or type(payment["amount_paid"]) is not int
            or payment["amount_paid"] != amount or payment["currency"] != "usd"
            or payment["invoice"] != data["id"] or payment["livemode"] is not False
            or payment["type"] != "payment_intent"
            or not isinstance(payment["payment_intent"], str)
            or not payment["payment_intent"].startswith("pi_")):
        raise ValueError("Invoice payment is not a matching test PaymentIntent")
    return payment["payment_intent"]


def reconcile_refund(session, event, gateway):
    data = event.data
    gift = session.scalar(select(DonorGiftRecord).where(
        DonorGiftRecord.stripe_payment_intent_id == data.get("payment_intent"),
    ).with_for_update()) if data.get("payment_intent") else None
    if gift is None:
        raise ValueError("Refund payment is not yet linked to a donor gift")
    # Serialize retrieval under the gift lock. Webhook timestamps have second-level
    # precision and cannot order competing refund states reliably.
    current = gateway.retrieve_refund(data["id"])
    if (current.get("id") != data["id"]
            or current.get("payment_intent") != gift.stripe_payment_intent_id
            or current.get("currency") != "usd" or type(current.get("amount")) is not int
            or not 0 < current["amount"] <= gift.amount_minor):
        raise ValueError("Retrieved refund does not match the donor gift")
    status = RefundStatus(current.get("status", "").upper()).value
    observed = datetime.now(UTC)
    refund = session.scalar(select(DonorRefundRecord).where(
        DonorRefundRecord.stripe_refund_id == data["id"]))
    if refund is None:
        refund = DonorRefundRecord(gift_id=gift.id, stripe_refund_id=data["id"],
                                   amount_minor=current["amount"], currency="USD",
                                   status=status, observed_at=observed)
        session.add(refund)
    elif refund.gift_id != gift.id or refund.amount_minor != current["amount"]:
        raise ValueError("Refund identity or amount changed")
    refund.status, refund.observed_at = status, observed
    session.flush()
    total = session.scalar(select(func.coalesce(func.sum(DonorRefundRecord.amount_minor), 0)).where(
        DonorRefundRecord.gift_id == gift.id, DonorRefundRecord.status == "SUCCEEDED"))
    if total > gift.amount_minor:
        raise ValueError("Successful refunds exceed the gift")
    session.add(DonorRefundObservationRecord(refund_id=refund.id, source_event_id=event.id,
                                           status=status, observed_at=observed))


def load_history(session, donor_id):
    """Owner-scoped inputs for calculation/replay; no invented periods for legacy gifts."""
    rows = session.execute(select(DonorGiftRecord, DonorCheckoutRecord).join(
        DonorCheckoutRecord, DonorCheckoutRecord.id == DonorGiftRecord.checkout_id,
    ).where(DonorGiftRecord.donor_id == donor_id)).all()
    gifts = [Gift(row.stripe_object_id, str(row.donor_id), row.amount_minor, _utc(row.paid_at),
                  row.frequency,
                  ReferralCandidate(str(draft.referrer_id), draft.referral_kind,
                                    _utc(draft.referral_expires_at)) if draft.referrer_id else None,
                  str(row.period_id) if row.period_id else None)
             for row, draft in rows]
    refunds = [Refund(r.stripe_refund_id, g.stripe_object_id, r.amount_minor, r.status,
                      _utc(r.observed_at)) for r, g in session.execute(
        select(DonorRefundRecord, DonorGiftRecord).join(
            DonorGiftRecord, DonorGiftRecord.id == DonorRefundRecord.gift_id,
        ).where(DonorGiftRecord.donor_id == donor_id))]
    periods = [BillingPeriod(str(p.id), str(p.donor_id), _utc(p.due_at), _utc(p.ends_at),
                             "FAILED" if p.status == "FAILED" else "EXPECTED")
               for p in session.scalars(select(DonorBillingPeriodRecord).where(
                   DonorBillingPeriodRecord.donor_id == donor_id))]
    return gifts, refunds, periods
