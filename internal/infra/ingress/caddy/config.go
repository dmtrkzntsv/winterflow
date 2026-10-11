// Package caddy embeds Caddy v2 as the agent's ingress: per-app domains from
// each app's committed config.json are merged into one Caddy JSON config.
// BuildConfig is a pure function so the whole translation is golden-testable.
package caddy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// AppIngress is one app's committed ingress plus its display name (from the
// same config.json), which names the app in the domain delete refusal.
type AppIngress struct {
	AppID   string
	Name    string
	Ingress model.Ingress
}

// label is how messages name the app: its display name, else its id.
func (a AppIngress) label() string {
	if a.Name != "" {
		return a.Name
	}
	return a.AppID
}

type Options struct {
	HTTPPort   int
	HTTPSPort  int
	ACMEEmail  string
	StorageDir string
	LogLevel   string
	// RateLimitRPS/RateLimitBurst configure the predefined per-client-IP
	// throttle in front of every route (0 rps disables it). A ceiling on
	// request fan-out so traffic spikes answer 429 instead of saturating the
	// host.
	RateLimitRPS   float64
	RateLimitBurst int
	// TrustedProxies (INGRESS_TRUSTED_PROXIES) are the CIDRs or IPs whose
	// CF-Connecting-IP / X-Forwarded-For headers Caddy believes for the
	// client IP the throttle keys on. Invalid entries are dropped with a
	// warning (fail closed: never trusted).
	TrustedProxies []string
}

// --- minimal typed mirror of Caddy's documented JSON config ---------------
// Only the fields we emit. Values arrive via json.Marshal of typed fields —
// user strings can never alter the config structure.

type caddyConfig struct {
	Admin   *adminConfig   `json:"admin,omitempty"`
	Logging *loggingConfig `json:"logging,omitempty"`
	Storage map[string]any `json:"storage,omitempty"`
	Apps    map[string]any `json:"apps"`
}

type adminConfig struct {
	Disabled bool `json:"disabled"`
}

type loggingConfig struct {
	Logs map[string]logConfig `json:"logs"`
}

type logConfig struct {
	Level string `json:"level"`
}

type httpApp struct {
	HTTPPort  int                    `json:"http_port,omitempty"`
	HTTPSPort int                    `json:"https_port,omitempty"`
	Servers   map[string]*httpServer `json:"servers"`
}

type httpServer struct {
	Listen []string        `json:"listen"`
	Routes []route         `json:"routes"`
	Logs   *map[string]any `json:"logs,omitempty"` // presence toggles access logs
	// Connection hygiene: slow-header and idle connections must not pile up
	// on a small host. Durations are Caddy duration strings.
	ReadHeaderTimeout string `json:"read_header_timeout,omitempty"`
	IdleTimeout       string `json:"idle_timeout,omitempty"`
	// Client IP behind trusted proxies: for a request whose TCP peer is in
	// TrustedProxies, Caddy takes the client IP (its client_ip var, which
	// the throttle keys on) from ClientIPHeaders, right to left, skipping
	// trusted hops (strict); anyone else's headers are ignored.
	TrustedProxies       *ipSource `json:"trusted_proxies,omitempty"`
	ClientIPHeaders      []string  `json:"client_ip_headers,omitempty"`
	TrustedProxiesStrict int       `json:"trusted_proxies_strict,omitempty"`
}

// ipSource is Caddy's http.ip_sources.static module.
type ipSource struct {
	Source string   `json:"source"`
	Ranges []string `json:"ranges"`
}

// clientIPHeaders are read in this order: Cloudflare's header, then the
// generic one.
var clientIPHeaders = []string{"CF-Connecting-IP", "X-Forwarded-For"}

type route struct {
	Match  []matcher        `json:"match,omitempty"`
	Handle []map[string]any `json:"handle"`
}

type matcher struct {
	Host []string `json:"host,omitempty"`
	Path []string `json:"path,omitempty"`
	// Not excludes requests any of these matcher sets match.
	Not []matcher `json:"not,omitempty"`
}

type tlsApp struct {
	Automation   *tlsAutomation   `json:"automation,omitempty"`
	Certificates *tlsCertificates `json:"certificates,omitempty"`
}

// tlsCertificates loads uploaded (certificate-mode) PEM pairs from disk.
// Automatic HTTPS skips every name a loaded certificate covers.
type tlsCertificates struct {
	LoadFiles []certKeyFiles `json:"load_files"`
}

type certKeyFiles struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

type tlsAutomation struct {
	Policies []tlsPolicy `json:"policies"`
}

type tlsPolicy struct {
	Subjects []string         `json:"subjects"`
	Issuers  []map[string]any `json:"issuers"`
}

// ---------------------------------------------------------------------------

// mapLogLevel translates winterflow's LOG_LEVEL vocabulary to zap's.
func mapLogLevel(l string) string {
	switch strings.ToLower(l) {
	case "debug":
		return "DEBUG"
	case "warn", "warning":
		return "WARN"
	case "error":
		return "ERROR"
	default:
		return "INFO"
	}
}

