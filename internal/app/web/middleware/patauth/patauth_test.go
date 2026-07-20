package patauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
)

type fakeUsers struct{ valid string }

func (f *fakeUsers) FindByToken(_ context.Context, tok string) (model.User, error) {
	if tok == f.valid {
		return model.User{ID: "user-1", Name: "Alice"}, nil
	}
	return model.User{}, model.ErrInvalidToken
}

// jwtMarker stands in for the go-pkgz JWT middleware so the test can see
// whether the request fell through.
func jwtMarker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fell-Through", "jwt")
		w.WriteHeader(http.StatusUnauthorized)
	})
}

func run(t *testing.T, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	mw := Middleware(&fakeUsers{valid: "wfp_good"}, jwtMarker)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := webutil.GetUserID(r)
		if err != nil {
			t.Errorf("GetUserID: %v", err)
		}
		_, _ = w.Write([]byte(id))
	}))
	r := httptest.NewRequest("GET", "/api/v1/app/get-apps", nil)
	if authorization != "" {
		r.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestValidPATAuthenticates(t *testing.T) {
	w := run(t, "Bearer wfp_good")
	if w.Code != http.StatusOK || w.Body.String() != "user-1" {
		t.Fatalf("code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestInvalidPATIs401NotFallthrough(t *testing.T) {
	w := run(t, "Bearer wfp_bad")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if w.Header().Get("X-Fell-Through") == "jwt" {
		t.Error("invalid wfp_ token must not fall through to JWT parsing")
	}
}

func TestNonPATFallsThroughToJWT(t *testing.T) {
	for _, h := range []string{"", "Bearer eyJhbGciOi.jwt.jwt", "Basic dXNlcjp3ZnBfZ29vZA=="} {
		w := run(t, h)
		if w.Header().Get("X-Fell-Through") != "jwt" {
			t.Errorf("Authorization %q: expected fall-through to JWT middleware", h)
		}
	}
}
