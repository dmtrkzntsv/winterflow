package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &testCA{c, key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

// sign issues a leaf for pub.
func (ca *testCA) sign(t *testing.T, pub *ecdsa.PublicKey) string {
	t.Helper()
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "hub"}, DNSNames: []string{"hub-02.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func ok(w http.ResponseWriter, cert, ca string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"certificate": cert, "ca_certificate": ca}})
}

func join(t *testing.T, url, dir, token string) error {
	t.Helper()
	t.Setenv("WINTERFLOW_HUB_TOKEN", "")
	return runJoin([]string{"--api-url", url, "--public-addr", "hub-02.example.com:50051", "--cert-dir", dir},
		strings.NewReader(token+"\n"), &bytes.Buffer{})
}

func TestJoinHappyPath(t *testing.T) {
	ca := newTestCA(t)
	var got struct{ Token, CSR, PublicAddr string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hub/enroll" {
			t.Errorf("path %s", r.URL.Path)
		}
		var b struct {
			Token      string `json:"token"`
			CSR        string `json:"csr"`
			PublicAddr string `json:"public_addr"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		got.Token, got.CSR, got.PublicAddr = b.Token, b.CSR, b.PublicAddr
		blk, _ := pem.Decode([]byte(b.CSR))
		csr, err := x509.ParseCertificateRequest(blk.Bytes)
		if err != nil {
			t.Error(err)
			return
		}
		ok(w, ca.sign(t, csr.PublicKey.(*ecdsa.PublicKey)), ca.pem)
	}))
	defer srv.Close()
	dir := filepath.Join(t.TempDir(), "certs")
	if err := join(t, srv.URL, dir, "wfh_tok"); err != nil {
		t.Fatal(err)
	}
	if got.Token != "wfh_tok" || got.PublicAddr != "hub-02.example.com:50051" {
		t.Fatalf("request %+v", got)
	}
	for _, f := range []string{"hub.crt", "ca.crt", "hub.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, "hub.key")); st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode().Perm())
	}
}

func TestJoinTokenFromEnv(t *testing.T) {
	ca := newTestCA(t)
	var tok string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ Token, CSR string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		tok = b.Token
		blk, _ := pem.Decode([]byte(b.CSR))
		csr, _ := x509.ParseCertificateRequest(blk.Bytes)
		ok(w, ca.sign(t, csr.PublicKey.(*ecdsa.PublicKey)), ca.pem)
	}))
	defer srv.Close()
	t.Setenv("WINTERFLOW_HUB_TOKEN", "wfh_env")
	err := runJoin([]string{"--api-url", srv.URL, "--public-addr", "h.example.com:1", "--cert-dir", t.TempDir()}, strings.NewReader(""), &bytes.Buffer{})
	if err != nil || tok != "wfh_env" {
		t.Fatalf("err %v tok %q", err, tok)
	}
}

func TestJoinRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"message":"invalid or expired token","data":null}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	err := join(t, srv.URL, dir, "wfh_x")
	var je *joinError
	if !errors.As(err, &je) || je.code != exitRefused || !strings.Contains(err.Error(), "invalid or expired token") {
		t.Fatalf("err %v", err)
	}
	if _, e := os.Stat(filepath.Join(dir, "hub.crt")); e == nil {
		t.Fatal("hub.crt written on refusal")
	}
}

func TestJoinServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	var je *joinError
	if err := join(t, srv.URL, t.TempDir(), "wfh_x"); !errors.As(err, &je) || je.code != exitOther {
		t.Fatalf("err %v", err)
	}
}

func TestJoinMismatchedCertificate(t *testing.T) {
	ca := newTestCA(t)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok(w, ca.sign(t, &other.PublicKey), ca.pem) // for a key the hub does not hold
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := join(t, srv.URL, dir, "wfh_x"); err == nil {
		t.Fatal("mismatched certificate accepted")
	}
	if _, e := os.Stat(filepath.Join(dir, "hub.crt")); e == nil {
		t.Fatal("hub.crt written")
	}
}

func TestJoinWrongCA(t *testing.T) {
	ca, other := newTestCA(t), newTestCA(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct{ CSR string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		blk, _ := pem.Decode([]byte(b.CSR))
		csr, _ := x509.ParseCertificateRequest(blk.Bytes)
		ok(w, ca.sign(t, csr.PublicKey.(*ecdsa.PublicKey)), other.pem)
	}))
	defer srv.Close()
	if err := join(t, srv.URL, t.TempDir(), "wfh_x"); err == nil {
		t.Fatal("certificate not chaining to the returned CA accepted")
	}
}

func TestJoinDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/api/v1/hub/enroll", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if err := join(t, srv.URL, t.TempDir(), "wfh_x"); err == nil {
		t.Fatal("redirect treated as success")
	}
	if hits.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestJoinRefusesPlainHTTPToRemoteHost(t *testing.T) {
	if err := join(t, "http://api.example.com", t.TempDir(), "wfh_x"); err == nil {
		t.Fatal("plain http to a remote host accepted")
	}
}

func TestJoinNeedsToken(t *testing.T) {
	if err := join(t, "https://api.example.com", t.TempDir(), ""); err == nil {
		t.Fatal("no token: want error")
	}
}

func TestJoinKeepsExistingKey(t *testing.T) {
	dir := t.TempDir()
	key, err := loadOrCreateKey(filepath.Join(dir, "hub.key"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadOrCreateKey(filepath.Join(dir, "hub.key"))
	if err != nil || !again.Equal(key) {
		t.Fatal("existing key replaced")
	}
}
