package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// runCSR is the hidden `csr` subcommand the hub installer calls: it makes a
// P-256 key at --key (0600; an existing key is kept) and prints a PEM
// certificate request for --host on out. The key never leaves the file.
func runCSR(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("csr", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	keyPath := fs.String("key", "", "path of the private key (created if absent)")
	host := fs.String("host", "", "hub host, used as the request's common name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || *host == "" {
		return errors.New("csr: --key and --host are required")
	}
	key, err := loadOrCreateKey(*keyPath)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: *host},
	}, key)
	if err != nil {
		return err
	}
	return pem.Encode(out, &pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func loadOrCreateKey(path string) (*ecdsa.PrivateKey, error) {
	if b, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("%s is not a PEM file", path)
		}
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if key.Curve != elliptic.P256() {
			return nil, fmt.Errorf("%s: expected a P-256 key", path)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	if err := pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}); err != nil {
		_ = f.Close()
		return nil, err
	}
	return key, f.Close()
}
