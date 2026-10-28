package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ContentReader struct{ DB queries.DBTX }

// PublicContent is the published site content. Ledger entries stay maps
// because they carry only an allowlisted subset of stored fields.
type PublicContent struct {
	Cadence       *string                      `json:"cadence"`
	Ledger        []map[string]json.RawMessage `json:"ledger"`
	LedgerAsOf    *string                      `json:"ledger_as_of"`
	PublicationID *pgtype.UUID                 `json:"publication_id,omitempty"`
	PublishedAt   *time.Time                   `json:"published_at,omitempty"`
	State         string                       `json:"state"`
}

func (reader ContentReader) PublicContent(ctx context.Context) (PublicContent, error) {
	row, err := queries.New(reader.DB).PublishedContent(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicContent{State: "unpublished", Ledger: []map[string]json.RawMessage{}}, nil
	}
	if err != nil {
		return PublicContent{}, err
	}
	var snapshot struct {
		Ledger  []map[string]json.RawMessage `json:"ledger"`
		AsOf    string                       `json:"ledger_as_of"`
		Cadence string                       `json:"cadence"`
	}
	if err = json.Unmarshal(row.Payload, &snapshot); err != nil {
		return PublicContent{}, err
	}
	// The public transport never serializes a whole database publication or source row.
	ledger := make([]map[string]json.RawMessage, 0, len(snapshot.Ledger))
	for position, item := range snapshot.Ledger {
		public := map[string]json.RawMessage{}
		public["id"] = json.RawMessage(strconv.Quote(row.ID.String() + ":" + strconv.Itoa(position)))
		for _, key := range []string{"metric", "value", "unit", "illustrative"} {
			if value, ok := item[key]; ok {
				public[key] = value
			}
		}
		ledger = append(ledger, public)
	}
	return PublicContent{State: "published", PublishedAt: &row.CreatedAt.Time, PublicationID: &row.ID,
		LedgerAsOf: &snapshot.AsOf, Cadence: &snapshot.Cadence, Ledger: ledger}, nil
}
