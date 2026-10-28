package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"dad/donor-api/internal/store"
	"dad/donor-api/internal/store/queries"
)

type actionsStub struct {
	AdminSource
	offsets []int64
	target  pgtype.UUID
	kind    string
}

func (s *actionsStub) PendingAction(
	_ context.Context,
	session string,
	_ pgtype.UUID,
	target pgtype.UUID,
	kind string,
) (store.PendingAction, error) {
	if session != "staff-session" {
		return store.PendingAction{}, store.ErrStaffDenied
	}
	s.target, s.kind = target, kind
	return store.PendingAction{}, nil
}

func TestPendingActionValidatesTargetAndKind(t *testing.T) {
	stub := &actionsStub{}
	server := NewServer("", Services{Admin: stub})
	id := "7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b"
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"?target_id=" + id + "&kind=REFUND", 200},
		{"?target_id=" + id + "&kind=CANCEL", 200},
		{"?target_id=" + id + "&kind=UNKNOWN", 422},
		{"?target_id=bad&kind=REFUND", 422},
		{"?kind=REFUND", 422},
	} {
		stub.kind = ""
		r := httptest.NewRequest("GET", "/donor/staff/donors/"+id+"/actions/pending"+tc.query, nil)
		r.AddCookie(&http.Cookie{Name: staffCookie, Value: "staff-session"})
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, r)
		if response.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, response.Code, response.Body)
		}
		if tc.status == 200 && (stub.target.String() != id || stub.kind == "") {
			t.Fatal("lookup filters not forwarded")
		}
		if tc.status != 200 && stub.kind != "" {
			t.Fatal("invalid lookup reached store")
		}
	}
}

func (s *actionsStub) DonorActions(_ context.Context, session string, _ pgtype.UUID, offset int64) (store.ActionPage, error) {
	s.offsets = append(s.offsets, offset)
	if session != "staff-session" {
		return store.ActionPage{}, store.ErrStaffDenied
	}
	if offset > 0 {
		return store.ActionPage{Actions: []queries.StaffActionPageRow{}}, nil
	}
	next := int64(50)
	return store.ActionPage{Actions: make([]queries.StaffActionPageRow, 50), NextOffset: &next}, nil
}

func TestStaffActionHistoryIsPaged(t *testing.T) {
	stub := &actionsStub{}
	server := NewServer("", Services{Admin: stub})
	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/donor/staff/donors/7d6f6b1c-6f4e-4c3e-9a51-6f1d9c1e2a3b/actions"+query, nil)
		r.AddCookie(&http.Cookie{Name: staffCookie, Value: "staff-session"})
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, r)
		return response
	}
	for _, tc := range []struct {
		query string
		rows  int
		next  any
	}{{"", 50, float64(50)}, {"?offset=50", 0, nil}} {
		response := get(tc.query)
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || len(body) != 2 {
			t.Fatalf("%q: %d %s", tc.query, response.Code, response.Body)
		}
		rows, ok := body["actions"].([]any)
		if !ok || len(rows) != tc.rows || body["next_offset"] != tc.next {
			t.Fatalf("%q: %s", tc.query, response.Body)
		}
	}
	if len(stub.offsets) != 2 || stub.offsets[0] != 0 || stub.offsets[1] != 50 {
		t.Fatalf("offsets passed to store: %v", stub.offsets)
	}
	for _, query := range []string{"?offset=-1", "?offset=x", "?offset=9223372036854775807"} {
		if response := get(query); response.Code != 422 {
			t.Fatalf("%s: got %d", query, response.Code)
		}
	}
	if len(stub.offsets) != 2 {
		t.Fatal("invalid offset reached the store")
	}
}

func TestStaffErrorsMapToStatuses(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		detail string
	}{
		{store.ErrStaffDenied, 401, "Passkey, invitation, or session invalid or expired"},
		{store.ErrStaffForbidden, 403, "Permission denied"},
		{store.ErrStaffInput, 422, "Invalid request fields"},
		{store.ErrStaffExists, 409, "A staff account with this email already exists"},
		{store.ErrStaffConflict, 409, "Staff access changed or conflicts with this request"},
		{store.EmailRoleError{Role: "DONOR"}, 409, "This email already has a donor account. Use a different email for this login."},
		{store.EmailRoleError{Role: "PARTNER"}, 409, "This email already signs in to a partner organization. Use a different email for this login."},
	} {
		if status, detail := staffStatus(tc.err); status != tc.status || detail != tc.detail {
			t.Errorf("%v: got %d %q, want %d %q", tc.err, status, detail, tc.status, tc.detail)
		}
	}
}
