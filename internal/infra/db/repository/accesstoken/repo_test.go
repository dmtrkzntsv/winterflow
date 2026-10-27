package accesstoken

import (
	"context"
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

type fixture struct {
	repo *Repo
	d    *db.DB
	ctx  context.Context
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedOrg(t, d, dbtest.ID(1))
	dbtest.SeedMember(t, d, dbtest.ID(10), dbtest.ID(1))
	dbtest.SeedMember(t, d, dbtest.ID(11), dbtest.ID(1))
	return &fixture{repo: r, d: d, ctx: dbtest.Ctx(t), now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

// insert stores a token of kind for user under hash h, id dbtest.ID(n).
func (f *fixture) insert(t *testing.T, n int, user string, kind model.TokenKind, h string, mut func(*model.AccessToken)) model.AccessToken {
	t.Helper()
	tok := model.AccessToken{ID: dbtest.ID(n), UserID: user, Kind: kind, Scope: model.ScopeFull, CreatedAt: f.now}
	if mut != nil {
		mut(&tok)
	}
	if err := f.repo.Insert(f.ctx, tok, h); err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *fixture) get(t *testing.T, h string) model.AccessToken {
	t.Helper()
	tok, err := f.repo.GetByHash(f.ctx, h)
	if err != nil {
		t.Fatalf("GetByHash(%s): %v", h, err)
	}
	return tok
}

func TestInsertGetRoundTrip(t *testing.T) {
	f := newFixture(t)
	user := dbtest.ID(10)
	exp := f.now.Add(time.Hour)
	f.insert(t, 100, user, model.TokenOAuth, "h-full", func(tok *model.AccessToken) {
		tok.Scope, tok.Name, tok.Prefix, tok.UserAgent, tok.GrantID = model.ScopeMCP, "n", "wfp_x", "ua", dbtest.ID(900)
		tok.LastUsedAt, tok.ExpiresAt = f.now, &exp
	})
	got := f.get(t, "h-full")
	if got.ID != dbtest.ID(100) || got.UserID != user || got.Kind != model.TokenOAuth || got.Scope != model.ScopeMCP ||
		got.Name != "n" || got.Prefix != "wfp_x" || got.UserAgent != "ua" || got.GrantID != dbtest.ID(900) ||
		!got.CreatedAt.Equal(f.now) || !got.LastUsedAt.Equal(f.now) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) ||
		got.RevokedAt != nil {
		t.Fatalf("got %+v", got)
	}

	// Optional fields left empty read back empty (stored as NULL).
	f.insert(t, 101, user, model.TokenPAT, "h-bare", nil)
	bare := f.get(t, "h-bare")
	if bare.Name != "" || bare.GrantID != "" || !bare.LastUsedAt.IsZero() || bare.ExpiresAt != nil {
		t.Fatalf("bare = %+v", bare)
	}
	if n := dbtest.QueryString(t, f.d, `SELECT COUNT(*) FROM access_tokens WHERE id = ? AND last_used_at IS NULL AND grant_id IS NULL`, dbtest.ID(101)); n != "1" {
		t.Fatal("empty fields not stored as NULL")
	}

	if _, err := f.repo.GetByHash(f.ctx, "h-none"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown hash err = %v, want ErrNotFound", err)
	}
}

func TestTouchSkipsRevoked(t *testing.T) {
	f := newFixture(t)
	f.insert(t, 100, dbtest.ID(10), model.TokenSession, "h", nil)
	later, exp := f.now.Add(time.Minute), f.now.Add(48*time.Hour)
	ok, err := f.repo.Touch(f.ctx, dbtest.ID(100), later, &exp)
	if err != nil || !ok {
		t.Fatalf("Touch = %v, %v", ok, err)
	}
	if got := f.get(t, "h"); !got.LastUsedAt.Equal(later) || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("after touch: %+v", got)
	}
	if err := f.repo.Revoke(f.ctx, dbtest.ID(100), later); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.repo.Touch(f.ctx, dbtest.ID(100), later.Add(time.Minute), &exp); err != nil || ok {
		t.Fatalf("Touch of a revoked token = %v, %v; want false", ok, err)
	}
	if got := f.get(t, "h"); got.RevokedAt == nil || !got.RevokedAt.Equal(later) || !got.LastUsedAt.Equal(later) {
		t.Fatalf("revoked token changed: %+v", got)
	}
}

func TestRevokeScopes(t *testing.T) {
	f := newFixture(t)
	a, b := dbtest.ID(10), dbtest.ID(11)
	f.insert(t, 100, a, model.TokenPAT, "a-pat", nil)
	f.insert(t, 101, a, model.TokenSession, "a-s1", nil)
	f.insert(t, 102, a, model.TokenSession, "a-s2", nil)
	f.insert(t, 103, b, model.TokenSession, "b-s", nil)
	f.insert(t, 104, a, model.TokenOAuth, "a-o1", func(tok *model.AccessToken) { tok.GrantID = dbtest.ID(900) })
	f.insert(t, 105, a, model.TokenOAuth, "a-o2", func(tok *model.AccessToken) { tok.GrantID = dbtest.ID(901) })
	revoked := func(h string) bool { return f.get(t, h).RevokedAt != nil }

	// One token: only the owner's, only of the kind asked, only once.
	for _, c := range []struct {
		user string
		kind model.TokenKind
	}{{b, model.TokenPAT}, {a, model.TokenSession}} {
		if ok, err := f.repo.RevokeUserToken(f.ctx, c.user, c.kind, dbtest.ID(100), f.now); err != nil || ok {
			t.Fatalf("RevokeUserToken(%s, %s) = %v, %v; want false", c.user, c.kind, ok, err)
		}
	}
	if ok, err := f.repo.RevokeUserToken(f.ctx, a, model.TokenPAT, dbtest.ID(100), f.now); err != nil || !ok {
		t.Fatalf("RevokeUserToken = %v, %v", ok, err)
	}
	if ok, _ := f.repo.RevokeUserToken(f.ctx, a, model.TokenPAT, dbtest.ID(100), f.now); ok {
		t.Fatal("a revoked token was revoked again")
	}

	if err := f.repo.RevokeAllOfKind(f.ctx, a, model.TokenSession, f.now); err != nil {
		t.Fatal(err)
	}
	if !revoked("a-s1") || !revoked("a-s2") || revoked("b-s") || revoked("a-o1") {
		t.Fatal("RevokeAllOfKind reached past the user's sessions")
	}

	if err := f.repo.RevokeByGrant(f.ctx, dbtest.ID(900), f.now); err != nil {
		t.Fatal(err)
	}
	if !revoked("a-o1") || revoked("a-o2") {
		t.Fatal("RevokeByGrant reached past its grant")
	}
}

func TestListByUser(t *testing.T) {
	f := newFixture(t)
	a := dbtest.ID(10)
	f.insert(t, 101, a, model.TokenPAT, "p2", func(tok *model.AccessToken) { tok.CreatedAt = f.now.Add(time.Minute) })
	f.insert(t, 100, a, model.TokenPAT, "p1", nil)
	f.insert(t, 102, a, model.TokenSession, "s", nil)
	f.insert(t, 103, dbtest.ID(11), model.TokenPAT, "other", nil)
	if err := f.repo.Revoke(f.ctx, dbtest.ID(101), f.now); err != nil {
		t.Fatal(err)
	}
	list, err := f.repo.ListByUser(f.ctx, a, model.TokenPAT)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != dbtest.ID(100) || list[1].ID != dbtest.ID(101) || list[1].RevokedAt == nil {
		t.Fatalf("list = %+v, want both PATs oldest first, the revoked one included", list)
	}
}

func TestDeleteDead(t *testing.T) {
	f := newFixture(t)
	a := dbtest.ID(10)
	old, recent := f.now.Add(-48*time.Hour), f.now.Add(-time.Hour)
	f.insert(t, 100, a, model.TokenSession, "expired-old", func(tok *model.AccessToken) { tok.ExpiresAt = &old })
	f.insert(t, 101, a, model.TokenSession, "expired-recent", func(tok *model.AccessToken) { tok.ExpiresAt = &recent })
	f.insert(t, 102, a, model.TokenPAT, "revoked-old", nil)
	f.insert(t, 103, a, model.TokenPAT, "revoked-recent", nil)
	f.insert(t, 104, a, model.TokenPAT, "live", nil)
	if err := f.repo.Revoke(f.ctx, dbtest.ID(102), old); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Revoke(f.ctx, dbtest.ID(103), recent); err != nil {
		t.Fatal(err)
	}
	n, err := f.repo.DeleteDead(f.ctx, f.now.Add(-24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("DeleteDead = %d, %v; want 2", n, err)
	}
	for _, h := range []string{"expired-recent", "revoked-recent", "live"} {
		f.get(t, h)
	}
}
