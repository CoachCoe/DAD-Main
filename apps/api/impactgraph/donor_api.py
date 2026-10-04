"""Standalone donor API, independent of evidence storage, AI, and blockchain readiness.

Run with uvicorn impactgraph.donor_api:app. Checkout remains Stripe sandbox-only.
"""

from __future__ import annotations

from typing import Annotated
from uuid import UUID

import stripe
from fastapi import FastAPI, Header, HTTPException, Request, Response
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from pydantic import BaseModel, ConfigDict, Field, StrictInt
from sqlalchemy import select, text
from starlette.concurrency import run_in_threadpool

from .database import create_session_factory
from .donor_email import EmailSettings, queue_login_email
from .donor_projections import dashboard, refresh_projection
from .donors import (
    DonorAccessDenied,
    GiftFrequency,
    ReferralKind,
    authenticated_donor,
    capture_referral,
    claim_account,
    sign_out,
)
from .persistence import DonorGiftRecord
from .public_api import RateLimiter
from .stripe_payments import (
    CheckoutConflict,
    PaymentSettings,
    PaymentUnavailable,
    StripeGateway,
    accept_event,
    begin_checkout,
    verify_event,
)

VISITOR_COOKIE = "dad_referral"
SESSION_COOKIE = "dad_donor_session"
MAX_BODY_BYTES = 256 * 1024


class CheckoutRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    email: str = Field(max_length=320)
    amount_minor: StrictInt = Field(ge=100, le=99_999_999)
    frequency: GiftFrequency = GiftFrequency.MONTHLY


class ReferralRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    kind: ReferralKind = ReferralKind.LINK


class LoginRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    email: str = Field(max_length=320)


class ClaimRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")
    token: str = Field(min_length=32, max_length=128)


