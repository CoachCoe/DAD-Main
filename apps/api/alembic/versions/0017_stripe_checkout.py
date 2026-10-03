"""Stripe checkout attempts, recurring reservations, verified inbox and collected gifts."""

import sqlalchemy as sa
from alembic import op

revision = "0017_stripe_checkout"
down_revision = "0016_donor_foundation"
branch_labels = None
depends_on = None


def _base():
    return [sa.Column("id", sa.UUID(), primary_key=True),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False,
                      server_default=sa.func.now())]


def upgrade():
    op.create_table(
        "donor_stripe_checkouts", *_base(),
        sa.Column("checkout_id", sa.UUID(), sa.ForeignKey("donor_checkouts.id"),
                  nullable=False, unique=True),
        sa.Column("request_hash", sa.String(64), nullable=False),
        sa.Column("request_params", sa.JSON(), nullable=False),
        sa.Column("stripe_session_id", sa.String(255), unique=True),
        sa.Column("stripe_subscription_id", sa.String(255), unique=True),
        sa.Column("hosted_url", sa.Text()),
        sa.Column("status", sa.String(16), nullable=False),
    )
    op.create_table(
        "donor_recurring_reservations", *_base(),
        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False, unique=True),
        sa.Column("attempt_id", sa.UUID(), sa.ForeignKey("donor_stripe_checkouts.id"),
                  nullable=False, unique=True),
    )
    op.create_table(
        "donor_stripe_events", *_base(),
        sa.Column("stripe_event_id", sa.String(255), nullable=False, unique=True),
        sa.Column("event_type", sa.String(80), nullable=False),
        sa.Column("occurred_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("data", sa.JSON(), nullable=False),
        sa.Column("processed_at", sa.DateTime(timezone=True)),
        sa.Column("attempts", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("last_error", sa.String(80)),
    )
    op.create_index("ix_donor_stripe_events_processed_at", "donor_stripe_events", ["processed_at"])
    op.create_table(
        "donor_gifts", *_base(),
        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
        sa.Column("checkout_id", sa.UUID(), sa.ForeignKey("donor_checkouts.id"), nullable=False),
        sa.Column("stripe_object_id", sa.String(255), nullable=False, unique=True),
        sa.Column("amount_minor", sa.BigInteger(), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("frequency", sa.String(8), nullable=False),
        sa.Column("paid_at", sa.DateTime(timezone=True), nullable=False),
        sa.CheckConstraint("amount_minor > 0 AND currency = 'USD'", name="ck_donor_gift_money"),
    )
    for column in ("donor_id", "checkout_id", "paid_at"):
        op.create_index(f"ix_donor_gifts_{column}", "donor_gifts", [column])


def downgrade():
    if op.get_bind().execute(sa.text(
        "SELECT (SELECT count(*) FROM donor_stripe_checkouts) + "
        "(SELECT count(*) FROM donor_stripe_events)"
    )).scalar():
        raise RuntimeError("Stripe checkout attempts exist; reconcile and migrate before downgrade")
    for table in ("donor_gifts", "donor_stripe_events", "donor_recurring_reservations",
                  "donor_stripe_checkouts"):
        op.drop_table(table)
