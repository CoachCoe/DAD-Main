package store

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"impactgraph/donor-api/internal/identity"
	"impactgraph/donor-api/internal/store/queries"
)

type PublicationRequest struct {
	InitiativesCSV string `json:"initiatives_csv"`
	LedgerCSV      string `json:"ledger_csv"`
	AsOf           string `json:"as_of"`
	Cadence        string `json:"cadence"`
}

var initiativeSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var ledgerInteger = regexp.MustCompile(`^[0-9]{1,15}$`)

func spreadsheetRows(text string, columns []string) ([]map[string]string, error) {
	if len(text) > 512*1024 || !utf8.ValidString(text) {
		return nil, ErrStaffInput
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimLeft(text, "\ufeff")))
	header, err := reader.Read()
	if err != nil || !slices.Equal(header, columns) {
		return nil, ErrStaffInput
	}
	rows := []map[string]string{}
	for {
		values, err := reader.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil || len(rows) >= 500 {
			return nil, ErrStaffInput
		}
		row := make(map[string]string, len(columns))
		for i, column := range columns {
			row[column] = values[i]
		}
		rows = append(rows, row)
	}
}

func contentSourceID(ctx context.Context, q *queries.Queries, value string) (string, error) {
	var id pgtype.UUID
	if id.Scan(value) != nil || !id.Valid {
		return "", ErrStaffInput
	}
	if _, err := q.ContentSourceByID(ctx, id); err != nil {
		return "", err
	}
	return id.String(), nil
}

func (s ContentService) Publish(ctx context.Context, token string, r PublicationRequest) (map[string]interface{}, error) {
	return staffTransaction(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (map[string]interface{}, error) {
		zone, err := time.LoadLocation("America/New_York")
		if err != nil {
			return nil, err
		}
		today := now.In(zone).Format(time.DateOnly)
		asOf, err := time.Parse(time.DateOnly, r.AsOf)
		cadence := identity.TrimSpace(r.Cadence)
		if err != nil || asOf.Format(time.DateOnly) > today || len(cadence) == 0 || utf8.RuneCountInString(cadence) > 100 {
			return nil, ErrStaffInput
		}
		initiatives, err := spreadsheetRows(r.InitiativesCSV, []string{"slug", "title", "summary", "partner", "planned_start", "board_approval_source_id", "approved_on"})
		if err != nil {
			return nil, err
		}
		q := queries.New(tx)
		slugs := map[string]bool{}
		for _, row := range initiatives {
			slug := row["slug"]
			if len(slug) > 100 || !initiativeSlug.MatchString(slug) || slugs[slug] {
				return nil, ErrStaffInput
			}
			slugs[slug] = true
			for field, maximum := range map[string]int{"title": 150, "summary": 4000, "partner": 200} {
				if identity.TrimSpace(row[field]) == "" || utf8.RuneCountInString(row[field]) > maximum {
					return nil, ErrStaffInput
				}
			}
			approved, err := time.Parse(time.DateOnly, row["approved_on"])
			if err != nil || approved.Format(time.DateOnly) > today {
				return nil, ErrStaffInput
			}
			if _, err = time.Parse(time.DateOnly, row["planned_start"]); err != nil {
				return nil, ErrStaffInput
			}
			row["board_approval_source_id"], err = contentSourceID(ctx, q, row["board_approval_source_id"])
			if err != nil {
				return nil, err
			}
		}
		rows, err := spreadsheetRows(r.LedgerCSV, []string{"metric", "value", "unit", "source_id", "illustrative"})
		if err != nil {
			return nil, err
		}
		ledger := make([]map[string]interface{}, 0, len(rows))
		for _, row := range rows {
			if identity.TrimSpace(row["metric"]) == "" || utf8.RuneCountInString(identity.TrimSpace(row["metric"])) > 150 || !ledgerInteger.MatchString(row["value"]) || !slices.Contains([]string{"USD_CENTS", "PEOPLE", "INITIATIVES", "PARTNERS"}, row["unit"]) || (row["illustrative"] != "true" && row["illustrative"] != "false") {
				return nil, ErrStaffInput
			}
			value, err := strconv.ParseInt(row["value"], 10, 64)
			if err != nil {
				return nil, ErrStaffInput
			}
			illustrative := row["illustrative"] == "true"
			var source interface{}
			if row["source_id"] != "" || !illustrative {
				source, err = contentSourceID(ctx, q, row["source_id"])
				if err != nil {
					return nil, err
				}
			}
			ledger = append(ledger, map[string]interface{}{"metric": row["metric"], "value": value, "unit": row["unit"], "source_id": source, "illustrative": illustrative})
		}
		payload, err := json.Marshal(map[string]interface{}{"initiatives": initiatives, "ledger": ledger, "ledger_as_of": r.AsOf, "cadence": cadence})
		if err != nil {
			return nil, err
		}
		id, err := NewID()
		if err != nil {
			return nil, err
		}
		if err = q.InsertPublication(ctx, queries.InsertPublicationParams{ID: id, ActorID: actor.ID, Payload: payload, CreatedAt: timestamp(now)}); err != nil {
			return nil, err
		}
		if err = q.PublishContentHead(ctx, id); err != nil {
			return nil, err
		}
		if err = auditStaff(ctx, q, actor.ID, "content.published", id.String(), map[string]int{"initiatives": len(initiatives), "metrics": len(ledger)}, now); err != nil {
			return nil, err
		}
		return map[string]interface{}{"publication_id": id}, nil
	})
}
