"""Durable donor login email delivery."""

import sqlalchemy as sa
from alembic import op

revision = "0018_donor_login_email"
down_revision = "0017_stripe_checkout"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table(
        "donor_login_emails",
        sa.Column("id", sa.UUID(), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False,
                  server_default=sa.func.now()),
        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
        sa.Column("encrypted_token", sa.LargeBinary()),
        sa.Column("expires_at", sa.DateTime(timezone=True)),
        sa.Column("available_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("completed_at", sa.DateTime(timezone=True)),
        sa.Column("attempts", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("last_error", sa.String(80)),
    )
    for column in ("donor_id", "available_at"):
        op.create_index(f"ix_donor_login_emails_{column}", "donor_login_emails", [column])


def downgrade():
    if op.get_bind().execute(sa.text(
        "SELECT count(*) FROM donor_login_emails WHERE completed_at IS NULL"
    )).scalar():
        raise RuntimeError("Pending donor email exists; drain before downgrade")
    op.drop_table("donor_login_emails")
