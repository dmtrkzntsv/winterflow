package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/models"
	"winterflow/pkg/logger"
)

func newUserRepo(t *testing.T) *DbUserRepository {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	return NewDbUserRepository(newTestDB(t), log)
}

func TestCreateUserCreatesOrgMembershipAndAccount(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()

	u, err := repo.CreateUser(ctx, dto.UserDTO{
		Name:      "Alice",
		AvatarURL: "https://example.com/a.png",
		Provider:  "google",
		AccountID: "goog-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.Name != "Alice" {
		t.Fatalf("user = %+v", u)
	}

	// Membership: the auto-created org is discoverable.
	orgID, err := repo.PrimaryOrganizationID(ctx, u.ID)
	if err != nil || orgID == "" {
		t.Fatalf("PrimaryOrganizationID = %q, %v", orgID, err)
	}

	// Connected account resolves back to the same user.
	got, err := repo.GetByConnectedAccount(ctx, "google", "goog-123")
	if err != nil || got.ID != u.ID {
		t.Fatalf("GetByConnectedAccount = %+v, %v", got, err)
	}

	// And a plain lookup works.
	byID, err := repo.GetUser(ctx, u.ID)
	if err != nil || byID.Name != "Alice" {
		t.Fatalf("GetUser = %+v, %v", byID, err)
	}
}

func TestUserLookupsReturnNotFound(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()

	if _, err := repo.GetByConnectedAccount(ctx, "google", "nope"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetByConnectedAccount err = %v", err)
	}
	if _, err := repo.GetUser(ctx, "nope"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("GetUser err = %v", err)
	}
	if _, err := repo.PrimaryOrganizationID(ctx, "nope"); !errors.Is(err, model.ErrorUserNotFound) {
		t.Fatalf("PrimaryOrganizationID err = %v", err)
	}
}

func TestCreateTokenAndFindByToken(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	u, err := repo.CreateUser(ctx, dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g1"})
	if err != nil {
		t.Fatal(err)
	}

	rec, plaintext, err := repo.CreateToken(ctx, u.ID, "ci deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
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
	var stored models.UserToken
	if err := repo.db.GetDB().NewSelect().Model(&stored).Where("token_id = ?", rec.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash == plaintext || stored.TokenHash == "" {
		t.Error("token stored in plaintext or empty")
	}
}

func TestFindByTokenRejectsUnknownAndExpired(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	u, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g2"})

	if _, err := repo.FindByToken(ctx, "wfp_nope"); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}

	past := time.Now().Add(-time.Hour)
	_, expired, err := repo.CreateToken(ctx, u.ID, "old", &past)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindByToken(ctx, expired); !errors.Is(err, model.ErrInvalidToken) {
		t.Errorf("expired token: err = %v, want ErrInvalidToken", err)
	}
}

func TestFindByTokenStampsLastUsed(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	u, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "A", Provider: "google", AccountID: "g3"})
	rec, plaintext, _ := repo.CreateToken(ctx, u.ID, "t", nil)

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
	repo := newUserRepo(t)
	ctx := context.Background()
	owner, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "O", Provider: "google", AccountID: "g4"})
	other, _ := repo.CreateUser(ctx, dto.UserDTO{Name: "X", Provider: "google", AccountID: "g5"})
	rec, plaintext, _ := repo.CreateToken(ctx, owner.ID, "mine", nil)

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
	repo := newUserRepo(t)
	ctx := context.Background()
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
	repo := newUserRepo(t)
	ctx := context.Background()

	if n, err := repo.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v", n, err)
	}

	admin, err := repo.BootstrapLocalAdmin(ctx, "  Admin@Example.COM ", "SuperSecret1")
	if err != nil {
		t.Fatal(err)
	}
	if admin.Name != "admin" {
		t.Errorf("name = %q, want local-part 'admin'", admin.Name)
	}
	if role, err := repo.RoleOf(ctx, admin.ID); err != nil || role != "owner" {
		t.Errorf("RoleOf = %q, %v; want owner", role, err)
	}

	// Email was normalized on write; verify with normalized form.
	got, err := repo.VerifyLocalCredentials(ctx, "admin@example.com", "SuperSecret1")
	if err != nil || got.ID != admin.ID {
		t.Fatalf("verify after bootstrap: %+v, %v", got, err)
	}

	// Second bootstrap must refuse: users exist now.
	if _, err := repo.BootstrapLocalAdmin(ctx, "evil@example.com", "x"); !errors.Is(err, model.ErrNotBootstrap) {
		t.Errorf("second bootstrap err = %v, want ErrNotBootstrap", err)
	}
}

