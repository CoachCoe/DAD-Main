"""Staff-only R1 routes; never accepts role, actor, or donor ownership from headers."""
from datetime import UTC, datetime, timedelta
from typing import Annotated, Literal
from uuid import UUID

from fastapi import Header, HTTPException, Request, Response
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy import select

from . import donor_staff as staff
from .donor_email import queue_login_email
from .donor_exports import DATASETS, csv_rows
from .persistence import (
    DonorGiftRecord,
    DonorMessageRecord,
    DonorMetricProjectionRecord,
    DonorRecord,
    DonorStaffAuditRecord,
    DonorStaffRecord,
)


class Credentials(BaseModel):
    model_config = ConfigDict(extra="forbid")
    email: str = Field(max_length=320)
    password: str = Field(min_length=1, max_length=128)
    code: str = Field(pattern=r"^[0-9]{6}$")


class Invitation(BaseModel):
    model_config = ConfigDict(extra="forbid")
    email: str = Field(max_length=320)
    name: str = Field(min_length=1, max_length=100)
    role: Literal["SUPPORT", "FINANCE", "ADMIN"]


class Token(BaseModel):
    model_config = ConfigDict(extra="forbid")
    token: str = Field(min_length=32, max_length=128)


class Activation(Token):
    password: str = Field(min_length=12, max_length=128)
    code: str = Field(pattern=r"^[0-9]{6}$")


