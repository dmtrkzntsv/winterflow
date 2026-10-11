package server

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func adminGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{Admin: adminGuard})
	want := map[string]bool{ // "METHOD path" -> behind the admin guard
		"GET /api/v1/server/get-servers":        false,
		"GET /api/v1/server/get-servers-status": false,
		"GET /api/v1/server/get-public-key":     false,
		"POST /api/v1/server/register":          true,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted {
			t.Errorf("%s: want a synchronous User route with a handler", key)
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
