// Package enrolltest holds the fixtures the enrollment HTTP tests share: a
// real P-256 CSR, a CA-backed signer and a fixed hub picker.
package enrolltest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"path/filepath"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/cert"
	certpkg "winterflow/pkg/cert"
)

// CSR returns a PEM certificate request for a fresh key on curve.
func CSR(t testing.TB, curve elliptic.Curve) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "ignored"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

// Signer creates a throwaway CA in a temp dir and loads it.
func Signer(t testing.TB) *cert.CASigner {
	t.Helper()
	dir := t.TempDir()
	if _, err := certpkg.EnsureHubPKI(dir, []string{"hub.test"}); err != nil {
		t.Fatal(err)
	}
	s, err := cert.NewCASigner(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Picker always answers Hub, or Err when set.
type Picker struct {
	Hub model.Hub
	Err error
}

func (p Picker) Pick(context.Context) (model.Hub, error) { return p.Hub, p.Err }

// DefaultHub is the hub a Picker{Hub: DefaultHub} hands out.
var DefaultHub = model.Hub{ID: "hub-01.test", PublicAddr: "hub-01.test:443"}
