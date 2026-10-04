"""Persist invoice billing periods and reconciled refund facts."""

import sqlalchemy as sa
from alembic import op

revision = "0019_donor_financial_facts"
down_revision = "0018_donor_login_email"
branch_labels = None
depends_on = None


def base():
    return [sa.Column("id", sa.UUID(), primary_key=True),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False,
                      server_default=sa.func.now())]


def upgrade():
    op.create_table(
        "donor_billing_periods", *base(),
        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
        sa.Column("checkout_id", sa.UUID(), sa.ForeignKey("donor_checkouts.id"), nullable=False),
        sa.Column("stripe_invoice_id", sa.String(255), nullable=False, unique=True),
        sa.Column("stripe_subscription_id", sa.String(255), nullable=False),
        sa.Column("due_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("ends_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("status", sa.String(8), nullable=False),
        sa.CheckConstraint("ends_at > due_at", name="ck_donor_period_dates"),
        sa.CheckConstraint("status IN ('EXPECTED', 'FAILED', 'PAID')", name="ck_donor_period_status"),
    )
    op.create_index("ix_donor_billing_periods_donor_id", "donor_billing_periods", ["donor_id"])
    with op.batch_alter_table("donor_gifts") as batch:
        batch.add_column(sa.Column("stripe_payment_intent_id", sa.String(255)))
        batch.create_unique_constraint("uq_donor_gifts_payment_intent", ["stripe_payment_intent_id"])
        batch.add_column(sa.Column("period_id", sa.UUID()))
        batch.create_foreign_key("fk_donor_gifts_period", "donor_billing_periods", ["period_id"], ["id"])
    # Only one-time payment IDs can be recovered from existing gift rows without Stripe.
    op.execute("UPDATE donor_gifts SET stripe_payment_intent_id = stripe_object_id "
               "WHERE frequency = 'ONE_TIME' AND stripe_object_id LIKE 'pi_%'")
    op.create_table(
        "donor_refunds", *base(),
        sa.Column("gift_id", sa.UUID(), sa.ForeignKey("donor_gifts.id"), nullable=False),
        sa.Column("stripe_refund_id", sa.String(255), nullable=False, unique=True),
        sa.Column("amount_minor", sa.BigInteger(), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("status", sa.String(16), nullable=False),
        sa.Column("observed_at", sa.DateTime(timezone=True), nullable=False),
        sa.CheckConstraint("amount_minor > 0 AND currency = 'USD'", name="ck_donor_refund_money"),
        sa.CheckConstraint("status IN ('PENDING', 'REQUIRES_ACTION', 'SUCCEEDED', 'FAILED', 'CANCELED')",
                           name="ck_donor_refund_status"),
    )
    op.create_index("ix_donor_refunds_gift_id", "donor_refunds", ["gift_id"])
    op.create_table(
        "donor_refund_observations", *base(),
        sa.Column("refund_id", sa.UUID(), sa.ForeignKey("donor_refunds.id"), nullable=False),
        sa.Column("source_event_id", sa.UUID(), sa.ForeignKey("donor_stripe_events.id"),
                  nullable=False, unique=True),
        sa.Column("status", sa.String(16), nullable=False),
        sa.Column("observed_at", sa.DateTime(timezone=True), nullable=False),
    )
    op.create_index("ix_donor_refund_observations_refund_id", "donor_refund_observations", ["refund_id"])


def downgrade():
    if op.get_bind().execute(sa.text(
        "SELECT (SELECT count(*) FROM donor_billing_periods) + (SELECT count(*) FROM donor_refunds)"
    )).scalar():
        raise RuntimeError("Financial facts exist; reconcile before downgrade")
    op.drop_table("donor_refund_observations")
    op.drop_table("donor_refunds")
    with op.batch_alter_table("donor_gifts") as batch:
        batch.drop_constraint("fk_donor_gifts_period", type_="foreignkey")
        batch.drop_constraint("uq_donor_gifts_payment_intent", type_="unique")
        batch.drop_column("period_id")
        batch.drop_column("stripe_payment_intent_id")
    op.drop_table("donor_billing_periods")
