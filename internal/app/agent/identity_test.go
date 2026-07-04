package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func identityEnv(t *testing.T) *config.ServerConfig {
	t.Helper()
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	t.Setenv("HUB_CERT_DIR", t.TempDir())
	t.Setenv("AGENT_ID", "")
	return config.NewServerConfig("distributed")
}

func writeAgentCert(t *testing.T, cfg *config.ServerConfig, cn string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.GetAgentCertPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.GetAgentCertPath(),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAgentIDEnvOverrideWins(t *testing.T) {
	cfg := identityEnv(t)
	t.Setenv("AGENT_ID", "explicit-id")
	writeAgentCert(t, cfg, "cn-id")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	if got := ResolveAgentID(cfg, log); got != "explicit-id" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAgentIDUsesCertCN(t *testing.T) {
	cfg := identityEnv(t)
	writeAgentCert(t, cfg, "srv-from-cert")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	if got := ResolveAgentID(cfg, log); got != "srv-from-cert" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAgentIDPersistsGeneratedID(t *testing.T) {
	cfg := identityEnv(t)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})

	first := ResolveAgentID(cfg, log)
	if first == "" {
		t.Fatal("empty generated id")
	}
	// Stable across restarts (read back from disk, not regenerated).
	second := ResolveAgentID(cfg, log)
	if second != first {
		t.Fatalf("identity not stable: %q vs %q", first, second)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.GetAgentDataDir(), "agent-id"))
	if err != nil || string(raw) != first {
		t.Fatalf("persisted id = %q (%v), want %q", raw, err, first)
	}
	// A cert appearing later takes precedence over the persisted fallback.
	writeAgentCert(t, cfg, "claimed-server-id")
	if got := ResolveAgentID(cfg, log); got != "claimed-server-id" {
		t.Fatalf("cert should win once present, got %q", got)
	}
}
