package authserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/backend"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/pkg/util"
)

func newRepo(t *testing.T) (*Repo, context.Context) {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedOrg(t, d, dbtest.ID(1))
	dbtest.SeedMember(t, d, dbtest.ID(10), dbtest.ID(1))
	dbtest.SeedMember(t, d, dbtest.ID(11), dbtest.ID(1))
	return r, dbtest.Ctx(t)
}

func TestRepo_ClientCodeGrantLifecycle(t *testing.T) {
	r, ctx := newRepo(t)
	userID := dbtest.ID(10)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

	c := model.OAuthClient{ID: util.GenerateID(), Name: "Claude", RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}, CreatedAt: now}
	if err := r.InsertClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetClient(ctx, c.ID)
	if err != nil || got.Name != "Claude" || len(got.RedirectURIs) != 1 || got.LastUsedAt != nil {
		t.Fatalf("GetClient = %+v, %v", got, err)
	}
	if _, err := r.GetClient(ctx, util.GenerateID()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing client: want NotFound, got %v", err)
	}

	code := model.OAuthCode{CodeHash: "h1", ClientID: c.ID, UserID: userID, RedirectURI: c.RedirectURIs[0],
		CodeChallenge: "ch", Resource: "https://x.test/mcp", CreatedAt: now, ExpiresAt: now.Add(2 * time.Minute)}
	if err := r.InsertCode(ctx, code); err != nil {
		t.Fatal(err)
	}
	if got, err := r.ConsumeCode(ctx, "h1"); err != nil || got.UserID != userID {
		t.Fatalf("consume = %+v %v", got, err)
	}
	if _, err := r.ConsumeCode(ctx, "h1"); err == nil {
		t.Fatal("second consume must be NotFound")
	}

	g := model.OAuthGrant{ID: util.GenerateID(), UserID: userID, ClientID: c.ID, RefreshTokenHash: "r1", CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(90 * 24 * time.Hour)}
	if err := r.InsertGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	if n, err := r.RotateGrant(ctx, g.ID, "r1", "r2", now, now.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("rotate = %d %v", n, err)
	}
	if n, _ := r.RotateGrant(ctx, g.ID, "r1", "r3", now, now.Add(time.Hour)); n != 0 {
		t.Fatal("stale rotate must match nothing")
	}
	if _, err := r.GetGrantBySpentRefreshHash(ctx, "r1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("an unrecorded hash is not spent: %v", err)
	}
	if err := r.InsertSpentRefreshHash(ctx, g.ID, "r1", now); err != nil {
		t.Fatal(err)
	}
	if err := r.InsertSpentRefreshHash(ctx, g.ID, "r0", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	bySpent, err := r.GetGrantBySpentRefreshHash(ctx, "r1")
	if err != nil || bySpent.ID != g.ID || bySpent.RotatedAt == nil || bySpent.PrevRefreshTokenHash == nil || *bySpent.PrevRefreshTokenHash != "r1" {
		t.Fatalf("by spent = %+v %v", bySpent, err)
	}
	if old, err := r.GetGrantBySpentRefreshHash(ctx, "r0"); err != nil || old.ID != g.ID {
		t.Fatalf("older spent hash = %+v %v", old, err)
	}
	byCur, err := r.GetGrantByRefreshHash(ctx, "r2")
	if err != nil || byCur.ID != g.ID {
		t.Fatalf("by refresh = %+v %v", byCur, err)
	}
	byID, err := r.GetGrant(ctx, g.ID)
	if err != nil || byID.RefreshTokenHash != "r2" || byID.PrevRefreshTokenHash == nil || *byID.PrevRefreshTokenHash != "r1" {
		t.Fatalf("by id = %+v %v", byID, err)
	}
	if _, err := r.GetGrant(ctx, util.GenerateID()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing grant: want NotFound, got %v", err)
	}
	list, err := r.ListUnrevokedGrants(ctx, userID)
	if err != nil || len(list) != 1 || list[0].ClientName != "Claude" || len(list[0].RedirectURIs) != 1 {
		t.Fatalf("list = %+v %v", list, err)
	}
	if n, _ := r.RevokeUserGrants(ctx, userID, now); n != 1 {
		t.Fatal("revoke user grants")
	}
	if n, _ := r.RevokeGrant(ctx, g.ID, now); n != 0 {
		t.Fatal("revoking an already revoked grant must match nothing")
	}
	if list, _ := r.ListUnrevokedGrants(ctx, userID); len(list) != 0 {
		t.Fatal("revoked grant still listed")
	}
	if n, err := r.DeleteDeadGrants(ctx, now.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("delete dead = %d %v", n, err)
	}
	if _, err := r.GetGrantBySpentRefreshHash(ctx, "r1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("spent hashes must go with their grant: %v", err)
	}

	stale := model.OAuthClient{ID: util.GenerateID(), Name: "x", RedirectURIs: []string{"https://a.test/cb"}, CreatedAt: now.Add(-48 * time.Hour)}
	if err := r.InsertClient(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.PurgeUnusedClients(ctx, now.Add(-24*time.Hour)); n != 1 {
		t.Fatalf("purge = %d", n)
	}
	if err := r.MarkClientUsed(ctx, c.ID, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetClient(ctx, c.ID); got.LastUsedAt == nil || !got.LastUsedAt.Equal(now) {
		t.Fatalf("MarkClientUsed not persisted: %+v", got)
	}

	if err := r.InsertCode(ctx, model.OAuthCode{CodeHash: "old", ClientID: c.ID, UserID: userID, RedirectURI: "x",
		CodeChallenge: "c", Resource: "r", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := r.PurgeExpiredCodes(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ConsumeCode(ctx, "old"); err == nil {
		t.Fatal("expired code must have been purged")
	}
}

func TestRepo_RevokeOtherGrants(t *testing.T) {
	r, ctx := newRepo(t)
	userID, otherUser := dbtest.ID(10), dbtest.ID(11)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

	newClient := func() string {
		c := model.OAuthClient{ID: util.GenerateID(), Name: "c", RedirectURIs: []string{"https://a.test/cb"}, CreatedAt: now}
		if err := r.InsertClient(ctx, c); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	newGrant := func(user, client string) string {
		g := model.OAuthGrant{ID: util.GenerateID(), UserID: user, ClientID: client, RefreshTokenHash: util.GenerateID(),
			CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := r.InsertGrant(ctx, g); err != nil {
			t.Fatal(err)
		}
		return g.ID
	}
	client, otherClient := newClient(), newClient()
	old1, old2 := newGrant(userID, client), newGrant(userID, client)
	keep := newGrant(userID, client)
	sibling := newGrant(userID, otherClient)
	foreign := newGrant(otherUser, client)

	ids, err := r.RevokeOtherGrants(ctx, userID, client, keep, now)
	if err != nil || len(ids) != 2 {
		t.Fatalf("revoked = %v %v", ids, err)
	}
	got := map[string]bool{ids[0]: true, ids[1]: true}
	if !got[old1] || !got[old2] {
		t.Fatalf("revoked the wrong grants: %v", ids)
	}
	for _, id := range []string{keep, sibling, foreign} {
		if g, _ := r.GetGrant(ctx, id); g.RevokedAt != nil {
			t.Fatalf("grant %s must stay live", id)
		}
	}
	if ids, err := r.RevokeOtherGrants(ctx, userID, client, keep, now); err != nil || len(ids) != 0 {
		t.Fatalf("already revoked grants must not be returned again: %v %v", ids, err)
	}
}

func TestRepo_LockUserGrantsUnknownUser(t *testing.T) {
	r, ctx := newRepo(t)
	if err := r.LockUserGrants(ctx, dbtest.ID(10)); err != nil {
		t.Fatalf("lock = %v", err)
	}
	if err := r.LockUserGrants(ctx, dbtest.ID(99)); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown user: want NotFound, got %v", err)
	}
}

// TestRepo_LockUserGrantsSerializesExchanges replays two code exchanges for
// one user and client on two Postgres connections (READ COMMITTED). Without
// the lock the second exchange's RevokeOtherGrants cannot see the first's
// uncommitted grant and both stay live; with it the second waits, then
// replaces the first. SQLite has one connection, so its exchanges are
// serialized already.
func TestRepo_LockUserGrantsSerializesExchanges(t *testing.T) {
	d := dbtest.Open(t)
	if d.Driver != backend.Postgres {
		t.Skip("Postgres only: SQLite runs one transaction at a time")
	}
	r, err := New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	dbtest.SeedOrg(t, d, dbtest.ID(1))
	dbtest.SeedMember(t, d, dbtest.ID(10), dbtest.ID(1))
	ctx := dbtest.Ctx(t)
	userID := dbtest.ID(10)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	c := model.OAuthClient{ID: util.GenerateID(), Name: "Claude", RedirectURIs: []string{"https://claude.ai/cb"}, CreatedAt: now}
	if err := r.InsertClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	grant := func(hash string) model.OAuthGrant {
		return model.OAuthGrant{ID: util.GenerateID(), UserID: userID, ClientID: c.ID, RefreshTokenHash: hash,
			CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(time.Hour)}
	}
	exchange := func(ctx context.Context, g model.OAuthGrant, held func()) ([]string, error) {
		var revoked []string
		err := d.Tx.WithTx(ctx, func(ctx context.Context) error {
			if err := r.LockUserGrants(ctx, userID); err != nil {
				return err
			}
			if err := r.InsertGrant(ctx, g); err != nil {
				return err
			}
			held()
			var err error
			revoked, err = r.RevokeOtherGrants(ctx, userID, c.ID, g.ID, now)
			return err
		})
		return revoked, err
	}

	gA, gB := grant("ra"), grant("rb")
	aHolds, releaseA := make(chan struct{}), make(chan struct{})
	aDone := make(chan error, 1)
	go func() {
		_, err := exchange(ctx, gA, func() { close(aHolds); <-releaseA })
		aDone <- err
	}()
	<-aHolds

	var bInside atomic.Bool
	type result struct {
		revoked []string
		err     error
	}
	bDone := make(chan result, 1)
	go func() {
		revoked, err := exchange(ctx, gB, func() { bInside.Store(true) })
		bDone <- result{revoked, err}
	}()
	time.Sleep(300 * time.Millisecond)
	if bInside.Load() {
		close(releaseA)
		t.Fatal("the second exchange inserted its grant while the first held the lock")
	}
	close(releaseA)
	if err := <-aDone; err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	b := <-bDone
	if b.err != nil || len(b.revoked) != 1 || b.revoked[0] != gA.ID {
		t.Fatalf("second exchange revoked %v (%v), want [%s]", b.revoked, b.err, gA.ID)
	}
	live, err := r.ListUnrevokedGrants(ctx, userID)
	if err != nil || len(live) != 1 || live[0].Grant.ID != gB.ID {
		t.Fatalf("live grants = %+v, %v; want only %s", live, err, gB.ID)
	}
}
