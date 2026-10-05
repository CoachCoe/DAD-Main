package main

import (
	"net/netip"
	"slices"
	"testing"
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
