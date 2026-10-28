package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"strings"
	"time"

	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/recognition"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrFoundingClaimsUnconfigured = errors.New("founding claims are not configured")

const (
	maxFoundingClaimBytes = 4 << 20
	maxFoundingClaimRows  = 50000
	foundingClaimChunk    = 1000
	foundingClaimSheet    = "claims"
)

// FoundingClaimKey keys the stored hash of each waitlist address, so the
// hashes cannot be reversed by hashing guessed addresses.
type FoundingClaimKey []byte

// ParseFoundingClaimKey reads a base64url-encoded 32-byte key.
func ParseFoundingClaimKey(value string) (FoundingClaimKey, error) {
	key, err := base64.URLEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("must be 32 random bytes, base64url-encoded")
	}
	return key, nil
}

// hash is the stored form of a normalized address.
func (k FoundingClaimKey) hash(email string) string {
	mac := hmac.New(sha256.New, k)
	mac.Write([]byte(email))
	return hex.EncodeToString(mac.Sum(nil))
}

// grant records Founding Donor for a donor whose address has an unexpired
// claim. Without a key no claims can have been imported.
func (k FoundingClaimKey) grant(ctx context.Context, q *queries.Queries, donor pgtype.UUID, email string, now time.Time) error {
	if len(k) == 0 {
		return nil
	}
	return q.GrantFoundingClaim(ctx, queries.GrantFoundingClaimParams{DonorID: donor, EmailHash: k.hash(email), Now: timestamp(now)})
}

// FoundingClaimService imports website waitlist sign-ups and earlier website
// donors so their accounts earn Founding Donor (D-6).
type FoundingClaimService struct {
	Staff *StaffService
	Key   FoundingClaimKey
}

type FoundingClaimImport struct {
	Rows int `json:"rows"`
}

// Authorize also refuses when no key is configured, so the caller can reject
// an import before reading its body.
func (s FoundingClaimService) Authorize(ctx context.Context, token string) error {
	if _, err := requireStaff(ctx, queries.New(s.Staff.DB), token, "badge.manage", time.Now().UTC()); err != nil {
		return err
	}
	if len(s.Key) == 0 {
		return ErrFoundingClaimsUnconfigured
	}
	return nil
}

type foundingClaim struct {
	signedUp time.Time
	source   string
}

// Import records each row of a CSV with the columns email, signed_up_on
// (YYYY-MM-DD) and source (WAITLIST or WEBSITE_DONOR), and grants Founding
// Donor to the donors who already hold those addresses. Only a keyed hash of
// each address is stored. It refuses the whole sheet if any row is invalid.
func (s FoundingClaimService) Import(ctx context.Context, token, sheet string) (FoundingClaimImport, error) {
	if len(s.Key) == 0 {
		return FoundingClaimImport{}, ErrFoundingClaimsUnconfigured
	}
	now := time.Now().UTC()
	problems := &PublicationError{}
	claims := parseFoundingClaims(problems, sheet, now)
	if len(problems.Problems) > 0 {
		return FoundingClaimImport{}, problems
	}
	emails := make([]string, 0, len(claims))
	for email := range claims {
		emails = append(emails, email)
	}
	slices.Sort(emails)
	return staffWrite(
		ctx,
		s.Staff,
		token,
		"badge.manage",
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (FoundingClaimImport, error) {
			q := queries.New(tx)
			for chunk := range slices.Chunk(emails, foundingClaimChunk) {
				params := queries.ImportFoundingClaimsParams{
					Hashes:     make([]string, len(chunk)),
					Dates:      make([]pgtype.Date, len(chunk)),
					Sources:    make([]string, len(chunk)),
					ImportedAt: timestamp(now),
				}
				for i, email := range chunk {
					params.Hashes[i] = s.Key.hash(email)
					params.Dates[i] = pgtype.Date{Time: claims[email].signedUp, Valid: true}
					params.Sources[i] = claims[email].source
				}
				if _, err := q.ImportFoundingClaims(ctx, params); err != nil {
					return FoundingClaimImport{}, err
				}
				if err := q.GrantFoundingClaimsByEmail(ctx, queries.GrantFoundingClaimsByEmailParams{Emails: chunk, Now: timestamp(now)}); err != nil {
					return FoundingClaimImport{}, err
				}
			}
			result := FoundingClaimImport{Rows: len(claims)}
			return result, auditStaff(ctx, q, actor.ID, "founding.claims_imported", "donor_founding_claims", result, now)
		},
	)
}

// parseFoundingClaims returns the sheet's claims by normalized address, each
// address keeping its earliest sign-up.
func parseFoundingClaims(problems *PublicationError, sheet string, now time.Time) map[string]foundingClaim {
	if len(sheet) > maxFoundingClaimBytes {
		problems.add(foundingClaimSheet, 0, "", "too_large")
		return nil
	}
	if !plainText(sheet, true) {
		problems.add(foundingClaimSheet, 0, "", "invalid_text")
		return nil
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimLeft(sheet, "\ufeff")))
	header, err := reader.Read()
	if err != nil || !slices.Equal(header, []string{"email", "signed_up_on", "source"}) {
		problems.add(foundingClaimSheet, 1, "", "invalid_header")
		return nil
	}
	cutoff := recognition.FoundingCutoff.Format(time.DateOnly)
	claims := map[string]foundingClaim{}
	for rows := 0; ; rows++ {
		values, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return claims
		}
		if err != nil {
			line := 0
			if parseErr, ok := errors.AsType[*csv.ParseError](err); ok {
				line = parseErr.StartLine
			}
			problems.add(foundingClaimSheet, line, "", "malformed_csv")
			return nil
		}
		line, _ := reader.FieldPos(0)
		if rows >= maxFoundingClaimRows {
			problems.add(foundingClaimSheet, line, "", "too_many_rows")
			return nil
		}
		email, err := identity.NormalizeEmail(values[0])
		if err != nil {
			problems.add(foundingClaimSheet, line, "email", "invalid_email")
		}
		signedUp, err := time.Parse(time.DateOnly, values[1])
		if err != nil || values[1] >= cutoff {
			problems.add(foundingClaimSheet, line, "signed_up_on", "invalid_date")
		}
		source := values[2]
		if source != "WAITLIST" && source != "WEBSITE_DONOR" {
			problems.add(foundingClaimSheet, line, "source", "invalid_source")
		}
		if len(problems.Problems) > 0 {
			continue
		}
		if held, ok := claims[email]; !ok || signedUp.Before(held.signedUp) {
			claims[email] = foundingClaim{signedUp: signedUp, source: source}
		}
	}
}
