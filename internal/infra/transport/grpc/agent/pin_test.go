package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/cert"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// fakeHub is a TLS gRPC AgentService that accepts every registration and
// holds every stream open.
type fakeHub struct {
	proto.UnimplementedAgentServiceServer
	streams atomic.Int32
}

func (h *fakeHub) RegisterAgent(context.Context, *proto.RegisterAgentRequest) (*proto.RegisterAgentResponse, error) {
	return &proto.RegisterAgentResponse{Base: &proto.BaseResponse{ResponseCode: proto.ResponseCode_RESPONSE_CODE_SUCCESS}}, nil
}

func (h *fakeHub) AgentStream(s grpc.BidiStreamingServer[proto.AgentMessage, proto.ServerCommand]) error {
	h.streams.Add(1)
	<-s.Context().Done()
	return nil
}

type pinEnv struct {
	dir     string // HUB_CERT_DIR: ca.crt, hub.crt, agent.crt, ...
	addr    string // the fake hub, 127.0.0.1:<port>
	hubFP   string // fingerprint of the certificate the hub serves
	hub     *fakeHub
	agent   *Agent
	api     *fakeAPI
	dataDir string
}

// startPinEnv makes a CA, an agent certificate (CN "a") and a hub
// certificate (base "hub" carries the winterflow-hub URI; any other base is
// a plain CA-signed server certificate), serves a fake hub with it and
// builds an agent that dials it and asks a fake API.
func startPinEnv(t *testing.T, base string) *pinEnv {
	t.Helper()
	dir := t.TempDir()
	hosts := []string{"127.0.0.1"}
	if _, err := cert.EnsureHubPKI(dir, hosts); err != nil {
		t.Fatal(err)
	}
	if base != "hub" {
		if _, err := cert.EnsureServerCert(dir, base, hosts); err != nil {
			t.Fatal(err)
		}
	}
	// Agent key and certificate.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(filepath.Join(dir, "agent.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, caKey, err := cert.LoadCA(filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"))
	if err != nil {
		t.Fatal(err)
	}
	agentPEM, _, err := cert.SignCSRAs(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})), "a", ca, caKey, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.crt"), []byte(agentPEM), 0o644); err != nil {
		t.Fatal(err)
	}

	// The hub.
	pair, err := tls.LoadX509KeyPair(filepath.Join(dir, base+".crt"), filepath.Join(dir, base+".key"))
	if err != nil {
		t.Fatal(err)
	}
	hubPEM, _ := os.ReadFile(filepath.Join(dir, base+".crt"))
	hubFP, err := cert.Fingerprint(string(hubPEM))
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})))
	hub := &fakeHub{}
	proto.RegisterAgentServiceServer(srv, hub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dataDir := filepath.Join(dir, "data")
	t.Setenv("HUB_CERT_DIR", dir)
	t.Setenv("AGENT_DATA_DIR", dataDir)
	t.Setenv("AGENT_CA_CERT_PATH", "")
	cfg := config.NewServerConfig("distributed")

	api := &fakeAPI{t: t, certPEM: agentPEM, addr: lis.Addr().String(), hit: make(chan struct{}, 1)}
	apiSrv := httptest.NewServer(api)
	t.Cleanup(apiSrv.Close)

	a := NewAgent(logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "t"}), cfg, "a")
	a.apiURL = apiSrv.URL
	a.httpClient = apiSrv.Client()
	a.loadKey = func() (*ecdsa.PrivateKey, error) { return key, nil }
	addr := lis.Addr().String()
	a.fallbackHub = func() string { return addr }
	t.Cleanup(func() { a.teardownConn() })
	return &pinEnv{dir: dir, addr: addr, hubFP: hubFP, hub: hub, agent: a, api: api, dataDir: dataDir}
}

// connectOnce runs one real bring-up (TLS handshake, register, stream).
func (e *pinEnv) connectOnce(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := e.agent.connectRegisterStream(ctx)
	e.agent.teardownConn()
	return err
}

