from tests.donor_contracts import artifacts


def test_go_schema_and_http_contract_match_python():
    for path, expected in artifacts().items():
        assert path.read_text() == expected, f"Refresh {path.name} with python -m tests.donor_contracts and review compatibility"
