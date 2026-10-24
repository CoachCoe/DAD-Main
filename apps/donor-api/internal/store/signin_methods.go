package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"time"

	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/oidc"
	"dad/donor-api/internal/sms"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrSignInUnavailable is returned when a sign-in method is not configured on
// this deployment (no SMS provider, or no OIDC provider).
var ErrSignInUnavailable = errors.New("this sign-in method is not available")

// ErrSignInCode is returned for an expired, missing, or wrong SMS code, and for
// a verified identity with no matching account. It is deliberately one error so
// a caller cannot tell "no account" from "wrong code" — neither reveals whether
// an account exists.
var ErrSignInCode = errors.New("that code is incorrect or has expired")

// phonePatternStore mirrors the ck_donor_phone constraint (E.164).
var phonePatternStore = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

const (
	phoneCodeTTL      = 10 * time.Minute
	phoneCodeMaxTries = 5
)

// PhoneService runs phone/SMS sign-in. Sender is nil when SMS is unconfigured.
type PhoneService struct {
	DB     *pgxpool.Pool
	Sender sms.Sender
}

// BeginSignIn sends a fresh one-time code to phone. An unknown or malformed
// phone is not an error (the response never reveals whether an account exists);
// a code is only stored and sent for a phone that matches an account.
func (s PhoneService) BeginSignIn(ctx context.Context, phone string) error {
	if s.Sender == nil {
		return ErrSignInUnavailable
	}
	if !phonePatternStore.MatchString(phone) {
		return nil
	}
	code, err := numericCode(6)
	if err != nil {
		return err
	}
	sent := false
	if err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if _, err := q.DonorByPhone(ctx, text(phone)); errors.Is(err, pgx.ErrNoRows) {
			return nil // no account: do not store or send, but report success
		} else if err != nil {
			return err
		}
		sent = true
		return q.UpsertPhoneCode(ctx, queries.UpsertPhoneCodeParams{
			Phone: phone, CodeHash: HashToken(code), ExpiresAt: timestamp(time.Now().UTC().Add(phoneCodeTTL)),
		})
	}); err != nil {
		return err
	}
	if !sent {
		return nil
	}
	return s.Sender.Send(ctx, phone, fmt.Sprintf("Your DAD sign-in code is %s. It expires in 10 minutes.", code))
}

// FinishSignIn verifies the code for phone and issues a session for client.
// Wrong, expired, over-tried, or no-account all collapse to ErrSignInCode.
func (s PhoneService) FinishSignIn(ctx context.Context, phone, code string, client Client) (Tokens, error) {
	if s.Sender == nil {
		return Tokens{}, ErrSignInUnavailable
	}
	if !phonePatternStore.MatchString(phone) {
		return Tokens{}, ErrSignInCode
	}
	tokens, recordAttempt, err := s.finish(ctx, phone, code, client)
	if recordAttempt {
		// The verification tx rolled back on rejection, so the failed attempt is
		// recorded in its own committed statement — otherwise a wrong code would
		// never increment the counter and could be retried without limit.
		if recErr := queries.New(s.DB).RecordPhoneCodeAttempt(ctx, phone); recErr != nil {
			return Tokens{}, recErr
		}
	}
	return tokens, err
}

// finish verifies the code in one transaction. It returns recordAttempt=true
// when the caller must persist a failed attempt after the tx rolls back.
func (s PhoneService) finish(ctx context.Context, phone, code string, client Client) (Tokens, bool, error) {
	tokens, err := transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (Tokens, error) {
		q := queries.New(tx)
		pending, err := q.PhoneCode(ctx, phone)
		if errors.Is(err, pgx.ErrNoRows) {
			return Tokens{}, ErrSignInCode
		}
		if err != nil {
			return Tokens{}, err
		}
		now := time.Now().UTC()
		expired := now.After(pending.ExpiresAt.Time)
		overTried := pending.Attempts >= phoneCodeMaxTries
		matched := HashToken(code) == pending.CodeHash
		if expired || overTried || !matched {
			// A live-but-wrong code: signal that the attempt must be counted.
			if !expired && !overTried {
				return Tokens{}, errCountAttempt
			}
			return Tokens{}, ErrSignInCode
		}
		donor, err := q.DonorByPhone(ctx, text(phone))
		if errors.Is(err, pgx.ErrNoRows) {
			return Tokens{}, ErrSignInCode
		}
		if err != nil {
			return Tokens{}, err
		}
		if err := q.DeletePhoneCode(ctx, phone); err != nil {
			return Tokens{}, err
		}
		return issueSession(ctx, q, donor.ID, client, now)
	})
	if errors.Is(err, errCountAttempt) {
		return Tokens{}, true, ErrSignInCode
	}
	return tokens, false, err
}

