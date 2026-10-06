package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

type PartnerRequest struct {
	Organization string `json:"organization"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	Website      string `json:"website"`
	Message      string `json:"message"`
	Consent      bool   `json:"contact_consent"`
}
type PartnerService struct{ DB *pgxpool.Pool }

// Signup records a partner inquiry. id makes it idempotent: a retry with the
// same fields succeeds, and different fields under the same id conflict.
func (s PartnerService) Signup(ctx context.Context, id pgtype.UUID, r PartnerRequest) error {
	if !ValidRequestID(id) || !r.Consent {
		return ErrStaffInput
	}
	for _, field := range []struct {
		value     string
		maximum   int
		multiline bool
	}{{r.Organization, 200, false}, {r.Name, 100, false}, {r.Email, 320, false}, {r.Website, 500, false}, {r.Message, 2000, true}} {
		if !plainText(field.value, field.multiline) || utf8.RuneCountInString(field.value) > field.maximum {
			return ErrStaffInput
		}
	}
	r.Organization, r.Name, r.Website, r.Message = identity.TrimSpace(r.Organization), identity.TrimSpace(r.Name), identity.TrimSpace(r.Website), identity.TrimSpace(r.Message)
	var err error
	r.Email, err = identity.NormalizeEmail(r.Email)
	if err != nil || r.Organization == "" || r.Name == "" {
		return ErrStaffInput
	}
	if r.Website != "" {
		u, err := url.Parse(r.Website)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return ErrStaffInput
		}
	}
	// Preserve the canonical preimage of existing idempotent partner submissions.
	parts := make([]string, 0, 5)
	for _, entry := range []struct{ key, value string }{{"email", r.Email}, {"message", r.Message}, {"name", r.Name}, {"organization", r.Organization}, {"website", r.Website}} {
		quoted, err := credentials.QuoteASCII(entry.value)
		if err != nil {
			return err
		}
		parts = append(parts, `"`+entry.key+`":`+quoted)
	}
	digest := sha256.Sum256([]byte("{" + strings.Join(parts, ",") + "}"))
	hash := hex.EncodeToString(digest[:])
	return pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := queries.New(tx)
		if err := q.InsertPartnerSignup(ctx, queries.InsertPartnerSignupParams{ID: id, Organization: r.Organization, Name: r.Name, Email: r.Email, Website: r.Website, Message: r.Message, RequestHash: hash}); err != nil {
			return err
		}
		row, err := q.PartnerSignupByID(ctx, id)
		if err != nil {
			return err
		}
		if row.RequestHash != hash {
			return ErrStaffConflict
		}
		return nil
	})
}
