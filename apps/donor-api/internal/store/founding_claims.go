package store

import (
	"context"
	"crypto/sha256"
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

// FoundingClaimExpiry is when waitlist claims (D-6) stop being honoured.
var FoundingClaimExpiry = recognition.FoundingCutoff.AddDate(0, 0, 90)

const (
	maxFoundingClaimBytes = 4 << 20
	maxFoundingClaimRows  = 50000
	foundingClaimChunk    = 1000
	foundingClaimSheet    = "claims"
)

// FoundingClaimService imports website waitlist sign-ups and earlier website
// donors so their accounts earn Founding Donor (D-6).
type FoundingClaimService struct{ Staff *StaffService }

type FoundingClaimImport struct {
	Rows int `json:"rows"`
}

func (s FoundingClaimService) Authorize(ctx context.Context, token string) error {
	_, err := requireStaff(ctx, queries.New(s.Staff.DB), token, "badge.manage", time.Now().UTC())
	return err
}

type foundingClaim struct {
	signedUp time.Time
	source   string
}

// Import records each row of a CSV with the columns email, signed_up_on
// (YYYY-MM-DD) and source (WAITLIST or WEBSITE_DONOR). Only a hash of each
// address is stored. It refuses the whole sheet if any row is invalid.
func (s FoundingClaimService) Import(ctx context.Context, token, sheet string) (FoundingClaimImport, error) {
	now := time.Now().UTC()
	problems := &PublicationError{}
	claims := parseFoundingClaims(problems, sheet, now)
	if len(problems.Problems) > 0 {
		return FoundingClaimImport{}, problems
	}
	hashes := make([]string, 0, len(claims))
	for hash := range claims {
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)
	return staffWrite(
		ctx,
		s.Staff,
		token,
		"badge.manage",
		func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (FoundingClaimImport, error) {
			q := queries.New(tx)
			for chunk := range slices.Chunk(hashes, foundingClaimChunk) {
				params := queries.ImportFoundingClaimsParams{
					Hashes:     chunk,
					Dates:      make([]pgtype.Date, len(chunk)),
					Sources:    make([]string, len(chunk)),
					ImportedAt: timestamp(now),
					ExpiresAt:  timestamp(FoundingClaimExpiry),
				}
				for i, hash := range chunk {
					params.Dates[i] = pgtype.Date{Time: claims[hash].signedUp, Valid: true}
					params.Sources[i] = claims[hash].source
				}
				if _, err := q.ImportFoundingClaims(ctx, params); err != nil {
					return FoundingClaimImport{}, err
				}
			}
			result := FoundingClaimImport{Rows: len(claims)}
			return result, auditStaff(ctx, q, actor.ID, "founding.claims_imported", "donor_founding_claims", result, now)
		},
	)
}

func parseFoundingClaims(problems *PublicationError, sheet string, now time.Time) map[string]foundingClaim {
	if !now.Before(FoundingClaimExpiry) {
		problems.add(foundingClaimSheet, 0, "", "claims_expired")
		return nil
	}
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
		hash := FoundingEmailHash(email)
		if held, ok := claims[hash]; !ok || signedUp.Before(held.signedUp) {
			claims[hash] = foundingClaim{signedUp: signedUp, source: source}
		}
	}
}

// FoundingEmailHash is the stored form of a normalized address. It matches
// the database's encode(sha256(lower(email)), 'hex').
func FoundingEmailHash(email string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(email)))
	return hex.EncodeToString(digest[:])
}
