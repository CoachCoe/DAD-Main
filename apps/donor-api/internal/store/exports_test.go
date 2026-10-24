package store

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"dad/donor-api/internal/dbroles"
)

type failedExportWriter struct{}

func (failedExportWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportsEnforcePermissionsEscapeFormulasAndAuditFailures(t *testing.T) {
	pool := database(t)
	api := as(t, dbroles.API)
	ctx := context.Background()
	actor, admin := staffFixture(t, pool, "ADMIN")
	_, support := staffFixture(t, pool, "SUPPORT")
	_, finance := staffFixture(t, pool, "FINANCE")
	id, _ := donor(t, pool)
	if _, err := pool.Exec(ctx, "UPDATE donors SET public_name=$2 WHERE id=$1", id, " \t=HYPERLINK(\"https://example.invalid\")"); err != nil {
		t.Fatal(err)
	}
	s := &StaffService{DB: api}
	if err := s.Export(ctx, support, "donors", io.Discard); !errors.Is(err, ErrStaffForbidden) {
		t.Fatal("support exported donors", err)
	}
	if err := s.Export(ctx, finance, "staff", io.Discard); !errors.Is(err, ErrStaffForbidden) {
		t.Fatal("finance exported staff", err)
	}
	for name, dataset := range exportDatasets {
		var body bytes.Buffer
		if err := s.Export(ctx, admin, name, &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rows, err := csv.NewReader(&body).ReadAll()
		if err != nil || len(rows) == 0 || !slices.Equal(rows[0], strings.Fields(dataset.fields)) {
			t.Fatalf("%s: invalid CSV %v", name, err)
		}
		if name == "referrals" && !slices.Contains(rows[0], "referral_kind") {
			t.Fatal("referral type omitted")
		}
		if name == "referral-touches" && !slices.Contains(rows[0], "kind") {
			t.Fatal("touch channel omitted")
		}
		if name == "gifts" && (!slices.Contains(rows[0], "referrer_id") || !slices.Contains(rows[0], "referral_kind")) {
			t.Fatal("payment attribution omitted")
		}
		if name == "audit" && !slices.Contains(rows[0], "details") {
			t.Fatal("audit details omitted")
		}
		if name == "donors" {
			found := false
			for _, row := range rows[1:] {
				if row[0] == id.String() {
					found = true
					if !strings.HasPrefix(row[len(row)-1], "' \t=") {
						t.Fatal("formula not escaped")
					}
				}
			}
			if !found {
				t.Fatal("donor missing from export")
			}
		}
		for _, field := range rows[0] {
			if slices.Contains([]string{"token_hash", "credential_id", "public_key", "session", "encrypted_data"}, field) {
				t.Fatal("credential field exported")
			}
		}
	}
	if err := s.Export(ctx, admin, "audit", failedExportWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("writer failure swallowed", err)
	}
	var incomplete int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_staff_audit WHERE actor_id=$1 AND action='data.export_finished' AND details->>'completed'='false'", actor).Scan(&incomplete); err != nil ||
		incomplete != 1 {
		t.Fatal("incomplete export not audited", err)
	}
}

func TestReferralExportCarriesTheReferralTypeOnEveryRow(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	actor, admin := staffFixture(t, pool, "ADMIN")
	referrer, _ := donor(t, pool)
	kinds := map[string]string{}
	for _, kind := range []string{"LINK", "IRL"} {
		referred, _ := donor(t, pool)
		kinds[referred.String()] = kind
		mustExec(
			t,
			pool,
			"INSERT INTO donor_metric_projections(id,donor_id,referrer_id,referral_kind,net_minor,computed_at,next_refresh_at) VALUES($1,$2,$3,$4,3000,now(),now())",
			testID(t),
			referred,
			referrer,
			kind,
		)
	}
	var body bytes.Buffer
	if err := (&StaffService{DB: as(t, dbroles.API)}).Export(ctx, admin, "referrals", &body); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	column := slices.Index(rows[0], "referral_kind")
	for _, row := range rows[1:] {
		if row[column] != "LINK" && row[column] != "IRL" {
			t.Fatalf("referral exported without its type: %v", row)
		}
		if want, ok := kinds[row[0]]; ok {
			if row[column] != want || row[1] != referrer.String() {
				t.Fatalf("referral exported as %v, want %s from %s", row, want, referrer)
			}
			delete(kinds, row[0])
		}
	}
	if len(kinds) != 0 {
		t.Fatal("referrals missing from export", kinds)
	}
	var completed bool
	var count int
	if err = pool.QueryRow(ctx, "SELECT (details->>'completed')::bool,(details->>'row_count')::int FROM donor_staff_audit WHERE actor_id=$1 AND action='data.export_finished' AND target='referrals'", actor).Scan(&completed, &count); err != nil ||
		!completed ||
		count != len(rows)-1 {
		t.Fatal("finished export not audited with its row count", completed, count, err)
	}
}

func TestSafeCSVCellNeutralizesTextButNotNumbers(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{"=1+1", "'=1+1"},
		{" @SUM(A1)", "' @SUM(A1)"},
		{"\tplain", "'\tplain"},
		{int64(-5), "-5"},
		{map[string]any{"reason": "=HYPERLINK()"}, `{"reason":"=HYPERLINK()"}`},
		{nil, ""},
	} {
		if got, err := safeCSVCell(c.in); err != nil || got != c.want {
			t.Fatalf("%#v: got %q %v", c.in, got, err)
		}
	}
}
