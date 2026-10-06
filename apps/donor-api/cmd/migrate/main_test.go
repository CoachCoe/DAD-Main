package main

import "testing"

func TestRunRejectsInvalidInvocationBeforeTheDatabase(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		dsn  string
	}{
		{"unknown flag", []string{"--force"}, "postgresql://unreachable.invalid/donor"},
		{"stray argument", []string{"up"}, "postgresql://unreachable.invalid/donor"},
		{"no database", nil, ""},
	} {
		err := run(tc.args, func(string) string { return tc.dsn })
		if err == nil || err.Error() == "migration database unavailable" {
			t.Errorf("%s: got %v, want an invocation error", tc.name, err)
		}
	}
}
