package server

import (
	"crypto/elliptic"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/dto"
	"winterflow/internal/domain/usecase/enrollment"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	"winterflow/internal/domain/usecase/hubs"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	enrollmentrepo "winterflow/internal/infra/db/repository/enrollment"
	serverrepo "winterflow/internal/infra/db/repository/server"
	userrepo "winterflow/internal/infra/db/repository/user"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

type enrollEnv struct {
	h      *Handler
	uc     *enrollment.UseCase
	repo   *enrollmentrepo.Repo
	userID string
	picker *enrolltest.Picker
	// regCode is a pending registration (standalone's path).
	regCode string
}

func newEnrollEnv(t *testing.T) *enrollEnv {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn, err := db.Open(log, "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	users, err := userrepo.New(conn.Tx, conn.Driver, log)
	if err != nil {
		t.Fatal(err)
	}
	servers, err := serverrepo.New(conn.Tx, conn.Driver, log)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := enrollmentrepo.New(conn.Tx, conn.Driver)
	if err != nil {
		t.Fatal(err)
	}
	ctx := dbtest.Ctx(t)
	u, err := users.FindOrCreateUser(ctx, dto.UserDTO{Name: "Alice", Provider: "google", AccountID: "g1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := servers.RegisterServer(ctx, dto.ServerRegistrationDTO{
		ServerID: "srv-r", CertificateID: "cert-r", Hostname: "box", Code: "REGREG",
		ExpiresAt: time.Now().Add(time.Hour), Certificate: []byte("PEM"), CertificateExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	picker := &enrolltest.Picker{Hub: enrolltest.DefaultHub}
	uc := enrollment.New(repo, enrolltest.Signer(t), picker, nil)
	h := NewHandler(&Deps{
		Logger: log, ServerRepository: servers, UserService: users, StatusCache: nil,
		Cfg: config.NewServerConfig("distributed"), Enrollment: uc,
	})
	return &enrollEnv{h: h, uc: uc, repo: repo, userID: u.ID, picker: picker, regCode: "REGREG"}
}

func (e *enrollEnv) register(t *testing.T, code string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/v1/server/register", strings.NewReader(`{"code":"`+code+`"}`))
	w := httptest.NewRecorder()
	e.h.Register(w, authedAs(t, r, e.userID))
	return w
}

func TestRegisterClaimsEnrollment(t *testing.T) {
	e := newEnrollEnv(t)
	ctx := dbtest.Ctx(t)
	res, err := e.uc.Start(ctx, enrolltest.CSR(t, elliptic.P256()), "box", "203.0.113.1")
	if err != nil {
		t.Fatal(err)
	}
	w := e.register(t, res.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("register = %d %s", w.Code, w.Body)
	}
	var body struct {
		Data RegisterServerResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Data.Server.ID == "" {
		t.Fatalf("body %s (%v)", w.Body, err)
	}
	st, err := e.uc.Status(ctx, res.PollSecret)
	if err != nil || st.State != "claimed" || st.ServerID != body.Data.Server.ID || st.Certificate == "" || st.HubAddr != enrolltest.DefaultHub.PublicAddr {
		t.Fatalf("status = %+v (%v)", st, err)
	}
}

func TestRegisterFallsBackToRegistration(t *testing.T) {
	e := newEnrollEnv(t)
	if w := e.register(t, e.regCode); w.Code != http.StatusOK {
		t.Fatalf("register = %d %s", w.Code, w.Body)
	}
	if w := e.register(t, "ZZZZZZ"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown code = %d %s", w.Code, w.Body)
	}
}

func TestRegisterNoHub503(t *testing.T) {
	e := newEnrollEnv(t)
	res, err := e.uc.Start(dbtest.Ctx(t), enrolltest.CSR(t, elliptic.P256()), "box", "203.0.113.1")
	if err != nil {
		t.Fatal(err)
	}
	e.picker.Err = hubs.ErrNoHub
	w := e.register(t, res.Code)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "no hub is available, try again in a minute") {
		t.Fatalf("register = %d %s", w.Code, w.Body)
	}
	// The code stays valid: the next try, with a hub, claims it.
	e.picker.Err = nil
	if w := e.register(t, res.Code); w.Code != http.StatusOK {
		t.Fatalf("retry = %d %s", w.Code, w.Body)
	}
}

func TestRegisterRateLimitedPerUser(t *testing.T) {
	e := newEnrollEnv(t)
	for i := 0; i < 5; i++ {
		if w := e.register(t, "ZZZZZZ"); w.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d = %d", i+1, w.Code)
		}
	}
	if w := e.register(t, "ZZZZZZ"); w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "too many attempts, wait a minute") {
		t.Fatalf("6th attempt = %d %s", w.Code, w.Body)
	}
}

func TestRegisterExpiredEnrollmentCodeDoesNotFallBack(t *testing.T) {
	e := newEnrollEnv(t)
	past := time.Now().Add(-time.Hour)
	uc := enrollment.New(e.repo, enrolltest.Signer(t), e.picker, func() time.Time { return past })
	res, err := uc.Start(dbtest.Ctx(t), enrolltest.CSR(t, elliptic.P256()), "box", "203.0.113.1")
	if err != nil {
		t.Fatal(err)
	}
	w := e.register(t, res.Code)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "registration code has expired") {
		t.Fatalf("register = %d %s", w.Code, w.Body)
	}
}
