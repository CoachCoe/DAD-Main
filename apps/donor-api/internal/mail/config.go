package mail

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"impactgraph/donor-api/internal/credentials"
)

// FromEnvironment returns nil when mail is deliberately unconfigured. An
// invalid configuration's error names the variables to fix, never their values.
func FromEnvironment(getenv func(string) string) (*Settings, error) {
	s := Settings{Host: getenv("DONOR_SMTP_HOST"), Port: 587, Username: getenv("DONOR_SMTP_USERNAME"), Password: getenv("DONOR_SMTP_PASSWORD"), Sender: getenv("DONOR_EMAIL_FROM"), EncryptionKey: getenv("DONOR_EMAIL_ENCRYPTION_KEY")}
	if s.Host == "" && s.Username == "" && s.Password == "" && s.Sender == "" && s.EncryptionKey == "" {
		return nil, nil
	}
	var invalid []string
	if port := getenv("DONOR_SMTP_PORT"); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil {
			invalid = append(invalid, "DONOR_SMTP_PORT")
		}
		s.Port = value
	}
	for _, check := range []struct {
		name string
		ok   bool
	}{
		{"DONOR_SMTP_HOST", s.Host != "" && !strings.ContainsAny(s.Host, "\r\n")},
		// Port 465 needs implicit TLS, which is unsupported; use STARTTLS on 587.
		{"DONOR_SMTP_PORT", s.Port >= 1 && s.Port <= 65535 && s.Port != 465},
		{"DONOR_EMAIL_FROM", validAddress(s.Sender)},
		{"DONOR_EMAIL_ENCRYPTION_KEY", credentials.ValidateKey(s.EncryptionKey) == nil},
		{"DONOR_SMTP_USERNAME and DONOR_SMTP_PASSWORD", (s.Username == "") == (s.Password == "")},
	} {
		if !check.ok && !slices.Contains(invalid, check.name) {
			invalid = append(invalid, check.name)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("%w: check %s", ErrConfiguration, strings.Join(invalid, ", "))
	}
	return &s, nil
}
