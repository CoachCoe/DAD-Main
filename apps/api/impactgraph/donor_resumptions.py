"""Shorten a pause to the next regular renewal, never charge skipped invoices."""

from datetime import UTC, datetime, timedelta

from sqlalchemy import select

from .donor_pauses import _read
from .donor_subscriptions import SubscriptionConflict, _attempt, _owner
from .donors import _utc
from .persistence import DonorPauseRecord, DonorResumeRecord


def _view(row):
    return {"request_id": str(row.id), "pause_id": str(row.pause_id),
            "next_renewal": _utc(row.next_renewal), "confirmed": row.confirmed_at is not None}


def resume_options(factory, gateway, token):
    from .donor_projections import refresh_projection

    with factory.begin() as session:
        donor = _owner(session, token)
        attempt = _attempt(session, donor.id)
        if attempt is None or not attempt.stripe_subscription_id:
            return {"state": "unavailable"}
        schedule, remote, _anchor = _read(session, attempt, gateway)
        refresh_projection(session, donor.id)
        pause = session.scalar(select(DonorPauseRecord).where(
            DonorPauseRecord.attempt_id == attempt.id).order_by(DonorPauseRecord.created_at.desc()).limit(1))
        if pause is None or not pause.confirmed_at or schedule.stops_at is not None or schedule.status != "active":
            return {"state": "unavailable"}
        command = session.scalar(select(DonorResumeRecord).where(DonorResumeRecord.pause_id == pause.id))
        if command:
            return {"state": "saved", **_view(command)}
        if (not remote.get("pause_collection") or _utc(schedule.period_end) >= _utc(pause.resumes_at)
                or datetime.now(UTC) >= _utc(schedule.period_end)):
            return {"state": "unavailable"}
        return {"state": "ready", "pause_id": str(pause.id), "next_renewal": _utc(schedule.period_end),
                "original_resume": _utc(pause.resumes_at)}


def resume_recurring(factory, gateway, token, *, request_id, pause_id, next_renewal):
    from .donor_projections import refresh_projection

    if request_id.version != 4 or next_renewal.tzinfo is None:
        raise ValueError("Invalid resumption confirmation")
    with factory.begin() as session:
        donor = _owner(session, token)
        pause = session.scalar(select(DonorPauseRecord).where(
            DonorPauseRecord.id == pause_id, DonorPauseRecord.donor_id == donor.id))
        if pause is None:
            raise SubscriptionConflict("No owned pause was found")
        command = session.get(DonorResumeRecord, request_id)
        if command:
            if (command.donor_id != donor.id or command.pause_id != pause.id
                    or _utc(command.next_renewal) != _utc(next_renewal)):
                raise SubscriptionConflict("Resumption key already used")
            if command.confirmed_at:
                latest = session.scalar(select(DonorPauseRecord.id).where(
                    DonorPauseRecord.attempt_id == pause.attempt_id).order_by(DonorPauseRecord.created_at.desc()).limit(1))
                attempt = _attempt(session, donor.id, pause.attempt_id)
                schedule, _remote, _anchor = _read(session, attempt, gateway)
                if latest != pause.id or schedule.stops_at is not None or schedule.status != "active":
                    raise SubscriptionConflict("Giving has changed since this request. Refresh its status")
                return _view(command)
        else:
            existing = session.scalar(select(DonorResumeRecord).where(DonorResumeRecord.pause_id == pause.id))
            if existing:
                raise SubscriptionConflict("A resumption request already exists. Refresh its status")
            latest = session.scalar(select(DonorPauseRecord.id).where(
                DonorPauseRecord.attempt_id == pause.attempt_id).order_by(DonorPauseRecord.created_at.desc()).limit(1))
            if latest != pause.id:
                raise SubscriptionConflict("This pause has been replaced")
            attempt = _attempt(session, donor.id, pause.attempt_id)
            schedule, remote, _anchor = _read(session, attempt, gateway)
            if (not pause.confirmed_at or not remote.get("pause_collection")
                    or schedule.status != "active" or schedule.stops_at is not None
                    or _utc(schedule.period_end) != _utc(next_renewal)
                    or not datetime.now(UTC) < _utc(next_renewal) < _utc(pause.resumes_at)):
                raise SubscriptionConflict("Renewal date changed. Refresh and confirm again")
            session.add(DonorResumeRecord(id=request_id, donor_id=donor.id, pause_id=pause.id,
                                          next_renewal=next_renewal, created_at=datetime.now(UTC)))
    with factory.begin() as session:
        donor = _owner(session, token)
        pause = session.get(DonorPauseRecord, pause_id)
        command = session.get(DonorResumeRecord, request_id)
        attempt = _attempt(session, donor.id, pause.attempt_id)
        schedule, _remote, _anchor = _read(session, attempt, gateway)
        if schedule.status != "active" or schedule.stops_at is not None:
            raise SubscriptionConflict("Giving has changed. Refresh its status")
        if command.confirmed_at is None:
            if (schedule.status != "active" or schedule.stops_at is not None
                    or _utc(schedule.period_end) != _utc(command.next_renewal)
                    or datetime.now(UTC) >= _utc(command.next_renewal)):
                raise SubscriptionConflict("Renewal changed. Reconcile before retry")
            if datetime.now(UTC) - _utc(command.created_at) >= timedelta(hours=23):
                raise SubscriptionConflict("Resumption requires reconciliation before retry")
            # Keep voiding skipped invoices and preserve the billing anchor. Only the
            # end of the pause changes; no pay/finalize or immediate-resume call is made.
            gateway.pause_collection(attempt.stripe_subscription_id, int(_utc(command.next_renewal).timestamp()),
                                     "dad-resume-" + str(command.id))
            _read(session, attempt, gateway)
            if command.confirmed_at is None:
                raise SubscriptionConflict("Resumption is not confirmed. Retry to check its status")
        refresh_projection(session, donor.id)
        return _view(command)
