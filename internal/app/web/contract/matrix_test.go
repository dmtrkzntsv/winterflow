package contract_test

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/app/web/api"
)

// probe describes how to call one route for the cross-cutting checks.
type probe struct {
	// valid builds a well-formed call addressed at serverID; nil for
	// routes that are not server-addressed.
	valid func(serverID string) call
	// malformed is one call the route must answer 400 (bad body, missing
	// parameter). Every route with input must set it (TestProbesCoverTable).
	malformed call
}

func sq(id string) url.Values            { return url.Values{"server_id": {id}} }
func aq(id string) url.Values            { return url.Values{"server_id": {id}, "app_id": {anApp}} }
func post(path string, body any) call    { return call{Method: http.MethodPost, Path: path, Body: body} }
func get(path string, q url.Values) call { return call{Method: http.MethodGet, Path: path, Query: q} }
func bad(method, path string) call {
	if method == http.MethodGet {
		return call{Method: method, Path: path, Invalid: true}
	}
	return call{Method: method, Path: path, Body: "{", Invalid: true}
}

const anApp = "00000000-0000-0000-0000-000000000001"

// diskProbe is a valid local storage body; diskAdd adds it.
var (
	diskProbe = map[string]any{"name": "disk", "type": "local", "local": map[string]any{"path": "/mnt/usb"}}
	diskAdd   = map[string]any{"name": "disk", "type": "local", "local": map[string]any{"path": "/mnt/usb"}, "create": true}
)