func (e *pinEnv) storePins(t *testing.T, fps []string) {
	t.Helper()
	host, port, _ := net.SplitHostPort(e.addr)
	if err := NewHubStore(e.dataDir).Save(host, port, fps); err != nil {
		t.Fatal(err)
	}
	e.agent.hubStore = NewHubStore(e.dataDir)
}

const otherFP = "00000000000000000000000000000000000000000000000000000000000000aa"

func TestPinnedHubAccepted(t *testing.T) {
	e := startPinEnv(t, "hub")
	e.storePins(t, []string{otherFP, strings.ToUpper(e.hubFP)})
	if err := e.connectOnce(t); err != nil {
		t.Fatalf("pinned hub refused: %v", err)
	}
	if e.agent.peerFingerprint() != e.hubFP {
		t.Fatalf("peer fingerprint = %q, want %q", e.agent.peerFingerprint(), e.hubFP)
	}
}

func TestUnknownFingerprintRefused(t *testing.T) {
	e := startPinEnv(t, "hub")
	e.storePins(t, []string{otherFP})
	err := e.connectOnce(t)
	if !errors.Is(err, ErrHubNotTrusted) {
		t.Fatalf("err = %v, want ErrHubNotTrusted", err)
	}
	if e.hub.streams.Load() != 0 {
		t.Fatal("a stream was opened to an untrusted hub")
	}
}

func TestMissingHubURIRefused(t *testing.T) {
	// A CA-signed server certificate for the right name but without the
	// hub marker (the Redis certificate), with and without a pin set
	// holding its fingerprint: refused either way.
	e := startPinEnv(t, "redis")
	if err := e.connectOnce(t); !errors.Is(err, ErrHubNotTrusted) || !strings.Contains(err.Error(), cert.HubURIPrefix) {
		t.Fatalf("no pins: err = %v, want ErrHubNotTrusted naming the URI", err)
	}
	e.storePins(t, []string{e.hubFP})
	if err := e.connectOnce(t); !errors.Is(err, ErrHubNotTrusted) {
		t.Fatalf("pinned: err = %v, want ErrHubNotTrusted", err)
	}
}

