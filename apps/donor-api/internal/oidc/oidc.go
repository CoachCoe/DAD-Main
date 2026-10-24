// Package oidc verifies Apple and Google id_tokens for "Sign in with Apple /
// Google" (PRD §5.1). Like internal/sms it degrades: FromEnvironment returns
// nil when no provider is configured, so a deployment without client ids simply
// offers no social sign-in. A Verifier turns a provider's id_token into a
// verified Identity (stable subject); the store then links that identity to a
// donor.
//
// The live verifier (fetch the provider JWKS, validate RS256/ES256 signature,
// issuer, audience, and expiry) is not built yet — it is dropped in behind the
// Verifier interface once client ids exist. Today a stub verifier validates an
// HMAC-signed test token so the whole sign-in and account-linking flow is
// exercised end to end.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrConfiguration names the variables to fix without echoing their values.
var ErrConfiguration = errors.New("invalid OIDC configuration")

// ErrToken is returned for any id_token that does not verify.
var ErrToken = errors.New("the sign-in token could not be verified")

// Provider is one social sign-in provider.
type Provider string

const (
	Apple  Provider = "APPLE"
	Google Provider = "GOOGLE"
)

// Valid reports whether p names a supported provider.
func (p Provider) Valid() bool { return p == Apple || p == Google }

// ParseProvider maps a path segment ("apple"/"google") to a Provider.
func ParseProvider(s string) (Provider, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "apple":
		return Apple, true
	case "google":
		return Google, true
	default:
		return "", false
	}
}

// Identity is a verified external account: the provider and its stable, opaque
// subject ('sub'). Email is carried for first-time account creation only and is
// never the identity key, since it can change.
type Identity struct {
	Provider Provider
	Subject  string
	Email    string
}

// Verifier validates a provider's id_token and returns the verified Identity.
type Verifier interface {
	Verify(ctx context.Context, provider Provider, idToken string) (Identity, error)
}

// Settings is a resolved OIDC configuration. ClientIDs is the audience expected
// per provider. Mode selects the verifier; "stub" (DONOR_OIDC_MODE=stub) is the
// only mode wired today.
type Settings struct {
	Mode      string
	ClientIDs map[Provider]string
	StubKey   string // HMAC key the stub verifier accepts; required in stub mode
}

// FromEnvironment reads the OIDC configuration, or nil when none is set.
func FromEnvironment(getenv func(string) string) (*Settings, error) {
	mode := strings.ToLower(strings.TrimSpace(getenv("DONOR_OIDC_MODE")))
	apple := strings.TrimSpace(getenv("DONOR_OIDC_APPLE_CLIENT_ID"))
	google := strings.TrimSpace(getenv("DONOR_OIDC_GOOGLE_CLIENT_ID"))
	stubKey := getenv("DONOR_OIDC_STUB_KEY")
	if mode == "" && apple == "" && google == "" && stubKey == "" {
		return nil, nil
	}
	s := Settings{Mode: mode, ClientIDs: map[Provider]string{}, StubKey: stubKey}
	if apple != "" {
		s.ClientIDs[Apple] = apple
	}
	if google != "" {
		s.ClientIDs[Google] = google
	}
	var invalid []string
	switch mode {
	case "stub":
		if stubKey == "" {
			invalid = append(invalid, "DONOR_OIDC_STUB_KEY")
		}
	case "live":
		return nil, fmt.Errorf("%w: DONOR_OIDC_MODE=live is not yet implemented; set DONOR_OIDC_MODE=stub for now", ErrConfiguration)
	default:
		invalid = append(invalid, "DONOR_OIDC_MODE (expected: stub)")
	}
	if len(s.ClientIDs) == 0 {
		invalid = append(invalid, "DONOR_OIDC_APPLE_CLIENT_ID or DONOR_OIDC_GOOGLE_CLIENT_ID")
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("%w: check %s", ErrConfiguration, strings.Join(invalid, ", "))
	}
	return &s, nil
}

// NewVerifier builds the Verifier for a resolved configuration.
func NewVerifier(s *Settings) Verifier {
	if s == nil {
		return nil
	}
	switch s.Mode {
	case "stub":
		return &stubVerifier{clientIDs: s.ClientIDs, key: s.StubKey}
	default:
		return nil
	}
}

// Configured reports whether a provider is accepted by this configuration.
func (s *Settings) Configured(p Provider) bool {
	if s == nil {
		return false
	}
	_, ok := s.ClientIDs[p]
	return ok
}
