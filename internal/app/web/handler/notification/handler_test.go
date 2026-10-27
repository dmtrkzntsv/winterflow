package notification

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	webutil "winterflow/internal/app/web/util"

	"winterflow/internal/domain/model"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/pkg/logger"
)

func asUser(id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, webutil.WithPrincipal(r, model.Principal{UserID: id, Kind: model.TokenSession}))
	})
}

// TestStreamIsSubscribedWhenHeadersArrive: the handler flushes the headers
// right after subscribing, so a client that has its 200 cannot miss a
// notification published afterwards.
func TestStreamIsSubscribedWhenHeadersArrive(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	nm := notificationsvc.NewNotificationManager(log)
	h := NewHandler(&Deps{Logger: log, NotificationManager: nm})
	srv := httptest.NewServer(asUser("u1", http.HandlerFunc(h.Stream)))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req) // returns only once headers are flushed
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	nm.Publish("u1", model.Notification{Type: model.NotificationOperationResult, Ref: "r1"})
	line := nextData(t, bufio.NewReader(resp.Body))
	if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, `"ref":"r1"`) {
		t.Fatalf("event = %q", line)
	}
}

func TestStreamRequiresUser(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	h := NewHandler(&Deps{Logger: log, NotificationManager: notificationsvc.NewNotificationManager(log)})
	rec := httptest.NewRecorder()
	h.Stream(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sse", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

// nextData returns the stream's next data line, skipping the retry field and
// blank separators.
func nextData(t *testing.T, br *bufio.Reader) string {
	t.Helper()
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			return line
		}
	}
}

// A stream is authenticated once, at connect: it must end after its
// lifetime so the browser reconnects through the auth middleware (a revoked
// session stops receiving events), and it announces a short retry.
func TestStreamEndsAfterLifetime(t *testing.T) {
	if streamLifetime+streamJitter > 15*time.Minute {
		t.Fatalf("stream lifetime %v+%v is too long for revocation to bite", streamLifetime, streamJitter)
	}
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	nm := notificationsvc.NewNotificationManager(log)
	h := NewHandler(&Deps{Logger: log, NotificationManager: nm})
	if d := h.lifetime(); d < streamLifetime || d >= streamLifetime+streamJitter {
		t.Fatalf("default lifetime = %v", d)
	}
	h.lifetime = func() time.Duration { return 100 * time.Millisecond }
	srv := httptest.NewServer(asUser("u1", http.HandlerFunc(h.Stream)))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body) // returns once the server ends the stream
	if err != nil {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
	if !strings.HasPrefix(string(body), "retry: 1000\n\n") {
		t.Fatalf("body = %q, want a retry field first", body)
	}
}
