package httpapi

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
)

type problem struct {
	Detail string `json:"detail"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		status = http.StatusInternalServerError
		w.Header().Set("Cache-Control", "no-store")
		data = []byte(`{"detail":"Response unavailable"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err = w.Write(append(data, '\n')); err != nil {
		// Close a truncated response without logging private response contents.
		panic(http.ErrAbortHandler)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	return decodeJSONLimit(w, r, target, maxJSONBody)
}

func decodeJSONLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	if value := r.Header.Get("Content-Type"); value != "" {
		media, _, err := mime.ParseMediaType(value)
		if err != nil || (media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json"))) {
			return false
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

// optional decodes a field that may be omitted but must not be null.
func optional[T any](raw json.RawMessage, fallback T) (T, bool) {
	if raw == nil {
		return fallback, true
	}
	var value T
	if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return value, false
	}
	return value, true
}
