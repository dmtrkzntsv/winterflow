package contract_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/go-pkgz/auth/v2/avatar"

	"winterflow/pkg/util"
)

func TestAuthClaimFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	anon := e.anon()

	var st struct {
		Bootstrap           bool `json:"bootstrap"`
		RegistrationEnabled bool `json:"registration_enabled"`
	}
	anon.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if !st.Bootstrap || !st.RegistrationEnabled {
		t.Fatalf("fresh instance state = %+v, want bootstrap + open", st)
	}

	t.Run("register rejects a short password", func(t *testing.T) {
		anon.post(t, "/api/v1/auth/register", map[string]string{
			"name": "X", "email": "x@example.test", "password": "1",
		}).expect(t, http.StatusBadRequest)
	})

	owner := e.claim(t)

	anon.get(t, "/api/v1/auth/state", nil).expect(t, http.StatusOK).data(t, &st)
	if st.Bootstrap || st.RegistrationEnabled {
		t.Fatalf("claimed standalone state = %+v, want closed", st)
	}
	t.Run("standalone registration closes after the claim", func(t *testing.T) {
		env := anon.post(t, "/api/v1/auth/register", map[string]string{
			"name": "Late", "email": "late@example.test", "password": "password",
		}).expect(t, http.StatusBadRequest).data(t, nil)
		if env.Message != "registration is disabled" {
			t.Fatalf("message = %q", env.Message)
		}
	})
	t.Run("login with a wrong password is 403", func(t *testing.T) {
		e.anon().do(t, call{Method: http.MethodPost, Path: "/auth/local/login", Body: map[string]string{
			"user": owner.email, "passwd": "wrong", "aud": "contract",
		}}).expect(t, http.StatusForbidden)
	})
	t.Run("session endpoints", func(t *testing.T) {
		var status struct {
			Status string `json:"status"`
		}
		r := owner.get(t, "/auth/status", nil).expect(t, http.StatusOK)
		if err := json.Unmarshal(r.Body, &status); err != nil || status.Status != "logged in" {
			t.Fatalf("status = %s", r.Body)
		}
		owner.get(t, "/auth/user", nil).expect(t, http.StatusOK)
		anon.get(t, "/auth/user", nil).expect(t, http.StatusUnauthorized)
		owner.get(t, "/auth/list", nil).expect(t, http.StatusOK)
		anon.get(t, "/avatar/missing.image", nil).expect(t, http.StatusForbidden)
	})
	t.Run("logout clears the session", func(t *testing.T) {
		owner.get(t, "/auth/logout", nil).expect(t, http.StatusOK)
		owner.get(t, "/api/v1/user/get-profile", nil).expect(t, http.StatusUnauthorized)
	})
}

// TestAvatar fetches a stored avatar through GET /avatar/{avatar}. The
// store is the one the server reads (AVATARS_STORAGE_PATH, shared by every
// test), so the avatar is put under a fresh id.
func TestAvatar(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	img := []byte{0x00, 0x01, 0x02, 0x03, 0xfe, 0xff} // sniffs as octet-stream: served as image/*
	id, err := avatar.NewLocalFS(os.Getenv("AVATARS_STORAGE_PATH")).Put(util.GenerateID(), bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	r := e.anon().get(t, "/avatar/"+id, nil).expect(t, http.StatusOK)
	if !bytes.Equal(r.Body, img) || r.Header.Get("Content-Type") != "image/*" {
		t.Fatalf("avatar = %q (%s), want %q", r.Body, r.Header.Get("Content-Type"), img)
	}
}
