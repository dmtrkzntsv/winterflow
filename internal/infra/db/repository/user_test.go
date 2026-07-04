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
