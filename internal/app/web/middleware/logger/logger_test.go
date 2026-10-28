package logger

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	applog "winterflow/pkg/logger"
)

func TestLogPathRedactsSecretLinks(t *testing.T) {
	for in, want := range map[string]string{
		"/secrets/wfx_abc":        "/secrets/[redacted]",
		"/secrets/wfx_abc/x":      "/secrets/[redacted]",
		"/api/v1/app/get-apps":    "/api/v1/app/get-apps",
		"/server/s1/app/a1":       "/server/s1/app/a1",
		"/secretsx/wfx_not_token": "/secretsx/wfx_not_token",
	} {
		if got := LogPath(in); got != want {
			t.Errorf("LogPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequestLogOmitsToken(t *testing.T) {
	var buf bytes.Buffer
	l := &applog.Logger{Logger: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	h := WithLogger(l)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/secrets/wfx_SECRETTOKEN", nil))
	if strings.Contains(buf.String(), "wfx_SECRETTOKEN") || !strings.Contains(buf.String(), "/secrets/[redacted]") {
		t.Fatalf("request log = %q", buf.String())
	}
}