// errCountAttempt is an internal sentinel: the verification tx failed on a
// live-but-wrong code and the caller must record the attempt after rollback.
var errCountAttempt = errors.New("record failed phone attempt")

// OIDCService runs Apple/Google sign-in. Verifier/Settings are nil when OIDC is
// unconfigured.
type OIDCService struct {
	DB       *pgxpool.Pool
	Verifier oidc.Verifier
	Settings *oidc.Settings
}

// SignIn verifies a provider id_token and issues a session for the donor it
// belongs to. A verified identity is matched first by a prior link, then by the
// verified email of an existing donor (which also records the link); a verified
// identity with no matching donor returns ErrSignInCode rather than creating an
// account — account creation happens in the giving flow, as it does for email.
func (s OIDCService) SignIn(ctx context.Context, provider oidc.Provider, idToken string, client Client) (Tokens, error) {
	if s.Verifier == nil || !s.Settings.Configured(provider) {
		return Tokens{}, ErrSignInUnavailable
	}
	ident, err := s.Verifier.Verify(ctx, provider, idToken)
	if err != nil {
		return Tokens{}, ErrSignInCode
	}
	return transact(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) (Tokens, error) {
		q := queries.New(tx)
		now := time.Now().UTC()

		// Already linked: sign that donor in.
		if link, err := q.OIDCIdentity(ctx, queries.OIDCIdentityParams{
			Provider: string(ident.Provider), Subject: ident.Subject,
		}); err == nil {
			donor, err := q.LockDonor(ctx, link.DonorID)
			if err != nil {
				return Tokens{}, err
			}
			if donor.Disabled {
				return Tokens{}, ErrSignInCode
			}
			if err := q.TouchOIDCIdentity(ctx, queries.TouchOIDCIdentityParams{ID: link.ID, LastUsedAt: timestamp(now)}); err != nil {
				return Tokens{}, err
			}
			return issueSession(ctx, q, donor.ID, client, now)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return Tokens{}, err
		}

		// Not linked: link to an existing donor by verified email, else refuse.
		if ident.Email == "" {
			return Tokens{}, ErrSignInCode
		}
		canonical, err := identity.NormalizeEmail(ident.Email)
		if err != nil {
			return Tokens{}, ErrSignInCode
		}
		donor, err := q.DonorByEmail(ctx, canonical)
		if errors.Is(err, pgx.ErrNoRows) {
			return Tokens{}, ErrSignInCode
		}
		if err != nil {
			return Tokens{}, err
		}
		locked, err := q.LockDonor(ctx, donor.ID)
		if err != nil {
			return Tokens{}, err
		}
		if locked.Disabled {
			return Tokens{}, ErrSignInCode
		}
		id, err := NewID()
		if err != nil {
			return Tokens{}, err
		}
		if err := q.InsertOIDCIdentity(ctx, queries.InsertOIDCIdentityParams{
			ID: id, DonorID: locked.ID, Provider: string(ident.Provider), Subject: ident.Subject,
			LastUsedAt: timestamp(now),
		}); err != nil {
			return Tokens{}, err
		}
		return issueSession(ctx, q, locked.ID, client, now)
	})
}

// numericCode returns an n-digit base-10 code with uniform digits.
func numericCode(n int) (string, error) {
	const digits = "0123456789"
	buf := make([]byte, n)
	for i := range buf {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		if err != nil {
			return "", err
		}
		buf[i] = digits[idx.Int64()]
	}
	return string(buf), nil
}
