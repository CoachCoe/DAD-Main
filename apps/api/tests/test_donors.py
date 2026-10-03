"""A referral survives guest checkout and email claim without granting public ownership."""

import os
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime, timedelta
from threading import Barrier
from uuid import uuid4

import pytest
from sqlalchemy import create_engine, event, func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session
from sqlalchemy.schema import CreateSchema, DropSchema

from impactgraph.donors import (
    DonorAccessDenied,
    GiftFrequency,
    ReferralKind,
    activate_sharing,
    authenticated_donor,
    capture_referral,
    claim_account,
    guest_donor,
    issue_login_link,
    normalize_email,
    owned_checkout,
    prepare_checkout,
    sign_out,
)
from impactgraph.persistence import (
    Base,
    DonorCheckoutRecord,
    DonorLoginRecord,
    DonorRecord,
    DonorSessionRecord,
    ReferralTouchRecord,
)

NOW = datetime(2026, 12, 17, 12, tzinfo=UTC)


@pytest.fixture(params=["sqlite"] + (["postgres"] if os.getenv("TEST_POSTGRES_URL") else []))
def engine(request, tmp_path):
    postgres = request.param == "postgres"
    engine = create_engine(os.environ["TEST_POSTGRES_URL"] if postgres else
                           f"sqlite+pysqlite:///{tmp_path / 'donors.db'}")
    schema = f"donors_{uuid4().hex}" if postgres else None
    if postgres:
        with engine.begin() as connection:
            connection.execute(CreateSchema(schema))
        engine = engine.execution_options(schema_translate_map={None: schema})
    else:
        @event.listens_for(engine, "connect")
        def foreign_keys(connection, _):
            connection.execute("PRAGMA foreign_keys=ON")

    tables = [table for table in Base.metadata.sorted_tables
              if table.name == "donors" or table.name.startswith("donor_")]
    try:
        Base.metadata.create_all(engine, tables=tables)
        yield engine
    finally:
        if postgres:
            with engine.begin() as connection:
                connection.execute(DropSchema(schema, cascade=True))
        engine.dispose()


@pytest.fixture
def session(engine):
    with Session(engine, expire_on_commit=False) as session:
        yield session


def account(session, email="alice@example.org"):
    donor = guest_donor(session, email)
    link = issue_login_link(session, email, now=NOW)
    token = claim_account(session, link, now=NOW)
    return donor, token


def referrer(session, email="alice@example.org", name="Alice"):
    donor, token = account(session, email)
    return donor, activate_sharing(session, token, name, now=NOW)


def test_guest_checkout_survives_claim_without_losing_attribution(session):
    with session.begin():
        alice, code = referrer(session)
        visit = capture_referral(session, code, ReferralKind.IRL, now=NOW)
        checkout = prepare_checkout(session, " Ben@Example.org ", 3000, visitor_token=visit, now=NOW)
        assert session.get(DonorRecord, checkout.donor_id).claimed_at is None
        checkout_id, alice_id = checkout.id, alice.id
    # A new transaction/session identity map models returning from email later.
    session.expunge_all()
    with session.begin():
        link = issue_login_link(session, "ben@example.org", now=NOW)
        login = claim_account(session, link, now=NOW + timedelta(minutes=1))
        claimed = owned_checkout(session, login, checkout_id, now=NOW + timedelta(minutes=1))
        assert claimed.referrer_id == alice_id
        assert claimed.referral_kind == "IRL"
        assert claimed.currency == "USD" and claimed.frequency == "MONTHLY"
        assert claimed.amount_minor == 3000


def test_first_touch_wins_without_extending_window(session):
    with session.begin():
        alice, code = referrer(session)
        _, carol = referrer(session, "carol@example.org", "Carol")
        visit = capture_referral(session, code, ReferralKind.LINK, now=NOW)
        assert capture_referral(session, carol, ReferralKind.IRL, visitor_token=visit,
                                now=NOW + timedelta(days=29)) == visit
        checkout = prepare_checkout(session, "ben@example.org", 3000, visitor_token=visit,
                                    now=NOW + timedelta(days=29))
        assert checkout.referrer_id == alice.id and checkout.referral_kind == "LINK"
        assert checkout.referral_expires_at.replace(tzinfo=UTC) == NOW + timedelta(days=30)
        expired = prepare_checkout(session, "ben@example.org", 3000, visitor_token=visit,
                                   now=NOW + timedelta(days=30))
        assert expired.referrer_id is None


def test_new_touch_after_expiry_does_not_rewrite_existing_checkout(session):
    with session.begin():
        alice, code = referrer(session)
        carol, other = referrer(session, "carol@example.org", "Carol")
        visit = capture_referral(session, code, ReferralKind.LINK, now=NOW)
        original = prepare_checkout(session, "ben@example.org", 3000, visitor_token=visit, now=NOW)
        later = NOW + timedelta(days=30)
        fresh = capture_referral(session, other, ReferralKind.IRL, visitor_token=visit, now=later)
        assert fresh != visit
        next_checkout = prepare_checkout(session, "ben@example.org", 3000,
                                         visitor_token=fresh, now=later)
        assert next_checkout.referrer_id == carol.id
        assert original.referrer_id == alice.id and original.referral_kind == "LINK"


