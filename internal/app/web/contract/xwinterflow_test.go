package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

const xwfCompose = `services:
  app:
    image: nginx
    environment:
      BASE_URL: http://localhost
  db:
    image: postgres
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL: { required: true, pattern: "https?://.+" }
`

// save-app answers 400 for what x-winterflow refuses, before the agent sees
// anything; a valid save with an expose target and a pin goes through.
func TestSaveAppXWinterflow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	registerDomain(t, owner, sid, "*.example.test", model.DomainTLSAuto)
	save := func(config string, vars ...happ.ContentItemRequest) *response {
		return owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "notes", Icon: "box", Color: "#112233"}, Config: json.RawMessage(config),
			Files:     []happ.ContentItemRequest{{Name: "compose.yml", Content: xwfCompose}},
			Variables: vars,
		}})
	}
	ok := happ.ContentItemRequest{Name: "BASE_URL", Content: "https://notes.example.test"}
	for want, r := range map[string]*response{
		"variable BASE_URL: a value is required":                               save(`{}`),
		`variable BASE_URL: \"notes\" does not match`:                          save(`{}`, happ.ContentItemRequest{Name: "BASE_URL", Content: "notes"}),
		"variable BASE_URL: a plain value can't contain $":                     save(`{}`, happ.ContentItemRequest{Name: "BASE_URL", Content: "https://x/$y"}),
		"db:5432 is not an http expose entry of this app":                      save(`{"ingress":{"domains":[{"domain":"db.example.test","expose":"db:5432","ssl":true}],"redirects":[]}}`, ok),
		"domain \\\"a.example.test\\\": set expose or upstream_port, not both": save(`{"ingress":{"domains":[{"domain":"a.example.test","expose":"app:8080","upstream_port":80,"ssl":true}],"redirects":[]}}`, ok),
	} {
		r.expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), want) {
			t.Errorf("want %q in %s", want, r.Body)
		}
	}
	if n := len(e.agent.seen(command.TypeAppSave)); n != 0 {
		t.Fatalf("%d refused save(s) reached the agent", n)
	}
	var saved command.SaveAppResponse
	// A pin of an entry the compose file no longer has (web:80/http) is
	// dropped by the agent, never a 400.
	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: sid, App: model.App{Name: "notes", Icon: "box", Color: "#112233"},
		Config:    json.RawMessage(`{"ingress":{"domains":[{"domain":"notes.example.test","expose":"app:8080","ssl":true}],"redirects":[]},"ports":{"db:5432/tcp":{"host":5432,"pinned":true},"web:80/http":{"host":5432,"pinned":true}}}`),
		Files:     []happ.ContentItemRequest{{Name: "compose.yml", Content: xwfCompose}},
		Variables: []happ.ContentItemRequest{ok},
	}}, &saved)
	if apps := listApps(t, owner, sid); len(apps) != 1 || len(apps[0].Domains) != 1 || apps[0].Domains[0].Domain != "notes.example.test" {
		t.Fatalf("apps = %+v", apps)
	}
}

// get-app carries the declared variables and the expose entries with their
// host ports (validated against api/openapi.yaml like every answer).
func TestGetAppXWinterflow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	appID := saveApp(t, owner, sid, "notes", nil)
	def := "http://localhost"
	e.agent.on(command.TypeAppGet, func(raw json.RawMessage) reply {
		in := decode[command.GetAppRequest](raw)
		return reply{Payload: command.GetAppResponse{
			App: command.AppPayload{AppID: in.AppID, Config: []byte(`{}`), Variables: []command.ContentItem{}, Files: []command.ContentItem{}},
			DeclaredVariables: []xwinterflow.DeclaredVariable{
				{Name: "BASE_URL", Required: true, Pattern: "https?://.+", Default: &def, Services: []string{"app"}},
				{Name: "TOKEN", Encrypted: true, Services: []string{}},
			},
			Expose: []command.ExposedPort{
				{ID: "app:8080/http", Container: "app", Port: 8080, Protocol: "http", HostPort: 20000, Bind: "127.0.0.1"},
				{ID: "db:5432/tcp", Container: "db", Port: 5432, Protocol: "tcp", HostPort: 5432, Bind: "0.0.0.0", Pinned: true},
			},
			XWinterflowWarnings: []string{"x-winterflow.variables.TOKEN: no service environment and no override uses it"},
		}}
	})
	var got command.GetAppResponse
	owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-app", Query: url.Values{"server_id": {sid}, "app_id": {appID}}}, &got)
	if len(got.DeclaredVariables) != 2 || got.DeclaredVariables[1].Default != nil || len(got.Expose) != 2 || got.Expose[1].HostPort != 5432 {
		t.Fatalf("get-app = %+v", got)
	}
}
