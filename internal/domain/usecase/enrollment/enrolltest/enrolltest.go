// Package enrolltest holds the fixtures the enrollment HTTP tests share: a
// real P-256 CSR, a CA-backed signer, a fixed hub picker and in-memory hub Redis users.
package enrolltest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
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

// Pins answers a fixed trusted hub certificate set, or Err when set.
type Pins struct {
	Fingerprints []string
	Err          error
}

func (p Pins) Active(context.Context) ([]string, error) { return p.Fingerprints, p.Err }

// RedisUsers is an in-memory port.HubRedisUsers: Ensure gives each hub id
// "hub:<id>" and a new password per call; Delete forgets it. Both refuse
// an id port.CheckHubRedisID refuses, as the Redis one does. Err, when set,
// fails both.
type RedisUsers struct {
	mu      sync.Mutex
	n       int
	Err     error
	Users   map[string]string // username → current password
	Deleted []string          // hub ids, in Delete order
}

var _ port.HubRedisUsers = (*RedisUsers)(nil)

// Ensure creates or resets the hub's user.
func (r *RedisUsers) Ensure(_ context.Context, hubID string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return "", "", r.Err
	}
	if err := port.CheckHubRedisID(hubID); err != nil {
		return "", "", err
	}
	if r.Users == nil {
		r.Users = map[string]string{}
	}
	r.n++
	user, pass := "hub:"+hubID, fmt.Sprintf("pw-%s-%d", hubID, r.n)
	r.Users[user] = pass
	return user, pass, nil
}

// Delete removes the hub's user (a missing one is not an error).
func (r *RedisUsers) Delete(_ context.Context, hubID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	if err := port.CheckHubRedisID(hubID); err != nil {
		return err
	}
	delete(r.Users, "hub:"+hubID)
	r.Deleted = append(r.Deleted, hubID)
	return nil
}

// Password is the hub's current password ("" when it has no user).
func (r *RedisUsers) Password(hubID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Users["hub:"+hubID]
}