def test_direct_and_untrusted_visits_do_not_invent_credit(session):
    with session.begin():
        assert capture_referral(session, "invented", ReferralKind.IRL, now=NOW) is None
        _, code = referrer(session)
        token = capture_referral(session, code, ReferralKind.LINK, now=NOW)
        assert capture_referral(session, "invented", ReferralKind.IRL,
                                visitor_token=token, now=NOW) == token
        for visitor in (None, "made-up-cookie"):
            checkout = prepare_checkout(session, "ben@example.org", 3000,
                                        visitor_token=visitor, now=NOW)
            assert checkout.referrer_id is None and checkout.referral_kind is None


def test_self_referral_is_removed_but_gift_is_allowed(session):
    with session.begin():
        alice, code = referrer(session)
        visit = capture_referral(session, code, ReferralKind.IRL, now=NOW)
        checkout = prepare_checkout(session, " ALICE@EXAMPLE.ORG ", 3000,
                                    visitor_token=visit, now=NOW)
        assert checkout.donor_id == alice.id
        assert checkout.referrer_id is None


def test_matching_email_or_checkout_id_does_not_grant_read_access(session):
    with session.begin():
        checkout = prepare_checkout(session, "ben@example.org", 3000, now=NOW)
        _, alice_session = account(session)
        for identifier in (checkout.id, uuid4()):
            with pytest.raises(LookupError, match="Checkout not found"):
                owned_checkout(session, alice_session, identifier, now=NOW)
        for secret in ("ben@example.org", str(checkout.id), "unknown"):
            with pytest.raises(DonorAccessDenied):
                owned_checkout(session, secret, checkout.id, now=NOW)


def test_login_tokens_are_hashed_single_use_and_invalidate_siblings(session):
    with session.begin():
        guest_donor(session, "ben@example.org")
        first = issue_login_link(session, "ben@example.org", now=NOW)
        second = issue_login_link(session, "ben@example.org", now=NOW)
        hashes = list(session.scalars(select(DonorLoginRecord.token_hash)))
        assert first not in hashes and second not in hashes
        login = claim_account(session, first, now=NOW)
        assert login != first
        assert login != session.scalar(select(DonorSessionRecord.token_hash))
        for token in (first, second):
            with pytest.raises(DonorAccessDenied):
                claim_account(session, token, now=NOW)
        assert session.scalar(select(func.count()).select_from(DonorSessionRecord)) == 1


def test_login_expiry_is_exact_and_does_not_claim_account(session):
    with session.begin():
        donor = guest_donor(session, "ben@example.org")
        link = issue_login_link(session, donor.email, now=NOW)
        with pytest.raises(DonorAccessDenied):
            claim_account(session, link, now=NOW + timedelta(minutes=15))
        assert donor.claimed_at is None
        assert session.scalar(select(func.count()).select_from(DonorSessionRecord)) == 0


def test_unknown_email_creates_no_account_or_link(session):
    with session.begin():
        assert issue_login_link(session, "unknown@example.org", now=NOW) is None
        assert session.scalar(select(func.count()).select_from(DonorRecord)) == 0


def test_session_expiry_logout_and_disabled_donor(session):
    with session.begin():
        donor, token = account(session)
        assert authenticated_donor(session, token, now=NOW + timedelta(days=29)).id == donor.id
        with pytest.raises(DonorAccessDenied):
            authenticated_donor(session, token, now=NOW + timedelta(days=30))
        donor.disabled = True
        session.flush()
        with pytest.raises(DonorAccessDenied):
            authenticated_donor(session, token, now=NOW)
        assert issue_login_link(session, donor.email, now=NOW) is None
        donor.disabled = False
        sign_out(session, token, now=NOW)
        sign_out(session, token, now=NOW)
        with pytest.raises(DonorAccessDenied):
            authenticated_donor(session, token, now=NOW)


def test_sharing_requires_claim_and_explicit_name_and_code_rotation(session):
    with session.begin():
        donor = guest_donor(session, "alice@example.org")
        assert donor.referral_code is None and donor.public_name is None
        with pytest.raises(DonorAccessDenied):
            activate_sharing(session, donor.email, "Alice", now=NOW)
        link = issue_login_link(session, donor.email, now=NOW)
        token = claim_account(session, link, now=NOW)
        code = activate_sharing(session, token, "Alice", now=NOW)
        assert activate_sharing(session, token, "Al", now=NOW) == code
        replacement = activate_sharing(session, token, "Al", replace_code=True, now=NOW)
        assert replacement != code
        assert capture_referral(session, code, ReferralKind.LINK, now=NOW) is None
        assert capture_referral(session, replacement, ReferralKind.IRL, now=NOW)


def test_case_matching_does_not_merge_aliases(session):
    with session.begin():
        donor = guest_donor(session, " Ben@Example.org ")
        assert guest_donor(session, "ben@example.org").id == donor.id
        assert guest_donor(session, "ben+giving@example.org").id != donor.id
        assert guest_donor(session, "b.en@example.org").id != donor.id