func TestVerifyLocalCredentials(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	admin, err := repo.BootstrapLocalAdmin(ctx, "a@b.io", "correct-horse-1")
	if err != nil {
		t.Fatal(err)
	}

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
	repo := newUserRepo(t)
	ctx := context.Background()
	admin, _ := repo.BootstrapLocalAdmin(ctx, "boss@x.io", "bootpass-123")
	orgID, err := repo.PrimaryOrganizationID(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	member, err := repo.CreateMemberUser(ctx, orgID, "Bob", "Bob@X.io", "member", "temp-pass-16char")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.PrimaryOrganizationID(ctx, member.ID); err != nil || got != orgID {
		t.Errorf("member org = %q, %v; want admin's org %q", got, err, orgID)
	}
	if role, _ := repo.RoleOf(ctx, member.ID); role != "member" {
		t.Errorf("role = %q", role)
	}
	if u, err := repo.VerifyLocalCredentials(ctx, "bob@x.io", "temp-pass-16char"); err != nil || u.ID != member.ID {
		t.Errorf("temp password login: %v", err)
	}
	if creds, err := repo.GetCredentials(ctx, member.ID); err != nil || !creds.MustChangePassword || creds.Email != "bob@x.io" {
		t.Errorf("creds = %+v, %v; want must_change=true", creds, err)
	}

	// Duplicate email refused.
	if _, err := repo.CreateMemberUser(ctx, orgID, "Bob2", "bob@x.io", "member", "zz"); !errors.Is(err, model.ErrEmailTaken) {
		t.Errorf("dup email err = %v, want ErrEmailTaken", err)
	}

	// No second org was created for the member.
	members, err := repo.ListMembers(ctx, orgID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %d, %v", len(members), err)
	}
}

func TestSetPasswordClearsMustChange(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	admin, _ := repo.BootstrapLocalAdmin(ctx, "boss@y.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, admin.ID)
	m, _ := repo.CreateMemberUser(ctx, orgID, "M", "m@y.io", "member", "old-temp-pass1")

	if err := repo.SetPassword(ctx, m.ID, "brand-new-pass1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@y.io", "old-temp-pass1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Error("old password still valid after SetPassword")
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@y.io", "brand-new-pass1"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
	if creds, _ := repo.GetCredentials(ctx, m.ID); creds.MustChangePassword {
		t.Error("must_change_password not cleared")
	}
}

func TestUpdateMemberRoleLastOwnerGuard(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	admin, _ := repo.BootstrapLocalAdmin(ctx, "boss@z.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, admin.ID)
	m, _ := repo.CreateMemberUser(ctx, orgID, "M", "m@z.io", "member", "temp-password1")

	// Demoting the only owner is refused.
	if err := repo.UpdateMemberRole(ctx, orgID, admin.ID, "member"); !errors.Is(err, model.ErrLastOwner) {
		t.Errorf("demote last owner err = %v, want ErrLastOwner", err)
	}
	// Promote member to admin: fine.
	if err := repo.UpdateMemberRole(ctx, orgID, m.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if role, _ := repo.RoleOf(ctx, m.ID); role != "admin" {
		t.Errorf("role = %q", role)
	}
	// Removing the only owner is refused too.
	if err := repo.RemoveMember(ctx, orgID, admin.ID); !errors.Is(err, model.ErrLastOwner) {
		t.Errorf("remove last owner err = %v, want ErrLastOwner", err)
	}
}

func TestRemoveMemberDeletesUserAndTokens(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	admin, _ := repo.BootstrapLocalAdmin(ctx, "boss@w.io", "bootpass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, admin.ID)
	m, _ := repo.CreateMemberUser(ctx, orgID, "M", "m@w.io", "member", "temp-password1")
	_, patPlain, err := repo.CreateToken(ctx, m.ID, "m's token", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.RemoveMember(ctx, orgID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.VerifyLocalCredentials(ctx, "m@w.io", "temp-password1"); !errors.Is(err, model.ErrInvalidCredentials) {
		t.Error("removed member can still log in")
	}
	if _, err := repo.FindByToken(ctx, patPlain); !errors.Is(err, model.ErrInvalidToken) {
		t.Error("removed member's PAT still resolves")
	}
	if members, _ := repo.ListMembers(ctx, orgID); len(members) != 1 {
		t.Errorf("members after removal = %d, want 1", len(members))
	}
}

func TestRegisterLocalUserCreatesOwnOrg(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()

	a, err := repo.RegisterLocalUser(ctx, "Alice", "Alice@X.io", "alicepass-123")
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.RegisterLocalUser(ctx, "Bob", "bob@x.io", "bobpass-1234")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Alice" {
		t.Errorf("name = %q", a.Name)
	}
	orgA, _ := repo.PrimaryOrganizationID(ctx, a.ID)
	orgB, _ := repo.PrimaryOrganizationID(ctx, b.ID)
	if orgA == "" || orgA == orgB {
		t.Errorf("orgs not distinct: %q vs %q", orgA, orgB)
	}
	if role, _ := repo.RoleOf(ctx, a.ID); role != "owner" {
		t.Errorf("role = %q", role)
	}
	if u, err := repo.VerifyLocalCredentials(ctx, "alice@x.io", "alicepass-123"); err != nil || u.ID != a.ID {
		t.Errorf("login after register: %v", err)
	}
	if _, err := repo.RegisterLocalUser(ctx, "Eve", "ALICE@x.io", "x-password-1"); !errors.Is(err, model.ErrEmailTaken) {
		t.Errorf("dup email err = %v, want ErrEmailTaken", err)
	}
}

func TestGetUpdateOrganization(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()
	u, _ := repo.RegisterLocalUser(ctx, "Alice", "a@o.io", "alicepass-123")
	orgID, _ := repo.PrimaryOrganizationID(ctx, u.ID)

	org, err := repo.GetOrganization(ctx, orgID)
	if err != nil || org.ID != orgID || org.Name == "" {
		t.Fatalf("GetOrganization = %+v, %v", org, err)
	}
	if err := repo.UpdateOrganization(ctx, orgID, "Homelab", "server", "#3b82f6"); err != nil {
		t.Fatal(err)
	}
	org, _ = repo.GetOrganization(ctx, orgID)
	if org.Name != "Homelab" || org.Icon != "server" || org.Color != "#3b82f6" {
		t.Errorf("after update: %+v", org)
	}
	if _, err := repo.GetOrganization(ctx, "nope"); err == nil {
		t.Error("unknown org must error")
	}
}
