package docker

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

// adminGuard stands in for rbac.RequireAdmin; a named func so its code
// pointer identifies it.
func adminGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{Admin: adminGuard})
	// "METHOD path" -> behind the admin guard.
	want := map[string]bool{
		"GET /api/v1/registry/list":    false,
		"POST /api/v1/registry/create": true,
		"POST /api/v1/registry/delete": true,
		"GET /api/v1/network/list":     false,
		"POST /api/v1/network/create":  true,
		"POST /api/v1/network/delete":  true,
		"GET /api/v1/volume/list":      false,
		"POST /api/v1/volume/create":   true,
		"POST /api/v1/volume/delete":   true,
		"POST /api/v1/agent/update":    true,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || !rt.Accepted || rt.Result == nil {
			t.Errorf("%s: want a User, Accepted route with a handler and a Result", key)
		}
		if _, ok := rt.Query.(api.ServerQuery); !ok {
			t.Errorf("%s: Query = %T, want api.ServerQuery", key, rt.Query)
		}
		admin := false
		for _, mw := range rt.Middleware {
			admin = admin || reflect.ValueOf(mw).Pointer() == reflect.ValueOf(adminGuard).Pointer()
		}
		got[key] = admin
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> admin) = %v\nwant %v", got, want)
	}
}
