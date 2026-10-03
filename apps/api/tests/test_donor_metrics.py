from dataclasses import replace
from datetime import UTC, datetime, timedelta
from itertools import permutations

import pytest

from impactgraph.donor_metrics import (
    FOUNDING_CUTOFF,
    BillingPeriod,
    Gift,
    IncompleteBillingHistory,
    PeriodStatus,
    ReferralCandidate,
    Refund,
    RefundStatus,
    founding_donor,
    net_gifts,
    referral_totals,
    relationships,
    streak,
)
from impactgraph.donors import GiftFrequency, ReferralKind

JAN = datetime(2027, 1, 1, tzinfo=UTC)
FEB = datetime(2027, 2, 1, tzinfo=UTC)
MAR = datetime(2027, 3, 1, tzinfo=UTC)
APR = datetime(2027, 4, 1, tzinfo=UTC)
MAY = datetime(2027, 5, 1, tzinfo=UTC)
CANDIDATE = ReferralCandidate("alice", ReferralKind.LINK, FEB)
PERIODS = [BillingPeriod("jan", "ben", JAN, FEB), BillingPeriod("feb", "ben", FEB, MAR),
           BillingPeriod("mar", "ben", MAR, APR)]
GIFTS = [Gift("g1", "ben", 3000, JAN, GiftFrequency.MONTHLY, CANDIDATE, "jan"),
         Gift("g2", "ben", 9000, FEB, GiftFrequency.MONTHLY, None, "feb"),
         Gift("g3", "ben", 15000, MAR, GiftFrequency.MONTHLY, None, "mar")]


def refund(gift_id="g1", amount=3000, *, status=RefundStatus.SUCCEEDED, when=MAR, id="r1"):
    return Refund(id, gift_id, amount, status, when)


def test_direct_only_referrals_and_realized_channel_split():
    gifts = [*GIFTS, Gift("g4", "cara", 100, JAN, candidate=replace(CANDIDATE, kind=ReferralKind.IRL)),
             Gift("g5", "dee", 500, JAN, candidate=replace(CANDIDATE, referrer_id="ben"))]
    totals = referral_totals(gifts, [], as_of=MAR)
    assert (totals["alice"].link_count, totals["alice"].irl_count) == (1, 1)
    assert (totals["alice"].link_minor, totals["alice"].irl_minor) == (27000, 100)
    assert totals["alice"].total_minor == 27100
    assert totals["ben"].total_minor == 500


def test_refund_removes_then_later_gift_restores_same_relationship():
    first = GIFTS[0]
    refunds = [refund()]
    assert referral_totals([first], refunds, as_of=MAR)["alice"].link_count == 0
    later = replace(GIFTS[1], paid_at=MAR, candidate=replace(CANDIDATE, referrer_id="other", expires_at=MAY))
    restored = referral_totals([first, later], refunds, as_of=MAR)
    assert restored["alice"].link_count == 1
    assert restored["alice"].total_minor == 9000
    assert "other" not in restored
    assert relationships([later, first], as_of=MAR)["ben"].first_gift_id == first.id


@pytest.mark.parametrize("status", list(RefundStatus))
def test_only_successful_refunds_reduce_dollars(status):
    totals = referral_totals([GIFTS[0]], [refund(amount=1000, status=status)], as_of=MAR)["alice"]
    assert totals.link_count == 1
    assert totals.total_minor == (2000 if status == RefundStatus.SUCCEEDED else 3000)


def test_first_payment_not_arrival_order_or_later_referral_controls_credit():
    direct = replace(GIFTS[0], candidate=None)
    referred_later = replace(GIFTS[1], candidate=replace(CANDIDATE, expires_at=MAY))
    assert referral_totals([referred_later, direct], [], as_of=MAR) == {}
    expected = relationships(GIFTS, as_of=MAR)
    for ordering in permutations(GIFTS):
        assert relationships(ordering, as_of=MAR) == expected
    # Stable payment IDs resolve the rare same-timestamp tie.
    assert relationships([replace(referred_later, paid_at=JAN), direct], as_of=MAR)["ben"].referrer_id is None


@pytest.mark.parametrize("when,credited", [(FEB - timedelta(microseconds=1), True), (FEB, False)])
def test_attribution_expiry_uses_successful_payment_timestamp(when, credited):
    gift = replace(GIFTS[0], paid_at=when)
    result = relationships([gift], as_of=MAR)["ben"]
    assert (result.referrer_id == "alice") == credited


