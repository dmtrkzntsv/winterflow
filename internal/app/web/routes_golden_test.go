package web

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"winterflow/internal/infra/bootstrap"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/routes.golden")

// testServer builds the real router with zero-value dependencies. Nothing is
// called at construction time, so nil services are fine for walking routes.
func testServer(t *testing.T) *chi.Mux {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("WEB_URL", "http://localhost")
	t.Setenv("AVATARS_STORAGE_PATH", t.TempDir())
	deps := &bootstrap.Deps{
		Log: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}),
		Cfg: config.NewServerConfig("standalone"),
	}
	srv := NewServer(context.Background(), deps)
	return srv.Handler.(*chi.Mux)
}

// funcID names a handler or middleware stably. Named functions and method
// values keep their symbol name. Closures are named by where their literal
// lives (package dir/file:line): the compiler names an inlined closure after
// its caller (e.g. "registerRoutes.Middleware.func2"), which would change as
// soon as the construction moves, even though the middleware is the same.
func funcID(f any) string {
	pc := reflect.ValueOf(f).Pointer()
	fn := runtime.FuncForPC(pc)
	name := fn.Name()
	if !closureName.MatchString(name) {
		return name
	}
	file, line := fn.FileLine(pc)
	return fmt.Sprintf("closure@%s/%s:%d", filepath.Base(filepath.Dir(file)), filepath.Base(file), line)
}

var closureName = regexp.MustCompile(`\.func\d+(\.\d+)?$`)

// walkRoutes renders every registered route as
// "METHOD PATH handler <- mw1, mw2, ..." (global middleware included), sorted.
func walkRoutes(t *testing.T, r chi.Routes) []string {
	t.Helper()
	var out []string
	err := chi.Walk(r, func(method, route string, h http.Handler, mws ...func(http.Handler) http.Handler) error {
		names := make([]string, 0, len(mws))
		for _, mw := range mws {
			names = append(names, funcID(mw))
		}
		out = append(out, method+" "+route+" "+funcID(h)+" <- "+strings.Join(names, ", "))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(out)
	return out
}

func TestRoutesGolden(t *testing.T) {
	got := strings.Join(walkRoutes(t, testServer(t)), "\n") + "\n"
	path := filepath.Join("testdata", "routes.golden")
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test ./internal/app/web/ -run TestRoutesGolden -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("registered routes changed.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
