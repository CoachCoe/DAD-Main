"""Guest donors, passwordless credentials and pre-account referral attribution."""

import sqlalchemy as sa

from alembic import op

revision = "0016_donor_foundation"
down_revision = "0015_unrestricted_funding"
branch_labels = None
depends_on = None


def _base():
    return [
        sa.Column("id", sa.UUID(as_uuid=True), primary_key=True),
        sa.Column("created_at", sa.DateTime(timezone=True), nullable=False,
                  server_default=sa.func.now()),
    ]


def upgrade() -> None:
    op.create_table(
        "donors", *_base(),
        sa.Column("email", sa.String(320), nullable=False, unique=True),
        sa.Column("claimed_at", sa.DateTime(timezone=True)),
        sa.Column("disabled", sa.Boolean(), nullable=False, server_default=sa.false()),
        sa.Column("public_name", sa.String(80)),
        sa.Column("referral_code", sa.String(64), unique=True),
    )
    for name, terminal in (("donor_login_links", "consumed_at"), ("donor_sessions", "revoked_at")):
        op.create_table(
            name, *_base(),
            sa.Column("donor_id", sa.UUID(as_uuid=True), sa.ForeignKey("donors.id"), nullable=False),
            sa.Column("token_hash", sa.String(64), nullable=False, unique=True),
            sa.Column("expires_at", sa.DateTime(timezone=True), nullable=False),
            sa.Column(terminal, sa.DateTime(timezone=True)),
        )
        op.create_index(f"ix_{name}_donor_id", name, ["donor_id"])
    op.create_table(
        "donor_referral_touches", *_base(),
        sa.Column("token_hash", sa.String(64), nullable=False, unique=True),
        sa.Column("referrer_id", sa.UUID(as_uuid=True), sa.ForeignKey("donors.id"), nullable=False),
        sa.Column("kind", sa.String(4), nullable=False),
        sa.Column("touched_at", sa.DateTime(timezone=True), nullable=False),
        sa.Column("expires_at", sa.DateTime(timezone=True), nullable=False),
        sa.CheckConstraint("kind IN ('LINK', 'IRL')", name="ck_donor_touch_kind"),
    )
    op.create_index("ix_donor_referral_touches_referrer_id", "donor_referral_touches", ["referrer_id"])
    op.create_table(
        "donor_checkouts", *_base(),
        sa.Column("donor_id", sa.UUID(as_uuid=True), sa.ForeignKey("donors.id"), nullable=False),
        sa.Column("amount_minor", sa.BigInteger(), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False, server_default="USD"),
        sa.Column("frequency", sa.String(8), nullable=False),
        sa.Column("referrer_id", sa.UUID(as_uuid=True), sa.ForeignKey("donors.id")),
        sa.Column("referral_kind", sa.String(4)),
        sa.Column("referral_expires_at", sa.DateTime(timezone=True)),
        sa.CheckConstraint("amount_minor >= 100", name="ck_donor_checkout_minimum"),
        sa.CheckConstraint("currency = 'USD'", name="ck_donor_checkout_currency"),
        sa.CheckConstraint("frequency IN ('MONTHLY', 'ONE_TIME')", name="ck_donor_frequency"),
        sa.CheckConstraint(
            "(referrer_id IS NULL AND referral_kind IS NULL AND referral_expires_at IS NULL) "
            "OR (referrer_id IS NOT NULL AND referral_kind IS NOT NULL "
            "AND referral_kind IN ('LINK', 'IRL') AND referral_expires_at IS NOT NULL)",
            name="ck_donor_checkout_attribution",
        ),
        sa.CheckConstraint("referrer_id IS NULL OR referrer_id <> donor_id",
                           name="ck_donor_checkout_self_referral"),
    )
    for column in ("donor_id", "referrer_id"):
        op.create_index(f"ix_donor_checkouts_{column}", "donor_checkouts", [column])


def downgrade() -> None:
    # Removing empty scaffolding is reversible; silently erasing donor records is not.
    if op.get_bind().execute(sa.text("SELECT count(*) FROM donors")).scalar():
        raise RuntimeError("Donor records exist; export and explicitly migrate them before downgrade")
    for table in ("donor_checkouts", "donor_referral_touches", "donor_sessions",
                  "donor_login_links", "donors"):
        op.drop_table(table)
