"""Staff financial commands and authoritative refund observations."""
import sqlalchemy as sa
from alembic import op

revision = "0029_donor_staff_actions"
down_revision = "0028_donor_staff"
branch_labels = None
depends_on = None


def upgrade():
    op.create_table("donor_staff_actions",
        sa.Column("id", sa.UUID(), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.Column("actor_id", sa.UUID(), sa.ForeignKey("donor_staff.id"), nullable=False),
        sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
        sa.Column("kind", sa.String(16), nullable=False), sa.Column("target_id", sa.UUID(), nullable=False),
        sa.Column("amount_minor", sa.BigInteger()), sa.Column("period_end", sa.DateTime(timezone=True)),
        sa.Column("reason", sa.String(500), nullable=False), sa.Column("stripe_refund_id", sa.String(255), unique=True),
        sa.Column("status", sa.String(16), nullable=False, server_default="AUTHORIZED"),
        sa.Column("completed_at", sa.DateTime(timezone=True)),
        sa.CheckConstraint("(kind = 'REFUND' AND amount_minor IS NOT NULL AND amount_minor > 0 AND period_end IS NULL) OR "
                           "(kind = 'CANCEL' AND amount_minor IS NULL AND period_end IS NOT NULL)", name="ck_donor_staff_action_shape"))
    for column in ("actor_id", "donor_id", "target_id"):
        op.create_index(f"ix_donor_staff_actions_{column}", "donor_staff_actions", [column])
    with op.batch_alter_table("donor_refund_observations") as batch:
        batch.alter_column("source_event_id", existing_type=sa.UUID(), nullable=True)
        batch.add_column(sa.Column("source_action_id", sa.UUID()))
        batch.create_foreign_key("fk_donor_refund_observation_action", "donor_staff_actions", ["source_action_id"], ["id"])
        batch.create_index("ix_donor_refund_observations_source_action_id", ["source_action_id"])
        batch.create_check_constraint("ck_donor_refund_observation_source", "(source_event_id IS NULL) <> (source_action_id IS NULL)")


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_staff_actions")).scalar():
        raise RuntimeError("Preserve staff financial audit records before downgrade")
    with op.batch_alter_table("donor_refund_observations") as batch:
        batch.drop_constraint("ck_donor_refund_observation_source", type_="check")
        batch.drop_index("ix_donor_refund_observations_source_action_id")
        batch.drop_constraint("fk_donor_refund_observation_action", type_="foreignkey")
        batch.drop_column("source_action_id")
        batch.alter_column("source_event_id", existing_type=sa.UUID(), nullable=False)
    op.drop_table("donor_staff_actions")
