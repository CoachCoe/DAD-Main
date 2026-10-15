// Package buildinfo reports which build is running, so a deployed copy can
// name its exact commit on the health endpoints. The reconciliation and QA
// workflows pin work to a copy's reported commit and stop if it changes
// mid-run (see docs/testing/two-bot-qa-loop-plan.md), and knowing the exact
// deployed commit is ordinary operational hygiene regardless.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// commit may be set at link time for container builds where VCS stamping is
// unavailable: -ldflags "-X impactgraph/donor-api/internal/buildinfo.commit=<sha>".
// When empty, Revision falls back to the VCS revision the Go toolchain embeds
// for a build from a source checkout.
var commit string

// Revision returns the running build's commit, or "unknown" if neither a
// link-time value nor an embedded VCS revision is available (e.g. a build with
// VCS stamping disabled and no ldflags override).
func Revision() string {
	if c := strings.TrimSpace(commit); c != "" {
		return c
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return setting.Value
		}
	}
	return "unknown"
}

// Modified reports whether the build was made from a dirty working tree, when
// the toolchain recorded it. A deployed copy should never report true.
func Modified() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" {
			return setting.Value == "true"
		}
	}
	return false
}
