"""Approved spreadsheet snapshots, defensible ledger sources, and partner interest."""
import csv
import hashlib
import io
import json
import os
import re
from datetime import UTC, date, datetime
from urllib.parse import urlsplit
from uuid import UUID

from cryptography.fernet import Fernet
from sqlalchemy import select
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.dialects.sqlite import insert as sqlite_insert

from . import donor_staff as staff
from .donor_receipts import YEAR_ZONE
from .donors import normalize_email
from .persistence import (
    DonorContentHeadRecord,
    DonorContentPublicationRecord,
    DonorContentSourceRecord,
    DonorPartnerSignupRecord,
)

INITIATIVE_COLUMNS = ["slug", "title", "summary", "partner", "planned_start", "board_approval_source_id", "approved_on"]
LEDGER_COLUMNS = ["metric", "value", "unit", "source_id", "illustrative"]


def content_cipher():
    key = os.getenv("DONOR_CONTENT_ENCRYPTION_KEY", "")
    if not key:
        raise ValueError("Content-source encryption is not configured")
    return Fernet(key.encode())


def store_source(session, actor, name, data):
    if not data or len(data) > 10 * 1024 * 1024 or not 1 <= len(name) <= 120 or any(ord(c) < 32 for c in name):
        raise ValueError("Invalid source document")
    row = DonorContentSourceRecord(actor_id=actor.id, name=name, sha256=hashlib.sha256(data).hexdigest(),
                                    encrypted_data=content_cipher().encrypt(data))
    session.add(row)
    session.flush()
    staff.audit(session, actor.id, "content.source_uploaded", row.id, sha256=row.sha256)
    return {"id": str(row.id), "name": row.name, "sha256": row.sha256}


def read_source(session, actor, source_id):
    row = session.get(DonorContentSourceRecord, source_id)
    if row is None:
        raise LookupError("Source not found")
    data = content_cipher().decrypt(row.encrypted_data)
    if hashlib.sha256(data).hexdigest() != row.sha256:
        raise ValueError("Source integrity mismatch")
    staff.audit(session, actor.id, "content.source_read", row.id)
    return data


def _rows(value, columns):
    if len(value.encode()) > 512 * 1024:
        raise ValueError("Spreadsheet exceeds 512 KB")
    reader = csv.DictReader(io.StringIO(value.lstrip("\ufeff"), newline=""), strict=True)
    try:
        if reader.fieldnames != columns:
            raise ValueError("Spreadsheet headers must be: " + ",".join(columns))
        rows = list(reader)
    except csv.Error as exc:
        raise ValueError("Malformed CSV") from exc
    if len(rows) > 500 or any(None in row or any(value is None for value in row.values()) for row in rows):
        raise ValueError("Malformed spreadsheet or too many rows")
    return rows


def _source(session, value):
    try:
        source_id = UUID(value)
    except (ValueError, TypeError) as exc:
        raise ValueError("A stored source document is required") from exc
    if session.get(DonorContentSourceRecord, source_id) is None:
        raise ValueError("Source document was not uploaded")
    return str(source_id)


