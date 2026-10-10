package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
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
