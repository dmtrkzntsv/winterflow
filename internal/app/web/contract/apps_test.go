package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/app/web/handler/server"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
)

// saveApp creates an app through the API and returns its id.
func saveApp(t *testing.T, c *client, serverID, name string, config json.RawMessage) string {
	t.Helper()
	if config == nil {
		config = json.RawMessage(`{}`)
	}
	var saved command.SaveAppResponse
	c.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: serverID,
		App:      model.App{Name: name, Icon: "box", Color: "#112233"},
		Config:   config,
		Files:    []happ.ContentItemRequest{{Name: "compose.yaml", Content: "services: {}"}},
	}}, &saved)
	if saved.AppID == "" || saved.Revision == "" {
		t.Fatalf("save result = %+v", saved)
	}
	return saved.AppID
}

// listApps is GET get-apps (the DB cache).
func listApps(t *testing.T, c *client, serverID string) []happ.AppWithDomains {
	t.Helper()
	var d happ.GetAppsResponse
	c.get(t, "/api/v1/app/get-apps", url.Values{"server_id": {serverID}}).expect(t, http.StatusOK).data(t, &d)
	return d.Apps
}

func TestAppLifecycle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	ingress := json.RawMessage(`{"ingress":{"domains":[{"domain":"web.example.test","upstream_port":8080}]}}`)

	t.Run("an uncovered hostname is refused", func(t *testing.T) {
		r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "web", Icon: "box", Color: "#112233"}, Config: ingress,
		}}).expect(t, http.StatusBadRequest)
		const want = "web.example.test is not covered by a domain registered on this server — register web.example.test or *.example.test"
		if !strings.Contains(string(r.Body), want) {
			t.Fatalf("body = %s", r.Body)
		}
		if n := len(e.agent.seen(command.TypeAppSave)); n != 0 {
			t.Fatalf("%d uncovered save(s) reached the agent", n)
		}
	})
	registerDomain(t, owner, sid, "*.example.test", command.DomainTLSAuto)

	appID := saveApp(t, owner, sid, "web", ingress)
	apps := listApps(t, owner, sid)
	if len(apps) != 1 || apps[0].ID != appID || apps[0].Name != "web" || len(apps[0].Domains) != 1 {
		t.Fatalf("after save: %+v", apps)
	}
	if got := e.agent.seen(command.TypeAppSave); len(got) != 1 || got[0].AgentID != sid {
		t.Fatalf("app.save reached the agent as %+v", got)
	}

	t.Run("domain check sees the claim", func(t *testing.T) {
		var d happ.CheckDomainResponse
		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
		if d.Available || len(d.Claims) != 1 || d.Claims[0].AppName != "web" ||
			!d.Covered || d.Registration == nil || d.Registration.Name != "*.example.test" || d.Registration.TLS != "auto" {
			t.Fatalf("check = %+v", d)
		}
		var deep happ.CheckDomainResponse
		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"a.web.example.test"}}).expect(t, http.StatusOK).data(t, &deep)
		if !deep.Available || deep.Covered || deep.Registration != nil {
			t.Fatalf("two labels under the wildcard = %+v", deep)
		}
		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"free.example.test"}}).expect(t, http.StatusOK).data(t, &d)
		if !d.Available {
			t.Fatalf("free domain = %+v", d)
		}
	})
	t.Run("the check rejects a malformed hostname", func(t *testing.T) {
		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"not a domain"}}).expect(t, http.StatusBadRequest)
	})
	t.Run("a second app cannot take the domain", func(t *testing.T) {
		owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "dup", Icon: "box", Color: "#112233"}, Config: ingress,
		}}).expect(t, http.StatusBadRequest)
	})

	q := url.Values{"server_id": {sid}, "app_id": {appID}}
	t.Run("agent reads", func(t *testing.T) {
		var got command.GetAppResponse
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-app", Query: q}, &got)
		if got.App.AppID != appID {
			t.Fatalf("get-app = %+v", got)
		}
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-revisions", Query: q}, nil)
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-logs",
			Query: url.Values{"server_id": {sid}, "app_id": {appID}, "tail": {"50"}}}, nil)
		owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/image/get-tags",
			Query: url.Values{"server_id": {sid}, "image": {"nginx"}}}, nil)
	})
	t.Run("control and rollback", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/control-app",
			Body: happ.ControlAppRequest{ServerID: sid, AppID: appID, Action: command.ControlRestart}}, nil)
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/rollback-app",
			Body: happ.RollbackAppRequest{ServerID: sid, AppID: appID, Hash: "rev-1"}}, nil)
	})
	t.Run("rename updates the DB", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/rename-app",
			Body: happ.RenameAppRequest{ServerID: sid, AppID: appID, Name: "web-2"}}, nil)
		if apps := listApps(t, owner, sid); apps[0].Name != "web-2" {
			t.Fatalf("after rename: %+v", apps)
		}
	})
	t.Run("status comes from the events queue", func(t *testing.T) {
		body, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{{AppID: appID, StatusCode: 1, Containers: []command.ContainerStatus{}}}})
		e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventAppsStatus, Payload: body})
		// The fan-out reaches the owner's stream before the next dispatch,
		// so its operation_result cannot race the status burst.
		ev := awaitEvent(t, owner, "apps_status", func(ev event) bool {
			return ev.Type == model.NotificationAppsStatus
		})
		var pushed command.AppsStatusPayload
		if err := json.Unmarshal(ev.Payload, &pushed); err != nil || pushed.ServerID != sid || len(pushed.Apps) != 1 || pushed.Apps[0].AppID != appID {
			t.Fatalf("apps_status payload = %s (%v)", ev.Payload, err)
		}
		var st command.GetAppsStatusResponse
		eventually(t, func() bool {
			owner.get(t, "/api/v1/app/get-apps-status", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &st)
			return len(st.Apps) == 1
		})
	})
	t.Run("refresh reconciles from the agent", func(t *testing.T) {
		e.agent.on(command.TypeAppsList, func(json.RawMessage) reply {
			return reply{Payload: command.ListAppsResponse{Apps: []model.App{{ID: appID, Name: "renamed-on-disk", Icon: "box", Color: "#112233"}}}}
		})
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/refresh-apps", Query: url.Values{"server_id": {sid}}}, nil)
		if apps := listApps(t, owner, sid); len(apps) != 1 || apps[0].Name != "renamed-on-disk" {
			t.Fatalf("after refresh: %+v", apps)
		}
	})
	t.Run("delete removes the app and its domains", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
			Body: happ.DeleteAppRequest{ServerID: sid, AppID: appID}}, nil)
		if apps := listApps(t, owner, sid); len(apps) != 0 {
			t.Fatalf("after delete: %+v", apps)
		}
		var d happ.CheckDomainResponse
		owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {sid}, "domain": {"web.example.test"}}).expect(t, http.StatusOK).data(t, &d)
		if !d.Available {
			t.Fatalf("domain still claimed: %+v", d)
		}
	})
}

