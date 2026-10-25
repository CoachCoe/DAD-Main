package httpapi

import (
	"context"
	"net/http"
	"time"

	"dad/donor-api/internal/store"
)

// ReportSource is the staff §4 success-metrics read (report.read). It serves a
// single snapshot over a half-open [start, end) window of committed server
// facts; see store.ReportingService.
type ReportSource interface {
	Report(ctx context.Context, token string, start, end time.Time) (store.Report, error)
}

func (a *api) reporting() service {
	return service{a.Reporting != nil, staffUnavailable, readTimeout, staffStatus}
}

// report serves GET /donor/staff/report?start=...&end=... . start and end are
// RFC3339 timestamps bounding a half-open [start, end) window; end doubles as
// the observation cutoff. Both are required so a report always carries its
// window rather than defaulting to an implicit one.
func (a *api) report(w http.ResponseWriter, r *http.Request, session string) {
	start, okStart := timeParam(r, "start")
	end, okEnd := timeParam(r, "end")
	if !okStart || !okEnd {
		invalidFields(w, r)
		return
	}
	a.reporting().serve(w, r, func(ctx context.Context) (any, error) {
		return a.Reporting.Report(ctx, session, start, end)
	})
}

// timeParam reads a required RFC3339 timestamp query parameter.
func timeParam(r *http.Request, name string) (time.Time, bool) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
