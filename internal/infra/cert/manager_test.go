package cert

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

const (
	testCASubject     = "/C=CA/O=WinterFlow Test/OU=CA/CN=Test CA/emailAddress=ca@test.local"
	testServerSubject = "/C=CA/O=WinterFlow Test/OU=SERVER/CN=localhost"
)

// setCertEnv points the cert-related env vars at a fresh temp dir and returns
// the config plus the cert directory. HUB_CERT_DIR intentionally points at a
// directory that does not exist yet — the manager must create it.
func setCertEnv(t *testing.T) (*config.ServerConfig, string) {
	t.Helper()
	dir := t.TempDir()
	certDir := filepath.Join(dir, "hub-certs")

	extPath := filepath.Join(dir, "ext.cnf")
	extContent := "[alt_names]\nDNS.1 = localhost\nIP.1 = 127.0.0.1\n"
	if err := os.WriteFile(extPath, []byte(extContent), 0o644); err != nil {
		t.Fatalf("write ext file: %v", err)
	}

	t.Setenv("HUB_CERT_DIR", certDir)
	t.Setenv("HUB_CERT_EXT_PATH", extPath)
	t.Setenv("HUB_CA_SUBJECT", testCASubject)
	t.Setenv("HUB_SERVER_SUBJECT", testServerSubject)

	return config.NewServerConfig("standalone"), certDir
}

func newTestManager(t *testing.T) (*Manager, *config.ServerConfig, string) {
	t.Helper()
	cfg, certDir := setCertEnv(t)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "cert-test"})
	m, err := NewManager(cfg, log)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, cfg, certDir
}

func parseCertFile(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("no CERTIFICATE PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate %s: %v", path, err)
	}
	return cert
}

func TestNewManagerConfigErrors(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "cert-test"})

	cases := []struct {
		name   string
		envVar string
	}{
		{"missing cert dir", "HUB_CERT_DIR"},
		{"missing ext path", "HUB_CERT_EXT_PATH"},
		{"missing CA subject", "HUB_CA_SUBJECT"},
		{"missing server subject", "HUB_SERVER_SUBJECT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := setCertEnv(t)
			t.Setenv(tc.envVar, "")
			if _, err := NewManager(cfg, log); err == nil {
				t.Fatalf("expected error when %s is unset", tc.envVar)
			}
		})
	}

	t.Run("subject without CN", func(t *testing.T) {
		cfg, _ := setCertEnv(t)
		t.Setenv("HUB_CA_SUBJECT", "/O=NoCommonName")
		if _, err := NewManager(cfg, log); err == nil {
			t.Fatal("expected error for CA subject without CN")
		}
	})
}

func TestGenerateServerCreatesAllArtifacts(t *testing.T) {
	m, cfg, certDir := newTestManager(t)

	if IsServerCertificateGenerated(m) {
		t.Fatal("IsServerCertificateGenerated should be false before generation")
	}

	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}

	for _, p := range []string{
		cfg.GetHubCAKeyPath(),
		cfg.GetHubCACertPath(),
		cfg.GetHubKeyPath(),
		cfg.GetHubCertPath(),
		filepath.Join(certDir, cfg.GetHubFullchainFilename()),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected artifact at %s: %v", p, err)
		}
	}

	checks := []struct {
		name   string
		exists func() (bool, error)
	}{
		{"CA key", m.ExistsCAKey},
		{"CA cert", m.ExistsCACertificate},
		{"server key", m.ExistsServerKey},
		{"server cert", m.ExistsServerCertificate},
		{"fullchain", m.ExistsFullchainCertificate},
	}
	for _, c := range checks {
		if exists, err := c.exists(); err != nil || !exists {
			t.Errorf("%s exists = (%v, %v), want (true, nil)", c.name, exists, err)
		}
	}

	if !IsServerCertificateGenerated(m) {
		t.Error("IsServerCertificateGenerated should be true after generation")
	}

	// The server certificate must chain to the generated CA and carry the SANs
	// from the ext file.
	caCert := parseCertFile(t, cfg.GetHubCACertPath())
	serverCert := parseCertFile(t, cfg.GetHubCertPath())
	if !caCert.IsCA {
		t.Error("CA certificate should have IsCA=true")
	}
	if err := serverCert.CheckSignatureFrom(caCert); err != nil {
		t.Errorf("server certificate not signed by CA: %v", err)
	}
	if serverCert.Subject.CommonName != "localhost" {
		t.Errorf("server CN = %q, want localhost", serverCert.Subject.CommonName)
	}
	foundDNS := false
	for _, d := range serverCert.DNSNames {
		if d == "localhost" {
			foundDNS = true
		}
	}
	if !foundDNS {
		t.Errorf("server certificate missing localhost SAN, got %v", serverCert.DNSNames)
	}
}

