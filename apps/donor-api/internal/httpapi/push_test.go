package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"impactgraph/donor-api/internal/store"
)

// pushDevices accepts only appSession and records the devices it is given.
type pushDevices struct {
	devices    []store.PushDevice
	unregister int
	err        error
}

func (p *pushDevices) RegisterDevice(_ context.Context, token string, device store.PushDevice) (store.PushRegistration, error) {
	if token != appSession {
		return store.PushRegistration{}, store.ErrDenied
	}
	p.devices = append(p.devices, device)
	return store.PushRegistration{Registered: true, Platform: &device.Platform}, p.err
}

func (p *pushDevices) UnregisterDevice(_ context.Context, token string) (store.PushRegistration, error) {
	if token != appSession {
		return store.PushRegistration{}, store.ErrDenied
	}
	p.unregister++
	return store.PushRegistration{}, p.err
}

const deviceBody = `{"platform":"IOS","token":"` + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + `","environment":"SANDBOX"}`

func TestPushDeviceRoutesAdmitOnlyTheApp(t *testing.T) {
	for _, path := range []string{"/donor/me/push-device", "/donor/me/push-device/unregister"} {
		devices := &pushDevices{}
		server := NewServer("", Services{Push: devices})
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, appRequest("POST", path, deviceBody))
		if response.Code != http.StatusOK || len(devices.devices)+devices.unregister != 1 {
			t.Fatalf("%s from the app: got %d %s", path, response.Code, response.Body)
		}

		browser := httptest.NewRequest("POST", path, strings.NewReader(deviceBody))
		browser.Header.Set("Origin", testPublicOrigin)
		browser.AddCookie(&http.Cookie{Name: donorCookie, Value: appSession})
		response = httptest.NewRecorder()
		server.Handler.ServeHTTP(response, browser)
		if response.Code != http.StatusForbidden || len(devices.devices)+devices.unregister != 1 {
			t.Fatalf("%s from a signed-in browser: got %d, want 403 before storage", path, response.Code)
		}

		anonymous := appRequest("POST", path, deviceBody)
		anonymous.Header.Del("Authorization")
		response = httptest.NewRecorder()
		server.Handler.ServeHTTP(response, anonymous)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a session: got %d, want 401", path, response.Code)
		}
	}
}

func TestPushDeviceRegistrationValidatesAndReportsConfiguration(t *testing.T) {
	devices := &pushDevices{}
	server := NewServer("", Services{Push: devices})
	call := func(body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, appRequest("POST", "/donor/me/push-device", body))
		return response
	}
	for _, body := range []string{
		`{"platform":"WINDOWS","token":"x"}`,
		`{"platform":"IOS"}`,
		`{"platform":"IOS","token":"x","environment":null}`,
		`{"platform":"ANDROID","token":"x","name":"phone"}`,
	} {
		if response := call(body); response.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: got %d, want 422", body, response.Code)
		}
	}
	if len(devices.devices) != 0 {
		t.Fatal("invalid registrations reached storage")
	}
	if response := call(`{"platform":"ANDROID","token":"fcm-token"}`); response.Code != http.StatusOK || devices.devices[0] != (store.PushDevice{Platform: "ANDROID", Token: "fcm-token"}) {
		t.Fatalf("android registration: got %d %+v", response.Code, devices.devices)
	}
	devices.err = store.ErrPushToken
	if response := call(deviceBody); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected token: got %d", response.Code)
	}
	devices.err = store.ErrPushUnconfigured
	response := call(deviceBody)
	var problem struct{ Code string }
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || response.Code != http.StatusServiceUnavailable || problem.Code != "PUSH_UNCONFIGURED" {
		t.Fatalf("unconfigured platform: got %d %s", response.Code, response.Body)
	}
}

// conversionDonor records the since parameter it is given.
type conversionDonor struct {
	sessionDonor
	since *time.Time
}

func (c *conversionDonor) Conversions(_ context.Context, token string, since *time.Time) (store.Conversions, error) {
	c.since = since
	return store.Conversions{}, c.check(token)
}

func TestConversionsServeBothClients(t *testing.T) {
	donor := &conversionDonor{}
	server := NewServer("", Services{Donor: donor})
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, appRequest("GET", "/donor/me/conversions?since=2026-10-01T12:00:00.25Z", ""))
	if want := time.Date(2026, 10, 1, 12, 0, 0, 250_000_000, time.UTC); response.Code != http.StatusOK || donor.since == nil || !donor.since.Equal(want) {
		t.Fatalf("app request: got %d with since %v", response.Code, donor.since)
	}
	browser := httptest.NewRequest("GET", "/donor/me/conversions", nil)
	browser.AddCookie(&http.Cookie{Name: donorCookie, Value: appSession})
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, browser)
	if response.Code != http.StatusOK || donor.since != nil {
		t.Fatalf("browser request: got %d with since %v", response.Code, donor.since)
	}
}