func proxyRoute(host string, port int) route {
	return route{
		Match: []matcher{{Host: []string{host}}},
		Handle: []map[string]any{{
			"handler":   "reverse_proxy",
			"upstreams": []map[string]any{{"dial": "127.0.0.1:" + strconv.Itoa(port)}},
		}},
	}
}

func redirectRoute(host, path, location string, code int) route {
	m := matcher{Host: []string{host}}
	if path != "" {
		m.Path = []string{path}
	}
	return route{
		Match: []matcher{m},
		Handle: []map[string]any{{
			"handler":     "static_response",
			"status_code": strconv.Itoa(code),
			"headers":     http.Header{"Location": []string{location}},
		}},
	}
}

// notFoundRoute answers 404 for every host under a registration that no app
// route claimed (it sits after all app routes on its server).
func notFoundRoute(m matcher) route {
	return route{
		Match: []matcher{m},
		Handle: []map[string]any{{
			"handler":     "static_response",
			"status_code": "404",
			"headers":     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
			"body":        "404 not found: no app is served on this hostname\n",
		}},
	}
}

// catchAllRoutes are the 404 routes for hosts no app claims: one per off
// registration (HTTP server) and per certificate registration (HTTPS server,
// where the loaded certificate completes the handshake first). Auto
// registrations get none — an unknown host there has no certificate, so its
// handshake fails before any route could answer. A wildcard's catch-all
// leaves out the exact registrations under it: those win their host and may
// sit on the other server.
func catchAllRoutes(regs []domains.Registration) (httpRoutes, httpsRoutes []route) {
	for _, r := range regs {
		if r.TLS == model.DomainTLSAuto {
			continue
		}
		m := matcher{Host: []string{r.Name}}
		if model.IsWildcardDomain(r.Name) {
			var exact []string
			for _, o := range regs {
				if !model.IsWildcardDomain(o.Name) && model.DomainCovers(r.Name, o.Name) {
					exact = append(exact, o.Name)
				}
			}
			if len(exact) > 0 {
				m.Not = []matcher{{Host: exact}}
			}
		}
		if r.TLS == model.DomainTLSOff {
			httpRoutes = append(httpRoutes, notFoundRoute(m))
		} else {
			httpsRoutes = append(httpsRoutes, notFoundRoute(m))
		}
	}
	return httpRoutes, httpsRoutes
}

// throttle is the predefined rate-limit route: non-terminal, first on both
// servers, passing allowed requests through to the routes below. ok is
// false when it is disabled (0 rps).
func throttle(opts Options) (rt route, ok bool) {
	if opts.RateLimitRPS <= 0 {
		return route{}, false
	}
	return route{Handle: []map[string]any{{
		"handler": "winterflow_rate_limit",
		"rps":     opts.RateLimitRPS,
		"burst":   opts.RateLimitBurst,
	}}}, true
}

// trustedRanges splits INGRESS_TRUSTED_PROXIES entries into the CIDRs/IPs
// Caddy accepts (normalized to prefixes) and the ones it would reject.
func trustedRanges(entries []string) (valid, bad []string) {
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if p, err := caddyhttp.CIDRExpressionToPrefix(e); err == nil {
			valid = append(valid, p.Masked().String())
		} else {
			bad = append(bad, e)
		}
	}
	return valid, bad
}

