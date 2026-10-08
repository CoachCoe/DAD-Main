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

	"impactgraph/donor-api/internal/dbroles"
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
		if name == "enrolments" && len(rows) != 1 {
			t.Fatal("inactive enrolment export has records")
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
	if err := s.Export(ctx, admin, "enrolments", failedExportWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("writer failure swallowed", err)
	}
	var incomplete int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM donor_staff_audit WHERE actor_id=$1 AND action='data.export_finished' AND details->>'completed'='false'", actor).Scan(&incomplete); err != nil || incomplete != 1 {
		t.Fatal("incomplete export not audited", err)
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
