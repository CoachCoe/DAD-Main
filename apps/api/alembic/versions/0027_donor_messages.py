"""Durable financial and optional notification delivery."""
import sqlalchemy as sa
from alembic import op

revision = "0027_donor_messages"
down_revision = "0026_donor_preferences"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table("donor_messages",
        sa.Column("id", sa.UUID(), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
        sa.Column("dedup_key", sa.String(160), nullable=False, unique=True),
        sa.Column("kind", sa.String(20), nullable=False),
        sa.Column("context", sa.JSON(), nullable=False),
        sa.Column("available_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("completed_at", sa.DateTime(timezone=True)),
        sa.Column("attempts", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("last_error", sa.String(80)), sa.Column("suppressed_reason", sa.String(80)),
        sa.Column("subject", sa.String(200)), sa.Column("body", sa.Text()))
    for column in ("donor_id", "available_at"):
        op.create_index(f"ix_donor_messages_{column}", "donor_messages", [column])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_messages")).scalar():
        raise RuntimeError("Preserve delivery history before downgrade")
    op.drop_table("donor_messages")
