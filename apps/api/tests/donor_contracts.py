"""Export deterministic donor contracts; Alembic remains the schema migration owner."""
import json
from pathlib import Path

from sqlalchemy.dialects import postgresql
from sqlalchemy.schema import CreateIndex, CreateTable

from impactgraph.persistence import Base

ROOT = Path(__file__).resolve().parents[2] / "donor-api"


def artifacts():
    from impactgraph.donor_api import create_app
    from impactgraph.donor_email import EmailSettings
    from impactgraph.stripe_payments import PaymentSettings

    app = create_app(factory=lambda: None, settings=PaymentSettings(), email_settings=EmailSettings())
    tables = [t for t in Base.metadata.sorted_tables if t.name == "donors" or t.name.startswith("donor_")]
    ddl = "-- R1 donor baseline, compatible with the existing donor database.\n"
    for table in tables:
        ddl += str(CreateTable(table).compile(dialect=postgresql.dialect())).strip() + ";\n\n"
        for index in sorted(table.indexes, key=lambda index: index.name):
            ddl += str(CreateIndex(index).compile(dialect=postgresql.dialect())).strip() + ";\n"
    ddl = "\n".join(line.rstrip() for line in ddl.splitlines()) + "\n"
    return {ROOT / "db/migrations/0001_r1.sql": ddl,
            ROOT / "testdata/openapi.python.json": json.dumps(app.openapi(), indent=2, sort_keys=True) + "\n"}


if __name__ == "__main__":
    import base64
    import os

    # Importing the Python ASGI module also constructs its default app. Isolate that
    # startup from repository credentials; exporting contracts needs no live services.
    os.environ.update(DATABASE_URL="sqlite+pysqlite:///:memory:", AI_PROVIDER="mock",
        DEMO_MODE="local", STRIPE_SECRET_KEY="", STRIPE_WEBHOOK_SECRET="",
        DONOR_PUBLIC_ORIGIN="http://localhost:3000",
        EVIDENCE_ENCRYPTION_KEY=base64.b64encode(os.urandom(32)).decode())
    for path, content in artifacts().items():
        path.write_text(content)
