package oauthlogin

import (
	"context"
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/types"
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

func (f *fixture) link(t *testing.T, user, provider, subject, email string) error {
	t.Helper()
	return f.repo.LinkIdentity(f.ctx, model.Identity{Provider: provider, Subject: subject, UserID: user, Email: email, CreatedAt: f.now})
}

func (f *fixture) setPassword(t *testing.T, user, email string) {
	t.Helper()
	dbtest.Exec(t, f.d, `INSERT INTO user_credentials (user_id, email, password_hash, must_change_password, updated_at) VALUES (?, ?, 'x', ?, ?)`,
		user, email, false, types.DateTime(f.now))
	dbtest.Exec(t, f.d, `INSERT INTO user_connected_accounts (provider, external_id, user_id) VALUES ('local', ?, ?)`, email, user)
}

func TestLinkAndList(t *testing.T) {
	f := newFixture(t)
	a, b := dbtest.ID(10), dbtest.ID(11)
	if err := f.link(t, a, "oidc", "sub-a", "a@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := f.link(t, b, "oidc", "sub-a", "b@example.test"); !errors.Is(err, model.ErrIdentityTaken) {
		t.Fatalf("linking a's account to b = %v, want ErrIdentityTaken", err)
	}
	if err := f.link(t, a, "oidc", "sub-other", "a@example.test"); !errors.Is(err, model.ErrProviderAlreadyLinked) {
		t.Fatalf("a second oidc account = %v, want ErrProviderAlreadyLinked", err)
	}
	// Re-linking one the user has refreshes its email.
	if err := f.link(t, a, "oidc", "sub-a", "new@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := f.link(t, a, "google", "g-a", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.link(t, dbtest.ID(99), "apple", "x", ""); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("link for a missing user = %v", err)
	}
	f.setPassword(t, a, "a@example.test")

	ids, err := f.repo.Identities(f.ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("identities = %+v, want google and oidc (no local row)", ids)
	}
	got, err := f.repo.IdentityBySubject(f.ctx, "oidc", "sub-a")
	if err != nil || got.UserID != a || got.Email != "new@example.test" || !got.CreatedAt.Equal(f.now) {
		t.Fatalf("IdentityBySubject = %+v, %v", got, err)
	}
	if _, err := f.repo.IdentityBySubject(f.ctx, "oidc", "nope"); !errors.Is(err, model.ErrIdentityNotFound) {
		t.Fatalf("unknown subject = %v", err)
	}
	if _, err := f.repo.IdentityBySubject(f.ctx, "local", "a@example.test"); !errors.Is(err, model.ErrIdentityNotFound) {
		t.Fatalf("the local row is not an identity, got %v", err)
	}
	if err := f.repo.UpdateIdentityEmail(f.ctx, "oidc", "sub-a", "z@example.test"); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.repo.IdentityBySubject(f.ctx, "oidc", "sub-a"); got.Email != "z@example.test" {
		t.Fatalf("email after update = %q", got.Email)
	}
}

func TestUnlink(t *testing.T) {
	f := newFixture(t)
	a, b := dbtest.ID(10), dbtest.ID(11)
	for _, p := range []string{"oidc", "google"} {
		if err := f.link(t, a, p, "s-"+p, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.repo.UnlinkIdentity(f.ctx, a, "apple"); !errors.Is(err, model.ErrIdentityNotFound) {
		t.Fatalf("unlink of an unlinked provider = %v", err)
	}
	if err := f.repo.UnlinkIdentity(f.ctx, a, "oidc"); err != nil {
		t.Fatalf("unlink with another provider left = %v", err)
	}
	if err := f.repo.UnlinkIdentity(f.ctx, a, "google"); !errors.Is(err, model.ErrLastSignInMethod) {
		t.Fatalf("unlink of the last method = %v, want ErrLastSignInMethod", err)
	}
	// With a password the last provider can go; the password's own row
	// cannot be unlinked.
	if err := f.link(t, b, "oidc", "s-b", ""); err != nil {
		t.Fatal(err)
	}
	f.setPassword(t, b, "b@example.test")
	if err := f.repo.UnlinkIdentity(f.ctx, b, "local"); !errors.Is(err, model.ErrIdentityNotFound) {
		t.Fatalf("unlink local = %v", err)
	}
	if err := f.repo.UnlinkIdentity(f.ctx, b, "oidc"); err != nil {
		t.Fatalf("unlink with a password = %v", err)
	}
	if err := f.repo.UnlinkIdentity(f.ctx, dbtest.ID(99), "oidc"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("unlink for a missing user = %v", err)
	}
}

func TestEmailInUseAndRekey(t *testing.T) {
	f := newFixture(t)
	a := dbtest.ID(10)
	f.setPassword(t, a, "pw@example.test")
	if err := f.link(t, a, "google", "legacy-hash", "g@example.test"); err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]bool{"pw@example.test": true, "g@example.test": true, "free@example.test": false} {
		if got, err := f.repo.EmailInUse(f.ctx, email); err != nil || got != want {
			t.Errorf("EmailInUse(%s) = %v, %v; want %v", email, got, err, want)
		}
	}
	if ok, err := f.repo.RekeyIdentity(f.ctx, "google", "legacy-hash", "sub"); err != nil || !ok {
		t.Fatalf("rekey = %v, %v", ok, err)
	}
	if ok, err := f.repo.RekeyIdentity(f.ctx, "google", "legacy-hash", "sub"); err != nil || ok {
		t.Fatalf("second rekey = %v, %v; want nothing to move", ok, err)
	}
	if got, err := f.repo.IdentityBySubject(f.ctx, "google", "sub"); err != nil || got.UserID != a {
		t.Fatalf("rekeyed identity = %+v, %v", got, err)
	}
}

func TestLoginStates(t *testing.T) {
	f := newFixture(t)
	st := model.LoginState{StateHash: "h1", Provider: "oidc", Nonce: "n", CodeVerifier: "v", FlowHash: "fh",
		Intent: model.IntentLink, ReturnTo: "/settings", LinkUserID: dbtest.ID(10), CreatedAt: f.now, ExpiresAt: f.now.Add(10 * time.Minute)}
	if err := f.repo.InsertLoginState(f.ctx, st); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.ConsumeLoginState(f.ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if got.StateHash != st.StateHash || got.Nonce != "n" || got.CodeVerifier != "v" || got.FlowHash != "fh" || got.Intent != st.Intent ||
		got.ReturnTo != "/settings" || got.LinkUserID != st.LinkUserID || !got.ExpiresAt.Equal(st.ExpiresAt) || !got.CreatedAt.Equal(f.now) {
		t.Fatalf("consumed %+v, want %+v", got, st)
	}
	if _, err := f.repo.ConsumeLoginState(f.ctx, "h1"); !errors.Is(err, model.ErrLoginStateNotFound) {
		t.Fatalf("second consume = %v, want ErrLoginStateNotFound", err)
	}

	old := model.LoginState{StateHash: "old", Provider: "oidc", Nonce: "n", Intent: model.IntentLogin, CreatedAt: f.now, ExpiresAt: f.now}
	fresh := old
	fresh.StateHash, fresh.ExpiresAt = "fresh", f.now.Add(time.Hour)
	for _, s := range []model.LoginState{old, fresh} {
		if err := f.repo.InsertLoginState(f.ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.repo.DeleteExpiredLoginStates(f.ctx, f.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.ConsumeLoginState(f.ctx, "old"); !errors.Is(err, model.ErrLoginStateNotFound) {
		t.Fatalf("expired state survived the sweep: %v", err)
	}
	if got, err := f.repo.ConsumeLoginState(f.ctx, "fresh"); err != nil || got.LinkUserID != "" {
		t.Fatalf("fresh state = %+v, %v", got, err)
	}
}
