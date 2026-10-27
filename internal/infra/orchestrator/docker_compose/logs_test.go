package dockercompose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

func TestDetectLogLevel(t *testing.T) {
	cases := []struct {
		msg  string
		want command.LogLevel
	}{
		{"TRACE fine detail", command.LogLevelTrace},
		{"debug: something", command.LogLevelDebug},
		{"INFO ok", command.LogLevelInfo},
		{"WARN careful", command.LogLevelWarn},
		{"WARNING careful", command.LogLevelWarn},
		{"ERROR broken", command.LogLevelError},
		{"FATAL dead", command.LogLevelFatal},
		{"plain text", command.LogLevelUnknown},
	}
	for _, c := range cases {
		if got := detectLogLevel(c.msg); got != c.want {
			t.Errorf("detectLogLevel(%q) = %d, want %d", c.msg, got, c.want)
		}
	}
}

func TestGetLogsNotDeployed(t *testing.T) {
	r := newTestRepo(t)
	// An app without a rendered run dir has no logs; this is not an error and
	// never shells out to docker.
	resp, err := r.GetLogs(context.Background(), command.GetLogsRequest{AppID: "ghost", Tail: 50})
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if resp.AppID != "ghost" {
		t.Errorf("AppID = %q, want ghost", resp.AppID)
	}
	if len(resp.Logs) != 0 {
		t.Errorf("Logs = %v, want empty", resp.Logs)
	}
}

func TestParseComposeLogLine(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		wantOK        bool
		wantContainer string
		wantMessage   string
		wantLevel     command.LogLevel
	}{
		{
			name:          "container prefix + rfc3339 timestamp",
			line:          "web-1  | 2026-06-20T12:00:00.000000000Z INFO server started",
			wantOK:        true,
			wantContainer: "web-1",
			wantMessage:   "INFO server started",
			wantLevel:     command.LogLevelInfo,
		},
		{
			name:        "no container prefix, error level",
			line:        "2026-06-20T12:00:01Z ERROR boom",
			wantOK:      true,
			wantMessage: "ERROR boom",
			wantLevel:   command.LogLevelError,
		},
		{
			name:          "no timestamp keeps full message",
			line:          "db-1  | plain text line",
			wantOK:        true,
			wantContainer: "db-1",
			wantMessage:   "plain text line",
			wantLevel:     command.LogLevelUnknown,
		},
		{
			name:        "ansi codes stripped",
			line:        "\x1b[31mWARN something\x1b[0m",
			wantOK:      true,
			wantMessage: "WARN something",
			wantLevel:   command.LogLevelWarn,
		},
		{
			name:   "blank line dropped",
			line:   "   ",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := parseComposeLogLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if entry.Container != tc.wantContainer {
				t.Errorf("container = %q, want %q", entry.Container, tc.wantContainer)
			}
			if entry.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", entry.Message, tc.wantMessage)
			}
			if entry.Level != tc.wantLevel {
				t.Errorf("level = %d, want %d", entry.Level, tc.wantLevel)
			}
		})
	}
}

// logsDocker installs a docker stub that logs its argv and prints one
// compose log line, or, for a service named "nope", compose's error.
func logsDocker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"for a; do last=$a; done\n" +
		"if [ \"$last\" = nope ]; then echo 'no such service: nope' >&2; exit 1; fi\n" +
		"echo 'my-postgres  | 2026-06-20T12:00:00.000000000Z ready'\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return log
}

// TestGetLogsService: a service is passed to `docker compose logs` as its
// last argument (compose filters it, container_name or not) and echoed.
func TestGetLogsService(t *testing.T) {
	r := newTestRepo(t)
	dir := r.appDataDir("app-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services:\n  db:\n    image: postgres\n    container_name: my-postgres\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	log := logsDocker(t)
	resp, err := r.GetLogs(ctx, command.GetLogsRequest{AppID: "app-1", Tail: 10, Service: "db"})
	if err != nil {
		t.Fatal(err)
	}
	calls := dockerCalls(t, log)
	if len(calls) != 1 || !strings.HasSuffix(calls[0], " logs --no-color --timestamps --tail 10 db") {
		t.Fatalf("calls = %q", calls)
	}
	if resp.Service != "db" || len(resp.Logs) != 1 || resp.Logs[0].Container != "my-postgres" {
		t.Fatalf("resp = %+v", resp)
	}

	log = logsDocker(t)
	resp, err = r.GetLogs(ctx, command.GetLogsRequest{AppID: "app-1", Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	if calls := dockerCalls(t, log); len(calls) != 1 || !strings.HasSuffix(calls[0], " logs --no-color --timestamps --tail 10") {
		t.Fatalf("calls without a service = %q", calls)
	}
	if resp.Service != "" {
		t.Fatalf("service echoed without one: %+v", resp)
	}

	logsDocker(t)
	if _, err := r.GetLogs(ctx, command.GetLogsRequest{AppID: "app-1", Service: "nope"}); err == nil || !strings.Contains(err.Error(), `no service "nope"`) {
		t.Fatalf("unknown service = %v", err)
	}
}

func TestGetLogsRequestService(t *testing.T) {
	for name, ok := range map[string]bool{"": true, "db": true, "my_app.web-2": true, "--follow": false, "-f": false,
		"db web": false, "a/b": false, strings.Repeat("a", 129): false} {
		err := command.GetLogsRequest{AppID: "app-1", Service: name}.Validate()
		if (err == nil) != ok {
			t.Errorf("service %q: Validate = %v", name, err)
		}
	}
}
