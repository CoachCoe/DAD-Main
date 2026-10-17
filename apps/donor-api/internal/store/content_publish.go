package store

import (
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dad/donor-api/internal/identity"
	"dad/donor-api/internal/recognition"
	"dad/donor-api/internal/store/queries"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PublicationRequest struct {
	InitiativesCSV string `json:"initiatives_csv"`
	LedgerCSV      string `json:"ledger_csv"`
	AsOf           string `json:"as_of"`
	Cadence        string `json:"cadence"`
}

var initiativeSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var ledgerInteger = regexp.MustCompile(`^[0-9]{1,15}$`)
var ledgerUnits = []string{"USD_CENTS", "PEOPLE", "INITIATIVES", "PARTNERS"}

// PublicationProblem locates one reason a publication was refused. Sheet is
// "initiatives" or "ledger", or empty for a request field named by Column;
// Row counts the header as row 1. Reason is a stable code.
type PublicationProblem struct {
	Sheet  string `json:"sheet"`
	Row    int    `json:"row"`
	Column string `json:"column"`
	Reason string `json:"reason"`
}

// PublicationError lists every problem found in a publication, up to
// maxPublicationProblems. It is an ErrStaffInput.
type PublicationError struct {
	Problems []PublicationProblem
}

const maxPublicationProblems = 100

func (e *PublicationError) Error() string        { return "invalid publication" }
func (e *PublicationError) Is(target error) bool { return target == ErrStaffInput }

func (e *PublicationError) add(sheet string, row int, column, reason string) {
	if len(e.Problems) < maxPublicationProblems {
		e.Problems = append(e.Problems, PublicationProblem{Sheet: sheet, Row: row, Column: column, Reason: reason})
	}
}

// sheetRow is a spreadsheet row with its row number.
type sheetRow struct {
	number int
	cells  map[string]string
}

// spreadsheetRows parses a CSV export of at most 512 KB and 500 rows whose
// header is exactly columns. It returns no rows if the sheet is unusable.
func spreadsheetRows(problems *PublicationError, name, sheet string, columns []string) []sheetRow {
	if len(sheet) > 512*1024 {
		problems.add(name, 0, "", "too_large")
		return nil
	}
	if !plainText(sheet, true) {
		problems.add(name, 0, "", "invalid_text")
		return nil
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimLeft(sheet, "\ufeff")))
	header, err := reader.Read()
	if err != nil || !slices.Equal(header, columns) {
		problems.add(name, 1, "", "invalid_header")
		return nil
	}
	var rows []sheetRow
	for {
		values, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return rows
		}
		if err != nil {
			line := 0
			if parseErr, ok := errors.AsType[*csv.ParseError](err); ok {
				line = parseErr.StartLine
			}
			problems.add(name, line, "", "malformed_csv")
			return nil
		}
		line, _ := reader.FieldPos(0)
		if len(rows) >= 500 {
			problems.add(name, line, "", "too_many_rows")
			return nil
		}
		cells := make(map[string]string, len(columns))
		for i, column := range columns {
			cells[column] = values[i]
		}
		rows = append(rows, sheetRow{number: line, cells: cells})
	}
}

// sourceCitations maps each cited source document to where it is cited.
type sourceCitations map[pgtype.UUID][]PublicationProblem

// cite records a source cited at row's column, returning its canonical form.
func (c sourceCitations) cite(problems *PublicationError, sheet string, row sheetRow, column string) string {
	var id pgtype.UUID
	if id.Scan(row.cells[column]) != nil || !id.Valid {
		problems.add(sheet, row.number, column, "invalid_source")
		return row.cells[column]
	}
	c[id] = append(c[id], PublicationProblem{Sheet: sheet, Row: row.number, Column: column})
	return id.String()
}

type Publication struct {
	PublicationID pgtype.UUID `json:"publication_id"`
}

// Publish validates both spreadsheets in full and replaces the public
// snapshot. An invalid publication, reported as a *PublicationError, leaves
// the current one in place.
func (s ContentService) Publish(ctx context.Context, token string, r PublicationRequest) (Publication, error) {
	return staffWrite(ctx, s.Staff, token, "content.publish", func(tx pgx.Tx, actor queries.DonorStaff, now time.Time) (Publication, error) {
		problems := &PublicationError{}
		today := now.In(recognition.NewYork).Format(time.DateOnly)
		if asOf, err := time.Parse(time.DateOnly, r.AsOf); err != nil {
			problems.add("", 0, "as_of", "invalid_date")
		} else if asOf.Format(time.DateOnly) > today {
			problems.add("", 0, "as_of", "future_date")
		}
		cadence := identity.TrimSpace(r.Cadence)
		if cadence == "" || !plainText(cadence, false) || utf8.RuneCountInString(cadence) > 100 {
			problems.add("", 0, "cadence", "invalid_text")
		}
		sources := sourceCitations{}
		initiatives := parseInitiatives(problems, r.InitiativesCSV, today, sources)
		ledger := parseLedger(problems, r.LedgerCSV, sources)
		q := queries.New(tx)
		if err := checkSources(ctx, q, problems, sources); err != nil {
			return Publication{}, err
		}
		if len(problems.Problems) > 0 {
			slices.SortStableFunc(problems.Problems, func(a, b PublicationProblem) int {
				return cmp.Or(cmp.Compare(a.Sheet, b.Sheet), cmp.Compare(a.Row, b.Row))
			})
			return Publication{}, problems
		}
		payload, err := json.Marshal(map[string]any{"initiatives": initiatives, "ledger": ledger, "ledger_as_of": r.AsOf, "cadence": cadence})
		if err != nil {
			return Publication{}, err
		}
		id, err := NewID()
		if err != nil {
			return Publication{}, err
		}
		if err = q.InsertPublication(ctx, queries.InsertPublicationParams{ID: id, ActorID: actor.ID, Payload: payload, CreatedAt: timestamp(now)}); err != nil {
			return Publication{}, err
		}
		if err = q.PublishContentHead(ctx, id); err != nil {
			return Publication{}, err
		}
		if err = auditStaff(ctx, q, actor.ID, "content.published", id.String(), map[string]int{"initiatives": len(initiatives), "metrics": len(ledger)}, now); err != nil {
			return Publication{}, err
		}
		return Publication{PublicationID: id}, nil
	})
}

