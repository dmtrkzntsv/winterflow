//go:build integration

package dockercompose

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// TestDeleteAppWithVolumes deploys an app owning a named volume and mounting
// an external one, deletes it with deleteVolumes=true, and checks the owned
// volume is gone while the external one survives. Run with:
//
//	go test -tags integration -run TestDeleteAppWithVolumes ./internal/infra/orchestrator/docker_compose/
func TestDeleteAppWithVolumes(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	repo := NewRepository(config.NewServerConfig("standalone"),
		logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	const ext = "wf-itg-external-vol"
	if out, err := exec.Command("docker", "volume", "create", ext).CombinedOutput(); err != nil {
		t.Fatalf("create external: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", ext).Run() })

	appID := "itg-vol-app"
	// Safety net if an assertion fails before the explicit delete below.
	t.Cleanup(func() {
		_, _ = repo.DeleteApp(context.Background(), appID, true)
		_ = exec.Command("docker", "volume", "rm", "-f", projectName(appID)+"_data").Run()
	})
	compose := "services:\n  s:\n    image: alpine:3.20\n    command: [\"sleep\", \"300\"]\n" +
		"    volumes: [\"data:/data\", \"ext:/ext\"]\n" +
		"volumes:\n  data: {}\n  ext:\n    external: true\n    name: " + ext + "\n"
	app := command.AppPayload{
		AppID:  appID,
		Config: mustJSON(t, map[string]any{"name": "itg-vol", "files": []map[string]any{{"filename": "compose.yml"}}}),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
	}
	if _, err := repo.SaveApp(ctx, app); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	owned := projectName(appID) + "_data"
	if !volumeExists(t, owned) {
		t.Fatalf("owned volume %s not created", owned)
	}

	res, err := repo.DeleteApp(ctx, appID, true)
	if err != nil {
		t.Fatalf("DeleteApp: %v (warnings %v)", err, res.Warnings)
	}
	if len(res.Warnings) > 0 {
		t.Fatalf("clean delete must not warn: %v", res.Warnings)
	}
	if volumeExists(t, owned) {
		t.Fatalf("owned volume %s must be removed", owned)
	}
	if !volumeExists(t, ext) {
		t.Fatal("external volume must survive")
	}
}

func volumeExists(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("docker", "volume", "ls", "-q", "--filter", "name=^"+name+"$").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out)) == name
}