@pytest.mark.parametrize("email", ["bad", "a@@b.com", "a b@example.org", "a\n@example.org",
                                   ".a@example.org", "a..b@example.org", "a@-bad.org"])
def test_invalid_email_is_rejected(email):
    with pytest.raises(ValueError):
        normalize_email(email)


@pytest.mark.parametrize("amount", [0, 99, -100, True, 3000.0, "3000", 2**63])
def test_invalid_amount_creates_no_guest(session, amount):
    with session.begin():
        with pytest.raises(ValueError):
            prepare_checkout(session, "ben@example.org", amount, now=NOW)
        assert session.scalar(select(func.count()).select_from(DonorRecord)) == 0


def test_one_time_checkout_and_smallest_allowed_gift(session):
    with session.begin():
        checkout = prepare_checkout(session, "ben@example.org", 100,
                                    frequency=GiftFrequency.ONE_TIME, now=NOW)
        assert checkout.frequency == "ONE_TIME" and checkout.currency == "USD"


@pytest.mark.parametrize("changes", [
    {"amount_minor": 99}, {"currency": "EUR"}, {"frequency": "YEARLY"},
    {"referral_kind": "IRL"}, {"referrer_id": "self"},
])
def test_database_enforces_draft_invariants(session, changes):
    with session.begin():
        donor = guest_donor(session, "ben@example.org")
        values = {"donor_id": donor.id, "amount_minor": 3000,
                  "currency": "USD", "frequency": "MONTHLY"}
        values.update(changes)
        if values.get("referrer_id") == "self":
            values.update(referrer_id=donor.id, referral_kind="IRL", referral_expires_at=NOW)
        with pytest.raises(IntegrityError), session.begin_nested():
            session.add(DonorCheckoutRecord(**values))
            session.flush()


def test_rollback_leaves_no_claim_or_session_and_link_can_retry(session):
    with session.begin():
        donor = guest_donor(session, "ben@example.org")
        link = issue_login_link(session, donor.email, now=NOW)
    with pytest.raises(RuntimeError), session.begin():
        claim_account(session, link, now=NOW)
        raise RuntimeError("delivery handler failed before commit")
    with session.begin():
        assert session.get(DonorRecord, donor.id).claimed_at is None
        assert session.scalar(select(func.count()).select_from(DonorSessionRecord)) == 0
        assert claim_account(session, link, now=NOW)


def test_visitor_secret_is_not_persisted(session):
    with session.begin():
        _, code = referrer(session)
        visit = capture_referral(session, code, ReferralKind.IRL, now=NOW)
        assert session.scalar(select(ReferralTouchRecord.token_hash)) != visit


def test_concurrent_redemption_creates_only_one_session(engine):
    with Session(engine) as session, session.begin():
        guest_donor(session, "ben@example.org")
        link = issue_login_link(session, "ben@example.org", now=NOW)
    start = Barrier(2)

    def redeem():
        start.wait(timeout=10)
        try:
            with Session(engine) as session, session.begin():
                claim_account(session, link, now=NOW)
            return "claimed"
        except DonorAccessDenied:
            return "refused"

    with ThreadPoolExecutor(max_workers=2) as pool:
        attempts = [pool.submit(redeem) for _ in range(2)]
        assert sorted(attempt.result(timeout=15) for attempt in attempts) == ["claimed", "refused"]
    with Session(engine) as session:
        assert session.scalar(select(func.count()).select_from(DonorSessionRecord)) == 1


def test_disabling_account_invalidates_previously_issued_link(session):
    with session.begin():
        donor = guest_donor(session, "ben@example.org")
        link = issue_login_link(session, donor.email, now=NOW)
        donor.disabled = True
    with pytest.raises(DonorAccessDenied), session.begin():
        claim_account(session, link, now=NOW)


def test_aborted_guest_checkout_does_not_leave_a_donor(session):
    with pytest.raises(RuntimeError), session.begin():
        prepare_checkout(session, "ben@example.org", 3000, now=NOW)
        raise RuntimeError("checkout creation failed")
    with session.begin():
        assert session.scalar(select(func.count()).select_from(DonorRecord)) == 0
        assert session.scalar(select(func.count()).select_from(DonorCheckoutRecord)) == 0


def test_concurrent_guest_checkouts_share_one_identity(engine):
    start = Barrier(2)

    def checkout():
        start.wait(timeout=10)
        with Session(engine) as session, session.begin():
            return prepare_checkout(session, "ben@example.org", 3000, now=NOW).donor_id

    with ThreadPoolExecutor(max_workers=2) as pool:
        attempts = [pool.submit(checkout) for _ in range(2)]
        assert len({attempt.result(timeout=15) for attempt in attempts}) == 1
    with Session(engine) as session:
        assert session.scalar(select(func.count()).select_from(DonorRecord)) == 1
        assert session.scalar(select(func.count()).select_from(DonorCheckoutRecord)) == 2
