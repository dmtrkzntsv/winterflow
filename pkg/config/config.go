package config

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
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

func (c *ServerConfig) GetApiURL() string {
	return os.Getenv("API_URL")
}

func (c *ServerConfig) GetWebURL() string {
	return os.Getenv("WEB_URL")
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
	case "env":
		username, pass := c.GetEnvAuth()
		if username == "" || pass == "" {
			result = false
		} else {
			result = true
		}
	}
	return result
}

func (c *ServerConfig) GetGoogleAuth() (clientID string, clientSecret string) {
	return os.Getenv("AUTH_GOOGLE_CLIENT_ID"), os.Getenv("AUTH_GOOGLE_CLIENT_SECRET")
}

func (c *ServerConfig) GetEnvAuth() (username string, password string) {
	return os.Getenv("AUTH_ENV_USERNAME"), os.Getenv("AUTH_ENV_PASSWORD")
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

// GetAppsTemplatesDir holds per-app, per-revision source templates:
// {dataDir}/apps_templates/{appID}/{revision}/.
func (c *ServerConfig) GetAppsTemplatesDir() string {
	return path.Join(c.GetAgentDataDir(), "apps_templates")
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

func (c *ServerConfig) GetHubCSRPath() string {
	return path.Join(c.GetHubCertDir(), c.GetHubCSRFilename())
}

func (c *ServerConfig) GetHubFullchainFilename() string {
	return "hub_fullchain.crt"
}

func (c *ServerConfig) GetHubFullchainPath() string {
	return path.Join(c.GetHubCertDir(), c.GetHubFullchainFilename())
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

func (c *ServerConfig) GetAgentCSRPath() string {
	return path.Join(c.GetHubCertDir(), c.GetAgentCSRFilename())
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

func (c *ServerConfig) GetMode() string {
	return c.mode
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
