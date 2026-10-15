package xwinterflow

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/render")

// golden compares got with testdata/render/<name>.yml (rewritten by -update).
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "render", name+".yml")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func render(t *testing.T, s Spec, values []string, ports map[string]int) []byte {
	t.Helper()
	has := map[string]bool{}
	for _, v := range values {
		has[v] = true
	}
	out, err := Render(s, func(n string) bool { return has[n] }, func(e Expose) int { return ports[e.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRenderNotes(t *testing.T) {
	s := parse(t, ModeApp, notes)
	out := render(t, s, []string{"BASE_URL", "SESSION_SECRET", "LANGUAGE", "DB_PASSWORD"},
		map[string]int{"app:8080/http": 20013, "db:5432/tcp": 20014})
	golden(t, "notes", out)
	if strings.Contains(string(out), "notes.example.com") || strings.Contains(string(out), "changeme") {
		t.Fatalf("a value leaked into the override file:\n%s", out)
	}
}

// A variable without a value emits nothing: the body literal stands.
func TestRenderVariables(t *testing.T) {
	s := parse(t, ModeApp, notes)
	s.Expose, s.Overrides = nil, nil
	golden(t, "variables", render(t, s, []string{"LANGUAGE"}, nil))
}

func TestRenderExpose(t *testing.T) {
	s := parse(t, ModeApp, `
services:
  web: { image: a, ports: ["8080:80"] }
  dns: { image: b }
x-winterflow:
  expose:
    - { container: web, port: 80 }
    - { container: dns, port: 53, protocol: tcp }
    - { container: dns, port: 53, protocol: udp }
`)
	golden(t, "expose", render(t, s, nil, map[string]int{"web:80/http": 20000, "dns:53/tcp": 5353, "dns:53/udp": 5353}))
}

// Lists in overrides replace the body's (!override); an environment list
// merges as a mapping; expose entries join an overridden ports list.
func TestRenderOverrides(t *testing.T) {
	s := parse(t, ModeApp, `
services:
  app:
    image: a
    command: ["serve"]
    ports: ["9000:9000"]
    environment: [MODE=dev]
    healthcheck: { test: ["CMD", "true"] }
x-winterflow:
  expose: [{ container: app, port: 8080 }]
  variables:
    MODE: {}
  overrides:
    services:
      app:
        command: &cmd ["serve", "--port", "8080"]
        entrypoint: *cmd
        ports: ["127.0.0.1:9000:9000"]
        environment:
          - MODE=prod
          - PASSTHROUGH
        healthcheck:
          test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
        labels:
          app.id: ${WF_APP_ID}
`)
	golden(t, "overrides", render(t, s, []string{"MODE"}, map[string]int{"app:8080/http": 20001}))
}

func TestRenderNothing(t *testing.T) {
	out, err := Render(parse(t, ModeApp, "services:\n  app: { image: a }\n"), func(string) bool { return true }, func(Expose) int { return 1 })
	if err != nil || out != nil {
		t.Fatalf("no block: %q %v", out, err)
	}
	s := parse(t, ModeApp, "services:\n  app: { image: a, environment: { A: x } }\nx-winterflow:\n  variables:\n    A: {}\n")
	if out := render(t, s, nil, nil); out != nil {
		t.Fatalf("no value: %s", out)
	}
}

func TestRenderNeedsHostPort(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app: { image: a }\nx-winterflow:\n  expose: [{ container: app, port: 80 }]\n")
	_, err := Render(s, func(string) bool { return false }, func(Expose) int { return 0 })
	if err == nil || !strings.Contains(err.Error(), "app:80/http has no host port yet") {
		t.Fatalf("err = %v", err)
	}
}
