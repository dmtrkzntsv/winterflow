package config

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// MaxBackupRetentionDays caps BACKUP_RETENTION_DAYS (100 years).
const MaxBackupRetentionDays = 36500

// GetBackupRetentionDays is the ceiling on the age of every backup copy
// (BACKUP_RETENTION_DAYS, default 180): no copy of any kind, of any app, is
// kept longer. 0 means no ceiling; a negative or unparsable value is the
// default. Values above MaxBackupRetentionDays (100 years, so "forever" can
// be typed as a big number without overflowing durations) are capped to it.
// Read by standalone and agent.
func (c *ServerConfig) GetBackupRetentionDays() int {
	if v, err := strconv.Atoi(os.Getenv("BACKUP_RETENTION_DAYS")); err == nil && v >= 0 {
		return min(v, MaxBackupRetentionDays)
	}
	return 180
}

// Default EXPOSE_PORT_RANGE bounds.
const (
	DefaultExposePortLow  = 20000
	DefaultExposePortHigh = 29999
)

// GetExposePortRange is the range the agent allocates host ports for
// x-winterflow expose entries from (EXPOSE_PORT_RANGE, "low-high", default
// 20000-29999). A malformed value, one outside 1-65535 or with low above
// high, is the default. Read by standalone and agent.
func (c *ServerConfig) GetExposePortRange() (low, high int) {
	lo, hi, ok := strings.Cut(os.Getenv("EXPOSE_PORT_RANGE"), "-")
	l, errL := strconv.Atoi(strings.TrimSpace(lo))
	h, errH := strconv.Atoi(strings.TrimSpace(hi))
	if !ok || errL != nil || errH != nil || l < 1 || h > 65535 || l > h {
		return DefaultExposePortLow, DefaultExposePortHigh
	}
	return l, h
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
// Default off — many home servers already have a proxy (or a tunnel) owning
// 80/443, so Caddy is opt-in: set INGRESS_ENABLED=true to serve app domains
// from this process. Disabled means Caddy is never loaded — the agent
// reports ingress:false and the UI hides per-app domain editing.
func (c *ServerConfig) IsIngressEnabled() bool {
	switch strings.ToLower(os.Getenv("INGRESS_ENABLED")) {
	case "true", "1":
		return true
	default:
		return false
	}
}

// IsMetricsEnabled reports whether resource metrics are collected and sent
// (agent, standalone) and stored (API). Default on; METRICS_ENABLED=false
// (or 0) turns the whole pipeline off.
func (c *ServerConfig) IsMetricsEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("METRICS_ENABLED"))) {
	case "false", "0", "no", "off":
		return false
	default:
		return true
	}
}

// AllowsPrivateAlertTargets reports whether alert notifications may go to
// loopback, private and link-local addresses (a LAN ntfy, Gotify or mail
// server). Default: allowed in standalone (the box's own network is the
// point), refused by the distributed API, whose self-registered orgs must
// not reach its internal network. ALERTS_ALLOW_PRIVATE_TARGETS=true/false
// overrides either default.
func (c *ServerConfig) AllowsPrivateAlertTargets() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ALERTS_ALLOW_PRIVATE_TARGETS"))) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return c.IsStandalone()
	}
}

// minMetricsDiskInterval keeps the disk scan (one `docker system df -v`,
// ~45 s of daemon work on a busy box) from being scheduled back to back.
const minMetricsDiskInterval = 5 * time.Minute

// GetMetricsDiskInterval is how often the agent measures per-app disk usage
// (default 1h). "0" disables the scan; an unparsable value falls back to 1h
// and anything shorter than 5m is raised to 5m.
func (c *ServerConfig) GetMetricsDiskInterval() time.Duration {
	v := strings.TrimSpace(os.Getenv("METRICS_DISK_INTERVAL"))
	if v == "" {
		return time.Hour
	}
	if v == "0" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return time.Hour
	}
	if d == 0 {
		return 0
	}
	return max(d, minMetricsDiskInterval)
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

// GetIngressTrustedProxies lists the proxies (comma-separated CIDRs or IPs in
// INGRESS_TRUSTED_PROXIES, default none) whose CF-Connecting-IP /
// X-Forwarded-For headers the ingress believes (Caddy's trusted_proxies).
// Without them every request behind a tunnel or CDN shares the proxy's IP —
// and one throttle bucket.
func (c *ServerConfig) GetIngressTrustedProxies() []string {
	var out []string
	for _, v := range strings.Split(os.Getenv("INGRESS_TRUSTED_PROXIES"), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
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

// GetGitHubReleasesURL is the base URL self-update binaries are downloaded
// from. The expected layout is {base}/{vYYMMDD.N}/winterflow-{component}-{os}-{arch}
// where component is "standalone" or "agent" — the asset naming produced by
// .goreleaser.yaml and consumed by scripts/install.sh. Override with
// GITHUB_RELEASES_URL.
func (c *ServerConfig) GetGitHubReleasesURL() string {
	if v := os.Getenv("GITHUB_RELEASES_URL"); v != "" {
		return v
	}
	return "https://github.com/dmtrkzntsv/winterflow/releases/download"
}
