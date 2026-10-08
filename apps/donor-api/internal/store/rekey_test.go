package store

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestEveryEncryptedColumnIsRotatedWithItsKey(t *testing.T) {
	pool := database(t)
	rows, err := pool.Query(context.Background(), `SELECT table_name, column_name FROM information_schema.columns
WHERE table_schema='public' AND column_name LIKE 'encrypted\_%' ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := pgx.CollectRows(rows, pgx.RowToStructByPos[EncryptedColumn])
	if err != nil {
		t.Fatal(err)
	}
	rotated := slices.Concat(EmailKeyColumns, ContentKeyColumns)
	for _, column := range columns {
		if !slices.Contains(rotated, column) {
			t.Errorf("%s.%s is not in any key's column list, so rekey would never re-encrypt it", column.Table, column.Column)
		}
	}
	if len(columns) != len(rotated) {
		t.Errorf("found %d encrypted columns, want the %d listed", len(columns), len(rotated))
	}
}
