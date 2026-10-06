package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"net/url"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

func saveStaffCredentials(ctx context.Context, q *queries.Queries, s queries.DonorStaff) error {
	return q.SaveStaffCredentials(ctx, queries.SaveStaffCredentialsParams{ID: s.ID, PasswordHash: s.PasswordHash, TotpSecret: s.TotpSecret, TotpCounter: s.TotpCounter, Failures: s.Failures, LockedUntil: s.LockedUntil})
}

const (
	staffSessionLifetime = 8 * time.Hour
	staffLockout         = 15 * time.Minute
	maxSignInFailures    = 5
)

// SignIn verifies a staff password and TOTP code and returns a new session
// token. Unknown addresses verify against a dummy hash so timing does not
// reveal which accounts exist; five failures lock an account for 15 minutes.
func (s *StaffService) SignIn(ctx context.Context, email, password, code string) (string, error) {
	canonical, err := identity.NormalizeEmail(email)
	if err != nil {
		return "", ErrStaffDenied
	}
	release, err := s.passwordSlot(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	var token string
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		staff, err := q.StaffByEmail(ctx, canonical)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		found := err == nil
		now := time.Now().UTC()
		counter, err := s.verifySignIn(staff, found, password, code, now)
		if err != nil {
			return err
		}
		if counter < 0 {
			// The failure is committed, then reported.
			if found {
				return recordFailedSignIn(ctx, q, staff, now)
			}
			return nil
		}
		staff.TotpCounter, staff.Failures, staff.LockedUntil = counter, 0, pgtype.Timestamptz{}
		if err = saveStaffCredentials(ctx, q, staff); err != nil {
			return err
		}
		if token, err = randomToken(48); err != nil {
			return err
		}
		id, err := NewID()
		if err != nil {
			return err
		}
		if err = q.InsertStaffSession(ctx, queries.InsertStaffSessionParams{ID: id, StaffID: staff.ID, TokenHash: HashToken(token), ExpiresAt: timestamp(now.Add(staffSessionLifetime))}); err != nil {
			return err
		}
		return auditStaff(ctx, q, staff.ID, "staff.signed_in", staff.ID.String(), nil, now)
	})
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", ErrStaffDenied
	}
	return token, nil
}

// verifySignIn returns the TOTP counter a successful sign-in consumes, or -1.
// The password is always verified, so every attempt costs the same.
func (s *StaffService) verifySignIn(staff queries.DonorStaff, found bool, password, code string, now time.Time) (int64, error) {
	hash := s.dummyHash
	if found && staff.PasswordHash.Valid {
		hash = staff.PasswordHash.String
	}
	verified, err := credentials.VerifyPassword(password, hash)
	locked := staff.LockedUntil.Valid && staff.LockedUntil.Time.After(now)
	if err != nil || !verified || !found || len(staff.TotpSecret) == 0 || staff.Disabled || locked {
		return -1, nil
	}
	secret, err := credentials.Decrypt(staff.TotpSecret, s.EncryptionKey)
	if err != nil {
		return -1, err
	}
	counter, err := credentials.VerifyOTP(secret, code, now, staff.TotpCounter)
	if err != nil {
		return -1, nil
	}
	return counter, nil
}

// recordFailedSignIn counts a failure against an account that is not locked,
// starting a fresh count once an earlier lock has expired, and audits it.
func recordFailedSignIn(ctx context.Context, q *queries.Queries, staff queries.DonorStaff, now time.Time) error {
	if !staff.LockedUntil.Valid || !staff.LockedUntil.Time.After(now) {
		if staff.LockedUntil.Valid {
			staff.Failures = 0
			staff.LockedUntil = pgtype.Timestamptz{}
		}
		staff.Failures++
		if staff.Failures >= maxSignInFailures {
			staff.LockedUntil = timestamp(now.Add(staffLockout))
		}
		if err := saveStaffCredentials(ctx, q, staff); err != nil {
			return err
		}
	}
	return auditStaff(ctx, q, pgtype.UUID{}, "staff.sign_in_failed", staff.ID.String(), nil, now)
}

