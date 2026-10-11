package server

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"winterflow/internal/app/web/api"
)

func TestRoutes(t *testing.T) {
	routes := NewHandler(&Deps{}).Routes(api.Guards{})
	// "METHOD path" -> documents 403 (server_id of another org).
	want := map[string]bool{
		"GET /api/v1/server/get-servers":        false,
		"GET /api/v1/server/get-servers-status": false,
		"GET /api/v1/server/get-public-key":     true,
		"POST /api/v1/server/register":          false,
	}
	got := map[string]bool{}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		if rt.Handler == nil || rt.Auth != api.User || rt.Accepted {
			t.Errorf("%s: want a synchronous User route with a handler", key)
		}
		// No role gate: any org member may register a server.
		if len(rt.Middleware) != 0 {
			t.Errorf("%s: %d middleware, want none", key, len(rt.Middleware))
		}
		got[key] = slices.Contains(rt.Errors, http.StatusForbidden)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("routes (path -> 403) = %v\nwant %v", got, want)
	}
}
