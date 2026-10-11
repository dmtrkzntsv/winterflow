package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// tag returns a middleware that appends name to the X-Chain header, so a
// request through the router reveals the order middleware ran in.
func tag(name string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("X-Chain", name)
			next.ServeHTTP(w, r)
		})
	}
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }

func serve(t *testing.T, r http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRegisterChains(t *testing.T) {
	r := chi.NewRouter()
	Register(r, []Route{
		{Method: http.MethodGet, Path: "/public", Handler: ok},
		{Method: http.MethodPost, Path: "/limited", Handler: ok, Middleware: []Middleware{tag("limit")}},
		{Method: http.MethodGet, Path: "/user", Handler: ok, Auth: User},
		{Method: http.MethodPost, Path: "/admin", Handler: ok, Auth: User, Middleware: []Middleware{tag("admin"), tag("extra")}},
		{Method: http.MethodGet, Path: "/auth/user", Auth: Public, Mounted: true},
	}, tag("auth"))

	cases := []struct {
		method, path, chain string
		status              int
	}{
		{http.MethodGet, "/public", "", http.StatusTeapot},
		{http.MethodPost, "/limited", "limit", http.StatusTeapot},
		{http.MethodGet, "/user", "auth", http.StatusTeapot},
		{http.MethodPost, "/admin", "auth,admin,extra", http.StatusTeapot},
		{http.MethodPost, "/public", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/auth/user", "", http.StatusNotFound}, // Mounted: not registered
	}
	for _, tc := range cases {
		rec := serve(t, r, tc.method, tc.path)
		if rec.Code != tc.status {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, rec.Code, tc.status)
		}
		if got := strings.Join(rec.Header().Values("X-Chain"), ","); got != tc.chain {
			t.Errorf("%s %s: chain %q, want %q", tc.method, tc.path, got, tc.chain)
		}
	}
}

func TestRegisterFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		route  Route
		authMW Middleware
		want   string
	}{
		{"nil handler", Route{Method: http.MethodGet, Path: "/a"}, tag("auth"), "has no handler"},
		{"nil authMW", Route{Method: http.MethodGet, Path: "/a", Handler: ok, Auth: User}, nil, "authMW is nil"},
		{"nil guard", Route{Method: http.MethodGet, Path: "/a", Handler: ok, Auth: User, Middleware: []Middleware{nil}}, tag("auth"), "middleware 0 is nil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				p := recover()
				if p == nil || !strings.Contains(p.(string), tc.want) {
					t.Fatalf("panic = %v, want it to contain %q", p, tc.want)
				}
			}()
			Register(chi.NewRouter(), []Route{tc.route}, tc.authMW)
		})
	}
}
