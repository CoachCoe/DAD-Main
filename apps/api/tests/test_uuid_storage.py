"""Numeric-looking UUIDs must never acquire SQLite numeric affinity."""
from uuid import UUID

from sqlalchemy import select, text
from sqlalchemy.orm import Session

from impactgraph.persistence import DonorRecord


def test_numeric_uuid_round_trips_without_precision_loss(engine):
    identifiers = [UUID("12345678-1234-4234-8234-123456789012"),
                   UUID("12345678-1234-4234-8234-123456789013")]
    with Session(engine) as session:
        session.add_all(DonorRecord(id=identifier, email=f"{identifier}@example.org")
                        for identifier in identifiers)
        session.commit()
        session.expunge_all()
        assert set(session.scalars(select(DonorRecord.id))) == set(identifiers)
        for identifier in identifiers:
            assert session.get(DonorRecord, identifier).id == identifier
        if engine.dialect.name == "sqlite":
            assert set(session.scalars(text("SELECT typeof(id) FROM donors"))) == {"text"}
