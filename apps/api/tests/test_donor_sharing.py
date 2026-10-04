from datetime import UTC, datetime, timedelta

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_api import SESSION_COOKIE, create_app
from impactgraph.donor_projections import refresh_projection
from impactgraph.donor_sharing import referral_svg
from impactgraph.donors import activate_sharing, claim_account, guest_donor, issue_login_link
from impactgraph.persistence import DonorCheckoutRecord, DonorRecord, ReferralTouchRecord
from impactgraph.stripe_payments import PaymentSettings, accept_event, process_next_event
from tests.test_stripe_payments import Gateway, event_for

ORIGIN = "https://dad.example"


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


def owner(factory, email="owner@example.org", name=None):
    with factory.begin() as session:
        donor = guest_donor(session, email)
        secret = claim_account(session, issue_login_link(session, email))
        if name:
            activate_sharing(session, secret, name)
        refresh_projection(session, donor.id)
    return secret


def browser(factory, token=None):
    result = TestClient(create_app(factory=factory, settings=PaymentSettings(origin=ORIGIN),
                                  gateway=Gateway()), base_url=ORIGIN)
    if token:
        result.cookies.set(SESSION_COOKIE, token)
    return result


def post(client, route, data):
    return client.post(route, json=data, headers={"Origin": ORIGIN})


def test_sharing_requires_identity_and_explicit_public_name(factory):
    guest = browser(factory)
    assert guest.get("/donor/me/sharing").status_code == 401
    assert guest.get("/donor/me/referral-code.svg").status_code == 401
    client = browser(factory, owner(factory))
    details = client.get("/donor/me/sharing")
    assert details.json() == {"public_name": None, "link": None, "irl_link": None}
    assert client.get("/donor/me/referral-code.svg").status_code == 409
    assert client.post("/donor/me/sharing", json={"public_name": "Sam"}).status_code == 403
    assert post(client, "/donor/me/sharing", {"public_name": "owner@example.org"}).status_code == 422
    saved = post(client, "/donor/me/sharing", {"public_name": "Sam"})
    assert saved.status_code == 200
    assert saved.json()["public_name"] == "Sam"
    assert saved.json()["link"].startswith(ORIGIN + "/r/")
    assert saved.json()["irl_link"] == saved.json()["link"] + "?via=irl"
    assert "owner@example.org" not in saved.text
    assert client.get("/donor/me/sharing").headers["cache-control"] == "no-store"
    image = client.get("/donor/me/referral-code.svg")
    assert image.status_code == 200 and image.headers["content-type"].startswith("image/svg+xml")
    assert image.headers["content-disposition"] == "inline"
    assert image.headers["cache-control"] == "no-store"
    assert b"<svg" in image.content and b"<path" in image.content
    assert b"owner@example.org" not in image.content


def test_code_uses_irl_url_high_correction_and_quiet_zone(monkeypatch):
    import qrcode

    real = qrcode.QRCode
    captured = []

    def record(**kwargs):
        result = real(**kwargs)
        captured.append(result)
        return result

    monkeypatch.setattr("impactgraph.donor_sharing.qrcode.QRCode", record)
    url = ORIGIN + "/r/test-public-code?via=irl"
    svg = referral_svg(url)
    assert captured[0].error_correction == qrcode.constants.ERROR_CORRECT_H
    assert captured[0].border == 4
    assert b"".join(chunk.data for chunk in captured[0].data_list) == url.encode()
    assert b"fill=\"white\"" in svg


def test_link_then_irl_preserves_first_referrer_channel_and_deadline(factory):
    first = browser(factory, owner(factory, name="First"))
    second = browser(factory, owner(factory, "second@example.org", "Second"))
    first_code = first.get("/donor/me/sharing").json()["link"].rsplit("/", 1)[-1]
    second_code = second.get("/donor/me/sharing").json()["link"].rsplit("/", 1)[-1]
    visitor = browser(factory)
    initial = post(visitor, f"/donor/referrals/{first_code}", {"kind": "LINK"})
    assert initial.json() == {"captured": True, "public_name": "First"}
    with factory() as session:
        expires = session.scalar(select(ReferralTouchRecord)).expires_at
    later = post(visitor, f"/donor/referrals/{second_code}", {"kind": "IRL"})
    assert later.json()["public_name"] == "First"
    with factory() as session:
        rows = session.scalars(select(ReferralTouchRecord)).all()
        assert len(rows) == 1 and rows[0].kind == "LINK" and rows[0].expires_at == expires


def test_replacement_invalidates_new_visits_but_not_captured_attribution(factory):
    client = browser(factory, owner(factory, name="Sam"))
    old = client.get("/donor/me/sharing").json()["link"].rsplit("/", 1)[-1]
    existing = browser(factory)
    post(existing, f"/donor/referrals/{old}", {"kind": "IRL"})
    replaced = post(client, "/donor/me/sharing", {"public_name": "Sam", "replace_code": True}).json()
    assert not replaced["link"].endswith(old)
    fresh = browser(factory)
    assert post(fresh, f"/donor/referrals/{old}", {"kind": "LINK"}).json()["captured"] is False
    assert post(existing, f"/donor/referrals/{old}", {"kind": "LINK"}).json()["captured"] is True
    with factory.begin() as session:
        session.scalar(select(ReferralTouchRecord)).expires_at = datetime.now(UTC) - timedelta(seconds=1)
    new = replaced["link"].rsplit("/", 1)[-1]
    assert post(existing, f"/donor/referrals/{new}", {"kind": "LINK"}).json()["captured"] is True
    with factory() as session:
        assert len(session.scalars(select(ReferralTouchRecord)).all()) == 2


def test_irl_guest_checkout_credits_owner_before_guest_claims(factory):
    from uuid import uuid4

    client = browser(factory, owner(factory, name="Sam"))
    code = client.get("/donor/me/sharing").json()["link"].rsplit("/", 1)[-1]
    visitor = browser(factory)
    post(visitor, f"/donor/referrals/{code}", {"kind": "IRL"})
    result = visitor.post("/donor/checkouts", json={"email": "guest@example.org", "amount_minor": 3000,
                                                  "frequency": "ONE_TIME"},
                          headers={"Origin": ORIGIN, "Idempotency-Key": str(uuid4())})
    assert result.status_code == 201
    accept_event(factory, event_for(result.json()))
    assert process_next_event(factory)
    metrics = client.get("/donor/me/dashboard").json()
    assert metrics["referrals"]["irl"] == 1 and metrics["referrals"]["link"] == 0
    assert metrics["referrals"]["total_minor"] == 3000
    with factory() as session:
        guest = session.scalar(select(DonorRecord).where(DonorRecord.email == "guest@example.org"))
        assert guest.claimed_at is None
        draft = session.scalar(select(DonorCheckoutRecord).where(DonorCheckoutRecord.donor_id == guest.id))
        assert draft.referral_kind == "IRL"
