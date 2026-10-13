package templates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	tuc "winterflow/internal/domain/usecase/templates"
)

// The dispatcher drops a result that arrives after its keep time: an
// instance command must keep its result longer than a caller may wait.
func TestObservedResultOutlivesTheWaitBound(t *testing.T) {
	if appuc.ObservedResultTTL <= WaitBound {
		t.Fatalf("ObservedResultTTL %v does not outlive WaitBound %v", appuc.ObservedResultTTL, WaitBound)
	}
}

func TestAwait(t *testing.T) {
	done := make(chan port.CommandResult, 1)
	done <- port.CommandResult{Success: true}
	if res, ok := await(context.Background(), done, time.Minute); !ok || !res.Success {
		t.Fatalf("delivered = %+v, %v", res, ok)
	}
	start := time.Now()
	if _, ok := await(context.Background(), make(chan port.CommandResult), 20*time.Millisecond); ok || time.Since(start) > 5*time.Second {
		t.Fatal("the bound did not end the wait")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := await(ctx, make(chan port.CommandResult), time.Minute); ok {
		t.Fatal("a gone client did not end the wait")
	}
}

func outcome() (tuc.Outcome, chan port.CommandResult) {
	done := make(chan port.CommandResult, 1)
	final := func(context.Context) (tuc.InstanceView, error) {
		return tuc.InstanceView{Key: "k", State: "created"}, nil
	}
	return tuc.Outcome{RequestID: "req-1", Done: done, Final: final}, done
}

func TestRespond(t *testing.T) {
	h := &Handler{log: testLog, waitBound: 20 * time.Millisecond}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	answer := func(r *http.Request, out tuc.Outcome, wait bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.respond(w, r, out, wait, "dispatched")
		return w
	}

	out, _ := outcome()
	if w := answer(r, out, false); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"request_id":"req-1"`) {
		t.Fatalf("no wait = %d %s", w.Code, w.Body)
	}
	out, done := outcome()
	done <- port.CommandResult{Success: true, Payload: []byte(`{"warnings":["w1"]}`)}
	if w := answer(r, out, true); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"created"`) ||
		!strings.Contains(w.Body.String(), `"warnings":["w1"]`) || !strings.Contains(w.Body.String(), `"request_id":"req-1"`) {
		t.Fatalf("waited success = %d %s", w.Code, w.Body)
	}
	out, done = outcome()
	done <- port.CommandResult{Error: "pull failed"}
	if w := answer(r, out, true); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "pull failed") {
		t.Fatalf("waited failure = %d %s", w.Code, w.Body)
	}
	out, done = outcome()
	done <- port.CommandResult{}
	if w := answer(r, out, true); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), `"message":"the agent reported a failure"`) {
		t.Fatalf("a failure without a message = %d %s", w.Code, w.Body)
	}
	out, _ = outcome()
	if w := answer(r, out, true); w.Code != http.StatusAccepted {
		t.Fatalf("past the bound = %d %s", w.Code, w.Body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, _ = outcome()
	if w := answer(r.WithContext(ctx), out, true); w.Body.Len() != 0 {
		t.Fatalf("answered a gone client: %d %s", w.Code, w.Body)
	}
	if w := answer(r, tuc.Outcome{Instance: &tuc.InstanceView{Key: "k"}}, true); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"request_id":""`) {
		t.Fatalf("nothing dispatched = %d %s", w.Code, w.Body)
	}
}

// Instance routes need server_id (400) owned by the caller's org (403,
// and without a checker every server is refused), a key, and a canonical
// template id.
func TestInstanceRoutesFailClosed(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog})
	h.users = orgs{}
	cases := map[string]struct {
		w    *httptest.ResponseRecorder
		code int
	}{
		"get, no checker":        {serve(h.GetInstance, http.MethodGet, "/?server_id=s&key=k", ""), http.StatusForbidden},
		"get, no server":         {serve(h.GetInstance, http.MethodGet, "/?key=k", ""), http.StatusBadRequest},
		"list, no checker":       {serve(h.GetInstances, http.MethodGet, "/?server_id=s", ""), http.StatusForbidden},
		"create, no checker":     {serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s","key":"k","template_id":"x"}`), http.StatusForbidden},
		"create, malformed body": {serve(h.CreateInstance, http.MethodPost, "/", `{`), http.StatusBadRequest},
	}
	for name, tc := range cases {
		if tc.w.Code != tc.code {
			t.Errorf("%s = %d, want %d: %s", name, tc.w.Code, tc.code, tc.w.Body)
		}
	}
	h.servers = allServers{}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"create, no key":           serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s","template_id":"x"}`),
		"create, malformed id":     serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s","key":"k","template_id":"x"}`),
		"get, no key":              serve(h.GetInstance, http.MethodGet, "/?server_id=s", ""),
		"list, malformed template": serve(h.GetInstances, http.MethodGet, "/?template_id=x", ""),
	} {
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, w.Code, w.Body)
		}
	}
}

// allServers lets the caller act on any server.
type allServers struct{}

func (allServers) UserOwnsServer(context.Context, string, string) (bool, error) { return true, nil }
