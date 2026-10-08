package mail

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestEnvironmentConfiguration(t *testing.T) {
	key := base64.URLEncoding.EncodeToString(make([]byte, 32))
	valid := map[string]string{"DONOR_SMTP_HOST": "smtp.example.org", "DONOR_EMAIL_FROM": "DAD <sender@example.org>", "DONOR_EMAIL_ENCRYPTION_KEY": key}
	for _, tc := range []struct {
		name, field, value string
		invalid            bool
	}{
		{"defaults", "", "", false},
		{"custom port", "DONOR_SMTP_PORT", "2525", false},
		{"bad port", "DONOR_SMTP_PORT", "abc", true},
		{"port range", "DONOR_SMTP_PORT", "65536", true},
		{"implicit TLS port", "DONOR_SMTP_PORT", "465", true},
		{"missing host", "DONOR_SMTP_HOST", "", true},
		{"bad sender", "DONOR_EMAIL_FROM", "not-an-address", true},
		{"header injection", "DONOR_EMAIL_FROM", "sender@example.org\r\nBcc: victim@example.org", true},
		{"invalid key", "DONOR_EMAIL_ENCRYPTION_KEY", "configured", true},
		{"key list", "DONOR_EMAIL_ENCRYPTION_KEY", base64.URLEncoding.EncodeToString([]byte(strings.Repeat("n", 32))) + "," + key, false},
		{"invalid retired key", "DONOR_EMAIL_ENCRYPTION_KEY", key + ",configured", true},
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
				if want := tc.field; !strings.Contains(err.Error(), want) || (tc.value != "" && strings.Contains(err.Error(), tc.value)) {
					t.Fatalf("got %q, want it to name %s without its value", err, want)
				}
				if tc.name == "invalid retired key" && (!strings.Contains(err.Error(), "key 2 of 2") || strings.Contains(err.Error(), key)) {
					t.Fatalf("got %q, want it to name the second key's position without key material", err)
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
