from datetime import UTC, datetime

import pytest
from sqlalchemy import func, select
from sqlalchemy.orm import sessionmaker

from impactgraph.donor_financial_facts import load_history
from impactgraph.donor_metrics import net_gifts, streak
from impactgraph.donors import GiftFrequency
from impactgraph.persistence import (
    DonorBillingPeriodRecord,
    DonorGiftRecord,
    DonorRefundObservationRecord,
    DonorRefundRecord,
    StripeEventRecord,
)
from impactgraph.stripe_payments import accept_event, process_next_event
from tests.test_stripe_payments import Gateway, checkout, event_for


@pytest.fixture
def factory(engine):
    return sessionmaker(engine, expire_on_commit=False)


class RefundGateway:
    def __init__(self, current):
        self.current = current

    def retrieve_refund(self, refund_id):
        assert refund_id == self.current["id"]
        return self.current


def process(factory, event, gateway=None):
    assert accept_event(factory, event)
    assert process_next_event(factory, gateway=gateway)


def refund_event(payment_id, *, status="pending", event_id="evt_refund", refund_id="re_example", amount=1000):
    return {"id": event_id, "type": "refund.updated", "created": int(datetime.now(UTC).timestamp()),
            "data": {"object": {"id": refund_id, "payment_intent": payment_id,
                                "status": status, "currency": "usd", "amount": amount}}}


def test_failed_then_paid_period_and_late_failure(factory):
    result = checkout(factory, Gateway(), frequency=GiftFrequency.MONTHLY)
    paid = event_for(result, "invoice.payment_succeeded")
    failed = event_for(result, "invoice.payment_succeeded", object_id=paid["data"]["object"]["id"])
    failed["type"] = "invoice.payment_failed"
    failed["data"]["object"]["status"] = "open"
    failed["data"]["object"]["amount_paid"] = 0
    process(factory, failed)
    with factory() as session:
        assert session.scalar(select(DonorBillingPeriodRecord)).status == "FAILED"
        assert session.scalar(select(DonorGiftRecord)) is None
    process(factory, paid)
    failed["id"] += "_late"
    process(factory, failed)
    with factory() as session:
        assert session.scalar(select(DonorBillingPeriodRecord)).status == "PAID"
        gift = session.scalar(select(DonorGiftRecord))
        gifts, refunds, periods = load_history(session, gift.donor_id)
        assert streak(gifts, refunds, periods, str(gift.donor_id), as_of=datetime.now(UTC)).current == 1
        assert gift.stripe_payment_intent_id.startswith("pi_in_")


@pytest.mark.parametrize("monthly", [False, True])
def test_refund_uses_current_state_and_retains_observations(factory, monthly):
    result = checkout(factory, Gateway(), frequency=GiftFrequency.MONTHLY if monthly else GiftFrequency.ONE_TIME)
    paid = event_for(result, "invoice.payment_succeeded" if monthly else "payment_intent.succeeded")
    process(factory, paid)
    with factory() as session:
        gift = session.scalar(select(DonorGiftRecord))
        payment_id, donor_id = gift.stripe_payment_intent_id, gift.donor_id
    event = refund_event(payment_id)
    current = dict(event["data"]["object"], status="succeeded")
    gateway = RefundGateway(current)
    process(factory, event, gateway)
    with factory() as session:
        assert session.scalar(select(DonorRefundRecord)).status == "SUCCEEDED"
        gifts, refunds, _ = load_history(session, donor_id)
        assert sum(net_gifts(gifts, refunds, as_of=datetime.now(UTC)).values()) == 2000
    # A later failure reverses the effect, even if the delivered event is an older success.
    gateway.current["status"] = "failed"
    event["id"] += "_late"
    event["data"]["object"]["status"] = "succeeded"
    process(factory, event, gateway)
    with factory() as session:
        assert session.scalar(select(DonorRefundRecord)).status == "FAILED"
        assert session.scalar(select(func.count()).select_from(DonorRefundObservationRecord)) == 2
        gifts, refunds, _ = load_history(session, donor_id)
        assert sum(net_gifts(gifts, refunds, as_of=datetime.now(UTC)).values()) == 3000
    # Identical webhook replay is already processed; no extra audit observation.
    assert accept_event(factory, event)
    assert not process_next_event(factory, gateway=gateway)