def install_staff_routes(app, factory, settings, browser_request):
    def run(request, permission, operation):
        try:
            with factory.begin() as session:
                actor = staff.require_staff(session, request.cookies.get(staff.COOKIE), permission)
                return operation(session, actor)
        except staff.StaffDenied as exc:
            raise HTTPException(401, "Staff sign-in required") from exc
        except staff.StaffForbidden as exc:
            raise HTTPException(403, "Permission denied") from exc
        except LookupError as exc:
            raise HTTPException(404, "Record not found") from exc
        except ValueError as exc:
            raise HTTPException(409, str(exc)) from exc

    @app.post("/donor/staff/sign-in")
    def sign_in(body: Credentials, request: Request, response: Response):
        browser_request(request)
        try:
            token = staff.sign_in(factory, body.email, body.password, body.code)
        except staff.StaffDenied as exc:
            raise HTTPException(401, "Credentials invalid, expired, or temporarily locked") from exc
        except ValueError as exc:
            raise HTTPException(409, "Staff sign-in is unavailable") from exc
        response.set_cookie(staff.COOKIE, token, max_age=8 * 3600, httponly=True, samesite="strict",
                            secure=settings.origin.startswith("https:"), path="/")
        return {"signed_in": True}

    @app.post("/donor/staff/provision")
    def provision(body: Token, request: Request):
        browser_request(request)
        try:
            with factory.begin() as session:
                return staff.provision(session, body.token)
        except staff.StaffDenied as exc:
            raise HTTPException(401, "Invitation invalid or expired") from exc
        except ValueError as exc:
            raise HTTPException(503, "Staff setup is unavailable") from exc

    @app.post("/donor/staff/activate")
    def activate(body: Activation, request: Request):
        browser_request(request)
        try:
            staff.activate(factory, body.token, body.password, body.code)
        except staff.StaffDenied as exc:
            raise HTTPException(401, "Invitation or authenticator code invalid or expired") from exc
        except ValueError as exc:
            raise HTTPException(503, "Staff setup is unavailable") from exc
        return {"activated": True}

    @app.get("/donor/staff/me")
    def me(request: Request):
        return run(request, None, lambda _session, actor: staff.view(actor))

    @app.post("/donor/staff/sign-out")
    def sign_out(request: Request, response: Response):
        browser_request(request)
        run(request, None, lambda session, _actor: staff.sign_out(session, request.cookies.get(staff.COOKIE)))
        response.delete_cookie(staff.COOKIE, path="/")
        return {"signed_out": True}

    @app.get("/donor/staff/accounts")
    def accounts(request: Request):
        return run(request, "staff.manage", lambda session, _actor: [staff.view(row) for row in
            session.scalars(select(DonorStaffRecord).order_by(DonorStaffRecord.email))])

    @app.post("/donor/staff/accounts")
    def invite(body: Invitation, request: Request):
        browser_request(request)
        def action(session, actor):
            token = staff.invite_staff(session, **body.model_dump(), actor_id=actor.id)
            return {"invitation_url": f"{settings.origin}/staff/activate#token={token}"}
        return run(request, "staff.manage", action)

    @app.post("/donor/staff/accounts/{staff_id}/disable")
    def disable(staff_id: UUID, request: Request):
        browser_request(request)
        def action(session, actor):
            staff.disable_staff(session, actor, staff_id)
            return {"disabled": True}
        return run(request, "staff.manage", action)

    @app.get("/donor/staff/exports")
    def exports(request: Request):
        return run(request, "data.export", lambda _session, actor: {
            "datasets": [name for name, (_, _, permission) in DATASETS.items() if permission in staff.PERMISSIONS[actor.role]]})

    @app.get("/donor/staff/exports/{dataset}.csv")
    def export(dataset: str, request: Request):
        if dataset not in DATASETS:
            raise HTTPException(404, "Dataset not found")
        permission = DATASETS[dataset][2]
        record_id = run(request, permission, lambda session, actor: staff.audit(session, actor.id, "data.export_started", dataset).id)
        return StreamingResponse(csv_rows(factory, request.cookies.get(staff.COOKIE), dataset, record_id),
            media_type="text/csv", headers={"Content-Disposition": f'attachment; filename="dad-{dataset}.csv"',
                                           "X-Content-Type-Options": "nosniff"})

    @app.get("/donor/staff/enrolments")
    def enrolments(request: Request):
        return run(request, "data.export", lambda _session, _actor: {"state": "not_active_until_r2", "enrolments": []})

    @app.post("/donor/staff/donors/{donor_id}/claim-email")
    def resend_claim(donor_id: UUID, request: Request):
        browser_request(request)
        def action(session, actor):
            donor = session.get(DonorRecord, donor_id)
            if donor is None:
                raise LookupError("Donor not found")
            queue_login_email(session, donor.email)
            staff.audit(session, actor.id, "message.claim_requested", donor_id)
            return {"queued": True}
        return run(request, "message.resend", action)

    @app.get("/donor/staff/donors/{donor_id}/messages")
    def messages(donor_id: UUID, request: Request, offset: int = 0):
        if offset < 0:
            raise HTTPException(422, "Invalid offset")
        def action(session, actor):
            if session.get(DonorRecord, donor_id) is None:
                raise LookupError("Donor not found")
            rows = session.scalars(select(DonorMessageRecord).where(DonorMessageRecord.donor_id == donor_id)
                .order_by(DonorMessageRecord.created_at.desc(), DonorMessageRecord.id).offset(offset).limit(51)).all()
            staff.audit(session, actor.id, "message.list", donor_id)
            return {"messages": [{"id": str(row.id), "kind": row.kind, "attempts": row.attempts,
                    "completed_at": row.completed_at, "last_error": row.last_error, "suppressed_reason": row.suppressed_reason,
                    "available_at": row.available_at} for row in rows[:50]],
                    "next_offset": offset + 50 if len(rows) > 50 else None}
        return run(request, "donor.read", action)

    @app.post("/donor/staff/messages/{message_id}/resend")
    def resend(message_id: UUID, request: Request, idempotency_key: Annotated[UUID, Header()]):
        browser_request(request)
        if idempotency_key.version != 4:
            raise HTTPException(422, "Use a UUID v4 request key")
        def action(session, actor):
            # Serialize per donor to keep two simultaneous retries from creating two jobs.
            source = session.get(DonorMessageRecord, message_id)
            if source is None:
                raise LookupError("Message not found")
            session.execute(select(DonorRecord.id).where(DonorRecord.id == source.donor_id).with_for_update())
            key = "resend:" + str(idempotency_key)
            existing = session.scalar(select(DonorMessageRecord).where(DonorMessageRecord.dedup_key == key))
            if existing:
                if existing.context.get("resend_source") != str(message_id) or existing.context.get("resend_actor") != str(actor.id):
                    raise ValueError("Resend key already used")
                return {"queued": True, "id": str(existing.id)}
            if source.kind not in ("RECEIPT", "ANNUAL") or source.suppressed_reason:
                raise ValueError("Only unsuppressed gift records and annual summaries may be resent")
            if source.completed_at is None and source.attempts < 5:
                raise ValueError("The original message is still queued")
            recent = session.scalar(select(DonorMessageRecord.id).where(DonorMessageRecord.donor_id == source.donor_id,
                DonorMessageRecord.kind == source.kind, DonorMessageRecord.created_at > datetime.now(UTC) - timedelta(minutes=1)))
            if recent:
                raise ValueError("Wait one minute before requesting another copy")
            job = DonorMessageRecord(donor_id=source.donor_id, kind=source.kind, dedup_key=key,
                context={**source.context, "resend_source": str(message_id), "resend_actor": str(actor.id)}, available_at=datetime.now(UTC))
            session.add(job)
            session.flush()
            staff.audit(session, actor.id, "message.resend_requested", job.id, source_id=str(message_id))
            return {"queued": True, "id": str(job.id)}
        return run(request, "message.resend", action)

    @app.get("/donor/staff/donors")
    def donors(request: Request, q: str = "", offset: int = 0):
        if len(q) > 320 or offset < 0:
            raise HTTPException(422, "Invalid search")
        def action(session, actor):
            term = q.strip().replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")
            rows = session.scalars(select(DonorRecord).where(DonorRecord.email.ilike(f"%{term}%", escape="\\"))
                .order_by(DonorRecord.email, DonorRecord.id).offset(offset).limit(51)).all()
            staff.audit(session, actor.id, "donor.search", "donors", offset=offset, result_count=min(50, len(rows)))
            return {"donors": [{"id": str(row.id), "email": row.email, "public_name": row.public_name,
                    "claimed": row.claimed_at is not None, "disabled": row.disabled} for row in rows[:50]],
                    "next_offset": offset + 50 if len(rows) > 50 else None}
        return run(request, "donor.read", action)

    @app.get("/donor/staff/donors/{donor_id}")
    def donor(donor_id: UUID, request: Request, gifts_offset: int = 0, referrals_offset: int = 0):
        if gifts_offset < 0 or referrals_offset < 0:
            raise HTTPException(422, "Invalid offset")
        def action(session, actor):
            row = session.get(DonorRecord, donor_id)
            if row is None:
                raise LookupError("Donor not found")
            projection = session.scalar(select(DonorMetricProjectionRecord).where(DonorMetricProjectionRecord.donor_id == donor_id))
            gifts = session.scalars(select(DonorGiftRecord).where(DonorGiftRecord.donor_id == donor_id)
                .order_by(DonorGiftRecord.paid_at.desc(), DonorGiftRecord.id).offset(gifts_offset).limit(51)).all()
            referred = session.execute(select(DonorRecord, DonorMetricProjectionRecord).join(DonorMetricProjectionRecord,
                DonorMetricProjectionRecord.donor_id == DonorRecord.id).where(DonorMetricProjectionRecord.referrer_id == donor_id)
                .order_by(DonorRecord.id).offset(referrals_offset).limit(51)).all()
            referrer = session.get(DonorRecord, projection.referrer_id) if projection and projection.referrer_id else None
            staff.audit(session, actor.id, "donor.view", donor_id)
            return {"id": str(row.id), "email": row.email, "public_name": row.public_name,
                    "streak": {"current": projection.current_streak, "best": projection.best_streak,
                               "at_risk": projection.at_risk, "computed_at": projection.computed_at} if projection else None,
                    "referrer": {"id": str(referrer.id), "email": referrer.email, "type": projection.referral_kind} if referrer else None,
                    "gifts": [{"id": str(g.id), "amount_minor": g.amount_minor, "currency": g.currency,
                               "frequency": g.frequency, "paid_at": g.paid_at} for g in gifts[:50]],
                    "referred": [{"id": str(d.id), "email": d.email, "type": p.referral_kind,
                                  "net_minor": p.net_minor} for d, p in referred[:50]],
                    "next_gifts_offset": gifts_offset + 50 if len(gifts) > 50 else None,
                    "next_referrals_offset": referrals_offset + 50 if len(referred) > 50 else None,
                    "as_of": datetime.now(UTC)}
        return run(request, "donor.read", action)

    @app.get("/donor/staff/audit")
    def audit(request: Request, offset: int = 0):
        if offset < 0:
            raise HTTPException(422, "Invalid offset")
        def action(session, _actor):
            rows = session.scalars(select(DonorStaffAuditRecord).order_by(DonorStaffAuditRecord.created_at.desc(),
                DonorStaffAuditRecord.id).offset(offset).limit(51)).all()
            return {"events": [{"id": str(row.id), "actor_id": str(row.actor_id) if row.actor_id else None,
                                "action": row.action, "target": row.target, "at": row.created_at, "details": row.details}
                               for row in rows[:50]], "next_offset": offset + 50 if len(rows) > 50 else None}
        return run(request, "audit.read", action)
