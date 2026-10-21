package agentauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/cert"
)

type fakeRepo struct {
	servers map[string]bool
	hubs    map[string]string
	cert    map[string]string
	err     error
}

func (f fakeRepo) ServerHub(_ context.Context, id string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if !f.servers[id] {
		return "", model.ErrNotFound
	}
	return f.hubs[id], nil
}

func (f fakeRepo) ActiveServerCertificate(_ context.Context, id string) (string, error) {
	c, ok := f.cert[id]
	if !ok {
		return "", model.ErrNotFound
	}
	return c, nil
}

func newCert(t *testing.T, cn string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn}, NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	fp, err := cert.Fingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, fp
}

func TestCheckAllowed(t *testing.T) {
	pemA, fpA := newCert(t, "srv-1")
	repo := fakeRepo{servers: map[string]bool{"srv-1": true}, cert: map[string]string{"srv-1": pemA}}
	v, _, err := Check(context.Background(), repo, "srv-1", fpA, "")
	if err != nil || v != bus.VerdictAllowed {
		t.Fatalf("got %q, %v", v, err)
	}
	// The old registration flow stored the PEM base64-encoded.
	repo.cert["srv-1"] = base64.StdEncoding.EncodeToString([]byte(pemA))
	if v, _, err := Check(context.Background(), repo, "srv-1", fpA, ""); err != nil || v != bus.VerdictAllowed {
		t.Fatalf("base64 form: got %q, %v", v, err)
	}
}

func TestCheckUnknownServer(t *testing.T) {
	v, reason, err := Check(context.Background(), fakeRepo{}, "nope", "ab", "")
	if err != nil || v != bus.VerdictUnknown || reason != "server not found" {
		t.Fatalf("got %q %q %v", v, reason, err)
	}
}

func TestCheckWrongFingerprint(t *testing.T) {
	pemA, _ := newCert(t, "srv-1")
	_, fpB := newCert(t, "srv-1")
	repo := fakeRepo{servers: map[string]bool{"srv-1": true}, cert: map[string]string{"srv-1": pemA}}
	for _, fp := range []string{fpB, ""} {
		v, reason, err := Check(context.Background(), repo, "srv-1", fp, "")
		if err != nil || v != bus.VerdictRevoked || reason != "certificate is not active" {
			t.Fatalf("fp %q: got %q %q %v", fp, v, reason, err)
		}
	}
	// A server without any active certificate is revoked too.
	repo.cert = nil
	if v, _, _ := Check(context.Background(), repo, "srv-1", fpB, ""); v != bus.VerdictRevoked {
		t.Fatalf("no cert: got %q", v)
	}
}

func TestCheckDBErrorIsReturned(t *testing.T) {
	boom := errors.New("db down")
	_, _, err := Check(context.Background(), fakeRepo{err: boom}, "srv-1", "x", "")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckMoved(t *testing.T) {
	pemA, fpA := newCert(t, "srv-1")
	_, fpB := newCert(t, "srv-1")
	repo := fakeRepo{servers: map[string]bool{"srv-1": true}, hubs: map[string]string{"srv-1": "hub-02"}, cert: map[string]string{"srv-1": pemA}}
	v, reason, err := Check(context.Background(), repo, "srv-1", fpA, "hub-01")
	if err != nil || v != bus.VerdictMoved || reason != "server belongs to another hub" {
		t.Fatalf("got %q %q %v", v, reason, err)
	}
	if v, _, err := Check(context.Background(), repo, "srv-1", fpA, "hub-02"); err != nil || v != bus.VerdictAllowed {
		t.Fatalf("own hub: got %q %v", v, err)
	}
	// A revoked certificate is refused, never sent elsewhere.
	if v, _, _ := Check(context.Background(), repo, "srv-1", fpB, "hub-01"); v != bus.VerdictRevoked {
		t.Fatalf("revoked on another hub: got %q", v)
	}
	// A server without a hub (enrolled before hubs) is sent to ask the API.
	repo.hubs = nil
	if v, _, _ := Check(context.Background(), repo, "srv-1", fpA, "hub-01"); v != bus.VerdictMoved {
		t.Fatalf("no hub: got %q", v)
	}
}
