package web

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestRouteTableMatchesRouter proves the router serves exactly the table:
// every non-Mounted route is registered, nothing else is (besides the two
// library mounts), and every Mounted route lives under one of those mounts.
func TestRouteTableMatchesRouter(t *testing.T) {
	var walked []string
	err := chi.Walk(testServer(t), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if route == "/auth/*" || route == "/avatar/*" {
			return nil
		}
		walked = append(walked, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var table []string
	for _, rt := range APIRoutes() {
		if rt.Mounted {
			if !strings.HasPrefix(rt.Path, "/auth/") && !strings.HasPrefix(rt.Path, "/avatar/") {
				t.Errorf("Mounted %s %s is outside the /auth and /avatar mounts", rt.Method, rt.Path)
			}
			continue
		}
		table = append(table, rt.Method+" "+rt.Path)
	}
	sort.Strings(walked)
	sort.Strings(table)
	if strings.Join(walked, "\n") != strings.Join(table, "\n") {
		t.Errorf("router and table differ.\nrouter:\n%s\ntable:\n%s", strings.Join(walked, "\n"), strings.Join(table, "\n"))
	}
}

func TestHealth(t *testing.T) {
	rec := httptest.NewRecorder()
	testServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_/health", nil))
	want := `{"success":true,"message":"healthy","data":null}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("GET /_/health = %d %q, want 200 %q", rec.Code, rec.Body.String(), want)
	}
}
