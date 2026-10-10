package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

type fakeDispatcher struct {
	last port.DispatchInput
}

func (f *fakeDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	f.last = in
	return "req-9", nil
}

// fakeServerAccess owns exactly the servers in `owned` for any user.
type fakeServerAccess struct {
	owned map[string]bool
}

func (f *fakeServerAccess) UserOwnsServer(_ context.Context, _, serverID string) (bool, error) {
	return f.owned[serverID], nil
}

func newHandler(t *testing.T) (*Handler, *fakeDispatcher) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	fd := &fakeDispatcher{}
	return NewHandler(&Deps{
		Logger:            log,
		CommandDispatcher: fd,
		Servers:           &fakeServerAccess{owned: map[string]bool{"s1": true}},
	}), fd
}

func authed(r *http.Request) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "provider_u1", Attributes: map[string]any{"user_id": "u1"}})
}

func TestDockerHandlersReturn202(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		method  string
		body    string
	}{
		{"list-registries", func(h *Handler) http.HandlerFunc { return h.ListRegistries }, "GET", ""},
		{"create-registry", func(h *Handler) http.HandlerFunc { return h.CreateRegistry }, "POST",
			`{"address":"docker.io","username":"u","password":"enc"}`},
		{"delete-registry", func(h *Handler) http.HandlerFunc { return h.DeleteRegistry }, "POST",
			`{"address":"docker.io"}`},
		{"list-networks", func(h *Handler) http.HandlerFunc { return h.ListNetworks }, "GET", ""},
		{"create-network", func(h *Handler) http.HandlerFunc { return h.CreateNetwork }, "POST",
			`{"name":"net1"}`},
		{"delete-network", func(h *Handler) http.HandlerFunc { return h.DeleteNetwork }, "POST",
			`{"name":"net1"}`},
		{"list-volumes", func(h *Handler) http.HandlerFunc { return h.ListVolumes }, "GET", ""},
		{"create-volume", func(h *Handler) http.HandlerFunc { return h.CreateVolume }, "POST", `{"name":"data"}`},
		{"delete-volume", func(h *Handler) http.HandlerFunc { return h.DeleteVolume }, "POST", `{"name":"data"}`},
		{"update-agent", func(h *Handler) http.HandlerFunc { return h.UpdateAgent }, "POST",
			`{"version":"1.2.3"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fd := newHandler(t)
			r := httptest.NewRequest(tc.method, "/x?server_id=s1", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			tc.handler(h)(w, authed(r))
			if w.Code != http.StatusAccepted {
				t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
			}
			var body struct {
				Data struct {
					RequestID string `json:"request_id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.RequestID != "req-9" {
				t.Fatalf("body = %s, err %v", w.Body.String(), err)
			}
			if fd.last.UserID != "u1" || fd.last.AgentID != "s1" {
				t.Fatalf("dispatched as %+v", fd.last)
			}
		})
	}
}

// TestDockerHandlersRejectForeignServer fixates the ownership check: a
// server_id outside the caller's organizations is refused with 403 before
// anything is dispatched.
func TestDockerHandlersRejectForeignServer(t *testing.T) {
	h, fd := newHandler(t)
	r := authed(httptest.NewRequest("GET", "/x?server_id=other", nil))
	w := httptest.NewRecorder()
	h.ListRegistries(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign server must be 403, got %d", w.Code)
	}
	if fd.last.AgentID != "" {
		t.Fatalf("command must not be dispatched, got %+v", fd.last)
	}
}

func TestDockerHandlersValidate(t *testing.T) {
	h, _ := newHandler(t)

	// Missing server_id query param.
	r := authed(httptest.NewRequest("GET", "/x", nil))
	w := httptest.NewRecorder()
	h.ListRegistries(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing server_id: %d", w.Code)
	}

	// Unauthenticated.
	r = httptest.NewRequest("GET", "/x?server_id=s1", nil)
	w = httptest.NewRecorder()
	h.ListNetworks(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated must be 401, got %d", w.Code)
	}

	// Missing required body fields.
	r = authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(`{}`)))
	w = httptest.NewRecorder()
	h.CreateRegistry(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty create-registry body: %d", w.Code)
	}

	// Malformed JSON.
	r = authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(`{`)))
	w = httptest.NewRecorder()
	h.CreateNetwork(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed create-network body: %d", w.Code)
	}
}

func TestVolumeHandlersRejectForeignServer(t *testing.T) {
	for name, hf := range map[string]func(*Handler) http.HandlerFunc{
		"list":   func(h *Handler) http.HandlerFunc { return h.ListVolumes },
		"create": func(h *Handler) http.HandlerFunc { return h.CreateVolume },
		"delete": func(h *Handler) http.HandlerFunc { return h.DeleteVolume },
	} {
		t.Run(name, func(t *testing.T) {
			h, fd := newHandler(t)
			r := authed(httptest.NewRequest("POST", "/x?server_id=other", strings.NewReader(`{"name":"data"}`)))
			w := httptest.NewRecorder()
			hf(h)(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("foreign server must be 403, got %d", w.Code)
			}
			if fd.last.AgentID != "" {
				t.Fatalf("must not dispatch, got %+v", fd.last)
			}
		})
	}
}

func TestVolumeHandlersRejectBadNames(t *testing.T) {
	for _, body := range []string{`{}`, `{"name":"-rf"}`, `{"name":"../x"}`, `{"name":"a b"}`} {
		for name, hf := range map[string]func(*Handler) http.HandlerFunc{
			"create": func(h *Handler) http.HandlerFunc { return h.CreateVolume },
			"delete": func(h *Handler) http.HandlerFunc { return h.DeleteVolume },
		} {
			h, fd := newHandler(t)
			r := authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(body)))
			w := httptest.NewRecorder()
			hf(h)(w, r)
			if w.Code != http.StatusBadRequest || fd.last.AgentID != "" {
				t.Fatalf("%s %s: status %d, dispatched %+v", name, body, w.Code, fd.last)
			}
		}
	}
}

func TestVolumeHandlersDispatchTypes(t *testing.T) {
	h, fd := newHandler(t)
	r := authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(`{"name":"data"}`)))
	h.DeleteVolume(httptest.NewRecorder(), r)
	if fd.last.Type != command.TypeVolumeDelete {
		t.Fatalf("type = %q", fd.last.Type)
	}
	if p, ok := fd.last.Payload.(command.DeleteVolumeRequest); !ok || p.Name != "data" {
		t.Fatalf("payload = %#v", fd.last.Payload)
	}
}
