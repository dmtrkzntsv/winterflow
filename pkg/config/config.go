package config

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
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

func (c *ServerConfig) GetLogLevel() string {
	return os.Getenv("LOG_LEVEL")
}

func (c *ServerConfig) GetApiPort() string {
	return os.Getenv("API_PORT")
}

// GetApiListenAddr is the address the HTTP API listens on: API_LISTEN_ADDR
// (an IP or host; empty = every interface) joined with API_PORT.
func (c *ServerConfig) GetApiListenAddr() string {
	return net.JoinHostPort(strings.TrimSpace(os.Getenv("API_LISTEN_ADDR")), c.GetApiPort())
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

// GetDbURL is DATABASE_URL. Standalone defaults to a local SQLite file;
// the distributed API has no default (Postgres is required).
func (c *ServerConfig) GetDbURL() string {
	v := os.Getenv("DATABASE_URL")
	if v == "" && c.IsStandalone() {
		return "sqlite://winterflow.sqlite"
	}
	return v
}

// ValidateDatabaseURL checks the DATABASE_URL scheme against the topology:
// standalone takes sqlite or postgres, the distributed API postgres only.
func (c *ServerConfig) ValidateDatabaseURL() error {
	u := c.GetDbURL()
	isPG := strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://")
	isSQLite := strings.HasPrefix(u, "sqlite://")
	if c.IsStandalone() {
		if !isPG && !isSQLite {
			return fmt.Errorf("DATABASE_URL must start with sqlite://, postgres:// or postgresql:// in standalone mode")
		}
		return nil
	}
	if !isPG {
		return fmt.Errorf("DATABASE_URL must start with postgres:// or postgresql:// in distributed mode (SQLite is standalone only)")
	}
	return nil
}

// ValidateEnrollment checks that the CA the API signs agent certificates
// with (HUB_CERT_DIR) exists and that its key matches its certificate.
func (c *ServerConfig) ValidateEnrollment() error {
	if _, err := tls.LoadX509KeyPair(c.GetHubCACertPath(), c.GetHubCAKeyPath()); err != nil {
		return fmt.Errorf("enrollment CA (%s, %s): %w", c.GetHubCACertPath(), c.GetHubCAKeyPath(), err)
	}
	return nil
}

func (c *ServerConfig) GetAllowedOrigins() string {
	v := os.Getenv("CORS_ALLOW_ORIGINS")
	if v == "" {
		return "*"
	}
	return v
}

// IsAuthSupported reports whether an OPTIONAL sign-in provider ("google",
// "apple", "oidc") has every variable it needs, in either topology. The
// local email+password provider is always on and never consulted here.
func (c *ServerConfig) IsAuthSupported(a string) bool {
	switch a {
	case "google":
		gcid, gcs := c.GetGoogleAuth()
		return gcid != "" && gcs != ""
	case "apple":
		a := c.GetAppleAuth()
		return a.ClientID != "" && a.TeamID != "" && a.KeyID != "" && a.PrivateKeyPath != ""
	case "oidc":
		o := c.GetOIDCAuth()
		return o.Issuer != "" && o.ClientID != "" && o.ClientSecret != ""
	}
	return false
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

// AppleAuth is Sign in with Apple's configuration: the Services ID
// (ClientID), the team and key ids, and the path of the .p8 key that signs
// the client secret.
type AppleAuth struct {
	ClientID, TeamID, KeyID, PrivateKeyPath string
}

// GetAppleAuth reads AUTH_APPLE_CLIENT_ID, AUTH_APPLE_TEAM_ID,
// AUTH_APPLE_KEY_ID and AUTH_APPLE_PRIVATE_KEY_PATH.
func (c *ServerConfig) GetAppleAuth() AppleAuth {
	return AppleAuth{
		ClientID:       strings.TrimSpace(os.Getenv("AUTH_APPLE_CLIENT_ID")),
		TeamID:         strings.TrimSpace(os.Getenv("AUTH_APPLE_TEAM_ID")),
		KeyID:          strings.TrimSpace(os.Getenv("AUTH_APPLE_KEY_ID")),
		PrivateKeyPath: strings.TrimSpace(os.Getenv("AUTH_APPLE_PRIVATE_KEY_PATH")),
	}
}

// OIDCAuth is a generic OpenID Connect provider's configuration; Name is
// its button label.
type OIDCAuth struct {
	Issuer, ClientID, ClientSecret, Name string
}

// GetOIDCAuth reads AUTH_OIDC_ISSUER, AUTH_OIDC_CLIENT_ID,
// AUTH_OIDC_CLIENT_SECRET and AUTH_OIDC_NAME (default "SSO").
func (c *ServerConfig) GetOIDCAuth() OIDCAuth {
	name := strings.TrimSpace(os.Getenv("AUTH_OIDC_NAME"))
	if name == "" {
		name = "SSO"
	}
	return OIDCAuth{
		Issuer:       strings.TrimSpace(os.Getenv("AUTH_OIDC_ISSUER")),
		ClientID:     strings.TrimSpace(os.Getenv("AUTH_OIDC_CLIENT_ID")),
		ClientSecret: os.Getenv("AUTH_OIDC_CLIENT_SECRET"),
		Name:         name,
	}
}

// GetPublicOrigin is WEB_URL as the instance's public origin
// ("scheme://host[:port]", host lower-cased), for redirect URIs registered
// with a provider and as the OAuth authorization server's issuer. ok is
// false unless WEB_URL is an https origin with no path, query or fragment
// (a trailing "/" is allowed), or the same over plain http on a loopback
// host (local development).
func (c *ServerConfig) GetPublicOrigin() (origin string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(os.Getenv("WEB_URL")))
	if err != nil || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return "", false
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", false
		}
	default:
		return "", false
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), true
}

