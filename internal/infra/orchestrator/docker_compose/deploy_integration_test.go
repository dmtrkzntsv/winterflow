//go:build integration

package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// TestSaveAppDeploysContainer is an integration test (build tag `integration`,
// requires Docker) exercising the real create->deploy->rollback path on the
// git-per-app layout: SaveApp commits and runs `docker compose up` in the app
// folder (compose interpolates ${VAR} from the committed .env), Rollback
// restores the first commit and redeploys. Run with:
//
//	go test -tags integration -run TestSaveAppDeploysContainer ./internal/infra/orchestrator/docker_compose/
func TestSaveAppDeploysContainer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_DATA_DIR", dir)

	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	repo := NewRepository(cfg, log)

	appID := "itg-test-app"
	compose := "services:\n  hello:\n    image: hello-world:latest\n    container_name: ${CONTAINER_NAME}\n"

	app := command.AppPayload{
		AppID:  appID,
		Config: mustJSON(t, map[string]any{"name": "itg-test", "files": []map[string]any{{"filename": "compose.yml"}}, "variables": []map[string]any{{"name": "CONTAINER_NAME"}}}),
		Files: []command.ContentItem{
			{Name: "compose.yml", Content: []byte(compose)},
		},
		Variables: []command.ContentItem{
			{Name: "CONTAINER_NAME", Content: []byte("itg_hello")},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	t.Cleanup(func() {
		_ = repo.DeleteApp(context.Background(), appID)
	})

	h1, err := repo.SaveApp(ctx, app)
	if err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	if h1 == "" {
		t.Fatal("no commit hash")
	}

	// The folder IS the deployment: compose.yml verbatim, ${VAR} in .env.
	appDir := filepath.Join(cfg.GetAppsDataDir(), appID)
	if raw, err := os.ReadFile(filepath.Join(appDir, "compose.yml")); err != nil || !contains(string(raw), "${CONTAINER_NAME}") {
		t.Fatalf("compose.yml should keep the placeholder (compose interpolates): %q %v", raw, err)
	}
	if raw, err := os.ReadFile(filepath.Join(appDir, ".env")); err != nil || !contains(string(raw), "CONTAINER_NAME=itg_hello") {
		t.Fatalf(".env = %q (%v)", raw, err)
	}
	// The human-readable symlink resolves to the app folder.
	if _, err := os.Stat(filepath.Join(cfg.GetAppsDir(), "itg-test")); err != nil {
		t.Fatalf("apps/ symlink missing: %v", err)
	}
	// The container name proves compose actually interpolated from .env.
	statuses, err := repo.GetAppsStatus(ctx)
	if err != nil {
		t.Fatalf("GetAppsStatus: %v", err)
	}
	found := false
	for _, s := range statuses {
		if s.AppID != appID {
			continue
		}
		found = true
		if len(s.Containers) == 0 || !contains(s.Containers[0].Name, "itg_hello") {
			t.Errorf("container name not interpolated: %+v", s.Containers)
		}
	}
	if !found {
		t.Errorf("app %s not found in statuses %+v", appID, statuses)
	}

	// Second save + rollback: the first compose content comes back, deployed.
	app.Files[0].Content = []byte(compose + "    # v2 marker\n")
	if _, err := repo.SaveApp(ctx, app); err != nil {
		t.Fatalf("second SaveApp: %v", err)
	}
	newHead, err := repo.Rollback(ctx, appID, h1)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if newHead == h1 {
		t.Fatal("rollback must create a new commit")
	}
	if raw, _ := os.ReadFile(filepath.Join(appDir, "compose.yml")); contains(string(raw), "# v2 marker") {
		t.Fatalf("rollback did not restore v1 compose: %q", raw)
	}
	revs, _, err := repo.Revisions(ctx, appID)
	if err != nil || len(revs) != 3 {
		t.Fatalf("history after rollback = %+v (%v)", revs, err)
	}
}

// TestNetworkLifecycle exercises the real network create -> list -> delete path
// against Docker.
func TestNetworkLifecycle(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	cfg := config.NewServerConfig("standalone")
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	repo := NewRepository(cfg, log)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	name := "wf-itg-net"
	t.Cleanup(func() { _ = repo.DeleteNetwork(context.Background(), name) })

	if err := repo.CreateNetwork(ctx, command.CreateNetworkRequest{Name: name}); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}

	nets, err := repo.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	found := false
	for _, n := range nets {
		if n.Name == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("created network %q not in list", name)
	}

	if err := repo.DeleteNetwork(ctx, name); err != nil {
		t.Fatalf("DeleteNetwork: %v", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
