package main

import (
	"net"
	"slices"
	"testing"
)

// One SERVFAIL or lost packet used to take the wildcard out of the router's
// config, and every preview then asked for a certificate of its own.
func TestWildcardSurvivesFailedProbes(t *testing.T) {
	var w wildcard
	step := func(what string, delegated, stored, wantChanged, wantReady bool) {
		t.Helper()
		if changed := w.record(delegated, stored); changed != wantChanged || w.ready.Load() != wantReady {
			t.Fatalf("%s: changed %v, ready %v; want changed %v, ready %v", what, changed, w.ready.Load(), wantChanged, wantReady)
		}
	}

	// At boot the network may not be up yet, and nothing is stored.
	step("first probe fails", false, false, false, false)
	step("the delegation is seen", true, false, true, true)
	for range wildcardFailures - 1 {
		step("a probe fails", false, false, false, true)
	}
	step("a probe works again", true, false, false, true)
	for range wildcardFailures - 1 {
		step("a probe fails after the count started over", false, false, false, true)
	}
	step("the fifth failure in a row", false, false, true, false)
	step("still failing", false, false, false, false)
	step("the delegation is back", true, false, true, true)

	// A stored certificate keeps the wildcard however long DNS fails, also
	// from boot, until it expires.
	for range 2 * wildcardFailures {
		step("a probe fails with a certificate stored", false, true, false, true)
	}
	step("the stored certificate expired", false, false, true, false)
	var booted wildcard
	if !booted.record(false, true) || !booted.ready.Load() {
		t.Fatal("a stored certificate wasn't used at boot while DNS didn't answer")
	}
}

// The fallback binds the machine's own addresses. Behind NAT the public IP
// isn't one of them, so binding that failed and the wildcard was never issued.
func TestChallengeAddrs(t *testing.T) {
	var local []net.Addr
	for _, cidr := range []string{
		"127.0.0.1/8", "::1/128", "10.0.3.17/24", "203.0.113.7/24", "169.254.169.254/16",
		"fe80::1c2b:3aff:fe4d:5e6f/64", "2001:db8::5/64", "172.17.0.1/16",
	} {
		ip, ipn, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		local = append(local, &net.IPNet{IP: ip, Mask: ipn.Mask})
	}
	local = append(local, &net.IPAddr{IP: net.ParseIP("198.51.100.9")})
	want := []string{"10.0.3.17:53", "203.0.113.7:53", "[2001:db8::5]:53", "172.17.0.1:53", "198.51.100.9:53"}
	if got := challengeAddrs(local, "53"); !slices.Equal(got, want) {
		t.Fatalf("addresses to bind = %v, want %v", got, want)
	}
	if got := challengeAddrs(nil, "53"); len(got) != 0 {
		t.Fatalf("addresses to bind without interfaces = %v", got)
	}
}
