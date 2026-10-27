package user

import (
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	want := []string{
		"POST /api/v1/user/create-token",
		"GET /api/v1/user/get-tokens",
		"POST /api/v1/user/delete-token",
		"GET /api/v1/user/get-profile",
		"POST /api/v1/user/change-password",
		"POST /api/v1/user/logout-all",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted || len(rt.Middleware) != 0 {
			t.Errorf("%s: want a synchronous User route with a handler and no extra middleware", key)
		}
		got = append(got, key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
