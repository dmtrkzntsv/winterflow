package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

func TestDomainRoutesDispatch(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		request *http.Request
		typ     command.Type
	}{
		{"list", func(h *Handler) http.HandlerFunc { return h.ListRegisteredDomains },
			httptest.NewRequest("GET", "/api/v1/domain/list?server_id=s1", nil), command.TypeDomainList},
		{"create", func(h *Handler) http.HandlerFunc { return h.CreateDomain },
			httptest.NewRequest("POST", "/api/v1/domain/create?server_id=s1", strings.NewReader(`{"name":"*.x.app","tls":"off"}`)), command.TypeDomainCreate},
		{"delete", func(h *Handler) http.HandlerFunc { return h.DeleteDomain },
			httptest.NewRequest("POST", "/api/v1/domain/delete?server_id=s1", strings.NewReader(`{"name":"*.x.app"}`)), command.TypeDomainDelete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			w := do(tc.handler(h), authed(tc.request))
			if w.Code != http.StatusAccepted || decodeRequestID(t, w) != "req-123" {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if fd.last.Type != tc.typ || fd.last.AgentID != "s1" || fd.last.UserID != "u1" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
}

func TestDomainRoutesRejectBadInput(t *testing.T) {
	cases := []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		target  string
		body    string
		want    int
	}{
		{"list without server", func(h *Handler) http.HandlerFunc { return h.ListRegisteredDomains }, "/x", "", http.StatusBadRequest},
		{"list foreign server", func(h *Handler) http.HandlerFunc { return h.ListRegisteredDomains }, "/x?server_id=other", "", http.StatusForbidden},
		{"create foreign server", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=other", `{"name":"x.app","tls":"off"}`, http.StatusForbidden},
		{"create bad json", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{`, http.StatusBadRequest},
		{"create bad name", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{"name":"*.*.x.app","tls":"off"}`, http.StatusBadRequest},
		{"create bad mode", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{"name":"x.app","tls":"manual"}`, http.StatusBadRequest},
		{"create cert without key", func(h *Handler) http.HandlerFunc { return h.CreateDomain }, "/x?server_id=s1", `{"name":"x.app","tls":"certificate","certificate":"PEM"}`, http.StatusBadRequest},
		{"delete foreign server", func(h *Handler) http.HandlerFunc { return h.DeleteDomain }, "/x?server_id=other", `{"name":"x.app"}`, http.StatusForbidden},
		{"delete bad name", func(h *Handler) http.HandlerFunc { return h.DeleteDomain }, "/x?server_id=s1", `{"name":"../x"}`, http.StatusBadRequest},
		{"delete bad json", func(h *Handler) http.HandlerFunc { return h.DeleteDomain }, "/x?server_id=s1", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			r := httptest.NewRequest("POST", tc.target, strings.NewReader(tc.body))
			if w := do(tc.handler(h), authed(r)); w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			if fd.last.Type != "" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
	h, _, _ := newHandler(t)
	if w := do(h.CreateDomain, httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(`{}`))); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
}

// fakeServerDomains answers ListForServer from rows (or err).
type fakeServerDomains struct {
	rows []model.RegisteredDomain
	err  error
}

func (f fakeServerDomains) ListForServer(context.Context, string) ([]model.RegisteredDomain, error) {
	return f.rows, f.err
}
func (fakeServerDomains) Upsert(context.Context, string, model.RegisteredDomain) error { return nil }
func (fakeServerDomains) Delete(context.Context, string, string) error                 { return nil }
func (fakeServerDomains) ReplaceForServer(context.Context, string, []model.RegisteredDomain) error {
	return nil
}

func domainsHandler(sd port.ServerDomainRepository) *Handler {
	return NewHandler(&Deps{
		Logger:                 logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		CommandDispatcher:      &fakeDispatcher{},
		AppRepository:          &fakeAppRepo{},
		ServerDomainRepository: sd,
		Servers:                &fakeServerAccess{owned: map[string]bool{"s1": true}},
	})
}

func TestGetDomainsReadsTheIndex(t *testing.T) {
	h := domainsHandler(fakeServerDomains{rows: []model.RegisteredDomain{{Name: "*.x.app", TLS: model.DomainTLSOff}}})
	w := do(h.GetDomains, authed(httptest.NewRequest("GET", "/api/v1/domain/get-domains?server_id=s1", nil)))
	var body struct {
		Data GetDomainsResponse `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil ||
		len(body.Data.Domains) != 1 || body.Data.Domains[0].Name != "*.x.app" {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	for target, want := range map[string]int{
		"/x":               http.StatusBadRequest,
		"/x?server_id=bad": http.StatusForbidden,
	} {
		if w := do(h.GetDomains, authed(httptest.NewRequest("GET", target, nil))); w.Code != want {
			t.Errorf("%s = %d, want %d", target, w.Code, want)
		}
	}
	failing := domainsHandler(fakeServerDomains{err: errors.New("db down")})
	if w := do(failing.GetDomains, authed(httptest.NewRequest("GET", "/x?server_id=s1", nil))); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "failed to load domains") {
		t.Fatalf("index error = %d %s", w.Code, w.Body.String())
	}
}

// TestCheckDomainRejectsBadSyntaxBeforeAnyQuery: live typing sends partial
// names; they are refused without an ownership (or claims) query.
func TestCheckDomainRejectsBadSyntaxBeforeAnyQuery(t *testing.T) {
	access := &fakeServerAccess{owned: map[string]bool{"s1": true}}
	h := NewHandler(&Deps{
		Logger:            logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		CommandDispatcher: &fakeDispatcher{},
		AppRepository:     &fakeAppRepo{},
		Servers:           access,
	})
	w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?server_id=s1&domain=Not+A+Host", nil)))
	if w.Code != http.StatusBadRequest || access.checks != 0 {
		t.Fatalf("status %d, ownership checks %d: %s", w.Code, access.checks, w.Body.String())
	}
	failing := domainsHandler(fakeServerDomains{err: errors.New("db down")})
	if w := do(failing.CheckDomain, authed(httptest.NewRequest("GET", "/x?server_id=s1&domain=a.example.com", nil))); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "failed to check domain") {
		t.Fatalf("index error = %d %s", w.Code, w.Body.String())
	}
}
