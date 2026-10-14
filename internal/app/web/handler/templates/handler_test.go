package templates

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "templates-test"})

// Every route needs a user; template routes are org-scoped, instance
// routes are commands that can also wait (a 202 result and a 200 answer).
func TestRoutes(t *testing.T) {
	seen := map[string]bool{}
	for _, rt := range NewHandler(&Deps{}).Routes(api.Guards{}) {
		key := rt.Method + " " + rt.Path
		if seen[key] {
			t.Errorf("%s twice", key)
		}
		seen[key] = true
		if rt.Handler == nil || rt.Auth != api.User {
			t.Errorf("%s: want a User route with a handler", key)
		}
		switch {
		case strings.HasPrefix(rt.Path, "/api/v1/template/"):
			if rt.Tag != "templates" || rt.Accepted {
				t.Errorf("%s: want a synchronous route tagged templates", key)
			}
		case strings.HasPrefix(rt.Path, "/api/v1/instance/"):
			if rt.Tag != "instances" || (rt.Accepted && (rt.Response == nil || rt.Result == nil)) {
				t.Errorf("%s: want a route tagged instances; a command documents its 202 result and its waited 200", key)
			}
		default:
			t.Errorf("%s: outside /api/v1/template/ and /api/v1/instance/", key)
		}
	}
	for _, want := range []string{"GET /api/v1/template/get-templates", "GET /api/v1/template/get-template",
		"POST /api/v1/template/save-template", "POST /api/v1/template/delete-template"} {
		if !seen[want] {
			t.Errorf("missing %s", want)
		}
	}
}

func TestFail(t *testing.T) {
	h := &Handler{log: testLog}
	cases := []struct {
		err  error
		code int
		msg  string
	}{
		{model.ErrTemplateNotFound, http.StatusNotFound, "template not found"},
		{fmt.Errorf("wrapped: %w", model.ErrKeyInUse), http.StatusConflict, "key in use"},
		{model.ErrTemplateNameTaken, http.StatusBadRequest, "already exists"},
		{errors.New("db down"), http.StatusBadRequest, "failed to save"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		h.fail(w, tc.err, "save")
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.msg) {
			t.Errorf("%v -> %d %s", tc.err, w.Code, w.Body)
		}
	}
}

type orgs struct{ err error }

func (o orgs) PrimaryOrganizationID(context.Context, string) (string, error) { return "org-1", o.err }

func authed(r *http.Request) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "provider_u1", Attributes: map[string]any{"user_id": "u1"}})
}

func serve(fn http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	fn(w, authed(httptest.NewRequest(method, target, strings.NewReader(body))))
	return w
}

// Unwired or failing dependencies fail closed; malformed ids are 400s on
// both databases (Postgres could not even look them up).
func TestTemplateRoutesFailClosed(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog})
	if w := serve(h.ListTemplates, http.MethodGet, "/", ""); w.Code != http.StatusBadRequest {
		t.Errorf("no org resolver = %d", w.Code)
	}
	w := httptest.NewRecorder()
	h.ListTemplates(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d", w.Code)
	}
	h.users = orgs{err: errors.New("down")}
	if w := serve(h.ListTemplates, http.MethodGet, "/", ""); w.Code != http.StatusBadRequest {
		t.Errorf("org lookup error = %d", w.Code)
	}
	h.users = orgs{}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"get, malformed id":      serve(h.GetTemplate, http.MethodGet, "/?id=x", ""),
		"get, negative revision": serve(h.GetTemplate, http.MethodGet, "/?id=00000000-0000-7000-8000-000000000001&revision=-1", ""),
		"save, malformed id":     serve(h.SaveTemplate, http.MethodPost, "/", `{"id":"x","name":"a","body":{}}`),
		"delete, malformed id":   serve(h.DeleteTemplate, http.MethodPost, "/", `{"id":"x"}`),
		"delete, malformed body": serve(h.DeleteTemplate, http.MethodPost, "/", `{`),
	} {
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, w.Code, w.Body)
		}
	}
}

// A save larger than the body cap is refused before it is decoded whole.
func TestSaveTemplateCapsTheRequest(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog})
	h.users = orgs{}
	body := `{"name":"big","body":{"files":{"compose.yaml":"` + strings.Repeat("x", maxSaveBytes) + `"}}}`
	if w := serve(h.SaveTemplate, http.MethodPost, "/", body); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized save = %d %s", w.Code, w.Body)
	}
}

// memberElsewhere: the caller belongs to the server's org (ownership
// passes), but it is not the org they act for (their primary one).
type memberElsewhere struct{ err error }

func (memberElsewhere) UserOwnsServer(context.Context, string, string) (bool, error) {
	return true, nil
}
func (m memberElsewhere) ServerInOrg(context.Context, string, string) (bool, error) {
	return false, m.err
}

// An instance never sits on a server outside the org the call acts for,
// even one of another org the caller is a member of.
func TestInstanceRoutesRequireTheCallersOrgServer(t *testing.T) {
	for _, servers := range []memberElsewhere{{}, {err: errors.New("db down")}} {
		h := NewHandler(&Deps{Logger: testLog, Users: orgs{}, Servers: servers})
		if w := serve(h.GetInstance, http.MethodGet, "/?server_id=s-b&key=k", ""); w.Code != http.StatusForbidden {
			t.Errorf("get-instance on another org's server (%v) = %d %s", servers.err, w.Code, w.Body)
		}
		if w := serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s-b","key":"k","template_id":"00000000-0000-7000-8000-000000000021"}`); w.Code != http.StatusForbidden {
			t.Errorf("create-instance on another org's server (%v) = %d %s", servers.err, w.Code, w.Body)
		}
	}
}
