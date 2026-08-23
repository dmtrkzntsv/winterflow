package repository

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
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

func TestGetServersIncludesCapabilities(t *testing.T) {
	conn := newTestDB(t)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	repo := NewDbServerRepository(conn, log)

	seedOrgWithServer(t, conn, "org-1", "srv-1")
	seedUser(t, conn, "alice", "org-1")
	if err := repo.SaveCapabilities(context.Background(), "srv-1",
		map[string]string{"server_ip": "203.0.113.7", "system_cpu_cores": "8"}, nil); err != nil {
		t.Fatal(err)
	}

	servers, err := repo.GetServers(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	caps := map[string]string{}
	for _, c := range servers[0].Capabilities {
		caps[c.Name] = c.Value
	}
	if caps["server_ip"] != "203.0.113.7" || caps["system_cpu_cores"] != "8" {
		t.Fatalf("capabilities = %v", caps)
	}

	// The JSON the handler sends must expose name/value in lower case.
	raw, _ := json.Marshal(servers[0])
	if !strings.Contains(string(raw), `"name":"server_ip"`) || !strings.Contains(string(raw), `"value":"203.0.113.7"`) {
		t.Fatalf("serialized server = %s", raw)
	}
}

func TestGetServersIncludesFeatures(t *testing.T) {
	conn := newTestDB(t)
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	repo := NewDbServerRepository(conn, log)

	seedOrgWithServer(t, conn, "org-1", "srv-1")
	seedUser(t, conn, "alice", "org-1")
	if err := repo.SaveCapabilities(context.Background(), "srv-1",
		nil, map[string]bool{"ingress": true}); err != nil {
		t.Fatal(err)
	}

	servers, err := repo.GetServers(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(servers))
	}
	if servers[0].Features["ingress"] != true {
		t.Fatalf("features = %v", servers[0].Features)
	}
}
