"""Fixed-length, donor-authorized pauses of future monthly renewals."""

from calendar import monthrange
from datetime import UTC, datetime, timedelta

from sqlalchemy import select

from .donor_subscriptions import SubscriptionConflict, _attempt, _owner
from .donors import _utc
from .persistence import DonorPauseRecord, DonorResumeRecord


def effective_resume(session, pause):
    resume = session.scalar(select(DonorResumeRecord).where(DonorResumeRecord.pause_id == pause.id))
    return _utc(resume.next_renewal) if resume and resume.confirmed_at else _utc(pause.resumes_at)


def renewal_after(boundary, months, anchor):
    """Retain Stripe's original UTC billing day, including 29th–31st anchors."""
    boundary, anchor = _utc(boundary), _utc(anchor)
    if type(months) is not int or months not in {1, 2, 3}:
        raise ValueError("Choose 1, 2, or 3 renewals")
    expected = boundary.replace(day=min(anchor.day, monthrange(boundary.year, boundary.month)[1]),
                                hour=anchor.hour, minute=anchor.minute, second=anchor.second, microsecond=0)
    if boundary != expected:
        raise ValueError("Billing boundary differs from Stripe's anchor")
    index = boundary.year * 12 + boundary.month - 1 + months
    year, month = divmod(index, 12)
    month += 1
    return boundary.replace(year=year, month=month, day=min(anchor.day, monthrange(year, month)[1]))


def reconcile_pause(session, attempt, remote, *, now=None):
    """Recognize only pauses authorized here; unexpected schedule edits require review."""
    now = _utc(now or datetime.now(UTC))
    pause = remote.get("pause_collection")
    row = session.scalar(select(DonorPauseRecord).where(
        DonorPauseRecord.attempt_id == attempt.id).order_by(DonorPauseRecord.created_at.desc()).limit(1))
    resume = session.scalar(select(DonorResumeRecord).where(DonorResumeRecord.pause_id == row.id)) if row else None
    if pause:
        if (resume and pause.get("behavior") == "void"
                and pause.get("resumes_at") == int(_utc(resume.next_renewal).timestamp())):
            resume.confirmed_at = resume.confirmed_at or now
            return row
        if (row is None or pause.get("behavior") != "void"
                or pause.get("resumes_at") != int(_utc(row.resumes_at).timestamp())
                or (resume and resume.confirmed_at)):
            raise ValueError("Unrecognized payment pause")
        row.confirmed_at = row.confirmed_at or now
    elif (row and row.confirmed_at and now < effective_resume(session, row)
          and remote.get("status") not in {"canceled", "incomplete_expired"}):
        raise ValueError("Pause ended earlier than the authorized schedule")
    return row


def _read(session, attempt, gateway):
    from .donor_subscriptions import reconcile_subscription

    remote = gateway.retrieve_subscription(attempt.stripe_subscription_id)
    schedule = reconcile_subscription(session, attempt, gateway, remote=remote)
    anchor = remote.get("billing_cycle_anchor")
    if type(anchor) is not int or remote.get("schedule") or remote.get("pending_update"):
        raise ValueError("Unsupported billing anchor or scheduled change")
    return schedule, remote, datetime.fromtimestamp(anchor, UTC)


def pause_options(factory, gateway, token):
    from .donor_projections import refresh_projection

    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id)
        if attempt is None or not attempt.stripe_subscription_id:
            return {"state": "unavailable", "pause": None, "options": []}
        schedule, _remote, anchor = _read(session, attempt, gateway)
        refresh_projection(session, donor.id)
        if schedule.stops_at is not None or schedule.status in {"canceled", "incomplete_expired"}:
            return {"state": "unavailable", "pause": None, "options": []}
        row = session.scalar(select(DonorPauseRecord).where(
            DonorPauseRecord.attempt_id == attempt.id).order_by(DonorPauseRecord.created_at.desc()).limit(1))
        active = row and (row.confirmed_at is None or datetime.now(UTC) < effective_resume(session, row))
        return {"state": "ready", "subscription_id": str(attempt.id),
                "pause": {"request_id": str(row.id), "months": row.months, "starts_at": _utc(row.starts_at), "resumes_at": effective_resume(session, row),
                          "confirmed": row.confirmed_at is not None} if active else None,
                "options": [{"months": months, "starts_at": _utc(schedule.period_end),
                             "resumes_at": renewal_after(schedule.period_end, months, anchor)}
                            for months in (1, 2, 3)]
                if not active and schedule.status == "active" and schedule.stops_at is None else []}


def pause_recurring(factory, gateway, token, *, request_id, attempt_id, months, starts_at, resumes_at):
    from .donor_projections import refresh_projection

    if request_id.version != 4 or type(months) is not int or months not in {1, 2, 3}:
        raise ValueError("Invalid pause confirmation")
    if starts_at.tzinfo is None or resumes_at.tzinfo is None:
        raise ValueError("Pause dates require time zones")
    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id, attempt_id)
        if attempt is None or not attempt.stripe_subscription_id:
            raise SubscriptionConflict("No confirmed monthly gift was found")
        row = session.get(DonorPauseRecord, request_id)
        if row:
            if (row.donor_id != donor.id or row.attempt_id != attempt.id or row.months != months
                    or _utc(row.starts_at) != _utc(starts_at) or _utc(row.resumes_at) != _utc(resumes_at)):
                raise SubscriptionConflict("Pause key already used")
        else:
            other = session.scalar(select(DonorPauseRecord).where(
                DonorPauseRecord.attempt_id == attempt.id).order_by(DonorPauseRecord.created_at.desc()).limit(1))
            if other and (other.confirmed_at is None or datetime.now(UTC) < effective_resume(session, other)):
                raise SubscriptionConflict("A pause already exists. Refresh to view its dates")
            schedule, remote, anchor = _read(session, attempt, gateway)
            if (schedule.status != "active" or schedule.stops_at is not None or remote.get("pause_collection")
                    or _utc(schedule.period_end) != _utc(starts_at)
                    or renewal_after(starts_at, months, anchor) != _utc(resumes_at)):
                raise SubscriptionConflict("Pause dates changed. Refresh and confirm again")
            session.add(DonorPauseRecord(id=request_id, donor_id=donor.id, attempt_id=attempt.id,
                                        months=months, starts_at=starts_at, resumes_at=resumes_at,
                                        created_at=datetime.now(UTC)))
    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id, attempt_id)
        row = session.get(DonorPauseRecord, request_id)
        schedule, remote, anchor = _read(session, attempt, gateway)
        if row.confirmed_at is None:
            if (datetime.now(UTC) >= _utc(row.starts_at) or schedule.stops_at is not None
                    or schedule.status != "active" or _utc(schedule.period_end) != _utc(row.starts_at)):
                raise SubscriptionConflict("Renewal changed. Reconcile the pause before retry")
            if datetime.now(UTC) - _utc(row.created_at) >= timedelta(hours=23):
                raise SubscriptionConflict("Pause requires reconciliation before retry")
            gateway.pause_collection(attempt.stripe_subscription_id, int(_utc(row.resumes_at).timestamp()),
                                     "dad-pause-" + str(row.id))
            _read(session, attempt, gateway)
            if row.confirmed_at is None:
                raise SubscriptionConflict("Pause is not confirmed. Retry to check its status")
        refresh_projection(session, donor.id)
        result = {"request_id": str(row.id), "months": row.months, "starts_at": _utc(row.starts_at), "resumes_at": effective_resume(session, row), "confirmed": True}
    return {"pause": result}
