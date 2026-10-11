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

// DomainTLS is how TLS works for hostnames under a registration.
type DomainTLS string

const (
	// DomainTLSAuto: the agent obtains one ACME (HTTP-01) cert per app host.
	DomainTLSAuto DomainTLS = "auto"
	// DomainTLSCertificate: an uploaded PEM cert + key serves every host.
	DomainTLSCertificate DomainTLS = "certificate"
	// DomainTLSOff: TLS ends in front (tunnel, proxy); plain HTTP only.
	DomainTLSOff DomainTLS = "off"
)

// Valid reports whether t is one of the three modes.
func (t DomainTLS) Valid() bool {
	return t == DomainTLSAuto || t == DomainTLSCertificate || t == DomainTLSOff
}

// RegisteredDomain is a name an agent accepts ingress hostnames under, with
// its TLS mode. NotAfter and Issuer (the issuer's CN) describe the stored
// certificate in certificate mode. The agent's domains/ dir is the source of
// truth; the API's server_domains index mirrors it.
type RegisteredDomain struct {
	Name     string     `json:"name"`
	TLS      DomainTLS  `json:"tls"`
	NotAfter *time.Time `json:"not_after,omitempty"`
	Issuer   string     `json:"issuer,omitempty"`
}

// RegistrationName makes RegisteredDomain (and every type embedding it) a
// Registered, for Winning.
func (d RegisteredDomain) RegistrationName() string { return d.Name }

// Registered is anything carrying a registration name.
type Registered interface{ RegistrationName() string }

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

// Winning returns the registration among regs that covers host: an exact
// name wins over a wildcard. Only "*.<parent of host>" can be the wildcard,
// so two wildcards never compete for one host.
func Winning[T Registered](regs []T, host string) (T, bool) {
	var wildcard T
	found := false
	for _, r := range regs {
		n := r.RegistrationName()
		if !DomainCovers(n, host) {
			continue
		}
		if !IsWildcardDomain(n) {
			return r, true
		}
		wildcard, found = r, true
	}
	return wildcard, found
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
