package httpapi

import (
	"context"
	"io"
	"net/http"
	"time"
)

type ExportSource interface {
	ExportDatasets(context.Context, string) (map[string]interface{}, error)
	Export(context.Context, string, string, io.Writer) error
}

type exportWriter struct {
	response http.ResponseWriter
	name     string
	started  bool
}

func (w *exportWriter) Write(data []byte) (int, error) {
	if !w.started {
		w.response.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.response.Header().Set("Content-Disposition", `attachment; filename="dad-`+w.name+`.csv"`)
		w.response.Header().Set("X-Content-Type-Options", "nosniff")
		w.started = true
	}
	return w.response.Write(data)
}

func installExportRoutes(mux *http.ServeMux, services Services, reply replyFunc) {
	source := services.Exports
	ready := func(w http.ResponseWriter) bool {
		if source != nil {
			return true
		}
		reply(w, 503, map[string]string{"detail": "Exports are temporarily unavailable"})
		return false
	}
	mux.HandleFunc("GET /donor/staff/exports", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		body, err := source.ExportDatasets(ctx, staffToken(r))
		staffReply(w, reply, body, err)
	})
	mux.HandleFunc("GET /donor/staff/exports/{file}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		file := r.PathValue("file")
		if len(file) < 5 || file[len(file)-4:] != ".csv" {
			reply(w, 404, map[string]string{"detail": "Dataset not found"})
			return
		}
		name := file[:len(file)-4]
		for _, c := range name {
			if (c < 'a' || c > 'z') && c != '-' {
				reply(w, 404, map[string]string{"detail": "Dataset not found"})
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		writer := &exportWriter{response: w, name: name}
		if err := source.Export(ctx, staffToken(r), name, writer); err != nil {
			if writer.started {
				// Abort a partial download; a truncated CSV must not look successful.
				panic(http.ErrAbortHandler)
			}
			staffReply(w, reply, nil, err)
		}
	})
}
