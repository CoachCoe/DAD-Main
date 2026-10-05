package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
)

type ExportSource interface {
	ExportDatasets(context.Context, string) (map[string]any, error)
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

func (a *api) exportsAvailable(w http.ResponseWriter, r *http.Request) bool {
	if a.Exports == nil {
		writeError(w, r, http.StatusServiceUnavailable, "Exports are temporarily unavailable", nil)
		return false
	}
	return true
}

func (a *api) exportDatasets(w http.ResponseWriter, r *http.Request) {
	if !a.exportsAvailable(w, r) {
		return
	}
	ctx, cancel := withTimeout(r, readTimeout)
	defer cancel()
	body, err := a.Exports.ExportDatasets(ctx, cookieValue(r, staffCookie))
	respond(w, r, body, err, staffStatus)
}

func validDatasetName(name string) bool {
	return name != "" && strings.Trim(name, "abcdefghijklmnopqrstuvwxyz-") == ""
}

func (a *api) export(w http.ResponseWriter, r *http.Request) {
	if !a.exportsAvailable(w, r) {
		return
	}
	name, ok := strings.CutSuffix(r.PathValue("file"), ".csv")
	if !ok || !validDatasetName(name) {
		writeError(w, r, http.StatusNotFound, "Dataset not found", nil)
		return
	}
	ctx, cancel := withTimeout(r, providerTimeout)
	defer cancel()
	writer := &exportWriter{response: w, name: name}
	if err := a.Exports.Export(ctx, cookieValue(r, staffCookie), name, writer); err != nil {
		if writer.started {
			// Abort a partial download; a truncated CSV must not look successful.
			panic(http.ErrAbortHandler)
		}
		respond(w, r, nil, err, staffStatus)
	}
}
