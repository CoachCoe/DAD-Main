package mail

import (
	"strconv"

	"impactgraph/donor-api/internal/credentials"
)

// FromEnvironment returns nil when mail is deliberately unconfigured.
func FromEnvironment(getenv func(string) string) (*Settings, error) {
	s := Settings{Host: getenv("DONOR_SMTP_HOST"), Port: 587, Username: getenv("DONOR_SMTP_USERNAME"), Password: getenv("DONOR_SMTP_PASSWORD"), Sender: getenv("DONOR_EMAIL_FROM"), EncryptionKey: getenv("DONOR_EMAIL_ENCRYPTION_KEY")}
	if s.Host == "" && s.Username == "" && s.Password == "" && s.Sender == "" && s.EncryptionKey == "" {
		return nil, nil
	}
	if port := getenv("DONOR_SMTP_PORT"); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil {
			return nil, ErrConfiguration
		}
		s.Port = value
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if (s.Username == "") != (s.Password == "") {
		return nil, ErrConfiguration
	}
	if _, err := credentials.Encrypt(nil, s.EncryptionKey); err != nil {
		return nil, ErrConfiguration
	}
	return &s, nil
}
