package authn_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/authn"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/accesstoken"
	userrepo "winterflow/internal/infra/db/repository/user"
	"winterflow/pkg/pat"
)

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// grants records RevokeUserGrants calls.
type grants struct{ users []string }

func (g *grants) RevokeUserGrants(_ context.Context, userID string) error {
	g.users = append(g.users, userID)
	return nil
}

type fixture struct {
	uc    *authn.UseCase
	d     *db.DB
	clock *clock
	users *userrepo.Repo
	ctx   context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := accesstoken.New(d.Tx, d.Driver)
	if err != nil {
		t.Fatal(err)
	}
	users, err := userrepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	return &fixture{uc: authn.New(repo, c.Now), d: d, clock: c, users: users, ctx: dbtest.Ctx(t)}
}

func (f *fixture) user(t *testing.T, email string) model.User {
	t.Helper()
	u, err := f.users.RegisterLocalUser(f.ctx, "U", email, "password-123")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) session(t *testing.T, userID string) string {
	t.Helper()
	raw, err := f.uc.IssueSession(f.ctx, userID, "Firefox")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *fixture) pat(t *testing.T, userID string, expiresAt *time.Time) (model.AccessToken, string) {
	t.Helper()
	tok, raw, err := f.uc.CreatePAT(f.ctx, userID, "ci", expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return tok, raw
}

func (f *fixture) mustAuth(t *testing.T, raw string) model.Principal {
	t.Helper()
	p, err := f.uc.Authenticate(f.ctx, raw)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return p
}

func (f *fixture) mustNotAuth(t *testing.T, raw, why string) {
	t.Helper()
	if _, err := f.uc.Authenticate(f.ctx, raw); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("%s: Authenticate err = %v, want ErrUnauthorized", why, err)
	}
}

func TestAuthenticateSession(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	raw := f.session(t, u.ID)
	if !strings.HasPrefix(raw, "wfs_") || len(raw) < len("wfs_")+43 {
		t.Fatalf("session token %q: want wfs_ and 32 random bytes", raw)
	}
	p := f.mustAuth(t, raw)
	if p.UserID != u.ID || p.Kind != model.TokenSession || p.Scope != model.ScopeFull || p.TokenID == "" {
		t.Fatalf("principal = %+v", p)
	}
	// Only the hash is stored.
	stored := dbtest.QueryString(t, f.d, `SELECT token_hash FROM access_tokens WHERE id = ?`, p.TokenID)
	if stored != pat.Hash(raw) {
		t.Fatalf("stored %q, want the SHA-256 of the token", stored)
	}
	f.mustNotAuth(t, "wfs_unknown", "unknown token")
	f.mustNotAuth(t, "", "empty token")
}

