package cert

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestIssueAgentCert(t *testing.T) {
	caDir := t.TempDir()
	if _, err := EnsureHubPKI(caDir, []string{"localhost"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "agent-certs")
	if err := IssueAgentCert(caDir, out, "srv-dev"); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(out, AgentCertFile), filepath.Join(out, AgentKeyFile)); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(out, AgentKeyFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode: %v %v", fi, err)
	}
	c := readAgentCert(t, out)
	if c.Subject.CommonName != "srv-dev" {
		t.Errorf("CN = %q", c.Subject.CommonName)
	}
	ca, _, err := loadCertificate(filepath.Join(out, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := c.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Errorf("verify against the copied CA: %v", err)
	}

	// Into the CA directory itself: no copy of ca.crt over itself.
	before := readFile(t, filepath.Join(caDir, "ca.crt"))
	if err := IssueAgentCert(caDir, caDir, "srv-dev"); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, filepath.Join(caDir, "ca.crt"))) != string(before) {
		t.Error("ca.crt changed")
	}
}

func TestIssueAgentCertErrors(t *testing.T) {
	if err := IssueAgentCert(t.TempDir(), t.TempDir(), "srv"); err == nil {
		t.Error("no CA accepted")
	}
	if err := IssueAgentCert("", t.TempDir(), "srv"); err == nil {
		t.Error("empty CA dir accepted")
	}
	if err := IssueAgentCert(t.TempDir(), t.TempDir(), ""); err == nil {
		t.Error("empty server id accepted")
	}
}