def test_refund_before_payment_waits_and_retries(factory):
    result = checkout(factory, Gateway())
    paid = event_for(result)
    event = refund_event(paid["data"]["object"]["id"])
    gateway = RefundGateway(dict(event["data"]["object"], status="succeeded"))
    process(factory, event, gateway)
    with factory() as session:
        assert session.scalar(select(StripeEventRecord)).processed_at is None
        assert session.scalar(select(DonorRefundRecord)) is None
    for _ in range(9):
        assert process_next_event(factory, gateway=gateway)
    assert not process_next_event(factory, gateway=gateway)
    accept_event(factory, paid)
    assert process_next_event(factory, gateway=gateway)
    with factory.begin() as session:
        waiting = session.scalar(select(StripeEventRecord).where(StripeEventRecord.stripe_event_id == event["id"]))
        waiting.attempts = 0
    assert process_next_event(factory, gateway=gateway)
    with factory() as session:
        assert session.scalar(select(DonorRefundRecord)).status == "SUCCEEDED"


@pytest.mark.parametrize("bad", ["truncated", "proration", "split", "wrong_invoice", "offline"])
def test_unsupported_invoice_facts_are_retained_for_reconciliation(factory, bad):
    result = checkout(factory, Gateway(), frequency=GiftFrequency.MONTHLY)
    event = event_for(result, "invoice.payment_succeeded")
    obj = event["data"]["object"]
    if bad == "truncated":
        obj["lines"]["has_more"] = True
    elif bad == "proration":
        obj["lines"]["data"][0]["parent"]["subscription_item_details"]["proration"] = True
    elif bad == "split":
        obj["payments"]["data"] *= 2
    elif bad == "wrong_invoice":
        obj["payments"]["data"][0]["invoice"] = "in_other"
    else:
        obj["payments"]["data"][0]["payment"]["type"] = "payment_record"
    process(factory, event)
    with factory() as session:
        assert session.scalar(select(DonorGiftRecord)) is None
        assert session.scalar(select(DonorBillingPeriodRecord)) is None
        assert session.scalar(select(StripeEventRecord)).last_error == "ValueError"


def test_excess_refunds_roll_back_and_do_not_corrupt_current_facts(factory):
    result = checkout(factory, Gateway())
    paid = event_for(result)
    process(factory, paid)
    pi = paid["data"]["object"]["id"]
    for index in range(2):
        event = refund_event(pi, event_id=f"evt_{index}", refund_id=f"re_{index}", amount=2000)
        process(factory, event, RefundGateway(dict(event["data"]["object"], status="succeeded")))
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorRefundRecord)) == 1
        assert session.scalar(select(func.count()).select_from(DonorRefundObservationRecord)) == 1


def test_verified_redelivery_enriches_legacy_gift_without_collecting_twice(factory):
    result = checkout(factory, Gateway(), frequency=GiftFrequency.MONTHLY)
    event = event_for(result, "invoice.payment_succeeded")
    process(factory, event)
    with factory.begin() as session:
        gift = session.scalar(select(DonorGiftRecord))
        gift.period_id = None
        gift.stripe_payment_intent_id = None
    event["id"] += "_complete_redelivery"
    process(factory, event)
    with factory() as session:
        assert session.scalar(select(func.count()).select_from(DonorGiftRecord)) == 1
        gift = session.scalar(select(DonorGiftRecord))
        assert gift.period_id and gift.stripe_payment_intent_id


def test_retrieval_failure_preserves_retry_and_no_financial_change(factory):
    class Unavailable:
        def retrieve_refund(self, refund_id):
            raise ConnectionError("provider unavailable")

    result = checkout(factory, Gateway())
    paid = event_for(result)
    process(factory, paid)
    event = refund_event(paid["data"]["object"]["id"])
    process(factory, event, Unavailable())
    with factory() as session:
        assert session.scalar(select(DonorRefundRecord)) is None
        row = session.scalar(select(StripeEventRecord).where(StripeEventRecord.stripe_event_id == event["id"]))
        assert row.processed_at is None and row.last_error == "ConnectionError"


def test_history_is_owner_scoped(factory):
    first = checkout(factory, Gateway(), email="a@example.org")
    second = checkout(factory, Gateway(), email="b@example.org")
    process(factory, event_for(first))
    process(factory, event_for(second))
    with factory() as session:
        gift = session.scalar(select(DonorGiftRecord).limit(1))
        gifts, refunds, periods = load_history(session, gift.donor_id)
        assert len(gifts) == 1 and gifts[0].donor_id == str(gift.donor_id)
        assert refunds == periods == []


def test_invoice_inbox_does_not_keep_customer_or_free_form_nested_fields(factory):
    result = checkout(factory, Gateway(), frequency=GiftFrequency.MONTHLY)
    event = event_for(result, "invoice.payment_succeeded")
    obj = event["data"]["object"]
    obj["customer_email"] = "private@example.org"
    obj["lines"]["data"][0]["description"] = "sensitive description"
    obj["lines"]["data"][0]["period"]["unexpected"] = "sensitive nested value"
    accept_event(factory, event)
    with factory() as session:
        stored = str(session.scalar(select(StripeEventRecord)).data)
        assert "private@example.org" not in stored
        assert "sensitive" not in stored
