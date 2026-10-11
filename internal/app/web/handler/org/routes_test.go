package org

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	want := []string{
		"POST /api/v1/org/create-user",
		"GET /api/v1/org/get-members",
		"POST /api/v1/org/remove-member",
		"POST /api/v1/org/reset-member-password",
		"GET /api/v1/org/get-organization",
		"POST /api/v1/org/update-organization",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		got = append(got, key)
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted {
			t.Errorf("%s: want a synchronous User route with a handler", key)
		}
		// No role gate, so nothing answers 403: every member administers.
		if len(rt.Middleware) != 0 || slices.Contains(rt.Errors, http.StatusForbidden) {
			t.Errorf("%s: middleware %d, errors %v; want none and no 403", key, len(rt.Middleware), rt.Errors)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
