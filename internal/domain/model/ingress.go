package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Ingress-related sentinel errors, matched by the HTTP layer to map failures
// to 400s with the underlying message.
var (
	ErrIngressInvalid = errors.New("invalid ingress")
	ErrDomainTaken    = errors.New("domain already in use")
)

// Ingress is the app's routing config, stored under the "ingress" key of the
// committed config.json. The agent filesystem is the source of truth; the API
// mirrors route/redirect domains into the app_domains DB index.
type Ingress struct {
	Domains   []IngressDomain   `json:"domains"`
	Redirects []IngressRedirect `json:"redirects"`
}

// IngressDomain maps a hostname to a host port the app's compose file
// publishes (ideally bound to 127.0.0.1). SSL=true means an ACME cert plus
// the automatic HTTP->HTTPS redirect; false means plain HTTP on port 80 only.
type IngressDomain struct {
	Domain       string `json:"domain"`
	UpstreamPort int    `json:"upstream_port"`
	SSL          bool   `json:"ssl"`
}

// IngressRedirect is either a domain-level redirect (Path == "", preserves the
// request URI, needs its own SSL choice because serving https://source
// requires a cert) or a path rule (Path != "", scoped to a domain the app
// already claims, redirects the matched prefix to the exact To URL).
type IngressRedirect struct {
	Domain string `json:"domain"`
	Path   string `json:"path,omitempty"`
	To     string `json:"to"`
	Code   int    `json:"code"`
	SSL    bool   `json:"ssl,omitempty"`
}

// hostnameRe is a strict lowercase RFC-1123 hostname: labels of [a-z0-9-],
// no leading/trailing hyphen, at least one label, no wildcard/port/scheme.
var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func validHostname(h string) bool {
	return h != "" && len(h) <= 253 && hostnameRe.MatchString(h)
}

var validRedirectCodes = map[int]bool{301: true, 302: true, 307: true, 308: true}

// Validate enforces the shared rulebook (UI, API, and agent all rely on it).
// Strict typing here is the injection defense: every value later lands in a
// typed JSON field of the generated Caddy config. One caveat: Caddy expands
// {...} placeholders in static_response header values (including Location)
// at request time regardless of typed JSON, so redirect targets and paths
// are additionally checked for braces below.
func (i *Ingress) Validate() error {
	seen := map[string]bool{}
	claim := func(domain, what string) error {
		if !validHostname(domain) {
			return fmt.Errorf("%s: %q is not a valid lowercase hostname", what, domain)
		}
		if seen[domain] {
			return fmt.Errorf("%s: duplicate domain %q within the app", what, domain)
		}
		seen[domain] = true
		return nil
	}

	for _, d := range i.Domains {
		if err := claim(d.Domain, "domain"); err != nil {
			return err
		}
		if d.UpstreamPort < 1 || d.UpstreamPort > 65535 {
			return fmt.Errorf("domain %q: upstream port %d out of range 1-65535", d.Domain, d.UpstreamPort)
		}
	}

	for _, r := range i.Redirects {
		u, err := url.Parse(r.To)
		if err != nil || !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("redirect for %q: target %q must be an absolute http(s) URL", r.Domain, r.To)
		}
		// Caddy's static_response handler runs every header VALUE (including
		// Location) through its placeholder replacer, expanding things like
		// {env.SECRET} or {http.request.*} before the response is sent. The
		// generator itself appends the one trusted literal
		// {http.request.uri} to domain-level redirects (see redirectRoute in
		// internal/infra/ingress/caddy/config.go); legitimate user input never
		// needs braces, so both are rejected outright to close off exfiltration
		// of process env vars or request internals via a crafted redirect target.
		if strings.ContainsAny(r.To, "{}") {
			return fmt.Errorf("redirect for %q: target must not contain { or }", r.Domain)
		}
		if strings.ContainsAny(r.Path, "{}") {
			return fmt.Errorf("redirect for %q: path must not contain { or }", r.Domain)
		}
		if !validRedirectCodes[r.Code] {
			return fmt.Errorf("redirect for %q: code %d not one of 301, 302, 307, 308", r.Domain, r.Code)
		}
		if r.Path == "" {
			// Domain-level redirect: claims its hostname like a route does.
			if err := claim(r.Domain, "redirect"); err != nil {
				return err
			}
			continue
		}
		// Path rule: must ride a domain this app already claims.
		if !strings.HasPrefix(r.Path, "/") {
			return fmt.Errorf("redirect path %q must start with /", r.Path)
		}
		if !seen[r.Domain] {
			return fmt.Errorf("path rule for %q: domain not claimed by this app (path rule domains must match a route or redirect domain listed above it)", r.Domain)
		}
	}
	return nil
}

// DomainNames returns the hostnames this ingress claims (routes + domain-level
// redirect sources, in declaration order). Path rules claim nothing new.
func (i *Ingress) DomainNames() []string {
	var out []string
	for _, d := range i.Domains {
		out = append(out, d.Domain)
	}
	for _, r := range i.Redirects {
		if r.Path == "" {
			out = append(out, r.Domain)
		}
	}
	return out
}

// ParseIngress extracts the optional "ingress" key from an app config blob.
// (nil, nil) when the key is absent — the feature is untouched and every
// caller must no-op.
func ParseIngress(configBlob []byte) (*Ingress, error) {
	var probe struct {
		Ingress *Ingress `json:"ingress"`
	}
	if err := json.Unmarshal(configBlob, &probe); err != nil {
		return nil, fmt.Errorf("parse app config: %w", err)
	}
	return probe.Ingress, nil
}

// DomainClaim names the app/server holding a domain — the payload of the
// cross-app conflict error and of the /domains/check endpoint.
type DomainClaim struct {
	Domain     string `json:"domain"`
	AppID      string `json:"app_id"`
	AppName    string `json:"app_name"`
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
}

// AppDomainInfo is the display slice of an app_domains row (app cards, chips).
type AppDomainInfo struct {
	Domain string `json:"domain"`
	SSL    bool   `json:"ssl"`
	Kind   string `json:"kind"` // "route" | "redirect"
}
