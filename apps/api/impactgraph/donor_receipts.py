"""Owner-scoped sandbox gift records; never represented as legal tax receipts."""
from datetime import UTC, datetime
from zoneinfo import ZoneInfo

from sqlalchemy import select

from .donors import _utc
from .persistence import DonorGiftRecord, DonorRefundRecord

YEAR_ZONE = ZoneInfo("America/New_York")


def gift_record(session, donor_id, gift_id, *, now=None):
    now = now or datetime.now(UTC)
    gift = session.scalar(select(DonorGiftRecord).where(DonorGiftRecord.id == gift_id,
                            DonorGiftRecord.donor_id == donor_id, DonorGiftRecord.paid_at <= now))
    if gift is None:
        raise LookupError("Gift not found")
    refunds = session.scalars(select(DonorRefundRecord).where(DonorRefundRecord.gift_id == gift.id)
                               .order_by(DonorRefundRecord.observed_at, DonorRefundRecord.id)).all()
    refunded = sum(row.amount_minor for row in refunds if row.status == "SUCCEEDED")
    if refunded > gift.amount_minor:
        raise ValueError("Refunds require reconciliation")
    return {"id": str(gift.id), "amount_minor": gift.amount_minor, "currency": gift.currency,
            "paid_at": _utc(gift.paid_at).isoformat(), "frequency": gift.frequency,
            "refunded_minor": refunded, "net_minor": gift.amount_minor - refunded,
            "refunds": [{"amount_minor": row.amount_minor, "status": row.status,
                         "observed_at": _utc(row.observed_at).isoformat()} for row in refunds]}


def receipt(session, donor_id, gift_id, *, now=None):
    now = now or datetime.now(UTC)
    return {"mode": "test", "tax_receipt": False, "as_of": now.isoformat(),
            "receipt_id": "DAD-TEST-" + str(gift_id), "gift": gift_record(session, donor_id, gift_id, now=now)}


def annual_summary(session, donor_id, year, *, now=None):
    now = now or datetime.now(UTC)
    if type(year) is not int or not 2026 <= year <= now.astimezone(YEAR_ZONE).year:
        raise ValueError("Invalid summary year")
    start, end = datetime(year, 1, 1, tzinfo=YEAR_ZONE), datetime(year + 1, 1, 1, tzinfo=YEAR_ZONE)
    ids = session.scalars(select(DonorGiftRecord.id).where(DonorGiftRecord.donor_id == donor_id,
        DonorGiftRecord.paid_at >= start, DonorGiftRecord.paid_at < end, DonorGiftRecord.paid_at <= now)
        .order_by(DonorGiftRecord.paid_at, DonorGiftRecord.id)).all()
    gifts = [gift_record(session, donor_id, gift_id, now=now) for gift_id in ids]
    return {"mode": "test", "tax_receipt": False, "year": year, "timezone": YEAR_ZONE.key,
            "as_of": now.isoformat(), "gifts": gifts,
            "amount_minor": sum(gift["amount_minor"] for gift in gifts),
            "refunded_minor": sum(gift["refunded_minor"] for gift in gifts),
            "net_minor": sum(gift["net_minor"] for gift in gifts), "currency": "USD"}


def money(minor):
    return f"${minor // 100:,}.{minor % 100:02d}"


def render_receipt(document):
    gift = document["gift"]
    frequency = "Monthly" if gift["frequency"] == "MONTHLY" else "One-time"
    return (f"DAD sandbox gift record {document['receipt_id']}\n"
            "TEST PAYMENT — NOT A TAX RECEIPT. No real donation was collected.\n\n"
            f"{frequency} test gift: {money(gift['amount_minor'])} USD\n"
            f"Completed: {gift['paid_at']}\n"
            f"Successful refunds: {money(gift['refunded_minor'])} USD\n"
            f"Net test amount: {money(gift['net_minor'])} USD\n"
            f"As of: {document['as_of']}\n\n"
            "DAD launches in April 2027. Pre-launch giving earns permanent Founding Donor recognition; "
            "recognition in this sandbox is simulated.\n")


def render_summary(document):
    rows = "\n".join(f"{gift['paid_at']} | {money(gift['amount_minor'])} | refunds {money(gift['refunded_minor'])} | {gift['id']}"
                     for gift in document["gifts"])
    return (f"DAD sandbox annual summary — {document['year']}\n"
            "TEST PAYMENTS — NOT A TAX RECEIPT. No real donations were collected.\n\n"
            f"Calendar year uses {document['timezone']}. As of {document['as_of']}.\n"
            "Refund adjustments reflect the latest verified status for gifts in this year, "
            "including adjustments observed in a later year. This is not a tax deduction calculation.\n\n"
            f"{rows or 'No confirmed gifts in this year.'}\n\n"
            f"Collected: {money(document['amount_minor'])} USD\n"
            f"Successful refunds: {money(document['refunded_minor'])} USD\n"
            f"Net: {money(document['net_minor'])} USD\n")
