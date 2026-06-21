package dockercompose

import (
	"testing"

	"winterflow/internal/domain/command"
)

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
