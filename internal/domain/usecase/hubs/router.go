package hubs

import (
	"context"
	"fmt"

	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
)

// ErrServerHasNoHub: the server was never assigned a hub (enrolled before
// hubs were assigned); its agent has to be reinstalled. Classed
// model.ErrConflict, so a route answers 409 with the message.
var ErrServerHasNoHub error = model.ErrServerHasNoHub

// Router names the request queue a command for a server is published to.
type Router interface {
	QueueFor(ctx context.Context, serverID string) (string, error)
}

// ServerHubs is the slice of port.ServerRepository the router needs.
type ServerHubs interface {
	ServerHub(ctx context.Context, serverID string) (string, error)
}

type hubRouter struct {
	servers ServerHubs
	cfg     *config.ServerConfig
}

// NewRouter routes by servers.hub_id, read on every call: a cache would go
// stale on the other API replicas when a server moves to another hub.
func NewRouter(servers ServerHubs, cfg *config.ServerConfig) Router {
	return &hubRouter{servers: servers, cfg: cfg}
}

func (r *hubRouter) QueueFor(ctx context.Context, serverID string) (string, error) {
	hub, err := r.servers.ServerHub(ctx, serverID)
	if err != nil {
		return "", fmt.Errorf("server %s hub: %w", serverID, err)
	}
	if hub == "" {
		return "", ErrServerHasNoHub
	}
	return r.cfg.HubRequestQueue(hub), nil
}

type singleQueue struct{ queue string }

// SingleQueue routes every command to the plain request queue (standalone:
// the in-process bridge is the only consumer).
func SingleQueue(cfg *config.ServerConfig) Router {
	return singleQueue{queue: cfg.GetBusRequestQueue()}
}

func (s singleQueue) QueueFor(context.Context, string) (string, error) { return s.queue, nil }