// probes covers every route that takes input or is server-addressed.
// TestProbesCoverTable keeps it in step with the route table.
var probes = map[string]probe{
	"GET /api/v1/app/get-apps":        {valid: func(s string) call { return get("/api/v1/app/get-apps", sq(s)) }, malformed: bad("GET", "/api/v1/app/get-apps")},
	"GET /api/v1/app/get-apps-status": {valid: func(s string) call { return get("/api/v1/app/get-apps-status", sq(s)) }, malformed: bad("GET", "/api/v1/app/get-apps-status")},
	"POST /api/v1/app/refresh-apps": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Query: sq(s)}
	}, malformed: call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Invalid: true}},
	"POST /api/v1/app/save-app": {valid: func(s string) call {
		return post("/api/v1/app/save-app", map[string]any{"server_id": s, "app": map[string]any{"name": "x"}, "config": map[string]any{}})
	}, malformed: bad("POST", "/api/v1/app/save-app")},
	"GET /api/v1/app/get-app":       {valid: func(s string) call { return get("/api/v1/app/get-app", aq(s)) }, malformed: bad("GET", "/api/v1/app/get-app")},
	"GET /api/v1/app/get-logs":      {valid: func(s string) call { return get("/api/v1/app/get-logs", aq(s)) }, malformed: bad("GET", "/api/v1/app/get-logs")},
	"GET /api/v1/app/get-revisions": {valid: func(s string) call { return get("/api/v1/app/get-revisions", aq(s)) }, malformed: bad("GET", "/api/v1/app/get-revisions")},
	"POST /api/v1/app/rollback-app": {valid: func(s string) call {
		return post("/api/v1/app/rollback-app", map[string]any{"server_id": s, "app_id": anApp, "hash": "rev-1"})
	}, malformed: bad("POST", "/api/v1/app/rollback-app")},
	"GET /api/v1/image/get-tags": {valid: func(s string) call {
		return get("/api/v1/image/get-tags", url.Values{"server_id": {s}, "image": {"nginx"}})
	}, malformed: bad("GET", "/api/v1/image/get-tags")},
	"POST /api/v1/app/control-app": {valid: func(s string) call {
		return post("/api/v1/app/control-app", map[string]any{"server_id": s, "app_id": anApp, "action": "start"})
	}, malformed: bad("POST", "/api/v1/app/control-app")},
	"POST /api/v1/app/delete-app": {valid: func(s string) call {
		return post("/api/v1/app/delete-app", map[string]any{"server_id": s, "app_id": anApp})
	}, malformed: bad("POST", "/api/v1/app/delete-app")},
	"POST /api/v1/app/rename-app": {valid: func(s string) call {
		return post("/api/v1/app/rename-app", map[string]any{"server_id": s, "app_id": anApp, "name": "y"})
	}, malformed: bad("POST", "/api/v1/app/rename-app")},
	"POST /api/v1/app/update-backups": {valid: func(s string) call {
		return post("/api/v1/app/update-backups", map[string]any{"server_id": s, "app_id": anApp, "backups": []any{map[string]any{"name": "database"}}})
	}, malformed: bad("POST", "/api/v1/app/update-backups")},
	"GET /api/v1/app/list-restore-points": {valid: func(s string) call {
		return get("/api/v1/app/list-restore-points", url.Values{"server_id": {s}, "app_id": {anApp}, "backup": {"database"}})
	}, malformed: bad("GET", "/api/v1/app/list-restore-points")},
	"POST /api/v1/app/run-backup": {valid: func(s string) call {
		return post("/api/v1/app/run-backup", map[string]any{"server_id": s, "app_id": anApp, "backup": "database"})
	}, malformed: bad("POST", "/api/v1/app/run-backup")},
	"POST /api/v1/app/delete-restore-point": {valid: func(s string) call {
		return post("/api/v1/app/delete-restore-point", map[string]any{"server_id": s, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"})
	}, malformed: bad("POST", "/api/v1/app/delete-restore-point")},
	"POST /api/v1/app/restore-backup": {valid: func(s string) call {
		return post("/api/v1/app/restore-backup", map[string]any{"server_id": s, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"})
	}, malformed: bad("POST", "/api/v1/app/restore-backup")},
	"GET /api/v1/storage/list-orphans": {valid: func(s string) call {
		return get("/api/v1/storage/list-orphans", url.Values{"server_id": {s}, "name": {"disk"}})
	}, malformed: bad("GET", "/api/v1/storage/list-orphans")},
	"POST /api/v1/storage/delete-orphan": {valid: func(s string) call {
		return post("/api/v1/storage/delete-orphan", map[string]any{"server_id": s, "name": "disk", "app_id": "gone-app"})
	}, malformed: bad("POST", "/api/v1/storage/delete-orphan")},
	"GET /api/v1/domain/get-domains": {valid: func(s string) call { return get("/api/v1/domain/get-domains", sq(s)) }, malformed: bad("GET", "/api/v1/domain/get-domains")},
	"GET /api/v1/domain/list":        {valid: func(s string) call { return get("/api/v1/domain/list", sq(s)) }, malformed: bad("GET", "/api/v1/domain/list")},
	"POST /api/v1/domain/create": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/domain/create", Query: sq(s), Body: map[string]any{"name": "*.example.test", "tls": "off"}}
	}, malformed: bad("POST", "/api/v1/domain/create")},
	"POST /api/v1/domain/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/domain/delete", Query: sq(s), Body: map[string]any{"name": "*.example.test"}}
	}, malformed: bad("POST", "/api/v1/domain/delete")},
	"GET /api/v1/storage/get-storages": {valid: func(s string) call { return get("/api/v1/storage/get-storages", sq(s)) }, malformed: bad("GET", "/api/v1/storage/get-storages")},
	"GET /api/v1/storage/list":         {valid: func(s string) call { return get("/api/v1/storage/list", sq(s)) }, malformed: bad("GET", "/api/v1/storage/list")},
	"POST /api/v1/storage/test": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/storage/test", Query: sq(s), Body: diskProbe}
	}, malformed: bad("POST", "/api/v1/storage/test")},
	"POST /api/v1/storage/save": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/storage/save", Query: sq(s), Body: diskAdd}
	}, malformed: bad("POST", "/api/v1/storage/save")},
	"POST /api/v1/storage/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/storage/delete", Query: sq(s), Body: map[string]any{"name": "disk"}}
	}, malformed: bad("POST", "/api/v1/storage/delete")},
	"GET /api/v1/domains/check": {valid: func(s string) call {
		return get("/api/v1/domains/check", url.Values{"server_id": {s}, "domain": {"free.example.test"}})
	}, malformed: bad("GET", "/api/v1/domains/check")},

	"GET /api/v1/registry/list": {valid: func(s string) call { return get("/api/v1/registry/list", sq(s)) }, malformed: bad("GET", "/api/v1/registry/list")},
	"POST /api/v1/registry/create": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/registry/create", Query: sq(s), Body: map[string]any{"address": "ghcr.io", "username": "u"}}
	}, malformed: bad("POST", "/api/v1/registry/create")},
	"POST /api/v1/registry/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/registry/delete", Query: sq(s), Body: map[string]any{"address": "ghcr.io"}}
	}, malformed: bad("POST", "/api/v1/registry/delete")},
	"GET /api/v1/network/list": {valid: func(s string) call { return get("/api/v1/network/list", sq(s)) }, malformed: bad("GET", "/api/v1/network/list")},
	"POST /api/v1/network/create": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/network/create", Query: sq(s), Body: map[string]any{"name": "n"}}
	}, malformed: bad("POST", "/api/v1/network/create")},
	"POST /api/v1/network/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/network/delete", Query: sq(s), Body: map[string]any{"name": "n"}}
	}, malformed: bad("POST", "/api/v1/network/delete")},
	"GET /api/v1/volume/list": {valid: func(s string) call { return get("/api/v1/volume/list", sq(s)) }, malformed: bad("GET", "/api/v1/volume/list")},
	"POST /api/v1/volume/create": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/volume/create", Query: sq(s), Body: map[string]any{"name": "v"}}
	}, malformed: bad("POST", "/api/v1/volume/create")},
	"POST /api/v1/volume/delete": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/volume/delete", Query: sq(s), Body: map[string]any{"name": "v"}}
	}, malformed: bad("POST", "/api/v1/volume/delete")},
	"POST /api/v1/agent/update": {valid: func(s string) call {
		return call{Method: http.MethodPost, Path: "/api/v1/agent/update", Query: sq(s), Body: map[string]any{"version": "v1"}}
	}, malformed: bad("POST", "/api/v1/agent/update")},

	"GET /api/v1/metrics/server": {valid: func(s string) call {
		return get("/api/v1/metrics/server", url.Values{"server_id": {s}, "range": {"1h"}})
	}, malformed: bad("GET", "/api/v1/metrics/server")},
	"GET /api/v1/metrics/app": {valid: func(s string) call {
		return get("/api/v1/metrics/app", url.Values{"server_id": {s}, "app_id": {anApp}, "range": {"24h"}})
	}, malformed: bad("GET", "/api/v1/metrics/app")},

	"GET /api/v1/server/get-public-key": {valid: func(s string) call { return get("/api/v1/server/get-public-key", sq(s)) }, malformed: bad("GET", "/api/v1/server/get-public-key")},
	"POST /api/v1/server/register":      {malformed: post("/api/v1/server/register", map[string]any{"code": "1"})},

	"POST /api/v1/auth/register":             {malformed: bad("POST", "/api/v1/auth/register")},
	"POST /api/v1/user/create-token":         {malformed: post("/api/v1/user/create-token", map[string]any{"name": ""})},
	"POST /api/v1/user/delete-token":         {malformed: bad("POST", "/api/v1/user/delete-token")},
	"POST /api/v1/user/change-password":      {malformed: post("/api/v1/user/change-password", map[string]any{"current_password": "x", "new_password": "1"})},
	"POST /api/v1/org/create-user":           {malformed: post("/api/v1/org/create-user", map[string]any{"name": "n", "email": "not-an-email"})},
	"POST /api/v1/org/remove-member":         {malformed: post("/api/v1/org/remove-member", map[string]any{"user_id": ""})},
	"POST /api/v1/org/reset-member-password": {malformed: post("/api/v1/org/reset-member-password", map[string]any{"user_id": ""})},
	"POST /api/v1/org/update-organization":   {malformed: post("/api/v1/org/update-organization", map[string]any{"name": ""})},

	"POST /api/v1/org/save-shared-variable":   {malformed: bad("POST", "/api/v1/org/save-shared-variable")},
	"POST /api/v1/org/delete-shared-variable": {malformed: bad("POST", "/api/v1/org/delete-shared-variable")},
	"GET /api/v1/server/list-shared-variables": {valid: func(s string) call { return get("/api/v1/server/list-shared-variables", sq(s)) },
		malformed: bad("GET", "/api/v1/server/list-shared-variables")},
	"POST /api/v1/server/save-shared-variable": {valid: func(s string) call {
		return post("/api/v1/server/save-shared-variable", map[string]any{"server_id": s, "name": "PROBE", "value": "1"})
	}, malformed: bad("POST", "/api/v1/server/save-shared-variable")},
	// The id does not exist, so the own-server control answers 400, not 403.
	"POST /api/v1/server/delete-shared-variable": {valid: func(s string) call {
		return post("/api/v1/server/delete-shared-variable", map[string]any{"server_id": s, "id": anApp})
	}, malformed: bad("POST", "/api/v1/server/delete-shared-variable")},
	"POST /api/v1/shared-variables/resync": {valid: func(s string) call {
		return post("/api/v1/shared-variables/resync", map[string]any{"server_id": s})
	}, malformed: bad("POST", "/api/v1/shared-variables/resync")},
	"POST /api/v1/shared-variables/reload": {valid: func(s string) call {
		return post("/api/v1/shared-variables/reload", map[string]any{"server_id": s})
	}, malformed: bad("POST", "/api/v1/shared-variables/reload")},

	"GET /api/v1/template/get-template":     {malformed: bad("GET", "/api/v1/template/get-template")},
	"POST /api/v1/template/save-template":   {malformed: bad("POST", "/api/v1/template/save-template")},
	"POST /api/v1/template/delete-template": {malformed: bad("POST", "/api/v1/template/delete-template")},

	// The template and key do not exist, so the own-server control answers
	// 404, not 403.
	"POST /api/v1/instance/create-instance": {valid: func(s string) call {
		return post("/api/v1/instance/create-instance", map[string]any{"server_id": s, "key": "k", "template_id": anApp})
	}, malformed: bad("POST", "/api/v1/instance/create-instance")},
	"POST /api/v1/instance/update-instance": {valid: func(s string) call {
		return post("/api/v1/instance/update-instance", map[string]any{"server_id": s, "key": "k"})
	}, malformed: bad("POST", "/api/v1/instance/update-instance")},
	"POST /api/v1/instance/upgrade-instance": {valid: func(s string) call {
		return post("/api/v1/instance/upgrade-instance", map[string]any{"server_id": s, "key": "k", "revision": 2})
	}, malformed: bad("POST", "/api/v1/instance/upgrade-instance")},
	"GET /api/v1/instance/get-instance": {valid: func(s string) call {
		return get("/api/v1/instance/get-instance", url.Values{"server_id": {s}, "key": {"k"}})
	}, malformed: bad("GET", "/api/v1/instance/get-instance")},
	"GET /api/v1/instance/get-instances": {valid: func(s string) call { return get("/api/v1/instance/get-instances", sq(s)) },
		malformed: call{Method: http.MethodGet, Path: "/api/v1/instance/get-instances", Query: url.Values{"template_id": {"not-an-id"}}}},
}

