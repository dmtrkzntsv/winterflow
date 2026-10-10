package auth

import (
	"net/http"
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func limitGuard(next http.Handler) http.Handler { return next }

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{AuthLimit: limitGuard})
	want := map[string]bool{ // "METHOD path" -> behind the auth rate limiter
		"POST /api/v1/auth/register": true,
		"GET /api/v1/auth/state":     false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.Public {
			t.Errorf("%s: want a Public route with a handler", key)
		}
		limited := false
		for _, mw := range rt.Middleware {
			limited = limited || reflect.ValueOf(mw).Pointer() == reflect.ValueOf(limitGuard).Pointer()
		}
		got[key] = limited
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> limited) = %v\nwant %v", got, want)
	}
}
