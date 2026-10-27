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
	type shape struct {
		auth    api.Auth
		limited bool // behind the auth rate limiter
	}
	want := map[string]shape{
		"POST /api/v1/auth/login":    {api.Public, true},
		"POST /api/v1/auth/logout":   {api.User, false},
		"POST /api/v1/auth/register": {api.Public, true},
		"GET /api/v1/auth/state":     {api.Public, false},
	}
	got := map[string]shape{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil {
			t.Errorf("%s: no handler", key)
		}
		limited := false
		for _, mw := range rt.Middleware {
			limited = limited || reflect.ValueOf(mw).Pointer() == reflect.ValueOf(limitGuard).Pointer()
		}
		got[key] = shape{rt.Auth, limited}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
