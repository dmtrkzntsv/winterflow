package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckDomainAvailable(t *testing.T) {
	h, _, _ := newHandler(t)
	w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?server_id=s1&domain=app.example.com", nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var body struct {
		Data CheckDomainResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.Available || len(body.Data.Claims) != 0 {
		t.Fatalf("data = %+v, want available with no claims", body.Data)
	}
}

func TestCheckDomainNeedsAnOwnedServer(t *testing.T) {
	h, _, _ := newHandler(t)
	if w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?domain=app.example.com", nil))); w.Code != http.StatusBadRequest {
		t.Fatalf("missing server_id = %d, want 400", w.Code)
	}
	if w := do(h.CheckDomain, authed(httptest.NewRequest("GET", "/api/v1/domains/check?server_id=other&domain=app.example.com", nil))); w.Code != http.StatusForbidden {
		t.Fatalf("foreign server = %d, want 403", w.Code)
	}
	if w := do(h.CheckDomain, httptest.NewRequest("GET", "/api/v1/domains/check?server_id=s1&domain=app.example.com", nil)); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", w.Code)
	}
}
