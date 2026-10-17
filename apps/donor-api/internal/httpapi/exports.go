package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"

	"dad/donor-api/internal/store"
)

type ExportSource interface {
	ExportDatasets(context.Context, string) (store.ExportDatasets, error)
	Export(context.Context, string, string, io.Writer) error
}

// exportWriter sends CSV headers only once data is ready, so a failure before
// the first row can still be reported as a JSON error.
type exportWriter struct {
	response http.ResponseWriter
	name     string
	started  bool
}

func (w *exportWriter) Write(data []byte) (int, error) {
	if !w.started {
		w.response.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.response.Header().Set("Content-Disposition", `attachment; filename="dad-`+w.name+`.csv"`)
		w.started = true
	}
	return w.response.Write(data)
}

func (a *api) exports() service {
	return service{a.Exports != nil, "Exports are temporarily unavailable", readTimeout, staffStatus}
}

func (a *api) exportDatasets(w http.ResponseWriter, r *http.Request, session string) {
	a.exports().serve(w, r, func(ctx context.Context) (any, error) { return a.Exports.ExportDatasets(ctx, session) })
}

func validDatasetName(name string) bool {
	return name != "" && strings.Trim(name, "abcdefghijklmnopqrstuvwxyz-") == ""
}

// export streams up to the provider budget: large datasets take longer than a read.
func (a *api) export(w http.ResponseWriter, r *http.Request, session string) {
	name, ok := strings.CutSuffix(r.PathValue("file"), ".csv")
	if !ok || !validDatasetName(name) {
		writeError(w, r, http.StatusNotFound, "Dataset not found", nil)
		return
	}
	if !a.exports().available(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, providerTimeout)
	defer cancel()
	writer := &exportWriter{response: w, name: name}
	if err := a.Exports.Export(ctx, session, name, writer); err != nil {
		if writer.started {
			// Abort a partial download; a truncated CSV must not look successful.
			panic(http.ErrAbortHandler)
		}
		respond(w, r, nil, err, staffStatus)
	}
}
