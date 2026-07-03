package repository

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/models"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// newTestDB opens a migrated throwaway SQLite database.
func newTestDB(t *testing.T) *db.BunConnection {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn := db.NewBunConnection(log, "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	t.Cleanup(func() { _ = conn.Shutdown() })
	return conn
}

func seedUser(t *testing.T, conn *db.BunConnection, userID, orgID string) {
	t.Helper()
	ctx := context.Background()
	dbi := conn.GetDB()
	if _, err := dbi.NewInsert().Model(&models.User{
		UserID: userID, Name: userID, CreatedAt: types.NewDateTime(), LastSeen: types.NewDateTime(),
	}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := dbi.NewInsert().Model(&models.OrganizationUser{
		OrganizationID: orgID, UserID: userID, Role: "owner", CreatedAt: types.NewDateTime(),
	}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func seedOrgWithServer(t *testing.T, conn *db.BunConnection, orgID, serverID string) {
	t.Helper()
	ctx := context.Background()
	dbi := conn.GetDB()
	if _, err := dbi.NewInsert().Model(&models.Organization{
		OrganizationID: orgID, Name: orgID, CreatedAt: types.NewDateTime(),
	}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if serverID != "" {
		if _, err := dbi.NewInsert().Model(&models.Server{
			ServerID: serverID, OrganizationID: orgID, Name: "box", CreatedAt: types.NewDateTime(),
		}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetServerUserIDsReturnsOrgMembers(t *testing.T) {
	conn := newTestDB(t)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	repo := NewDbServerRepository(conn, log)

	seedOrgWithServer(t, conn, "org-1", "srv-1")
	seedOrgWithServer(t, conn, "org-2", "srv-2")
	seedUser(t, conn, "alice", "org-1")
	seedUser(t, conn, "bob", "org-1")
	seedUser(t, conn, "eve", "org-2")

	got, err := repo.GetServerUserIDs(context.Background(), "srv-1")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != "alice" || got[1] != "bob" {
		t.Fatalf("want [alice bob], got %v", got)
	}
}

func TestGetServerUserIDsUnknownServerIsEmpty(t *testing.T) {
	conn := newTestDB(t)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	repo := NewDbServerRepository(conn, log)

	got, err := repo.GetServerUserIDs(context.Background(), "nope")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}
