"""Spreadsheet publications, ledger source documents, and partner interest forms."""
import sqlalchemy as sa
from alembic import op

revision = "0030_donor_public_content"
down_revision = "0029_donor_staff_actions"
branch_labels = None
depends_on = None


def base():
    return [sa.Column("id", sa.UUID(), primary_key=True),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now())]


def upgrade():
    op.create_table("donor_content_sources", *base(),
        sa.Column("actor_id", sa.UUID(), sa.ForeignKey("donor_staff.id"), nullable=False),
        sa.Column("name", sa.String(120), nullable=False), sa.Column("sha256", sa.String(64), nullable=False),
        sa.Column("encrypted_data", sa.LargeBinary(), nullable=False))
    op.create_table("donor_content_publications", *base(),
        sa.Column("actor_id", sa.UUID(), sa.ForeignKey("donor_staff.id"), nullable=False),
        sa.Column("payload", sa.JSON(), nullable=False))
    op.create_table("donor_content_head", sa.Column("id", sa.String(16), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.Column("publication_id", sa.UUID(), sa.ForeignKey("donor_content_publications.id")))
    op.create_table("donor_partner_signups", *base(),
        sa.Column("organization", sa.String(200), nullable=False), sa.Column("name", sa.String(100), nullable=False),
        sa.Column("email", sa.String(320), nullable=False), sa.Column("website", sa.String(500), nullable=False),
        sa.Column("message", sa.String(2000), nullable=False), sa.Column("request_hash", sa.String(64), nullable=False),
        sa.Column("consent_version", sa.String(80), nullable=False))
    op.create_index("ix_donor_partner_signups_email", "donor_partner_signups", ["email"])


def downgrade():
    for table in ("donor_content_sources", "donor_content_publications", "donor_partner_signups"):
        if op.get_bind().execute(sa.text(f"SELECT count(*) FROM {table}")).scalar():
            raise RuntimeError("Preserve publication sources and partner signups before downgrade")
    for table in ("donor_partner_signups", "donor_content_head", "donor_content_publications", "donor_content_sources"):
        op.drop_table(table)