// GetJwtSecret is JWT_SECRET. Despite the name it no longer signs anything
// (sessions are opaque tokens): it is only the key alert notification
// channel URLs are encrypted with.
func (c *ServerConfig) GetJwtSecret() string {
	return os.Getenv("JWT_SECRET")
}

func (c *ServerConfig) GetRedisCredentials() (addr, pass string, db int) {
	addr, pass = os.Getenv("REDIS_ADDR"), os.Getenv("REDIS_PASSWORD")
	db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
	if err != nil {
		db = 0
	}
	return addr, pass, db
}

// GetRedisUsername is the Redis ACL user to connect as (REDIS_USERNAME);
// empty means the default user. A hub connects as its own user,
// hub:<hub_id>, created by the API when the hub enrolls.
func (c *ServerConfig) GetRedisUsername() string {
	return os.Getenv("REDIS_USERNAME")
}

// GetRedisTLS reports whether Redis connections use TLS. REDIS_TLS wins;
// otherwise on for distributed and off for standalone (which never builds a
// Redis client anyway).
func (c *ServerConfig) GetRedisTLS() bool {
	switch strings.ToLower(os.Getenv("REDIS_TLS")) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	return !c.IsStandalone()
}

// GetRedisCACertPath is the CA that signed Redis's certificate; default the
// Winterflow CA.
func (c *ServerConfig) GetRedisCACertPath() string {
	if v := os.Getenv("REDIS_CA_CERT_PATH"); v != "" {
		return v
	}
	return c.GetHubCACertPath()
}

// Bus channel names are fixed. Standalone's in-process bridge uses the plain
// request queue; in the distributed topology each hub reads its own
// HubRequestQueue.
func (c *ServerConfig) GetBusRequestQueue() string  { return "bus:requests" }
func (c *ServerConfig) GetBusResponseQueue() string { return "bus:responses" }

// GetBusEventsQueue is the channel agents push unsolicited events onto
// (heartbeat liveness, app/server status).
func (c *ServerConfig) GetBusEventsQueue() string { return "bus:events" }

// HubRequestQueue is the API->hub command queue of one hub.
func (c *ServerConfig) HubRequestQueue(hubID string) string { return "bus:requests:" + hubID }

// GetHubRequestsChannel carries hub->API requests (agent auth, enrollment).
func (c *ServerConfig) GetHubRequestsChannel() string { return "hub:requests" }