func TestGenerateServerIsIdempotentWithoutOverride(t *testing.T) {
	m, cfg, _ := newTestManager(t)
	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}

	before, err := os.ReadFile(cfg.GetHubCACertPath())
	if err != nil {
		t.Fatalf("read CA cert: %v", err)
	}
	beforeKey, err := os.ReadFile(cfg.GetHubCAKeyPath())
	if err != nil {
		t.Fatalf("read CA key: %v", err)
	}

	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("second GenerateServer: %v", err)
	}

	after, err := os.ReadFile(cfg.GetHubCACertPath())
	if err != nil {
		t.Fatalf("re-read CA cert: %v", err)
	}
	afterKey, err := os.ReadFile(cfg.GetHubCAKeyPath())
	if err != nil {
		t.Fatalf("re-read CA key: %v", err)
	}

	if !bytes.Equal(before, after) {
		t.Error("CA certificate was regenerated despite override=false")
	}
	if !bytes.Equal(beforeKey, afterKey) {
		t.Error("CA key was regenerated despite override=false")
	}
}

func TestGenerateServerOverrideRegenerates(t *testing.T) {
	m, cfg, _ := newTestManager(t)
	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}

	beforeCACert, _ := os.ReadFile(cfg.GetHubCACertPath())
	beforeCAKey, _ := os.ReadFile(cfg.GetHubCAKeyPath())
	beforeServer, _ := os.ReadFile(cfg.GetHubCertPath())

	if err := m.GenerateServer(true); err != nil {
		t.Fatalf("GenerateServer(override): %v", err)
	}

	afterCACert, _ := os.ReadFile(cfg.GetHubCACertPath())
	afterCAKey, _ := os.ReadFile(cfg.GetHubCAKeyPath())
	afterServer, _ := os.ReadFile(cfg.GetHubCertPath())

	if bytes.Equal(beforeCACert, afterCACert) {
		t.Error("CA certificate unchanged despite override=true")
	}
	if bytes.Equal(beforeCAKey, afterCAKey) {
		t.Error("CA key unchanged despite override=true")
	}
	if bytes.Equal(beforeServer, afterServer) {
		t.Error("server certificate unchanged despite override=true")
	}

	// The regenerated material must still form a valid chain.
	caCert := parseCertFile(t, cfg.GetHubCACertPath())
	serverCert := parseCertFile(t, cfg.GetHubCertPath())
	if err := serverCert.CheckSignatureFrom(caCert); err != nil {
		t.Errorf("regenerated server certificate not signed by regenerated CA: %v", err)
	}
}

func TestGenerateServerRegeneratesOnlyMissingArtifacts(t *testing.T) {
	m, cfg, _ := newTestManager(t)
	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}

	beforeCA, _ := os.ReadFile(cfg.GetHubCACertPath())
	beforeServer, _ := os.ReadFile(cfg.GetHubCertPath())

	if err := m.DeleteServerCertificate(); err != nil {
		t.Fatalf("DeleteServerCertificate: %v", err)
	}
	if IsServerCertificateGenerated(m) {
		t.Error("IsServerCertificateGenerated should be false with the server cert missing")
	}

	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer after delete: %v", err)
	}

	afterCA, _ := os.ReadFile(cfg.GetHubCACertPath())
	afterServer, _ := os.ReadFile(cfg.GetHubCertPath())
	if !bytes.Equal(beforeCA, afterCA) {
		t.Error("CA certificate should be untouched when only the server cert was missing")
	}
	if bytes.Equal(beforeServer, afterServer) {
		t.Error("server certificate should have been regenerated")
	}
	if !IsServerCertificateGenerated(m) {
		t.Error("IsServerCertificateGenerated should be true again")
	}
}

