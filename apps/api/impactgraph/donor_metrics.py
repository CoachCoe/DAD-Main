"""Pure R1 calculations over verified financial facts, never checkout drafts.

The caller supplies the clock and complete billing history. Stripe ingestion must
supply refund outcomes and billing periods before these results reach a dashboard.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from enum import StrEnum
from itertools import pairwise
from zoneinfo import ZoneInfo

from .donors import GiftFrequency, ReferralKind

FOUNDING_CUTOFF = datetime(2027, 4, 22, tzinfo=ZoneInfo("America/New_York"))
STREAK_GRACE = timedelta(days=14)


def _time(value):
    if value.tzinfo is None or value.utcoffset() is None:
        raise ValueError("Financial timestamps must have a time zone")
    return value.astimezone(UTC)


def _money(value):
    if type(value) is not int or value <= 0:
        raise ValueError("Money must be positive integer USD cents")


def _unique(items):
    result = {}
    for item in items:
        if not item.id:
            raise ValueError("Facts require stable identifiers")
        if item.id in result and result[item.id] != item:
            raise ValueError("Conflicting facts for the same identifier")
        result[item.id] = item
    return list(result.values())


@dataclass(frozen=True)
class ReferralCandidate:
    referrer_id: str
    kind: ReferralKind
    expires_at: datetime

    def __post_init__(self):
        if not self.referrer_id:
            raise ValueError("Referrer ID is required")
        ReferralKind(self.kind)
        _time(self.expires_at)


@dataclass(frozen=True)
class Gift:
    id: str
    donor_id: str
    amount_minor: int
    paid_at: datetime
    frequency: GiftFrequency = GiftFrequency.ONE_TIME
    candidate: ReferralCandidate | None = None
    period_id: str | None = None
    currency: str = "USD"

    def __post_init__(self):
        _money(self.amount_minor)
        _time(self.paid_at)
        if not self.donor_id or self.currency != "USD":
            raise ValueError("A gift requires a donor and USD currency")
        GiftFrequency(self.frequency)
        if self.frequency == GiftFrequency.ONE_TIME and self.period_id is not None:
            raise ValueError("One-time gifts cannot cover a recurring billing period")


class RefundStatus(StrEnum):
    PENDING = "PENDING"
    REQUIRES_ACTION = "REQUIRES_ACTION"
    SUCCEEDED = "SUCCEEDED"
    FAILED = "FAILED"
    CANCELED = "CANCELED"


@dataclass(frozen=True)
class Refund:
    id: str
    gift_id: str
    amount_minor: int
    status: RefundStatus
    updated_at: datetime
    currency: str = "USD"

    def __post_init__(self):
        _money(self.amount_minor)
        _time(self.updated_at)
        RefundStatus(self.status)
        if self.currency != "USD":
            raise ValueError("Refund currency must be USD")


def net_gifts(gifts, refunds, *, as_of):
    """One current state per refund ID; pending/failed refunds do not reduce collection.

    Reject future refund states instead of guessing their earlier status. Historical
    reports require the adapter to retain and select the appropriate state history.
    """
    as_of = _time(as_of)
    gifts = _unique(gifts)
    by_id = {gift.id: gift for gift in gifts}
    net = {gift.id: gift.amount_minor for gift in gifts if _time(gift.paid_at) <= as_of}
    for refund in _unique(refunds):
        gift = by_id.get(refund.gift_id)
        if gift is None:
            raise ValueError("Refund references a missing gift")
        if _time(refund.updated_at) > as_of:
            raise ValueError("Refund snapshot contains a future state")
        if _time(refund.updated_at) < _time(gift.paid_at):
            raise ValueError("Refund precedes successful payment")
        if refund.amount_minor > gift.amount_minor:
            raise ValueError("Refund exceeds original gift")
        if refund.status == RefundStatus.SUCCEEDED:
            net[gift.id] -= refund.amount_minor
            if net[gift.id] < 0:
                raise ValueError("Successful refunds exceed the original gift")
    return net


@dataclass(frozen=True)
class Relationship:
    donor_id: str
    first_gift_id: str
    converted_at: datetime
    referrer_id: str | None
    kind: ReferralKind | None


def relationships(gifts, *, as_of):
    """Earliest successful payment fixes attribution, including direct arrivals.

    Refunds never select a different referral. An earlier payment discovered later
    corrects history; it is not a new attribution opportunity. Stable payment IDs
    break equal-timestamp ties independently of webhook arrival order.
    """
    as_of = _time(as_of)
    result = {}
    for gift in sorted(_unique(gifts), key=lambda row: (_time(row.paid_at), row.id)):
        if _time(gift.paid_at) > as_of or gift.donor_id in result:
            continue
        candidate = gift.candidate
        eligible = candidate is not None and candidate.referrer_id != gift.donor_id \
            and _time(gift.paid_at) < _time(candidate.expires_at)
        result[gift.donor_id] = Relationship(
            gift.donor_id, gift.id, gift.paid_at,
            candidate.referrer_id if eligible else None,
            ReferralKind(candidate.kind) if eligible else None,
        )
    return result


@dataclass(frozen=True)
class ReferralTotals:
    link_count: int = 0
    irl_count: int = 0
    link_minor: int = 0
    irl_minor: int = 0
    currency: str = "USD"

    @property
    def total_minor(self):
        return self.link_minor + self.irl_minor


def referral_totals(gifts, refunds, *, as_of):
    gifts = _unique(gifts)
    net = net_gifts(gifts, refunds, as_of=as_of)
    relations = relationships(gifts, as_of=as_of)
    raised = {}
    for gift in gifts:
        raised[gift.donor_id] = raised.get(gift.donor_id, 0) + net.get(gift.id, 0)
    totals = {}
    for donor_id, relation in relations.items():
        if relation.referrer_id is None:
            continue
        values = totals.setdefault(relation.referrer_id, [0, 0, 0, 0])
        index = 0 if relation.kind == ReferralKind.LINK else 1
        amount = raised[donor_id]
        values[index] += int(amount > 0)
        values[index + 2] += amount
    return {donor_id: ReferralTotals(*values) for donor_id, values in totals.items()}


def founding_donor(gifts, donor_id, *, as_of, previously_awarded=False):
    """Permanent recognition; refunds, cancellation, and late claiming cannot revoke it."""
    as_of = _time(as_of)
    return previously_awarded or any(
        gift.donor_id == donor_id and _time(gift.paid_at) <= as_of
        and _time(gift.paid_at) < _time(FOUNDING_CUTOFF) for gift in _unique(gifts)
    )


class PeriodStatus(StrEnum):
    EXPECTED = "EXPECTED"
    FAILED = "FAILED"
    SKIPPED = "SKIPPED"


@dataclass(frozen=True)
class BillingPeriod:
    id: str
    donor_id: str
    due_at: datetime
    ends_at: datetime
    status: PeriodStatus = PeriodStatus.EXPECTED

    def __post_init__(self):
        duration = _time(self.ends_at) - _time(self.due_at)
        if not self.donor_id or not timedelta(days=27) <= duration <= timedelta(days=32):
            raise ValueError("Expected a monthly billing period and a donor")
        PeriodStatus(self.status)


@dataclass(frozen=True)
class Streak:
    current: int
    best: int
    at_risk: bool
    grace_ends_at: datetime | None


class IncompleteBillingHistory(ValueError):
    pass


def streak(gifts, refunds, periods, donor_id, *, as_of, previous_best=0, stopped_period_ends=(), pause_windows=()):
    """Count distinct scheduled recurring periods, not calendar months or retries.

    The adapter supplies failed/skipped periods and the current schedule. No monthly
    dates are guessed from charge timestamps. Preserve previously achieved bests.
    """
    as_of = _time(as_of)
    stopped = {_time(end) for end in stopped_period_ends}
    paused = {_time(start) for start, end in pause_windows if _time(start) <= as_of < _time(end)}
    if type(previous_best) is not int or previous_best < 0:
        raise ValueError("Historical best must be a nonnegative integer")
    gifts = _unique(gifts)
    net = net_gifts(gifts, refunds, as_of=as_of)
    periods = sorted((p for p in _unique(periods) if p.donor_id == donor_id),
                     key=lambda p: _time(p.due_at))
    by_id = {p.id: p for p in periods}
    for before, after in pairwise(periods):
        if _time(before.ends_at) > _time(after.due_at):
            raise ValueError("Donor billing periods cannot overlap")
    payments = {p.id: [] for p in periods}
    for gift in gifts:
        if gift.donor_id != donor_id or gift.frequency != GiftFrequency.MONTHLY:
            continue
        if gift.id not in net:
            continue
        if gift.period_id not in by_id:
            raise IncompleteBillingHistory("Recurring gift has no verified billing period")
        if by_id[gift.period_id].status == PeriodStatus.SKIPPED:
            raise ValueError("A skipped billing period cannot have a successful payment")
        payments[gift.period_id].append(gift)
    elapsed = [p for p in periods if _time(p.due_at) <= as_of]
    if elapsed and _time(elapsed[-1].ends_at) <= as_of \
            and elapsed[-1].status != PeriodStatus.SKIPPED and _time(elapsed[-1].ends_at) not in stopped | paused:
        raise IncompleteBillingHistory("Current renewal or skipped-period fact is missing")

    def calculate():
        current = best = 0
        risk = False
        deadline = None
        last = None
        last_paid = None
        for period in elapsed:
            due, end = _time(period.due_at), _time(period.ends_at)
            if last != due:
                current = 0
            qualifying = [g for g in payments[period.id] if net[g.id] > 0]
            paid = min((_time(g.paid_at) for g in qualifying), default=None)
            risk, deadline = False, None
            if paid is not None:
                if paid >= due + STREAK_GRACE or (last_paid and paid < last_paid):
                    current = 0
                current += 1
                last_paid = paid
                best = max(best, current)
            elif (period.status == PeriodStatus.SKIPPED or payments[period.id]
                  or as_of >= due + STREAK_GRACE):
                current = 0
                last_paid = None
            else:
                risk = current > 0
                deadline = due + STREAK_GRACE if risk else None
            last = end
        return Streak(current, best, risk, deadline)

    actual = calculate()
    if elapsed and _time(elapsed[-1].ends_at) <= as_of and _time(elapsed[-1].ends_at) in stopped | paused:
        return Streak(0, max(previous_best, actual.best), False, None)
    return Streak(actual.current, max(previous_best, actual.best),
                  actual.at_risk, actual.grace_ends_at)
