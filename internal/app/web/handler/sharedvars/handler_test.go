package sharedvars

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	svuc "winterflow/internal/domain/usecase/sharedvars"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "sharedvars-test"})

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	want := []string{
		"GET /api/v1/org/list-shared-variables",
		"POST /api/v1/org/save-shared-variable",
		"POST /api/v1/org/delete-shared-variable",
		"GET /api/v1/org/list-encryption-keys",
		"GET /api/v1/server/list-shared-variables",
		"POST /api/v1/server/save-shared-variable",
		"POST /api/v1/server/delete-shared-variable",
		"POST /api/v1/shared-variables/resync",
		"POST /api/v1/shared-variables/reload",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		got = append(got, key)
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted || rt.Tag != "variables" {
			t.Errorf("%s: want a synchronous User route tagged variables", key)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}

type orgs struct {
	org string
	err error
}

func (o orgs) PrimaryOrganizationID(context.Context, string) (string, error) { return o.org, o.err }

func authed(r *http.Request) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "provider_u1", Attributes: map[string]any{"user_id": "u1"}})
}

func serve(h http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h(w, authed(httptest.NewRequest(method, target, strings.NewReader(body))))
	return w
}

func TestCallerOrgFailures(t *testing.T) {
	unwired := NewHandler(&Deps{Logger: testLog})
	if w := serve(unwired.ListOrg, http.MethodGet, "/", ""); w.Code != http.StatusBadRequest {
		t.Errorf("unwired org resolver = %d, want 400", w.Code)
	}
	broken := NewHandler(&Deps{Logger: testLog, Users: orgs{err: errors.New("db down")}})
	if w := serve(broken.EncryptionKeys, http.MethodGet, "/", ""); w.Code != http.StatusBadRequest {
		t.Errorf("org lookup error = %d, want 400", w.Code)
	}
	w := httptest.NewRecorder()
	broken.ListOrg(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", w.Code)
	}
}

// Without a server checker every server-addressed route fails closed.
func TestServerRoutesFailClosed(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog, Users: orgs{org: "o1"}})
	cases := []struct {
		name   string
		h      http.HandlerFunc
		method string
		target string
		body   string
		want   int
	}{
		{"list without server_id", h.ListServer, http.MethodGet, "/", "", http.StatusBadRequest},
		{"list", h.ListServer, http.MethodGet, "/?server_id=s1", "", http.StatusForbidden},
		{"save", h.SaveServer, http.MethodPost, "/", `{"server_id":"s1","name":"A","value":"1"}`, http.StatusForbidden},
		{"delete", h.DeleteServer, http.MethodPost, "/", `{"server_id":"s1","id":"v1"}`, http.StatusForbidden},
		{"resync", h.Resync, http.MethodPost, "/", `{"server_id":"s1"}`, http.StatusForbidden},
		{"reload", h.Reload, http.MethodPost, "/", `{"server_id":"s1"}`, http.StatusForbidden},
	}
	for _, tc := range cases {
		if w := serve(tc.h, tc.method, tc.target, tc.body); w.Code != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, w.Code, tc.want)
		}
	}
}

func TestFailMapsUsecaseErrors(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog})
	cases := []struct {
		err  error
		code int
		msg  string
	}{
		{&svuc.ValidationError{Msg: "bad name"}, http.StatusBadRequest, "bad name"},
		{svuc.ErrForbidden, http.StatusForbidden, "forbidden"},
		{model.ErrRolloutStopping, http.StatusConflict, "a reload is stopping"},
		{errors.New("secret detail"), http.StatusBadRequest, "failed to save shared variable"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		h.fail(w, tc.err, "save shared variable")
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.msg) || strings.Contains(w.Body.String(), "secret detail") {
			t.Errorf("%v -> %d %s", tc.err, w.Code, w.Body)
		}
	}
}
