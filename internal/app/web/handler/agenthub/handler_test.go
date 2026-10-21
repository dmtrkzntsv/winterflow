package agenthub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/usecase/agenthub"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/pkg/agentsig"
	"winterflow/pkg/logger"
)

type fakeLocator struct {
	addr  string
	moved bool
	err   error
	got   struct {
		serverID, nonce, sig string
		ts                   int64
	}
}

func (f *fakeLocator) Where(_ context.Context, serverID string, ts int64, nonce, sig string) (string, bool, error) {
	f.got.serverID, f.got.ts, f.got.nonce, f.got.sig = serverID, ts, nonce, sig
	return f.addr, f.moved, f.err
}

func call(t *testing.T, loc *fakeLocator, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(&Deps{Logger: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error"}), Locator: loc})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/hub", strings.NewReader(body))
	if sig != "" {
		r.Header.Set(agentsig.Header, sig)
	}
	w := httptest.NewRecorder()
	h.Where(w, r)
	return w
}

const goodBody = `{"server_id":"srv-1","timestamp":1700000000,"nonce":"n1"}`

func TestWhere(t *testing.T) {
	loc := &fakeLocator{addr: "hub-02:443", moved: true}
	w := call(t, loc, goodBody, "c2ln")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var env struct {
		Data WhereResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.HubAddr != "hub-02:443" || !env.Data.Moved {
		t.Fatalf("data = %+v", env.Data)
	}
	if loc.got.serverID != "srv-1" || loc.got.ts != 1700000000 || loc.got.nonce != "n1" || loc.got.sig != "c2ln" {
		t.Fatalf("usecase got %+v", loc.got)
	}
}

func TestWhereErrors(t *testing.T) {
	long := strings.Repeat("x", maxField+1)
	for name, tc := range map[string]struct {
		body, sig string
		err       error
		want      int
	}{
		"malformed":     {body: "{", sig: "c2ln", want: http.StatusBadRequest},
		"no server":     {body: `{"timestamp":1,"nonce":"n"}`, sig: "c2ln", want: http.StatusBadRequest},
		"no nonce":      {body: `{"server_id":"s","timestamp":1}`, sig: "c2ln", want: http.StatusBadRequest},
		"long nonce":    {body: `{"server_id":"s","timestamp":1,"nonce":"` + long + `"}`, sig: "c2ln", want: http.StatusBadRequest},
		"no signature":  {body: goodBody, want: http.StatusUnauthorized},
		"bad signature": {body: goodBody, sig: "c2ln", err: agenthub.ErrUnauthorized, want: http.StatusUnauthorized},
		"stale":         {body: goodBody, sig: "c2ln", err: agenthub.ErrStaleRequest, want: http.StatusBadRequest},
		"no hub":        {body: goodBody, sig: "c2ln", err: hubs.ErrNoHub, want: http.StatusServiceUnavailable},
		"db down":       {body: goodBody, sig: "c2ln", err: errors.New("boom"), want: http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			w := call(t, &fakeLocator{err: tc.err}, tc.body, tc.sig)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body)
			}
			if tc.want == http.StatusInternalServerError && strings.Contains(w.Body.String(), "boom") {
				t.Fatalf("internal error leaked: %s", w.Body)
			}
		})
	}
}

func TestRoutesArePublicAndThrottled(t *testing.T) {
	h := NewHandler(&Deps{})
	limit := func(next http.Handler) http.Handler { return next }
	routes := h.Routes(api.Guards{EnrollLimit: limit})
	if len(routes) != 1 {
		t.Fatalf("routes = %d", len(routes))
	}
	rt := routes[0]
	if rt.Method != http.MethodPost || rt.Path != "/api/v1/agent/hub" || rt.Auth != api.Public || rt.Tag != "enroll" || len(rt.Middleware) != 1 || rt.Middleware[0] == nil {
		t.Fatalf("route = %+v", rt)
	}
}
