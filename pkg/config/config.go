package config

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type ServerConfig struct {
	mode string
}

func NewServerConfig(mode string) *ServerConfig {
	return &ServerConfig{mode: mode}
}

func (c *ServerConfig) GetRegion() string {
	return os.Getenv("REGION")
}

func (c *ServerConfig) GetLogLevel() string {
	return os.Getenv("LOG_LEVEL")
}

func (c *ServerConfig) GetApiPort() string {
	return os.Getenv("API_PORT")
}

func (c *ServerConfig) GetWebURL() string {
	return os.Getenv("WEB_URL")
}

// GetSecureCookies reports whether auth cookies should carry the Secure flag.
// Secure cookies are only sent over HTTPS (browsers also treat localhost as
// secure), so we derive this from the WEB_URL scheme: https in production,
// off for plain-http dev (e.g. accessing the dev server over http://<host>),
// where a Secure cookie would be silently dropped and every request 401s.
func (c *ServerConfig) GetSecureCookies() bool {
	return strings.HasPrefix(os.Getenv("WEB_URL"), "https://")
}

func (c *ServerConfig) GetDbURL() string {
	return os.Getenv("DATABASE_URL")
}

func (c *ServerConfig) GetAllowedOrigins() string {
	v := os.Getenv("CORS_ALLOW_ORIGINS")
	if v == "" {
		return "*"
	}
	return v
}

// IsAuthSupported reports whether an OPTIONAL auth provider is configured.
// The local email+password provider is always on and never consulted here.
func (c *ServerConfig) IsAuthSupported(a string) bool {
	result := false
	switch a {
	case "google":
		if !c.IsStandalone() {
			gcid, gcs := c.GetGoogleAuth()
			if gcid == "" || gcs == "" {
				result = false
			} else {
				result = true
			}
		}
	}
	return result
}

// IsRegistrationEnabled reports whether self-signup is open. Default on;
// set REGISTRATION_ENABLED=false to close it. The first-user claim step
// ignores this (a fresh instance must never be unclaimable).
func (c *ServerConfig) IsRegistrationEnabled() bool {
	switch strings.ToLower(os.Getenv("REGISTRATION_ENABLED")) {
	case "false", "0":
		return false
	default:
		return true
	}
}

func (c *ServerConfig) GetGoogleAuth() (clientID string, clientSecret string) {
	return os.Getenv("AUTH_GOOGLE_CLIENT_ID"), os.Getenv("AUTH_GOOGLE_CLIENT_SECRET")
}

func (c *ServerConfig) GetJwtSecret() string {
	return os.Getenv("JWT_SECRET")
}

func (c *ServerConfig) GetAvatarsStoragePath() string {
	v := os.Getenv("AVATARS_STORAGE_PATH")
	if v == "" {
		dir, _ := os.Getwd()
		v = filepath.Join(dir, "data/avatars")
		if _, err := os.Stat(v); os.IsNotExist(err) {
			_ = os.MkdirAll(v, os.ModePerm)
		}
	}
	return v
}

func (c *ServerConfig) GetRedisCredentials() (addr, pass string, db int) {
	addr, pass = os.Getenv("REDIS_ADDR"), os.Getenv("REDIS_PASSWORD")
	db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
	if err != nil {
		db = 0
	}
	return addr, pass, db
}

func (c *ServerConfig) GetBusRequestQueue() string {
	v := os.Getenv("BUS_REQUEST_QUEUE")
	if v == "" {
		v = "requests:" + c.GetRegion()
	}
	return v
}

func (c *ServerConfig) GetBusResponseQueue() string {
	v := os.Getenv("BUS_RESPONSE_QUEUE")
	if v == "" {
		v = "responses:" + c.GetRegion()
	}
	return v
}

// GetBusEventsQueue is the channel agents push unsolicited events onto
// (heartbeat liveness, app/server status). Region-scoped: a region maps to a
// single API instance, which is the sole consumer.
func (c *ServerConfig) GetBusEventsQueue() string {
	v := os.Getenv("BUS_EVENTS_QUEUE")
	if v == "" {
		v = "events:" + c.GetRegion()
	}
	return v
}

// GetAgentDataDir is the root under which the agent stores app revisions and
// rendered deployments. Defaults to "data" (relative to the working directory)
// to match the repo layout; override with AGENT_DATA_DIR in production
// (v1 used /opt/winterflow).
func (c *ServerConfig) GetAgentDataDir() string {
	if v := os.Getenv("AGENT_DATA_DIR"); v != "" {
		return v
	}
	return "data"
}

// GetAppsDataDir holds the canonical per-app deployment folders (each one a
// git repository): {dataDir}/apps-data/{appID}/. The sibling GetAppsDir holds
// human-readable symlinks into it.
func (c *ServerConfig) GetAppsDataDir() string {
	return path.Join(c.GetAgentDataDir(), "apps-data")
}

// GetAppsDir holds the rendered, ready-to-run deployments:
// {dataDir}/apps/{appID}/.
func (c *ServerConfig) GetAppsDir() string {
	return path.Join(c.GetAgentDataDir(), "apps")
}

func (c *ServerConfig) GetHubHost() string {
	return os.Getenv("HUB_HOST")
}

func (c *ServerConfig) GetHubPort() string {
	return os.Getenv("HUB_PORT")
}

