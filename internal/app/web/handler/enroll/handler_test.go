package enroll

import (
	"crypto/elliptic"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/usecase/enrollment"
	"winterflow/internal/domain/usecase/enrollment/enrolltest"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	enrollmentrepo "winterflow/internal/infra/db/repository/enrollment"
	"winterflow/pkg/logger"
)

func newHandler(t *testing.T) *Handler {
	t.Helper()
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	conn, err := db.Open(log, "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Shutdown() })
	repo, err := enrollmentrepo.New(conn.Tx, conn.Driver)
	if err != nil {
		t.Fatal(err)
	}
	uc := enrollment.New(repo, enrolltest.Signer(t), enrolltest.Picker{Hub: enrolltest.DefaultHub}, nil)
	return NewHandler(&Deps{Logger: log, Enrollments: uc})
}

func call(t *testing.T, fn http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/", strings.NewReader(string(b))).WithContext(dbtest.Ctx(t))
	w := httptest.NewRecorder()
	fn(w, r)
	return w
}

func data[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var env struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

func start(t *testing.T, h *Handler) CodeResponse {
	t.Helper()
	w := call(t, h.Start, StartRequest{CSR: enrolltest.CSR(t, elliptic.P256()), Hostname: "box"})
	if w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
	return data[CodeResponse](t, w)
}

func TestEnrollStartOK(t *testing.T) {
	h := newHandler(t)
	res := start(t, h)
	if len(res.Code) != 6 || res.PollSecret == "" || res.EnrollmentID == "" || res.CodesLeft != 9 || res.ExpiresAt.IsZero() {
		t.Fatalf("unexpected %+v", res)
	}
	w := call(t, h.Status, PollRequest{PollSecret: res.PollSecret})
	if st := data[StatusResponse](t, w); w.Code != 200 || st.State != "pending" || st.Code != res.Code {
		t.Fatalf("status = %d %+v", w.Code, st)
	}
}

func TestEnrollBadCSR400(t *testing.T) {
	h := newHandler(t)
	for _, c := range []string{"garbage", enrolltest.CSR(t, elliptic.P384())} {
		if w := call(t, h.Start, StartRequest{CSR: c}); w.Code != http.StatusBadRequest {
			t.Fatalf("csr %.20q: %d", c, w.Code)
		}
	}
	long := StartRequest{CSR: enrolltest.CSR(t, elliptic.P256()), Hostname: strings.Repeat("a", 254)}
	if w := call(t, h.Start, long); w.Code != http.StatusBadRequest {
		t.Fatalf("long hostname: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"csr":"`+strings.Repeat("x", 70<<10)+`"}`))
	w := httptest.NewRecorder()
	h.Start(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: %d", w.Code)
	}
}

func TestEnrollStatusUnknownSecret400(t *testing.T) {
	h := newHandler(t)
	for name, fn := range map[string]http.HandlerFunc{"status": h.Status, "refresh": h.Refresh} {
		if w := call(t, fn, PollRequest{PollSecret: "nope"}); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
}

func TestRefreshNoCodesLeft409(t *testing.T) {
	h := newHandler(t)
	res := start(t, h)
	for i := 0; i < enrollment.MaxCodes-1; i++ {
		if w := call(t, h.Refresh, PollRequest{PollSecret: res.PollSecret}); w.Code != http.StatusOK {
			t.Fatalf("refresh %d: %d %s", i, w.Code, w.Body)
		}
	}
	if w := call(t, h.Refresh, PollRequest{PollSecret: res.PollSecret}); w.Code != http.StatusConflict {
		t.Fatalf("last refresh: %d %s", w.Code, w.Body)
	}
}
