package secretreq

import (
	"context"
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org    = dbtest.ID(1)
	server = dbtest.ID(2)
	user   = dbtest.ID(3)
	t0     = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
)

func open(t *testing.T) *Repo {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org, server)
	dbtest.SeedMember(t, d, user, org)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func create(t *testing.T, r *Repo, id, hash, appID string, at time.Time) {
	t.Helper()
	if err := r.Create(context.Background(), model.SecretRequest{
		ID: id, OrgID: org, ServerID: server, AppID: appID, CreatedBy: user,
		Names: []string{"API_KEY", "DB_PASSWORD"}, Notes: map[string]string{"API_KEY": "from the dashboard"},
		Message: "please", CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour),
	}, hash); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql"); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestLifecycle(t *testing.T) {
	r, ctx := open(t), context.Background()
	id := dbtest.ID(10)
	create(t, r, id, "hash-1", "app-1", t0)

	got, err := r.GetByTokenHash(ctx, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.OrgID != org || got.ServerID != server || got.AppID != "app-1" || got.CreatedBy != user ||
		len(got.Names) != 2 || got.Notes["API_KEY"] != "from the dashboard" || got.Message != "please" ||
		got.Status != model.SecretPending || got.Ciphertexts != nil || !got.CreatedAt.Equal(t0) ||
		!got.ExpiresAt.Equal(t0.Add(24*time.Hour)) || got.SubmittedAt != nil || got.EndedAt != nil {
		t.Fatalf("pending = %+v", got)
	}
	if _, err := r.GetByTokenHash(ctx, "nope"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown hash = %v", err)
	}
	if _, err := r.Get(ctx, dbtest.ID(99), id); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("other org = %v", err)
	}

	// Past its deadline a request takes no submission.
	if ok, err := r.Submit(ctx, id, map[string]string{"API_KEY": "c"}, t0.Add(25*time.Hour), t0.Add(49*time.Hour)); err != nil || ok {
		t.Fatalf("late submit = %v, %v", ok, err)
	}
	sub := t0.Add(time.Hour)
	cts := map[string]string{"API_KEY": "c1", "DB_PASSWORD": "c2"}
	if ok, err := r.Submit(ctx, id, cts, sub, sub.Add(24*time.Hour)); err != nil || !ok {
		t.Fatalf("submit = %v, %v", ok, err)
	}
	if ok, err := r.Submit(ctx, id, cts, sub, sub.Add(24*time.Hour)); err != nil || ok {
		t.Fatalf("second submit = %v, %v", ok, err)
	}
	got, _ = r.Get(ctx, org, id)
	if got.Status != model.SecretSubmitted || got.Ciphertexts["DB_PASSWORD"] != "c2" || got.SubmittedAt == nil ||
		!got.SubmittedAt.Equal(sub) || !got.ExpiresAt.Equal(sub.Add(24*time.Hour)) {
		t.Fatalf("submitted = %+v", got)
	}

	used := sub.Add(time.Minute)
	if ok, err := r.MarkUsed(ctx, id, used); err != nil || !ok {
		t.Fatalf("use = %v, %v", ok, err)
	}
	if ok, err := r.MarkUsed(ctx, id, used); err != nil || ok {
		t.Fatalf("second use = %v, %v", ok, err)
	}
	got, _ = r.Get(ctx, org, id)
	if got.Status != model.SecretUsed || got.Ciphertexts != nil || got.UsedAt == nil || got.EndedAt == nil || !got.EndedAt.Equal(used) {
		t.Fatalf("used = %+v", got)
	}
}

func TestExpireAndPrune(t *testing.T) {
	r, ctx := open(t), context.Background()
	pending, submitted, fresh := dbtest.ID(10), dbtest.ID(11), dbtest.ID(12)
	create(t, r, pending, "h-pending", "", t0)
	create(t, r, submitted, "h-submitted", "", t0)
	create(t, r, fresh, "h-fresh", "", t0.Add(30*time.Hour))
	if ok, err := r.Submit(ctx, submitted, map[string]string{"API_KEY": "c"}, t0.Add(time.Hour), t0.Add(25*time.Hour)); err != nil || !ok {
		t.Fatalf("submit = %v, %v", ok, err)
	}
	now := t0.Add(26 * time.Hour)
	if n, err := r.Expire(ctx, now); err != nil || n != 2 {
		t.Fatalf("expire = %d, %v", n, err)
	}
	got, _ := r.Get(ctx, org, submitted)
	if got.Status != model.SecretExpired || got.Ciphertexts != nil || got.EndedAt == nil || !got.EndedAt.Equal(now) {
		t.Fatalf("expired submitted = %+v", got)
	}
	if got, _ := r.Get(ctx, org, fresh); got.Status != model.SecretPending {
		t.Fatalf("fresh = %+v", got)
	}
	if n, err := r.Prune(ctx, now); err != nil || n != 0 {
		t.Fatalf("prune at the end = %d, %v", n, err)
	}
	if n, err := r.Prune(ctx, now.Add(time.Second)); err != nil || n != 2 {
		t.Fatalf("prune after = %d, %v", n, err)
	}
	if _, err := r.Get(ctx, org, fresh); err != nil {
		t.Fatalf("a live request was pruned: %v", err)
	}
}
