package cert

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
)

func testCSR(t *testing.T) string {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func parsePEM(t *testing.T, s string) *x509.Certificate {
	t.Helper()
	b, _ := pem.Decode([]byte(s))
	c, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCAHasMaxPathLenZero(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureHubPKI(dir, []string{"hub-01.example.com"}); err != nil {
		t.Fatal(err)
	}
	ca, _, err := LoadCA(dir+"/ca.crt", dir+"/ca.key")
	if err != nil {
		t.Fatal(err)
	}
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.MaxPathLen != 0 || !ca.MaxPathLenZero {
		t.Fatalf("CA constraints: %+v", ca)
	}
}

// shortCA writes a CA with one day left into dir.
func shortCA(t *testing.T, dir string) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "short"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	if err := os.WriteFile(dir+"/ca.crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/ca.key", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLeafNeverOutlivesCA(t *testing.T) {
	dir := t.TempDir()
	shortCA(t, dir)
	ca, key, err := LoadCA(dir+"/ca.crt", dir+"/ca.key")
	if err != nil {
		t.Fatal(err)
	}
	p, exp, err := SignCSRAs(testCSR(t), "srv", ca, key, 10)
	if err != nil || parsePEM(t, p).NotAfter.After(ca.NotAfter) || exp.After(ca.NotAfter) {
		t.Fatalf("SignCSRAs: %v", err)
	}
	p, exp, err = SignHubCSR(testCSR(t), "hub-01", []string{"hub-01.example.com"}, ca, key, 10)
	if err != nil || parsePEM(t, p).NotAfter.After(ca.NotAfter) || exp.After(ca.NotAfter) {
		t.Fatalf("SignHubCSR: %v", err)
	}
	if _, err := EnsureServerCert(dir, "redis", []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	c, _, _ := loadCertificate(dir + "/redis.crt")
	if c.NotAfter.After(ca.NotAfter) {
		t.Fatalf("server cert outlives CA: %v > %v", c.NotAfter, ca.NotAfter)
	}
}

func TestHubCertCarriesHubURI(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureHubPKI(dir, []string{"Hub-01.Example.com", "10.0.0.5"}); err != nil {
		t.Fatal(err)
	}
	c, _, _ := loadCertificate(dir + "/hub.crt")
	if id, ok := HubIDFromCert(c); !ok || id != "hub-01.example.com" {
		t.Fatalf("EnsureHubPKI id=%q ok=%v", id, ok)
	}
	if got := HubURI("Hub-01").String(); got != HubURIPrefix+"hub-01" {
		t.Fatalf("HubURI = %q", got)
	}
	ca, key, _ := LoadCA(dir+"/ca.crt", dir+"/ca.key")
	p, _, err := SignHubCSR(testCSR(t), "hub-02.example.com", []string{"hub-02.example.com"}, ca, key, 1)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := HubIDFromCert(parsePEM(t, p)); !ok || id != "hub-02.example.com" {
		t.Fatalf("SignHubCSR id=%q ok=%v", id, ok)
	}
	if _, ok := HubIDFromCert(ca); ok {
		t.Error("CA has a hub id")
	}
}

func TestRedisCertHasNoHubURI(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureHubPKI(dir, []string{"hub-01.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureServerCert(dir, "redis", []string{"127.0.0.1", "hub-01.example.com"}); err != nil {
		t.Fatal(err)
	}
	c, _, _ := loadCertificate(dir + "/redis.crt")
	if _, ok := HubIDFromCert(c); ok || len(c.URIs) != 0 {
		t.Fatalf("redis cert URIs: %v", c.URIs)
	}
}

func TestEnsureHubPKIReissuesCertWithoutURI(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureHubPKI(dir, []string{"hub-01.example.com"}); err != nil {
		t.Fatal(err)
	}
	// Replace hub.crt/hub.key with a URI-less pair (what an older version wrote).
	if _, err := EnsureServerCert(dir, "hub", []string{"hub-01.example.com"}); err != nil {
		t.Fatal(err)
	}
	c, _, _ := loadCertificate(dir + "/hub.crt")
	if _, ok := HubIDFromCert(c); ok {
		t.Fatal("setup: cert has a URI")
	}
	changed, err := EnsureHubPKI(dir, []string{"hub-01.example.com"})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	c, _, _ = loadCertificate(dir + "/hub.crt")
	if id, ok := HubIDFromCert(c); !ok || id != "hub-01.example.com" {
		t.Fatalf("id=%q ok=%v", id, ok)
	}
}

func TestCreateCAAtomic(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	// A non-empty directory at the cert path makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(certPath, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := createCA(certPath, keyPath); err == nil {
		t.Fatal("expected error")
	}
	if fileExists(keyPath) {
		t.Fatal("key left behind without a certificate")
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != "ca.crt" {
			t.Fatalf("stray file %s", e.Name())
		}
	}
}
