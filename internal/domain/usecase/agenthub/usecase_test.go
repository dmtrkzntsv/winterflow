package agenthub

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/pkg/agentsig"
	"winterflow/pkg/logger"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type fakeServers struct {
	mu      sync.Mutex
	hub     map[string]string
	cert    map[string]string
	updates int
}

func (f *fakeServers) ServerHub(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.hub[id]
	if !ok {
		return "", model.ErrNotFound
	}
	return h, nil
}

func (f *fakeServers) ActiveServerCertificate(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cert[id]
	if !ok {
		return "", model.ErrNotFound
	}
	return c, nil
}

func (f *fakeServers) SetServerHub(_ context.Context, id, from, to string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.hub[id]
	if !ok {
		return "", model.ErrNotFound
	}
	if cur != from {
		return cur, nil
	}
	f.hub[id] = to
	f.updates++
	return to, nil
}

// fakePins is the trusted hub certificate set.
type fakePins struct {
	fps []string
	err error
}

func (p *fakePins) Active(context.Context) ([]string, error) { return p.fps, p.err }

type fakeRegistry struct{ hubs []model.Hub }

func (f fakeRegistry) List(context.Context) ([]model.Hub, error) { return f.hubs, nil }

// fakePicker hands out picks in order; excluded records what it was told
// to skip.
type fakePicker struct {
	mu       sync.Mutex
	picks    []model.Hub
	err      error
	excluded []string
	barrier  *sync.WaitGroup
}

func (p *fakePicker) PickExcept(_ context.Context, hubID string) (model.Hub, error) {
	p.mu.Lock()
	p.excluded = append(p.excluded, hubID)
	var h model.Hub
	if len(p.picks) > 0 {
		h, p.picks = p.picks[0], p.picks[1:]
	}
	p.mu.Unlock()
	if p.barrier != nil {
		p.barrier.Done()
		p.barrier.Wait()
	}
	return h, p.err
}

var (
	hub1 = model.Hub{ID: "hub-01", PublicAddr: "hub-01:443", Accepting: true, Capacity: 10}
	hub2 = model.Hub{ID: "hub-02", PublicAddr: "hub-02:443", Accepting: true, Capacity: 10}
	hub3 = model.Hub{ID: "hub-03", PublicAddr: "hub-03:443", Accepting: true, Capacity: 10}
)

func newKeyCert(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "srv-1"}, NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

type fixture struct {
	uc      *UseCase
	servers *fakeServers
	picker  *fakePicker
	pins    *fakePins
	key     *ecdsa.PrivateKey
}

func setup(t *testing.T, current string, listed []model.Hub, picks ...model.Hub) *fixture {
	t.Helper()
	key, certPEM := newKeyCert(t)
	servers := &fakeServers{hub: map[string]string{"srv-1": current}, cert: map[string]string{"srv-1": certPEM}}
	picker := &fakePicker{picks: picks}
	pins := &fakePins{fps: []string{"aa", "bb"}}
	uc := New(servers, fakeRegistry{hubs: listed}, picker, pins, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}))
	uc.now = func() time.Time { return now }
	return &fixture{uc: uc, servers: servers, picker: picker, pins: pins, key: key}
}

