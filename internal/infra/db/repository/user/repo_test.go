package user

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/pkg/pat"
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func mustCreateUser(t *testing.T, r *Repo, d dto.UserDTO) model.User {
	t.Helper()
	u, err := r.CreateUser(dbtest.Ctx(t), d)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustBootstrap(t *testing.T, r *Repo, name, email, password string) model.User {
	t.Helper()
	u, err := r.BootstrapLocalAdmin(dbtest.Ctx(t), name, email, password)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustPrimaryOrg(t *testing.T, r *Repo, userID string) string {
	t.Helper()
	id, err := r.PrimaryOrganizationID(dbtest.Ctx(t), userID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustMember(t *testing.T, r *Repo, orgID, name, email, password string) model.User {
	t.Helper()
	u, err := r.CreateMemberUser(dbtest.Ctx(t), orgID, name, email, password)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustRegister(t *testing.T, r *Repo, name, email, password string) model.User {
	t.Helper()
	u, err := r.RegisterLocalUser(dbtest.Ctx(t), name, email, password)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustToken(t *testing.T, r *Repo, userID, name string, expiresAt *time.Time) (model.UserToken, string) {
	t.Helper()
	rec, plaintext, err := r.CreateToken(dbtest.Ctx(t), userID, name, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return rec, plaintext
}

func mustMembers(t *testing.T, r *Repo, orgID string) []model.Member {
	t.Helper()
	members, err := r.ListMembers(dbtest.Ctx(t), orgID)
	if err != nil {
		t.Fatal(err)
	}
	return members
}

// storedRole reads organization_users.role straight from the table: the app
// no longer reads it, but it is still written (see model.OrganizationRole).
func storedRole(t *testing.T, d *db.DB, userID string) string {
	t.Helper()
	return dbtest.QueryString(t, d, `SELECT role FROM organization_users WHERE user_id = ?`, userID)
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestCreateUserCreatesOrgMembershipAndAccount(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)

	u := mustCreateUser(t, repo, dto.UserDTO{
		Name:      "Alice",
		AvatarURL: "https://example.com/a.png",
		Provider:  "google",
		AccountID: "goog-123",
	})
	if u.ID == "" || u.Name != "Alice" {
		t.Fatalf("user = %+v", u)
	}

	// Membership: the auto-created org is discoverable.
	if orgID := mustPrimaryOrg(t, repo, u.ID); orgID == "" {
		t.Fatal("empty primary organization id")
	}

	// Connected account resolves back to the same user.
	got, err := repo.GetByConnectedAccount(ctx, "google", "goog-123")
	if err != nil || got.ID != u.ID {
		t.Fatalf("GetByConnectedAccount = %+v, %v", got, err)
	}

	// And a plain lookup works.
	byID, err := repo.GetUser(ctx, u.ID)
	if err != nil || byID.Name != "Alice" || byID.AvatarURL != "https://example.com/a.png" {
		t.Fatalf("GetUser = %+v, %v", byID, err)
	}
}

func TestUserLookupsReturnNotFound(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)

	if _, err := repo.GetByConnectedAccount(ctx, "google", "nope"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetByConnectedAccount err = %v", err)
	}
	if _, err := repo.GetUser(ctx, dbtest.ID(404)); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetUser err = %v", err)
	}
	if _, err := repo.PrimaryOrganizationID(ctx, dbtest.ID(404)); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("PrimaryOrganizationID err = %v", err)
	}
}

func TestCreateTokenAndFindByToken(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	u := mustCreateUser(t, repo, dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g1"})

	rec, plaintext := mustToken(t, repo, u.ID, "ci deploy", nil)
	if rec.Name != "ci deploy" || rec.Prefix == "" || rec.ID == "" {
		t.Fatalf("record = %+v", rec)
	}
	if !strings.HasPrefix(plaintext, "wfp_") || !strings.HasPrefix(plaintext, rec.Prefix) {
		t.Fatalf("plaintext %q / prefix %q mismatch", plaintext, rec.Prefix)
	}

	got, err := repo.FindByToken(ctx, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID {
		t.Errorf("resolved user %q, want %q", got.ID, u.ID)
	}

	// The DB must not contain the plaintext anywhere.
	stored := dbtest.QueryString(t, d, `SELECT token_hash FROM user_tokens WHERE token_id = ?`, rec.ID)
	if stored == plaintext || stored != pat.Hash(plaintext) {
		t.Error("token stored in plaintext or not as its hash")
	}
}

func TestFindByTokenRejectsUnknownAndExpired(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	u := mustCreateUser(t, repo, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g2"})

	if _, err := repo.FindByToken(ctx, "wfp_nope"); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}

	past := time.Now().Add(-time.Hour)
	_, expired := mustToken(t, repo, u.ID, "old", &past)
	if _, err := repo.FindByToken(ctx, expired); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("expired token: err = %v, want ErrInvalidToken", err)
	}
}

func TestFindByTokenStampsLastUsed(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	u := mustCreateUser(t, repo, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g3"})
	rec, plaintext := mustToken(t, repo, u.ID, "t", nil)

	if _, err := repo.FindByToken(ctx, plaintext); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListTokens(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("list = %+v", list)
	}
	if list[0].LastUsedAt == nil {
		t.Error("last_used_at not stamped on use")
	}
}

func TestDeleteTokenScopedToUser(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	owner := mustCreateUser(t, repo, dto.UserDTO{Name: "O", Provider: "google", AccountID: "g4"})
	other := mustCreateUser(t, repo, dto.UserDTO{Name: "X", Provider: "google", AccountID: "g5"})
	rec, plaintext := mustToken(t, repo, owner.ID, "mine", nil)

	if err := repo.DeleteToken(ctx, other.ID, rec.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Errorf("cross-user delete: err = %v, want ErrTokenNotFound", err)
	}
	if err := repo.DeleteToken(ctx, owner.ID, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindByToken(ctx, plaintext); !errors.Is(err, model.ErrInvalidToken) {
		t.Error("token still resolves after delete")
	}
	if err := repo.DeleteToken(ctx, owner.ID, rec.ID); !errors.Is(err, model.ErrTokenNotFound) {
		t.Errorf("double delete: err = %v, want ErrTokenNotFound", err)
	}
}

func TestFindOrCreateUserIsIdempotent(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	in := dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g-1"}

	first, err := repo.FindOrCreateUser(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.FindOrCreateUser(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("second call created a new user: %q vs %q", first.ID, second.ID)
	}
}

func TestBootstrapLocalAdminCreatesOwnerOnce(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)

	if n, err := repo.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v", n, err)
	}

	admin := mustBootstrap(t, repo, "Ada Admin", "  Admin@Example.COM ", "SuperSecret1")
	if admin.Name != "Ada Admin" {
		t.Errorf("name = %q, want submitted 'Ada Admin'", admin.Name)
	}
	if role := storedRole(t, d, admin.ID); role != "owner" {
		t.Errorf("stored role = %q, want owner", role)
	}

	// Email was normalized on write; verify with normalized form.
	got, err := repo.VerifyLocalCredentials(ctx, "admin@example.com", "SuperSecret1")
	if err != nil || got.ID != admin.ID {
		t.Fatalf("verify after bootstrap: %+v, %v", got, err)
	}

	// Second bootstrap must refuse: users exist now.
	if _, err := repo.BootstrapLocalAdmin(ctx, "", "evil@example.com", "x"); !errors.Is(err, model.ErrNotBootstrap) {
		t.Errorf("second bootstrap err = %v, want ErrNotBootstrap", err)
	}
}

func TestBootstrapLocalAdminNameFallsBackToEmailLocalPart(t *testing.T) {
	repo, _ := newRepo(t)
	admin := mustBootstrap(t, repo, "   ", "ops@host.io", "SuperSecret1")
	if admin.Name != "ops" {
		t.Fatalf("name = %q, want the email's local part", admin.Name)
	}
}

func TestVerifyLocalCredentials(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin := mustBootstrap(t, repo, "", "a@b.io", "correct-horse-1")

	if u, err := repo.VerifyLocalCredentials(ctx, "A@B.io", "correct-horse-1"); err != nil || u.ID != admin.ID {
		t.Errorf("valid creds: %+v, %v", u, err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "a@b.io", "wrong"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Errorf("wrong password err = %v", err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "nobody@b.io", "x"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Errorf("unknown email err = %v", err)
	}
}

func TestCreateMemberUserJoinsOrgWithoutNewOrg(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin := mustBootstrap(t, repo, "", "boss@x.io", "bootpass-123")
	orgID := mustPrimaryOrg(t, repo, admin.ID)

	member := mustMember(t, repo, orgID, "Bob", "Bob@X.io", "temp-pass-16char")
	if got := mustPrimaryOrg(t, repo, member.ID); got != orgID {
		t.Errorf("member org = %q; want admin's org %q", got, orgID)
	}
	// Admin, not the column default 'member': an older binary run against
	// this database must not demote accounts created without roles.
	if role := storedRole(t, d, member.ID); role != "admin" {
		t.Errorf("stored role = %q, want admin", role)
	}
	if u, err := repo.VerifyLocalCredentials(ctx, "bob@x.io", "temp-pass-16char"); err != nil || u.ID != member.ID {
		t.Errorf("temp password login: %v", err)
	}
	if creds, err := repo.GetCredentials(ctx, member.ID); err != nil || !creds.MustChangePassword || creds.Email != "bob@x.io" {
		t.Errorf("creds = %+v, %v; want must_change=true", creds, err)
	}

	// Duplicate email refused.
	if _, err := repo.CreateMemberUser(ctx, orgID, "Bob2", "bob@x.io", "zz"); !errors.Is(err, model.ErrEmailTaken) {
		t.Errorf("dup email err = %v, want ErrEmailTaken", err)
	}

	// No second org was created for the member.
	if members := mustMembers(t, repo, orgID); len(members) != 2 {
		t.Fatalf("members = %d, want 2", len(members))
	}
}

func TestSetPasswordClearsMustChange(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin := mustBootstrap(t, repo, "", "boss@y.io", "bootpass-123")
	orgID := mustPrimaryOrg(t, repo, admin.ID)
	m := mustMember(t, repo, orgID, "M", "m@y.io", "old-temp-pass1")

	if err := repo.SetPassword(ctx, m.ID, "brand-new-pass1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@y.io", "old-temp-pass1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Error("old password still valid after SetPassword")
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@y.io", "brand-new-pass1"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
	creds, err := repo.GetCredentials(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if creds.MustChangePassword {
		t.Error("must_change_password not cleared")
	}
}

func TestRemoveMemberAnyMemberOnlyInOrg(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	creator := mustBootstrap(t, repo, "", "boss@z.io", "bootpass-123")
	orgID := mustPrimaryOrg(t, repo, creator.ID)
	m := mustMember(t, repo, orgID, "M", "m@z.io", "temp-password1")
	outsider := mustRegister(t, repo, "Eve", "eve@z.io", "evepass-1234")

	// A user of another org is not a member here: refused, and untouched.
	if err := repo.RemoveMember(ctx, orgID, creator.ID, outsider.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Errorf("remove outsider err = %v, want ErrorUserNotFound", err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "eve@z.io", "evepass-1234"); err != nil {
		t.Errorf("outsider lost their account: %v", err)
	}

	// No owner rule: the org's creator can be removed like anyone else, and
	// the org lives on with the remaining member.
	if err := repo.RemoveMember(ctx, orgID, m.ID, creator.ID); err != nil {
		t.Fatalf("remove creator: %v", err)
	}
	members := mustMembers(t, repo, orgID)
	if len(members) != 1 || members[0].ID != m.ID {
		t.Fatalf("members after removing the creator = %+v", members)
	}
	if got := mustPrimaryOrg(t, repo, m.ID); got != orgID {
		t.Errorf("remaining member's org = %q; want %q", got, orgID)
	}
}

// Two members removing each other concurrently must not empty the org: once
// the caller itself has been removed, its pending removal is refused.
func TestRemoveMemberRefusesRemovedCaller(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	a := mustBootstrap(t, repo, "", "a@r.io", "bootpass-123")
	orgID := mustPrimaryOrg(t, repo, a.ID)
	b := mustMember(t, repo, orgID, "B", "b@r.io", "temp-password1")

	// B removes A first; A's in-flight request to remove B then arrives.
	if err := repo.RemoveMember(ctx, orgID, b.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveMember(ctx, orgID, a.ID, b.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("removal by a removed caller err = %v, want ErrorUserNotFound", err)
	}
	members := mustMembers(t, repo, orgID)
	if len(members) != 1 || members[0].ID != b.ID {
		t.Fatalf("members = %+v; want only B", members)
	}
	// A caller from another org is not a member here either.
	other := mustRegister(t, repo, "Eve", "eve@r.io", "evepass-1234")
	if err := repo.RemoveMember(ctx, orgID, other.ID, b.ID); !errors.Is(err, model.ErrorUserNotFound) {
		t.Errorf("foreign caller err = %v, want ErrorUserNotFound", err)
	}
}

func TestRemoveMemberDeletesUserAndTokens(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	admin := mustBootstrap(t, repo, "", "boss@w.io", "bootpass-123")
	orgID := mustPrimaryOrg(t, repo, admin.ID)
	m := mustMember(t, repo, orgID, "M", "m@w.io", "temp-password1")
	_, patPlain := mustToken(t, repo, m.ID, "m's token", nil)

	if err := repo.RemoveMember(ctx, orgID, admin.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@w.io", "temp-password1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Error("removed member can still log in")
	}
	if _, err := repo.FindByToken(ctx, patPlain); !errors.Is(err, model.ErrInvalidToken) {
		t.Error("removed member's PAT still resolves")
	}
	if members := mustMembers(t, repo, orgID); len(members) != 1 {
		t.Errorf("members after removal = %d, want 1", len(members))
	}
}

func TestRegisterLocalUserCreatesOwnOrg(t *testing.T) {
	repo, d := newRepo(t)
	ctx := dbtest.Ctx(t)

	a := mustRegister(t, repo, "Alice", "Alice@X.io", "alicepass-123")
	b := mustRegister(t, repo, "Bob", "bob@x.io", "bobpass-1234")
	if a.Name != "Alice" {
		t.Errorf("name = %q", a.Name)
	}
	orgA, orgB := mustPrimaryOrg(t, repo, a.ID), mustPrimaryOrg(t, repo, b.ID)
	if orgA == "" || orgA == orgB {
		t.Errorf("orgs not distinct: %q vs %q", orgA, orgB)
	}
	if name := dbtest.QueryString(t, d, `SELECT name FROM organizations WHERE organization_id = ?`, orgA); name != "Alice's org" {
		t.Errorf("org name = %q, want the name as given", name)
	}
	if role := storedRole(t, d, a.ID); role != "owner" {
		t.Errorf("stored role = %q, want owner", role)
	}
	if u, err := repo.VerifyLocalCredentials(ctx, "alice@x.io", "alicepass-123"); err != nil || u.ID != a.ID {
		t.Errorf("login after register: %v", err)
	}
	if _, err := repo.RegisterLocalUser(ctx, "Eve", "ALICE@x.io", "x-password-1"); !errors.Is(err, model.ErrEmailTaken) {
		t.Errorf("dup email err = %v, want ErrEmailTaken", err)
	}
}

func TestGetUpdateOrganization(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	u := mustRegister(t, repo, "Alice", "a@o.io", "alicepass-123")
	orgID := mustPrimaryOrg(t, repo, u.ID)

	org, err := repo.GetOrganization(ctx, orgID)
	if err != nil || org.ID != orgID || org.Name == "" {
		t.Fatalf("GetOrganization = %+v, %v", org, err)
	}
	if err := repo.UpdateOrganization(ctx, orgID, "Homelab", "server", "#3b82f6"); err != nil {
		t.Fatal(err)
	}
	org, err = repo.GetOrganization(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if org.Name != "Homelab" || org.Icon != "server" || org.Color != "#3b82f6" {
		t.Errorf("after update: %+v", org)
	}
	if _, err := repo.GetOrganization(ctx, dbtest.ID(404)); !errors.Is(err, model.ErrorUserNotFound) {
		t.Errorf("unknown org err = %v, want ErrorUserNotFound", err)
	}
}

func TestUserCreatedAtLocationIsUTC(t *testing.T) {
	repo, _ := newRepo(t)
	u := mustRegister(t, repo, "Zed", "z@utc.io", "zedpass-1234")
	got, err := repo.GetUser(dbtest.Ctx(t), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt.Location() != time.UTC || got.LastSeenAt.Location() != time.UTC {
		t.Fatalf("locations = %v / %v, want UTC", got.CreatedAt.Location(), got.LastSeenAt.Location())
	}
	if d := time.Since(got.CreatedAt); d < 0 || d > time.Minute {
		t.Fatalf("created_at %v is not now: the session or format shifted it", got.CreatedAt)
	}
}

func TestSetPasswordUnknownUser(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if err := repo.SetPassword(ctx, dbtest.ID(404), "whatever-123", false); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("err = %v, want ErrorUserNotFound", err)
	}
	if err := repo.UpdateOrganization(ctx, dbtest.ID(404), "x", "y", "#000000"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("UpdateOrganization err = %v", err)
	}
	if _, err := repo.GetCredentials(ctx, dbtest.ID(404)); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetCredentials err = %v", err)
	}
}

// A member without local credentials is listed with the provider of their
// connected account and no email.
func TestListMembersReportsProvider(t *testing.T) {
	repo, d := newRepo(t)
	g := mustCreateUser(t, repo, dto.UserDTO{Name: "Gina", Provider: "google", AccountID: "g-77", AvatarURL: "https://example.com/g.png"})
	orgID := mustPrimaryOrg(t, repo, g.ID)
	if name := dbtest.QueryString(t, d, `SELECT name FROM organizations WHERE organization_id = ?`, orgID); name != "gina's org" {
		t.Fatalf("org name = %q, want the lower-cased user name", name)
	}
	m := mustMember(t, repo, orgID, "Lou", "lou@x.io", "temp-password1")

	members := mustMembers(t, repo, orgID)
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}
	byID := map[string]model.Member{}
	for _, mm := range members {
		byID[mm.ID] = mm
	}
	if gm := byID[g.ID]; gm.Provider != "google" || gm.Email != "" || gm.AvatarURL != "https://example.com/g.png" {
		t.Errorf("google member = %+v", gm)
	}
	if lm := byID[m.ID]; lm.Provider != "local" || lm.Email != "lou@x.io" {
		t.Errorf("local member = %+v", lm)
	}
}

// A member with neither credentials nor a connected account (a row created
// outside the app) still lists, with an empty email and provider: a missing
// lookup row means "none", not a failure.
func TestListMembersWithoutCredentialsOrAccount(t *testing.T) {
	repo, d := newRepo(t)
	org, bare := dbtest.ID(1), dbtest.ID(2)
	dbtest.SeedOrg(t, d, org)
	dbtest.SeedMember(t, d, bare, org)

	members := mustMembers(t, repo, org)
	if len(members) != 1 || members[0].ID != bare || members[0].Email != "" || members[0].Provider != "" {
		t.Fatalf("members = %+v; want the bare member with no email or provider", members)
	}
}

// Postgres: concurrent mutual removals serialize on the caller's row lock,
// so exactly one succeeds and the org keeps a member. (SQLite serializes
// every writer, which the same assertions also hold for.)
func TestRemoveMemberConcurrentMutualRemoval(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	a := mustBootstrap(t, repo, "", "a@c.io", "bootpass-123")
	orgID := mustPrimaryOrg(t, repo, a.ID)
	b := mustMember(t, repo, orgID, "B", "b@c.io", "temp-password1")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, pair := range [][2]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		wg.Add(1)
		go func(i int, caller, target string) {
			defer wg.Done()
			errs[i] = repo.RemoveMember(ctx, orgID, caller, target)
		}(i, pair[0], pair[1])
	}
	wg.Wait()
	// The loser fails with ErrorUserNotFound, or on Postgres possibly with
	// a deadlock error (each side's cascade waits on the other's row lock);
	// either way it changed nothing.
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if members := mustMembers(t, repo, orgID); len(members) != 1 || ok != 1 {
		t.Fatalf("successes = %d (%v), members left = %d; want exactly one of each", ok, errs, len(members))
	}
}

// The account rows go in one transaction: a membership insert that fails
// (unknown org) after the user row went in leaves no user behind.
func TestCreateMemberUserIsAtomic(t *testing.T) {
	repo, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	if _, err := repo.CreateMemberUser(ctx, dbtest.ID(404), "Ghost", "ghost@x.io", "temp-password1"); err == nil {
		t.Fatal("member of an unknown org created")
	}
	if n, err := repo.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("users after the failed create = %d, %v; want 0", n, err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "ghost@x.io", "temp-password1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Fatalf("credentials survived: %v", err)
	}
}
