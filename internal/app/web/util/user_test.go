package util

import (
	"net/http/httptest"
	"testing"

	"winterflow/internal/domain/model"
)

func TestGetUserIDReadsPrincipal(t *testing.T) {
	r := WithPrincipal(httptest.NewRequest("GET", "/x", nil), model.Principal{UserID: "u1", TokenID: "t1", Kind: model.TokenSession})
	if id, err := GetUserID(r); err != nil || id != "u1" {
		t.Fatalf("GetUserID = %q, %v", id, err)
	}
	if p, ok := Principal(r); !ok || p.TokenID != "t1" {
		t.Fatalf("Principal = %+v, %v", p, ok)
	}

	// No principal: an error, never an empty id.
	if id, err := GetUserID(httptest.NewRequest("GET", "/x", nil)); err == nil {
		t.Fatalf("GetUserID without a principal = %q, want error", id)
	}
	// A principal without a user id is no identity either.
	r = WithPrincipal(httptest.NewRequest("GET", "/x", nil), model.Principal{TokenID: "t1"})
	if id, err := GetUserID(r); err == nil {
		t.Fatalf("GetUserID with an empty user id = %q, want error", id)
	}
}
