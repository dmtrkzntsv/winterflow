package util

import (
	"net/http/httptest"
	"testing"

	"github.com/go-pkgz/auth/v2/token"
)

func TestGetUserIDRequiresAttr(t *testing.T) {
	// Attribute present: resolved.
	u := token.User{ID: "google_abc"}
	u.SetStrAttr("user_id", "db-uuid-1")
	r := token.SetUserInfo(httptest.NewRequest("GET", "/x", nil), u)
	if id, err := GetUserID(r); err != nil || id != "db-uuid-1" {
		t.Fatalf("GetUserID = %q, %v", id, err)
	}

	// Attribute absent: must error — NOT fall back to the provider id.
	// (An unresolved identity, e.g. Google login with registration closed,
	// would otherwise leak a ghost id into every handler.)
	r2 := token.SetUserInfo(httptest.NewRequest("GET", "/x", nil), token.User{ID: "google_abc"})
	if id, err := GetUserID(r2); err == nil {
		t.Fatalf("GetUserID without attr = %q, want error", id)
	}
}
