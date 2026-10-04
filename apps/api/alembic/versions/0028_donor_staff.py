"""Individual staff identity, MFA, sessions, and audit history."""
import sqlalchemy as sa
from alembic import op

revision = "0028_donor_staff"
down_revision = "0027_donor_messages"
branch_labels = None
depends_on = None


def base():
    return [sa.Column("id", sa.UUID(), primary_key=True),
            sa.Column("created_at", sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now())]


def upgrade():
    op.create_table("donor_staff", *base(),
        sa.Column("email", sa.String(320), nullable=False, unique=True),
        sa.Column("name", sa.String(100), nullable=False), sa.Column("role", sa.String(16), nullable=False),
        sa.Column("disabled", sa.Boolean(), nullable=False, server_default="false"),
        sa.Column("password_hash", sa.String(255)), sa.Column("totp_secret", sa.LargeBinary()),
        sa.Column("totp_counter", sa.BigInteger(), nullable=False, server_default="-1"),
        sa.Column("failures", sa.Integer(), nullable=False, server_default="0"),
        sa.Column("locked_until", sa.DateTime(timezone=True)),
        sa.CheckConstraint("role IN ('SUPPORT', 'FINANCE', 'ADMIN')", name="ck_donor_staff_role"))
    op.create_table("donor_staff_invites", *base(),
        sa.Column("staff_id", sa.UUID(), sa.ForeignKey("donor_staff.id"), nullable=False),
        sa.Column("token_hash", sa.String(64), nullable=False, unique=True),
        sa.Column("expires_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("consumed_at", sa.DateTime(timezone=True)), sa.Column("pending_secret", sa.LargeBinary()),
        sa.Column("attempts", sa.Integer(), nullable=False, server_default="0"))
    op.create_table("donor_staff_sessions", *base(),
        sa.Column("staff_id", sa.UUID(), sa.ForeignKey("donor_staff.id"), nullable=False),
        sa.Column("token_hash", sa.String(64), nullable=False, unique=True),
        sa.Column("expires_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("revoked_at", sa.DateTime(timezone=True)))
    op.create_table("donor_staff_audit", *base(),
        sa.Column("actor_id", sa.UUID(), sa.ForeignKey("donor_staff.id")),
        sa.Column("action", sa.String(80), nullable=False), sa.Column("target", sa.String(160), nullable=False),
        sa.Column("details", sa.JSON(), nullable=False))
    for table, column in (("donor_staff_invites", "staff_id"), ("donor_staff_sessions", "staff_id"),
                          ("donor_staff_audit", "actor_id"), ("donor_staff_audit", "action")):
        op.create_index(f"ix_{table}_{column}", table, [column])


def downgrade():
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donor_staff")).scalar():
        raise RuntimeError("Preserve staff identity and audit records before downgrade")
    for table in ("donor_staff_audit", "donor_staff_sessions", "donor_staff_invites", "donor_staff"):
        op.drop_table(table)
