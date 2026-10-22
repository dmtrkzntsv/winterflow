package cert

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readAgentCert(t *testing.T, dir string) *x509.Certificate {
	t.Helper()
	c, _, err := loadCertificate(filepath.Join(dir, AgentCertFile))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEnsureAgentIdentityCreates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "certs")
	certPEM, created, err := EnsureAgentIdentity(dir, "srv-1")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if !bytes.Equal(certPEM, readFile(t, filepath.Join(dir, AgentCertFile))) {
		t.Error("returned PEM differs from agent.crt")
	}
	c := readAgentCert(t, dir)
	if c.Subject.CommonName != "srv-1" {
		t.Errorf("CN = %q", c.Subject.CommonName)
	}
	if len(c.ExtKeyUsage) != 1 || c.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || c.IsCA {
		t.Errorf("ExtKeyUsage = %v, IsCA = %v", c.ExtKeyUsage, c.IsCA)
	}
	if c.NotAfter.Before(time.Now().AddDate(9, 11, 0)) {
		t.Errorf("NotAfter = %v, want ~10 years", c.NotAfter)
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, AgentCertFile), filepath.Join(dir, AgentKeyFile)); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if k, err := loadECPrivateKey(filepath.Join(dir, AgentKeyFile)); err != nil || k.Curve != elliptic.P256() {
		t.Fatalf("key: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, AgentKeyFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode: %v %v", fi, err)
	}
	if id, ok := AgentIdentityID(dir); !ok || id != "srv-1" {
		t.Errorf("AgentIdentityID = %q, %v", id, ok)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("files = %v, want only agent.key and agent.crt", entries)
	}
}

// An existing pair is kept as is: secrets are encrypted to its key. A legacy
// CA-signed certificate counts too.
func TestEnsureAgentIdentityKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey, _, _ := selfSignedCA(t, t.TempDir())
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	certPEM, _, err := SignCSRAs(csrFor(t, "x", key), "srv-legacy", caCert, caKey, 5)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, AgentKeyFile), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AgentCertFile), []byte(certPEM), 0o600); err != nil {
		t.Fatal(err)
	}

	got, created, err := EnsureAgentIdentity(dir, "srv-legacy")
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if string(got) != certPEM || !bytes.Equal(readFile(t, filepath.Join(dir, AgentKeyFile)), keyPEM) {
		t.Error("existing pair was replaced")
	}

	// Again, on a freshly created identity.
	dir2 := t.TempDir()
	first, _, _ := EnsureAgentIdentity(dir2, "srv-2")
	second, created, err := EnsureAgentIdentity(dir2, "srv-2")
	if err != nil || created || !bytes.Equal(first, second) {
		t.Fatalf("second call: created=%v err=%v", created, err)
	}
}

func TestEnsureAgentIdentityRegenerates(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"key replaced": func(t *testing.T, dir string) {
			k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			der, _ := x509.MarshalECPrivateKey(k)
			if err := os.WriteFile(filepath.Join(dir, AgentKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"cert missing": func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, AgentCertFile))
		},
		"key missing": func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, AgentKeyFile))
		},
		"cert garbage": func(t *testing.T, dir string) {
			_ = os.WriteFile(filepath.Join(dir, AgentCertFile), []byte("junk"), 0o600)
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, _, err := EnsureAgentIdentity(dir, "srv-1"); err != nil {
				t.Fatal(err)
			}
			breakIt(t, dir)
			if _, ok := AgentIdentityID(dir); ok {
				t.Error("AgentIdentityID accepted a broken pair")
			}
			_, created, err := EnsureAgentIdentity(dir, "srv-1")
			if err != nil || !created {
				t.Fatalf("created=%v err=%v", created, err)
			}
			if _, err := tls.LoadX509KeyPair(filepath.Join(dir, AgentCertFile), filepath.Join(dir, AgentKeyFile)); err != nil {
				t.Fatalf("pair after regeneration: %v", err)
			}
		})
	}

	// A valid pair for another server id is replaced too.
	dir := t.TempDir()
	if _, _, err := EnsureAgentIdentity(dir, "srv-old"); err != nil {
		t.Fatal(err)
	}
	if _, created, err := EnsureAgentIdentity(dir, "srv-new"); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if c := readAgentCert(t, dir); c.Subject.CommonName != "srv-new" {
		t.Errorf("CN = %q", c.Subject.CommonName)
	}
}

// Files are written by a rename of a temp file beside them: nothing is left
// behind, and stray legacy files in the directory are not touched.
func TestEnsureAgentIdentityAtomic(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, "hub_fullchain.crt")
	if err := os.WriteFile(stray, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureAgentIdentity(dir, "srv-1"); err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, ".*.tmp*")); len(m) != 0 {
		t.Fatalf("temp files left: %v", m)
	}
	if string(readFile(t, stray)) != "legacy" {
		t.Error("stray legacy file changed")
	}

	// An unwritable directory fails without a half-written pair.
	if os.Geteuid() != 0 {
		ro := t.TempDir()
		if err := os.Chmod(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
		if _, _, err := EnsureAgentIdentity(ro, "srv-1"); err == nil {
			t.Fatal("expected an error in a read-only directory")
		}
		if entries, _ := os.ReadDir(ro); len(entries) != 0 {
			t.Errorf("left %v", entries)
		}
	}
}

func TestEnsureAgentIdentityRequiresArgs(t *testing.T) {
	if _, _, err := EnsureAgentIdentity("", "srv"); err == nil {
		t.Error("empty dir accepted")
	}
	if _, _, err := EnsureAgentIdentity(t.TempDir(), " "); err == nil {
		t.Error("empty server id accepted")
	}
	if _, ok := AgentIdentityID(t.TempDir()); ok {
		t.Error("AgentIdentityID ok on an empty dir")
	}
}
