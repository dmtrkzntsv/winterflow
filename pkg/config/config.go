package config

import (
	"os"
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
	return os.Getenv("HUB_CERT_EXT")
}

func (c *ServerConfig) GetHubCertDir() string {
	return os.Getenv("HUB_CERT_DIR")
}

func (c *ServerConfig) GetHubCACertPath() string {
	return os.Getenv("HUB_CA_CERT_PATH")
}

func (c *ServerConfig) GetHubCAKeyPath() string {
	return os.Getenv("HUB_CA_KEY_PATH")
}

func (c *ServerConfig) GetHubCertPath() string {
	return os.Getenv("HUB_CERT_PATH")
}

func (c *ServerConfig) GetHubKeyPath() string {
	return os.Getenv("HUB_KEY_PATH")
}

func (c *ServerConfig) GetAgentCertPath() string {
	return os.Getenv("AGENT_CERT_PATH")
}

func (c *ServerConfig) GetAgentKeyPath() string {
	return os.Getenv("AGENT_KEY_PATH")
}

func (c *ServerConfig) GetAgentCACertPath() string {
	return os.Getenv("AGENT_CA_CERT_PATH")
}

func (c *ServerConfig) GetMode() string {
	return c.mode
}

func (c *ServerConfig) IsStandalone() bool {
	return c.mode == "standalone"
}
