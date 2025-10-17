package config

import (
	"os"
)

type Config struct {
}

func NewConfig() *Config {
	return &Config{}
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

func (c *Config) GetAllowedOrigins() string {
	v := os.Getenv("CORS_ALLOW_ORIGINS")
	if v == "" {
		return "*"
	}
	return v
}

func (c *Config) IsAuthSupported(a string) bool {
	result := false
	if a == "google" {
		gcid, gcs := c.GetGoogleAuth()
		if gcid != "" && gcs != "" {
			result = true
		}
	}
	return result
}

func (c *Config) GetGoogleAuth() (clientID string, clientSecret string) {
	return os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET")
}

func (c *Config) GetJwtSecret() string {
	return os.Getenv("JWT_SECRET")
}
