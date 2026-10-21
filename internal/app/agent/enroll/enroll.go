// Package enroll implements the hidden `winterflow-agent enroll` subcommand:
// it generates the agent key, shows a code the user enters in the web app,
// and stores the certificate the API signs once the code is claimed.
package enroll

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Exit codes.
const (
	ExitOK              = 0
	ExitError           = 1
	ExitGaveUp          = 2
	ExitAlreadyEnrolled = 3
)

const defaultURL = "https://api.winterflow.dev"

// maxTransient bounds consecutive transient failures of one call.
const maxTransient = 40

type options struct {
	url, certDir, envOut, hostname string
	pollEvery                      time.Duration
	sleep                          func(time.Duration)
	backoff                        func(attempt int) time.Duration
	out                            io.Writer
}

// Main runs the subcommand and returns the process exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	fs.SetOutput(stderr)
	host, _ := os.Hostname()
	o := options{}
	fs.StringVar(&o.url, "url", defaultURL, "Winterflow API URL")
	fs.StringVar(&o.certDir, "cert-dir", "", "directory for agent.key, agent.crt and ca.crt (required)")
	fs.StringVar(&o.envOut, "env-out", "", "file receiving the agent's environment lines (required)")
	fs.StringVar(&o.hostname, "hostname", host, "name shown next to the code")
	if err := fs.Parse(args); err != nil {
		return ExitError
	}
	if o.certDir == "" || o.envOut == "" {
		fmt.Fprintln(stderr, "enroll: --cert-dir and --env-out are required")
		return ExitError
	}
	o.out = stdout
	o.pollEvery = 3 * time.Second
	o.sleep = time.Sleep
	o.backoff = func(n int) time.Duration {
		d := time.Duration(1<<min(n, 5)) * time.Second
		return min(d, 30*time.Second)
	}
	ctx, cancel := signalContext()
	defer cancel()
	code, err := run(ctx, o)
	if err != nil {
		fmt.Fprintln(stderr, "enroll:", err)
	}
	return code
}

func checkURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		h := u.Hostname()
		ip := net.ParseIP(h)
		if h != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("the API URL must be https (http is allowed only for loopback)")
		}
	default:
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func run(ctx context.Context, o options) (int, error) {
	base, err := checkURL(o.url)
	if err != nil {
		return ExitError, err
	}
	certPath := filepath.Join(o.certDir, "agent.crt")
	if _, err := os.Stat(certPath); err == nil {
		fmt.Fprintf(o.out, "Already enrolled (%s exists); nothing changed.\n", certPath)
		return ExitAlreadyEnrolled, nil
	}
	if err := os.MkdirAll(o.certDir, 0o755); err != nil {
		return ExitError, err
	}
	csr, err := generateKey(filepath.Join(o.certDir, "agent.key"), o.hostname)
	if err != nil {
		return ExitError, err
	}
	c := newClient(base)

	var res codeResult
	if err := withRetry(ctx, o, func() (err error) { res, err = c.start(ctx, csr, o.hostname); return }); err != nil {
		return failure(o, err)
	}
	printCode(o.out, res)

	for {
		select {
		case <-ctx.Done():
			return ExitError, ctx.Err()
		default:
		}
		o.sleep(o.pollEvery)
		var st statusResult
		if err := withRetry(ctx, o, func() (err error) { st, err = c.status(ctx, res.PollSecret); return }); err != nil {
			return failure(o, err)
		}
		switch st.State {
		case "pending":
		case "expired":
			var next codeResult
			if err := withRetry(ctx, o, func() (err error) { next, err = c.refresh(ctx, res.PollSecret); return }); err != nil {
				return failure(o, err)
			}
			next.PollSecret = res.PollSecret
			res = next
			printCode(o.out, res)
		case "failed":
			return gaveUp(o)
		case "claimed":
			if err := store(o, st); err != nil {
				return ExitError, err
			}
			fmt.Fprintf(o.out, "Connected as %s\n", st.ServerID)
			return ExitOK, nil
		default:
			return ExitError, fmt.Errorf("unexpected enrollment state %q", st.State)
		}
	}
}

func gaveUp(o options) (int, error) {
	fmt.Fprintln(o.out, "No code was entered. Run the installer again to get a new one.")
	return ExitGaveUp, nil
}

func failure(o options, err error) (int, error) {
	if errors.Is(err, errNoCodes) {
		return gaveUp(o)
	}
	return ExitError, err
}

// withRetry retries transient failures with backoff; other errors return.
func withRetry(ctx context.Context, o options, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil || !errors.Is(err, errTransient) {
			return err
		}
		if attempt >= maxTransient {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		o.sleep(o.backoff(attempt))
	}
}

func printCode(w io.Writer, r codeResult) {
	mins := int(time.Until(r.ExpiresAt).Round(time.Minute) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	line := "+------------------------------------------+"
	fmt.Fprintf(w, "\n%s\n|  Enter this code in Winterflow:  %-8s|\n|  Valid for %d minutes, %d codes left%s|\n%s\n\n",
		line, r.Code, mins, r.CodesLeft, strings.Repeat(" ", max(0, 42-len(fmt.Sprintf("  Valid for %d minutes, %d codes left", mins, r.CodesLeft)))), line)
}

// generateKey writes a fresh P-256 key (0600) and returns its PEM CSR.
func generateKey(path, hostname string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	if err := writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return "", err
	}
	cn := hostname
	if cn == "" {
		cn = "winterflow-agent"
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkixName(cn),
	}, key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})), nil
}

func store(o options, st statusResult) error {
	if st.Certificate == "" || st.CACertificate == "" || st.ServerID == "" {
		return errors.New("the server returned an incomplete certificate")
	}
	host, port, err := net.SplitHostPort(st.HubAddr)
	if err != nil {
		return fmt.Errorf("invalid hub address %q: %w", st.HubAddr, err)
	}
	if err := writeFile(filepath.Join(o.certDir, "ca.crt"), []byte(st.CACertificate), 0o644); err != nil {
		return err
	}
	env := fmt.Sprintf("HUB_HOST=%s\nHUB_PORT=%s\nAGENT_ID=%s\nWINTERFLOW_API_URL=%s\n", host, port, st.ServerID, strings.TrimRight(o.url, "/"))
	if err := writeFile(o.envOut, []byte(env), 0o600); err != nil {
		return err
	}
	// The certificate goes last: its presence marks the agent as enrolled.
	return writeFile(filepath.Join(o.certDir, "agent.crt"), []byte(st.Certificate), 0o644)
}

// writeFile writes to a temp name beside path and renames it into place.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".enroll-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
