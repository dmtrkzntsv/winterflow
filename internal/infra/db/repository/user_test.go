package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
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

func TestFindByToken(t *testing.T) {
	repo := newUserRepo(t)
	ctx := context.Background()

	u, err := repo.CreateUser(ctx, dto.UserDTO{Name: "Bob", Provider: "google", AccountID: "goog-9"})
	if err != nil {
		t.Fatal(err)
	}

	insertToken := func(tok string, expires *time.Time) {
		row := &models.UserToken{
			TokenID:   tok + "-id",
			Token:     tok,
			TokenType: "pat",
			UserID:    u.ID,
			CreatedAt: types.NewDateTime(),
		}
		if expires != nil {
			e := types.DateTime(*expires)
			row.ExpiresAt = &e
		}
		if _, err := repo.db.GetDB().NewInsert().Model(row).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	insertToken("valid-token", nil)
	past := time.Now().Add(-time.Hour)
	insertToken("expired-token", &past)

	got, err := repo.FindByToken(ctx, "valid-token")
	if err != nil || got.ID != u.ID {
		t.Fatalf("FindByToken(valid) = %+v, %v", got, err)
	}
	if _, err := repo.FindByToken(ctx, "expired-token"); !errors.Is(err, model.ErrInvalidToken) {
		t.Fatalf("expired token err = %v", err)
	}
	if _, err := repo.FindByToken(ctx, "unknown"); !errors.Is(err, model.ErrInvalidToken) {
		t.Fatalf("unknown token err = %v", err)
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
