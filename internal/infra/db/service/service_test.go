package service

import (
	"context"
	"path/filepath"
	"testing"

	"winterflow/internal/domain/dto"
	"winterflow/internal/infra/db"
	repo "winterflow/internal/infra/db/repository"
	"winterflow/pkg/logger"
)

func newServices(t *testing.T) (*DbUserService, *DbServerService) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn := db.NewBunConnection(log, "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	t.Cleanup(func() { _ = conn.Shutdown() })
	userRepo := repo.NewDbUserRepository(conn, log)
	serverRepo := repo.NewDbServerRepository(conn, log)
	return NewDbUserService(log, userRepo), NewDbServerService(log, serverRepo)
}

func TestFindOrCreateUserIsIdempotent(t *testing.T) {
	users, _ := newServices(t)
	ctx := context.Background()
	in := dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g-1"}

	first, err := users.FindOrCreateUser(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := users.FindOrCreateUser(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("second call created a new user: %q vs %q", first.ID, second.ID)
	}

	orgID, err := users.PrimaryOrganizationID(ctx, first.ID)
	if err != nil || orgID == "" {
		t.Fatalf("PrimaryOrganizationID = %q, %v", orgID, err)
	}
}

func TestServerServiceDelegates(t *testing.T) {
	users, servers := newServices(t)
	ctx := context.Background()

	u, err := users.FindOrCreateUser(ctx, dto.UserDTO{Name: "Bob", Provider: "google", AccountID: "g-2"})
	if err != nil {
		t.Fatal(err)
	}

	list, err := servers.GetServers(ctx, u.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("GetServers = %v, %v", list, err)
	}

	if _, ok, err := servers.PendingRegistrationCode(ctx); err != nil || ok {
		t.Fatalf("PendingRegistrationCode on empty DB = ok=%v, err=%v", ok, err)
	}
}
