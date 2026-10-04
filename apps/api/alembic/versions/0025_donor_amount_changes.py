"""Audited recurring amounts, effective from a verified renewal boundary."""
import sqlalchemy as sa
from alembic import op

revision = "0025_donor_amount_changes"
down_revision = "0024_donor_resumptions"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table("donor_amount_changes",
                    sa.Column("id", sa.UUID(), primary_key=True),
                    sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
                    sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
                    sa.Column("checkout_id", sa.UUID(), sa.ForeignKey("donor_checkouts.id"), nullable=False),
                    sa.Column("previous_minor", sa.BigInteger(), nullable=False),
                    sa.Column("amount_minor", sa.BigInteger(), nullable=False),
                    sa.Column("effective_at", sa.DateTime(timezone=True), nullable=False),
                    sa.Column("item_id", sa.String(255), nullable=False),
                    sa.Column("product_id", sa.String(255), nullable=False),
                    sa.Column("confirmed_at", sa.DateTime(timezone=True)),
                    sa.CheckConstraint("amount_minor >= 100 AND amount_minor <= 99999999 AND previous_minor >= 100",
                                       name="ck_donor_amount_change_minor"))
    for column in ("donor_id", "checkout_id"):
        op.create_index(f"ix_donor_amount_changes_{column}", "donor_amount_changes", [column])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_amount_changes")).scalar():
        raise RuntimeError("Preserve amount change audit records before downgrade")
    op.drop_table("donor_amount_changes")
