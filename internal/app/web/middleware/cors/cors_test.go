package cors

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, origins, method, origin string) http.Header {
	t.Helper()
	h := UseCORS(origins)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(method, "/api/v1/user/get-profile", nil)
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
