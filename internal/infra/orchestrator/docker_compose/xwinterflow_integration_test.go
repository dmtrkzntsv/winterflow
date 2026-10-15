//go:build integration

package dockercompose

import (
	"context"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// TestDeployXWinterflow deploys a compose file with an x-winterflow block
// for real (build tag integration, needs Docker and nginx:alpine): the http
// entry answers on 127.0.0.1 at its allocated port, and the declared
// variable's value reaches the container through the override file. Run:
//
//	go test -tags integration -run TestDeployXWinterflow ./internal/infra/orchestrator/docker_compose/
func TestDeployXWinterflow(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	t.Setenv("EXPOSE_PORT_RANGE", "31200-31299")
	repo := NewRepository(config.NewServerConfig("standalone"), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	const compose = `services:
  web:
    image: nginx:alpine
    environment:
      GREETING: hello
x-winterflow:
  expose:
    - { container: web, port: 80 }
  variables:
    GREETING: { pattern: "[a-z]+" }
`
	app := command.AppPayload{
		AppID:     "itg-xwf",
		Config:    []byte(`{"name":"itg-xwf","files":[{"filename":"compose.yml"}],"variables":[{"name":"GREETING"}]}`),
		Files:     []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
		Variables: []command.ContentItem{{Name: "GREETING", Content: []byte("howdy")}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Cleanup(func() { _, _ = repo.DeleteApp(context.Background(), app.AppID, false, false) })
	if _, err := repo.SaveApp(ctx, app); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	host := portsOf(t, repo, app.AppID)["web:80/http"].Host
	if host < 31200 || host > 31299 {
		t.Fatalf("host port = %d", host)
	}
	var code int
	for i := 0; i < 20 && code != http.StatusOK; i++ {
		if resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(host) + "/"); err == nil {
			code = resp.StatusCode
			resp.Body.Close()
		} else {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if code != http.StatusOK {
		t.Fatalf("GET 127.0.0.1:%d = %d", host, code)
	}
	out, err := exec.CommandContext(ctx, "docker", "exec", projectName(app.AppID)+"-web-1", "printenv", "GREETING").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "howdy" {
		t.Fatalf("GREETING in the container = %q (%v)", out, err)
	}
}