// parseInitiatives validates the initiatives spreadsheet, citing each board
// approval source in sources.
func parseInitiatives(problems *PublicationError, sheet, today string, sources sourceCitations) []map[string]string {
	const name = "initiatives"
	rows := spreadsheetRows(problems, name, sheet, []string{"slug", "title", "summary", "partner", "planned_start", "board_approval_source_id", "approved_on"})
	initiatives := make([]map[string]string, 0, len(rows))
	slugs := map[string]bool{}
	for _, row := range rows {
		slug := row.cells["slug"]
		switch {
		case len(slug) > 100 || !initiativeSlug.MatchString(slug):
			problems.add(name, row.number, "slug", "invalid_slug")
		case slugs[slug]:
			problems.add(name, row.number, "slug", "duplicate_slug")
		}
		slugs[slug] = true
		for _, field := range []struct {
			column  string
			maximum int
		}{{"title", 150}, {"summary", 4000}, {"partner", 200}} {
			if identity.TrimSpace(row.cells[field.column]) == "" {
				problems.add(name, row.number, field.column, "required")
			} else if utf8.RuneCountInString(row.cells[field.column]) > field.maximum {
				problems.add(name, row.number, field.column, "too_long")
			}
		}
		if approved, err := time.Parse(time.DateOnly, row.cells["approved_on"]); err != nil {
			problems.add(name, row.number, "approved_on", "invalid_date")
		} else if approved.Format(time.DateOnly) > today {
			problems.add(name, row.number, "approved_on", "future_date")
		}
		if _, err := time.Parse(time.DateOnly, row.cells["planned_start"]); err != nil {
			problems.add(name, row.number, "planned_start", "invalid_date")
		}
		row.cells["board_approval_source_id"] = sources.cite(problems, name, row, "board_approval_source_id")
		initiatives = append(initiatives, row.cells)
	}
	return initiatives
}

// parseLedger validates the ledger spreadsheet. A real figure must cite a
// source; an illustrative one may.
func parseLedger(problems *PublicationError, sheet string, sources sourceCitations) []map[string]any {
	const name = "ledger"
	rows := spreadsheetRows(problems, name, sheet, []string{"metric", "value", "unit", "source_id", "illustrative"})
	ledger := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		cells := row.cells
		if metric := identity.TrimSpace(cells["metric"]); metric == "" {
			problems.add(name, row.number, "metric", "required")
		} else if utf8.RuneCountInString(metric) > 150 {
			problems.add(name, row.number, "metric", "too_long")
		}
		value, err := strconv.ParseInt(cells["value"], 10, 64)
		if !ledgerInteger.MatchString(cells["value"]) || err != nil {
			problems.add(name, row.number, "value", "invalid_value")
		}
		if !slices.Contains(ledgerUnits, cells["unit"]) {
			problems.add(name, row.number, "unit", "invalid_unit")
		}
		if cells["illustrative"] != "true" && cells["illustrative"] != "false" {
			problems.add(name, row.number, "illustrative", "invalid_flag")
		}
		illustrative := cells["illustrative"] == "true"
		var source any
		if cells["source_id"] != "" || !illustrative {
			source = sources.cite(problems, name, row, "source_id")
		}
		ledger = append(ledger, map[string]any{"metric": cells["metric"], "value": value, "unit": cells["unit"], "source_id": source, "illustrative": illustrative})
	}
	return ledger
}

// checkSources reports every citation of a source document that was never
// uploaded.
func checkSources(ctx context.Context, q *queries.Queries, problems *PublicationError, sources sourceCitations) error {
	if len(sources) == 0 {
		return nil
	}
	found, err := q.ContentSourceIDs(ctx, slices.Collect(maps.Keys(sources)))
	if err != nil {
		return err
	}
	for _, id := range found {
		delete(sources, id)
	}
	for _, citations := range sources {
		for _, c := range citations {
			problems.add(c.Sheet, c.Row, c.Column, "unknown_source")
		}
	}
	return nil
}