func TestGenerateAgent(t *testing.T) {
	m, cfg, _ := newTestManager(t)
	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}

	const agentID = "agent-7c9e6679"
	expiresAt, err := m.GenerateAgent(agentID)
	if err != nil {
		t.Fatalf("GenerateAgent: %v", err)
	}
	if expiresAt == nil {
		t.Fatal("expected non-nil expiry timestamp")
	}
	// Manager signs agent certs with a 10-year validity.
	wantExpiry := time.Now().AddDate(10, 0, 0)
	if diff := expiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("expiresAt = %v, want ~%v", expiresAt, wantExpiry)
	}

	for _, c := range []struct {
		name   string
		exists func() (bool, error)
	}{
		{"agent key", m.ExistsAgentKey},
		{"agent CSR", m.ExistsAgentCSR},
		{"agent cert", m.ExistsAgentCertificate},
	} {
		if exists, err := c.exists(); err != nil || !exists {
			t.Errorf("%s exists = (%v, %v), want (true, nil)", c.name, exists, err)
		}
	}

	certData, err := m.GetAgentCertificate()
	if err != nil {
		t.Fatalf("GetAgentCertificate: %v", err)
	}
	block, _ := pem.Decode(certData)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("agent certificate is not a CERTIFICATE PEM block")
	}
	agentCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse agent certificate: %v", err)
	}
	if agentCert.Subject.CommonName != agentID {
		t.Errorf("agent CN = %q, want %q", agentCert.Subject.CommonName, agentID)
	}
	caCert := parseCertFile(t, cfg.GetHubCACertPath())
	if err := agentCert.CheckSignatureFrom(caCert); err != nil {
		t.Errorf("agent certificate not signed by hub CA: %v", err)
	}
	// The returned expiry must match the certificate's NotAfter (1s DER granularity).
	if diff := agentCert.NotAfter.Sub(*expiresAt); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("cert NotAfter %v != returned expiry %v", agentCert.NotAfter, expiresAt)
	}
}

func TestGenerateAgentWithoutCA(t *testing.T) {
	m, _, _ := newTestManager(t)
	if _, err := m.GenerateAgent("agent-1"); err == nil {
		t.Fatal("expected error when the CA has not been generated yet")
	}
}

func TestGenerateAgentEmptyID(t *testing.T) {
	m, _, _ := newTestManager(t)
	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}
	if _, err := m.GenerateAgent(""); err == nil {
		t.Fatal("expected error for empty certificate ID")
	}
}

func TestDeleteAgent(t *testing.T) {
	m, _, _ := newTestManager(t)
	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}
	if _, err := m.GenerateAgent("agent-1"); err != nil {
		t.Fatalf("GenerateAgent: %v", err)
	}

	if err := m.DeleteAgent(); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	for _, c := range []struct {
		name   string
		exists func() (bool, error)
	}{
		{"agent key", m.ExistsAgentKey},
		{"agent CSR", m.ExistsAgentCSR},
		{"agent cert", m.ExistsAgentCertificate},
	} {
		if exists, err := c.exists(); err != nil || exists {
			t.Errorf("%s exists = (%v, %v) after DeleteAgent, want (false, nil)", c.name, exists, err)
		}
	}

	// Deleting again is a no-op.
	if err := m.DeleteAgent(); err != nil {
		t.Fatalf("second DeleteAgent: %v", err)
	}

	if _, err := m.GetAgentCertificate(); err == nil {
		t.Fatal("expected error reading the agent certificate after deletion")
	}
}

