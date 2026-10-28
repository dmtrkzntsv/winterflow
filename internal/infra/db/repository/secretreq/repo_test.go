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

	claimAt := sub.Add(time.Minute)
	if ok, err := r.Claim(ctx, id, "c-1", claimAt, claimAt.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	// A live claim is not taken again; a release frees it.
	if ok, err := r.Claim(ctx, id, "c-2", claimAt, claimAt.Add(2*time.Minute)); err != nil || ok {
		t.Fatalf("second claim = %v, %v", ok, err)
	}
	got, _ = r.Get(ctx, org, id)
	if got.Status != model.SecretClaimed || got.ClaimedUntil == nil || !got.ClaimedUntil.Equal(claimAt.Add(2*time.Minute)) {
		t.Fatalf("claimed = %+v", got)
	}
	if ok, err := r.Release(ctx, id, "c-2"); err != nil || ok {
		t.Fatalf("release by another claim = %v, %v", ok, err)
	}
	if ok, err := r.Release(ctx, id, "c-1"); err != nil || !ok {
		t.Fatalf("release = %v, %v", ok, err)
	}
	if got, _ = r.Get(ctx, org, id); got.Status != model.SecretSubmitted || got.ClaimedUntil != nil {
		t.Fatalf("released = %+v", got)
	}
	// A lapsed claim is taken over; its old holder can neither hold nor use it.
	if ok, err := r.Claim(ctx, id, "c-3", claimAt, claimAt.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	later := claimAt.Add(time.Minute)
	if ok, err := r.Claim(ctx, id, "c-4", later, later.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("claim after the lapse = %v, %v", ok, err)
	}
	if ok, err := r.MarkDispatched(ctx, id, "c-3", later.Add(time.Hour)); err != nil || ok {
		t.Fatalf("dispatch by a lost claim = %v, %v", ok, err)
	}
	if ok, err := r.MarkUsed(ctx, id, "c-3", later); err != nil || ok {
		t.Fatalf("use by a lost claim = %v, %v", ok, err)
	}
	// A claim not yet dispatched is not used.
	if ok, err := r.MarkUsed(ctx, id, "c-4", later); err != nil || ok {
		t.Fatalf("use before the dispatch = %v, %v", ok, err)
	}
	if ok, err := r.MarkDispatched(ctx, id, "c-4", later.Add(3*time.Minute)); err != nil || !ok {
		t.Fatalf("dispatch = %v, %v", ok, err)
	}
	// Dispatched, it is taken by no claim, even once its deadline passed.
	gone := later.Add(time.Hour)
	if ok, err := r.Claim(ctx, id, "c-5", gone, gone.Add(2*time.Minute)); err != nil || ok {
		t.Fatalf("claim of a dispatched request = %v, %v", ok, err)
	}
	if got, _ = r.Get(ctx, org, id); got.Status != model.SecretDispatched || got.Ciphertexts == nil {
		t.Fatalf("dispatched = %+v", got)
	}

	used := later.Add(time.Second)
	if ok, err := r.MarkUsed(ctx, id, "c-4", used); err != nil || !ok {
		t.Fatalf("use = %v, %v", ok, err)
	}
	if ok, err := r.MarkUsed(ctx, id, "c-4", used); err != nil || ok {
		t.Fatalf("second use = %v, %v", ok, err)
	}
	got, _ = r.Get(ctx, org, id)
	if got.Status != model.SecretUsed || got.Ciphertexts != nil || got.UsedAt == nil || got.EndedAt == nil ||
		!got.EndedAt.Equal(used) || got.ClaimedUntil != nil {
		t.Fatalf("used = %+v", got)
	}
}

func TestExpireAndPrune(t *testing.T) {
	r, ctx := open(t), context.Background()
	pending, submitted, fresh, held := dbtest.ID(10), dbtest.ID(11), dbtest.ID(12), dbtest.ID(13)
	create(t, r, pending, "h-pending", "", t0)
	create(t, r, submitted, "h-submitted", "", t0)
	create(t, r, fresh, "h-fresh", "", t0.Add(30*time.Hour))
	create(t, r, held, "h-held", "", t0)
	for _, id := range []string{submitted, held} {
		if ok, err := r.Submit(ctx, id, map[string]string{"API_KEY": "c"}, t0.Add(time.Hour), t0.Add(25*time.Hour)); err != nil || !ok {
			t.Fatalf("submit = %v, %v", ok, err)
		}
	}
	// A save holds this one across its deadline: it is not expired under it.
	if ok, err := r.Claim(ctx, held, "c", t0.Add(25*time.Hour-time.Minute), t0.Add(26*time.Hour+30*time.Minute)); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	now := t0.Add(26 * time.Hour)
	if n, err := r.Expire(ctx, now); err != nil || n != 2 {
		t.Fatalf("expire = %d, %v", n, err)
	}
	if got, _ := r.Get(ctx, org, held); got.Status != model.SecretClaimed {
		t.Fatalf("held = %+v", got)
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
	// Once the claim lapses, the sweep expires it too.
	if n, err := r.Expire(ctx, t0.Add(27*time.Hour)); err != nil || n != 1 {
		t.Fatalf("expire after the lapse = %d, %v", n, err)
	}
	if n, err := r.Prune(ctx, now.Add(time.Second)); err != nil || n != 2 {
		t.Fatalf("prune after = %d, %v", n, err)
	}
	if _, err := r.Get(ctx, org, fresh); err != nil {
		t.Fatalf("a live request was pruned: %v", err)
	}
}

// TestDispatched: a dispatched request returns to submitted only by its
// holder's release (the send failed); one whose holder went away is ended
// used by the sweep once its deadline passed, its ciphertexts deleted.
func TestDispatched(t *testing.T) {
	r, ctx := open(t), context.Background()
	released, left := dbtest.ID(20), dbtest.ID(21)
	for i, id := range []string{released, left} {
		create(t, r, id, "h-d"+string(rune('a'+i)), "", t0)
		if ok, err := r.Submit(ctx, id, map[string]string{"API_KEY": "c"}, t0.Add(time.Hour), t0.Add(25*time.Hour)); err != nil || !ok {
			t.Fatalf("submit = %v, %v", ok, err)
		}
		at := t0.Add(2 * time.Hour)
		if ok, err := r.Claim(ctx, id, "c", at, at.Add(2*time.Minute)); err != nil || !ok {
			t.Fatalf("claim = %v, %v", ok, err)
		}
		if ok, err := r.MarkDispatched(ctx, id, "c", at.Add(2*time.Minute)); err != nil || !ok {
			t.Fatalf("dispatch = %v, %v", ok, err)
		}
	}
	if ok, err := r.Release(ctx, released, "other"); err != nil || ok {
		t.Fatalf("release by another claim = %v, %v", ok, err)
	}
	if ok, err := r.Release(ctx, released, "c"); err != nil || !ok {
		t.Fatalf("release = %v, %v", ok, err)
	}
	if got, _ := r.Get(ctx, org, released); got.Status != model.SecretSubmitted || got.Ciphertexts == nil {
		t.Fatalf("released = %+v", got)
	}
	// Before its deadline the sweep leaves it alone.
	if n, err := r.Expire(ctx, t0.Add(2*time.Hour+time.Minute)); err != nil || n != 0 {
		t.Fatalf("expire before the deadline = %d, %v", n, err)
	}
	end := t0.Add(3 * time.Hour)
	if n, err := r.Expire(ctx, end); err != nil || n != 1 {
		t.Fatalf("expire after the deadline = %d, %v", n, err)
	}
	got, _ := r.Get(ctx, org, left)
	if got.Status != model.SecretUsed || got.Ciphertexts != nil || got.EndedAt == nil || !got.EndedAt.Equal(end) {
		t.Fatalf("a dispatched request whose holder went away = %+v", got)
	}
	if ok, err := r.MarkUsed(ctx, left, "c", end); err != nil || ok {
		t.Fatalf("use after the sweep = %v, %v", ok, err)
	}
}
