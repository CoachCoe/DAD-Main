import csv
import io
from uuid import UUID, uuid4

import pytest
from cryptography.fernet import Fernet
from fastapi.testclient import TestClient
from sqlalchemy import func, select
from sqlalchemy.orm import sessionmaker

from impactgraph import donor_staff as staff
from impactgraph.donor_api import create_app
from impactgraph.donor_content import INITIATIVE_COLUMNS, LEDGER_COLUMNS
from impactgraph.persistence import DonorContentSourceRecord, DonorPartnerSignupRecord
from tests.test_donor_staff import account
from tests.test_stripe_payments import SETTINGS


@pytest.fixture
def setup(engine, monkeypatch):
    monkeypatch.setenv("DONOR_STAFF_ENCRYPTION_KEY", Fernet.generate_key().decode())
    monkeypatch.setenv("DONOR_CONTENT_ENCRYPTION_KEY", Fernet.generate_key().decode())
    factory = sessionmaker(engine, expire_on_commit=False)
    token, _, _ = account(factory)
    client = TestClient(create_app(factory=factory, settings=SETTINGS), base_url=SETTINGS.origin)
    client.cookies.set(staff.COOKIE, token)
    return factory, client


def sheet(columns, *rows):
    stream = io.StringIO()
    writer = csv.writer(stream)
    writer.writerow(columns)
    writer.writerows(rows)
    return stream.getvalue()


def source(client):
    response = client.post("/donor/staff/content/sources?name=approval.txt", content=b"Board approval and source figures",
                           headers={"Origin": SETTINGS.origin})
    assert response.status_code == 200
    return response.json()["id"]


def publication(source_id):
    return {"initiatives_csv": sheet(INITIATIVE_COLUMNS, ["first-plan", "First plan", "Approved plan", "Partner",
            "2027-04-22", source_id, "2026-01-01"]),
            "ledger_csv": sheet(LEDGER_COLUMNS, ["Collected", "3000", "USD_CENTS", source_id, "false"],
                                ["Example reach", "100", "PEOPLE", "", "true"]),
            "as_of": "2026-01-01", "cadence": "monthly"}


def test_public_snapshot_requires_sources_and_keeps_them_private(setup):
    factory, client = setup
    empty = client.get("/donor/public/content")
    assert empty.json()["ledger"] == [] and empty.json()["state"] == "unpublished"
    assert empty.headers["Cache-Control"] == "public, max-age=60"
    source_id = source(client)
    with factory() as session:
        stored = session.get(DonorContentSourceRecord, UUID(source_id))
        assert b"Board approval" not in stored.encrypted_data
    response = client.post("/donor/staff/content/publish", json=publication(source_id), headers={"Origin": SETTINGS.origin})
    assert response.status_code == 200
    public = client.get("/donor/public/content")
    assert public.json()["ledger"][0]["value"] == 3000
    assert public.json()["ledger"][1]["illustrative"] is True
    assert source_id not in public.text and "approved_on" not in public.text
    assert client.get(f"/donor/staff/content/sources/{source_id}").content == b"Board approval and source figures"
    client.cookies.clear()
    assert client.get(f"/donor/staff/content/sources/{source_id}").status_code == 401
    assert client.get("/donor/public/content").json() == public.json()


@pytest.mark.parametrize("fault", ["missing_source", "unlabeled", "decimal_money", "duplicate_slug", "bad_headers", "malformed_header"])
def test_invalid_publication_leaves_previous_snapshot_intact(setup, fault):
    _, client = setup
    body = publication(source(client))
    assert client.post("/donor/staff/content/publish", json=body, headers={"Origin": SETTINGS.origin}).status_code == 200
    previous = client.get("/donor/public/content").json()
    if fault == "missing_source":
        body["ledger_csv"] = sheet(LEDGER_COLUMNS, ["Collected", "3000", "USD_CENTS", str(uuid4()), "false"])
    elif fault == "unlabeled":
        body["ledger_csv"] = sheet(LEDGER_COLUMNS, ["Example", "100", "PEOPLE", "", "false"])
    elif fault == "decimal_money":
        body["ledger_csv"] = sheet(LEDGER_COLUMNS, ["Example", "30.00", "USD_CENTS", "", "true"])
    elif fault == "duplicate_slug":
        body["initiatives_csv"] += body["initiatives_csv"].splitlines(keepends=True)[1]
    elif fault == "malformed_header":
        body["ledger_csv"] = '"unterminated header'
    else:
        body["ledger_csv"] = "unexpected,headers\n"
    assert client.post("/donor/staff/content/publish", json=body, headers={"Origin": SETTINGS.origin}).status_code == 409
    assert client.get("/donor/public/content").json() == previous


def test_content_publication_requires_named_permission_and_same_origin(setup):
    factory, client = setup
    body = publication(source(client))
    assert client.post("/donor/staff/content/publish", json=body).status_code == 403
    finance, _, _ = account(factory, "FINANCE", "finance@example.org")
    client.cookies.set(staff.COOKIE, finance)
    assert client.post("/donor/staff/content/publish", json=body, headers={"Origin": SETTINGS.origin}).status_code == 403
    assert client.get("/donor/staff/partners").status_code == 403


def test_partner_signup_requires_consent_and_is_idempotent_without_an_account(setup):
    factory, client = setup
    client.cookies.clear()
    body = {"organization": "Organization", "name": "Contact", "email": "contact@example.org",
            "website": "https://example.org", "message": "Interested", "contact_consent": True}
    headers = {"Origin": SETTINGS.origin, "Idempotency-Key": str(uuid4())}
    path = "/donor/public/partner-signup"
    assert client.post(path, json=body, headers={"Idempotency-Key": headers["Idempotency-Key"]}).status_code == 403
    assert client.post(path, json={**body, "contact_consent": False}, headers=headers).status_code == 422
    assert client.post(path, json=body, headers=headers).json() == {"received": True, "mode": "test"}
    assert client.post(path, json=body, headers=headers).status_code == 200
    assert client.post(path, json={**body, "message": "Changed"}, headers=headers).status_code == 409
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorPartnerSignupRecord)) == 1
        row = session.scalar(select(DonorPartnerSignupRecord))
        assert row.consent_version == "r1-partner-contact-v1"
