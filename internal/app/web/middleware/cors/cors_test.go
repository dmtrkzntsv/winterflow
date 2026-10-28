package cors

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	webmcp "winterflow/internal/app/web/mcp"
)

func serve(t *testing.T, origins, method, origin string) http.Header {
	t.Helper()
	return serveAt(t, origins, method, origin, "/api/v1/user/get-profile")
}

func serveAt(t *testing.T, origins, method, origin, path string) http.Header {
	t.Helper()
	h := UseCORS(origins)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(method, path, nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec.Header()
}

// TestWildcardIsCredentialless: under "*" a foreign origin may make plain
// requests but gets neither credentials nor the CSRF header.
func TestWildcardIsCredentialless(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodOptions} {
		h := serve(t, "*", m, "https://evil.example")
		if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("%s: Allow-Origin = %q, want *", m, got)
		}
		if got := h.Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("%s: Allow-Credentials = %q, want none", m, got)
		}
		if got := h.Get("Access-Control-Allow-Headers"); strings.Contains(strings.ToLower(got), "x-winterflow-request") {
			t.Errorf("%s: Allow-Headers = %q admits the CSRF header", m, got)
		}
	}
}

// TestListedOriginIsTrusted: a listed origin gets itself echoed, credentials
// and the CSRF header, also when "*" is listed beside it.
func TestListedOriginIsTrusted(t *testing.T) {
	for _, origins := range []string{"https://app.example", "*, https://app.example"} {
		h := serve(t, origins, http.MethodOptions, "https://app.example")
		if got := h.Get("Access-Control-Allow-Origin"); got != "https://app.example" {
			t.Errorf("%q: Allow-Origin = %q", origins, got)
		}
		if got := h.Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("%q: Allow-Credentials = %q, want true", origins, got)
		}
		if got := h.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Winterflow-Request") {
			t.Errorf("%q: Allow-Headers = %q, want X-Winterflow-Request", origins, got)
		}
		if got := h.Get("Vary"); got != "Origin" {
			t.Errorf("%q: Vary = %q, want Origin", origins, got)
		}
	}
}

// TestUnlistedOriginWithoutWildcard gets no CORS headers at all.
func TestUnlistedOriginWithoutWildcard(t *testing.T) {
	h := serve(t, "https://app.example", http.MethodGet, "https://evil.example")
	for _, k := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Headers"} {
		if got := h.Get(k); got != "" {
			t.Errorf("%s = %q, want none", k, got)
		}
	}
}

// TestNoOrigin: a same-origin or non-browser request gets no CORS headers.
func TestNoOrigin(t *testing.T) {
	if got := serve(t, "*", http.MethodGet, "").Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q, want none", got)
	}
}

// TestMCPHeaders: on /mcp a listed origin and, under "*", any origin may
// send the MCP transport's headers and read WWW-Authenticate; a wildcard
// origin still gets neither credentials nor the CSRF header, and other
// paths are unchanged.
func TestMCPHeaders(t *testing.T) {
	if mcpPath != webmcp.Path {
		t.Fatalf("mcpPath = %q, want webmcp.Path %q", mcpPath, webmcp.Path)
	}
	for _, c := range []struct {
		origins, origin string
		trusted         bool
	}{
		{"https://app.example", "https://app.example", true},
		{"*", "https://claude.example", false},
	} {
		for _, m := range []string{http.MethodOptions, http.MethodPost} {
			h := serveAt(t, c.origins, m, c.origin, "/mcp")
			allow := strings.ToLower(h.Get("Access-Control-Allow-Headers"))
			for _, want := range []string{"mcp-protocol-version", "mcp-session-id", "authorization", "content-type"} {
				if !strings.Contains(allow, want) {
					t.Errorf("%s %s: Allow-Headers = %q, want %s", c.origin, m, allow, want)
				}
			}
			if got := h.Get("Access-Control-Expose-Headers"); !strings.Contains(got, "WWW-Authenticate") {
				t.Errorf("%s %s: Expose-Headers = %q, want WWW-Authenticate", c.origin, m, got)
			}
			creds := h.Get("Access-Control-Allow-Credentials") == "true"
			csrf := strings.Contains(allow, "x-winterflow-request")
			if creds != c.trusted || csrf != c.trusted {
				t.Errorf("%s %s: credentials %v, X-Winterflow-Request %v, want %v", c.origin, m, creds, csrf, c.trusted)
			}
		}
	}
	h := serve(t, "*", http.MethodOptions, "https://claude.example")
	if got := h.Get("Access-Control-Allow-Headers"); strings.Contains(got, "MCP-Protocol-Version") {
		t.Errorf("REST Allow-Headers = %q admits the MCP headers", got)
	}
	if got := h.Get("Access-Control-Expose-Headers"); got != "" {
		t.Errorf("REST Expose-Headers = %q, want none", got)
	}
	if got := serveAt(t, "https://app.example", http.MethodOptions, "https://evil.example", "/mcp").Get("Access-Control-Allow-Headers"); got != "" {
		t.Errorf("unlisted origin on /mcp: Allow-Headers = %q, want none", got)
	}
}
