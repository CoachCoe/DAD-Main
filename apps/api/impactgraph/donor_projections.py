"""Event-driven donor projections and scheduled expiry; all values remain sandbox-only."""

import argparse
import time
from dataclasses import replace
from datetime import UTC, datetime, timedelta
from uuid import UUID

from sqlalchemy import case, func, or_, select

from .database import create_session_factory
from .donor_financial_facts import load_history
from .donor_metrics import founding_donor, net_gifts, relationships, streak
from .donors import _utc
from .persistence import (
    DonorCheckoutRecord,
    DonorGiftRecord,
    DonorMetricProjectionRecord,
    DonorPauseRecord,
    DonorPaymentUpdateRecord,
    DonorRecord,
    DonorRefundObservationRecord,
    DonorRefundRecord,
    DonorSubscriptionRecord,
)


def event_donor_id(session, event):
    if event.data.get("payment_update_id"):
        return session.scalar(select(DonorPaymentUpdateRecord.donor_id).where(
            DonorPaymentUpdateRecord.id == UUID(event.data["payment_update_id"])))
    if event.data.get("checkout_id"):
        return session.scalar(select(DonorCheckoutRecord.donor_id).where(
            DonorCheckoutRecord.id == UUID(event.data["checkout_id"])))
    if event.data.get("payment_intent"):
        return session.scalar(select(DonorGiftRecord.donor_id).where(
            DonorGiftRecord.stripe_payment_intent_id == event.data["payment_intent"]))
    return None


def _historical_best(session, donor_id, gifts, refunds, periods, now, previous_best):
    # Replay known refund observations at payment and state-change times. Ignoring all
    # refunds would invent streaks that were never attained. No processor transition
    # time is inferred from the timestamp when we observed a refund.
    observations = session.execute(select(DonorRefundObservationRecord, DonorRefundRecord).join(
        DonorRefundRecord, DonorRefundRecord.id == DonorRefundObservationRecord.refund_id,
    ).join(DonorGiftRecord, DonorGiftRecord.id == DonorRefundRecord.gift_id).where(
        DonorGiftRecord.donor_id == donor_id,
        DonorRefundObservationRecord.observed_at <= now,
    ).order_by(DonorRefundObservationRecord.observed_at, DonorRefundObservationRecord.id)).all()
    by_id = {r.id: r for r in refunds}
    history = [replace(by_id[r.stripe_refund_id], status=o.status, updated_at=_utc(o.observed_at))
               for o, r in observations]
    observed_ids = {r.id for r in history}
    history.extend(r for r in refunds if r.id not in observed_ids and r.updated_at <= now)
    history.sort(key=lambda r: r.updated_at)
    points = {_utc(g.paid_at) for g in gifts if _utc(g.paid_at) <= now}
    points.update(r.updated_at for r in history)
    best = previous_best
    for point in sorted(points):
        latest = {}
        for refund in history:
            if refund.updated_at <= point:
                latest[refund.id] = refund
        try:
            best = max(best, streak(gifts, list(latest.values()), periods, str(donor_id),
                                   as_of=point, previous_best=best).best)
        except ValueError:
            # Missing or contradictory historical periods cannot confer recognition.
            continue
    return best


def refresh_projection(session, donor_id, *, now=None):
    now = _utc(now or datetime.now(UTC))
    # Every financial event and refresh takes the donor lock before gift/attempt locks.
    donor = session.scalar(select(DonorRecord).where(DonorRecord.id == donor_id).with_for_update())
    if donor is None:
        raise LookupError("Donor not found")
    row = session.scalar(select(DonorMetricProjectionRecord).where(
        DonorMetricProjectionRecord.donor_id == donor_id))
    if row is None:
        row = DonorMetricProjectionRecord(donor_id=donor_id, best_streak=0,
                                          computed_at=now, next_refresh_at=now)
        session.add(row)
    gifts, refunds, periods = load_history(session, donor_id)
    pauses = session.execute(select(DonorPauseRecord, DonorSubscriptionRecord).join(
        DonorSubscriptionRecord, DonorSubscriptionRecord.attempt_id == DonorPauseRecord.attempt_id,
    ).where(DonorPauseRecord.donor_id == donor_id, DonorPauseRecord.confirmed_at.is_not(None))).all()
    pause_windows = [(_utc(p.starts_at), datetime.max.replace(tzinfo=UTC)
                     if s.stops_at and _utc(s.stops_at) <= now else _utc(p.resumes_at)) for p, s in pauses]
    stopped = [_utc(end) for end in session.scalars(select(DonorSubscriptionRecord.stops_at).where(
        DonorSubscriptionRecord.donor_id == donor_id, DonorSubscriptionRecord.stops_at.is_not(None)))]
    relation = relationships(gifts, as_of=now).get(str(donor_id))
    row.first_gift_id = relation.first_gift_id if relation else None
    row.referrer_id = UUID(relation.referrer_id) if relation and relation.referrer_id else None
    row.referral_kind = relation.kind if relation else None
    if founding_donor(gifts, str(donor_id), as_of=now) and row.founding_awarded_at is None:
        row.founding_awarded_at = now
    row.computed_at = now
    row.next_refresh_at = now + timedelta(days=1)
    row.current_streak, row.grace_ends_at, row.at_risk = None, None, False
    try:
        row.net_minor = sum(net_gifts(gifts, refunds, as_of=now).values())
    except ValueError:
        row.net_minor = None
    row.best_streak = _historical_best(session, donor_id, gifts, refunds, periods, now, row.best_streak)
    try:
        result = streak(gifts, refunds, periods, str(donor_id), as_of=now, previous_best=row.best_streak,
                        stopped_period_ends=stopped, pause_windows=pause_windows)
        row.current_streak, row.best_streak = result.current, result.best
        row.at_risk, row.grace_ends_at = result.at_risk, result.grace_ends_at
    except ValueError:
        row.next_refresh_at = now + timedelta(minutes=5)
    boundaries = [*(_utc(p.due_at) for p in periods), *(_utc(p.ends_at) for p in periods),
                  *(_utc(g.paid_at) for g in gifts), *stopped,
                  *(t for window in pause_windows for t in window)]
    if row.grace_ends_at:
        boundaries.append(row.grace_ends_at)
    row.next_refresh_at = min([row.next_refresh_at, *(t for t in boundaries if t > now)])
    session.flush()
    return row