// IsIngressEnabled reports whether the embedded reverse proxy should run.
// Default on; set INGRESS_ENABLED=false to keep the process off ports 80/443
// entirely (unprivileged dev runs, or a host where another proxy owns them).
// Disabled means Caddy is never loaded — the agent reports ingress:false and
// the UI hides per-app domain editing.
func (c *ServerConfig) IsIngressEnabled() bool {
	switch strings.ToLower(os.Getenv("INGRESS_ENABLED")) {
	case "false", "0":
		return false
	default:
		return true
	}
}

// GetIngressHTTPPort is the embedded proxy's HTTP port (default 80).
func (c *ServerConfig) GetIngressHTTPPort() int {
	if v, err := strconv.Atoi(os.Getenv("INGRESS_HTTP_PORT")); err == nil && v > 0 {
		return v
	}
	return 80
}

// GetIngressHTTPSPort is the embedded proxy's HTTPS port (default 443).
func (c *ServerConfig) GetIngressHTTPSPort() int {
	if v, err := strconv.Atoi(os.Getenv("INGRESS_HTTPS_PORT")); err == nil && v > 0 {
		return v
	}
	return 443
}

// GetIngressACMEEmail is the optional ACME account email.
func (c *ServerConfig) GetIngressACMEEmail() string {
	return os.Getenv("INGRESS_ACME_EMAIL")
}

// GetIngressRateLimitRPS is the sustained per-client-IP request rate the
// embedded ingress allows before answering 429 (default 50). Predefined
// throttling so a traffic spike degrades politely instead of pinning the
// host's CPU. Set 0 to disable.
func (c *ServerConfig) GetIngressRateLimitRPS() float64 {
	if v, ok := os.LookupEnv("INGRESS_RATE_LIMIT_RPS"); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			return f
		}
	}
	return 50
}

// GetIngressRateLimitBurst is the short-burst allowance on top of the
// sustained rate (default 100 requests).
func (c *ServerConfig) GetIngressRateLimitBurst() int {
	if v, err := strconv.Atoi(os.Getenv("INGRESS_RATE_LIMIT_BURST")); err == nil && v > 0 {
		return v
	}
	return 100
}

func (c *ServerConfig) GetHubCASubject() string {
	return os.Getenv("HUB_CA_SUBJECT")
}

func (c *ServerConfig) GetHubServerSubject() string {
	return os.Getenv("HUB_SERVER_SUBJECT")
}

func (c *ServerConfig) GetHubCertExtPath() string {
	return os.Getenv("HUB_CERT_EXT_PATH")
}

func (c *ServerConfig) GetHubCertDir() string {
	return os.Getenv("HUB_CERT_DIR")
}

func (c *ServerConfig) GetHubCACertFilename() string {
	return "ca.crt"
}

func (c *ServerConfig) GetHubCACertPath() string {
	return path.Join(c.GetHubCertDir(), c.GetHubCACertFilename())
}

func (c *ServerConfig) GetHubCAKeyFilename() string {
	return "ca.key"
}

func (c *ServerConfig) GetHubCAKeyPath() string {
	return path.Join(c.GetHubCertDir(), c.GetHubCAKeyFilename())
}

func (c *ServerConfig) GetHubCertFilename() string {
	return "hub.crt"
}

func (c *ServerConfig) GetHubCertPath() string {
	return path.Join(c.GetHubCertDir(), c.GetHubCertFilename())
}

func (c *ServerConfig) GetHubCSRFilename() string {
	return "hub.csr"
}

func (c *ServerConfig) GetHubFullchainFilename() string {
	return "hub_fullchain.crt"
}

func (c *ServerConfig) GetHubKeyFilename() string {
	return "hub.key"
}

func (c *ServerConfig) GetHubKeyPath() string {
	return path.Join(c.GetHubCertDir(), c.GetHubKeyFilename())
}

func (c *ServerConfig) GetAgentCertFilename() string {
	return "agent.crt"
}

func (c *ServerConfig) GetAgentCertPath() string {
	return path.Join(c.GetHubCertDir(), c.GetAgentCertFilename())
}

func (c *ServerConfig) GetAgentKeyFilename() string {
	return "agent.key"
}

func (c *ServerConfig) GetAgentKeyPath() string {
	return path.Join(c.GetHubCertDir(), c.GetAgentKeyFilename())
}

func (c *ServerConfig) GetAgentCSRFilename() string {
	return "agent.csr"
}

// GetAgentCACertPath returns the path to the CA certificate the agent uses to
// verify the hub. It is the same CA that signs the hub's server certificate;
// an explicit AGENT_CA_CERT_PATH overrides it for deployments that ship the CA
// separately from the hub cert directory.
func (c *ServerConfig) GetAgentCACertPath() string {
	if p := os.Getenv("AGENT_CA_CERT_PATH"); p != "" {
		return p
	}
	return c.GetHubCACertPath()
}

func (c *ServerConfig) IsStandalone() bool {
	return c.mode == "standalone"
}

// GetGitHubReleasesURL is the base URL the agent downloads its self-update
// binaries from. The expected layout is
// {base}/{version}/winterflow-agent-{os}-{arch}. Override with
// GITHUB_RELEASES_URL.
func (c *ServerConfig) GetGitHubReleasesURL() string {
	if v := os.Getenv("GITHUB_RELEASES_URL"); v != "" {
		return v
	}
	return "https://github.com/winterflowio/winterflow-agent/releases/download"
}
