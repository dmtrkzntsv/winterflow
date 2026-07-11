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
)

type AppIngress struct {
	AppID   string
	Ingress model.Ingress
}

type Options struct {
	HTTPPort   int
	HTTPSPort  int
	ACMEEmail  string
	StorageDir string
	LogLevel   string
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
}

type route struct {
	Match  []matcher        `json:"match,omitempty"`
	Handle []map[string]any `json:"handle"`
}

type matcher struct {
	Host []string `json:"host,omitempty"`
	Path []string `json:"path,omitempty"`
}

type tlsApp struct {
	Automation tlsAutomation `json:"automation"`
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

// BuildConfig merges per-app ingress fragments into one Caddy config.
// Fragment validation failures and cross-app duplicate domains produce
// warnings and exclusions, never errors: one app's bad routing must not take
// down another's. Apps are processed in sorted-AppID order so duplicate
// resolution is deterministic.
func BuildConfig(apps []AppIngress, opts Options) ([]byte, []string, error) {
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
	var sslSubjects []string

	for _, a := range sorted {
		if err := a.Ingress.Validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("app %s: ingress excluded: %v", a.AppID, err))
			continue
		}
		// Claim this app's hostnames; on cross-app duplicates the app keeps
		// its other domains — only the contested hostname is dropped.
		mine := map[string]bool{}
		for _, d := range a.Ingress.DomainNames() {
			if owner, taken := claimed[d]; taken {
				warnings = append(warnings, fmt.Sprintf("app %s: domain %s already claimed by app %s, skipping", a.AppID, d, owner))
				continue
			}
			claimed[d] = a.AppID
			mine[d] = true
		}

		// Path rules first (must match before their domain's main route);
		// group them per target list depending on the parent domain's SSL.
		sslOf := map[string]bool{}
		for _, d := range a.Ingress.Domains {
			sslOf[d.Domain] = d.SSL
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path == "" {
				sslOf[r.Domain] = r.SSL
				continue
			}
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path == "" || !mine[r.Domain] {
				continue
			}
			rt := redirectRoute(r.Domain, r.Path, r.To, r.Code)
			if sslOf[r.Domain] {
				httpsRoutes = append(httpsRoutes, rt)
			} else {
				httpRoutes = append(httpRoutes, rt)
			}
		}
		for _, d := range a.Ingress.Domains {
			if !mine[d.Domain] {
				continue
			}
			rt := proxyRoute(d.Domain, d.UpstreamPort)
			if d.SSL {
				httpsRoutes = append(httpsRoutes, rt)
				sslSubjects = append(sslSubjects, d.Domain)
			} else {
				httpRoutes = append(httpRoutes, rt)
			}
		}
		for _, r := range a.Ingress.Redirects {
			if r.Path != "" || !mine[r.Domain] {
				continue
			}
			// Domain-level redirect preserves the request URI. The
			// {http.request.uri} placeholder is a fixed literal appended to a
			// validated absolute URL — not user templating.
			rt := redirectRoute(r.Domain, "", r.To+"{http.request.uri}", r.Code)
			if r.SSL {
				httpsRoutes = append(httpsRoutes, rt)
				sslSubjects = append(sslSubjects, r.Domain)
			} else {
				httpRoutes = append(httpRoutes, rt)
			}
		}
	}

	httpsServer := &httpServer{Listen: []string{":" + strconv.Itoa(httpsPort)}, Routes: httpsRoutes}
	// Servers listening only on the HTTP port are exempt from automatic
	// HTTPS: no certs, no redirects for their hosts. Caddy appends its own
	// HTTP->HTTPS redirects for the ssl hosts to this server.
	httpServerCfg := &httpServer{Listen: []string{":" + strconv.Itoa(httpPort)}, Routes: httpRoutes}
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

	if len(sslSubjects) > 0 {
		issuer := map[string]any{"module": "acme"}
		if opts.ACMEEmail != "" {
			issuer["email"] = opts.ACMEEmail
		}
		appsCfg["tls"] = tlsApp{Automation: tlsAutomation{Policies: []tlsPolicy{{
			Subjects: sslSubjects,
			Issuers:  []map[string]any{issuer},
		}}}}
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
