"""Isolated donor schemas, exercised on PostgreSQL when TEST_POSTGRES_URL is set."""

import os
from uuid import uuid4

import pytest
from sqlalchemy import create_engine, event
from sqlalchemy.schema import CreateSchema, DropSchema

from impactgraph.persistence import Base


@pytest.fixture(params=["sqlite"] + (["postgres"] if os.getenv("TEST_POSTGRES_URL") else []))
def engine(request, tmp_path):
    postgres = request.param == "postgres"
    engine = create_engine(os.environ["TEST_POSTGRES_URL"] if postgres else
                           f"sqlite+pysqlite:///{tmp_path / 'donors.db'}")
    schema = f"donors_{uuid4().hex}" if postgres else None
    if postgres:
        with engine.begin() as connection:
            connection.execute(CreateSchema(schema))
        engine = engine.execution_options(schema_translate_map={None: schema})
    else:
        @event.listens_for(engine, "connect")
        def foreign_keys(connection, _):
            connection.execute("PRAGMA foreign_keys=ON")

    tables = [table for table in Base.metadata.sorted_tables
              if table.name == "donors" or table.name.startswith("donor_")]
    try:
        Base.metadata.create_all(engine, tables=tables)
        yield engine
    finally:
        if postgres:
            with engine.begin() as connection:
                connection.execute(DropSchema(schema, cascade=True))
        engine.dispose()

