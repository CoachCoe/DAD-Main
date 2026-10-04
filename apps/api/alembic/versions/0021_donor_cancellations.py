"""Verified recurring schedules and durable donor cancellation commands."""

import sqlalchemy as sa
from alembic import op

revision = "0021_donor_cancellations"
down_revision = "0020_donor_metrics"
branch_labels = None
depends_on = None


def upgrade():
    for name, columns in (
        ("donor_subscriptions", [
            sa.Column("attempt_id", sa.UUID(), sa.ForeignKey("donor_stripe_checkouts.id"), nullable=False, unique=True),
            sa.Column("status", sa.String(32), nullable=False),
            sa.Column("period_end", sa.DateTime(timezone=True), nullable=False),
            sa.Column("stops_at", sa.DateTime(timezone=True)),
            sa.Column("observed_at", sa.DateTime(timezone=True), nullable=False),
        ]),
        ("donor_cancellations", [
            sa.Column("attempt_id", sa.UUID(), sa.ForeignKey("donor_stripe_checkouts.id"), nullable=False),
            sa.Column("confirmed_period_end", sa.DateTime(timezone=True), nullable=False),
            sa.Column("completed_at", sa.DateTime(timezone=True)),
        ]),
    ):
        op.create_table(name,
                        sa.Column("id", sa.UUID(), primary_key=True),
                        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
                        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
                        *columns)
        op.create_index(f"ix_{name}_donor_id", name, ["donor_id"])


def downgrade():
    for name in ("donor_subscriptions", "donor_cancellations"):
        if op.get_bind().execute(sa.text(f"SELECT count(*) FROM {name}")).scalar():
            raise RuntimeError("Preserve billing schedules and cancellation audit records before downgrade")
    op.drop_table("donor_cancellations")
    op.drop_table("donor_subscriptions")
