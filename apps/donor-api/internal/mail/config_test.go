package mail

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestEnvironmentConfiguration(t *testing.T) {
	valid := map[string]string{"DONOR_SMTP_HOST": "smtp.example.org", "DONOR_EMAIL_FROM": "DAD <sender@example.org>", "DONOR_EMAIL_ENCRYPTION_KEY": base64.URLEncoding.EncodeToString(make([]byte, 32))}
	for _, tc := range []struct {
		name, field, value string
		invalid            bool
	}{
		{"defaults", "", "", false},
		{"custom port", "DONOR_SMTP_PORT", "2525", false},
		{"bad port", "DONOR_SMTP_PORT", "abc", true},
		{"port range", "DONOR_SMTP_PORT", "65536", true},
		{"missing host", "DONOR_SMTP_HOST", "", true},
		{"bad sender", "DONOR_EMAIL_FROM", "invalid", true},
		{"header injection", "DONOR_EMAIL_FROM", "sender@example.org\r\nBcc: victim@example.org", true},
		{"invalid key", "DONOR_EMAIL_ENCRYPTION_KEY", "configured", true},
		{"partial credentials", "DONOR_SMTP_USERNAME", "sender", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := FromEnvironment(func(key string) string {
				if key == tc.field {
					return tc.value
				}
				return valid[key]
			})
			if tc.invalid {
				if !errors.Is(err, ErrConfiguration) || s != nil {
					t.Fatalf("invalid settings accepted: %v", err)
				}
				return
			}
			if err != nil || s == nil {
				t.Fatalf("valid settings rejected: %v", err)
			}
			wantPort := 587
			if tc.name == "custom port" {
				wantPort = 2525
			}
			if s.Port != wantPort {
				t.Fatalf("port = %d", s.Port)
			}
		})
	}
	s, err := FromEnvironment(func(string) string { return "" })
	if s != nil || err != nil {
		t.Fatal("unconfigured mail should be disabled")
	}
}
