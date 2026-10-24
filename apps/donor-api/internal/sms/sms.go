// Package sms sends one-time sign-in codes over SMS. It mirrors internal/mail:
// FromEnvironment returns nil when SMS is deliberately unconfigured, so a
// deployment without an SMS provider simply offers no phone sign-in rather than
// failing. The Sender interface is what the store depends on; the concrete
// provider (a stub today, a real gateway once credentials exist) is injected.
package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrConfiguration names the variables to fix; it never echoes their values.
var ErrConfiguration = errors.New("invalid SMS configuration")

// ErrUnavailable is returned by a Sender that cannot deliver right now.
var ErrUnavailable = errors.New("SMS delivery is unavailable")

// Sender delivers a sign-in code to a phone in E.164 form. The message text is
// composed by the caller so the wording lives with the rest of the app's copy.
type Sender interface {
	Send(ctx context.Context, phone, message string) error
}

// Settings is a resolved SMS configuration. Provider selects the concrete
// gateway; Stub is set when the deployment opts into the recording stub
// (DONOR_SMS_PROVIDER=stub), which is the only provider wired today — the real
// gateway is dropped in behind the same interface once the operator provisions
// credentials.
type Settings struct {
	Provider  string
	AccountID string
	AuthToken string
	Sender    string // the From number or alphanumeric sender id
}

// FromEnvironment reads the SMS configuration, or returns nil when none is set
// (phone sign-in then stays off). An invalid configuration errors naming the
// offending variables.
func FromEnvironment(getenv func(string) string) (*Settings, error) {
	s := Settings{
		Provider:  strings.ToLower(strings.TrimSpace(getenv("DONOR_SMS_PROVIDER"))),
		AccountID: getenv("DONOR_SMS_ACCOUNT_ID"),
		AuthToken: getenv("DONOR_SMS_AUTH_TOKEN"),
		Sender:    getenv("DONOR_SMS_FROM"),
	}
	if s.Provider == "" && s.AccountID == "" && s.AuthToken == "" && s.Sender == "" {
		return nil, nil
	}
	var invalid []string
	switch s.Provider {
	case "stub":
		// The stub needs no credentials; a sender id is still required so the
		// shape matches a real provider and the From copy is exercised.
		if s.Sender == "" {
			invalid = append(invalid, "DONOR_SMS_FROM")
		}
	case "twilio":
		// Shape only — the live Twilio client is not built yet, so a twilio
		// configuration is rejected with a clear message rather than silently
		// behaving like the stub.
		return nil, fmt.Errorf(
			"%w: DONOR_SMS_PROVIDER=twilio is not yet implemented; set DONOR_SMS_PROVIDER=stub for now",
			ErrConfiguration,
		)
	default:
		invalid = append(invalid, "DONOR_SMS_PROVIDER (expected: stub)")
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("%w: check %s", ErrConfiguration, strings.Join(invalid, ", "))
	}
	return &s, nil
}

// NewSender builds the Sender for a resolved configuration.
func NewSender(s *Settings) Sender {
	if s == nil {
		return nil
	}
	switch s.Provider {
	case "stub":
		return &StubSender{from: s.Sender}
	default:
		return nil
	}
}

// StubSender records the most recent message per phone instead of sending it,
// so development and tests can read back the code. It is safe for concurrent
// use. It is NOT a real gateway: a production deployment must configure a real
// provider once one is implemented.
type StubSender struct {
	from string
	mu   sync.Mutex
	last map[string]string
}

// Send records message as the latest for phone.
func (s *StubSender) Send(_ context.Context, phone, message string) error {
	if phone == "" || message == "" {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[string]string{}
	}
	s.last[phone] = message
	return nil
}

// Last returns the most recent message sent to phone, for tests and local dev.
func (s *StubSender) Last(phone string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.last[phone]
	return msg, ok
}
