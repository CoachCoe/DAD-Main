// Package buildinfo reports which build is running, so a deployed copy can
// name its exact commit on the health endpoints. The reconciliation and QA
// workflows pin work to a copy's reported commit and stop if it changes
// mid-run.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// commit may be set at link time for container builds where VCS stamping is
// unavailable: -ldflags "-X dad/donor-api/internal/buildinfo.commit=<sha>".
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
