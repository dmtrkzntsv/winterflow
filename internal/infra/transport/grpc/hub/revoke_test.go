package hub

import (
	"context"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/grpc/proto"
	membus "winterflow/internal/infra/transport/mem/bus"
	"winterflow/pkg/logger"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// blockingAuth allows, but only once released; entered signals the call.
type blockingAuth struct {
	entered chan struct{}
	release chan struct{}
}

func (a blockingAuth) Authorize(context.Context, string, string) (bus.AgentVerdict, string, error) {
	a.entered <- struct{}{}
	<-a.release
	return bus.VerdictAllowed, "", nil
}

func registerAsync(t *testing.T, h *Hub, serverID string) <-chan error {
	t.Helper()
	ctx := peerCtx([][]*x509.Certificate{{selfSigned(t, serverID)}})
	done := make(chan error, 1)
	go func() {
		_, err := h.RegisterAgent(ctx, &proto.RegisterAgentRequest{Base: &proto.BaseMessage{AgentId: serverID}})
		done <- err
	}()
	return done
}

func TestRevokeDuringAuthorizeRefusesRegistration(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	h := newTestHub(t, membus.NewBus(log))
	auth := blockingAuth{entered: make(chan struct{}), release: make(chan struct{})}
	h.auth = auth

	done := registerAsync(t, h, "srv-1")
	<-auth.entered
	h.revoke("srv-1")
	close(auth.release)
	if err := <-done; status.Code(err) != codes.PermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
	if _, ok := h.agents["srv-1"]; ok {
		t.Fatal("revoked server registered")
	}

	// A check that starts after the revocation (e.g. a re-enrolled server's
	// new cert) is not affected.
	auth2 := blockingAuth{entered: make(chan struct{}, 1), release: make(chan struct{})}
	close(auth2.release)
	h.auth = auth2
	if err := <-registerAsync(t, h, "srv-1"); err != nil {
		t.Fatalf("registration after the revocation: %v", err)
	}
}

func TestRevokedAtIsPruned(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	h := newTestHub(t, membus.NewBus(log))
	h.revoke("old")
	h.revokedAt["old"] = time.Now().Add(-revokeMemory - time.Second)
	h.revoke("new")
	if _, ok := h.revokedAt["old"]; ok {
		t.Fatal("old revocation kept")
	}
	if _, ok := h.revokedAt["new"]; !ok {
		t.Fatal("new revocation missing")
	}
	h.revokedAt["new"] = time.Now().Add(-revokeMemory - time.Second)
	h.reapOnce(time.Now())
	if len(h.revokedAt) != 0 {
		t.Fatalf("reaper left %v", h.revokedAt)
	}
}

func TestReapClosesStream(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	h := newTestHub(t, membus.NewBus(log))
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	h.agents["stale"] = &agent{lastSeen: time.Now().Add(-5 * time.Minute), streamActive: true, closeStream: cancel}

	if n := h.reapOnce(time.Now()); n != 1 {
		t.Fatalf("removed = %d", n)
	}
	if !errors.Is(context.Cause(ctx), errStale) {
		t.Fatalf("stream cause = %v, want errStale", context.Cause(ctx))
	}
}
