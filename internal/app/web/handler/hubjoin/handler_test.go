package hubjoin

import (
	"context"
	"crypto/elliptic"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	"winterflow/internal/domain/usecase/hubjoin"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/hubcert"
	"winterflow/internal/infra/db/repository/hubtoken"
	"winterflow/pkg/logger"
)

// noHubs is an empty hub list: every hub id is free.
type noHubs struct{}

func (noHubs) List(context.Context) ([]model.Hub, error) { return nil, nil }

func newHandler(t *testing.T) (*Handler, *hubjoin.UseCase) {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn, err := db.Open(log, "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	repo, err := hubtoken.New(conn.Tx, conn.Driver)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := hubcert.New(conn.Tx, conn.Driver)
	if err != nil {
		t.Fatal(err)
	}
	uc := hubjoin.New(repo, certs, enrolltest.Signer(t), noHubs{}, nil)
	return NewHandler(&Deps{Logger: log, Joiner: uc}), uc
}

func call(t *testing.T, h *Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/", strings.NewReader(string(b))).WithContext(dbtest.Ctx(t))
	w := httptest.NewRecorder()
	h.Enroll(w, r)
	return w
}

func TestEnroll(t *testing.T) {
	h, uc := newHandler(t)
	token, _, err := uc.Issue(dbtest.Ctx(t), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	csr := enrolltest.CSR(t, elliptic.P256())
	w := call(t, h, EnrollRequest{Token: token, CSR: csr, PublicAddr: "hub-02.example.com:443"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var env struct {
		Data EnrollResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.Data.Certificate, "BEGIN CERTIFICATE") || !strings.Contains(env.Data.CACertificate, "BEGIN CERTIFICATE") {
		t.Fatalf("data = %+v", env.Data)
	}

	// The token is single use: the same request is now a 400.
	if w := call(t, h, EnrollRequest{Token: token, CSR: csr, PublicAddr: "hub-02.example.com:443"}); w.Code != http.StatusBadRequest {
		t.Fatalf("replay = %d", w.Code)
	}
}

func TestEnrollBadRequests(t *testing.T) {
	h, uc := newHandler(t)
	token, _, _ := uc.Issue(dbtest.Ctx(t), time.Hour)
	csr := enrolltest.CSR(t, elliptic.P256())
	for name, req := range map[string]EnrollRequest{
		"unknown token": {Token: "wfh_nope", CSR: csr, PublicAddr: "h.example.com:443"},
		"bad csr":       {Token: token, CSR: "garbage", PublicAddr: "h.example.com:443"},
		"bad address":   {Token: token, CSR: csr, PublicAddr: "no-port"},
		"empty":         {},
	} {
		if w := call(t, h, req); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d: %s", name, w.Code, w.Body)
		}
	}
	// Malformed JSON.
	r := httptest.NewRequest("POST", "/", strings.NewReader("{")).WithContext(dbtest.Ctx(t))
	w := httptest.NewRecorder()
	h.Enroll(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed = %d", w.Code)
	}
}

func TestRouteIsPublicAndTightlyLimited(t *testing.T) {
	h, _ := newHandler(t)
	var hits []string
	guard := func(name string) api.Middleware {
		return func(next http.Handler) http.Handler { hits = append(hits, name); return next }
	}
	routes := h.Routes(api.Guards{AuthLimit: guard("auth"), EnrollLimit: guard("enroll"), EnrollStartLimit: guard("start")})
	if len(routes) != 1 || routes[0].Auth != api.Public || routes[0].Path != "/api/v1/hub/enroll" {
		t.Fatalf("routes = %+v", routes)
	}
	for _, m := range routes[0].Middleware {
		m(http.NotFoundHandler())
	}
	if len(hits) != 1 || hits[0] != "start" {
		t.Fatalf("guards = %v", hits)
	}
}
