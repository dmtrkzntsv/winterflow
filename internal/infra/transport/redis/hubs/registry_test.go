package hubs

import (
	"context"
	"sort"
	"testing"
	"time"

	"winterflow/internal/domain/model"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRegistry(t *testing.T) (*Registry, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	return NewRegistry(rc), mr
}

func TestAnnounceAndList(t *testing.T) {
	reg, mr := newRegistry(t)
	ctx := context.Background()
	a := model.Hub{ID: "hub-01.winterflow.dev", PublicAddr: "hub-01.winterflow.dev:50051", Version: "1.0.0", Capacity: 1000, ConnectedAgents: 3, Accepting: true}
	b := model.Hub{ID: "hub-02.winterflow.dev", PublicAddr: "hub-02.winterflow.dev:50051", Capacity: 500}
	if err := reg.Announce(ctx, a, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := reg.Announce(ctx, b, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if ttl := mr.TTL("hub:status:hub-01.winterflow.dev"); ttl != 90*time.Second {
		t.Fatalf("ttl = %v", ttl)
	}
	mr.Set("hub:status:broken", "{not json")

	got, err := reg.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("list = %+v", got)
	}

	// b's key expires; only a is left.
	mr.FastForward(31 * time.Second)
	got, err = reg.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("after expiry = %+v", got)
	}

	if err := reg.Withdraw(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := reg.List(ctx); err != nil || len(got) != 0 {
		t.Fatalf("after withdraw = %+v, %v", got, err)
	}
}

func TestListEmpty(t *testing.T) {
	reg, _ := newRegistry(t)
	got, err := reg.List(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("empty = %+v, %v", got, err)
	}
}

func TestRegistryRedisDown(t *testing.T) {
	reg, mr := newRegistry(t)
	mr.Close()
	if _, err := reg.List(context.Background()); err == nil {
		t.Fatal("list with redis down: want error")
	}
	if err := reg.Announce(context.Background(), model.Hub{ID: "x"}, time.Second); err == nil {
		t.Fatal("announce with redis down: want error")
	}
}

func TestDrainFlag(t *testing.T) {
	reg, mr := newRegistry(t)
	ctx := context.Background()
	const id = "hub-01.winterflow.dev"
	if on, err := reg.IsDraining(ctx, id); err != nil || on {
		t.Fatalf("before = %v, %v", on, err)
	}
	if err := reg.SetDraining(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if !mr.Exists("hub:draining:" + id) {
		t.Fatal("flag key missing")
	}
	if ttl := mr.TTL("hub:draining:" + id); ttl != 0 {
		t.Fatalf("flag has an expiry: %v", ttl)
	}
	if on, err := reg.IsDraining(ctx, id); err != nil || !on {
		t.Fatalf("after set = %v, %v", on, err)
	}
	if on, _ := reg.IsDraining(ctx, "hub-02.winterflow.dev"); on {
		t.Fatal("another hub reads draining")
	}
	if err := reg.SetDraining(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if on, err := reg.IsDraining(ctx, id); err != nil || on {
		t.Fatalf("after clear = %v, %v", on, err)
	}
	if err := reg.SetDraining(ctx, "", true); err == nil {
		t.Fatal("empty hub id accepted")
	}
	mr.Close()
	if _, err := reg.IsDraining(ctx, id); err == nil {
		t.Fatal("is-draining with redis down: want error")
	}
}
