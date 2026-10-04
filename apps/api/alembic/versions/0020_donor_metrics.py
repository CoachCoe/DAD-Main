"""Persistent donor recognition and dashboard projections."""

import sqlalchemy as sa
from alembic import op

revision = "0020_donor_metrics"
down_revision = "0019_donor_financial_facts"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table(
        "donor_metric_projections",
        sa.Column("id", sa.UUID(), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False, unique=True),
        sa.Column("referrer_id", sa.UUID(), sa.ForeignKey("donors.id")),
        sa.Column("referral_kind", sa.String(4)),
        sa.Column("first_gift_id", sa.String(255)),
        sa.Column("net_minor", sa.BigInteger()),
        sa.Column("founding_awarded_at", sa.DateTime(timezone=True)),
        sa.Column("best_streak", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("current_streak", sa.Integer()),
        sa.Column("at_risk", sa.Boolean(), nullable=False, server_default="false"),
        sa.Column("grace_ends_at", sa.DateTime(timezone=True)),
        sa.Column("computed_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("next_refresh_at", sa.DateTime(timezone=True), nullable=False),
        sa.CheckConstraint("net_minor IS NULL OR net_minor >= 0", name="ck_donor_metrics_net"),
        sa.CheckConstraint("best_streak >= 0 AND (current_streak IS NULL OR current_streak >= 0)",
                           name="ck_donor_metrics_streak"),
        sa.CheckConstraint("(referrer_id IS NULL AND referral_kind IS NULL) OR "
                           "(referrer_id IS NOT NULL AND referral_kind IN ('LINK', 'IRL'))",
                           name="ck_donor_metrics_referral"),
    )
    for column in ("referrer_id", "next_refresh_at"):
        op.create_index(f"ix_donor_metric_projections_{column}", "donor_metric_projections", [column])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_metric_projections")).scalar():
        raise RuntimeError("Recognition records exist; preserve awards and best streaks before downgrade")
    op.drop_table("donor_metric_projections")