def refresh_next(factory, *, now=None):
    now = _utc(now or datetime.now(UTC))
    with factory.begin() as session:
        donor = session.scalar(select(DonorRecord).outerjoin(
            DonorMetricProjectionRecord, DonorMetricProjectionRecord.donor_id == DonorRecord.id,
        ).where(or_(DonorMetricProjectionRecord.id.is_(None),
                    DonorMetricProjectionRecord.next_refresh_at <= now))
            .order_by(DonorRecord.id).with_for_update(of=DonorRecord, skip_locked=True).limit(1))
        if donor is None:
            return False
        refresh_projection(session, donor.id, now=now)
    return True


def dashboard(session, donor_id, *, now=None):
    now = _utc(now or datetime.now(UTC))
    row = session.scalar(select(DonorMetricProjectionRecord).where(
        DonorMetricProjectionRecord.donor_id == donor_id))
    if row is None:
        return {"mode": "test", "state": "preparing"}
    # Count each donor once using their permanent direct relationship and net collection.
    totals = session.execute(select(
        func.coalesce(func.sum(case((DonorMetricProjectionRecord.referral_kind == "LINK",
                                     case((DonorMetricProjectionRecord.net_minor > 0, 1), else_=0)), else_=0)), 0),
        func.coalesce(func.sum(case((DonorMetricProjectionRecord.referral_kind == "IRL",
                                     case((DonorMetricProjectionRecord.net_minor > 0, 1), else_=0)), else_=0)), 0),
        func.coalesce(func.sum(case((DonorMetricProjectionRecord.referral_kind == "LINK",
                                     DonorMetricProjectionRecord.net_minor), else_=0)), 0),
        func.coalesce(func.sum(case((DonorMetricProjectionRecord.referral_kind == "IRL",
                                     DonorMetricProjectionRecord.net_minor), else_=0)), 0),
        func.count(case((or_(DonorMetricProjectionRecord.net_minor.is_(None),
                            DonorMetricProjectionRecord.next_refresh_at <= now), 1))),
    ).where(DonorMetricProjectionRecord.referrer_id == donor_id)).one()
    # During backfill, a missing referred donor projection is unknown, never a zero.
    missing = session.scalar(select(DonorGiftRecord.id).join(
        DonorCheckoutRecord, DonorCheckoutRecord.id == DonorGiftRecord.checkout_id,
    ).outerjoin(DonorMetricProjectionRecord,
                DonorMetricProjectionRecord.donor_id == DonorGiftRecord.donor_id).where(
        DonorCheckoutRecord.referrer_id == donor_id, DonorMetricProjectionRecord.id.is_(None),
    ).limit(1))
    referrals_ready = not missing and totals[4] == 0
    fresh = _utc(row.next_refresh_at) > now
    return {
        "mode": "test", "state": "ready", "computed_at": row.computed_at,
        "referrals": {"link": totals[0], "irl": totals[1],
                      "link_minor": totals[2], "irl_minor": totals[3],
                      "total_minor": totals[2] + totals[3], "currency": "USD"}
        if referrals_ready else None,
        "streak": {"current": row.current_streak, "at_risk": row.at_risk,
                   "grace_ends_at": row.grace_ends_at}
        if fresh and row.current_streak is not None else None,
        "founding_donor": row.founding_awarded_at is not None,
    }


def main():
    parser = argparse.ArgumentParser(description="Refresh donor dashboard projections")
    parser.add_argument("--once", action="store_true", help="Process at most one due donor")
    args = parser.parse_args()
    factory = create_session_factory()
    while True:
        worked = refresh_next(factory)
        if args.once:
            return
        if not worked:
            time.sleep(2)


if __name__ == "__main__":
    main()
