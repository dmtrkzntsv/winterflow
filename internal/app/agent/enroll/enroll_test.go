package enroll

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type script struct {
	t        *testing.T
	statuses []string // states returned by successive status calls
	fail502  int32    // start calls answered 502 first
	starts   int32
	refresh  int32
	status   int32
	mu       sync.Mutex
	other    bool // sign a foreign key instead of the posted CSR
	csr      string
	hubAddr  string // overrides the claimed hub address
	serverID string // overrides the claimed server id
}

func (s *script) setCSR(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.other {
		s.csr = v
	}
}
func (s *script) getCSR() string { s.mu.Lock(); defer s.mu.Unlock(); return s.csr }

// sign issues a certificate for the stored CSR from a throwaway CA.
func (s *script) sign() (string, string) {
	block, _ := pem.Decode([]byte(s.getCSR()))
	req, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		s.t.Error(err)
		return "", ""
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "srv-1"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caCert, req.PublicKey, caKey)
	if err != nil {
		s.t.Error(err)
	}
	p := func(d []byte) string { return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})) }
	return p(der), p(caDER)
}

func (s *script) handler() http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, data any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": code == 200, "data": data})
	}
	mux.HandleFunc("/api/v1/agent/enroll", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		s.setCSR(b["csr"])
		if atomic.AddInt32(&s.fail502, -1) >= 0 {
			reply(w, 502, nil)
			return
		}
		atomic.AddInt32(&s.starts, 1)
		reply(w, 200, map[string]any{"enrollment_id": "e1", "poll_secret": "sec", "code": "AAAAAA",
			"expires_at": time.Now().Add(10 * time.Minute), "codes_left": 9})
	})
	mux.HandleFunc("/api/v1/agent/enroll/refresh", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&s.refresh, 1)
		reply(w, 200, map[string]any{"enrollment_id": "e1", "poll_secret": "sec", "code": "BBBBBB",
			"expires_at": time.Now().Add(10 * time.Minute), "codes_left": 8})
	})
	mux.HandleFunc("/api/v1/agent/enroll/status", func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&s.status, 1)) - 1
		st := s.statuses[min(i, len(s.statuses)-1)]
		d := map[string]any{"state": st}
		if st == "claimed" {
			d["server_id"] = "srv-1"
			if s.serverID != "" {
				d["server_id"] = s.serverID
			}
			d["certificate"], d["ca_certificate"] = s.sign()
			d["hub_addr"] = "hub-01.winterflow.dev:50051"
			if s.hubAddr != "" {
				d["hub_addr"] = s.hubAddr
			}
		}
		reply(w, 200, d)
	})
	return mux
}

func opts(url, dir string, out *bytes.Buffer) options {
	return options{url: url, certDir: dir, envOut: filepath.Join(dir, "agent.env"), hostname: "box",
		pollEvery: time.Millisecond, sleep: func(context.Context, time.Duration) {}, backoff: func(int) time.Duration { return 0 }, out: out}
}

func TestEnrollHappyPath(t *testing.T) {
	s := &script{t: t, statuses: []string{"pending", "claimed"}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	dir := t.TempDir()
	var out bytes.Buffer
	code, err := run(context.Background(), opts(srv.URL, dir, &out))
	if code != ExitOK || err != nil {
		t.Fatalf("code=%d err=%v out=%s", code, err, out.String())
	}
	for f, mode := range map[string]os.FileMode{"agent.key": 0o600, "agent.crt": 0o644, "ca.crt": 0o644, "agent.env": 0o600} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil || fi.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", f, err, fi)
		}
	}
	env, _ := os.ReadFile(filepath.Join(dir, "agent.env"))
	for _, l := range []string{"HUB_HOST=hub-01.winterflow.dev", "HUB_PORT=50051", "AGENT_ID=srv-1", "WINTERFLOW_API_URL=" + srv.URL} {
		if !strings.Contains(string(env), l+"\n") {
			t.Fatalf("env missing %q: %s", l, env)
		}
	}
	if !strings.Contains(out.String(), "AAAAAA") || !strings.Contains(out.String(), "9 codes left") || !strings.Contains(out.String(), "Connected as srv-1") {
		t.Fatalf("output: %s", out.String())
	}
	if !strings.Contains(s.getCSR(), "CERTIFICATE REQUEST") {
		t.Fatalf("csr: %q", s.csr)
	}
}

