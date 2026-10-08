package main

import (
	"cmp"
	"context"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"

	"impactgraph/donor-api/internal/dbroles"
	"impactgraph/donor-api/internal/dbroles/dbrolestest"
)

func TestTrustedProxiesAcceptsProxyNetworksAndRefusesOpenRanges(t *testing.T) {
	got, err := trustedProxies(" 172.30.42.10/32, ,10.0.0.0/8,fd00::/48")
	want := []netip.Prefix{netip.MustParsePrefix("172.30.42.10/32"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/48")}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, invalid := range []string{"0.0.0.0/0", "10.0.0.0/7", "::/0", "2001::/16", "not-a-cidr", "10.0.0.1"} {
		if _, err := trustedProxies(invalid); err == nil {
			t.Errorf("%q accepted", invalid)
		}
	}
}

func TestConfigureRejectsInvalidEnvironmentBeforeTheDatabase(t *testing.T) {
	// The database URL is unreachable: each case must fail on configuration first.
	base := map[string]string{"DONOR_API_DATABASE_URL": "postgresql://unreachable.invalid/donor"}
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"proxy range", map[string]string{"DONOR_TRUSTED_PROXY_CIDRS": "0.0.0.0/0"}},
		{"plain HTTP origin", map[string]string{"DONOR_PUBLIC_ORIGIN": "http://dad.example.org"}},
		{"origin with path", map[string]string{"DONOR_PUBLIC_ORIGIN": "https://dad.example.org/app"}},
		{"half of Stripe", map[string]string{"STRIPE_SECRET_KEY": "sk_test_123"}},
		{"retired staff key without a staff origin", map[string]string{"DONOR_STAFF_ENCRYPTION_KEY": "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA="}},
		{"plain HTTP staff origin", map[string]string{"DONOR_STAFF_ORIGIN": "http://staff.dad.example.org"}},
		{"staff origin with path", map[string]string{"DONOR_STAFF_ORIGIN": "https://staff.dad.example.org/staff"}},
		{"public origin in capitals", map[string]string{"DONOR_PUBLIC_ORIGIN": "https://DAD.example.org"}},
		{"public origin with its default port", map[string]string{"DONOR_PUBLIC_ORIGIN": "https://dad.example.org:443"}},
		{"staff origin in capitals", map[string]string{"DONOR_STAFF_ORIGIN": "https://Staff.dad.example.org"}},
		{"staff origin with its default port", map[string]string{"DONOR_STAFF_ORIGIN": "https://staff.dad.example.org:443"}},
		{"staff origin with a trailing slash", map[string]string{"DONOR_STAFF_ORIGIN": "https://staff.dad.example.org/"}},
		{"staff on the public host", map[string]string{"DONOR_PUBLIC_ORIGIN": "https://dad.example.org", "DONOR_STAFF_ORIGIN": "https://dad.example.org:8443"}},
		{"staff on a parent of the public host", map[string]string{"DONOR_PUBLIC_ORIGIN": "https://dad.example.org", "DONOR_STAFF_ORIGIN": "https://example.org"}},
		{"local staff on the public host", map[string]string{"DONOR_STAFF_ORIGIN": "http://localhost:3001"}},
		{"content key", map[string]string{"DONOR_CONTENT_ENCRYPTION_KEY": "short"}},
		{"retired content key", map[string]string{"DONOR_CONTENT_ENCRYPTION_KEY": "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDA=,short"}},
		{"partial SMTP", map[string]string{"DONOR_SMTP_HOST": "smtp.example.org"}},
		{"partial APNs", map[string]string{"DONOR_PUSH_APNS_TEAM_ID": "TEAM123456"}},
		{"unreadable FCM key", map[string]string{"DONOR_PUSH_FCM_KEY_FILE": "/nonexistent/firebase.json"}},
	} {
		getenv := func(name string) string { return cmp.Or(tc.env[name], base[name]) }
		_, release, err := configure(context.Background(), getenv)
		release()
		if err == nil || err.Error() == "donor database startup failed" {
			t.Errorf("%s: got %v, want a configuration error", tc.name, err)
		}
	}
}

func TestConfigureWithoutDatabaseServesHealthOnly(t *testing.T) {
	services, release, err := configure(context.Background(), func(string) string { return "" })
	defer release()
	if err != nil || services.Origin != "http://localhost:3000" || services.Ready != nil || services.Donor != nil || services.Staff != nil {
		t.Fatalf("got %+v, %v; want the local origin and no data services", services, err)
	}
}

func TestConfigureAcceptsAStaffHostBesideThePublicOne(t *testing.T) {
	for _, env := range []map[string]string{
		{"DONOR_STAFF_ORIGIN": "http://staff.localhost:3000"},
		{"DONOR_PUBLIC_ORIGIN": "https://dad.example.org", "DONOR_STAFF_ORIGIN": "https://staff.dad.example.org"},
		{"DONOR_PUBLIC_ORIGIN": "https://dad.example.org", "DONOR_STAFF_ORIGIN": "https://staff.example.org:8443"},
	} {
		services, release, err := configure(context.Background(), func(name string) string { return env[name] })
		release()
		if err != nil || services.StaffOrigin != env["DONOR_STAFF_ORIGIN"] {
			t.Fatalf("%v: got %q, %v", env, services.StaffOrigin, err)
		}
	}
}

func TestConfigureConnectsAsTheAPIRole(t *testing.T) {
	owner := os.Getenv("TEST_DATABASE_URL")
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"own login", map[string]string{"DONOR_API_DATABASE_URL": dbrolestest.URL(t, dbroles.API)}, ""},
		{"schema owner", map[string]string{"DONOR_API_DATABASE_URL": owner}, "can change the schema"},
		{"another role", map[string]string{"DONOR_API_DATABASE_URL": dbrolestest.URL(t, dbroles.Monitor)}, "not a member of dad_api"},
		{"local owner", map[string]string{"DONOR_GO_DATABASE_URL": owner, "DONOR_ALLOW_OWNER_CONNECTION": "true"}, ""},
	} {
		services, release, err := configure(context.Background(), func(name string) string { return tc.env[name] })
		release()
		if tc.want == "" && (err != nil || services.Ready == nil) {
			t.Errorf("%s: got %v, want the database services", tc.name, err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}
