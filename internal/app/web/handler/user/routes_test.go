package user

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	limit := func(next http.Handler) http.Handler { return next }
	routes := NewHandler(&Deps{}).Routes(api.Guards{AuthLimit: limit})
	// set-password hashes without verifying: it alone is rate limited.
	limited := map[string]bool{"POST /api/v1/user/set-password": true}
	want := []string{
		"POST /api/v1/user/create-token",
		"GET /api/v1/user/get-tokens",
		"POST /api/v1/user/delete-token",
		"GET /api/v1/user/get-profile",
		"POST /api/v1/user/change-password",
		"POST /api/v1/user/set-password",
		"POST /api/v1/user/logout-all",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		wantMW := 0
		if limited[key] {
			wantMW = 1
		}
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted || len(rt.Middleware) != wantMW {
			t.Errorf("%s: want a synchronous User route with a handler and %d middleware", key, wantMW)
		}
		got = append(got, key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
