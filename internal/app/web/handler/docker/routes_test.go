package docker

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
		"GET /api/v1/registry/list",
		"POST /api/v1/registry/create",
		"POST /api/v1/registry/delete",
		"GET /api/v1/network/list",
		"POST /api/v1/network/create",
		"POST /api/v1/network/delete",
		"GET /api/v1/volume/list",
		"POST /api/v1/volume/create",
		"POST /api/v1/volume/delete",
		"POST /api/v1/agent/update",
	}
	var got []string
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		got = append(got, key)
		if rt.Handler == nil || rt.Auth != api.User || !rt.Accepted || rt.Result == nil {
			t.Errorf("%s: want a User, Accepted route with a handler and a Result", key)
		}
		if _, ok := rt.Query.(api.ServerQuery); !ok {
			t.Errorf("%s: Query = %T, want api.ServerQuery", key, rt.Query)
		}
		// No role gate: every org member may call every docker route.
		if len(rt.Middleware) != 0 {
			t.Errorf("%s: %d middleware, want none", key, len(rt.Middleware))
		}
		// 403 stays documented: the server may belong to another org.
		if !slices.Contains(rt.Errors, http.StatusForbidden) {
			t.Errorf("%s: Errors = %v, want 403 for a foreign server", key, rt.Errors)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes = %v\nwant %v", got, want)
	}
}
