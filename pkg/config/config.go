package config

import "os"

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
