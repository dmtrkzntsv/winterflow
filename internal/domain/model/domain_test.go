package model

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateDomainName(t *testing.T) {
	for _, ok := range []string{"twillingate.app", "app.twillingate.app", "*.twillingate.app", "*.eu.twillingate.app", "localhost", "*.test"} {
		if err := ValidateDomainName(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "*", "*.", "**.x.app", "a.*.x.app", "*x.app", "x.*", "Twillingate.app", "*.Twillingate.app",
		"-x.app", "x..app", "x.app.", "x.app:443", "https://x.app", "*.*.x.app", strings.Repeat("a", 250) + ".app"} {
		if err := ValidateDomainName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestDomainCovers(t *testing.T) {
	cases := []struct {
		name, host string
		want       bool
	}{
		{"twillingate.app", "twillingate.app", true},
		{"twillingate.app", "a.twillingate.app", false},
		{"*.twillingate.app", "a.twillingate.app", true},
		{"*.twillingate.app", "twillingate.app", false},     // not the suffix itself
		{"*.twillingate.app", "a.b.twillingate.app", false}, // one label only
		{"*.twillingate.app", ".twillingate.app", false},    // empty label
		{"*.twillingate.app", "a.twillingate.apps", false},
		{"*.twillingate.app", "atwillingate.app", false},
		{"*.eu.twillingate.app", "x.eu.twillingate.app", true},
		{"*.Twillingate.App", "A.twillingate.app", true}, // case-insensitive
		{"TWILLINGATE.app", "twillingate.APP", true},
	}
	for _, tc := range cases {
		if got := DomainCovers(tc.name, tc.host); got != tc.want {
			t.Errorf("DomainCovers(%q, %q) = %v, want %v", tc.name, tc.host, got, tc.want)
		}
	}
}

func TestWinningDomain(t *testing.T) {
	names := []string{"*.twillingate.app", "app.twillingate.app", "twillingate.app", "*.eu.twillingate.app"}
	cases := []struct {
		host, want string
		ok         bool
	}{
		{"app.twillingate.app", "app.twillingate.app", true}, // exact wins over the wildcard
		{"x.twillingate.app", "*.twillingate.app", true},
		{"twillingate.app", "twillingate.app", true},
		{"x.eu.twillingate.app", "*.eu.twillingate.app", true},
		{"a.b.twillingate.app", "", false},
		{"example.com", "", false},
	}
	for _, tc := range cases {
		got, ok := WinningDomain(names, tc.host)
		if got != tc.want || ok != tc.ok {
			t.Errorf("WinningDomain(%q) = %q, %v; want %q, %v", tc.host, got, ok, tc.want, tc.ok)
		}
	}
	// Order does not matter: the exact name wins even listed after.
	if got, _ := WinningDomain([]string{"app.twillingate.app", "*.twillingate.app"}, "app.twillingate.app"); got != "app.twillingate.app" {
		t.Errorf("exact listed first: got %q", got)
	}
	if _, ok := WinningDomain(nil, "x.app"); ok {
		t.Error("no registrations must cover nothing")
	}
}

func TestNotCoveredError(t *testing.T) {
	err := NotCoveredError("shop.twillingate.app")
	if !errors.Is(err, ErrDomainNotCovered) {
		t.Fatalf("not wrapped: %v", err)
	}
	want := "shop.twillingate.app is not covered by a domain registered on this server — register shop.twillingate.app or *.twillingate.app"
	if err.Error() != want {
		t.Fatalf("message = %q", err)
	}
	for _, host := range []string{"localhost", "twillingate.app"} {
		// An apex never falls under *.<tld>: suggest the apex alone.
		if got := NotCoveredError(host).Error(); !strings.HasSuffix(got, "register "+host) {
			t.Fatalf("%s hint = %q", host, got)
		}
	}
}
