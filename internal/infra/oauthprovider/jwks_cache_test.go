package oauthprovider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// jwksFixture serves a switchable key set at /jwks with a Cache-Control
// header, counting fetches, behind a client whose discovery is pre-seeded.
type jwksFixture struct {
	mu           sync.Mutex
	kids         []string
	cacheControl string
	status       int
	fetches      int

	clock time.Time
	c     *Client
}

func newJWKSFixture(t *testing.T, cacheControl string, kids ...string) *jwksFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &jwksFixture{kids: kids, cacheControl: cacheControl, status: http.StatusOK,
		clock: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.fetches++
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		var set struct {
			Keys []jwk `json:"keys"`
		}
		for _, kid := range f.kids {
			set.Keys = append(set.Keys, jwk{Kty: "EC", Crv: "P-256", Kid: kid, Use: "sig",
				X: b64(key.X.Bytes()), Y: b64(key.Y.Bytes())})
		}
		if f.cacheControl != "" {
			w.Header().Set("Cache-Control", f.cacheControl)
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)
	f.c = NewClient(Issuer{IssuerURL: srv.URL}, nil)
	f.c.disc = &Discovery{Issuer: srv.URL, JWKSURI: srv.URL + "/jwks"}
	f.c.now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.clock }
	return f
}

func (f *jwksFixture) advance(d time.Duration) { f.mu.Lock(); f.clock = f.clock.Add(d); f.mu.Unlock() }
func (f *jwksFixture) serve(kids ...string)    { f.mu.Lock(); f.kids = kids; f.mu.Unlock() }
func (f *jwksFixture) fail(status int)         { f.mu.Lock(); f.status = status; f.mu.Unlock() }
func (f *jwksFixture) count() int              { f.mu.Lock(); defer f.mu.Unlock(); return f.fetches }

func TestJWKSRemovedKeyStopsVerifyingAfterTheCacheLifetime(t *testing.T) {
	ctx := context.Background()
	f := newJWKSFixture(t, "public, max-age=600", "old")
	if _, err := f.c.keyFor(ctx, "old"); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	f.serve("new") // the issuer drops "old"

	f.advance(9 * time.Minute)
	if _, err := f.c.keyFor(ctx, "old"); err != nil {
		t.Fatalf("within max-age the cached key still verifies: %v", err)
	}
	if n := f.count(); n != 1 {
		t.Fatalf("a fresh hit must not refetch, got %d fetches", n)
	}

	f.advance(2 * time.Minute) // past max-age
	if _, err := f.c.keyFor(ctx, "old"); err == nil {
		t.Fatal("a removed key must stop verifying once the cache expired")
	}
	if _, err := f.c.keyFor(ctx, "new"); err != nil {
		t.Fatalf("the current key verifies: %v", err)
	}
	if n := f.count(); n != 2 {
		t.Fatalf("want one refetch on expiry, got %d fetches", n)
	}
}

func TestJWKSDefaultLifetimeWithoutCacheControl(t *testing.T) {
	ctx := context.Background()
	f := newJWKSFixture(t, "", "a")
	if _, err := f.c.keyFor(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	f.advance(59 * time.Minute)
	_, _ = f.c.keyFor(ctx, "a")
	if n := f.count(); n != 1 {
		t.Fatalf("within an hour: %d fetches", n)
	}
	f.advance(2 * time.Minute)
	_, _ = f.c.keyFor(ctx, "a")
	if n := f.count(); n != 2 {
		t.Fatalf("after an hour: %d fetches, want 2", n)
	}
}

func TestJWKSExpiredSetFailsClosedWhenTheRefetchFails(t *testing.T) {
	ctx := context.Background()
	f := newJWKSFixture(t, "max-age=300", "a")
	if _, err := f.c.keyFor(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	f.fail(http.StatusInternalServerError)
	f.advance(6 * time.Minute)
	if _, err := f.c.keyFor(ctx, "a"); err == nil {
		t.Fatal("an expired set whose refetch failed must not verify")
	}
	f.fail(http.StatusOK)
	if _, err := f.c.keyFor(ctx, "a"); err != nil {
		t.Fatalf("the next lookup refetches: %v", err)
	}
}

func TestJWKSUnknownKidRefetchIsThrottled(t *testing.T) {
	ctx := context.Background()
	f := newJWKSFixture(t, "max-age=3600", "a")
	if _, err := f.c.keyFor(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.keyFor(ctx, "x"); err == nil {
		t.Fatal("unknown kid must fail")
	}
	if _, err := f.c.keyFor(ctx, "y"); err == nil {
		t.Fatal("unknown kid must fail")
	}
	if n := f.count(); n != 2 {
		t.Fatalf("one refetch per minute on a miss, got %d fetches", n)
	}
	f.serve("a", "rotated")
	f.advance(minJWKSRefresh)
	if _, err := f.c.keyFor(ctx, "rotated"); err != nil {
		t.Fatalf("after the throttle a rotated key is found: %v", err)
	}
}

func TestJWKSLifetime(t *testing.T) {
	for cc, want := range map[string]time.Duration{
		"":                          defaultJWKSLifetime,
		"public":                    defaultJWKSLifetime,
		"max-age=bogus":             defaultJWKSLifetime,
		"max-age=-5":                defaultJWKSLifetime,
		"max-age=7200":              2 * time.Hour,
		"public, MAX-AGE=1800":      30 * time.Minute,
		"max-age=0":                 minJWKSLifetime,
		"max-age=10":                minJWKSLifetime,
		"no-store":                  minJWKSLifetime,
		"no-cache, max-age=7200":    minJWKSLifetime,
		"max-age=604800":            maxJWKSLifetime,
		"max-age=99999999999999999": maxJWKSLifetime,
	} {
		if got := jwksLifetime(cc); got != want {
			t.Errorf("jwksLifetime(%q) = %v, want %v", cc, got, want)
		}
	}
}
