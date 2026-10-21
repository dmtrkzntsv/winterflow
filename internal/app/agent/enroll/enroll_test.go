package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	csr      string
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
		s.csr = b["csr"]
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
			d["certificate"] = "CERT"
			d["ca_certificate"] = "CA"
			d["hub_addr"] = "hub-01.winterflow.dev:50051"
		}
		reply(w, 200, d)
	})
	return mux
}

func opts(url, dir string, out *bytes.Buffer) options {
	return options{url: url, certDir: dir, envOut: filepath.Join(dir, "agent.env"), hostname: "box",
		pollEvery: time.Millisecond, sleep: func(time.Duration) {}, backoff: func(int) time.Duration { return 0 }, out: out}
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
	if !strings.Contains(s.csr, "CERTIFICATE REQUEST") {
		t.Fatalf("csr: %q", s.csr)
	}
}

func TestEnrollRefreshesExpiredCode(t *testing.T) {
	s := &script{t: t, statuses: []string{"expired", "claimed"}}
	srv := httptest.NewServer(s.handler())
	defer srv.Close()
	var out bytes.Buffer
	code, err := run(context.Background(), opts(srv.URL, t.TempDir(), &out))
	if code != ExitOK || err != nil || !strings.Contains(out.String(), "BBBBBB") || s.refresh != 1 {
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
	if code != ExitOK || err != nil || s.starts != 1 {
		t.Fatalf("code=%d err=%v starts=%d", code, err, s.starts)
	}
}

func TestMainRequiresFlags(t *testing.T) {
	var o, e bytes.Buffer
	if Main(nil, &o, &e) != ExitError {
		t.Fatal("want error")
	}
}
