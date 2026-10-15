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
	t.Cleanup(func() {
		// Its own bound: the test's ctx may be spent by now.
		cctx, ccancel := context.WithTimeout(context.Background(), time.Minute)
		defer ccancel()
		if _, err := repo.DeleteApp(cctx, app.AppID, false, false); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if _, err := repo.SaveApp(ctx, app); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	host := portsOf(t, repo, app.AppID)["web:80/http"].Host
	if host < 31200 || host > 31299 {
		t.Fatalf("host port = %d", host)
	}
	var code int
	for i := 0; i < 20 && code != http.StatusOK; i++ {
		if i > 0 {
			time.Sleep(250 * time.Millisecond) // after an error and a non-200 alike
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(host)+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp, err := http.DefaultClient.Do(req); err == nil {
			code = resp.StatusCode
			resp.Body.Close()
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
