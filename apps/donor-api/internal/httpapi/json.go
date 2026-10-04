package httpapi

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	data, err := json.Marshal(body)
	if err != nil {
		status = http.StatusInternalServerError
		w.Header().Set("Cache-Control", "no-store")
		data = []byte(`{"detail":"Response unavailable"}`)
	}
	w.WriteHeader(status)
	if _, err = w.Write(append(data, '\n')); err != nil {
		// Close a truncated response without logging private response contents.
		panic(http.ErrAbortHandler)
	}
}
