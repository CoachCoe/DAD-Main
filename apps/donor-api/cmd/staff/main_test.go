package main

import (
	"io"
	"testing"
)

func TestRunRejectsInvalidInvocationBeforeTheDatabase(t *testing.T) {
	env := map[string]string{"DONOR_PUBLIC_ORIGIN": "https://dad.example.org", "DONOR_GO_DATABASE_URL": "postgresql://unreachable.invalid/donor"}
	for _, tc := range []struct {
		name  string
		args  []string
		unset string
	}{
		{"missing name", []string{"--email=admin@example.org"}, ""},
		{"missing email", []string{"--name=Admin"}, ""},
		{"stray argument", []string{"--email=admin@example.org", "--name=Admin", "extra"}, ""},
		{"no origin", []string{"--email=admin@example.org", "--name=Admin"}, "DONOR_PUBLIC_ORIGIN"},
		{"no database", []string{"--email=admin@example.org", "--name=Admin"}, "DONOR_GO_DATABASE_URL"},
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
