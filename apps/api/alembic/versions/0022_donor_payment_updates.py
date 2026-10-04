"""Durable hosted payment-method setup attempts."""

import sqlalchemy as sa
from alembic import op

revision = "0022_donor_payment_updates"
down_revision = "0021_donor_cancellations"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table("donor_payment_updates",
                    sa.Column("id", sa.UUID(), primary_key=True),
                    sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
                    sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
                    sa.Column("attempt_id", sa.UUID(), sa.ForeignKey("donor_stripe_checkouts.id"), nullable=False),
                    sa.Column("customer_id", sa.String(255), nullable=False),
                    sa.Column("request_params", sa.JSON(), nullable=False),
                    sa.Column("stripe_session_id", sa.String(255), unique=True),
                    sa.Column("hosted_url", sa.Text()),
                    sa.Column("status", sa.String(16), nullable=False),
                    sa.Column("completed_at", sa.DateTime(timezone=True)))
    op.create_index("ix_donor_payment_updates_donor_id", "donor_payment_updates", ["donor_id"])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_payment_updates")).scalar():
        raise RuntimeError("Preserve payment-update audit records before downgrade")
    op.drop_table("donor_payment_updates")