func TestAuthenticateRevoked(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	one, two := f.session(t, u.ID), f.session(t, u.ID)
	_, patRaw := f.pat(t, u.ID, nil)

	if err := f.uc.RevokeToken(f.ctx, f.mustAuth(t, one).TokenID); err != nil {
		t.Fatal(err)
	}
	f.mustNotAuth(t, one, "revoked session")
	f.mustAuth(t, two)

	if err := f.uc.RevokeSessions(f.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	f.mustNotAuth(t, two, "session after RevokeSessions")
	f.mustAuth(t, patRaw) // sign-out everywhere leaves PATs alone
}

func TestAuthenticateExpired(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	sess := f.session(t, u.ID)
	exp := f.clock.Now().Add(time.Hour)
	_, patRaw := f.pat(t, u.ID, &exp)
	oauth, err := f.uc.IssueOAuthAccess(f.ctx, u.ID, dbtest.ID(900), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	f.clock.Advance(11 * time.Minute)
	f.mustNotAuth(t, oauth, "oauth token past its ttl")
	f.mustAuth(t, patRaw)

	f.clock.Advance(time.Hour)
	f.mustNotAuth(t, patRaw, "PAT past its expiry")

	f.clock.Advance(authn.SessionTTL)
	f.mustNotAuth(t, sess, "session unused for 30 days")
}

func TestSessionSlides(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	start := f.clock.Now()
	sess := f.session(t, u.ID)

	// Used every 20 days, a session outlives its 30-day window...
	for f.clock.Now().Sub(start) < authn.SessionMaxAge-20*24*time.Hour {
		f.clock.Advance(20 * 24 * time.Hour)
		f.mustAuth(t, sess)
	}
	// ...but never its 90-day cap.
	f.clock.Advance(authn.SessionMaxAge - f.clock.Now().Sub(start))
	f.mustNotAuth(t, sess, "session at its 90-day cap")

	// Unused, it ends 30 days after its last use.
	other := f.session(t, u.ID)
	f.clock.Advance(10 * 24 * time.Hour)
	f.mustAuth(t, other)
	f.clock.Advance(authn.SessionTTL - time.Minute)
	f.mustAuth(t, other)
	f.clock.Advance(authn.SessionTTL + time.Minute)
	f.mustNotAuth(t, other, "session 30 days after its last use")
}

func TestLastUsedWrittenAtMostOnceAMinute(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	tok, raw := f.pat(t, u.ID, nil)
	lastUsed := func() time.Time {
		t.Helper()
		list, err := f.uc.ListPATs(f.ctx, u.ID)
		if err != nil || len(list) != 1 {
			t.Fatalf("ListPATs = %v, %v", list, err)
		}
		return list[0].LastUsedAt
	}
	if !lastUsed().IsZero() || !tok.LastUsedAt.IsZero() {
		t.Fatal("a new PAT reads as used")
	}
	first := f.clock.Now()
	f.mustAuth(t, raw)
	f.clock.Advance(30 * time.Second)
	f.mustAuth(t, raw)
	if got := lastUsed(); !got.Equal(first) {
		t.Fatalf("last used = %v, want %v (no second write within a minute)", got, first)
	}
	f.clock.Advance(31 * time.Second)
	f.mustAuth(t, raw)
	if got := lastUsed(); !got.Equal(f.clock.Now()) {
		t.Fatalf("last used = %v, want %v", got, f.clock.Now())
	}
}

func TestPATRoundTrip(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	other := f.user(t, "b@x.io")
	exp := f.clock.Now().Add(24 * time.Hour)
	tok, raw := f.pat(t, u.ID, &exp)
	if !strings.HasPrefix(raw, "wfp_") || len(raw) < len("wfp_")+43 {
		t.Fatalf("PAT %q: want wfp_ and 32 random bytes", raw)
	}
	if tok.Name != "ci" || tok.Prefix != raw[:pat.PrefixLen] || tok.Kind != model.TokenPAT ||
		tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(exp) || tok.ID == "" {
		t.Fatalf("token = %+v", tok)
	}
	p := f.mustAuth(t, raw)
	if p.UserID != u.ID || p.Kind != model.TokenPAT || p.Scope != model.ScopeFull || p.TokenID != tok.ID {
		t.Fatalf("principal = %+v", p)
	}

	f.clock.Advance(time.Second)
	newer, _ := f.pat(t, u.ID, nil)
	list, err := f.uc.ListPATs(f.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != tok.ID {
		t.Fatalf("list = %+v, want newest first", list)
	}

	for _, id := range []string{"not-a-uuid", dbtest.ID(404)} {
		if err := f.uc.DeletePAT(f.ctx, u.ID, id); !errors.Is(err, model.ErrTokenNotFound) {
			t.Fatalf("DeletePAT(%q) err = %v, want ErrTokenNotFound", id, err)
		}
	}
	if err := f.uc.DeletePAT(f.ctx, other.ID, tok.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Fatalf("another user's DeletePAT err = %v, want ErrTokenNotFound", err)
	}
	sess := f.mustAuth(t, f.session(t, u.ID))
	if err := f.uc.DeletePAT(f.ctx, u.ID, sess.TokenID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Fatalf("DeletePAT of a session err = %v, want ErrTokenNotFound", err)
	}
	if err := f.uc.DeletePAT(f.ctx, u.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	f.mustNotAuth(t, raw, "deleted PAT")
	if err := f.uc.DeletePAT(f.ctx, u.ID, tok.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Fatalf("second DeletePAT err = %v, want ErrTokenNotFound", err)
	}
	if list, _ := f.uc.ListPATs(f.ctx, u.ID); len(list) != 1 || list[0].ID != newer.ID {
		t.Fatalf("list after delete = %+v", list)
	}
}

func TestMigratedPATStillAuthenticates(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	// A token from before the move: 40 base62 characters, as user_tokens held
	// it, copied over by migration 0021 with its hash.
	raw := "wfp_" + strings.Repeat("Ab3", 13) + "Z"
	dbtest.Exec(t, f.d, `INSERT INTO access_tokens (id, user_id, kind, token_hash, scope, name, token_prefix, created_at)
		VALUES (?, ?, 'pat', ?, 'full', 'old', ?, ?)`, dbtest.ID(77), u.ID, pat.Hash(raw), raw[:pat.PrefixLen], "2026-01-01 00:00:00")
	p := f.mustAuth(t, raw)
	if p.UserID != u.ID || p.Kind != model.TokenPAT || p.TokenID != dbtest.ID(77) {
		t.Fatalf("principal = %+v", p)
	}
}

func TestIssueOAuthAccess(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	grant := dbtest.ID(901)
	raw, err := f.uc.IssueOAuthAccess(f.ctx, u.ID, grant, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "wfo_") {
		t.Fatalf("oauth token %q lacks wfo_", raw)
	}
	p := f.mustAuth(t, raw)
	if p.UserID != u.ID || p.Kind != model.TokenOAuth || p.Scope != model.ScopeMCP {
		t.Fatalf("principal = %+v", p)
	}
	// An OAuth token does not slide: used just before its end, it still ends.
	f.clock.Advance(59 * time.Minute)
	f.mustAuth(t, raw)
	f.clock.Advance(2 * time.Minute)
	f.mustNotAuth(t, raw, "oauth token past its hour")
}

func TestRevokeAllForUser(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	bystander := f.user(t, "b@x.io")
	g := &grants{}
	f.uc.SetGrantRevoker(g)

	sess := f.session(t, u.ID)
	_, patRaw := f.pat(t, u.ID, nil)
	oauth, err := f.uc.IssueOAuthAccess(f.ctx, u.ID, dbtest.ID(902), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	keep := f.session(t, bystander.ID)

	if err := f.uc.RevokeAllForUser(f.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	f.mustNotAuth(t, sess, "session")
	f.mustNotAuth(t, patRaw, "PAT")
	f.mustNotAuth(t, oauth, "oauth token")
	f.mustAuth(t, keep)
	if len(g.users) != 1 || g.users[0] != u.ID {
		t.Fatalf("grant revoker calls = %v, want [%s]", g.users, u.ID)
	}

	// Without a grant revoker (no authorization server) it still works.
	f.uc.SetGrantRevoker(nil)
	if err := f.uc.RevokeAllForUser(f.ctx, bystander.ID); err != nil {
		t.Fatal(err)
	}
	f.mustNotAuth(t, keep, "bystander's session after their own RevokeAllForUser")
}

func TestRemoveMemberRevokesTokens(t *testing.T) {
	f := newFixture(t)
	admin, err := f.users.BootstrapLocalAdmin(f.ctx, "Admin", "boss@x.io", "bootpass-123")
	if err != nil {
		t.Fatal(err)
	}
	orgID, err := f.users.PrimaryOrganizationID(f.ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.users.CreateMemberUser(f.ctx, orgID, "M", "m@x.io", "temp-password1")
	if err != nil {
		t.Fatal(err)
	}
	sess := f.session(t, m.ID)
	_, patRaw := f.pat(t, m.ID, nil)
	adminSess := f.session(t, admin.ID)

	if err := authn.RemoveMember(f.ctx, f.users, f.uc, orgID, admin.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	f.mustNotAuth(t, sess, "removed member's session")
	f.mustNotAuth(t, patRaw, "removed member's PAT")
	f.mustAuth(t, adminSess)

	// A refused removal (not a member here) revokes nothing.
	outsider := f.user(t, "out@x.io")
	outSess := f.session(t, outsider.ID)
	if err := authn.RemoveMember(f.ctx, f.users, f.uc, orgID, admin.ID, outsider.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("removing a non-member err = %v, want ErrorUserNotFound", err)
	}
	f.mustAuth(t, outSess)
}

func TestPrune(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "a@x.io")
	revoked := f.session(t, u.ID)
	if err := f.uc.RevokeToken(f.ctx, f.mustAuth(t, revoked).TokenID); err != nil {
		t.Fatal(err)
	}
	exp := f.clock.Now().Add(time.Hour)
	_, expiring := f.pat(t, u.ID, &exp)
	_, live := f.pat(t, u.ID, nil)
	count := func() string {
		return dbtest.QueryString(t, f.d, `SELECT COUNT(*) FROM access_tokens WHERE user_id = ?`, u.ID)
	}

	f.clock.Advance(29 * 24 * time.Hour)
	if err := f.uc.Prune(f.ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != "3" {
		t.Fatalf("rows after an early prune = %s, want 3", n)
	}
	f.clock.Advance(2 * 24 * time.Hour)
	if err := f.uc.Prune(f.ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != "1" {
		t.Fatalf("rows after prune = %s, want 1 (the live PAT)", n)
	}
	f.mustAuth(t, live)
	f.mustNotAuth(t, expiring, "pruned PAT")
}
