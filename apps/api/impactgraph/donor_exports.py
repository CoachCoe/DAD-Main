"""Permission-scoped streaming CSV with a fixed allowlist excluding credentials."""
import csv
import io
from datetime import UTC, datetime

from sqlalchemy import select

from . import donor_staff as staff
from .persistence import (
    DonorAmountChangeRecord,
    DonorBillingPeriodRecord,
    DonorCancellationRecord,
    DonorContentPublicationRecord,
    DonorContentSourceRecord,
    DonorGiftRecord,
    DonorMessageRecord,
    DonorMetricProjectionRecord,
    DonorPartnerSignupRecord,
    DonorPauseRecord,
    DonorPreferenceEventRecord,
    DonorPreferenceRecord,
    DonorRecord,
    DonorRefundObservationRecord,
    DonorRefundRecord,
    DonorResumeRecord,
    DonorStaffActionRecord,
    DonorStaffAuditRecord,
    DonorStaffRecord,
    DonorSubscriptionRecord,
)

# Sensitive credentials, processor session URLs, SMTP tokens, and email bodies are
# deliberately absent. Every exported business dataset uses an explicit field list.
DATASETS = {
    "donors": (DonorRecord, "id created_at email claimed_at disabled public_name", "data.export"),
    "gifts": (DonorGiftRecord, "id donor_id checkout_id amount_minor currency frequency paid_at period_id", "data.export"),
    "refunds": (DonorRefundRecord, "id gift_id amount_minor currency status observed_at stripe_refund_id", "data.export"),
    "refund-history": (DonorRefundObservationRecord, "id refund_id source_event_id source_action_id status observed_at", "data.export"),
    "billing-periods": (DonorBillingPeriodRecord, "id donor_id checkout_id stripe_invoice_id due_at ends_at status", "data.export"),
    "referrals": (DonorMetricProjectionRecord, "donor_id referrer_id referral_kind first_gift_id net_minor computed_at", "data.export"),
    "streaks": (DonorMetricProjectionRecord, "donor_id current_streak best_streak at_risk grace_ends_at founding_awarded_at computed_at", "data.export"),
    "subscriptions": (DonorSubscriptionRecord, "id donor_id attempt_id status period_end stops_at observed_at", "data.export"),
    "pauses": (DonorPauseRecord, "id donor_id attempt_id months starts_at resumes_at confirmed_at created_at", "data.export"),
    "resumptions": (DonorResumeRecord, "id donor_id pause_id next_renewal confirmed_at created_at", "data.export"),
    "amount-changes": (DonorAmountChangeRecord, "id donor_id checkout_id previous_minor amount_minor effective_at confirmed_at created_at", "data.export"),
    "cancellations": (DonorCancellationRecord, "id donor_id attempt_id confirmed_period_end completed_at created_at", "data.export"),
    "preferences": (DonorPreferenceRecord, "donor_id version referral_emails marketing_emails", "data.export"),
    "consent-history": (DonorPreferenceEventRecord, "id donor_id version referral_emails marketing_emails consent_version source created_at", "data.export"),
    "messages": (DonorMessageRecord, "id donor_id kind available_at completed_at attempts last_error suppressed_reason created_at", "data.export"),
    "staff-actions": (DonorStaffActionRecord, "id actor_id donor_id kind target_id amount_minor period_end reason status stripe_refund_id completed_at created_at", "data.export"),
    "staff": (DonorStaffRecord, "id name email role disabled created_at", "staff.manage"),
    "audit": (DonorStaffAuditRecord, "id actor_id action target created_at", "audit.read"),
    "partner-signups": (DonorPartnerSignupRecord, "id organization name email website message consent_version created_at", "content.publish"),
    "content-sources": (DonorContentSourceRecord, "id actor_id name sha256 created_at", "content.publish"),
    "publications": (DonorContentPublicationRecord, "id actor_id payload created_at", "content.publish"),
    "enrolments": (None, "enrolment_id program_id submitted_at status", "data.export"),
}


def safe_cell(value):
    if value is None:
        return ""
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, str) and (value.lstrip("\ufeff \t\r\n").startswith(("=", "+", "-", "@"))
                                  or value.startswith(("\t", "\r", "\n"))):
        return "'" + value
    return value


def csv_rows(factory, token, dataset, audit_id):
    model, fields, permission = DATASETS[dataset]
    fields = fields.split()
    count, completed = 0, False
    buffer = io.StringIO(newline="")
    writer = csv.writer(buffer)
    def line(values):
        buffer.seek(0)
        buffer.truncate(0)
        writer.writerow([safe_cell(value) for value in values])
        return buffer.getvalue()
    try:
        with factory() as session:
            staff.require_staff(session, token, permission)
            yield line(fields)
            if model:
                statement = select(*(getattr(model, field) for field in fields)).order_by(model.id)
                if dataset == "referrals":
                    statement = statement.where(model.referrer_id.is_not(None))
                for row in session.execute(statement.execution_options(yield_per=500)):
                    count += 1
                    yield line(row)
            completed = True
    finally:
        with factory.begin() as session:
            started = session.get(DonorStaffAuditRecord, audit_id)
            if started:
                staff.audit(session, started.actor_id, "data.export_finished", dataset,
                            export_id=str(audit_id), completed=completed, row_count=count,
                            finished_at=datetime.now(UTC).isoformat())
