package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout swaps os.Stdout for a pipe while fn runs and returns what was
// written. The logger binds os.Stdout at construction time, so NewLogger must
// be called inside fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String()
}

// parseLines decodes each JSON log line into a map.
func parseLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not valid JSON: %q (%v)", line, err)
		}
		records = append(records, m)
	}
	return records
}

func TestNewLoggerFields(t *testing.T) {
	l := NewLogger(LoggerConfiguration{LogLevel: "info", Service: "api"})
	if l == nil || l.Logger == nil {
		t.Fatal("NewLogger returned nil logger")
	}
	if l.LogLevel != "info" || l.Service != "api" {
		t.Fatalf("NewLogger kept LogLevel=%q Service=%q, want info/api", l.LogLevel, l.Service)
	}
}

func TestLoggerEmitsJSONWithServiceAndArgs(t *testing.T) {
	out := captureStdout(t, func() {
		l := NewLogger(LoggerConfiguration{LogLevel: "debug", Service: "hub"})
		l.Info("hello", "request_id", "abc-123")
	})
	recs := parseLines(t, out)
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1: %q", len(recs), out)
	}
	r := recs[0]
	if r["msg"] != "hello" {
		t.Fatalf("msg = %v, want hello", r["msg"])
	}
	if r["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", r["level"])
	}
	if r["service"] != "hub" {
		t.Fatalf("service = %v, want hub", r["service"])
	}
	if r["request_id"] != "abc-123" {
		t.Fatalf("request_id = %v, want abc-123", r["request_id"])
	}
}

func TestLoggerWithoutServiceOmitsAttr(t *testing.T) {
	out := captureStdout(t, func() {
		l := NewLogger(LoggerConfiguration{})
		l.Info("no service")
	})
	recs := parseLines(t, out)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if _, ok := recs[0]["service"]; ok {
		t.Fatalf("service attr present (%v), want absent when Service is empty", recs[0]["service"])
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		wantLevels []string // levels of records that must come through, in order
	}{
		{"debug passes everything", "debug", []string{"DEBUG", "INFO", "WARN", "ERROR"}},
		{"info drops debug", "info", []string{"INFO", "WARN", "ERROR"}},
		{"warn drops debug and info", "warn", []string{"WARN", "ERROR"}},
		{"warning is an alias of warn", "warning", []string{"WARN", "ERROR"}},
		{"error drops all but error", "error", []string{"ERROR"}},
		{"levels are case-insensitive", "INFO", []string{"INFO", "WARN", "ERROR"}},
		{"empty level defaults to debug", "", []string{"DEBUG", "INFO", "WARN", "ERROR"}},
		{"unknown level defaults to debug", "bogus", []string{"DEBUG", "INFO", "WARN", "ERROR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				l := NewLogger(LoggerConfiguration{LogLevel: tt.level, Service: "test"})
				l.Debug("d")
				l.Info("i")
				l.Warn("w")
				l.Error("e")
			})
			recs := parseLines(t, out)
			var got []string
			for _, r := range recs {
				lvl, _ := r["level"].(string)
				got = append(got, lvl)
			}
			if len(got) != len(tt.wantLevels) {
				t.Fatalf("level %q: emitted %v, want %v", tt.level, got, tt.wantLevels)
			}
			for i := range got {
				if got[i] != tt.wantLevels[i] {
					t.Fatalf("level %q: emitted %v, want %v", tt.level, got, tt.wantLevels)
				}
			}
		})
	}
}

// Note: Fatal and Fatalf call os.Exit(1) and are deliberately not exercised.
