package userlimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"winterflow/internal/app/web/middleware/userlimit"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

func call(h http.Handler, userID string) int {
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	if userID != "" {
		r = webutil.WithPrincipal(r, model.Principal{UserID: userID, Kind: model.TokenPAT})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestPerUserBucket(t *testing.T) {
	h := userlimit.New(0.001, 3).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	for i := 0; i < 3; i++ {
		if got := call(h, "alice"); got != http.StatusOK {
			t.Fatalf("call %d = %d, want 200", i+1, got)
		}
	}
	if got := call(h, "alice"); got != http.StatusTooManyRequests {
		t.Fatalf("4th call = %d, want 429", got)
	}
	// Another user has a bucket of their own.
	if got := call(h, "bob"); got != http.StatusOK {
		t.Fatalf("other user = %d, want 200", got)
	}
}

func TestUnauthenticatedRefused(t *testing.T) {
	h := userlimit.New(10, 30).Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("reached the handler without a principal")
	}))
	if got := call(h, ""); got != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", got)
	}
}
