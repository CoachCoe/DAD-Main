"""Notification preferences and consent history."""
import sqlalchemy as sa
from alembic import op

revision = "0026_donor_preferences"
down_revision = "0025_donor_amount_changes"
branch_labels = None
depends_on = None


def upgrade():
    for table in ("donor_preferences", "donor_preference_events"):
        columns = [sa.Column("id", sa.UUID(), primary_key=True),
                   sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
                   sa.Column("donor_id", sa.UUID(), sa.ForeignKey("donors.id"), nullable=False),
                   sa.Column("version", sa.Integer(), nullable=False),
                   sa.Column("referral_emails", sa.Boolean(), nullable=False),
                   sa.Column("marketing_emails", sa.Boolean(), nullable=False)]
        if table == "donor_preferences":
            columns.append(sa.UniqueConstraint("donor_id"))
        else:
            columns += [sa.Column("consent_version", sa.String(80), nullable=False),
                        sa.Column("source", sa.String(40), nullable=False),
                        sa.UniqueConstraint("donor_id", "version", name="uq_donor_preference_version")]
        op.create_table(table, *columns)
    op.create_index("ix_donor_preference_events_donor_id", "donor_preference_events", ["donor_id"])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_preference_events")).scalar():
        raise RuntimeError("Preserve consent history before downgrade")
    op.drop_table("donor_preference_events")
    op.drop_table("donor_preferences")
