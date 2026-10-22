package hubtoken

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/infra/db/dbtest"
)

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql"); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestConsume(t *testing.T) {
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	n := now()
	if err := r.Create(ctx, "live", n.Add(time.Hour), n); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, "old", n.Add(-time.Minute), n.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Consume(ctx, "missing", "h", n); err != nil || ok {
		t.Fatalf("missing = %v, %v", ok, err)
	}
	if ok, err := r.Consume(ctx, "old", "h", n); err != nil || ok {
		t.Fatalf("expired = %v, %v", ok, err)
	}
	if ok, err := r.Consume(ctx, "live", "hub-01", n); err != nil || !ok {
		t.Fatalf("first = %v, %v", ok, err)
	}
	if ok, err := r.Consume(ctx, "live", "hub-02", n); err != nil || ok {
		t.Fatalf("second = %v, %v", ok, err)
	}
}

func TestConsumeConcurrent(t *testing.T) {
	d := dbtest.Open(t)
	r, _ := New(d.Tx, d.Driver)
	ctx := context.Background()
	n := now()
	if err := r.Create(ctx, "t", n.Add(time.Hour), n); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := r.Consume(ctx, "t", "h", n); err == nil && ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d winners, want 1", wins.Load())
	}
}

func TestPrune(t *testing.T) {
	d := dbtest.Open(t)
	r, _ := New(d.Tx, d.Driver)
	ctx := context.Background()
	n := now()
	for _, c := range []struct {
		hash string
		exp  time.Time
	}{{"expired-long", n.Add(-48 * time.Hour)}, {"expired-recent", n.Add(-time.Hour)}, {"live", n.Add(time.Hour)}, {"used-long", n.Add(time.Hour)}} {
		if err := r.Create(ctx, c.hash, c.exp, n.Add(-72*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _ := r.Consume(ctx, "used-long", "h", n.Add(-48*time.Hour)); !ok {
		t.Fatal("consume failed")
	}
	if err := r.Prune(ctx, n.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM hub_join_tokens`); got != "2" {
		t.Fatalf("%s rows left, want 2 (expired-recent, live)", got)
	}
}
