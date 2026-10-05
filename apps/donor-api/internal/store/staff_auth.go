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
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	staff, err := q.StaffByEmail(ctx, canonical)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	found := err == nil
	now := time.Now().UTC()
	hash := s.dummyHash
	if found && staff.PasswordHash.Valid {
		hash = staff.PasswordHash.String
	}
	verified, verifyErr := credentials.VerifyPassword(password, hash)
	locked := found && staff.LockedUntil.Valid && staff.LockedUntil.Time.After(now)
	counter := int64(-1)
	if verifyErr == nil && verified && found && len(staff.TotpSecret) > 0 && !staff.Disabled && !locked {
		secret, err := credentials.Decrypt(staff.TotpSecret, s.EncryptionKey)
		if err != nil {
			return "", err
		}
		if value, err := credentials.VerifyOTP(secret, code, now, staff.TotpCounter); err == nil {
			counter = value
		}
	}
	if counter < 0 {
		if found {
			if !locked {
				if staff.LockedUntil.Valid {
					staff.Failures = 0
					staff.LockedUntil = pgtype.Timestamptz{}
				}
				staff.Failures++
				if staff.Failures >= 5 {
					staff.LockedUntil = timestamp(now.Add(15 * time.Minute))
				}
				if err = saveStaffCredentials(ctx, q, staff); err != nil {
					return "", err
				}
			}
			if err = auditStaff(ctx, q, pgtype.UUID{}, "staff.sign_in_failed", staff.ID.String(), nil, now); err != nil {
				return "", err
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", ErrStaffDenied
	}
	staff.TotpCounter, staff.Failures, staff.LockedUntil = counter, 0, pgtype.Timestamptz{}
	if err = saveStaffCredentials(ctx, q, staff); err != nil {
		return "", err
	}
	token, err := randomToken(48)
	if err != nil {
		return "", err
	}
	id, err := NewID()
	if err != nil {
		return "", err
	}
	if err = q.InsertStaffSession(ctx, queries.InsertStaffSessionParams{ID: id, StaffID: staff.ID, TokenHash: HashToken(token), ExpiresAt: timestamp(now.Add(8 * time.Hour))}); err != nil {
		return "", err
	}
	if err = auditStaff(ctx, q, staff.ID, "staff.signed_in", staff.ID.String(), nil, now); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}

func staffInvitation(ctx context.Context, q *queries.Queries, token string, now time.Time) (queries.DonorStaffInvite, queries.DonorStaff, error) {
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
	if invite.ConsumedAt.Valid || !invite.ExpiresAt.Time.After(now) || invite.Attempts >= 5 {
		return invite, staff, ErrStaffDenied
	}
	return invite, staff, nil
}

func (s *StaffService) Provision(ctx context.Context, token string) (map[string]string, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	invite, staff, err := staffInvitation(ctx, q, token, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if len(invite.PendingSecret) == 0 {
		secret := make([]byte, 20)
		if _, err = rand.Read(secret); err != nil {
			return nil, err
		}
		invite.PendingSecret, err = credentials.Encrypt(secret, s.EncryptionKey)
		if err != nil {
			return nil, err
		}
		if err = q.SaveStaffInvite(ctx, queries.SaveStaffInviteParams{ID: invite.ID, Attempts: invite.Attempts, PendingSecret: invite.PendingSecret, ConsumedAt: invite.ConsumedAt}); err != nil {
			return nil, err
		}
	}
	secret, err := credentials.Decrypt(invite.PendingSecret, s.EncryptionKey)
	if err != nil {
		return nil, err
	}
	encoded := base32.StdEncoding.EncodeToString(secret)
	uri := "otpauth://totp/" + url.PathEscape("DAD Staff:"+staff.Email) + "?" + url.Values{"secret": {encoded}, "issuer": {"DAD Staff"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]string{"email": staff.Email, "secret": encoded, "uri": uri}, nil
}

func (s *StaffService) Activate(ctx context.Context, token, password, code string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 12 || utf8.RuneCountInString(password) > 128 {
		return ErrStaffInput
	}
	release, err := s.passwordSlot(ctx)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := queries.New(tx)
	now := time.Now().UTC()
	invite, staff, err := staffInvitation(ctx, q, token, now)
	if err != nil {
		return err
	}
	invite.Attempts++
	counter := int64(-1)
	if len(invite.PendingSecret) > 0 {
		secret, err := credentials.Decrypt(invite.PendingSecret, s.EncryptionKey)
		if err != nil {
			return err
		}
		if value, err := credentials.VerifyOTP(secret, code, now, -1); err == nil {
			counter = value
		}
	}
	if counter >= 0 {
		hash, err := credentials.HashPassword(password)
		if err != nil {
			return err
		}
		staff.PasswordHash, staff.TotpSecret, staff.TotpCounter = pgtype.Text{String: hash, Valid: true}, invite.PendingSecret, counter
		if err = saveStaffCredentials(ctx, q, staff); err != nil {
			return err
		}
		invite.ConsumedAt, invite.PendingSecret = timestamp(now), nil
		if err = auditStaff(ctx, q, staff.ID, "staff.activated", staff.ID.String(), nil, now); err != nil {
			return err
		}
	}
	if err = q.SaveStaffInvite(ctx, queries.SaveStaffInviteParams{ID: invite.ID, Attempts: invite.Attempts, PendingSecret: invite.PendingSecret, ConsumedAt: invite.ConsumedAt}); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	if counter < 0 {
		return ErrStaffDenied
	}
	return nil
}
