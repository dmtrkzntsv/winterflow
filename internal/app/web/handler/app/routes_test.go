package server

import (
	"reflect"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	validate := reflect.ValueOf(GetAppsValidationMiddleware).Pointer()
	// "METHOD path" -> runs GetAppsValidationMiddleware.
	want := map[string]bool{
		"GET /api/v1/app/get-apps":         true,
		"GET /api/v1/app/get-apps-status":  true,
		"POST /api/v1/app/refresh-apps":    true,
		"POST /api/v1/app/save-app":        false,
		"GET /api/v1/app/get-app":          false,
		"GET /api/v1/app/get-logs":         false,
		"GET /api/v1/app/get-revisions":    false,
		"POST /api/v1/app/rollback-app":    false,
		"GET /api/v1/image/get-tags":       false,
		"POST /api/v1/app/control-app":     false,
		"POST /api/v1/app/delete-app":      false,
		"POST /api/v1/app/rename-app":      false,
		"GET /api/v1/domain/get-domains":   false,
		"GET /api/v1/domain/list":          false,
		"POST /api/v1/domain/create":       false,
		"POST /api/v1/domain/delete":       false,
		"GET /api/v1/domains/check":        false,
		"GET /api/v1/storage/get-storages": false,
		"GET /api/v1/storage/list":         false,
		"POST /api/v1/storage/test":        false,
		"POST /api/v1/storage/save":        false,
		"POST /api/v1/storage/delete":      false,
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
			validated = validated || reflect.ValueOf(mw).Pointer() == validate
		}
		got[key] = validated
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> validated) = %v\nwant %v", got, want)
	}
}
