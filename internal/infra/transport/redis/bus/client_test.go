package redisbus

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
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/pkg/config"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, cn string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca *testCA) serverCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "redis"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsListener accepts one connection, completes (or fails) the handshake and
// reports its error; on success it answers every command with an error reply.
func tlsListener(t *testing.T, cert tls.Certificate) (addr string, handshake chan error) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	handshake = make(chan error, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				err := c.(*tls.Conn).Handshake()
				handshake <- err
				if err != nil {
					return
				}
				buf := make([]byte, 1024)
				for {
					if _, err := c.Read(buf); err != nil {
						return
					}
					_, _ = c.Write([]byte(strings.Repeat("-ERR unknown command\r\n", 4)))
				}
			}()
		}
	}()
	return ln.Addr().String(), handshake
}

func tlsConfig(t *testing.T, addr string, caPEM []byte) *config.ServerConfig {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(p, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REDIS_ADDR", addr)
	t.Setenv("REDIS_TLS", "true")
	t.Setenv("REDIS_CA_CERT_PATH", p)
	return config.NewServerConfig("distributed")
}

func TestNewRedisClientTLS(t *testing.T) {
	ca := newTestCA(t, "good")
	addr, handshake := tlsListener(t, ca.serverCert(t))

	rc, err := NewRedisClient(tlsConfig(t, addr, ca.pem))
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = Ping(ctx, rc) // the fake answers with an error reply; only the handshake matters
	if err := <-handshake; err != nil {
		t.Fatalf("handshake with the matching CA failed: %v", err)
	}

	other := newTestCA(t, "other")
	rc2, err := NewRedisClient(tlsConfig(t, addr, other.pem))
	if err != nil {
		t.Fatal(err)
	}
	defer rc2.Close()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	err = Ping(ctx2, rc2)
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("want a certificate verification error with a different CA, got %v", err)
	}
}

func TestNewRedisClientMissingCA(t *testing.T) {
	t.Setenv("REDIS_ADDR", "127.0.0.1:6380")
	t.Setenv("REDIS_TLS", "true")
	t.Setenv("REDIS_CA_CERT_PATH", filepath.Join(t.TempDir(), "absent.crt"))
	if _, err := NewRedisClient(config.NewServerConfig("distributed")); err == nil {
		t.Fatal("want an error for a missing CA file")
	}

	bad := filepath.Join(t.TempDir(), "bad.crt")
	_ = os.WriteFile(bad, []byte("not pem"), 0o600)
	t.Setenv("REDIS_CA_CERT_PATH", bad)
	if _, err := NewRedisClient(config.NewServerConfig("distributed")); err == nil {
		t.Fatal("want an error for an unparsable CA file")
	}

	t.Setenv("REDIS_TLS", "false")
	rc, err := NewRedisClient(config.NewServerConfig("distributed"))
	if err != nil {
		t.Fatalf("TLS off must not read the CA: %v", err)
	}
	rc.Close()
}

func TestNewRedisClientUsername(t *testing.T) {
	t.Setenv("REDIS_ADDR", "127.0.0.1:6380")
	t.Setenv("REDIS_TLS", "false")
	t.Setenv("REDIS_PASSWORD", "pw")
	t.Setenv("REDIS_USERNAME", "hub:hub-01.winterflow.dev")
	rc, err := NewRedisClient(config.NewServerConfig("distributed"))
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if o := rc.Options(); o.Username != "hub:hub-01.winterflow.dev" || o.Password != "pw" {
		t.Fatalf("options: username %q password %q", o.Username, o.Password)
	}

	t.Setenv("REDIS_USERNAME", "")
	rc2, err := NewRedisClient(config.NewServerConfig("distributed"))
	if err != nil {
		t.Fatal(err)
	}
	defer rc2.Close()
	if u := rc2.Options().Username; u != "" {
		t.Fatalf("unset REDIS_USERNAME: username %q, want the default user", u)
	}
}
