package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dad/donor-api/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

type privateCalendar struct {
	EventReadSource
	err error
}

func (s privateCalendar) CalendarForDonor(_ context.Context, token string, _ pgtype.UUID) (string, error) {
	if token != appSession {
		return "", errors.New("unexpected token")
	}
	return "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n", s.err
}

func TestPrivateCalendarRequiresAuthenticationAndNeverCaches(t *testing.T) {
	const path = "/donor/me/events/00000000-0000-4000-8000-000000000001/calendar.ics"
	for _, native := range []bool{false, true} {
		for _, failure := range []error{nil, store.ErrDenied, store.ErrEventNotFound} {
			server := NewServer("", Services{EventRead: privateCalendar{err: failure}})
			r := appRequest("GET", path, "")
			if !native {
				r = httptest.NewRequest("GET", path, nil)
				r.AddCookie(&http.Cookie{Name: donorCookie, Value: appSession})
			}
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, r)
			want := http.StatusOK
			if failure == store.ErrDenied {
				want = http.StatusUnauthorized
			} else if failure == store.ErrEventNotFound {
				want = http.StatusNotFound
			}
			if response.Code != want || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("native=%v failure=%v: %d %v", native, failure, response.Code, response.Header())
			}
			if failure == nil && (response.Header().Get("Content-Type") != "text/calendar; charset=utf-8" ||
				response.Body.String() != "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n") {
				t.Fatalf("calendar download: %v %q", response.Header(), response.Body.String())
			}
		}
	}
	server := NewServer("", Services{EventRead: privateCalendar{}})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous calendar: %d", response.Code)
	}
}
