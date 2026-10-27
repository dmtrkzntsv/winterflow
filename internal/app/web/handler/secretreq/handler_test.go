package secretreq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/app/web/api"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/secretreq"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "secretreq-test"})

// requests answers err, or a view of server s1.
type requests struct {
	err       error
	submitted map[string]string
}

func (q *requests) View(context.Context, string, string) (secretreq.View, error) {
	return secretreq.View{ServerID: "s1", ServerName: "homelab", Names: []string{"API_KEY"}, Notes: map[string]string{}}, q.err
}

func (q *requests) Submit(_ context.Context, _, _ string, values map[string]string) error {
	q.submitted = values
	return q.err
}

// keys publishes s1's key.
type keys struct{}

func (keys) GetCapability(_ context.Context, serverID, name string) (string, bool, error) {
	return "PUBKEY", serverID == "s1" && name == "public_key", nil
}

func authed(r *http.Request) *http.Request {
	return webutil.WithPrincipal(r, model.Principal{UserID: "u1", Kind: model.TokenSession})
}

func TestRoutes(t *testing.T) {
	limit := func(h http.Handler) http.Handler { return h }
	for _, rt := range NewHandler(&Deps{}).Routes(api.Guards{UserLimit: limit}) {
		if rt.Handler == nil || rt.Auth != api.User || rt.Tag != "secrets" || len(rt.Middleware) != 1 || rt.Middleware[0] == nil ||
			!strings.HasPrefix(rt.Path, "/api/v1/secret-request/") {
			t.Errorf("%s %s: want a User route tagged secrets behind the per-user limiter", rt.Method, rt.Path)
		}
	}
}

func TestView(t *testing.T) {
	q := &requests{}
	h := NewHandler(&Deps{Logger: testLog, Requests: q, Keys: keys{}})
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.View(w, authed(httptest.NewRequest(http.MethodGet, "/api/v1/secret-request/view"+query, nil)))
		return w
	}
	w := get("?token=wfx_x")
	var body struct {
		Data RequestView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK ||
		body.Data.PublicKey != "PUBKEY" || body.Data.ServerName != "homelab" || len(body.Data.Names) != 1 {
		t.Fatalf("view = %d %s", w.Code, w.Body)
	}
	if w := get(""); w.Code != http.StatusBadRequest {
		t.Fatalf("no token = %d", w.Code)
	}
	q.err = model.ErrNotFound
	if w := get("?token=wfx_x"); w.Code != http.StatusNotFound {
		t.Fatalf("a dead link = %d", w.Code)
	}
}

func TestSubmit(t *testing.T) {
	q := &requests{}
	h := NewHandler(&Deps{Logger: testLog, Requests: q})
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.Submit(w, authed(httptest.NewRequest(http.MethodPost, "/api/v1/secret-request/submit", strings.NewReader(body))))
		return w
	}
	if w := post(`{"token":"wfx_x","values":{"API_KEY":"ct"}}`); w.Code != http.StatusOK || q.submitted["API_KEY"] != "ct" {
		t.Fatalf("submit = %d %s", w.Code, w.Body)
	}
	if w := post(`{`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body = %d", w.Code)
	}
	if w := post(`{"values":{}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("no token = %d", w.Code)
	}
	q.err = model.Rejected("API_KEY: not an encrypted value")
	if w := post(`{"token":"wfx_x","values":{"API_KEY":"plain"}}`); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not an encrypted value") {
		t.Fatalf("plaintext = %d %s", w.Code, w.Body)
	}
	q.err = model.ErrNotFound
	if w := post(`{"token":"wfx_x","values":{}}`); w.Code != http.StatusNotFound {
		t.Fatalf("a dead link = %d", w.Code)
	}
}