func TestIsServerCertificateGeneratedPartialArtifacts(t *testing.T) {
	m, _, _ := newTestManager(t)
	if err := m.GenerateServer(false); err != nil {
		t.Fatalf("GenerateServer: %v", err)
	}

	deletes := []struct {
		name string
		del  func() error
	}{
		{"CA cert", m.DeleteCACertificate},
		{"CA key", m.DeleteCAKey},
		{"server cert", m.DeleteServerCertificate},
		{"server key", m.DeleteServerKey},
		{"fullchain", m.DeleteFullchainCertificate},
	}
	for _, d := range deletes {
		// Regenerate with override so every iteration starts from a fully
		// consistent set (regenerating only a missing CA key would leave it
		// mismatched with the surviving CA certificate).
		if err := m.GenerateServer(true); err != nil {
			t.Fatalf("regenerate before deleting %s: %v", d.name, err)
		}
		if !IsServerCertificateGenerated(m) {
			t.Fatalf("expected complete server material before deleting %s", d.name)
		}
		if err := d.del(); err != nil {
			t.Fatalf("delete %s: %v", d.name, err)
		}
		if IsServerCertificateGenerated(m) {
			t.Errorf("IsServerCertificateGenerated should be false with %s missing", d.name)
		}
	}
}

// Regenerating after ANY single artifact loss must yield a consistent,
// verifiable set without override: a broken CA pair escalates to a full
// regeneration (certs signed by a discarded CA can't chain to a new one),
// and dependents cascade (new server key -> new server cert -> new chain).
func TestGenerateServerHealsAnySingleMissingArtifact(t *testing.T) {
	m, cfg, _ := newTestManager(t)
	_ = cfg

	cases := []struct {
		name string
		del  func() error
	}{
		{"CA key", m.DeleteCAKey},
		{"CA cert", m.DeleteCACertificate},
		{"server key", m.DeleteServerKey},
		{"server cert", m.DeleteServerCertificate},
		{"fullchain", m.DeleteFullchainCertificate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := m.GenerateServer(true); err != nil {
				t.Fatalf("baseline: %v", err)
			}
			if err := tc.del(); err != nil {
				t.Fatalf("delete %s: %v", tc.name, err)
			}
			if err := m.GenerateServer(false); err != nil {
				t.Fatalf("heal after losing %s: %v", tc.name, err)
			}
			assertServerChainValid(t, m)
			// And a subsequent agent issuance must work against the healed CA.
			if _, err := m.GenerateAgent("post-heal-agent"); err != nil {
				t.Fatalf("agent issuance after healing %s: %v", tc.name, err)
			}
		})
	}
}

// assertServerChainValid verifies the on-disk server material is internally
// consistent: server cert signed by the CA cert, fullchain = server + CA.
func assertServerChainValid(t *testing.T, m *Manager) {
	t.Helper()
	dir := m.cfg.GetHubCertDir()
	caCert := parseCertFile(t, filepath.Join(dir, m.paths.CACert))
	serverCert := parseCertFile(t, filepath.Join(dir, m.paths.ServerCert))
	if err := serverCert.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("server cert does not chain to CA: %v", err)
	}
	full, err := os.ReadFile(filepath.Join(dir, m.paths.ServerFullChain))
	if err != nil {
		t.Fatalf("read fullchain: %v", err)
	}
	block, rest := pem.Decode(full)
	if block == nil {
		t.Fatal("fullchain: no first PEM block")
	}
	first, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("fullchain first cert: %v", err)
	}
	if first.SerialNumber.Cmp(serverCert.SerialNumber) != 0 {
		t.Fatal("fullchain does not start with the current server cert")
	}
	block2, _ := pem.Decode(rest)
	if block2 == nil {
		t.Fatal("fullchain: missing CA block")
	}
	second, err := x509.ParseCertificate(block2.Bytes)
	if err != nil {
		t.Fatalf("fullchain second cert: %v", err)
	}
	if second.SerialNumber.Cmp(caCert.SerialNumber) != 0 {
		t.Fatal("fullchain does not end with the current CA cert")
	}
}
