// Package agenthub answers an agent's "where should I connect": the hub its
// server is assigned to while that hub is live and not draining, else a new
// hub the server is moved to.
package agenthub

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/agentauth"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/pkg/agentsig"
	"winterflow/pkg/logger"
)

// MaxSkew is how far a request's timestamp may be from the API's clock.
const MaxSkew = 5 * time.Minute

var (
	// ErrUnauthorized: the signature does not verify against the server's
	// active certificate, that certificate has expired, or there is no such
	// server (401).
	ErrUnauthorized = errors.New("signature does not verify")
	// ErrStaleRequest: the timestamp is more than MaxSkew off (400).
	ErrStaleRequest = errors.New("timestamp is more than 5 minutes off")
)

// Servers is the slice of port.ServerRepository the usecase uses.
type Servers interface {
	ServerHub(ctx context.Context, serverID string) (string, error)
	ActiveServerCertificate(ctx context.Context, serverID string) (string, error)
	SetServerHub(ctx context.Context, serverID, fromHubID, toHubID string) (string, error)
}

var _ Servers = port.ServerRepository(nil)

// HubLister is the slice of port.HubRegistry the usecase reads.
type HubLister interface {
	List(ctx context.Context) ([]model.Hub, error)
}

// Picker chooses a hub other than the one given (*hubs.Picker).
type Picker interface {
	PickExcept(ctx context.Context, hubID string) (model.Hub, error)
}

type UseCase struct {
	servers  Servers
	registry HubLister
	picker   Picker
	log      *logger.Logger
	now      func() time.Time
}

func New(servers Servers, registry HubLister, picker Picker, log *logger.Logger) *UseCase {
	return &UseCase{servers: servers, registry: registry, picker: picker, log: log, now: time.Now}
}

// Where verifies the agent's signature over (serverID, ts, nonce) against
// the server's active certificate (refused once it has expired) and the timestamp against MaxSkew, then
// answers the address of the hub the agent should connect to. moved is false
// when the server's hub is listed and not draining (the agent's trouble is
// its own network); otherwise a hub other than the current one is picked
// and the server moved to it, and when another replica moved it first, that
// replica's hub is the answer. hubs.ErrNoHub is passed through.
func (u *UseCase) Where(ctx context.Context, serverID string, ts int64, nonce, sig string) (string, bool, error) {
	stored, err := u.servers.ActiveServerCertificate(ctx, serverID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return "", false, ErrUnauthorized
		}
		return "", false, fmt.Errorf("read certificate: %w", err)
	}
	certPEM := agentauth.CertPEM(stored)
	if agentsig.Verify(certPEM, agentsig.Payload(serverID, ts, nonce), sig) != nil {
		return "", false, ErrUnauthorized
	}
	if expired(certPEM, u.now()) {
		return "", false, ErrUnauthorized
	}
	now := u.now().Unix()
	skew := int64(MaxSkew / time.Second)
	if ts < now-skew || ts > now+skew {
		return "", false, ErrStaleRequest
	}
	current, err := u.servers.ServerHub(ctx, serverID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return "", false, ErrUnauthorized
		}
		return "", false, fmt.Errorf("read hub: %w", err)
	}
	listed, err := u.registry.List(ctx)
	if err != nil {
		return "", false, fmt.Errorf("list hubs: %w", err)
	}
	if h, ok := find(listed, current); ok && !h.Draining {
		return h.PublicAddr, false, nil
	}
	pick, err := u.picker.PickExcept(ctx, current)
	if err != nil {
		return "", false, err
	}
	got, err := u.servers.SetServerHub(ctx, serverID, current, pick.ID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return "", false, ErrUnauthorized
		}
		return "", false, fmt.Errorf("move server: %w", err)
	}
	if got == pick.ID {
		u.log.Info("server moved to another hub", "server_id", serverID, "from", current, "to", pick.ID)
		return pick.PublicAddr, true, nil
	}
	// Another replica moved it first: answer with its hub.
	if h, ok := find(listed, got); ok {
		return h.PublicAddr, true, nil
	}
	if listed, err = u.registry.List(ctx); err != nil {
		return "", false, fmt.Errorf("list hubs: %w", err)
	}
	if h, ok := find(listed, got); ok {
		return h.PublicAddr, true, nil
	}
	// Its hub dropped out of the list meanwhile: the agent asks again.
	return "", false, fmt.Errorf("server moved to hub %q, which is not listed: %w", got, hubs.ErrNoHub)
}

// expired reports whether the PEM certificate's NotAfter is before now (an
// unreadable one counts as expired; Verify has refused it already).
func expired(certPEM string, now time.Time) bool {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return true
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	return crt.NotAfter.Before(now)
}

func find(listed []model.Hub, id string) (model.Hub, bool) {
	if id == "" {
		return model.Hub{}, false
	}
	for _, h := range listed {
		if h.ID == id {
			return h, true
		}
	}
	return model.Hub{}, false
}
