package notification

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/model"
	notificationsvc "winterflow/internal/domain/service/notification"
	"winterflow/pkg/logger"
)

func asUser(id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := token.User{ID: id}
		u.SetStrAttr("user_id", id)
		next.ServeHTTP(w, token.SetUserInfo(r, u))
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
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
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