// TestAppAgentErrors: one error-scripted case per command family. An agent
// failure is status 1 with the message and leaves the DB untouched.
func TestAppAgentErrors(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	appID := saveApp(t, owner, sid, "web", nil)

	e.agent.fail(command.TypeAppSave, "compose failed")
	owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: sid, App: model.App{Name: "broken", Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
	}}, "compose failed")
	e.agent.fail(command.TypeAppRename, "rename failed")
	owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/app/rename-app",
		Body: happ.RenameAppRequest{ServerID: sid, AppID: appID, Name: "x"}}, "rename failed")
	e.agent.fail(command.TypeAppDelete, "delete failed")
	owner.failWith(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-app",
		Body: happ.DeleteAppRequest{ServerID: sid, AppID: appID}}, "delete failed")
	e.agent.fail(command.TypeAppGet, "no such app")
	owner.failWith(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-app",
		Query: url.Values{"server_id": {sid}, "app_id": {appID}}}, "no such app")

	if apps := listApps(t, owner, sid); len(apps) != 1 || apps[0].Name != "web" {
		t.Fatalf("failed commands changed the DB: %+v", apps)
	}
}

// registerServer claims one more pending server into c's org and returns
// its id.
func (c *client) registerServer(t *testing.T) string {
	t.Helper()
	var reg server.RegisterServerResponse
	c.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: c.e.seedPendingServer(t)}).
		expect(t, http.StatusOK).data(t, &reg)
	return reg.Server.ID
}

// TestHostnameUniquePerServer: apps on different servers may serve the same
// hostname (DNS round robin); on one server it stays exclusive.
func TestHostnameUniquePerServer(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	first := owner.serverID(t)
	second := owner.registerServer(t)
	shop := json.RawMessage(`{"ingress":{"domains":[{"domain":"shop.example.test","upstream_port":8080}]}}`)
	// The same registration may exist on any number of servers.
	registerDomain(t, owner, first, "*.example.test", command.DomainTLSOff)
	registerDomain(t, owner, second, "*.example.test", command.DomainTLSAuto)

	saveApp(t, owner, first, "shop-a", shop)
	saveApp(t, owner, second, "shop-b", shop)

	var d happ.CheckDomainResponse
	owner.get(t, "/api/v1/domains/check", url.Values{"server_id": {second}, "domain": {"shop.example.test"}}).
		expect(t, http.StatusOK).data(t, &d)
	if d.Available || len(d.Claims) != 1 || d.Claims[0].AppName != "shop-b" || d.Claims[0].ServerID != second {
		t.Fatalf("claims on the second server = %+v", d)
	}
	owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: first, App: model.App{Name: "shop-c", Icon: "box", Color: "#112233"}, Config: shop,
	}}).expect(t, http.StatusBadRequest)
}
