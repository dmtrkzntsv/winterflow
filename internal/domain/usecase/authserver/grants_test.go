package authserver

import (
	"errors"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

func TestConnectedApps(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	tr, err := f.exchange(cid, code)
	if err != nil {
		t.Fatal(err)
	}
	list := f.connected(t)
	if len(list) != 1 || list[0].ClientName != "Claude" || list[0].RedirectHost != "claude.ai" || list[0].GrantID == "" {
		t.Fatalf("%+v", list)
	}
	if err := f.s.RevokeConnectedApp(f.ctx, dbtest.ID(11), list[0].GrantID); !errors.Is(err, model.ErrGrantNotFound) {
		t.Fatalf("a foreign grant must be not found: %v", err)
	}
	if !f.authenticates(tr.AccessToken) {
		t.Fatal("the access token must authenticate before the revoke")
	}
	if err := f.s.RevokeConnectedApp(f.ctx, f.user, list[0].GrantID); err != nil {
		t.Fatal(err)
	}
	if f.authenticates(tr.AccessToken) {
		t.Fatal("revoking a connected app must cut its access token at once")
	}
	if l := f.connected(t); len(l) != 0 {
		t.Fatal("still listed")
	}
	if err := f.s.RevokeConnectedApp(f.ctx, f.user, list[0].GrantID); !errors.Is(err, model.ErrGrantNotFound) {
		t.Fatalf("an already revoked grant must be not found: %v", err)
	}
	_, err = f.refresh(cid, tr.RefreshToken)
	wantOAuth(t, err, "invalid_grant")
}

func TestRevokeUserGrants(t *testing.T) {
	f := newFixture(t)
	cid, code := f.approve(t)
	tr, err := f.exchange(cid, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.RevokeUserGrants(f.ctx, f.user); err != nil {
		t.Fatal(err)
	}
	if l := f.connected(t); len(l) != 0 {
		t.Fatal("grants must be revoked")
	}
	_, err = f.refresh(cid, tr.RefreshToken)
	wantOAuth(t, err, "invalid_grant")
}
