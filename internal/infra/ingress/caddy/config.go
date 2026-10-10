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

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
)

// AppIngress is one app's committed ingress plus its display name (from the
// same config.json), used to name apps in the domain delete refusal.
type AppIngress struct {
	AppID   string
	Name    string
	Ingress model.Ingress
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
	// AutomaticHTTPS lists catch-all names Caddy must not order certs for.
	AutomaticHTTPS *autoHTTPS `json:"automatic_https,omitempty"`
	// Connection hygiene: slow-header and idle connections must not pile up
	// on a small host. Durations are Caddy duration strings.
	ReadHeaderTimeout string `json:"read_header_timeout,omitempty"`
	IdleTimeout       string `json:"idle_timeout,omitempty"`
}

type autoHTTPS struct {
	SkipCertificates []string `json:"skip_certificates,omitempty"`
}

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

	names := make([]string, len(regs))
	modes := map[string]command.DomainTLS{}
	for i, r := range regs {
		names[i] = r.Name
		modes[r.Name] = r.TLS
	}

	httpsRoutes := []route{}
	httpRoutes := []route{}
	var sslSubjects []string

	for _, a := range sorted {
		if err := a.Ingress.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("app %s: ingress excluded: %v", a.AppID, err))
			continue
		}
		// Claim this app's covered hostnames; on cross-app duplicates the
		// app keeps its other domains — only the contested hostname is
		// dropped. modeOf doubles as "mine": the TLS mode of each claimed
		// host's winning registration.
		modeOf := map[string]command.DomainTLS{}
		for _, d := range a.Ingress.DomainNames() {
			win, ok := model.WinningDomain(names, d)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s is not covered by a registered domain, skipping", a.AppID, d))
				continue
			}
			if owner, taken := claimed[d]; taken {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s already claimed by app %s, skipping", a.AppID, d, owner))
				continue
			}
			claimed[d] = a.AppID
			modeOf[d] = modes[win]
		}
		// place puts a route on the server its host's mode selects; auto
		// hosts also become ACME subjects.
		place := func(host string, rt route, subject bool) {
			switch modeOf[host] {
			case command.DomainTLSOff:
				httpRoutes = append(httpRoutes, rt)
			case command.DomainTLSAuto:
				httpsRoutes = append(httpsRoutes, rt)
				if subject {
					sslSubjects = append(sslSubjects, host)
				}
			default: // certificate
				httpsRoutes = append(httpsRoutes, rt)
			}
		}

		// Path rules first: they must match before their domain's main route.
		for _, r := range a.Ingress.Redirects {
			if _, mine := modeOf[r.Domain]; r.Path == "" || !mine {
				continue
			}
			place(r.Domain, redirectRoute(r.Domain, r.Path, r.To, r.Code), false)
		}
		for _, d := range a.Ingress.Domains {
			if _, mine := modeOf[d.Domain]; !mine {
				continue
			}
			place(d.Domain, proxyRoute(d.Domain, d.UpstreamPort), true)
		}
		for _, r := range a.Ingress.Redirects {
			if _, mine := modeOf[r.Domain]; r.Path != "" || !mine {
				continue
			}
			// Domain-level redirect preserves the request URI. The
			// {http.request.uri} placeholder is a fixed literal appended to a
			// validated absolute URL — not user templating.
			place(r.Domain, redirectRoute(r.Domain, "", r.To+"{http.request.uri}", r.Code), true)
		}
	}

	// Catch-all 404 per registration, after every app route. A wildcard's
	// catch-all leaves out the exact registrations under it: those win
	// their host and may sit on the other server. Auto catch-alls must not
	// make Caddy order a certificate (a wildcard can't pass HTTP-01, an
	// unused name may not even point here), so their names are skipped for
	// certificates unless an app route serves that very host.
	var skipCerts []string
	var loadFiles []certKeyFiles
	for _, r := range regs {
		m := matcher{Host: []string{r.Name}}
		if model.IsWildcardDomain(r.Name) {
			var exact []string
			for _, n := range names {
				if !model.IsWildcardDomain(n) && model.DomainCovers(r.Name, n) {
					exact = append(exact, n)
				}
			}
			if len(exact) > 0 {
				m.Not = []matcher{{Host: exact}}
			}
		}
		switch r.TLS {
		case command.DomainTLSOff:
			httpRoutes = append(httpRoutes, notFoundRoute(m))
		case command.DomainTLSCertificate:
			httpsRoutes = append(httpsRoutes, notFoundRoute(m))
			loadFiles = append(loadFiles, certKeyFiles{Certificate: r.CertFile, Key: r.KeyFile})
		default: // auto
			httpsRoutes = append(httpsRoutes, notFoundRoute(m))
			if _, served := claimed[r.Name]; !served {
				skipCerts = append(skipCerts, r.Name)
			}
		}
	}

	// Predefined throttle: a non-terminal first route on both servers — the
	// middleware passes allowed requests through to the routes below.
	if opts.RateLimitRPS > 0 {
		throttle := route{Handle: []map[string]any{{
			"handler": "winterflow_rate_limit",
			"rps":     opts.RateLimitRPS,
			"burst":   opts.RateLimitBurst,
		}}}
		httpsRoutes = append([]route{throttle}, httpsRoutes...)
		httpRoutes = append([]route{throttle}, httpRoutes...)
	}

	httpsServer := &httpServer{Listen: []string{":" + strconv.Itoa(httpsPort)}, Routes: httpsRoutes}
	if len(skipCerts) > 0 {
		httpsServer.AutomaticHTTPS = &autoHTTPS{SkipCertificates: skipCerts}
	}
	// Servers listening only on the HTTP port are exempt from automatic
	// HTTPS: no certs, no redirects for their hosts. Caddy appends its own
	// HTTP->HTTPS redirects for the ssl hosts to this server.
	httpServerCfg := &httpServer{Listen: []string{":" + strconv.Itoa(httpPort)}, Routes: httpRoutes}
	for _, srv := range []*httpServer{httpsServer, httpServerCfg} {
		srv.ReadHeaderTimeout = "10s"
		srv.IdleTimeout = "5m"
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
	if len(sslSubjects) > 0 {
		issuer := map[string]any{"module": "acme"}
		if opts.ACMEEmail != "" {
			issuer["email"] = opts.ACMEEmail
		}
		tlsCfg.Automation = &tlsAutomation{Policies: []tlsPolicy{{
			Subjects: sslSubjects,
			Issuers:  []map[string]any{issuer},
		}}}
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