func (f *fixture) sign(t *testing.T, serverID string, ts int64, nonce string) string {
	t.Helper()
	sig, err := agentsig.Sign(f.key, agentsig.Payload(serverID, ts, nonce))
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func (f *fixture) where(t *testing.T) (string, bool, error) {
	t.Helper()
	ts := now.Unix()
	a, err := f.uc.Where(context.Background(), "srv-1", ts, "n1", f.sign(t, "srv-1", ts, "n1"))
	return a.HubAddr, a.Moved, err
}

func TestWhereStay(t *testing.T) {
	f := setup(t, "hub-01", []model.Hub{hub1, hub2})
	addr, moved, err := f.where(t)
	if err != nil || addr != "hub-01:443" || moved {
		t.Fatalf("got %q %v %v", addr, moved, err)
	}
	if f.servers.updates != 0 || len(f.picker.excluded) != 0 {
		t.Fatalf("stay touched the assignment: updates %d, picks %v", f.servers.updates, f.picker.excluded)
	}
	// A hub that stopped accepting new servers still serves its own.
	closed := hub1
	closed.Accepting = false
	f = setup(t, "hub-01", []model.Hub{closed, hub2})
	if addr, moved, err := f.where(t); err != nil || addr != "hub-01:443" || moved {
		t.Fatalf("closed hub: got %q %v %v", addr, moved, err)
	}
}

func TestWhereMovesFromDrainingHub(t *testing.T) {
	draining := hub1
	draining.Draining = true
	f := setup(t, "hub-01", []model.Hub{draining, hub2}, hub2)
	addr, moved, err := f.where(t)
	if err != nil || addr != "hub-02:443" || !moved {
		t.Fatalf("got %q %v %v", addr, moved, err)
	}
	if f.servers.hub["srv-1"] != "hub-02" || f.servers.updates != 1 {
		t.Fatalf("stored %q, updates %d", f.servers.hub["srv-1"], f.servers.updates)
	}
	if len(f.picker.excluded) != 1 || f.picker.excluded[0] != "hub-01" {
		t.Fatalf("picker excluded %v, want hub-01", f.picker.excluded)
	}
}

func TestWhereMovesFromMissingHub(t *testing.T) {
	f := setup(t, "hub-01", []model.Hub{hub2}, hub2)
	addr, moved, err := f.where(t)
	if err != nil || addr != "hub-02:443" || !moved || f.servers.hub["srv-1"] != "hub-02" {
		t.Fatalf("got %q %v %v (stored %q)", addr, moved, err, f.servers.hub["srv-1"])
	}
	// A server enrolled before hubs (none assigned) is given one.
	f = setup(t, "", []model.Hub{hub2}, hub2)
	if addr, moved, err := f.where(t); err != nil || addr != "hub-02:443" || !moved {
		t.Fatalf("no hub: got %q %v %v", addr, moved, err)
	}
}

func TestWhereStaleTimestamp(t *testing.T) {
	f := setup(t, "hub-01", []model.Hub{hub1})
	for _, ts := range []int64{now.Add(-6 * time.Minute).Unix(), now.Add(6 * time.Minute).Unix()} {
		_, err := f.uc.Where(context.Background(), "srv-1", ts, "n1", f.sign(t, "srv-1", ts, "n1"))
		if !errors.Is(err, ErrStaleRequest) {
			t.Fatalf("ts %d: err = %v, want ErrStaleRequest", ts, err)
		}
	}
	// Inside the window either way.
	for _, ts := range []int64{now.Add(-5 * time.Minute).Unix(), now.Add(5 * time.Minute).Unix()} {
		if _, err := f.uc.Where(context.Background(), "srv-1", ts, "n1", f.sign(t, "srv-1", ts, "n1")); err != nil {
			t.Fatalf("ts %d: %v", ts, err)
		}
	}
}

func TestWhereBadSignature(t *testing.T) {
	f := setup(t, "hub-01", []model.Hub{hub1})
	ts := now.Unix()
	// Signed for another nonce.
	if _, err := f.uc.Where(context.Background(), "srv-1", ts, "n2", f.sign(t, "srv-1", ts, "n1")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered: err = %v", err)
	}
	// Signed by another key.
	other, _ := newKeyCert(t)
	sig, _ := agentsig.Sign(other, agentsig.Payload("srv-1", ts, "n1"))
	if _, err := f.uc.Where(context.Background(), "srv-1", ts, "n1", sig); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("other key: err = %v", err)
	}
	// An unknown server reads the same.
	if _, err := f.uc.Where(context.Background(), "srv-9", ts, "n1", f.sign(t, "srv-9", ts, "n1")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown server: err = %v", err)
	}
	// A stale request with a bad signature is unauthorized, not stale.
	old := now.Add(-time.Hour).Unix()
	if _, err := f.uc.Where(context.Background(), "srv-1", old, "n1", "AAAA"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("stale+bad: err = %v", err)
	}
	// The certificate stored base64-encoded by the old registration flow.
	f.servers.cert["srv-1"] = base64.StdEncoding.EncodeToString([]byte(f.servers.cert["srv-1"]))
	if _, _, err := f.where(t); err != nil {
		t.Fatalf("base64 certificate: %v", err)
	}
}

