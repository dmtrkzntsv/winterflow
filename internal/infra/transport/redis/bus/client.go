package redisbus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"github.com/redis/go-redis/v9"

	"winterflow/pkg/config"
)

// NewRedisClient builds the one Redis client every process uses (API, hub,
// the hub registry), logged in as REDIS_USERNAME (empty: the default user).
// With REDIS_TLS on (default in distributed) it speaks TLS 1.2+ and verifies
// the server against REDIS_CA_CERT_PATH; a missing or unparsable CA file is
// an error rather than a silent fallback to the system roots or to
// plaintext.
func NewRedisClient(cfg *config.ServerConfig) (*redis.Client, error) {
	addr, pass, db := cfg.GetRedisCredentials()
	opts := &redis.Options{Addr: addr, Username: cfg.GetRedisUsername(), Password: pass, DB: db}
	if cfg.GetRedisTLS() {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("REDIS_ADDR %q: %w", addr, err)
		}
		caPath := cfg.GetRedisCACertPath()
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("redis CA certificate: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("redis CA certificate %s: no valid PEM certificate", caPath)
		}
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: host}
	}
	return redis.NewClient(opts), nil
}

func Ping(ctx context.Context, rdb *redis.Client) error {
	return rdb.Ping(ctx).Err()
}
