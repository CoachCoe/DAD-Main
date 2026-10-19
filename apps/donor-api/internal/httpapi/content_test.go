package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dad/donor-api/internal/store"
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
		return store.PublicContent{
			State:       "unpublished",
			Initiatives: []map[string]json.RawMessage{},
			Ledger:      []map[string]json.RawMessage{},
		}, nil
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

type uploadStub struct {
	ContentAdminSource
	authorizeErr error
	stored       int
}

func (s *uploadStub) AuthorizeSourceUpload(context.Context, string) error { return s.authorizeErr }
func (s *uploadStub) StoreSource(_ context.Context, _, name string, data []byte) (store.StoredSource, error) {
	s.stored++
	return store.StoredSource{Name: name}, nil
}

type unreadBody struct{ t *testing.T }

func (b unreadBody) Read([]byte) (int, error) {
	b.t.Error("upload body read before storage was known to be configured")
	return 0, io.EOF
}

func TestSourceUploadWithoutStorageKeyIsRejectedBeforeBody(t *testing.T) {
	upload := func(stub *uploadStub, body io.Reader) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/donor/staff/content/sources?name=source.pdf", body)
		r.Header.Set("Origin", testStaffOrigin)
		r.AddCookie(&http.Cookie{Name: staffCookie, Value: "staff-session"})
		response := httptest.NewRecorder()
		NewServer("", Services{ContentAdmin: stub}).Handler.ServeHTTP(response, r)
		return response
	}
	stub := &uploadStub{authorizeErr: store.ErrSourceStorageUnconfigured}
	response := upload(stub, unreadBody{t})
	if response.Code != 503 || !strings.Contains(response.Body.String(), `"code":"SOURCE_STORAGE_UNCONFIGURED"`) || stub.stored != 0 {
		t.Fatalf("unconfigured storage: %d %s", response.Code, response.Body)
	}
	stub = &uploadStub{authorizeErr: store.ErrStaffForbidden}
	if response = upload(stub, unreadBody{t}); response.Code != 403 {
		t.Fatalf("forbidden upload: %d", response.Code)
	}
	stub = &uploadStub{}
	if response = upload(stub, strings.NewReader("signed record")); response.Code != 200 || stub.stored != 1 {
		t.Fatalf("configured upload: %d %s", response.Code, response.Body)
	}
}

type publishStub struct {
	ContentAdminSource
	err error
}

func (publishStub) Authorize(context.Context, string) error { return nil }
func (s publishStub) Publish(context.Context, string, store.PublicationRequest) (store.Publication, error) {
	return store.Publication{}, s.err
}

func TestRefusedPublicationListsEveryProblem(t *testing.T) {
	refused := &store.PublicationError{
		Problems: []store.PublicationProblem{{Sheet: "ledger", Row: 3, Column: "value", Reason: "not_integer"}},
	}
	r := httptest.NewRequest(
		"POST",
		"/donor/staff/content/publish",
		strings.NewReader(`{"initiatives_csv":"","ledger_csv":"","as_of":"2026-10-01","cadence":"monthly"}`),
	)
	r.Header.Set("Origin", testStaffOrigin)
	r.AddCookie(&http.Cookie{Name: staffCookie, Value: "staff-session"})
	response := httptest.NewRecorder()
	NewServer("", Services{ContentAdmin: publishStub{err: refused}}).Handler.ServeHTTP(response, r)
	want := `{"detail":"The spreadsheet has problems","errors":[{"sheet":"ledger","row":3,"column":"value","reason":"not_integer"}]}`
	if response.Code != http.StatusUnprocessableEntity || strings.TrimSpace(response.Body.String()) != want {
		t.Fatalf("got %d %s, want 422 %s", response.Code, response.Body, want)
	}
}
