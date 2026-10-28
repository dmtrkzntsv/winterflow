package cmdresult

import (
	"context"
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

func open(t *testing.T) *Repo {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func create(t *testing.T, r *Repo, id string, at time.Time) {
	t.Helper()
	if err := r.Create(context.Background(), model.CommandRecord{
		RequestID: id, OrgID: "org-1", ServerID: "srv-1", Kind: "app.save",
		Status: model.CommandRunning, CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql"); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestRoundTrip(t *testing.T) {
	r, ctx := open(t), context.Background()
	create(t, r, "req-1", t0)
	got, err := r.Get(ctx, "org-1", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.CommandRunning || got.ServerID != "srv-1" || got.Kind != "app.save" ||
		!got.CreatedAt.Equal(t0) || got.FinishedAt != nil || got.Result != nil || got.Error != "" {
		t.Fatalf("running = %+v", got)
	}
	if _, err := r.Get(ctx, "org-2", "req-1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("other org = %v", err)
	}

	end := t0.Add(time.Minute)
	ok, err := r.Finish(ctx, "req-1", model.CommandFailed, []byte(`{"a":1}`), "boom", end)
	if err != nil || !ok {
		t.Fatalf("finish = %v, %v", ok, err)
	}
	got, _ = r.Get(ctx, "org-1", "req-1")
	if got.Status != model.CommandFailed || string(got.Result) != `{"a":1}` || got.Error != "boom" ||
		got.FinishedAt == nil || !got.FinishedAt.Equal(end) {
		t.Fatalf("finished = %+v", got)
	}
	if ok, err := r.Finish(ctx, "req-1", model.CommandSucceeded, nil, "", end); err != nil || ok {
		t.Fatalf("second finish = %v, %v", ok, err)
	}
	if ok, err := r.Finish(ctx, "missing", model.CommandSucceeded, nil, "", end); err != nil || ok {
		t.Fatalf("finish of no row = %v, %v", ok, err)
	}
}

func TestMarkStaleAndPrune(t *testing.T) {
	r, ctx := open(t), context.Background()
	create(t, r, "old", t0)
	create(t, r, "new", t0.Add(time.Hour))
	now := t0.Add(time.Hour)
	if n, err := r.MarkStale(ctx, t0.Add(30*time.Minute), now); err != nil || n != 1 {
		t.Fatalf("mark stale = %d, %v", n, err)
	}
	got, _ := r.Get(ctx, "org-1", "old")
	if got.Status != model.CommandUnknown || got.FinishedAt == nil || !got.FinishedAt.Equal(now) {
		t.Fatalf("old = %+v", got)
	}
	if n, err := r.Prune(ctx, now); err != nil || n != 0 {
		t.Fatalf("prune at its end = %d, %v", n, err)
	}
	if n, err := r.Prune(ctx, now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("prune after its end = %d, %v", n, err)
	}
	if _, err := r.Get(ctx, "org-1", "new"); err != nil {
		t.Fatalf("a running row was pruned: %v", err)
	}
}
