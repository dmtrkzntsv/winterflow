package catalogfeed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// feedServer serves catalog.json (ETag "v1") and catalog.json.sig, and
// records the requests it got.
type feedServer struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
	sig  func(w http.ResponseWriter) // nil: the signature
}

func newFeedServer(t *testing.T) *feedServer {
	t.Helper()
	s := &feedServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r)
		sig := s.sig
		s.mu.Unlock()
		switch r.URL.Path {
		case "/catalog/catalog.json":
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"format":1}`))
		case "/catalog/catalog.json.sig":
			if sig != nil {
				sig(w)
				return
			}
			_, _ = w.Write([]byte("c2lnbmF0dXJl\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *feedServer) requests() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*http.Request{}, s.reqs...)
}

func fetcher(t *testing.T, url string) *Fetcher {
	t.Helper()
	f, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFetchAndNotModified(t *testing.T) {
	s := newFeedServer(t)
	f := fetcher(t, s.URL+"/catalog/catalog.json")
	ctx := context.Background()
	got, err := f.FetchCatalog(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.NotModified || string(got.Feed) != `{"format":1}` || string(got.Sig) != "c2lnbmF0dXJl\n" || got.ETag != `"v1"` {
		t.Fatalf("fetch = %+v", got)
	}
	again, err := f.FetchCatalog(ctx, got.ETag)
	if err != nil || !again.NotModified || again.Feed != nil || again.Sig != nil || again.ETag != `"v1"` {
		t.Fatalf("second fetch = %+v, %v", again, err)
	}
	reqs := s.requests()
	if len(reqs) != 3 || reqs[0].Header.Get("User-Agent") != "winterflow" || reqs[0].Header.Get("If-None-Match") != "" ||
		reqs[1].URL.Path != "/catalog/catalog.json.sig" || reqs[2].Header.Get("If-None-Match") != `"v1"` {
		t.Fatalf("requests = %+v", reqs)
	}
}

// A feed whose catalog.json answers but whose .sig does not is an error,
// never a feed without a signature.
func TestFetchNeedsTheSignature(t *testing.T) {
	for name, c := range map[string]struct {
		sig  func(w http.ResponseWriter)
		want string
	}{
		"a missing .sig": {func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }, "catalog.json.sig: 404 Not Found"},
		"a failing .sig": {func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) }, "502 Bad Gateway"},
		"a 304 .sig":     {func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotModified) }, "304 Not Modified"},
		"a huge .sig":    {func(w http.ResponseWriter) { _, _ = w.Write(make([]byte, MaxSigBytes+1)) }, "larger than 4096 bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newFeedServer(t)
			s.sig = c.sig
			got, err := fetcher(t, s.URL+"/catalog/catalog.json").FetchCatalog(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), c.want) || got.Feed != nil {
				t.Fatalf("fetch = %+v, %v; want an error naming %q", got, err, c.want)
			}
		})
	}
}

func TestFetchRefuses(t *testing.T) {
	s := newFeedServer(t)
	for name, c := range map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"a server error": {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }, "500 Internal Server Error"},
		"a 304 unasked":  {func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotModified) }, "304 Not Modified"},
		"a feed over the cap": {func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(make([]byte, MaxFeedBytes+1))
		}, "larger than 8388608 bytes"},
		"a redirect to plain http": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://catalog.example.test/catalog.json", http.StatusFound)
		}, "http only for a loopback host"},
		"endless redirects": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
		}, "too many redirects"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()
			if _, err := fetcher(t, srv.URL+"/catalog.json").FetchCatalog(context.Background(), ""); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	// A loopback redirect is followed.
	redirect := httptest.NewServer(http.RedirectHandler(s.URL+"/catalog/catalog.json", http.StatusMovedPermanently))
	defer redirect.Close()
	if got, err := fetcher(t, redirect.URL+"/catalog/catalog.json").FetchCatalog(context.Background(), ""); err != nil || string(got.Feed) != `{"format":1}` {
		t.Fatalf("through a redirect = %+v, %v", got, err)
	}
}

// A server that never answers fails the fetch at the deadline.
func TestFetchHasADeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	f := fetcher(t, srv.URL+"/catalog.json")
	f.timeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := f.FetchCatalog(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the deadline did not hold")
	}
}

func TestNew(t *testing.T) {
	for _, ok := range []string{
		"https://winterflow.io/catalog/catalog.json",
		"http://127.0.0.1:8080/catalog.json",
		"http://[::1]:8080/catalog.json",
		"http://localhost/catalog.json",
	} {
		if _, err := New(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"http://winterflow.io/catalog/catalog.json",
		"http://10.0.0.1/catalog.json",
		"ftp://winterflow.io/catalog.json",
		"https:///catalog.json",
		"catalog.json",
		"://x",
	} {
		if _, err := New(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	f := fetcher(t, "https://winterflow.io/catalog/catalog.json?v=2")
	if f.sig.String() != "https://winterflow.io/catalog/catalog.json.sig?v=2" {
		t.Fatalf("sig URL = %s", f.sig)
	}
}
