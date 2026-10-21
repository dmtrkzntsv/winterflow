package main

import (
	"bytes"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestCSRSubcommand(t *testing.T) {
	key := filepath.Join(t.TempDir(), "sub", "hub.key")
	var out bytes.Buffer
	if err := runCSR([]string{"--key", key, "--host", "hub-02.example.com"}, &out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(key)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", st.Mode().Perm())
	}
	kb, _ := os.ReadFile(key)
	kblock, _ := pem.Decode(kb)
	if kblock == nil || kblock.Type != "EC PRIVATE KEY" {
		t.Fatal("key is not an EC PRIVATE KEY PEM")
	}
	ec, err := x509.ParseECPrivateKey(kblock.Bytes)
	if err != nil || ec.Curve != elliptic.P256() {
		t.Fatalf("key: %v", err)
	}
	block, _ := pem.Decode(out.Bytes())
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		t.Fatalf("stdout is not a CSR: %q", out.String())
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatal(err)
	}
	if csr.Subject.CommonName != "hub-02.example.com" {
		t.Fatalf("CN %q", csr.Subject.CommonName)
	}
	if bytes.Contains(out.Bytes(), []byte("PRIVATE")) {
		t.Fatal("stdout must not carry the key")
	}

	// A rerun keeps the existing key (the CSR is made from it again).
	var again bytes.Buffer
	if err := runCSR([]string{"--key", key, "--host", "hub-02.example.com"}, &again); err != nil {
		t.Fatal(err)
	}
	kb2, _ := os.ReadFile(key)
	if !bytes.Equal(kb, kb2) {
		t.Fatal("existing key was replaced")
	}
}

func TestCSRSubcommandNeedsFlags(t *testing.T) {
	if err := runCSR([]string{"--host", "h"}, &bytes.Buffer{}); err == nil {
		t.Fatal("no --key: want error")
	}
	if err := runCSR([]string{"--key", filepath.Join(t.TempDir(), "k")}, &bytes.Buffer{}); err == nil {
		t.Fatal("no --host: want error")
	}
}
