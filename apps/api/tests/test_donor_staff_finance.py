from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest
from cryptography.fernet import Fernet
from fastapi.testclient import TestClient
from sqlalchemy import select
from sqlalchemy.orm import sessionmaker

from impactgraph import donor_staff as staff
from impactgraph.donor_api import create_app
from impactgraph.donor_staff_finance import (
    cancel_giving,
    cancellation_quote,
    refund_gift,
    refund_quote,
)
from impactgraph.persistence import (
    DonorGiftRecord,
    DonorMetricProjectionRecord,
    DonorRefundObservationRecord,
    DonorRefundRecord,
    DonorStaffActionRecord,
    DonorStaffAuditRecord,
)
from impactgraph.stripe_payments import PaymentUnavailable
from tests.test_donor_pauses import make_pause_setup, uuid_from
from tests.test_donor_staff import account
from tests.test_stripe_payments import SETTINGS


@pytest.fixture
def setup(engine, monkeypatch):
    monkeypatch.setenv("DONOR_STAFF_ENCRYPTION_KEY", Fernet.generate_key().decode())
    factory = sessionmaker(engine, expire_on_commit=False)
    token, _, _ = account(factory, "FINANCE")
    gateway, _, _, _ = make_pause_setup(factory)
    gateway.refunds, gateway.refund_calls, gateway.lose_refund = {}, [], False
    def create(payment_intent, amount, command):
        row = {"id": "re_" + uuid4().hex, "payment_intent": payment_intent, "currency": "usd", "amount": amount,
               "status": "succeeded", "metadata": {"dad_staff_action": command}}
        gateway.refund_calls.append((payment_intent, amount, command))
        gateway.refunds[row["id"]] = row
        if gateway.lose_refund:
            gateway.lose_refund = False
            raise PaymentUnavailable("Response lost")
        return row
    gateway.create_refund = create
    gateway.list_payment_refunds = lambda payment: [row.copy() for row in gateway.refunds.values() if row["payment_intent"] == payment]
    with factory() as session:
        gift = session.scalar(select(DonorGiftRecord))
    return factory, gateway, token, gift


def refund(setup, amount=1000, key=None):
    factory, gateway, token, gift = setup
    return refund_gift(factory, gateway, token, donor_id=gift.donor_id, gift_id=gift.id,
                       request_id=key or uuid4(), amount_minor=amount, reason="Donor requested a refund")


def test_partial_and_full_refunds_update_realized_totals_and_preserve_badge(setup):
    factory, gateway, token, gift = setup
    assert refund_quote(factory, gateway, token, gift.donor_id, gift.id)["available_minor"] == 3000
    assert refund(setup)["status"] == "SUCCEEDED"
    with factory() as session:
        projection = session.scalar(select(DonorMetricProjectionRecord))
        assert projection.net_minor == 2000 and projection.current_streak == 1
        assert projection.founding_awarded_at is not None
        observation = session.scalar(select(DonorRefundObservationRecord))
        assert observation.source_action_id is not None and observation.source_event_id is None
        command = session.get(DonorStaffActionRecord, observation.source_action_id)
        assert command.actor_id and command.reason == "Donor requested a refund" and command.status == "OBSERVED"
    assert refund(setup, 2000)["status"] == "SUCCEEDED"
    with factory() as session:
        projection = session.scalar(select(DonorMetricProjectionRecord))
        assert projection.net_minor == 0 and projection.current_streak == 0 and projection.founding_awarded_at is not None
    assert refund_quote(factory, gateway, token, gift.donor_id, gift.id)["available_minor"] == 0


def test_lost_refund_response_reconciles_before_another_mutation(setup):
    factory, gateway, _, _ = setup
    key = uuid4()
    gateway.lose_refund = True
    with pytest.raises(PaymentUnavailable):
        refund(setup, key=key)
    with factory() as session:
        assert session.get(DonorStaffActionRecord, key).status == "AUTHORIZED"
        assert not session.scalar(select(DonorRefundRecord))
    assert refund(setup, key=key)["status"] == "SUCCEEDED"
    assert refund(setup, key=key)["status"] == "SUCCEEDED"
    assert len(gateway.refund_calls) == 1
    with factory() as session:
        assert len(session.scalars(select(DonorRefundRecord)).all()) == 1
        assert session.scalar(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "gift.action_authorized"))


def test_authorized_retry_cannot_change_amount_or_actor(setup):
    factory, gateway, _, gift = setup
    key = uuid4()
    refund(setup, key=key)
    with pytest.raises(ValueError, match="key already used"):
        refund(setup, 2000, key)
    other, _, _ = account(factory, "FINANCE", "other@example.org")
    with pytest.raises(ValueError, match="key already used"):
        refund_gift(factory, gateway, other, donor_id=gift.donor_id, gift_id=gift.id, request_id=key,
                    amount_minor=1000, reason="Donor requested a refund")
    assert len(gateway.refund_calls) == 1


