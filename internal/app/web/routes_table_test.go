package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"winterflow/internal/domain/usecase/enrollment"
	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"github.com/go-chi/chi/v5"
)

// TestRouteTableMatchesRouter proves the router serves exactly the table:
// every route is registered and nothing else is. No route is Mounted: no
// library serves routes of its own any more.
func TestRouteTableMatchesRouter(t *testing.T) {
	var walked []string
	err := chi.Walk(testServer(t), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		walked = append(walked, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var table []string
	for _, rt := range APIRoutes() {
		if rt.Mounted {
			t.Errorf("Mounted %s %s: no library mount serves it", rt.Method, rt.Path)
			continue
		}
		if rt.Tag == "enroll" {
			continue // mounted only where an enrollment usecase is wired
		}
		table = append(table, rt.Method+" "+rt.Path)
	}
	sort.Strings(walked)
	sort.Strings(table)
	if strings.Join(walked, "\n") != strings.Join(table, "\n") {
		t.Errorf("router and table differ.\nrouter:\n%s\ntable:\n%s", strings.Join(walked, "\n"), strings.Join(table, "\n"))
	}
}

// TestEnrollRoutesNotMountedWithoutEnrollment: standalone has no enrollment
// usecase, so the public enroll routes answer 404.
func TestEnrollRoutesNotMountedWithoutEnrollment(t *testing.T) {
	h := testServer(t)
	for _, p := range []string{"/api/v1/agent/enroll", "/api/v1/agent/enroll/refresh", "/api/v1/agent/enroll/status", "/api/v1/hub/enroll", "/api/v1/agent/hub"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, strings.NewReader("{}")))
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, rec.Code)
		}
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

// TestHubRoutesNeedTheirUsecase: with enrollment wired but no hub join or
// agent hub usecase, those two routes answer 404 instead of panicking.
func TestHubRoutesNeedTheirUsecase(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("WEB_URL", "http://localhost")
	t.Setenv("AVATARS_STORAGE_PATH", t.TempDir())
	deps := &bootstrap.Deps{
		Log:        logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}),
		Cfg:        config.NewServerConfig("distributed"),
		Enrollment: &enrollment.UseCase{},
	}
	h := NewServer(context.Background(), deps).Handler
	for _, p := range []string{"/api/v1/hub/enroll", "/api/v1/agent/hub"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, strings.NewReader("{}")))
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, rec.Code)
		}
	}
}
