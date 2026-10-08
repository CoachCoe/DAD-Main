package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"impactgraph/donor-api/internal/dbroles"
	"impactgraph/donor-api/internal/dbroles/dbrolestest"
)

func TestRunRejectsInvalidInvocationBeforeTheDatabase(t *testing.T) {
	env := map[string]string{"DONOR_STAFF_ORIGIN": "https://staff.dad.example.org", "DONOR_STAFF_CLI_DATABASE_URL": "postgresql://unreachable.invalid/donor"}
	for _, tc := range []struct {
		name  string
		args  []string
		unset string
	}{
		{"missing name", []string{"--email=admin@example.org"}, ""},
		{"missing email", []string{"--name=Admin"}, ""},
		{"stray argument", []string{"--email=admin@example.org", "--name=Admin", "extra"}, ""},
		{"recovery with a name", []string{"--recover", "--email=admin@example.org", "--name=Admin"}, ""},
		{"recovery without an email", []string{"--recover"}, ""},
		{"no staff origin", []string{"--email=admin@example.org", "--name=Admin"}, "DONOR_STAFF_ORIGIN"},
		{"no database", []string{"--email=admin@example.org", "--name=Admin"}, "DONOR_STAFF_CLI_DATABASE_URL"},
	} {
		getenv := func(name string) string {
			if name == tc.unset {
				return ""
			}
			return env[name]
		}
		if err := run(tc.args, getenv, io.Discard); err == nil || err.Error() == "database is unavailable" {
			t.Errorf("%s: got %v, want an invocation error", tc.name, err)
		}
	}
}

func TestRunRefusesTheSchemaOwner(t *testing.T) {
	dbrolestest.URL(t, dbroles.StaffCLI)
	env := map[string]string{"DONOR_STAFF_ORIGIN": "https://staff.dad.example.org", dbroles.StaffCLI.Variable(): os.Getenv("TEST_DATABASE_URL")}
	err := run([]string{"--recover", "--email=nobody@example.org"}, func(name string) string { return env[name] }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "can change the schema") {
		t.Fatalf("got %v, want the owner refused", err)
	}
}
