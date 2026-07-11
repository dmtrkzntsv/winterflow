package caddy

import (
	"context"
	"os"
	"path/filepath"
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
		HTTPPort:   m.cfg.GetIngressHTTPPort(),
		HTTPSPort:  m.cfg.GetIngressHTTPSPort(),
		ACMEEmail:  m.cfg.GetIngressACMEEmail(),
		StorageDir: filepath.Join(m.cfg.GetAgentDataDir(), "caddy"),
		LogLevel:   m.cfg.GetLogLevel(),
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
// can read Enabled() for the agent's feature map. A start failure (typically
// binding 80/443 without CAP_NET_BIND_SERVICE) logs, leaves the manager
// disabled, and returns nil: the agent runs on without ingress.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, warnings := m.buildConfig()
	for _, w := range warnings {
		m.log.Warn("ingress", "warning", w)
	}
	if cfg == nil {
		return nil
	}
	if err := caddy.Load(cfg, true); err != nil {
		m.log.Warn("ingress disabled: caddy failed to start (need CAP_NET_BIND_SERVICE for ports 80/443?)", "error", err)
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