def test_staff_cancellation_reuses_the_donor_billing_rules_without_impersonation(setup):
    factory, gateway, token, gift = setup
    chosen = cancellation_quote(factory, gateway, token, gift.donor_id)["subscription"]
    result = cancel_giving(factory, gateway, token, donor_id=gift.donor_id, attempt_id=uuid_from(chosen["id"]),
                          request_id=uuid4(), period_end=chosen["period_end"], reason="Donor requested cancellation")
    assert result["subscription"]["stops_at"] == chosen["period_end"]
    with factory() as session:
        assert session.scalar(select(DonorMetricProjectionRecord)).net_minor == 3000
        assert session.scalar(select(DonorStaffAuditRecord).where(DonorStaffAuditRecord.action == "gift.cancellation_confirmed"))
        command = session.scalar(select(DonorStaffActionRecord))
        assert command.kind == "CANCEL" and command.status == "OBSERVED"


def test_old_unknown_refund_is_not_reissued_after_provider_key_expiry(setup):
    factory, gateway, _, _ = setup
    key = uuid4()
    gateway.lose_refund = True
    with pytest.raises(PaymentUnavailable):
        refund(setup, key=key)
    gateway.refunds.clear()  # No authoritative evidence that this unknown command applied.
    with factory.begin() as session:
        session.get(DonorStaffActionRecord, key).created_at = datetime.now(UTC) - timedelta(days=2)
    with pytest.raises(ValueError, match="reconciliation"):
        refund(setup, key=key)
    assert len(gateway.refund_calls) == 1


def test_staff_refund_requires_permission_origin_and_strict_fields(setup):
    factory, gateway, token, gift = setup
    client = TestClient(create_app(factory=factory, settings=SETTINGS, gateway=gateway), base_url=SETTINGS.origin)
    path = f"/donor/staff/donors/{gift.donor_id}/gifts/{gift.id}/refund"
    assert client.get(path).status_code == 401
    support, _, _ = account(factory, "SUPPORT", "support@example.org")
    client.cookies.set(staff.COOKIE, support)
    assert client.get(path).status_code == 403
    client.cookies.set(staff.COOKIE, token)
    body = {"amount_minor": 1000, "reason": "Donor requested refund"}
    headers = {"Idempotency-Key": str(uuid4())}
    assert client.post(path, json=body, headers=headers).status_code == 403
    headers["Origin"] = SETTINGS.origin
    assert client.post(path, json={**body, "amount_minor": True}, headers=headers).status_code == 422
    assert client.post(path, json={**body, "actor_id": str(uuid4())}, headers=headers).status_code == 422
    assert client.post(path, json=body, headers=headers).json()["status"] == "SUCCEEDED"
    assert len(gateway.refund_calls) == 1


def test_preview_reconciles_lost_response_without_reissuing_refund(setup):
    factory, gateway, token, gift = setup
    key = uuid4()
    gateway.lose_refund = True
    with pytest.raises(PaymentUnavailable):
        refund(setup, key=key)
    assert refund_quote(factory, gateway, token, gift.donor_id, gift.id)["available_minor"] == 2000
    with factory() as session:
        assert session.get(DonorStaffActionRecord, key).status == "OBSERVED"
        assert session.scalar(select(DonorMetricProjectionRecord)).net_minor == 2000
    assert len(gateway.refund_calls) == 1


def test_insufficient_remaining_amount_closes_unsubmitted_request(setup):
    factory, gateway, _, _ = setup
    refund(setup, 2000)
    key = uuid4()
    assert refund(setup, 2000, key)["status"] == "NOT_SUBMITTED"
    assert refund(setup, 2000, key)["status"] == "NOT_SUBMITTED"
    assert refund(setup, 1000)["status"] == "SUCCEEDED"
    assert len(gateway.refund_calls) == 2
    with factory() as session:
        assert session.get(DonorStaffActionRecord, key).status == "ABANDONED"


def test_provider_refund_creation_has_frozen_metadata_and_idempotency():
    from types import SimpleNamespace
    from unittest.mock import Mock

    from impactgraph.stripe_payments import StripeGateway
    gateway = object.__new__(StripeGateway)
    create = Mock(return_value=SimpleNamespace(to_dict=dict))
    gateway.client = SimpleNamespace(v1=SimpleNamespace(refunds=SimpleNamespace(create=create)))
    gateway.create_refund("pi_owned", 1234, "command")
    create.assert_called_once_with({"payment_intent": "pi_owned", "amount": 1234,
        "reason": "requested_by_customer", "metadata": {"dad_staff_action": "command"}},
        options={"idempotency_key": "dad-staff-refund-command"})


def test_refund_command_cannot_omit_its_amount_at_database_boundary(setup):
    from sqlalchemy.exc import IntegrityError
    factory, _, token, gift = setup
    with pytest.raises(IntegrityError), factory.begin() as session:
        actor = staff.require_staff(session, token)
        session.add(DonorStaffActionRecord(actor_id=actor.id, donor_id=gift.donor_id, kind="REFUND", target_id=gift.id,
                                          amount_minor=None, reason="Invalid missing amount"))
        session.flush()
