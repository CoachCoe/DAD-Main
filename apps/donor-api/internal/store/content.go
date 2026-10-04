package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"impactgraph/donor-api/internal/store/queries"
)

type ContentReader struct{ DB queries.DBTX }

func (reader ContentReader) PublicContent(ctx context.Context) (map[string]interface{}, error) {
	row, err := queries.New(reader.DB).PublishedContent(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]interface{}{"state": "unpublished", "initiatives": []interface{}{}, "ledger": []interface{}{}, "ledger_as_of": nil, "cadence": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot struct {
		Initiatives []map[string]json.RawMessage `json:"initiatives"`
		Ledger      []map[string]json.RawMessage `json:"ledger"`
		AsOf        string                       `json:"ledger_as_of"`
		Cadence     string                       `json:"cadence"`
	}
	if err = json.Unmarshal(row.Payload, &snapshot); err != nil {
		return nil, err
	}
	// The public transport never serializes a whole database publication or source row.
	initiatives := make([]map[string]json.RawMessage, 0, len(snapshot.Initiatives))
	ledger := make([]map[string]json.RawMessage, 0, len(snapshot.Ledger))
	for _, item := range snapshot.Initiatives {
		public := map[string]json.RawMessage{}
		for _, key := range []string{"slug", "title", "summary", "partner", "planned_start"} {
			if value, ok := item[key]; ok {
				public[key] = value
			}
		}
		initiatives = append(initiatives, public)
	}
	for _, item := range snapshot.Ledger {
		public := map[string]json.RawMessage{}
		for _, key := range []string{"metric", "value", "unit", "illustrative"} {
			if value, ok := item[key]; ok {
				public[key] = value
			}
		}
		ledger = append(ledger, public)
	}
	return map[string]interface{}{"state": "published", "published_at": row.CreatedAt.Time, "publication_id": row.ID,
		"ledger_as_of": snapshot.AsOf, "cadence": snapshot.Cadence, "initiatives": initiatives, "ledger": ledger}, nil
}
