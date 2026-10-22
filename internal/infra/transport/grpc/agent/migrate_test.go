package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/agentsig"
	"winterflow/pkg/logger"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeAPI is the API's POST /api/v1/agent/hub: it verifies the signature
// against the agent's certificate and answers addr/moved.
type fakeAPI struct {
	t       *testing.T
	certPEM string
	mu      sync.Mutex
	addr    string
	moved   bool
	fps     []string // hub_fingerprints; nil leaves the field out
	asks    int
	hit     chan struct{}
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/agent/hub" {
		http.NotFound(w, r)
		return
	}
	var body struct {
		ServerID  string `json:"server_id"`
		Timestamp int64  `json:"timestamp"`
		Nonce     string `json:"nonce"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Nonce == "" {
		f.t.Errorf("bad body: %v %+v", err, body)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := agentsig.Verify(f.certPEM, agentsig.Payload(body.ServerID, body.Timestamp, body.Nonce), r.Header.Get(agentsig.Header)); err != nil || body.ServerID != "a" {
		f.t.Errorf("signature or server id refused: %v %q", err, body.ServerID)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	f.asks++
	data := map[string]any{"hub_addr": f.addr, "moved": f.moved}
	if f.fps != nil {
		data["hub_fingerprints"] = f.fps
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "ok", "data": data})
	if f.hit != nil {
		select {
		case f.hit <- struct{}{}:
		default:
		}
	}
}

func (f *fakeAPI) setFingerprints(fps []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fps = fps
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asks
}

// migrating builds an agent that asks the fake API, keeps its hub in a temp
// dir, and uses a fake clock that advances by every wait it takes.
func migrating(t *testing.T, addr string, moved bool) (*Agent, *fakeAPI, context.Context, context.CancelFunc) {
	a, api, ctx, cancel, _ := migratingClock(t, addr, moved)
	return a, api, ctx, cancel
}

// migratingClock is migrating plus a func that moves the fake clock.
func migratingClock(t *testing.T, addr string, moved bool) (*Agent, *fakeAPI, context.Context, context.CancelFunc, func(time.Duration)) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "a"}, NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{t: t, certPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), addr: addr, moved: moved, hit: make(chan struct{}, 1)}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := NewAgent(logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "t"}), nil, "a")
	a.deniedBackoff = testDenied
	a.retryBackoff = &[2]time.Duration{time.Second, 30 * time.Second}
	a.settle = 2 * time.Minute
	a.apiURL = srv.URL
	a.httpClient = srv.Client()
	a.loadKey = func() (*ecdsa.PrivateKey, error) { return key, nil }
	a.hubStore = NewHubStore(t.TempDir())
	a.fallbackHub = func() string { return "env-hub:50051" }
	var mu sync.Mutex
	clock := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	a.sleep = func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		clock = clock.Add(d)
		mu.Unlock()
		return ctx.Err() == nil
	}
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	return a, api, ctx, cancel, advance
}

type failDispatcher struct{ t *testing.T }

func (d failDispatcher) Dispatch(context.Context, *proto.RequestEnvelope) *proto.ResponseEnvelope {
	d.t.Error("hub.move reached the dispatcher")
	return nil
}

func TestMoveMessageTriggersAsk(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "hub-02.winterflow.dev:443", true)
	a.SetDispatcher(failDispatcher{t})
	var dials []string
	up := make(chan struct{}, 1)
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		dials = append(dials, a.hubAddr())
		if len(dials) > 1 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		up <- struct{}{}
		return make(chan struct{}), func() error { return nil }, nil
	}
	go func() {
		<-up
		// No stream: hub.move must return before ever sending a reply.
		a.handleRequest(ctx, nil, &proto.RequestEnvelope{RequestId: "r1", Type: string(command.HubMove)})
	}()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if api.count() != 1 {
		t.Fatalf("asks = %d, want 1", api.count())
	}
	if fmt.Sprint(dials) != "[env-hub:50051 hub-02.winterflow.dev:443]" {
		t.Fatalf("dials = %v", dials)
	}
}

func TestFailedPreconditionTriggersAskNotDenial(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "env-hub:50051", false)
	moved := fmt.Errorf("register: %w", status.Error(codes.FailedPrecondition, "server belongs to another hub"))
	n := 0
	var waits []time.Duration
	sleep := a.sleep
	a.sleep = func(ctx context.Context, d time.Duration) bool { waits = append(waits, d); return sleep(ctx, d) }
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		n++
		if n > 7 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		return nil, nil, moved
	}
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v, want nil (FailedPrecondition is not a denial)", err)
	}
	// Refusals at 0, 1, 3, 7, 15, 31 and 61 s (the ordinary backoff); asks
	// at least askFloor apart: 0, 31 and 61 s.
	if api.count() != 3 {
		t.Fatalf("asks = %d, want 3 (at 0, 31 and 61 s)", api.count())
	}
	for _, w := range waits[:6] {
		if w >= time.Minute {
			t.Fatalf("waits = %v: a denial backoff was used", waits)
		}
	}
}

func TestStayAnswerNamingAnotherHubMoves(t *testing.T) {
	// The server belongs to live hub-b; the agent dials env-hub (a stale
	// hub.json, a lost answer): the API's "stay" names hub-b, which is a move.
	a, _, ctx, cancel := migrating(t, "hub-b.winterflow.dev:443", false)
	var dials []string
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		dials = append(dials, a.hubAddr())
		if len(dials) > 1 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		return nil, nil, status.Error(codes.FailedPrecondition, "server belongs to another hub")
	}
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if fmt.Sprint(dials) != "[env-hub:50051 hub-b.winterflow.dev:443]" {
		t.Fatalf("dials = %v", dials)
	}
	host, port, _, ok := NewHubStore(filepath.Dir(a.hubStore.path)).Load()
	if !ok || host != "hub-b.winterflow.dev" || port != "443" {
		t.Fatalf("stored hub = %q %q %v", host, port, ok)
	}
}

func TestMoveRequestsHonorAskFloor(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "env-hub:50051", false)
	a.askFloor = 300 * time.Millisecond // real time: the pending move's timer
	done := make(chan struct{})
	n := 0
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		n++
		if n > 1 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		a.requestMove() // the hub's hub.move, after Run dropped stale ones
		return done, func() error { return nil }, nil
	}
	go func() {
		<-api.hit
		// A second move inside the floor waits for it, then asks.
		a.requestMove()
		if c := api.count(); c != 1 {
			t.Errorf("asks right after the second move = %d, want 1", c)
		}
		<-api.hit
		close(done)
	}()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if api.count() != 2 {
		t.Fatalf("asks = %d, want 2", api.count())
	}
}

func TestUnreachableThreeMinutesAsks(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "hub-01:443", false)
	start := a.now()
	var askedAt []time.Duration
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		if c := api.count(); c > len(askedAt) {
			askedAt = append(askedAt, a.now().Sub(start))
		}
		if a.now().Sub(start) > 10*time.Minute {
			cancel()
		}
		return nil, nil, status.Error(codes.Unavailable, "connection refused")
	}
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if api.count() != 3 {
		t.Fatalf("asks = %d at %v, want 3 (about 3, 6 and 9 minutes)", api.count(), askedAt)
	}
	if askedAt[0] < 3*time.Minute || askedAt[0] > 4*time.Minute {
		t.Fatalf("first ask at %v, want about 3 minutes", askedAt[0])
	}
	for i := 1; i < len(askedAt); i++ {
		if gap := askedAt[i] - askedAt[i-1]; gap < 3*time.Minute || gap > 4*time.Minute {
			t.Fatalf("asks at %v: gap %v, want about 3 minutes", askedAt, gap)
		}
	}
}

func TestStayKeepsHub(t *testing.T) {
	a, api, ctx, cancel, advance := migratingClock(t, "hub-01:443", false)
	if err := a.hubStore.Save("hub-01", "443", nil); err != nil {
		t.Fatal(err)
	}
	var dials []string
	done := make(chan struct{})
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		dials = append(dials, a.hubAddr())
		if len(dials) > 1 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		a.requestMove() // the hub's hub.move, after Run dropped stale ones
		return done, func() error { return nil }, nil
	}
	go func() {
		<-api.hit
		// The stay kept the stream: a second hub.move, past the floor, is
		// read by the same wait and asks again.
		advance(time.Minute)
		a.requestMove()
		<-api.hit
		close(done)
	}()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if api.count() != 2 {
		t.Fatalf("asks = %d, want 2", api.count())
	}
	if fmt.Sprint(dials) != "[hub-01:443 hub-01:443]" {
		t.Fatalf("dials = %v (a stay must not reconnect)", dials)
	}
}

func TestMovedSavesAndReconnects(t *testing.T) {
	a, _, ctx, cancel := migrating(t, "[2001:db8::7]:50051", true)
	var dials []string
	var waits []time.Duration
	waitsBeforeRedial := -1
	sleep := a.sleep
	a.sleep = func(ctx context.Context, d time.Duration) bool { waits = append(waits, d); return sleep(ctx, d) }
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		dials = append(dials, a.hubAddr())
		if len(dials) > 1 {
			waitsBeforeRedial = len(waits)
			cancel()
			return nil, nil, errors.New("script over")
		}
		return nil, nil, status.Error(codes.FailedPrecondition, "draining")
	}
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if fmt.Sprint(dials) != "[env-hub:50051 [2001:db8::7]:50051]" {
		t.Fatalf("dials = %v", dials)
	}
	if waitsBeforeRedial != 0 {
		t.Fatalf("waits before reconnecting = %v, want none", waits)
	}
	// hub.json holds the new hub; a fresh store (the next start) reads it.
	host, port, _, ok := NewHubStore(filepath.Dir(a.hubStore.path)).Load()
	if !ok || host != "2001:db8::7" || port != "50051" {
		t.Fatalf("stored hub = %q %q %v", host, port, ok)
	}
}

func TestAskErrorsKeepRetrying(t *testing.T) {
	a, _, ctx, cancel := migrating(t, "", true) // moved without an address
	n := 0
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		n++
		if n > 3 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		if a.hubAddr() != "env-hub:50051" {
			t.Errorf("dial %q after a bad answer", a.hubAddr())
		}
		return nil, nil, status.Error(codes.FailedPrecondition, "moved")
	}
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	a.apiURL = "http://127.0.0.1:1" // nothing listens
	if _, err := a.askWhere(context.Background()); err == nil {
		t.Fatal("ask against a closed port succeeded")
	}
}

func TestHubStoreAtomic(t *testing.T) {
	dir := t.TempDir()
	s := NewHubStore(dir)
	if _, _, _, ok := s.Load(); ok {
		t.Fatal("empty store loaded a hub")
	}
	if err := s.Save("hub-02.winterflow.dev", "443", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("hub-03.winterflow.dev", "443", nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "hub.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("dir holds %d entries, want only hub.json (no temp files)", len(entries))
	}
	host, port, _, ok := NewHubStore(dir).Load()
	if !ok || host != "hub-03.winterflow.dev" || port != "443" {
		t.Fatalf("Load = %q %q %v", host, port, ok)
	}
	// A corrupt or incomplete file is ignored (the env's hub is used).
	for _, bad := range []string{"{", `{"host":"h"}`, `{"host":"","port":"1"}`} {
		if err := os.WriteFile(filepath.Join(dir, "hub.json"), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, ok := NewHubStore(dir).Load(); ok {
			t.Fatalf("%q loaded", bad)
		}
	}
}
