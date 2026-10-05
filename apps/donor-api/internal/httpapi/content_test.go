package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"impactgraph/donor-api/internal/store"
)

type contentFunc func(context.Context) (store.PublicContent, error)

func (f contentFunc) PublicContent(ctx context.Context) (store.PublicContent, error) {
	return f(ctx)
}
func TestPublishedContentCacheAndBoundedDatabaseContext(t *testing.T) {
	reader := contentFunc(func(ctx context.Context) (store.PublicContent, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Error("database request lacks bounded deadline")
		}
		return store.PublicContent{State: "unpublished", Initiatives: []map[string]json.RawMessage{}, Ledger: []map[string]json.RawMessage{}}, nil
	})
	response := httptest.NewRecorder()
	server := NewServer("", Services{Content: reader})
	server.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/donor/public/content", nil))
	if response.Code != 200 || response.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("bad published response %d %v", response.Code, response.Header())
	}
}
func TestDatabaseFailureIsPrivateAndNotCached(t *testing.T) {
	reader := contentFunc(func(ctx context.Context) (store.PublicContent, error) {
		return store.PublicContent{}, errors.New("private database detail")
	})
	response := httptest.NewRecorder()
	server := NewServer("", Services{Content: reader})
	server.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/donor/public/content", nil))
	if response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("unsafe error %s", response.Body.String())
	}
}
