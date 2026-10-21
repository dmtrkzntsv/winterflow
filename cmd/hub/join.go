package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Exit codes of the join subcommand.
const (
	exitOther   = 1
	exitRefused = 2 // the control plane refused the token, request or address
)

// joinError carries the process exit code.
type joinError struct {
	code int
	err  error
}

func (e *joinError) Error() string { return e.err.Error() }
func (e *joinError) Unwrap() error { return e.err }

func joinFail(code int, format string, a ...any) error {
	return &joinError{code: code, err: fmt.Errorf(format, a...)}
}

// joinTimeout bounds the whole enrollment request.
const joinTimeout = 30 * time.Second

// runJoin is the hidden `join` subcommand the hub installer calls. It makes
// (or keeps) the hub's P-256 key in --cert-dir, redeems the one-time token
// (read from the WINTERFLOW_HUB_TOKEN variable, else the first line of in) at
// the API, checks that the returned certificate matches the key and chains
// to the returned CA, and stores ca.crt then hub.crt (written last).
func runJoin(args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	apiURL := fs.String("api-url", "", "control plane API URL")
	addr := fs.String("public-addr", "", "hub public address host:port")
	dir := fs.String("cert-dir", "", "directory for hub.key, hub.crt, ca.crt")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *apiURL == "" || *addr == "" || *dir == "" {
		return errors.New("join: --api-url, --public-addr and --cert-dir are required")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || host == "" {
		return fmt.Errorf("join: --public-addr must be host:port, got %q", *addr)
	}
	endpoint, err := joinEndpoint(*apiURL)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(os.Getenv("WINTERFLOW_HUB_TOKEN"))
	if token == "" {
		line, _ := bufio.NewReader(in).ReadString('\n')
		token = strings.TrimSpace(line)
	}
	if token == "" {
		return errors.New("join: no token (stdin or WINTERFLOW_HUB_TOKEN)")
	}

	keyPath := filepath.Join(*dir, "hub.key")
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: host},
	}, key)
	if err != nil {
		return err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})

	certPEM, caPEM, err := postJoin(endpoint, token, string(csrPEM), *addr)
	if err != nil {
		return err
	}
	if err := verifyJoin(certPEM, caPEM, keyPath); err != nil {
		return joinFail(exitOther, "the control plane's certificate is unusable: %v", err)
	}
	if err := writeAtomic(filepath.Join(*dir, "ca.crt"), []byte(caPEM)); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(*dir, "hub.crt"), []byte(certPEM)); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "joined: certificate for %s stored in %s\n", host, *dir)
	return err
}

// joinEndpoint validates the API URL (https; http only to a loopback host)
// and returns the enroll URL.
func joinEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("join: invalid --api-url %q", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		h := u.Hostname()
		if h != "localhost" && h != "::1" && !strings.HasPrefix(h, "127.") {
			return "", errors.New("join: --api-url must be https (http only to localhost)")
		}
	default:
		return "", errors.New("join: --api-url must be https (http only to localhost)")
	}
	return strings.TrimRight(u.String(), "/") + "/api/v1/hub/enroll", nil
}

func postJoin(endpoint, token, csr, addr string) (cert, ca string, err error) {
	body, _ := json.Marshal(map[string]string{"token": token, "csr": csr, "public_addr": addr})
	client := &http.Client{
		Timeout: joinTimeout,
		// A redirect could carry the token elsewhere: never follow one.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", joinFail(exitOther, "could not reach %s: %v", endpoint, err)
	}
	defer resp.Body.Close()
	var env struct {
		Message string `json:"message"`
		Data    struct {
			Certificate   string `json:"certificate"`
			CACertificate string `json:"ca_certificate"`
		} `json:"data"`
	}
	decErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env)
	switch {
	case resp.StatusCode == http.StatusBadRequest:
		return "", "", joinFail(exitRefused, "the control plane refused the join: %s", env.Message)
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", "", joinFail(exitOther, "the control plane is rate limiting joins; wait a minute and retry")
	case resp.StatusCode != http.StatusOK:
		return "", "", joinFail(exitOther, "the control plane answered HTTP %d to the join: %s", resp.StatusCode, env.Message)
	case decErr != nil:
		return "", "", joinFail(exitOther, "unreadable answer from the control plane: %v", decErr)
	}
	return env.Data.Certificate, env.Data.CACertificate, nil
}

// verifyJoin checks the certificate parses, matches the key and chains to the CA.
func verifyJoin(certPEM, caPEM, keyPath string) error {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), keyPEM)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return errors.New("the CA certificate is not valid PEM")
	}
	inter := x509.NewCertPool()
	for _, c := range pair.Certificate[1:] {
		if ic, err := x509.ParseCertificate(c); err == nil {
			inter.AddCert(ic)
		}
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	return err
}

// writeAtomic writes a world-readable file by renaming a temp file in place.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".join-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
