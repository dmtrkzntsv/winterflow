package hubs

import (
	"context"
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

type fakeRegistry struct {
	hubs []model.Hub
	err  error
}

func (f *fakeRegistry) Announce(context.Context, model.Hub, time.Duration) error { return nil }
func (f *fakeRegistry) Withdraw(context.Context, string) error                   { return nil }
func (f *fakeRegistry) List(context.Context) ([]model.Hub, error)                { return f.hubs, f.err }

type fakeCounts struct {
	counts map[string]int
	err    error
}

func (f fakeCounts) CountServersByHub(context.Context) (map[string]int, error) {
	return f.counts, f.err
}

func newTestPicker(hubs []model.Hub, counts map[string]int) *Picker {
	p := NewPicker(&fakeRegistry{hubs: hubs}, fakeCounts{counts: counts}, logger.NewLogger(logger.LoggerConfiguration{}))
	p.rand = rand.New(rand.NewPCG(1, 2))
	return p
}

func TestPickRandomAmongRoomy(t *testing.T) {
	hubs := []model.Hub{
		{ID: "full", Capacity: 10, Accepting: true},
		{ID: "closed", Capacity: 10, Accepting: false},
		{ID: "draining", Capacity: 10, Accepting: true, Draining: true},
		{ID: "roomy", Capacity: 10, Accepting: true},
	}
	p := newTestPicker(hubs, map[string]int{"full": 10, "roomy": 3})
	for range 100 {
		got, err := p.Pick(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != "roomy" || got.Assigned != 3 {
			t.Fatalf("picked %+v", got)
		}
	}
}

func TestPickSpreadsAmongRoomy(t *testing.T) {
	hubs := []model.Hub{
		{ID: "a", Capacity: 10, Accepting: true},
		{ID: "b", Capacity: 10, Accepting: true},
	}
	p := newTestPicker(hubs, nil)
	seen := map[string]int{}
	for range 100 {
		got, err := p.Pick(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		seen[got.ID]++
	}
	if seen["a"] == 0 || seen["b"] == 0 {
		t.Fatalf("not random: %v", seen)
	}
}

func TestPickAllFullLeastRatio(t *testing.T) {
	hubs := []model.Hub{
		{ID: "a", Capacity: 10, Accepting: true},
		{ID: "b", Capacity: 20, Accepting: true},
	}
	p := newTestPicker(hubs, map[string]int{"a": 10, "b": 25})
	got, err := p.Pick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// a: 10/10 = 1.0, b: 25/20 = 1.25 -> a. Swap the loads -> b.
	if got.ID != "a" {
		t.Fatalf("picked %s", got.ID)
	}
	p = newTestPicker([]model.Hub{
		{ID: "a", Capacity: 10, Accepting: true},
		{ID: "b", Capacity: 20, Accepting: true},
	}, map[string]int{"a": 12, "b": 20})
	if got, err := p.Pick(context.Background()); err != nil || got.ID != "b" {
		t.Fatalf("picked %+v, %v", got, err)
	}
}

func TestPickSkipsFullHub(t *testing.T) {
	// Capacities 10/20 with 10/15 assigned: the first is full, the second
	// has room and is chosen.
	p := newTestPicker([]model.Hub{
		{ID: "a", Capacity: 10, Accepting: true},
		{ID: "b", Capacity: 20, Accepting: true},
	}, map[string]int{"a": 10, "b": 15})
	if got, err := p.Pick(context.Background()); err != nil || got.ID != "b" {
		t.Fatalf("picked %+v, %v", got, err)
	}
}

func TestPickNoneListed(t *testing.T) {
	p := newTestPicker(nil, nil)
	if _, err := p.Pick(context.Background()); !errors.Is(err, ErrNoHub) {
		t.Fatalf("want ErrNoHub, got %v", err)
	}
	// Listed but none accepting or all draining: no hub either.
	p = newTestPicker([]model.Hub{
		{ID: "closed", Capacity: 10},
		{ID: "draining", Capacity: 10, Accepting: true, Draining: true},
	}, nil)
	if _, err := p.Pick(context.Background()); !errors.Is(err, ErrNoHub) {
		t.Fatalf("want ErrNoHub, got %v", err)
	}
}

func TestPickErrors(t *testing.T) {
	p := NewPicker(&fakeRegistry{err: errors.New("redis down")}, fakeCounts{}, logger.NewLogger(logger.LoggerConfiguration{}))
	if _, err := p.Pick(context.Background()); err == nil || errors.Is(err, ErrNoHub) {
		t.Fatalf("registry error: got %v", err)
	}
	p = NewPicker(&fakeRegistry{hubs: []model.Hub{{ID: "a", Capacity: 1, Accepting: true}}}, fakeCounts{err: errors.New("db down")}, logger.NewLogger(logger.LoggerConfiguration{}))
	if _, err := p.Pick(context.Background()); err == nil {
		t.Fatal("count error: want error")
	}
}

func TestPickExceptSkipsTheHub(t *testing.T) {
	hubs := []model.Hub{
		{ID: "a", Capacity: 10, Accepting: true},
		{ID: "b", Capacity: 10, Accepting: true},
	}
	p := newTestPicker(hubs, nil)
	for range 50 {
		got, err := p.PickExcept(context.Background(), "a")
		if err != nil || got.ID != "b" {
			t.Fatalf("picked %+v, %v", got, err)
		}
	}
	// The least-loaded fallback skips it too.
	p = newTestPicker(hubs, map[string]int{"a": 10, "b": 20})
	if got, err := p.PickExcept(context.Background(), "a"); err != nil || got.ID != "b" {
		t.Fatalf("full: picked %+v, %v", got, err)
	}
	// The only hub excluded: none.
	p = newTestPicker(hubs[:1], nil)
	if _, err := p.PickExcept(context.Background(), "a"); !errors.Is(err, ErrNoHub) {
		t.Fatalf("err = %v, want ErrNoHub", err)
	}
}
