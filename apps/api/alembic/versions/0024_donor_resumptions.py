"""Audited early resumption at a confirmed renewal boundary."""

import sqlalchemy as sa
from alembic import op

revision = "0024_donor_resumptions"
down_revision = "0023_donor_pauses"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table("donor_resumptions",
                    sa.Column("id", sa.UUID(), primary_key=True),
                    sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
                    sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
                    sa.Column("pause_id", sa.UUID(), sa.ForeignKey("donor_pauses.id"), nullable=False, unique=True),
                    sa.Column("next_renewal", sa.DateTime(timezone=True), nullable=False),
                    sa.Column("confirmed_at", sa.DateTime(timezone=True)))
    op.create_index("ix_donor_resumptions_donor_id", "donor_resumptions", ["donor_id"])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_resumptions")).scalar():
        raise RuntimeError("Preserve resumption audit records before downgrade")
    op.drop_table("donor_resumptions")
