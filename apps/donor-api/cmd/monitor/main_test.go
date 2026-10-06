package main

import (
	"context"
	"io"
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
