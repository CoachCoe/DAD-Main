package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dad/donor-api/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// donorPasskeyStub signs in any passkey and records what each call was given.
type donorPasskeyStub struct {
	err      error
	client   store.Client
	sessions []string
	name     string
}

func (s *donorPasskeyStub) BeginSignIn(context.Context) (store.Ceremony, error) {
	return store.Ceremony{ID: "ceremony"}, s.err
}
func (s *donorPasskeyStub) FinishSignIn(_ context.Context, _ string, _ []byte, client store.Client) (store.Tokens, error) {
	s.client = client
	if client.App {
		return appTokens, s.err
	}
	return browserTokens, s.err
}
func (s *donorPasskeyStub) BeginRegister(_ context.Context, session string) (store.Ceremony, error) {
	s.sessions = append(s.sessions, session)
	return store.Ceremony{ID: "ceremony"}, s.err
}
func (s *donorPasskeyStub) FinishRegister(_ context.Context, session, _ string, _ []byte, name string) (store.PasskeyList, error) {
	s.sessions, s.name = append(s.sessions, session), name
	return store.PasskeyList{Passkeys: []store.PasskeyInfo{}}, s.err
}
func (s *donorPasskeyStub) ListPasskeys(_ context.Context, session string) (store.PasskeyList, error) {
	s.sessions = append(s.sessions, session)
	return store.PasskeyList{Passkeys: []store.PasskeyInfo{}}, s.err
}
func (s *donorPasskeyStub) DeletePasskey(_ context.Context, session string, _ pgtype.UUID) (store.PasskeyList, error) {
	s.sessions = append(s.sessions, session)
	return store.PasskeyList{Passkeys: []store.PasskeyInfo{}}, s.err
}

func passkeyAnswer(fields string) string {
	return `{"ceremony":"` + strings.Repeat("c", 43) + `","credential":{"id":"passkey"}` + fields + `}`
}

func TestPasskeySignInReturnsTokensOnlyToTheApp(t *testing.T) {
	for _, tc := range []struct {
		name        string
		app         bool
		bodyTokens  bool
		cookieToken bool
	}{
		{"app", true, true, false},
		{"browser", false, false, true},
	} {
		r := httptest.NewRequest("POST", "/donor/passkeys/sign-in", strings.NewReader(passkeyAnswer("")))
		if tc.app {
			r.Header.Set("X-DAD-Client", "native")
		} else {
			r.Header.Set("Origin", "http://localhost:3000")
		}
		response := httptest.NewRecorder()
		NewServer("", Services{DonorPasskeys: &donorPasskeyStub{}}).Handler.ServeHTTP(response, r)
		var body map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		_, access := body["access_token"]
		cookieToken := strings.Contains(response.Header().Get("Set-Cookie"), browserTokens.Access)
		if response.Code != http.StatusOK || access != tc.bodyTokens || cookieToken != tc.cookieToken || (!tc.app && body["signed_in"] != true) {
			t.Errorf("%s: got %d %v with cookie %v, want tokens in body %v and cookie %v", tc.name, response.Code, body, cookieToken, tc.bodyTokens, tc.cookieToken)
		}
	}
}

func TestPasskeySignInRecordsOnlyTheAppsDeviceDescription(t *testing.T) {
	name := strings.Repeat("é", store.MaxDeviceName)
	for _, tc := range []struct {
		name, fields string
		app          bool
		status       int
		client       store.Client
	}{
		{"app device", `,"device_name":"` + name + `","platform":"ANDROID"`, true, http.StatusOK, store.Client{App: true, Device: name, Platform: "ANDROID"}},
		{"app without description", ``, true, http.StatusOK, store.Client{App: true}},
		{"browser description ignored", `,"device_name":"Laptop","platform":"IOS"`, false, http.StatusOK, store.Client{}},
		{"unknown platform", `,"platform":"WINDOWS"`, true, http.StatusUnprocessableEntity, store.Client{}},
		{"null platform", `,"platform":null`, true, http.StatusUnprocessableEntity, store.Client{}},
		{"long device name", `,"device_name":"` + name + `x"`, true, http.StatusUnprocessableEntity, store.Client{}},
	} {
		r := httptest.NewRequest("POST", "/donor/passkeys/sign-in", strings.NewReader(passkeyAnswer(tc.fields)))
		if tc.app {
			r.Header.Set("X-DAD-Client", "native")
		} else {
			r.Header.Set("Origin", "http://localhost:3000")
		}
		stub := &donorPasskeyStub{}
		response := httptest.NewRecorder()
		NewServer("", Services{DonorPasskeys: stub}).Handler.ServeHTTP(response, r)
		if response.Code != tc.status || stub.client != tc.client {
			t.Errorf("%s: got %d issuing to %+v, want %d issuing to %+v", tc.name, response.Code, stub.client, tc.status, tc.client)
		}
	}
}