def test_self_referral_and_future_gifts_do_not_get_credit():
    own = replace(GIFTS[0], candidate=replace(CANDIDATE, referrer_id="ben"))
    assert referral_totals([own], [], as_of=MAR) == {}
    assert referral_totals([GIFTS[0]], [], as_of=JAN - timedelta(seconds=1)) == {}


def test_duplicate_facts_do_not_double_count_but_conflicts_are_rejected():
    assert net_gifts([GIFTS[0], GIFTS[0]], [refund(), refund()], as_of=MAR) == {"g1": 0}
    with pytest.raises(ValueError, match="Conflicting"):
        net_gifts([GIFTS[0], replace(GIFTS[0], amount_minor=100)], [], as_of=MAR)
    with pytest.raises(ValueError, match="Conflicting"):
        net_gifts([GIFTS[0]], [refund(), refund(status=RefundStatus.FAILED)], as_of=MAR)


@pytest.mark.parametrize("refunds", [
    [refund(amount=3001)], [refund(amount=2000), refund(id="r2", amount=2000)],
    [refund(gift_id="missing")], [refund(when=JAN - timedelta(seconds=1))],
    [refund(when=APR)],
])
def test_invalid_refund_history_is_rejected(refunds):
    with pytest.raises(ValueError):
        net_gifts([GIFTS[0]], refunds, as_of=MAR)


def test_founding_uses_eastern_launch_cutoff_and_is_permanent():
    assert FOUNDING_CUTOFF.astimezone(UTC) == datetime(2027, 4, 22, 4, tzinfo=UTC)
    early = replace(GIFTS[0], paid_at=FOUNDING_CUTOFF - timedelta(microseconds=1))
    late = replace(early, paid_at=FOUNDING_CUTOFF)
    assert founding_donor([early], "ben", as_of=MAY)
    assert not founding_donor([late], "ben", as_of=MAY)
    assert not founding_donor([early], "ben", as_of=JAN)
    assert not founding_donor([early], "other", as_of=MAY)
    assert founding_donor([], "ben", as_of=MAY, previously_awarded=True)
    assert founding_donor([replace(early, frequency=GiftFrequency.ONE_TIME, period_id=None)],
                          "ben", as_of=MAY)


def test_streak_counts_billing_periods_not_charges_and_allows_amount_changes():
    duplicate_period_payment = replace(GIFTS[1], id="retry-payment")
    result = streak([*GIFTS, GIFTS[1], duplicate_period_payment], [], PERIODS, "ben", as_of=MAR)
    assert (result.current, result.best, result.at_risk) == (3, 3, False)


def test_one_time_gifts_do_not_build_or_repair_streaks():
    one_time = Gift("once", "ben", 100, FEB)
    result = streak([GIFTS[0], one_time], [], PERIODS[:2], "ben", as_of=FEB + timedelta(days=15))
    assert result.current == 0 and result.best == 1
    assert streak([one_time], [], [], "ben", as_of=MAR).current == 0


def test_failed_renewal_preserves_streak_until_exact_grace_deadline():
    periods = [PERIODS[0], replace(PERIODS[1], status=PeriodStatus.FAILED)]
    risk = streak(GIFTS[:1], [], periods, "ben", as_of=FEB + timedelta(days=13))
    assert risk.current == 1 and risk.at_risk
    assert risk.grace_ends_at == FEB + timedelta(days=14)
    expired = streak(GIFTS[:1], [], periods, "ben", as_of=FEB + timedelta(days=14))
    assert (expired.current, expired.best, expired.at_risk, expired.grace_ends_at) == (0, 1, False, None)


@pytest.mark.parametrize("delay,expected", [(13, 2), (14, 1), (18, 1)])
def test_recovery_within_grace_continues_and_late_recovery_restarts(delay, expected):
    gifts = [GIFTS[0], replace(GIFTS[1], paid_at=FEB + timedelta(days=delay))]
    result = streak(gifts, [], PERIODS[:2], "ben", as_of=FEB + timedelta(days=20))
    assert result.current == expected and not result.at_risk


def test_pause_or_cancellation_breaks_at_first_skipped_renewal_not_before():
    periods = [PERIODS[0], replace(PERIODS[1], status=PeriodStatus.SKIPPED), PERIODS[2]]
    assert streak(GIFTS[:1], [], periods, "ben", as_of=JAN + timedelta(days=20)).current == 1
    assert streak(GIFTS[:1], [], periods, "ben", as_of=FEB).current == 0
    assert streak([GIFTS[0], GIFTS[2]], [], periods, "ben", as_of=MAR).current == 1


