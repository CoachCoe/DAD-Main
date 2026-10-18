// Package push delivers donor app notifications through Apple Push
// Notification service (APNs) and Firebase Cloud Messaging (FCM). Each
// provider authenticates with a locally signed token, so key material never
// leaves the process. Notifications carry generic copy, a category and an
// app path, never donor data: they transit Apple's and Google's servers.
package push

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Platforms a device can register for, as the API names them.
const (
	IOS     = "IOS"
	Android = "ANDROID"
)

var (
	// ErrUnregistered means the provider no longer recognizes the device
	// token, because the app was removed or the token was replaced.
	ErrUnregistered = errors.New("push token is not registered")
	// ErrRejected means the provider refused the credentials or the request
	// itself; retrying cannot help until configuration changes.
	ErrRejected = errors.New("push provider rejected the request")
	// ErrConfiguration reports invalid push settings at startup.
	ErrConfiguration = errors.New("push configuration is invalid")
)

// Unavailable is a failure worth retrying, after After when the provider
// asked for a delay.
type Unavailable struct{ After time.Duration }

func (Unavailable) Error() string { return "push provider is unavailable" }

// Notification is what one device is shown. ID collapses repeats: a retry
// replaces a notification the device already shows instead of adding one.
type Notification struct {
	ID          string
	Category    string
	Title, Body string
	Path        string
	Expires     time.Time
}

// Device is a push token and, for APNs, the environment that issued it.
type Device struct {
	Token       string
	Environment string
}

type Provider interface {
	Send(context.Context, Device, Notification) error
}

// sendTimeout bounds one provider request, well inside a worker step.
const sendTimeout = 15 * time.Second

func newClient() *http.Client { return &http.Client{Timeout: sendTimeout} }

// Config is the providers whose settings are present.
type Config struct {
	APNs *APNs
	FCM  *FCM
}

// Providers returns the configured providers by platform.
func (c Config) Providers() map[string]Provider {
	providers := map[string]Provider{}
	if c.APNs != nil {
		providers[IOS] = c.APNs
	}
	if c.FCM != nil {
		providers[Android] = c.FCM
	}
	return providers
}

// FromEnvironment loads the providers whose settings are present, and fails
// when one is configured incompletely or its key cannot be used.
func FromEnvironment(getenv func(string) string) (Config, error) {
	var config Config
	apns := []string{
		getenv("DONOR_PUSH_APNS_KEY_FILE"),
		getenv("DONOR_PUSH_APNS_KEY_ID"),
		getenv("DONOR_PUSH_APNS_TEAM_ID"),
		getenv("DONOR_PUSH_APNS_TOPIC"),
	}
	if apns[0] != "" || apns[1] != "" || apns[2] != "" || apns[3] != "" {
		key, err := readFile(apns[0])
		if err != nil {
			return config, fmt.Errorf("%w: DONOR_PUSH_APNS_KEY_FILE is unreadable", ErrConfiguration)
		}
		if config.APNs, err = NewAPNs(key, apns[1], apns[2], apns[3]); err != nil {
			return config, err
		}
	}
	if path := getenv("DONOR_PUSH_FCM_KEY_FILE"); path != "" {
		key, err := readFile(path)
		if err != nil {
			return config, fmt.Errorf("%w: DONOR_PUSH_FCM_KEY_FILE is unreadable", ErrConfiguration)
		}
		if config.FCM, err = NewFCM(key); err != nil {
			return config, err
		}
	}
	return config, nil
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err == nil && len(data) > 1<<20 {
		err = errors.New("file exceeds 1 MiB")
	}
	return data, err
}

// jwt signs header and claims with sign, which receives the SHA-256 digest
// of the encoded pair.
func jwt(header, claims any, sign func(digest []byte) ([]byte, error)) (string, error) {
	encode := func(v any) (string, error) {
		data, err := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(data), err
	}
	h, err := encode(header)
	if err != nil {
		return "", err
	}
	c, err := encode(claims)
	if err != nil {
		return "", err
	}
	unsigned := h + "." + c
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := sign(digest[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// readResponse reads a bounded provider response body. Bodies can echo
// tokens and must never reach logs.
func readResponse(response *http.Response) []byte {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return data
}

// throttled maps throttling and server failures to Unavailable, honouring a
// Retry-After given in seconds, and returns nil for any other response.
func throttled(response *http.Response) error {
	if response.StatusCode != http.StatusTooManyRequests && response.StatusCode < 500 {
		return nil
	}
	seconds, _ := strconv.Atoi(response.Header.Get("Retry-After"))
	return Unavailable{After: time.Duration(max(seconds, 0)) * time.Second}
}
