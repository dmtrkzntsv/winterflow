package contract_test

import (
	"net/http"
	"sync"
	"testing"
)

// hit is one request the suite made: the table route it reached and the
// status it got.
type hit struct {
	Method, Path string
	Status       int
}

// hits collects every routed request across all tests in the binary.
var hits = struct {
	sync.Mutex
	set map[hit]int
}{set: map[hit]int{}}

// statusWriter captures the status on the first header write. Flush is
// passed through: the SSE handler needs an http.Flusher.
type statusWriter struct {
	http.ResponseWriter
	method, path string
	recorded     bool
}

func (w *statusWriter) WriteHeader(code int) {
	w.mark(code)
	w.ResponseWriter.WriteHeader(code)
}

// mark records the request once, under its table path.
func (w *statusWriter) mark(code int) {
	if w.recorded {
		return
	}
	w.recorded = true
	path := w.path
	if rt, _, ok := routeFor(w.method, w.path); ok {
		path = rt.Path
	}
	hits.Lock()
	hits.set[hit{w.method, path, code}]++
	hits.Unlock()
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.recorded {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if !w.recorded {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// record wraps the real router; only the harness installs it.
func record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, method: r.Method, path: r.URL.Path}
		next.ServeHTTP(sw, r)
		sw.mark(http.StatusOK) // a handler that never writes answers 200
	})
}

func TestMatchPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tpl, path string
		ok        bool
	}{
		{"/api/v1/sse", "/api/v1/sse", true},
		{"/api/v1/sse", "/api/v1/sse/x", false},
		{"/avatar/{avatar}", "/avatar/a.png", true},
		{"/avatar/{avatar}", "/avatar/", false},
		{"/auth/local/login", "/auth/google/login", false},
	}
	for _, tc := range cases {
		if _, ok := matchPath(tc.tpl, tc.path); ok != tc.ok {
			t.Errorf("matchPath(%q, %q) = %v, want %v", tc.tpl, tc.path, ok, tc.ok)
		}
	}
}
