package mcp_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	webmcp "winterflow/internal/app/web/mcp"
	"winterflow/internal/app/web/middleware/realip"
)

func TestRequestOrigin(t *testing.T) {
	for _, c := range []struct {
		name, remote, host, xff, xfp string
		tls                          bool
		want                         string
	}{
		{name: "plain", remote: "192.0.2.5:1", host: "homelab:8080", want: "http://homelab:8080"},
		{name: "tls", remote: "192.0.2.5:1", host: "wf.example.test", tls: true, want: "https://wf.example.test"},
		{name: "proto from the local proxy", remote: "127.0.0.1:1", host: "wf.example.test", xfp: "https", want: "https://wf.example.test"},
		{name: "proto from the local proxy, client address rewritten", remote: "127.0.0.1:1", host: "wf.example.test",
			xff: "192.0.2.5", xfp: "https", want: "https://wf.example.test"},
		{name: "last proto entry", remote: "127.0.0.1:1", host: "wf.example.test", xfp: "https, http", want: "http://wf.example.test"},
		{name: "proto from a LAN client ignored", remote: "192.0.2.5:1", host: "homelab:8080", xfp: "https", want: "http://homelab:8080"},
		{name: "ipv6", remote: "192.0.2.5:1", host: "[fd00::1]:8080", want: "http://[fd00::1]:8080"},
		{name: "not a host", remote: "192.0.2.5:1", host: "a@b", want: ""},
	} {
		var got string
		h := realip.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = webmcp.RequestOrigin(r) }))
		r := httptest.NewRequest("POST", "/mcp", nil)
		r.RemoteAddr, r.Host = c.remote, c.host
		if c.tls {
			r.TLS = &tls.ConnectionState{}
		} else {
			r.TLS = nil
		}
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.xfp != "" {
			r.Header.Set("X-Forwarded-Proto", c.xfp)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		if got != c.want {
			t.Errorf("%s: origin = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsLoopbackOrigin(t *testing.T) {
	for origin, want := range map[string]bool{
		"http://localhost:8080": true, "http://127.0.0.1:8080": true, "http://[::1]:8080": true,
		"http://app.localhost": true, "https://wf.example.test": false, "http://homelab:8080": false,
		"http://192.168.1.10:8080": false,
	} {
		if got := webmcp.IsLoopbackOrigin(origin); got != want {
			t.Errorf("IsLoopbackOrigin(%q) = %v, want %v", origin, got, want)
		}
	}
}