// GetHubBroadcastChannel carries API->all-hubs messages.
func (c *ServerConfig) GetHubBroadcastChannel() string { return "hub:broadcast" }

// HubResponsesChannel carries API->hub replies for one hub.
func HubResponsesChannel(hubID string) string { return "hub:responses:" + hubID }

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

// DefaultWinterflowAPIURL is the API an agent asks where to connect when
// WINTERFLOW_API_URL is unset.
const DefaultWinterflowAPIURL = "https://api.winterflow.dev"

// GetWinterflowAPIURL is the API origin the agent asks which hub to connect
// to (POST /api/v1/agent/hub). Enrollment writes it to the agent's env file;
// default DefaultWinterflowAPIURL. A trailing slash is dropped.
func (c *ServerConfig) GetWinterflowAPIURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("WINTERFLOW_API_URL")), "/"); v != "" {
		return v
	}
	return DefaultWinterflowAPIURL
}

// GetHubPublicAddr is the host:port agents dial to reach this hub.
func (c *ServerConfig) GetHubPublicAddr() string {
	return strings.TrimSpace(os.Getenv("HUB_PUBLIC_ADDR"))
}

// GetHubID identifies a hub on the bus: the host part of HUB_PUBLIC_ADDR,
// lower-cased (host names are case-insensitive; the API's hub join derives
// the same id for the hub's certificate).
func (c *ServerConfig) GetHubID() string {
	host, _, err := net.SplitHostPort(c.GetHubPublicAddr())
	if err != nil {
		return ""
	}
	return strings.ToLower(host)
}

// GetHubCapacity is how many agents this hub should carry (default 1000).
func (c *ServerConfig) GetHubCapacity() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("HUB_CAPACITY")))
	if err != nil || n < 1 {
		return 1000
	}
	return n
}

// GetHubAccepting reports whether new agents may be assigned to this hub
// (default true; HUB_ACCEPTING=false drains it of new enrollments).
func (c *ServerConfig) GetHubAccepting() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("HUB_ACCEPTING"))) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}

// ValidateHub checks HUB_PUBLIC_ADDR is a host:port.
func (c *ServerConfig) ValidateHub() error {
	host, port, err := net.SplitHostPort(c.GetHubPublicAddr())
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("HUB_PUBLIC_ADDR must be host:port (got %q)", c.GetHubPublicAddr())
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("HUB_PUBLIC_ADDR has an invalid port %q", port)
	}
	return nil
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

// DefaultCatalogURL is where winterflow.io publishes the catalog feed.
const DefaultCatalogURL = "https://winterflow.io/catalog/catalog.json"

// DefaultCatalogPublicKey verifies the published catalog feed: the ed25519
// public key (standard base64) whose private half signs winterflow.io's
// build of the feed.
const DefaultCatalogPublicKey = "QAyhWPQDK0IS8UvVtNA3X/vXwSisba2Lx/7bxI3CG0E="

// minCatalogRefresh keeps CATALOG_REFRESH from polling the feed more than
// every 5 minutes.
const minCatalogRefresh = 5 * time.Minute

// GetCatalogURL is the catalog feed the API syncs (api, standalone).
// Unset is DefaultCatalogURL; set but empty switches the catalog off.
func (c *ServerConfig) GetCatalogURL() string {
	if v, ok := os.LookupEnv("CATALOG_URL"); ok {
		return strings.TrimSpace(v)
	}
	return DefaultCatalogURL
}

// GetCatalogPublicKey is the key the feed's signature must verify against
// (api, standalone; default DefaultCatalogPublicKey).
func (c *ServerConfig) GetCatalogPublicKey() string {
	if v := strings.TrimSpace(os.Getenv("CATALOG_PUBLIC_KEY")); v != "" {
		return v
	}
	return DefaultCatalogPublicKey
}

// GetCatalogRefresh is how often the API syncs the catalog feed (api,
// standalone): a Go duration, default 24h; an unparsable or non-positive
// value is 24h, anything under 5m is raised to 5m.
func (c *ServerConfig) GetCatalogRefresh() time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("CATALOG_REFRESH")))
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	return max(d, minCatalogRefresh)
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