func staffInvitation(ctx context.Context, q *queries.Queries, token string) (queries.DonorStaffInvite, queries.DonorStaff, error) {
	var invite queries.DonorStaffInvite
	var staff queries.DonorStaff
	owner, err := q.StaffInviteOwner(ctx, HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return invite, staff, ErrStaffDenied
	}
	if err != nil {
		return invite, staff, err
	}
	staff, err = q.StaffByID(ctx, owner)
	if err != nil {
		return invite, staff, err
	}
	if staff.Disabled || staff.PasswordHash.Valid {
		return invite, staff, ErrStaffDenied
	}
	invite, err = q.StaffInviteByToken(ctx, HashToken(token))
	if err != nil {
		return invite, staff, err
	}
	if invite.ConsumedAt.Valid || !invite.ExpiresAt.Time.After(time.Now().UTC()) || invite.Attempts >= 5 {
		return invite, staff, ErrStaffDenied
	}
	return invite, staff, nil
}

// Provision returns the TOTP enrolment for an invitation, generating its
// secret on first use so reloading the page shows the same one.
func (s *StaffService) Provision(ctx context.Context, token string) (map[string]string, error) {
	var enrolment map[string]string
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		invite, staff, err := staffInvitation(ctx, q, token)
		if err != nil {
			return err
		}
		if len(invite.PendingSecret) == 0 {
			secret := make([]byte, 20)
			if _, err = rand.Read(secret); err != nil {
				return err
			}
			if invite.PendingSecret, err = credentials.Encrypt(secret, s.EncryptionKey); err != nil {
				return err
			}
			if err = saveStaffInvite(ctx, q, invite); err != nil {
				return err
			}
		}
		secret, err := credentials.Decrypt(invite.PendingSecret, s.EncryptionKey)
		if err != nil {
			return err
		}
		encoded := base32.StdEncoding.EncodeToString(secret)
		uri := "otpauth://totp/" + url.PathEscape("DAD Staff:"+staff.Email) + "?" + url.Values{"secret": {encoded}, "issuer": {"DAD Staff"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
		enrolment = map[string]string{"email": staff.Email, "secret": encoded, "uri": uri}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return enrolment, nil
}

// Activate sets the invited account's password once code proves its TOTP
// enrolment. Every attempt counts against the invitation, failed ones too.
func (s *StaffService) Activate(ctx context.Context, token, password, code string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 128 {
		return ErrStaffInput
	}
	release, err := s.passwordSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	activated := false
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		invite, staff, err := staffInvitation(ctx, q, token)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		invite.Attempts++
		if len(invite.PendingSecret) > 0 {
			secret, err := credentials.Decrypt(invite.PendingSecret, s.EncryptionKey)
			if err != nil {
				return err
			}
			if counter, err := credentials.VerifyOTP(secret, code, now, -1); err == nil {
				hash, err := credentials.HashPassword(password)
				if err != nil {
					return err
				}
				staff.PasswordHash, staff.TotpSecret, staff.TotpCounter = text(hash), invite.PendingSecret, counter
				if err = saveStaffCredentials(ctx, q, staff); err != nil {
					return err
				}
				invite.ConsumedAt, invite.PendingSecret = timestamp(now), nil
				if err = auditStaff(ctx, q, staff.ID, "staff.activated", staff.ID.String(), nil, now); err != nil {
					return err
				}
				activated = true
			}
		}
		return saveStaffInvite(ctx, q, invite)
	})
	if err != nil {
		return err
	}
	if !activated {
		return ErrStaffDenied
	}
	return nil
}

func saveStaffInvite(ctx context.Context, q *queries.Queries, invite queries.DonorStaffInvite) error {
	return q.SaveStaffInvite(ctx, queries.SaveStaffInviteParams{ID: invite.ID, Attempts: invite.Attempts, PendingSecret: invite.PendingSecret, ConsumedAt: invite.ConsumedAt})
}
