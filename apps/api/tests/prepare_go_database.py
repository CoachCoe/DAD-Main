"""Apply the existing migrations to the dedicated donor_go integration-test database."""
import base64
import os
from urllib.parse import urlsplit


def main():
    url = os.environ.get("TEST_POSTGRES_URL", "")
    if urlsplit(url).path != "/donor_go":
        raise SystemExit("TEST_POSTGRES_URL must explicitly target the dedicated donor_go test database")
    os.environ.update(DATABASE_URL=url, AI_PROVIDER="mock", DEMO_MODE="local",
                      EVIDENCE_ENCRYPTION_KEY=base64.b64encode(os.urandom(32)).decode())
    from alembic.config import Config

    from alembic import command

    command.upgrade(Config("alembic.ini"), "head")


if __name__ == "__main__":
    main()
