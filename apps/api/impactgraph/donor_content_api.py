"""Public spreadsheet snapshots and permission-protected publication tools."""
from datetime import date
from typing import Annotated
from urllib.parse import quote
from uuid import UUID

from cryptography.fernet import InvalidToken
from fastapi import Header, HTTPException, Request, Response
from pydantic import BaseModel, ConfigDict, Field, StrictBool
from sqlalchemy import select

from . import donor_staff as staff
from .donor_content import partner_signup, public_content, publish, read_source, store_source
from .persistence import (
    DonorContentPublicationRecord,
    DonorContentSourceRecord,
    DonorPartnerSignupRecord,
)


class Publication(BaseModel):
    model_config = ConfigDict(extra="forbid")
    initiatives_csv: str = Field(max_length=512 * 1024)
    ledger_csv: str = Field(max_length=512 * 1024)
    as_of: date
    cadence: str = Field(min_length=1, max_length=100)


class PartnerSignup(BaseModel):
    model_config = ConfigDict(extra="forbid")
    organization: str = Field(min_length=1, max_length=200)
    name: str = Field(min_length=1, max_length=100)
    email: str = Field(max_length=320)
    website: str = Field(default="", max_length=500)
    message: str = Field(default="", max_length=2000)
    contact_consent: StrictBool


def install_content_routes(app, factory, browser_request):
    def admin(request, operation):
        try:
            with factory.begin() as session:
                actor = staff.require_staff(session, request.cookies.get(staff.COOKIE), "content.publish")
                return operation(session, actor)
        except staff.StaffDenied as exc:
            raise HTTPException(401, "Staff sign-in required") from exc
        except staff.StaffForbidden as exc:
            raise HTTPException(403, "Permission denied") from exc
        except LookupError as exc:
            raise HTTPException(404, "Content not found") from exc
        except (ValueError, InvalidToken) as exc:
            raise HTTPException(409, "Content validation or source access failed. Check the spreadsheet and configuration") from exc

    @app.get("/donor/public/content")
    def content():
        with factory() as session:
            return public_content(session)

    @app.get("/donor/staff/content")
    def inventory(request: Request):
        def action(session, _actor):
            sources = session.execute(select(DonorContentSourceRecord.id, DonorContentSourceRecord.name,
                DonorContentSourceRecord.sha256).order_by(DonorContentSourceRecord.created_at.desc()).limit(500)).all()
            publications = session.execute(select(DonorContentPublicationRecord.id, DonorContentPublicationRecord.created_at)
                .order_by(DonorContentPublicationRecord.created_at.desc()).limit(20)).all()
            return {"sources": [{"id": str(row.id), "name": row.name, "sha256": row.sha256} for row in sources],
                    "publications": [{"id": str(row.id), "at": row.created_at} for row in publications]}
        return admin(request, action)

    @app.post("/donor/staff/content/sources")
    async def source_upload(request: Request, name: str):
        browser_request(request)
        admin(request, lambda _session, _actor: None)
        data = bytearray()
        async for chunk in request.stream():
            if len(data) + len(chunk) > 10 * 1024 * 1024:
                raise HTTPException(413, "Source document exceeds 10 MB")
            data.extend(chunk)
        return admin(request, lambda session, actor: store_source(session, actor, name, bytes(data)))

    @app.get("/donor/staff/content/sources/{source_id}")
    def source_download(source_id: UUID, request: Request):
        def action(session, actor):
            data = read_source(session, actor, source_id)
            name = session.get(DonorContentSourceRecord, source_id).name
            return Response(data, media_type="application/octet-stream", headers={
                "Content-Disposition": "attachment; filename*=UTF-8''" + quote(name, safe=""),
                "X-Content-Type-Options": "nosniff"})
        return admin(request, action)

    @app.post("/donor/staff/content/publish")
    def publication(body: Publication, request: Request):
        browser_request(request)
        return admin(request, lambda session, actor: publish(session, actor, **body.model_dump()))

    @app.post("/donor/public/partner-signup")
    def partner(body: PartnerSignup, request: Request, idempotency_key: Annotated[UUID, Header()]):
        browser_request(request)
        if body.contact_consent is not True:
            raise HTTPException(422, "Confirm permission for DAD to contact you about this inquiry")
        try:
            with factory.begin() as session:
                return partner_signup(session, idempotency_key, body.model_dump(exclude={"contact_consent"}))
        except ValueError as exc:
            raise HTTPException(409, str(exc)) from exc

    @app.get("/donor/staff/partners")
    def partners(request: Request, offset: int = 0):
        if offset < 0:
            raise HTTPException(422, "Invalid offset")
        def action(session, actor):
            rows = session.scalars(select(DonorPartnerSignupRecord).order_by(DonorPartnerSignupRecord.created_at.desc(),
                DonorPartnerSignupRecord.id).offset(offset).limit(51)).all()
            staff.audit(session, actor.id, "partner.inquiries_viewed", "partner-signups", count=min(len(rows), 50))
            return {"signups": [{"id": str(row.id), "organization": row.organization, "name": row.name,
                "email": row.email, "website": row.website, "message": row.message, "at": row.created_at}
                for row in rows[:50]], "next_offset": offset + 50 if len(rows) > 50 else None}
        return admin(request, action)
