package spa

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte("<html>shell</html>")},
		"favicon.svg":        {Data: []byte("<svg/>")},
		"assets/app-abc.js":  {Data: []byte("js")},
		"assets/app-abc.css": {Data: []byte("css")},
	}
}

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestHandler(t *testing.T) {
	h := Handler(testFS())

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string // substring; empty = don't check
		wantCache  string // exact Cache-Control; empty = don't check
	}{
		{"root serves shell", "GET", "/", 200, "shell", "no-cache"},
		{"client route falls back to shell", "GET", "/apps/some-id", 200, "shell", "no-cache"},
		{"register route falls back", "GET", "/register", 200, "shell", ""},
		{"real asset served immutable", "GET", "/assets/app-abc.js", 200, "js", "public, max-age=31536000, immutable"},
		{"real top-level file served", "GET", "/favicon.svg", 200, "<svg/>", "no-cache"},
		{"missing asset-like path 404s", "GET", "/assets/gone.js", 404, "", ""},
		{"missing extensioned path 404s", "GET", "/logo.png", 404, "", ""},
		{"api path never gets shell", "GET", "/api/v1/nope", 404, "", ""},
		{"api root reserved", "GET", "/api", 404, "", ""},
		{"auth path reserved", "GET", "/auth/google/login", 404, "", ""},
		{"avatar path reserved", "GET", "/avatar/x", 404, "", ""},
		{"internal path reserved", "GET", "/_/nope", 404, "", ""},
		// http.ServeFileFS itself rejects raw ".." URL paths with 400.
		{"traversal rejected by stdlib", "GET", "/../../etc/passwd", 400, "", ""},
		{"traversal to extensioned file 404s", "GET", "/../../etc/ssl/cert.pem", 404, "", ""},
		{"directory does not list", "GET", "/assets", 200, "shell", ""},
		{"post gets 404 not shell", "POST", "/apps", 404, "", ""},
		{"head works on shell", "HEAD", "/", 200, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := get(t, h, tt.method, tt.path)
			if w.Code != tt.wantStatus {
				t.Fatalf("%s %s: status = %d, want %d", tt.method, tt.path, w.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Fatalf("%s %s: body %q does not contain %q", tt.method, tt.path, w.Body.String(), tt.wantBody)
			}
			if tt.wantCache != "" && w.Header().Get("Cache-Control") != tt.wantCache {
				t.Fatalf("%s %s: Cache-Control = %q, want %q", tt.method, tt.path, w.Header().Get("Cache-Control"), tt.wantCache)
			}
		})
	}
}

// A shell reply for a route with a dot in it would be wrong only when the dot
// makes it look like a file; extensionless dotted segments (none exist in the
// app today) are an accepted 404 trade-off. This test pins the behavior.
func TestHandler_ExtensionHeuristic(t *testing.T) {
	h := Handler(testFS())
	if w := get(t, h, "GET", "/apps/v1.2"); w.Code != 404 {
		t.Fatalf("dotted path served %d, want 404", w.Code)
	}
}
