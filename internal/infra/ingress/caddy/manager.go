package caddy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	caddy "github.com/caddyserver/caddy/v2"
	// Curated module set (NOT modules/standard): http server + static_response,
	// reverse proxy, TLS/ACME, file_system cert storage.
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls"
	_ "github.com/caddyserver/caddy/v2/modules/caddytls/standardstek"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"
)

// Manager owns the embedded Caddy instance. Ingress failures degrade ingress
// only: Start never fails the agent, Reload never fails a deploy.
type Manager struct {
	cfg *config.ServerConfig
	log *logger.Logger

	mu      sync.Mutex
	enabled bool
}

func NewManager(cfg *config.ServerConfig, log *logger.Logger) *Manager {
	return &Manager{cfg: cfg, log: log}
}

func (m *Manager) options() Options {
	return Options{
		HTTPPort:       m.cfg.GetIngressHTTPPort(),
		HTTPSPort:      m.cfg.GetIngressHTTPSPort(),
		ACMEEmail:      m.cfg.GetIngressACMEEmail(),
		StorageDir:     filepath.Join(m.cfg.GetAgentDataDir(), "caddy"),
		LogLevel:       m.cfg.GetLogLevel(),
		RateLimitRPS:   m.cfg.GetIngressRateLimitRPS(),
		RateLimitBurst: m.cfg.GetIngressRateLimitBurst(),
	}
}

// scanApps reads every committed config.json under apps-data and returns the
// parsed ingress fragments. Unreadable/unparseable apps are skipped with a
// warning — one broken app must not stop the scan.
func (m *Manager) scanApps() ([]AppIngress, []string) {
	appsDir := m.cfg.GetAppsDataDir()
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			m.log.Warn("ingress: scan apps-data", "error", err)
		}
		return nil, nil
	}
	var out []AppIngress
	var warnings []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appID := e.Name()
		raw, err := os.ReadFile(filepath.Join(appsDir, appID, ".winterflow", "config.json"))
		if err != nil {
			continue // app without a committed config yet
		}
		ing, err := model.ParseIngress(raw)
		if err != nil {
			warnings = append(warnings, "app "+appID+": unreadable config, ingress skipped: "+err.Error())
			continue
		}
		if ing == nil {
			continue
		}
		out = append(out, AppIngress{AppID: appID, Ingress: *ing})
	}
	return out, warnings
}

func (m *Manager) buildConfig() ([]byte, []string) {
	apps, scanWarnings := m.scanApps()
	cfg, buildWarnings, err := BuildConfig(apps, m.options())
	warnings := append(scanWarnings, buildWarnings...)
	if err != nil {
		// Marshal failure of our own structs: effectively impossible, but
		// degrade rather than propagate.
		warnings = append(warnings, "ingress: build config: "+err.Error())
		return nil, warnings
	}
	return cfg, warnings
}

// Start builds the initial config and starts Caddy synchronously so callers
// can read Enabled() for the agent's feature map. Ingress is skipped outright
// when INGRESS_ENABLED=false or its ports collide with the API's; a genuine
// start failure logs, leaves the manager disabled, and returns nil. All three
// paths keep the agent running — only ingress goes away.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.cfg.IsIngressEnabled() {
		m.log.Info("ingress disabled by config (INGRESS_ENABLED=false); its ports are left to another proxy",
			"http_port", m.cfg.GetIngressHTTPPort(),
			"https_port", m.cfg.GetIngressHTTPSPort())
		return nil
	}
	if err := m.checkPortSeparation(); err != nil {
		m.log.Warn("ingress disabled: "+err.Error(),
			"hint", "the reverse proxy and the winterflow API must own different ports")
		return nil
	}

	cfg, warnings := m.buildConfig()
	for _, w := range warnings {
		m.log.Warn("ingress", "warning", w)
	}
	if cfg == nil {
		return nil
	}
	if err := caddy.Load(cfg, true); err != nil {
		m.log.Warn("ingress disabled: "+m.startFailureReason(err), "error", err)
		return nil
	}
	m.enabled = true
	m.log.Info("ingress started",
		"http_port", m.cfg.GetIngressHTTPPort(),
		"https_port", m.cfg.GetIngressHTTPSPort())

	go func() {
		<-ctx.Done()
		if err := caddy.Stop(); err != nil {
			m.log.Warn("ingress: caddy stop", "error", err)
		}
	}()
	return nil
}

// Reload rebuilds from disk and hot-swaps the config. On a load failure the
// previous config keeps serving and the error is returned as a warning for
// the triggering command's response.
func (m *Manager) Reload(ctx context.Context) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled {
		return nil
	}
	cfg, warnings := m.buildConfig()
	if cfg == nil {
		return warnings
	}
	if err := caddy.Load(cfg, false); err != nil {
		m.log.Error("ingress: reload failed, previous config keeps serving", "error", err)
		warnings = append(warnings, "ingress reload failed (previous routing still active): "+err.Error())
	}
	return warnings
}

func (m *Manager) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

// checkPortSeparation rejects a config where the proxy would fight the
// winterflow API for a port. In standalone both listen in ONE process, so an
// overlap is not a race to lose but a guaranteed silent half-start: caddy
// takes whichever it binds first and the failure surfaces as a confusing
// address-in-use much later. API_PORT is only set where the API actually
// runs (standalone), so this is a no-op for a lone agent.
func (m *Manager) checkPortSeparation() error {
	apiPort, err := strconv.Atoi(strings.TrimSpace(m.cfg.GetApiPort()))
	if err != nil || apiPort <= 0 {
		return nil
	}
	for _, p := range []struct {
		name string
		port int
	}{
		{"INGRESS_HTTP_PORT", m.cfg.GetIngressHTTPPort()},
		{"INGRESS_HTTPS_PORT", m.cfg.GetIngressHTTPSPort()},
	} {
		if p.port == apiPort {
			return fmt.Errorf("%s (%d) collides with API_PORT (%d)", p.name, p.port, apiPort)
		}
	}
	return nil
}

// startFailureReason turns caddy's load error into the diagnosis an operator
// can act on. A taken port and a missing capability both surface as a listen
// error, but the fixes are opposite — blaming capabilities for an occupied
// port sends people down the wrong path.
func (m *Manager) startFailureReason(err error) string {
	if strings.Contains(err.Error(), "address already in use") {
		return fmt.Sprintf("port %d or %d is already taken by another process",
			m.cfg.GetIngressHTTPPort(), m.cfg.GetIngressHTTPSPort())
	}
	if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission denied") {
		return fmt.Sprintf("no permission to bind ports %d/%d (privileged ports need CAP_NET_BIND_SERVICE; the systemd unit grants it)",
			m.cfg.GetIngressHTTPPort(), m.cfg.GetIngressHTTPSPort())
	}
	return "caddy failed to start"
}