def test_refunds_recalculate_continuity_without_erasing_later_months_or_prior_best():
    partial = streak(GIFTS, [refund(amount=100)], PERIODS, "ben", as_of=MAR)
    assert partial.current == 3
    full = streak(GIFTS, [refund()], PERIODS, "ben", as_of=MAR, previous_best=3)
    assert (full.current, full.best) == (2, 3)
    middle = streak(GIFTS, [refund("g2", 9000)], PERIODS, "ben", as_of=MAR, previous_best=3)
    assert (middle.current, middle.best) == (1, 3)
    # A second real payment covering that period can keep it qualified.
    replacement = replace(GIFTS[1], id="g2-replacement", amount_minor=100)
    assert streak([*GIFTS, replacement], [refund("g2", 9000)], PERIODS, "ben", as_of=MAR).current == 3


def test_historical_best_is_not_invented_from_money_refunded_before_later_periods():
    early_refund = refund(when=JAN + timedelta(days=1))
    result = streak(GIFTS[:2], [early_refund], PERIODS[:2], "ben", as_of=FEB, previous_best=1)
    assert (result.current, result.best) == (1, 1)


def test_full_refund_of_latest_period_ends_streak_without_payment_failure_grace():
    result = streak(GIFTS, [refund("g3", 15000)], PERIODS, "ben", as_of=MAR, previous_best=3)
    assert result.current == 0 and not result.at_risk and result.best == 3


def test_incomplete_period_data_never_silently_returns_a_plausible_streak():
    with pytest.raises(IncompleteBillingHistory):
        streak([replace(GIFTS[0], period_id=None)], [], PERIODS, "ben", as_of=JAN)
    with pytest.raises(IncompleteBillingHistory):
        streak(GIFTS[:1], [], PERIODS[:1], "ben", as_of=FEB)
    with pytest.raises(ValueError, match="overlap"):
        streak([], [], [PERIODS[0], replace(PERIODS[0], id="duplicate-period")], "ben", as_of=JAN)
    with pytest.raises(ValueError, match="skipped"):
        streak(GIFTS[:1], [], [replace(PERIODS[0], status=PeriodStatus.SKIPPED)], "ben", as_of=JAN)


@pytest.mark.parametrize("amount", [True, 0, -1, 1.5])
def test_money_is_strict_integer_minor_units(amount):
    with pytest.raises(ValueError):
        replace(GIFTS[0], amount_minor=amount)


def test_reject_naive_time_and_non_usd():
    with pytest.raises(ValueError):
        replace(GIFTS[0], paid_at=JAN.replace(tzinfo=None))
    with pytest.raises(ValueError):
        replace(GIFTS[0], currency="EUR")
    with pytest.raises(ValueError):
        replace(PERIODS[0], ends_at=JAN + timedelta(days=365))


def test_month_end_billing_uses_actual_periods_including_leap_year():
    jan = datetime(2028, 1, 31, 12, tzinfo=UTC)
    feb = datetime(2028, 2, 29, 12, tzinfo=UTC)
    mar = datetime(2028, 3, 31, 12, tzinfo=UTC)
    periods = [BillingPeriod("a", "ben", jan, feb), BillingPeriod("b", "ben", feb, mar)]
    gifts = [replace(GIFTS[0], paid_at=jan, period_id="a"),
             replace(GIFTS[1], paid_at=feb, period_id="b")]
    assert streak(gifts, [], periods, "ben", as_of=feb).current == 2


def test_missing_month_breaks_continuity_and_another_donor_does_not_fill_it():
    periods = [PERIODS[0], PERIODS[2], replace(PERIODS[1], donor_id="other")]
    gifts = [GIFTS[0], GIFTS[2], replace(GIFTS[1], donor_id="other")]
    result = streak(gifts, [], periods, "ben", as_of=MAR)
    assert result.current == 1


def test_late_older_payment_does_not_retroactively_extend_newer_streak():
    # February was still unpaid when March charged, then recovered afterwards.
    gifts = [GIFTS[0], replace(GIFTS[1], paid_at=MAR + timedelta(days=1)), GIFTS[2]]
    result = streak(gifts, [], PERIODS, "ben", as_of=MAR + timedelta(days=2))
    assert result.current == 1


def test_streak_ignores_fact_arrival_order():
    expected = streak(GIFTS, [], PERIODS, "ben", as_of=MAR)
    for ordering in permutations(GIFTS):
        assert streak(ordering, [], reversed(PERIODS), "ben", as_of=MAR) == expected
