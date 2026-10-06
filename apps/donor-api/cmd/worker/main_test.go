package main

import (
	"context"
	"errors"
	"testing"
)

func TestRunRejectsInvalidInvocationBeforeTheDatabase(t *testing.T) {
	env := map[string]string{"DONOR_GO_WORKERS_ENABLED": "true", "DONOR_GO_DATABASE_URL": "postgresql://unreachable.invalid/donor"}
	for _, tc := range []struct {
		name  string
		args  []string
		unset string
	}{
		{"no kind", nil, ""},
		{"unknown kind", []string{"--kind=refunds"}, ""},
		{"stray argument", []string{"--kind=stripe", "extra"}, ""},
		{"retry for another kind", []string{"--kind=messages", "--retry-event=evt_123"}, ""},
		{"retry without event id", []string{"--kind=stripe", "--retry-event=123"}, ""},
		{"workers disabled", []string{"--kind=stripe"}, "DONOR_GO_WORKERS_ENABLED"},
		{"no database", []string{"--kind=stripe"}, "DONOR_GO_DATABASE_URL"},
	} {
		getenv := func(name string) string {
			if name == tc.unset {
				return ""
			}
			return env[name]
		}
		err := run(context.Background(), tc.args, getenv)
		if _, ok := errors.AsType[configError](err); !ok || err.Error() == "worker database is unreachable" {
			t.Errorf("%s: got %v, want a configuration error", tc.name, err)
		}
	}
}
