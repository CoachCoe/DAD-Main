package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/store/queries"
)

var ErrSourceStorageUnconfigured = errors.New("source document storage is not configured")

type ContentService struct {
	Staff         *StaffService
	EncryptionKey string
}
type ContentDocument struct {
	Name string
	Data []byte
}

func (s ContentService) Authorize(ctx context.Context, token string) error {
	_, err := requireStaff(ctx, queries.New(s.Staff.DB), token, "content.publish", time.Now().UTC())
	return err
}

// AuthorizeSourceUpload also refuses when sources cannot be encrypted, so the
// caller can reject an upload before reading its body.
func (s ContentService) AuthorizeSourceUpload(ctx context.Context, token string) error {
	if err := s.Authorize(ctx, token); err != nil {
		return err
	}
	if s.EncryptionKey == "" {
		return ErrSourceStorageUnconfigured
	}
	return nil
}

// Content views keep alphabetical field order so responses match the original
// map encoding byte for byte.
type PublicationSummary struct {
	At pgtype.Timestamptz `json:"at"`
	ID pgtype.UUID        `json:"id"`
}
type ContentInventory struct {
	Publications []PublicationSummary        `json:"publications"`
	Sources      []queries.ContentSourcesRow `json:"sources"`
}
type StoredSource struct {
	ID     pgtype.UUID `json:"id"`
	Name   string      `json:"name"`
	Sha256 string      `json:"sha256"`
}
type PartnerSignup struct {
	At           pgtype.Timestamptz `json:"at"`
	Email        string             `json:"email"`
	ID           pgtype.UUID        `json:"id"`
	Message      string             `json:"message"`
	Name         string             `json:"name"`
	Organization string             `json:"organization"`
	Website      string             `json:"website"`
}
type PartnerPage struct {
	NextOffset *int64          `json:"next_offset"`
	Signups    []PartnerSignup `json:"signups"`
}

func (s ContentService) Inventory(ctx context.Context, token string) (ContentInventory, error) {
	return staffRead(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, _ queries.DonorStaff, _ time.Time) (ContentInventory, error) {
		q := queries.New(tx)
		sources, err := q.ContentSources(ctx)
		if err != nil {
			return ContentInventory{}, err
		}
		rows, err := q.ContentPublications(ctx)
		if err != nil {
			return ContentInventory{}, err
		}
		out := ContentInventory{Sources: sources, Publications: make([]PublicationSummary, 0, len(rows))}
		for _, row := range rows {
			out.Publications = append(out.Publications, PublicationSummary{ID: row.ID, At: row.CreatedAt})
		}
		return out, nil
	})
}

func (s ContentService) StoreSource(ctx context.Context, token, name string, data []byte) (StoredSource, error) {
	if len(data) == 0 || len(data) > 10*1024*1024 || !plainText(name, false) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 120 {
		return StoredSource{}, ErrStaffInput
	}
	return staffWrite(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (StoredSource, error) {
		if s.EncryptionKey == "" {
			return StoredSource{}, ErrSourceStorageUnconfigured
		}
		ciphertext, err := credentials.Encrypt(data, s.EncryptionKey)
		if err != nil {
			return StoredSource{}, err
		}
		id, err := NewID()
		if err != nil {
			return StoredSource{}, err
		}
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		q := queries.New(tx)
		if err = q.InsertContentSource(ctx, queries.InsertContentSourceParams{ID: id, ActorID: actor.ID, Name: name, Sha256: hash, EncryptedData: ciphertext}); err != nil {
			return StoredSource{}, err
		}
		if err = auditStaff(ctx, q, actor.ID, "content.source_uploaded", id.String(), map[string]string{"sha256": hash}, now); err != nil {
			return StoredSource{}, err
		}
		return StoredSource{ID: id, Name: name, Sha256: hash}, nil
	})
}

func (s ContentService) ReadSource(ctx context.Context, token string, id pgtype.UUID) (ContentDocument, error) {
	return staffRead(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (ContentDocument, error) {
		q := queries.New(tx)
		row, err := q.ContentSourceByID(ctx, id)
		if err != nil {
			return ContentDocument{}, err
		}
		data, err := credentials.Decrypt(row.EncryptedData, s.EncryptionKey)
		if err != nil {
			return ContentDocument{}, err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != row.Sha256 {
			return ContentDocument{}, ErrStaffConflict
		}
		if err = auditStaff(ctx, q, actor.ID, "content.source_read", id.String(), nil, now); err != nil {
			return ContentDocument{}, err
		}
		return ContentDocument{Name: row.Name, Data: data}, nil
	})
}

func (s ContentService) Partners(ctx context.Context, token string, offset int64) (PartnerPage, error) {
	if !pageOffsetValid(offset) {
		return PartnerPage{}, ErrStaffInput
	}
	return staffRead(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (PartnerPage, error) {
		q := queries.New(tx)
		rows, err := q.PartnerSignupsPage(ctx, offset)
		if err != nil {
			return PartnerPage{}, err
		}
		rows, next := page(rows, offset)
		out := PartnerPage{NextOffset: next, Signups: make([]PartnerSignup, 0, len(rows))}
		for _, row := range rows {
			out.Signups = append(out.Signups, PartnerSignup{ID: row.ID, Organization: row.Organization, Name: row.Name, Email: row.Email, Website: row.Website, Message: row.Message, At: row.CreatedAt})
		}
		if err = auditStaff(ctx, q, actor.ID, "partner.inquiries_viewed", "partner-signups", map[string]int{"count": len(rows)}, now); err != nil {
			return PartnerPage{}, err
		}
		return out, nil
	})
}
