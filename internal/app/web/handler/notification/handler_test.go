package notification

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// fakeAuth authenticates "tok" as user u1's token t1 until revoked, counting
// the checks.
type fakeAuth struct {
	mu      sync.Mutex
	revoked bool
	checks  int
}

func (f *fakeAuth) Authenticate(ctx context.Context, raw string) (model.Principal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	if _, ok := ctx.Deadline(); !ok {
		return model.Principal{}, errors.New("re-authentication without a deadline")
	}
	if raw != "tok" || f.revoked {
		return model.Principal{}, model.ErrUnauthorized
	}
	return model.Principal{UserID: "u1", TokenID: "t1", Kind: model.TokenSession}, nil
}

func (f *fakeAuth) revoke() {
	f.mu.Lock()
	f.revoked = true
	f.mu.Unlock()
}

func (f *fakeAuth) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

// openStream serves h as user u1's token t1 and opens the stream with the
// bearer credential "tok".
func openStream(t *testing.T, h *Handler) *http.Response {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Stream(w, webutil.WithPrincipal(r, model.Principal{UserID: "u1", TokenID: "t1", Kind: model.TokenSession}))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// A revoked session's open stream ends at the next re-authentication.
func TestStreamEndsWhenCredentialRevoked(t *testing.T) {
	if reauthInterval > time.Minute {
		t.Fatalf("reauthInterval = %v; a revoked session would keep its stream too long", reauthInterval)
	}
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	auth := &fakeAuth{}
	h := NewHandler(&Deps{Logger: log, NotificationManager: notificationsvc.NewNotificationManager(log), Auth: auth})
	h.every = 20 * time.Millisecond
	resp := openStream(t, h)

	auth.revoke()
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body) // returns once the server ends the stream
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream did not end cleanly: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the revoked session's stream is still open")
	}
}

// A stream whose credential still authenticates stays open across checks
// and keeps delivering.
func TestStreamStaysOpenWhileValid(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	nm := notificationsvc.NewNotificationManager(log)
	auth := &fakeAuth{}
	h := NewHandler(&Deps{Logger: log, NotificationManager: nm, Auth: auth})
	h.every = 10 * time.Millisecond
	resp := openStream(t, h)

	deadline := time.Now().Add(3 * time.Second)
	for auth.count() < 5 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d checks ran", auth.count())
		}
		time.Sleep(5 * time.Millisecond)
	}
	nm.Publish("u1", model.Notification{Type: model.NotificationOperationResult, Ref: "after-checks"})
	if line := nextData(t, bufio.NewReader(resp.Body)); !strings.Contains(line, `"ref":"after-checks"`) {
		t.Fatalf("event = %q", line)
	}
}

// Without an authenticator the stream fails closed at its first check.
func TestStreamWithoutAuthEndsAtFirstCheck(t *testing.T) {
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"})
	h := NewHandler(&Deps{Logger: log, NotificationManager: notificationsvc.NewNotificationManager(log)})
	h.every = 10 * time.Millisecond
	resp := openStream(t, h)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
}
