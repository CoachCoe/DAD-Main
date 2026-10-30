package dbroles

import (
	"net"
	"net/url"
	"strings"
)

// ConnectionURL returns the database URL named by variable (for example
// DONOR_API_DATABASE_URL), read from getenv, or "" when it is not configured.
//
// Three forms are accepted, so a password can reach the process as its own
// secret instead of inside a URL held in plaintext configuration:
//
//   - the full URL alone, as before;
//   - the full URL plus <prefix>_PASSWORD, where prefix is the variable without
//     its _URL suffix; the separate password replaces any one in the URL;
//   - no URL, but <prefix>_HOST, _USER, _PASSWORD and _NAME, with optional
//     _PORT (default 5432) and _SSLMODE.
//
// A full URL always takes precedence over the parts.
func ConnectionURL(variable string, getenv func(string) string) string {
	prefix := strings.TrimSuffix(variable, "_URL")
	password := getenv(prefix + "_PASSWORD")
	if raw := getenv(variable); raw != "" {
		if password == "" {
			return raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.User == nil {
			return raw
		}
		u.User = url.UserPassword(u.User.Username(), password)
		return u.String()
	}
	host := getenv(prefix + "_HOST")
	if host == "" {
		return ""
	}
	port := getenv(prefix + "_PORT")
	if port == "" {
		port = "5432"
	}
	u := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(getenv(prefix+"_USER"), password),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + getenv(prefix+"_NAME"),
	}
	if mode := getenv(prefix + "_SSLMODE"); mode != "" {
		u.RawQuery = url.Values{"sslmode": {mode}}.Encode()
	}
	return u.String()
}
