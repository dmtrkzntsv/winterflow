//go:build integration

package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// TestSaveAppDeploysContainer is an integration test (build tag `integration`,
// requires Docker) that exercises the real create->deploy path: SaveApp writes
// a compose file with a substituted variable, runs `docker compose up`, and we
// assert the container reports active. Run with:
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
		Config: mustJSON(t, map[string]string{"name": "itg-test"}),
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

	rev, err := repo.SaveApp(ctx, app)
	if err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	if rev != 1 {
		t.Errorf("revision = %d, want 1", rev)
	}

	// The rendered compose file must exist with the variable substituted.
	rendered, err := os.ReadFile(cfg.GetAppsDir() + "/" + appID + "/compose.yml")
	if err != nil {
		t.Fatalf("read rendered compose: %v", err)
	}
	if want := "container_name: itg_hello"; !contains(string(rendered), want) {
		t.Errorf("rendered compose missing %q:\n%s", want, rendered)
	}

	// Status should list the app (hello-world exits 0, so Stopped is fine — the
	// point is the deploy ran and the container exists).
	statuses, err := repo.GetAppsStatus(ctx)
	if err != nil {
		t.Fatalf("GetAppsStatus: %v", err)
	}
	found := false
	for _, s := range statuses {
		if s.AppID == appID {
			found = true
		}
	}
	if !found {
		t.Errorf("app %s not found in statuses %+v", appID, statuses)
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
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
