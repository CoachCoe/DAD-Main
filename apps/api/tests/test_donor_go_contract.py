"""Language-neutral business scenarios shared by the Python and Go implementations."""
import json
from dataclasses import asdict
from datetime import datetime
from pathlib import Path

import pytest

from impactgraph import donor_metrics as metrics

FIXTURES = Path(__file__).resolve().parents[2] / "donor-api/testdata/recognition.json"
CASES = json.loads(FIXTURES.read_text())


def instant(value):
    return datetime.fromisoformat(value)


def canonical(value):
    if isinstance(value, datetime):
        return value.isoformat().replace("+00:00", "Z")
    if isinstance(value, dict):
        return {key: canonical(item) for key, item in value.items()}
    return value


def evaluate(case):
    gifts = []
    for row in case["gifts"]:
        data = dict(row, paid_at=instant(row["paid_at"]), period_id=row["period_id"] or None)
        if row["candidate"]:
            data["candidate"] = metrics.ReferralCandidate(**dict(row["candidate"], expires_at=instant(row["candidate"]["expires_at"])))
        gifts.append(metrics.Gift(**data))
    refunds = [metrics.Refund(**dict(row, updated_at=instant(row["updated_at"]))) for row in case["refunds"]]
    periods = [metrics.BillingPeriod(**dict(row, due_at=instant(row["due_at"]), ends_at=instant(row["ends_at"]))) for row in case["periods"]]
    now = instant(case["as_of"])
    match case["operation"]:
        case "net":
            return metrics.net_gifts(gifts, refunds, as_of=now)
        case "referrals":
            return {key: asdict(value) for key, value in metrics.referral_totals(gifts, refunds, as_of=now).items()}
        case "founding":
            return metrics.founding_donor(gifts, case["donor_id"], as_of=now, previously_awarded=case["previously_awarded"])
        case "streak":
            return canonical(asdict(metrics.streak(gifts, refunds, periods, case["donor_id"], as_of=now,
                previous_best=case["previous_best"], stopped_period_ends=[instant(t) for t in case["stops"]],
                pause_windows=[(instant(p["start"]), instant(p["end"])) for p in case["pauses"]])))
        case _:
            raise AssertionError("Unknown fixture operation")


@pytest.mark.parametrize("case", CASES, ids=lambda case: case["name"])
def test_shared_recognition_contract(case):
    if "error" in case:
        expected = metrics.IncompleteBillingHistory if case["error"] == "incomplete" else ValueError
        with pytest.raises(expected):
            evaluate(case)
    else:
        assert evaluate(case) == case["expected"]