// BuildConfig merges per-app ingress fragments into one Caddy config, placing
// each hostname by the TLS mode of the registration that wins it (exact over
// wildcard): auto and certificate on the HTTPS server, off on the HTTP
// server; only auto hosts are ACME subjects. A hostname no registration
// covers is excluded with a warning (fail closed). Fragment validation
// failures and cross-app duplicates are warnings too, never errors: one
// app's bad routing must not take down another's. Apps are processed in
// sorted-AppID order so duplicate resolution is deterministic.
func BuildConfig(apps []AppIngress, regs []domains.Registration, opts Options) ([]byte, []string, error) {
	httpPort, httpsPort := opts.HTTPPort, opts.HTTPSPort
	if httpPort == 0 {
		httpPort = 80
	}
	if httpsPort == 0 {
		httpsPort = 443
	}

	sorted := make([]AppIngress, len(apps))
	copy(sorted, apps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AppID < sorted[j].AppID })

	var warnings []string
	claimed := map[string]string{} // domain -> appID that won it
	httpsRoutes := []route{}
	httpRoutes := []route{}
	var acmeSubjects []string

	for _, a := range sorted {
		if err := a.Ingress.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("app %s: ingress excluded: %v", a.AppID, err))
			continue
		}
		// mine: the TLS mode of each hostname this app claims (its winning
		// registration's). On cross-app duplicates the app keeps its other
		// domains — only the contested hostname is dropped.
		mine := map[string]model.DomainTLS{}
		for _, d := range a.Ingress.DomainNames() {
			win, ok := model.Winning(regs, d)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s is not covered by a domain on this server, skipping", a.AppID, d))
				continue
			}
			if owner, taken := claimed[d]; taken {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s already claimed by app %s, skipping", a.AppID, d, owner))
				continue
			}
			claimed[d] = a.AppID
			mine[d] = win.TLS
		}
		// place puts a route for one of this app's hosts on the server its
		// mode selects; an auto host serving content is an ACME subject.
		place := func(host string, rt route, subject bool) {
			mode, ok := mine[host]
			switch {
			case !ok:
			case mode == model.DomainTLSOff:
				httpRoutes = append(httpRoutes, rt)
			default:
				httpsRoutes = append(httpsRoutes, rt)
				if subject && mode == model.DomainTLSAuto {
					acmeSubjects = append(acmeSubjects, host)
				}
			}
		}

		// Path rules first: they must match before their domain's main route.
		for _, r := range a.Ingress.Redirects {
			if r.Path != "" {
				place(r.Domain, redirectRoute(r.Domain, r.Path, r.To, r.Code), false)
			}
		}
		for _, d := range a.Ingress.Domains {
			place(d.Domain, proxyRoute(d.Domain, d.UpstreamPort), true)
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path == "" {
				// Domain-level redirect preserves the request URI. The
				// {http.request.uri} placeholder is a fixed literal appended
				// to a validated absolute URL — not user templating.
				place(r.Domain, redirectRoute(r.Domain, "", r.To+"{http.request.uri}", r.Code), true)
			}
		}
	}

	httpCatchAll, httpsCatchAll := catchAllRoutes(regs)
	httpRoutes = append(httpRoutes, httpCatchAll...)
	httpsRoutes = append(httpsRoutes, httpsCatchAll...)
	if rt, ok := throttle(opts); ok {
		httpsRoutes = append([]route{rt}, httpsRoutes...)
		httpRoutes = append([]route{rt}, httpRoutes...)
	}

	trusted, bad := trustedRanges(opts.TrustedProxies)
	for _, b := range bad {
		warnings = append(warnings, fmt.Sprintf("INGRESS_TRUSTED_PROXIES: ignoring %q: not an IP or CIDR", b))
	}

	httpsServer := &httpServer{Listen: []string{":" + strconv.Itoa(httpsPort)}, Routes: httpsRoutes}
	// Servers listening only on the HTTP port are exempt from automatic
	// HTTPS: no certs, no redirects for their hosts. Caddy appends its own
	// HTTP->HTTPS redirects for the HTTPS server's hosts to this server.
	httpServerCfg := &httpServer{Listen: []string{":" + strconv.Itoa(httpPort)}, Routes: httpRoutes}
	for _, srv := range []*httpServer{httpsServer, httpServerCfg} {
		srv.ReadHeaderTimeout = "10s"
		srv.IdleTimeout = "5m"
		if len(trusted) > 0 {
			srv.TrustedProxies = &ipSource{Source: "static", Ranges: trusted}
			srv.ClientIPHeaders = clientIPHeaders
			srv.TrustedProxiesStrict = 1
		}
	}
	if strings.EqualFold(opts.LogLevel, "debug") {
		access := map[string]any{}
		httpsServer.Logs = &access
		httpServerCfg.Logs = &access
	}

	appsCfg := map[string]any{
		"http": httpApp{
			HTTPPort:  httpPort,
			HTTPSPort: httpsPort,
			Servers:   map[string]*httpServer{"https": httpsServer, "http": httpServerCfg},
		},
	}

	var tlsCfg tlsApp
	if len(acmeSubjects) > 0 {
		issuer := map[string]any{"module": "acme"}
		if opts.ACMEEmail != "" {
			issuer["email"] = opts.ACMEEmail
		}
		tlsCfg.Automation = &tlsAutomation{Policies: []tlsPolicy{{
			Subjects: acmeSubjects,
			Issuers:  []map[string]any{issuer},
		}}}
	}
	var loadFiles []certKeyFiles
	for _, r := range regs {
		if r.TLS == model.DomainTLSCertificate {
			loadFiles = append(loadFiles, certKeyFiles{Certificate: r.CertFile, Key: r.KeyFile})
		}
	}
	if len(loadFiles) > 0 {
		tlsCfg.Certificates = &tlsCertificates{LoadFiles: loadFiles}
	}
	if tlsCfg.Automation != nil || tlsCfg.Certificates != nil {
		appsCfg["tls"] = tlsCfg
	}

	cfg := caddyConfig{
		Admin:   &adminConfig{Disabled: true},
		Logging: &loggingConfig{Logs: map[string]logConfig{"default": {Level: mapLogLevel(opts.LogLevel)}}},
		Storage: map[string]any{"module": "file_system", "root": opts.StorageDir},
		Apps:    appsCfg,
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, warnings, err
	}
	return raw, warnings, nil
}