def publish(session, actor, initiatives_csv, ledger_csv, as_of, cadence):
    today = datetime.now(UTC).astimezone(YEAR_ZONE).date()
    if as_of > today or not 1 <= len(cadence.strip()) <= 100:
        raise ValueError("Confirm a past/current as-of date and update cadence")
    initiatives, ledger, slugs = [], [], set()
    for row in _rows(initiatives_csv, INITIATIVE_COLUMNS):
        if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", row["slug"]) or len(row["slug"]) > 100 or row["slug"] in slugs:
            raise ValueError("Initiative slugs must be unique lowercase words separated by hyphens")
        if any(not row[key].strip() or len(row[key]) > maximum for key, maximum in (("title", 150), ("summary", 4000), ("partner", 200))):
            raise ValueError("Each initiative needs a title, summary, and partner")
        if date.fromisoformat(row["approved_on"]) > today:
            raise ValueError("Board approval cannot be in the future")
        date.fromisoformat(row["planned_start"])
        row["board_approval_source_id"] = _source(session, row["board_approval_source_id"])
        initiatives.append(row)
        slugs.add(row["slug"])
    for row in _rows(ledger_csv, LEDGER_COLUMNS):
        if (not 1 <= len(row["metric"].strip()) <= 150 or not re.fullmatch(r"[0-9]{1,15}", row["value"])
                or row["unit"] not in {"USD_CENTS", "PEOPLE", "INITIATIVES", "PARTNERS"}
                or row["illustrative"] not in {"true", "false"}):
            raise ValueError("Invalid ledger metric, nonnegative integer value, unit, or illustrative flag")
        illustrative = row["illustrative"] == "true"
        source_id = _source(session, row["source_id"]) if row["source_id"] or not illustrative else None
        ledger.append({"metric": row["metric"], "value": int(row["value"]), "unit": row["unit"],
                       "source_id": source_id, "illustrative": illustrative})
    # One head lock makes a publication atomic even when two staff members publish.
    insert = pg_insert if session.bind.dialect.name == "postgresql" else sqlite_insert
    session.execute(insert(DonorContentHeadRecord).values(id="r1").on_conflict_do_nothing(index_elements=["id"]))
    head = session.scalar(select(DonorContentHeadRecord).where(DonorContentHeadRecord.id == "r1").with_for_update())
    row = DonorContentPublicationRecord(actor_id=actor.id, created_at=datetime.now(UTC), payload={
        "initiatives": initiatives, "ledger": ledger, "ledger_as_of": as_of.isoformat(), "cadence": cadence.strip()})
    session.add(row)
    session.flush()
    head.publication_id = row.id
    staff.audit(session, actor.id, "content.published", row.id, initiatives=len(initiatives), metrics=len(ledger))
    return {"publication_id": str(row.id)}


def public_content(session):
    row = session.scalar(select(DonorContentPublicationRecord).join(DonorContentHeadRecord,
        DonorContentHeadRecord.publication_id == DonorContentPublicationRecord.id).where(DonorContentHeadRecord.id == "r1"))
    if row is None:
        return {"state": "unpublished", "initiatives": [], "ledger": [], "ledger_as_of": None, "cadence": None}
    return {"state": "published", "published_at": row.created_at, "publication_id": str(row.id),
            "ledger_as_of": row.payload["ledger_as_of"], "cadence": row.payload["cadence"],
            "initiatives": [{key: value for key, value in item.items() if key not in {"board_approval_source_id", "approved_on"}}
                            for item in row.payload["initiatives"]],
            "ledger": [{key: value for key, value in item.items() if key != "source_id"} for item in row.payload["ledger"]]}


def partner_signup(session, request_id, data):
    if request_id.version != 4:
        raise ValueError("Use a UUID v4 request key")
    data = {key: value.strip() for key, value in data.items()}
    data["email"] = normalize_email(data["email"])
    website = urlsplit(data["website"])
    if data["website"] and (website.scheme != "https" or not website.hostname or website.username or website.password):
        raise ValueError("Use an HTTPS organization website")
    if not data["organization"] or not data["name"]:
        raise ValueError("Provide organization and contact name")
    digest = hashlib.sha256(json.dumps(data, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    insert = pg_insert if session.bind.dialect.name == "postgresql" else sqlite_insert
    session.execute(insert(DonorPartnerSignupRecord).values(id=request_id, **data, request_hash=digest,
        consent_version="r1-partner-contact-v1").on_conflict_do_nothing(index_elements=["id"]))
    row = session.get(DonorPartnerSignupRecord, request_id)
    if row and row.request_hash != digest:
        raise ValueError("Signup key already used")

    return {"received": True, "mode": "test"}
