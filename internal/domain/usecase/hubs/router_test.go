package hubs

import (
	"context"
	"errors"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/pkg/config"
)

type fakeServerHubs map[string]string // server id -> hub id ("" none)

func (f fakeServerHubs) ServerHub(_ context.Context, serverID string) (string, error) {
	hub, ok := f[serverID]
	if !ok {
		return "", model.ErrNotFound
	}
	return hub, nil
}

func TestRouterQueue(t *testing.T) {
	cfg := config.NewServerConfig("distributed")
	servers := fakeServerHubs{"srv-1": "hub-01.winterflow.dev"}
	r := NewRouter(servers, cfg)
	q, err := r.QueueFor(context.Background(), "srv-1")
	if err != nil || q != "bus:requests:hub-01.winterflow.dev" {
		t.Fatalf("queue = %q, %v", q, err)
	}
	// No cache: a moved server is routed to its new hub at once.
	servers["srv-1"] = "hub-02.winterflow.dev"
	q, err = r.QueueFor(context.Background(), "srv-1")
	if err != nil || q != "bus:requests:hub-02.winterflow.dev" {
		t.Fatalf("after move = %q, %v", q, err)
	}
}

func TestRouterNoHub(t *testing.T) {
	r := NewRouter(fakeServerHubs{"srv-1": ""}, config.NewServerConfig("distributed"))
	if _, err := r.QueueFor(context.Background(), "srv-1"); !errors.Is(err, ErrServerHasNoHub) {
		t.Fatalf("want ErrServerHasNoHub, got %v", err)
	}
	if _, err := r.QueueFor(context.Background(), "srv-unknown"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown server: want ErrNotFound, got %v", err)
	}
}

func TestSingleQueue(t *testing.T) {
	cfg := config.NewServerConfig("standalone")
	q, err := SingleQueue(cfg).QueueFor(context.Background(), "anything")
	if err != nil || q != "bus:requests" {
		t.Fatalf("queue = %q, %v", q, err)
	}
}

func TestErrServerHasNoHubIsConflict(t *testing.T) {
	if !errors.Is(ErrServerHasNoHub, model.ErrConflict) || ErrServerHasNoHub.Error() != "server has no hub; reinstall the agent" {
		t.Fatalf("class/message: %v", ErrServerHasNoHub)
	}
}
