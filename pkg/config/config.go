package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) GetRegion() string {
	return os.Getenv("REGION")
}

func (c *Config) GetLogLevel() string {
	return os.Getenv("LOG_LEVEL")
}

func (c *Config) GetApiPort() string {
	return os.Getenv("API_PORT")
}

func (c *Config) GetApiURL() string {
	return os.Getenv("API_URL")
}

func (c *Config) GetWebURL() string {
	return os.Getenv("WEB_URL")
}

func (c *Config) GetDbURL() string {
	return os.Getenv("DATABASE_URL")
}

func (c *Config) GetAllowedOrigins() string {
	v := os.Getenv("CORS_ALLOW_ORIGINS")
	if v == "" {
		return "*"
	}
	return v
}

func (c *Config) IsAuthSupported(a string) bool {
	result := false
	switch a {
	case "google":
		gcid, gcs := c.GetGoogleAuth()
		if gcid == "" || gcs == "" {
			result = false
		} else {
			result = true
		}
	case ".env":
		username, pass := c.GetEnvAuth()
		if username == "" || pass == "" {
			result = false
		} else {
			result = true
		}
	}
	return result
}

func (c *Config) GetGoogleAuth() (clientID string, clientSecret string) {
	return os.Getenv("AUTH_GOOGLE_CLIENT_ID"), os.Getenv("AUTH_GOOGLE_CLIENT_SECRET")
}

func (c *Config) GetEnvAuth() (username string, password string) {
	return os.Getenv("AUTH_ENV_USERNAME"), os.Getenv("AUTH_ENV_PASSWORD")
}

func (c *Config) GetJwtSecret() string {
	return os.Getenv("JWT_SECRET")
}

func (c *Config) GetAvatarsStoragePath() string {
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

func (c *Config) GetRedisCredentials() (addr, pass string, db int) {
	addr, pass = os.Getenv("REDIS_ADDR"), os.Getenv("REDIS_PASSWORD")
	db, err := strconv.Atoi(os.Getenv("REDIS_DB"))
	if err != nil {
		db = 0
	}
	return addr, pass, db
}

func (c *Config) GetBusRequestQueue() string {
	v := os.Getenv("BUS_REQUEST_QUEUE")
	if v == "" {
		v = "requests:" + c.GetRegion()
	}
	return v
}

func (c *Config) GetBusResponseQueue() string {
	v := os.Getenv("BUS_RESPONSE_QUEUE")
	if v == "" {
		v = "responses:" + c.GetRegion()
	}
	return v
}

func (c *Config) GetHubHost() string {
	return os.Getenv("HUB_HOST")
}

func (c *Config) GetHubPort() string {
	return os.Getenv("HUB_PORT")
}

func (c *Config) GetHubCACertPath() string {
	return os.Getenv("HUB_CA_CERT_PATH")
}

func (c *Config) GetHubCertPath() string {
	return os.Getenv("HUB_CERT_PATH")
}

func (c *Config) GetHubKeyPath() string {
	return os.Getenv("HUB_KEY_PATH")
}

func (c *Config) GetAgentCertPath() string {
	return os.Getenv("AGENT_CERT_PATH")
}

func (c *Config) GetAgentKeyPath() string {
	return os.Getenv("AGENT_KEY_PATH")
}

func (c *Config) GetAgentCACertPath() string {
	return os.Getenv("AGENT_CA_CERT_PATH")
}