func TestPasskeyRoutesActForTheSessionHolder(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/donor/me/passkeys", ""},
		{"POST", "/donor/me/passkeys/register/options", ""},
		{"POST", "/donor/me/passkeys/register", passkeyAnswer(`,"name":"Work laptop"`)},
		{"POST", "/donor/me/passkeys/" + testID + "/delete", ""},
	} {
		stub := &donorPasskeyStub{}
		response := httptest.NewRecorder()
		NewServer("", Services{DonorPasskeys: stub}).Handler.ServeHTTP(response, appRequest(tc.method, tc.path, tc.body))
		if response.Code != http.StatusOK || len(stub.sessions) != 1 || stub.sessions[0] != appSession {
			t.Errorf("%s %s: got %d acting for %v, want 200 for the bearer session", tc.method, tc.path, response.Code, stub.sessions)
		}
	}
}

func TestPasskeyRegistrationValidatesItsName(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		status       int
		stored       string
	}{
		{"named", `,"name":"Work laptop"`, http.StatusOK, "Work laptop"},
		{"unnamed", ``, http.StatusOK, ""},
		{"null name", `,"name":null`, http.StatusOK, ""},
		{"long name", `,"name":"` + strings.Repeat("a", store.MaxPasskeyNameRunes+1) + `"`, http.StatusUnprocessableEntity, ""},
		{"control character", `,"name":"line\nbreak"`, http.StatusUnprocessableEntity, ""},
	} {
		stub := &donorPasskeyStub{}
		response := httptest.NewRecorder()
		NewServer("", Services{DonorPasskeys: stub}).Handler.ServeHTTP(response, appRequest("POST", "/donor/me/passkeys/register", passkeyAnswer(tc.fields)))
		if response.Code != tc.status || stub.name != tc.stored {
			t.Errorf("%s: got %d storing %q, want %d storing %q", tc.name, response.Code, stub.name, tc.status, tc.stored)
		}
	}
}

func TestPasskeyErrors(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		err                error
		status             int
	}{
		{"POST", "/donor/passkeys/sign-in", passkeyAnswer(""), store.ErrPasskeyDenied, http.StatusUnauthorized},
		{"POST", "/donor/me/passkeys/register", passkeyAnswer(""), store.ErrPasskeyDenied, http.StatusUnauthorized},
		{"GET", "/donor/me/passkeys", "", store.ErrDenied, http.StatusUnauthorized},
		{"POST", "/donor/me/passkeys/" + testID + "/delete", "", pgx.ErrNoRows, http.StatusNotFound},
		{"POST", "/donor/me/passkeys/not-a-passkey/delete", "", nil, http.StatusNotFound},
		{"POST", "/donor/passkeys/sign-in/options", "", errors.New("private failure"), http.StatusServiceUnavailable},
	} {
		response := httptest.NewRecorder()
		NewServer("", Services{DonorPasskeys: &donorPasskeyStub{err: tc.err}}).Handler.ServeHTTP(response, appRequest(tc.method, tc.path, tc.body))
		if response.Code != tc.status || strings.Contains(response.Body.String(), "private") {
			t.Errorf("%s %s with %v: got %d %s, want %d", tc.method, tc.path, tc.err, response.Code, response.Body, tc.status)
		}
	}
	response := httptest.NewRecorder()
	NewServer("", Services{}).Handler.ServeHTTP(response, appRequest("POST", "/donor/passkeys/sign-in/options", ""))
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("unconfigured passkeys: got %d, want 503", response.Code)
	}
}
