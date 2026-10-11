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
	validate := reflect.ValueOf(GetAppsValidationMiddleware).Pointer()
	// "METHOD path" -> runs GetAppsValidationMiddleware. No app route is
	// admin-only: members keep the full app lifecycle.
	want := map[string]bool{
		"GET /api/v1/app/get-apps":        true,
		"GET /api/v1/app/get-apps-status": true,
		"POST /api/v1/app/refresh-apps":   true,
		"POST /api/v1/app/save-app":       false,
		"GET /api/v1/app/get-app":         false,
		"GET /api/v1/app/get-logs":        false,
		"GET /api/v1/app/get-revisions":   false,
		"POST /api/v1/app/rollback-app":   false,
		"GET /api/v1/image/get-tags":      false,
		"POST /api/v1/app/control-app":    false,
		"POST /api/v1/app/delete-app":     false,
		"POST /api/v1/app/rename-app":     false,
		"GET /api/v1/domains/check":       false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User {
			t.Errorf("%s: want a User route with a handler", key)
		}
		if rt.Accepted != (rt.Result != nil) {
			t.Errorf("%s: Accepted=%v but Result=%T", key, rt.Accepted, rt.Result)
		}
		validated := false
		for _, mw := range rt.Middleware {
			p := reflect.ValueOf(mw).Pointer()
			if p == reflect.ValueOf(adminGuard).Pointer() {
				t.Errorf("%s: app routes must not be admin-only", key)
			}
			validated = validated || p == validate
		}
		got[key] = validated
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> validated) = %v\nwant %v", got, want)
	}
}
