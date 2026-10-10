package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrDomainNotCovered marks a save whose ingress names a hostname no domain
// registered on the target server covers; the HTTP layer maps it to 400.
var ErrDomainNotCovered = errors.New("domain not covered")

// RegisteredDomain is one row of the API's server_domains index: a name an
// agent accepts ingress hostnames under, with its TLS mode ("auto",
// "certificate" or "off"). The agent's domains/ dir is the source of truth.
type RegisteredDomain struct {
	Name     string     `json:"name"`
	TLS      string     `json:"tls"`
	NotAfter *time.Time `json:"not_after,omitempty"`
	Issuer   string     `json:"issuer,omitempty"`
}

// IsWildcardDomain reports whether a registration name is a wildcard: "*."
// followed by the suffix it covers one label of.
func IsWildcardDomain(name string) bool { return strings.HasPrefix(name, "*.") }

// ValidateDomainName checks a registration name: an exact lowercase RFC-1123
// hostname (the ingress rule), or "*." plus one, where the "*" is the whole
// leftmost label.
func ValidateDomainName(name string) error {
	host := strings.TrimPrefix(name, "*.")
	if len(name) > 253 || !validHostname(host) {
		return fmt.Errorf("%q is not a valid domain: use a lowercase hostname like example.com, or *.example.com for every subdomain", name)
	}
	return nil
}

// DomainCovers reports whether the registration name covers host. An exact
// name covers only itself; "*.s" covers "<one label>.s" — not "s" itself and
// not "a.b.s". Comparison ignores case.
func DomainCovers(name, host string) bool {
	name, host = strings.ToLower(name), strings.ToLower(host)
	if !IsWildcardDomain(name) {
		return name == host
	}
	label, suffix, ok := strings.Cut(host, ".")
	return ok && label != "" && suffix == name[2:]
}

// WinningDomain returns the registration among names that covers host: an
// exact name wins over a wildcard. Only "*.<parent of host>" can be the
// wildcard, so two wildcards never compete for one host.
func WinningDomain(names []string, host string) (string, bool) {
	var wildcard string
	for _, n := range names {
		if !DomainCovers(n, host) {
			continue
		}
		if !IsWildcardDomain(n) {
			return n, true
		}
		wildcard = n
	}
	return wildcard, wildcard != ""
}

// notCoveredError is ErrDomainNotCovered with a message that stands on its
// own (the UI shows it as is).
type notCoveredError struct{ msg string }

func (e notCoveredError) Error() string { return e.msg }
func (e notCoveredError) Unwrap() error { return ErrDomainNotCovered }

// NotCoveredError explains how to cover host: register it, or the wildcard
// for its parent — unless that parent is a bare TLD (an apex like
// twillingate.app is never covered by *.twillingate.app, and *.app is no
// advice).
func NotCoveredError(host string) error {
	hint := host
	if _, parent, ok := strings.Cut(host, "."); ok && strings.Contains(parent, ".") {
		hint = host + " or *." + parent
	}
	return notCoveredError{fmt.Sprintf("%s is not covered by a domain registered on this server — register %s", host, hint)}
}
