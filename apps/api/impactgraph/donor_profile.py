"""Donor preferences with an append-only consent history and optimistic concurrency."""
from datetime import UTC, datetime

from sqlalchemy import select, update

from .donors import authenticated_donor
from .persistence import (
    DonorPreferenceEventRecord,
    DonorPreferenceRecord,
    DonorRecord,
    DonorSessionRecord,
)

CONSENT_VERSION = "r1-notifications-v1"


class PreferenceConflict(ValueError):
    pass


def profile(session, token):
    donor = authenticated_donor(session, token)
    preferences = session.scalar(select(DonorPreferenceRecord).where(DonorPreferenceRecord.donor_id == donor.id))
    return {"email": donor.email, "public_name": donor.public_name,
            "referral_emails": preferences.referral_emails if preferences else False,
            "marketing_emails": preferences.marketing_emails if preferences else False,
            "version": preferences.version if preferences else 0, "consent_version": CONSENT_VERSION}


def save_preferences(session, token, *, version, referral_emails, marketing_emails):
    donor = authenticated_donor(session, token)
    session.execute(select(DonorRecord.id).where(DonorRecord.id == donor.id).with_for_update())
    row = session.scalar(select(DonorPreferenceRecord).where(DonorPreferenceRecord.donor_id == donor.id))
    if version != (row.version if row else 0):
        raise PreferenceConflict("Preferences changed. Refresh before saving again")
    if row is None:
        row = DonorPreferenceRecord(donor_id=donor.id, version=0)
        session.add(row)
    row.version += 1
    row.referral_emails, row.marketing_emails = referral_emails, marketing_emails
    session.add(DonorPreferenceEventRecord(donor_id=donor.id, version=row.version,
        referral_emails=referral_emails, marketing_emails=marketing_emails, consent_version=CONSENT_VERSION,
        source="account"))
    session.flush()
    return profile(session, token)


def sign_out_everywhere(session, token):
    donor = authenticated_donor(session, token)
    session.execute(update(DonorSessionRecord).where(DonorSessionRecord.donor_id == donor.id,
                    DonorSessionRecord.revoked_at.is_(None)).values(revoked_at=datetime.now(UTC)))
