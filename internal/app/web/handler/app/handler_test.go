package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/logger"
)

// fakeDispatcher records DispatchInput and returns a canned request id.
type fakeDispatcher struct {
	last port.DispatchInput
}

func (f *fakeDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	f.last = in
	return "req-123", nil
}

type fakeAppRepo struct {
	apps []model.App
}

func (f *fakeAppRepo) GetApps(context.Context, string) ([]model.App, error) { return f.apps, nil }
func (f *fakeAppRepo) GetApp(context.Context, string) (model.App, error) {
	return model.App{}, model.ErrAppNotFound
}
func (f *fakeAppRepo) CreateApp(context.Context, model.App) error         { return nil }
func (f *fakeAppRepo) DeleteApp(context.Context, string) error            { return nil }
func (f *fakeAppRepo) RenameApp(context.Context, string, string) error    { return nil }
func (f *fakeAppRepo) SyncApps(context.Context, string, []model.App) error { return nil }

func newHandler(t *testing.T) (*Handler, *fakeDispatcher, *status.Cache) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	fd := &fakeDispatcher{}
	cache := status.NewCache(time.Minute)
	h := NewHandler(&Deps{
		Logger:            log,
		CommandDispatcher: fd,
		AppRepository:     &fakeAppRepo{apps: []model.App{{ID: "a1", Name: "grafana"}}},
		StatusCache:       cache,
	})
	return h, fd, cache
}

// authed attaches an authenticated user to the request, mirroring what the
// auth middleware does in production.
func authed(r *http.Request) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "provider_u1", Attributes: map[string]any{"user_id": "u1"}})
}

func do(h http.HandlerFunc, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func decodeRequestID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Data struct {
			RequestID string `json:"request_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return body.Data.RequestID
}

func TestAgentBoundHandlersReturn202WithRequestID(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		request *http.Request
	}{
		{
			"control",
			func(h *Handler) http.HandlerFunc { return h.ControlApp },
			httptest.NewRequest("POST", "/api/v1/app/control-app",
				strings.NewReader(`{"server_id":"s1","app_id":"a1","action":"start"}`)),
		},
		{
			"delete",
			func(h *Handler) http.HandlerFunc { return h.DeleteApp },
			httptest.NewRequest("POST", "/api/v1/app/delete-app",
				strings.NewReader(`{"server_id":"s1","app_id":"a1"}`)),
		},
		{
			"rename",
			func(h *Handler) http.HandlerFunc { return h.RenameApp },
			httptest.NewRequest("POST", "/api/v1/app/rename-app",
				strings.NewReader(`{"server_id":"s1","app_id":"a1","name":"n"}`)),
		},
		{
			"get-app",
			func(h *Handler) http.HandlerFunc { return h.GetApp },
			httptest.NewRequest("GET", "/api/v1/app/get-app?server_id=s1&app_id=a1&revision=2", nil),
		},
		{
			"get-logs",
			func(h *Handler) http.HandlerFunc { return h.GetLogs },
			httptest.NewRequest("GET", "/api/v1/app/get-logs?server_id=s1&app_id=a1&tail=50&since=10", nil),
		},
		{
			"refresh",
			func(h *Handler) http.HandlerFunc { return h.RefreshApps },
			httptest.NewRequest("POST", "/api/v1/app/refresh-apps?server_id=s1", nil),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			w := do(tc.handler(h), authed(tc.request))
			if w.Code != http.StatusAccepted {
				t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
			}
			if id := decodeRequestID(t, w); id != "req-123" {
				t.Fatalf("request_id = %q", id)
			}
			if fd.last.UserID != "u1" || fd.last.AgentID != "s1" {
				t.Fatalf("dispatched as %+v", fd.last)
			}
		})
	}
}

func TestCreateAppAccepts(t *testing.T) {
	h, fd, _ := newHandler(t)
	body := `{"server_id":"s1","app":{"name":"demo"},"config":{"name":"demo"},"files":[{"name":"compose.yml","content":"x","encrypted":false}],"variables":[]}`
	r := authed(httptest.NewRequest("POST", "/api/v1/app/create-app", strings.NewReader(body)))
	w := do(h.CreateApp, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if fd.last.AgentID != "s1" {
		t.Fatalf("dispatched to %q", fd.last.AgentID)
	}
}

func TestHandlersRejectUnauthenticated(t *testing.T) {
	h, _, _ := newHandler(t)
	r := httptest.NewRequest("POST", "/api/v1/app/control-app",
		strings.NewReader(`{"server_id":"s1","app_id":"a1","action":"start"}`))
	if w := do(h.ControlApp, r); w.Code == http.StatusAccepted {
		t.Fatalf("unauthenticated request accepted: %d", w.Code)
	}
}

func TestHandlersRejectInvalidInput(t *testing.T) {
	h, _, _ := newHandler(t)

	// Malformed body.
	r := authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{`)))
	if w := do(h.ControlApp, r); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: status %d", w.Code)
	}
	// Missing fields.
	r = authed(httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1"}`)))
	if w := do(h.DeleteApp, r); w.Code != http.StatusBadRequest {
		t.Fatalf("missing app_id: status %d", w.Code)
	}
	// Invalid control action.
	r = authed(httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"server_id":"s1","app_id":"a1","action":"explode"}`)))
	if w := do(h.ControlApp, r); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid action: status %d", w.Code)
	}
}

func TestGetAppsReturnsRepoList(t *testing.T) {
	h, _, _ := newHandler(t)
	r := authed(httptest.NewRequest("GET", "/api/v1/app/get-apps?server_id=s1", nil))
	w := do(h.GetApps, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Data struct {
			Apps []model.App `json:"apps"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Apps) != 1 || body.Data.Apps[0].Name != "grafana" {
		t.Fatalf("apps = %+v", body.Data.Apps)
	}
}

func TestGetAppsStatusReadsCache(t *testing.T) {
	h, _, cache := newHandler(t)
	cache.SetAppStatus("s1", nil, time.Now())

	r := authed(httptest.NewRequest("GET", "/api/v1/app/get-apps-status?server_id=s1", nil))
	w := do(h.GetAppsStatus, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
}

func TestGetAppsValidationMiddlewareRequiresServerID(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := GetAppsValidationMiddleware(next)

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing server_id: status %d", w.Code)
	}

	w = httptest.NewRecorder()
	mw.ServeHTTP(w, httptest.NewRequest("GET", "/x?server_id=s1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("valid request blocked: status %d", w.Code)
	}
}