def create_app(*, factory=None, settings=None, gateway=None, email_settings=None):
    settings = settings or PaymentSettings.from_env()
    factory = factory or create_session_factory()
    email_settings = email_settings or EmailSettings.from_env()
    app = FastAPI(title="DAD Donor API", version="0.1.0")
    limiter = RateLimiter(requests=20, window=60)

    @app.exception_handler(RequestValidationError)
    async def invalid_request(request, exc):
        # Pydantic's default error includes the supplied value, potentially a token.
        return JSONResponse(status_code=422, content={"detail": "Invalid request fields"})

    def browser_request(request):
        if request.headers.get("origin") != settings.origin:
            raise HTTPException(403, "Request origin is not allowed")
        address = request.client.host if request.client else "unknown"
        if not limiter.check(address)[0]:
            raise HTTPException(429, "Too many requests", headers={"Retry-After": "60"})

    @app.middleware("http")
    async def private_responses(request, call_next):
        response = await call_next(request)
        response.headers["Cache-Control"] = "no-store"
        response.headers["Referrer-Policy"] = "no-referrer"
        return response

    @app.post("/donor/login", status_code=202)
    def login(body: LoginRequest, request: Request):
        browser_request(request)
        if not email_settings.configured:
            raise HTTPException(503, "Account email is not configured yet")
        try:
            with factory.begin() as session:
                queue_login_email(session, body.email)
        except ValueError as exc:
            raise HTTPException(422, "Enter a valid email address") from exc
        return {"message": "If this email matches a donor account, a sign-in link will arrive shortly."}

    @app.post("/donor/claim")
    def claim(body: ClaimRequest, request: Request, response: Response):
        browser_request(request)
        try:
            with factory.begin() as session:
                token = claim_account(session, body.token)
                donor = authenticated_donor(session, token)
                refresh_projection(session, donor.id)
                # Replace this browser's old session, leaving other devices alone.
                sign_out(session, request.cookies.get(SESSION_COOKIE, ""))
        except DonorAccessDenied as exc:
            raise HTTPException(401, "This link is invalid or expired. Request a new link.") from exc
        response.set_cookie(SESSION_COOKIE, token, max_age=30 * 86400, httponly=True,
                            secure=settings.origin.startswith("https:"), samesite="lax", path="/")
        return {"claimed": True}

    @app.post("/donor/logout")
    def logout(request: Request, response: Response):
        browser_request(request)
        with factory.begin() as session:
            sign_out(session, request.cookies.get(SESSION_COOKIE, ""))
        response.delete_cookie(SESSION_COOKIE, path="/")
        return {"signed_out": True}

    @app.get("/donor/me/dashboard")
    def donor_dashboard(request: Request):
        try:
            with factory() as session:
                donor = authenticated_donor(session, request.cookies.get(SESSION_COOKIE, ""))
                return dashboard(session, donor.id)
        except DonorAccessDenied as exc:
            raise HTTPException(401, "Sign in required") from exc

    @app.get("/donor/me/gifts")
    def gifts(request: Request, offset: int = 0):
        if offset < 0:
            raise HTTPException(422, "Invalid offset")
        try:
            with factory() as session:
                donor = authenticated_donor(session, request.cookies.get(SESSION_COOKIE, ""))
                rows = session.scalars(select(DonorGiftRecord).where(
                    DonorGiftRecord.donor_id == donor.id,
                ).order_by(DonorGiftRecord.paid_at.desc(), DonorGiftRecord.id.desc())
                    .offset(offset).limit(51)).all()
                return {"email": donor.email, "mode": "test", "gifts": [
                    {"id": str(gift.id), "amount_minor": gift.amount_minor,
                     "currency": gift.currency, "frequency": gift.frequency,
                     "paid_at": gift.paid_at} for gift in rows[:50]
                ], "next_offset": offset + 50 if len(rows) > 50 else None}
        except DonorAccessDenied as exc:
            raise HTTPException(401, "Sign in required") from exc

    @app.get("/health/live")
    def live():
        return {"status": "alive"}

    @app.get("/health/ready")
    def ready():
        try:
            with factory() as session:
                session.execute(text("SELECT 1"))
        except Exception as exc:
            raise HTTPException(503, "Donor database unavailable") from exc
        return {"status": "ready", "paymentsConfigured": bool(settings.secret_key),
                "mode": "test"}

    @app.post("/donor/referrals/{code}")
    def referral(code: str, body: ReferralRequest, request: Request, response: Response):
        browser_request(request)
        with factory.begin() as session:
            token = capture_referral(session, code, body.kind,
                                     visitor_token=request.cookies.get(VISITOR_COOKIE))
        if token:
            response.set_cookie(VISITOR_COOKIE, token, max_age=30 * 86400,
                                httponly=True, secure=settings.origin.startswith("https:"),
                                samesite="lax", path="/")
        else:
            response.delete_cookie(VISITOR_COOKIE, path="/")
        return {"captured": token is not None}

    @app.post("/donor/checkouts", status_code=201)
    def checkout(body: CheckoutRequest, request: Request,
                 idempotency_key: Annotated[UUID, Header(alias="Idempotency-Key")]):
        browser_request(request)
        try:
            processor = gateway or StripeGateway(settings)
            return begin_checkout(factory, processor, settings, request_id=idempotency_key,
                                  email=body.email, amount_minor=body.amount_minor,
                                  frequency=body.frequency,
                                  visitor_token=request.cookies.get(VISITOR_COOKIE))
        except CheckoutConflict as exc:
            raise HTTPException(409, str(exc)) from exc
        except DonorAccessDenied as exc:
            raise HTTPException(403, "Giving is unavailable for this account") from exc
        except ValueError as exc:
            raise HTTPException(422, str(exc)) from exc
        except (stripe.StripeError, PaymentUnavailable) as exc:
            raise HTTPException(503, "Checkout is temporarily unavailable; retry the same request") from exc

    @app.post("/donor/stripe/webhook")
    async def webhook(request: Request):
        body = bytearray()
        async for chunk in request.stream():
            body.extend(chunk)
            if len(body) > MAX_BODY_BYTES:
                raise HTTPException(413, "Webhook payload is too large")
        try:
            verified = verify_event(bytes(body), request.headers.get("stripe-signature", ""), settings)
        except PaymentUnavailable as exc:
            raise HTTPException(503, "Webhook verification unavailable") from exc
        except (ValueError, stripe.SignatureVerificationError) as exc:
            raise HTTPException(400, "Invalid Stripe event") from exc
        try:
            await run_in_threadpool(accept_event, factory, verified)
        except (ValueError, KeyError, TypeError) as exc:
            raise HTTPException(400, "Invalid Stripe event shape") from exc
        return {"received": True}  # Returned only after the database transaction commits.

    return app


app = create_app()
