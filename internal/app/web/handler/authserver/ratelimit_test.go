package authserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"winterflow/internal/app/web/api"
	"winterflow/internal/app/web/middleware/ratelimit"
)

// TestRateLimitedOAuthEndpointsAnswerOAuthJSON drives the token and
// registration routes through api.Register with the limiters routes.go
// builds: past the burst, each answers 429 as a JSON OAuth error with
// Retry-After, no-store and the any-origin CORS header its handler sets.
func TestRateLimitedOAuthEndpointsAnswerOAuthJSON(t *testing.T) {
	pass := func(next http.Handler) http.Handler { return next }
	tokenLim, regLim := ratelimit.New(5, 1), ratelimit.New(0.2, 1)
	g := api.Guards{AuthLimit: pass, EnrollLimit: pass, EnrollStartLimit: pass,
		OAuthRegisterLimit: regLim.MiddlewareWith(TooManyRegistrations),
		OAuthTokenLimit:    tokenLim.MiddlewareWith(TooManyTokenRequests)}
	r := chi.NewRouter()
	api.Register(r, NewHandler(&Deps{}).Routes(g), pass)

	for _, tc := range []struct {
		path, retryAfter string
	}{
		{TokenPath, "1"},
		{RegisterPath, "5"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			send := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}"))
				req.RemoteAddr = "203.0.113.7:1234"
				req.Header.Set("Origin", "https://claude.ai")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				return w
			}
			if w := send(); w.Code == http.StatusTooManyRequests {
				t.Fatalf("first request already limited")
			}
			w := send()
			if w.Code != http.StatusTooManyRequests {
				t.Fatalf("status %d, want 429", w.Code)
			}
			h := w.Header()
			if got := h.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type %q", got)
			}
			if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Access-Control-Allow-Origin %q", got)
			}
			if got := h.Get("Retry-After"); got != tc.retryAfter {
				t.Errorf("Retry-After %q, want %q", got, tc.retryAfter)
			}
			if got := h.Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control %q", got)
			}
			var body struct {
				Error       string `json:"error"`
				Description string `json:"error_description"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body %q: %v", w.Body.String(), err)
			}
			if body.Error != "temporarily_unavailable" || body.Description == "" {
				t.Errorf("body %+v", body)
			}
		})
	}
}
