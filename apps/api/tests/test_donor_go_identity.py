"""Keep representative Go identity vectors tied to the current Python behavior."""
import json
from pathlib import Path

import pytest

from impactgraph.donors import normalize_email


def test_shared_identity_sequences():
    fixture = Path(__file__).resolve().parents[2] / "donor-api/testdata/identity.json"
    for case in json.loads(fixture.read_text())["cases"]:
        if case["expected"] is None:
            with pytest.raises(ValueError):
                normalize_email(case["input"])
        else:
            assert normalize_email(case["input"]) == case["expected"]
