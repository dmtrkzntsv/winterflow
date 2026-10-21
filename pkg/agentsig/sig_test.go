package agentsig

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func newKeyCert(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "srv-1"}, NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestPayload(t *testing.T) {
	if got := string(Payload("srv-1", 1700000000, "abc")); got != "srv-1\n1700000000\nabc" {
		t.Fatalf("payload = %q", got)
	}
}

func TestSignVerify(t *testing.T) {
	key, certPEM := newKeyCert(t)
	payload := Payload("srv-1", 1700000000, "n1")
	sig, err := Sign(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(certPEM, payload, sig); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if err := Verify(certPEM, Payload("srv-1", 1700000001, "n1"), sig); err == nil {
		t.Fatal("tampered payload verified")
	}
	_, otherPEM := newKeyCert(t)
	if err := Verify(otherPEM, payload, sig); err == nil {
		t.Fatal("other key verified")
	}
	for _, bad := range []string{"", "!!!", "AAAA"} {
		if err := Verify(certPEM, payload, bad); err == nil {
			t.Fatalf("signature %q verified", bad)
		}
	}
	if err := Verify("not a pem", payload, sig); err == nil {
		t.Fatal("garbage certificate verified")
	}
}