// carriesServerID reports whether t, or a struct nested in it by embedding,
// has a field bound to server_id by a query or json tag.
func carriesServerID(t reflect.Type) bool {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if tagName(f.Tag, "query") == "server_id" || tagName(f.Tag, "json") == "server_id" {
			return true
		}
		if f.Anonymous && carriesServerID(f.Type) {
			return true
		}
	}
	return false
}

// tagName is the name part of a struct tag, without options such as
// ",omitempty".
func tagName(tag reflect.StructTag, key string) string {
	name, _, _ := strings.Cut(tag.Get(key), ",")
	return name
}

func TestCarriesServerID(t *testing.T) {
	t.Parallel()
	type embedded struct {
		ServerID string `query:"server_id"`
	}
	cases := []struct {
		name string
		v    any
		want bool
	}{
		{"json", struct {
			ID string `json:"server_id"`
		}{}, true},
		{"json omitempty", &struct {
			ID string `json:"server_id,omitempty"`
		}{}, true},
		{"query via embedding", struct{ embedded }{}, true},
		{"other field", struct {
			ID string `json:"server_ids,omitempty"`
		}{}, false},
		{"not a struct", "server_id", false},
	}
	for _, tc := range cases {
		if got := carriesServerID(reflect.TypeOf(tc.v)); got != tc.want {
			t.Errorf("%s: carriesServerID = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// serverAddressed reports whether the route takes a server_id, in its
// query or its body: the routes the org-ownership guard must protect.
func serverAddressed(rt api.Route) bool {
	for _, v := range []any{rt.Query, rt.Request} {
		if v != nil && carriesServerID(reflect.TypeOf(v)) {
			return true
		}
	}
	return false
}

// TestProbesCoverTable: every non-Mounted route with input (query or body)
// has a probe with a malformed call, every server-addressed route has a
// valid call for TestForeignServer, and every probe names a real route.
func TestProbesCoverTable(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, rt := range table {
		key := rt.Method + " " + rt.Path
		known[key] = true
		if rt.Mounted || (rt.Query == nil && rt.Request == nil) {
			continue
		}
		if p, ok := probes[key]; !ok || p.malformed.Path == "" {
			t.Errorf("%s takes input but has no malformed probe", key)
		}
		if serverAddressed(rt) && probes[key].valid == nil {
			t.Errorf("%s is server-addressed but has no valid probe, so TestForeignServer would skip its ownership check", key)
		}
	}
	for key := range probes {
		if !known[key] {
			t.Errorf("probe %s names no route", key)
		}
	}
}

// TestUnauthenticated: every User route answers 401 to an anonymous call.
func TestUnauthenticated(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	anon := e.anon()
	for _, rt := range table {
		if rt.Auth != api.User {
			continue
		}
		t.Run(rt.Method+" "+rt.Path, func(t *testing.T) {
			anon.do(t, call{Method: rt.Method, Path: rt.Path, Invalid: true}).expect(t, http.StatusUnauthorized)
		})
	}
}

// TestForeignServer: a server of another org is 403 on every
// server-addressed route, before anything reaches the agent.
func TestForeignServer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	foreign := e.foreignServer(t)
	own := owner.serverID(t)
	for key, p := range probes {
		if p.valid == nil {
			continue
		}
		t.Run(key, func(t *testing.T) {
			owner.do(t, p.valid(foreign)).expect(t, http.StatusForbidden)
		})
	}
	if n := e.agent.count(); n != 0 {
		t.Fatalf("%d command(s) reached the agent for a foreign server", n)
	}
	// Positive control: the same calls against the caller's own server are
	// not refused, so the 403s above come from ownership and not from, say,
	// a request the route rejects for another reason.
	for key, p := range probes {
		if p.valid == nil {
			continue
		}
		t.Run("own "+key, func(t *testing.T) {
			if r := owner.do(t, p.valid(own)); r.Status == http.StatusForbidden || r.Status == http.StatusUnauthorized {
				t.Fatalf("own server answered %d: %s", r.Status, r.Body)
			}
		})
	}
}

// TestMalformed: one bad body or missing parameter per route is 400.
func TestMalformed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	for key, p := range probes {
		if p.malformed.Path == "" {
			continue
		}
		t.Run(key, func(t *testing.T) {
			owner.do(t, p.malformed).expect(t, http.StatusBadRequest)
		})
	}
}
