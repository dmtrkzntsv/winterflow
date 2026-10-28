package realip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func remoteAfter(t *testing.T, remote string, headers map[string]string) string {
	t.Helper()
	var got string
	h := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = ClientIP(r) }))
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestSpoofedHeadersFromNonLoopbackIgnored(t *testing.T) {
	for _, hdr := range []map[string]string{
		{"X-Real-IP": "192.0.2.9"},
		{"X-Forwarded-For": "192.0.2.9"},
		{"True-Client-IP": "192.0.2.9"},
	} {
		if got := remoteAfter(t, "203.0.113.7:4242", hdr); got != "203.0.113.7" {
			t.Errorf("%v from a public peer: got %q, want the peer", hdr, got)
		}
	}
}

func TestHeadersFromLoopbackHonoured(t *testing.T) {
	cases := []struct {
		remote  string
		headers map[string]string
		want    string
	}{
		{"[::1]:5000", map[string]string{"X-Forwarded-For": "2001:db8::1"}, "2001:db8::1"},
		{"127.0.0.1:5000", map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8"}, "5.6.7.8"},
		// X-Real-IP is client-controlled behind a plain reverse proxy: ignored.
		{"127.0.0.1:5000", map[string]string{"X-Real-IP": "198.51.100.4"}, "127.0.0.1"},
		// The proxy appends the peer it saw: the last entry is the one it vouches for.
		{"127.0.0.1:5000", map[string]string{"X-Forwarded-For": "10.9.9.9, 198.51.100.4"}, "198.51.100.4"},
		{"127.0.0.1:5000", map[string]string{"X-Real-IP": "198.51.100.4", "X-Forwarded-For": "192.0.2.1"}, "192.0.2.1"},
		// Garbage keeps the peer.
		{"127.0.0.1:5000", map[string]string{"X-Forwarded-For": "not-an-ip"}, "127.0.0.1"},
		{"127.0.0.1:5000", nil, "127.0.0.1"},
	}
	for _, c := range cases {
		if got := remoteAfter(t, c.remote, c.headers); got != c.want {
			t.Errorf("%s %v: got %q, want %q", c.remote, c.headers, got, c.want)
		}
	}
}

func TestClientIPWithoutPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7"
	if got := ClientIP(r); got != "203.0.113.7" {
		t.Fatalf("got %q", got)
	}
}

func TestPeerIsLoopbackSurvivesTheRewrite(t *testing.T) {
	for _, c := range []struct {
		remote, xff string
		want        bool
	}{
		{"127.0.0.1:1", "192.0.2.9", true},    // rewritten: the peer was the local proxy
		{"127.0.0.1:1", "", true},             // not rewritten, still loopback
		{"203.0.113.7:1", "127.0.0.1", false}, // a public peer claiming loopback
	} {
		var got bool
		h := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = PeerIsLoopback(r) }))
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("peer %s, XFF %q: PeerIsLoopback = %v, want %v", c.remote, c.xff, got, c.want)
		}
	}
}