func TestEmptyPinSetAcceptedWithWarning(t *testing.T) {
	e := startPinEnv(t, "hub")
	// No hub.json at all (an agent installed before pinning).
	if err := e.connectOnce(t); err != nil {
		t.Fatalf("no hub.json: %v", err)
	}
	if !e.agent.warnedNoPins.Load() {
		t.Fatal("no warning logged for the empty set")
	}
	// A hub.json written before pinning: no "fingerprints" key.
	if err := os.MkdirAll(e.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(e.addr)
	raw, _ := json.Marshal(map[string]string{"host": host, "port": port})
	if err := os.WriteFile(filepath.Join(e.dataDir, "hub.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e.agent.hubStore = NewHubStore(e.dataDir)
	if _, _, fps, ok := e.agent.hubStore.Load(); !ok || len(fps) != 0 {
		t.Fatalf("old hub.json: ok=%v fps=%v", ok, fps)
	}
	if err := e.connectOnce(t); err != nil {
		t.Fatalf("old hub.json: %v", err)
	}
	// An empty set from the API is the same.
	e.storePins(t, []string{})
	if err := e.connectOnce(t); err != nil {
		t.Fatalf("empty set: %v", err)
	}
}

func TestUnknownFingerprintAsksAndConnects(t *testing.T) {
	e := startPinEnv(t, "hub")
	e.storePins(t, []string{otherFP})
	e.api.setFingerprints([]string{e.hubFP})
	a := e.agent
	a.retryBackoff = &[2]time.Duration{10 * time.Millisecond, 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran := make(chan error, 1)
	go func() { ran <- a.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for e.hub.streams.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("no stream after the ask; asks = %d", e.api.count())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-ran; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if e.api.count() != 1 {
		t.Fatalf("asks = %d, want 1", e.api.count())
	}
	if _, _, fps, _ := NewHubStore(e.dataDir).Load(); !slices.Equal(fps, []string{e.hubFP}) {
		t.Fatalf("stored set = %v, want the API's", fps)
	}
}

func TestPinRefusalAsksAtMostOncePerFloor(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "env-hub:50051", false)
	api.setFingerprints([]string{otherFP})
	refused := errors.Join(ErrHubNotTrusted, errors.New("register: unavailable"))
	n := 0
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		n++
		if n > 7 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		return nil, nil, refused
	}
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	// Refusals at 0 s (ask: the set changes from empty, redial at once),
	// 0 s (inside the floor), 1, 3, 7, 15 s (inside), 31 s (ask: unchanged).
	if api.count() != 2 {
		t.Fatalf("asks = %d, want 2", api.count())
	}
	if _, _, fps, _ := a.hubStore.Load(); !slices.Equal(fps, []string{otherFP}) {
		t.Fatalf("stored set = %v", fps)
	}
}

// refreshing runs a connected agent (one stream that stays up) with a short
// pin refresh until the API has been asked asks times or the agent
// reconnects; it returns the number of bring-ups.
func refreshing(t *testing.T, a *Agent, api *fakeAPI, ctx context.Context, cancel context.CancelFunc, asks int) int {
	t.Helper()
	a.pinRefresh = 20 * time.Millisecond
	ups := 0
	a.bringUp = func(context.Context) (<-chan struct{}, func() error, error) {
		ups++
		if ups > 1 {
			cancel()
			return nil, nil, errors.New("script over")
		}
		return make(chan struct{}), func() error { return nil }, nil
	}
	go func() {
		for i := 0; i < asks; i++ {
			select {
			case <-api.hit:
			case <-time.After(10 * time.Second):
				t.Error("no refresh ask")
			}
		}
		// Let Run act on the last answer before stopping it.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("Run = %v", err)
	}
	return ups
}

func TestRefreshUpdatesHubJSON(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "env-hub:50051", false)
	if err := a.hubStore.Save("env-hub", "50051", []string{"aa"}); err != nil {
		t.Fatal(err)
	}
	a.seenFP = "aa"
	api.setFingerprints([]string{"AA", "bb"})
	if ups := refreshing(t, a, api, ctx, cancel, 2); ups != 1 {
		t.Fatalf("bring-ups = %d: a refresh that still trusts the hub reconnected", ups)
	}
	host, port, fps, ok := NewHubStore(filepath.Dir(a.hubStore.path)).Load()
	if !ok || host != "env-hub" || port != "50051" || !slices.Equal(fps, []string{"aa", "bb"}) {
		t.Fatalf("hub.json = %q %q %v %v", host, port, fps, ok)
	}
}

func TestRefreshDropsRetiredHub(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "env-hub:50051", false)
	if err := a.hubStore.Save("env-hub", "50051", []string{"aa"}); err != nil {
		t.Fatal(err)
	}
	a.seenFP = "aa"
	api.setFingerprints([]string{"bb"}) // the connected hub's certificate was retired
	if ups := refreshing(t, a, api, ctx, cancel, 1); ups != 2 {
		t.Fatalf("bring-ups = %d, want a reconnect after the refresh", ups)
	}
}

func TestAnswerWithoutFingerprintsKeepsSet(t *testing.T) {
	a, api, ctx, cancel := migrating(t, "env-hub:50051", false)
	if err := a.hubStore.Save("env-hub", "50051", []string{"aa"}); err != nil {
		t.Fatal(err)
	}
	api.setFingerprints(nil) // an API older than the pin set
	refreshing(t, a, api, ctx, cancel, 1)
	if _, _, fps, _ := a.hubStore.Load(); !slices.Equal(fps, []string{"aa"}) {
		t.Fatalf("stored set = %v, want it kept", fps)
	}
}

func TestHubStoreFingerprints(t *testing.T) {
	dir := t.TempDir()
	if err := NewHubStore(dir).Save("hub-01", "443", []string{"AB", "ab", " cd ", ""}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "hub.json"))
	if !strings.Contains(string(raw), `"fingerprints":["ab","cd"]`) {
		t.Fatalf("hub.json = %s", raw)
	}
	if err := NewHubStore(dir).Save("hub-01", "443", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "hub.json"))
	if !strings.Contains(string(raw), `"fingerprints":[]`) {
		t.Fatalf("hub.json = %s", raw)
	}
}
