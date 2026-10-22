package hub

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"winterflow/pkg/cert"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

func selfSigned(t *testing.T, cn string) *x509.Certificate {
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
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func peerCtx(chains [][]*x509.Certificate) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: chains}},
	})
}

func TestPeerIdentity(t *testing.T) {
	c := selfSigned(t, "srv-123")
	want, err := cert.Fingerprint(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})))
	if err != nil {
		t.Fatal(err)
	}

	id, fp, err := peerIdentity(peerCtx([][]*x509.Certificate{{c}}))
	if err != nil {
		t.Fatal(err)
	}
	if id != "srv-123" || fp != want {
		t.Fatalf("got (%q, %q), want (srv-123, %q)", id, fp, want)
	}

	cases := map[string]context.Context{
		"no peer":     context.Background(),
		"not tls":     peer.NewContext(context.Background(), &peer.Peer{}),
		"no chains":   peerCtx(nil),
		"empty chain": peerCtx([][]*x509.Certificate{{}}),
		"empty cn":    peerCtx([][]*x509.Certificate{{selfSigned(t, "")}}),
	}
	for name, ctx := range cases {
		if _, _, err := peerIdentity(ctx); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