func TestWhereNoHub(t *testing.T) {
	f := setup(t, "hub-01", nil)
	f.picker.err = hubs.ErrNoHub
	if _, _, err := f.where(t); !errors.Is(err, hubs.ErrNoHub) {
		t.Fatalf("err = %v, want ErrNoHub", err)
	}
	if f.servers.hub["srv-1"] != "hub-01" {
		t.Fatalf("assignment changed to %q", f.servers.hub["srv-1"])
	}
}

func TestWhereConcurrentMovesAgree(t *testing.T) {
	f := setup(t, "hub-01", []model.Hub{hub2, hub3}, hub2, hub3)
	// Both callers read hub-01 and pick before either writes.
	f.picker.barrier = &sync.WaitGroup{}
	f.picker.barrier.Add(2)
	type answer struct {
		addr  string
		moved bool
		err   error
	}
	out := make(chan answer, 2)
	sig := f.sign(t, "srv-1", now.Unix(), "n1")
	for range 2 {
		go func() {
			a, err := f.uc.Where(context.Background(), "srv-1", now.Unix(), "n1", sig)
			out <- answer{a.HubAddr, a.Moved, err}
		}()
	}
	a, b := <-out, <-out
	if a.err != nil || b.err != nil || a.addr != b.addr || !a.moved || !b.moved {
		t.Fatalf("answers differ: %+v %+v", a, b)
	}
	if f.servers.updates != 1 || f.servers.hub["srv-1"]+":443" != a.addr {
		t.Fatalf("updates %d, stored %q, answered %q", f.servers.updates, f.servers.hub["srv-1"], a.addr)
	}
}

func TestWhereExpiredCertificate(t *testing.T) {
	f := setup(t, "hub-01", []model.Hub{hub1})
	// The certificate expires at now+1h: an hour and a minute later it is
	// refused even with a good signature and a fresh timestamp.
	later := now.Add(time.Hour + time.Minute)
	f.uc.now = func() time.Time { return later }
	ts := later.Unix()
	if _, err := f.uc.Where(context.Background(), "srv-1", ts, "n1", f.sign(t, "srv-1", ts, "n1")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired certificate: err = %v, want ErrUnauthorized", err)
	}
}

func TestWhereReturnsHubFingerprints(t *testing.T) {
	// Staying and moving both carry the trusted set.
	for name, f := range map[string]*fixture{
		"stay": setup(t, "hub-01", []model.Hub{hub1}),
		"move": setup(t, "hub-01", []model.Hub{hub2}, hub2),
	} {
		ts := now.Unix()
		a, err := f.uc.Where(context.Background(), "srv-1", ts, "n1", f.sign(t, "srv-1", ts, "n1"))
		if err != nil || len(a.HubFingerprints) != 2 || a.HubFingerprints[0] != "aa" || a.HubFingerprints[1] != "bb" {
			t.Fatalf("%s: %+v %v", name, a, err)
		}
	}
	// None trusted yet: an empty set, never nil.
	f := setup(t, "hub-01", []model.Hub{hub1})
	f.pins.fps = nil
	ts := now.Unix()
	if a, err := f.uc.Where(context.Background(), "srv-1", ts, "n1", f.sign(t, "srv-1", ts, "n1")); err != nil || a.HubFingerprints == nil {
		t.Fatalf("empty: %+v %v", a, err)
	}
}

func TestWherePinsUnavailable(t *testing.T) {
	// The set cannot be read: fail closed before moving anything.
	f := setup(t, "hub-01", []model.Hub{hub2}, hub2)
	f.pins.err = errors.New("db down")
	if _, _, err := f.where(t); !errors.Is(err, ErrPinsUnavailable) {
		t.Fatalf("err = %v, want ErrPinsUnavailable", err)
	}
	if f.servers.hub["srv-1"] != "hub-01" {
		t.Fatalf("moved to %q although the answer failed", f.servers.hub["srv-1"])
	}
	// An unauthenticated caller learns nothing about it.
	f.pins.err = errors.New("db down")
	ts := now.Unix()
	if _, err := f.uc.Where(context.Background(), "srv-1", ts, "n2", f.sign(t, "srv-1", ts, "n1")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bad signature: err = %v", err)
	}
}
