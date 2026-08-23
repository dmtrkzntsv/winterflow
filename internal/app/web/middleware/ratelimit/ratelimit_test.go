package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serve(t *testing.T, mw http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/auth/local/login", nil)
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)
	return w
}

func TestMiddlewareThrottlesPerIP(t *testing.T) {
	l := New(1, 2)
	calls := 0
	mw := l.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))

	for i := 0; i < 2; i++ {
		if w := serve(t, mw, "203.0.113.7:1"); w.Code != http.StatusOK {
			t.Fatalf("within burst: %d", w.Code)
		}
	}
	if w := serve(t, mw, "203.0.113.7:2"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("beyond burst: %d, want 429", w.Code)
	}
	// A different client is unaffected.
	if w := serve(t, mw, "198.51.100.9:1"); w.Code != http.StatusOK {
		t.Fatalf("other client throttled: %d", w.Code)
	}
	if calls != 3 {
		t.Fatalf("handler calls = %d, want 3", calls)
	}
}

func TestAllowSweepsIdleVisitors(t *testing.T) {
	l := New(1, 1)
	now := time.Now()
	l.Allow("a", now)
	l.Allow("b", now)
	if len(l.visitors) != 2 {
		t.Fatalf("visitors = %d", len(l.visitors))
	}
	// Next allow after the eviction window sweeps the stale buckets.
	l.Allow("c", now.Add(idleEviction+2*time.Minute))
	if len(l.visitors) != 1 {
		t.Fatalf("stale buckets not swept: %d", len(l.visitors))
	}
}

func TestAllowFailsOpenAtCapacity(t *testing.T) {
	l := New(1, 1)
	now := time.Now()
	for i := 0; i < maxVisitors; i++ {
		l.visitors[string(rune(i))+"x"] = &visitor{seen: now}
	}
	if !l.Allow("new-client", now) {
		t.Fatal("at capacity the limiter must fail open")
	}
	if len(l.visitors) != maxVisitors {
		t.Fatalf("map grew past cap: %d", len(l.visitors))
	}
}