func TestEnrollRefreshesExpiredCode(t *testing.T) {
	s := &script{t: t, statuses: []string{"expired", "claimed"}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	var out bytes.Buffer
	code, err := run(context.Background(), opts(srv.URL, t.TempDir(), &out))
	if code != ExitOK || err != nil || !strings.Contains(out.String(), "BBBBBB") || atomic.LoadInt32(&s.refresh) != 1 {
		t.Fatalf("code=%d err=%v out=%s", code, err, out.String())
	}
}

func TestEnrollGivesUp(t *testing.T) {
	s := &script{t: t, statuses: []string{"failed"}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	dir := t.TempDir()
	var out bytes.Buffer
	code, _ := run(context.Background(), opts(srv.URL, dir, &out))
	if code != ExitGaveUp || !strings.Contains(out.String(), "No code was entered. Run the installer again to get a new one.") {
		t.Fatalf("code=%d out=%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.crt")); err == nil {
		t.Fatal("agent.crt written")
	}
}

func TestEnrollGivesUpOnNoCodesLeft(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/enroll", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(409) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var out bytes.Buffer
	if code, _ := run(context.Background(), opts(srv.URL, t.TempDir(), &out)); code != ExitGaveUp {
		t.Fatalf("code=%d", code)
	}
}

func TestEnrollAlreadyEnrolled(t *testing.T) {
	dir := t.TempDir()
	crt := filepath.Join(dir, "agent.crt")
	_ = os.WriteFile(crt, []byte("old"), 0o644)
	var out bytes.Buffer
	if code, _ := run(context.Background(), opts("https://example.invalid", dir, &out)); code != ExitAlreadyEnrolled {
		t.Fatalf("code=%d", code)
	}
	if b, _ := os.ReadFile(crt); string(b) != "old" {
		t.Fatal("cert changed")
	}
	if _, err := os.Stat(filepath.Join(dir, "agent.key")); err == nil {
		t.Fatal("key written")
	}
}

func TestEnrollRejectsHTTPURL(t *testing.T) {
	var out bytes.Buffer
	if code, err := run(context.Background(), opts("http://api.example.com", t.TempDir(), &out)); code != ExitError || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestEnrollTransientErrorRetries(t *testing.T) {
	s := &script{t: t, statuses: []string{"claimed"}, fail502: 1}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	var out bytes.Buffer
	code, err := run(context.Background(), opts(srv.URL, t.TempDir(), &out))
	if code != ExitOK || err != nil || atomic.LoadInt32(&s.starts) != 1 {
		t.Fatalf("code=%d err=%v starts=%d", code, err, atomic.LoadInt32(&s.starts))
	}
}

func TestMainRequiresFlags(t *testing.T) {
	var o, e bytes.Buffer
	if Main(nil, &o, &e) != ExitError {
		t.Fatal("want error")
	}
}

func TestEnrollDoesNotFollowRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://203.0.113.9/api/v1/agent/enroll", http.StatusTemporaryRedirect)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var out bytes.Buffer
	code, err := run(context.Background(), opts(srv.URL, t.TempDir(), &out))
	if code != ExitError || err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestCheckURL(t *testing.T) {
	for u, ok := range map[string]bool{
		"https://api.winterflow.dev": true, "http://localhost:8080": true, "http://[::1]:8080": true,
		"http://127.0.0.1": true, "http://localhost.evil.com": false, "http://u@localhost": false,
		"ftp://localhost": false, "http://example.com": false, "https://u@example.com": false,
	} {
		if _, err := checkURL(u); (err == nil) != ok {
			t.Errorf("%s: err=%v want ok=%v", u, err, ok)
		}
	}
}

func TestEnrollNeverPrintsPollSecret(t *testing.T) {
	s := &script{t: t, statuses: []string{"expired", "claimed"}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	var out bytes.Buffer
	run(context.Background(), opts(srv.URL, t.TempDir(), &out))
	if strings.Contains(out.String(), "sec\n") || strings.Contains(out.String(), "poll_secret") {
		t.Fatal(out.String())
	}
}

func TestEnrollRejectsMismatchedCertificate(t *testing.T) {
	s := &script{t: t, statuses: []string{"claimed"}, other: true}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k)
	s.csr = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	dir := t.TempDir()
	var out bytes.Buffer
	code, err := run(context.Background(), opts(srv.URL, dir, &out))
	if code != ExitError || err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("code=%d err=%v", code, err)
	}
	for _, f := range []string{"agent.crt", "ca.crt", "agent.env"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s written", f)
		}
	}
}

func TestEnrollRejectsHostileServerValues(t *testing.T) {
	for name, c := range map[string]struct{ addr, id string }{
		"host newline": {"hub\nLD_PRELOAD=/x:50051", "srv-1"},
		"host space":   {"hub host:50051", "srv-1"},
		"port zero":    {"hub.example.com:0", "srv-1"},
		"port big":     {"hub.example.com:70000", "srv-1"},
		"id newline":   {"hub.example.com:50051", "srv\nLD_PRELOAD=/x"},
		"id empty chr": {"hub.example.com:50051", "srv 1"},
	} {
		t.Run(name, func(t *testing.T) {
			s := &script{t: t, statuses: []string{"claimed"}, hubAddr: c.addr, serverID: c.id}
			srv := httptest.NewServer(s.handler())
			defer srv.Close()
			dir := t.TempDir()
			var out bytes.Buffer
			code, err := run(context.Background(), opts(srv.URL, dir, &out))
			if code != ExitError || err == nil {
				t.Fatalf("code=%d err=%v", code, err)
			}
			for _, f := range []string{"agent.crt", "agent.env"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
					t.Fatalf("%s written", f)
				}
			}
		})
	}
}

func TestEnrollAcceptsIPv6Hub(t *testing.T) {
	s := &script{t: t, statuses: []string{"claimed"}, hubAddr: "[2001:db8::1]:50051"}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	var out bytes.Buffer
	if code, err := run(context.Background(), opts(srv.URL, t.TempDir(), &out)); code != ExitOK {
		t.Fatalf("code=%d err=%v", code, err)
	}
}
