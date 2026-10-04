package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type contentFunc func(context.Context) (map[string]interface{}, error)

func (f contentFunc) PublicContent(ctx context.Context) (map[string]interface{}, error) {
	return f(ctx)
}
func TestPublishedContentCacheAndBoundedDatabaseContext(t *testing.T) {
	reader := contentFunc(func(ctx context.Context) (map[string]interface{}, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Error("database request lacks bounded deadline")
		}
		return map[string]interface{}{"state": "unpublished", "initiatives": []string{}, "ledger": []string{}, "ledger_as_of": nil, "cadence": nil}, nil
	})
	response := httptest.NewRecorder()
	server := NewServer("", reader, nil)
	server.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/donor/public/content", nil))
	if response.Code != 200 || response.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("bad published response %d %v", response.Code, response.Header())
	}
}
func TestDatabaseFailureIsPrivateAndNotCached(t *testing.T) {
	reader := contentFunc(func(ctx context.Context) (map[string]interface{}, error) {
		return nil, errors.New("private database detail")
	})
	response := httptest.NewRecorder()
	server := NewServer("", reader, nil)
	server.Handler.ServeHTTP(response, httptest.NewRequest("GET", "/donor/public/content", nil))
	if response.Code != 503 || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("unsafe error %s", response.Body.String())
	}
}
