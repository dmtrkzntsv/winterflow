package rollouts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "rollouts-test"})

const anID = "00000000-0000-7000-8000-000000000101"

type orgs struct{}

func (orgs) PrimaryOrganizationID(context.Context, string) (string, error) { return "org-1", nil }

// queue records what the routes asked and answers err.
type queue struct {
	err    error
	called string
}

func (q *queue) List(_ context.Context, orgID, kind, scopeKey string) ([]model.RolloutView, error) {
	q.called = fmt.Sprintf("list %s %s %s", orgID, kind, scopeKey)
	return []model.RolloutView{{ID: anID, Kind: kind}}, q.err
}

func (q *queue) Stop(_ context.Context, orgID, id string) (model.RolloutView, error) {
	q.called = fmt.Sprintf("stop %s %s", orgID, id)
	return model.RolloutView{ID: id, StopRequested: true}, q.err
}

func authed(r *http.Request) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "provider_u1", Attributes: map[string]any{"user_id": "u1"}})
}

func TestRoutes(t *testing.T) {
	for _, rt := range NewHandler(&Deps{}).Routes(api.Guards{}) {
		if rt.Handler == nil || rt.Auth != api.User || rt.Tag != "rollouts" || rt.Accepted || !strings.HasPrefix(rt.Path, "/api/v1/rollout/") {
			t.Errorf("%s %s: want a synchronous User route tagged rollouts", rt.Method, rt.Path)
		}
	}
}

func TestGetRollouts(t *testing.T) {
	q := &queue{}
	h := NewHandler(&Deps{Logger: testLog, Users: orgs{}, Rollouts: q})
	get := func(query url.Values) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.GetRollouts(w, authed(httptest.NewRequest(http.MethodGet, "/api/v1/rollout/get-rollouts?"+query.Encode(), nil)))
		return w
	}
	if w := get(url.Values{"kind": {"vars_reload"}}); w.Code != http.StatusOK || q.called != "list org-1 vars_reload " {
		t.Fatalf("list = %d %s (%s)", w.Code, w.Body, q.called)
	}
	if w := get(url.Values{}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "kind is required") {
		t.Fatalf("no kind = %d %s", w.Code, w.Body)
	}
	if w := get(url.Values{"kind": {"template_upgrade"}, "scope_key": {"x"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("a scope key that is no id = %d", w.Code)
	}
	q.err = model.ErrTemplateNotFound
	if w := get(url.Values{"kind": {"template_upgrade"}, "scope_key": {anID}}); w.Code != http.StatusNotFound {
		t.Fatalf("another org's template = %d", w.Code)
	}
	q.err = errors.New("db down")
	if w := get(url.Values{"kind": {"vars_reload"}}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "failed to list rollouts") {
		t.Fatalf("db error = %d %s", w.Code, w.Body)
	}
}

func TestStopRollout(t *testing.T) {
	q := &queue{}
	h := NewHandler(&Deps{Logger: testLog, Users: orgs{}, Rollouts: q})
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.StopRollout(w, authed(httptest.NewRequest(http.MethodPost, "/api/v1/rollout/stop-rollout", strings.NewReader(body))))
		return w
	}
	if w := post(`{"id":"` + anID + `"}`); w.Code != http.StatusOK || q.called != "stop org-1 "+anID {
		t.Fatalf("stop = %d %s (%s)", w.Code, w.Body, q.called)
	}
	if w := post(`{"id":"nope"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a bad id = %d", w.Code)
	}
	q.err = model.ErrRolloutNotFound
	if w := post(`{"id":"` + anID + `"}`); w.Code != http.StatusNotFound {
		t.Fatalf("another org's rollout = %d", w.Code)
	}
}
