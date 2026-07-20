package dockercompose

import (
	"context"
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
