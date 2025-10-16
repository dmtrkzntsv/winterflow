package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) GetLogLevel() string {
	return os.Getenv("LOG_LEVEL")
}

func (c *Config) GetServerPort() string {
	return os.Getenv("PORT")
}

func (c *Config) GetRouteTimeout() time.Duration {
	v := os.Getenv("ROUTE_TIMEOUT_MS")
	if v == "" {
		return 0
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
