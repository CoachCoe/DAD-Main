"""Authorized monthly pause schedules and acknowledgements."""

import sqlalchemy as sa
from alembic import op

revision = "0023_donor_pauses"
down_revision = "0022_donor_payment_updates"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table("donor_pauses",
                    sa.Column("id", sa.UUID(), primary_key=True),
                    sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
                    sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
                    sa.Column("attempt_id", sa.UUID(), sa.ForeignKey("donor_stripe_checkouts.id"), nullable=False),
                    sa.Column("months", sa.Integer(), nullable=False),
                    sa.Column("starts_at", sa.DateTime(timezone=True), nullable=False),
                    sa.Column("resumes_at", sa.DateTime(timezone=True), nullable=False),
                    sa.Column("confirmed_at", sa.DateTime(timezone=True)),
                    sa.CheckConstraint("months IN (1, 2, 3)", name="ck_donor_pause_months"),
                    sa.CheckConstraint("resumes_at > starts_at", name="ck_donor_pause_dates"))
    for column in ("donor_id", "attempt_id"):
        op.create_index(f"ix_donor_pauses_{column}", "donor_pauses", [column])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_pauses")).scalar():
        raise RuntimeError("Preserve pause schedules and audit records before downgrade")
    op.drop_table("donor_pauses")
