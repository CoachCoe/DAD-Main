package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"
	"testing"
)

func TestRunRejectsInvalidInvocationBeforeTheDatabase(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		dsn  string
	}{
		{"zero age", []string{"--max-age=0"}, "postgresql://unreachable.invalid/donor"},
		{"negative age", []string{"--max-age=-1m"}, "postgresql://unreachable.invalid/donor"},
		{"stray argument", []string{"queues"}, "postgresql://unreachable.invalid/donor"},
		{"no database", nil, ""},
	} {
		err := run(context.Background(), tc.args, func(string) string { return tc.dsn }, io.Discard)
		if err == nil || err.Error() == "monitor database unavailable" {
			t.Errorf("%s: got %v, want an invocation error", tc.name, err)
		}
	}
}

func TestRunReportsEveryQueue(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("TEST_DATABASE_URL is required; use the migrated donor_go test database")
	}
	var out bytes.Buffer
	// Other tests' queue rows may need attention; only the report is checked.
	_ = run(context.Background(), nil, func(string) string { return dsn }, &out)
	var rows []struct{ Queue string }
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("report %q: %v", out.String(), err)
	}
	var queues []string
	for _, row := range rows {
		queues = append(queues, row.Queue)
	}
	if want := []string{"email-changes", "login-email", "messages", "projections", "push", "stripe", "subscriptions"}; !slices.Equal(queues, want) {
		t.Fatalf("queues %v, want %v", queues, want)
	}
}
