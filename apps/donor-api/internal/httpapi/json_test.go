package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONEncodingFailureCannotAdvertiseCachedSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, map[string]interface{}{"private": make(chan int)})
	if w.Code != 500 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "private") {
		t.Fatal("invalid response was successful, cached, or exposed private fields")
	}
}

type disconnectedWriter struct{ httptest.ResponseRecorder }

func (*disconnectedWriter) Write([]byte) (int, error) { return 0, errors.New("disconnected") }

func TestJSONWriteFailureAbortsResponse(t *testing.T) {
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Error("partial response did not abort")
		}
	}()
	writeJSON(&disconnectedWriter{}, 200, map[string]bool{"ok": true})
}
