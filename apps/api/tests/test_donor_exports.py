import csv
import io
from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from cryptography.fernet import Fernet
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph import donor_staff as staff
from impactgraph.donor_api import create_app
from impactgraph.donor_exports import DATASETS, safe_cell
from impactgraph.donors import guest_donor
from impactgraph.persistence import DonorMessageRecord, DonorStaffAuditRecord
from tests.test_donor_pauses import make_pause_setup
from tests.test_donor_staff import account
from tests.test_stripe_payments import SETTINGS


@pytest.fixture
def setup(engine, monkeypatch):
    monkeypatch.setenv("DONOR_STAFF_ENCRYPTION_KEY", Fernet.generate_key().decode())
    factory = sessionmaker(engine, expire_on_commit=False)
    token, _, _ = account(factory)
    client = TestClient(create_app(factory=factory, settings=SETTINGS), base_url=SETTINGS.origin)
    client.cookies.set(staff.COOKIE, token)
    return factory, client


def test_every_export_has_valid_allowlisted_columns_and_audit(setup):
    factory, client = setup
    with factory.begin() as session:
        donor = guest_donor(session, "person@example.org")
        donor.public_name = "=HYPERLINK(\"https://example.org\")"
    for dataset in DATASETS:
        response = client.get(f"/donor/staff/exports/{dataset}.csv")
        assert response.status_code == 200
        assert response.headers["Cache-Control"] == "no-store"
        rows = list(csv.reader(io.StringIO(response.text)))
        assert rows[0] == DATASETS[dataset][1].split()
        assert "token_hash" not in rows[0] and "password_hash" not in rows[0] and "totp_secret" not in rows[0]
        if dataset == "donors":
            assert rows[1][-1].startswith("'=HYPERLINK")
        if dataset == "enrolments":
            assert len(rows) == 1
    with factory() as session:
        finished = session.scalars(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "data.export_finished")).all()
        assert len(finished) == len(DATASETS)
        assert all(row.actor_id and row.details["completed"] for row in finished)
    assert client.get("/donor/staff/enrolments").json()["state"] == "not_active_until_r2"


def test_export_permissions_are_checked_before_streaming(setup):
    factory, client = setup
    support, _, _ = account(factory, "SUPPORT", "support@example.org")
    client.cookies.set(staff.COOKIE, support)
    assert client.get("/donor/staff/exports").status_code == 403
    assert client.get("/donor/staff/exports/gifts.csv").status_code == 403
    finance, _, _ = account(factory, "FINANCE", "finance@example.org")
    client.cookies.set(staff.COOKIE, finance)
    assert client.get("/donor/staff/exports/gifts.csv").status_code == 200
    assert client.get("/donor/staff/exports/staff.csv").status_code == 403
    assert client.get("/donor/staff/exports/audit.csv").status_code == 403
    assert "staff" not in client.get("/donor/staff/exports").json()["datasets"]
    client.cookies.clear()
    assert client.get("/donor/staff/exports/donors.csv").status_code == 401


@pytest.mark.parametrize("value", ["=1+1", " +cmd", "\t@sum(1)", "\ufeff-1+2", "\ntext"])
def test_csv_formula_prefixes_are_neutralized(value):
    assert safe_cell(value) == "'" + value
    assert safe_cell(3000) == 3000 and safe_cell(None) == ""


def test_receipt_resend_is_actor_bound_idempotent_and_audited(setup):
    factory, client = setup
    make_pause_setup(factory)
    with factory.begin() as session:
        message = session.scalar(select(DonorMessageRecord))
        message.created_at = datetime.now(UTC) - timedelta(minutes=2)
        message.completed_at = datetime.now(UTC) - timedelta(minutes=1)
        message_id = message.id
    key = str(uuid4())
    headers = {"Origin": SETTINGS.origin, "Idempotency-Key": key}
    path = f"/donor/staff/messages/{message_id}/resend"
    assert client.post(path, headers={"Idempotency-Key": key}).status_code == 403
    result = client.post(path, headers=headers)
    assert result.status_code == 200 and result.json()["queued"]
    assert client.post(path, headers=headers).json() == result.json()
    with factory() as session:
        rows = session.scalars(select(DonorMessageRecord)).all()
        assert len(rows) == 2
        assert session.scalar(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "message.resend_requested"))
    support, _, _ = account(factory, "SUPPORT", "support@example.org")
    client.cookies.set(staff.COOKIE, support)
    assert client.post(path, headers=headers).status_code == 409


def test_pending_message_is_not_duplicated_by_staff(setup):
    factory, client = setup
    make_pause_setup(factory)
    with factory.begin() as session:
        message = session.scalar(select(DonorMessageRecord))
        message.created_at = datetime.now(UTC) - timedelta(minutes=2)
        message_id = message.id
    result = client.post(f"/donor/staff/messages/{message_id}/resend", headers={"Origin": SETTINGS.origin, "Idempotency-Key": str(uuid4())})
    assert result.status_code == 409
    with factory() as session:
        assert len(session.scalars(select(DonorMessageRecord)).all()) == 1
