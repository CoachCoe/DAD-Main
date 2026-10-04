package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/credentials"
	"impactgraph/donor-api/internal/store/queries"
)

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

func (s ContentService) Inventory(ctx context.Context, token string) (map[string]interface{}, error) {
	return staffTransaction(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, _ queries.DonorStaff, _ time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		sources, err := q.ContentSources(ctx)
		if err != nil {
			return nil, err
		}
		rows, err := q.ContentPublications(ctx)
		if err != nil {
			return nil, err
		}
		publications := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			publications = append(publications, map[string]interface{}{"id": row.ID, "at": row.CreatedAt})
		}
		return map[string]interface{}{"sources": sources, "publications": publications}, nil
	})
}

func (s ContentService) StoreSource(ctx context.Context, token, name string, data []byte) (map[string]interface{}, error) {
	if len(data) == 0 || len(data) > 10*1024*1024 || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 120 {
		return nil, ErrStaffInput
	}
	for _, r := range name {
		if r < 32 {
			return nil, ErrStaffInput
		}
	}
	return staffTransaction(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		ciphertext, err := credentials.Encrypt(data, s.EncryptionKey)
		if err != nil {
			return nil, err
		}
		id, err := NewID()
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		q := queries.New(tx)
		if err = q.InsertContentSource(ctx, queries.InsertContentSourceParams{ID: id, ActorID: actor.ID, Name: name, Sha256: hash, EncryptedData: ciphertext}); err != nil {
			return nil, err
		}
		if err = auditStaff(ctx, q, actor.ID, "content.source_uploaded", id.String(), map[string]string{"sha256": hash}, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": id, "name": name, "sha256": hash}, nil
	})
}

func (s ContentService) ReadSource(ctx context.Context, token string, id pgtype.UUID) (ContentDocument, error) {
	return staffTransaction(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (ContentDocument, error) {
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

func (s ContentService) Partners(ctx context.Context, token string, offset int64) (map[string]interface{}, error) {
	if !pageOffsetValid(offset) {
		return nil, ErrStaffInput
	}
	return staffTransaction(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		q := queries.New(tx)
		rows, err := q.PartnerSignupsPage(ctx, offset)
		if err != nil {
			return nil, err
		}
		rows, next := page(rows, offset)
		signups := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			signups = append(signups, map[string]interface{}{"id": row.ID, "organization": row.Organization, "name": row.Name, "email": row.Email, "website": row.Website, "message": row.Message, "at": row.CreatedAt})
		}
		if err = auditStaff(ctx, q, actor.ID, "partner.inquiries_viewed", "partner-signups", map[string]int{"count": len(rows)}, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"signups": signups, "next_offset": next}, nil
	})
}
