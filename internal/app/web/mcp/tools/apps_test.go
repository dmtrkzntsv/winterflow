package tools_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/transport/codec"
	"winterflow/pkg/util"
)

// capture records the payload of every command of the given types the
// agent receives, answering each with answer (an empty object when nil).
type capture struct {
	mu   sync.Mutex
	got  map[command.Type]json.RawMessage
	seen int
}

func (e *env) capture(answer map[command.Type]any, types ...command.Type) *capture {
	c := &capture{got: map[command.Type]json.RawMessage{}}
	for _, typ := range types {
		typ := typ
		e.agent.on(typ, func(raw json.RawMessage) reply {
			c.mu.Lock()
			c.got[typ] = append(json.RawMessage(nil), raw...)
			c.seen++
			c.mu.Unlock()
			if a, ok := answer[typ]; ok {
				return reply{Payload: a}
			}
			return reply{Payload: map[string]any{}}
		})
	}
	return c
}

func (c *capture) decode(t *testing.T, typ command.Type, v any) {
	t.Helper()
	c.mu.Lock()
	raw, ok := c.got[typ]
	c.mu.Unlock()
	if !ok {
		t.Fatalf("the agent received no %s", typ)
	}
	if err := codec.DecodePayload(raw, v); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
}

// TestListAppsJoinsStatus: list_apps answers the agent's apps, each with
// its status from the server's latest report (null when it has none).
func TestListAppsJoinsStatus(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.deps.StatusCache.SetAppStatus(alice.serverID, []command.AppStatus{
		{AppID: "app-1", StatusCode: command.ContainerStatusActive},
	}, nil, time.Now())
	e.agent.on(command.TypeAppsList, func(json.RawMessage) reply {
		return reply{Payload: command.ListAppsResponse{Apps: []model.App{
			{ID: "app-1", Name: "web"}, {ID: "app-2", Name: "db"},
		}}}
	})
	var out struct {
		Apps []struct {
			ID     string             `json:"id"`
			Name   string             `json:"name"`
			Status *command.AppStatus `json:"status"`
		} `json:"apps"`
		Truncated bool `json:"truncated"`
	}
	ok(t, e.session(t, alice), "list_apps", map[string]any{"server_id": alice.serverID}, &out)
	if len(out.Apps) != 2 || out.Truncated {
		t.Fatalf("list_apps = %+v", out)
	}
	if out.Apps[0].ID != "app-1" || out.Apps[0].Status == nil || out.Apps[0].Status.StatusCode != command.ContainerStatusActive {
		t.Fatalf("app-1 = %+v, want its reported status", out.Apps[0])
	}
	if out.Apps[1].ID != "app-2" || out.Apps[1].Status != nil {
		t.Fatalf("app-2 = %+v, want no status", out.Apps[1])
	}
}

// TestAppCommandsCarryArguments: each lifecycle tool sends its command
// with the caller's arguments and answers the agent's result.
func TestAppCommandsCarryArguments(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.deps.StatusCache.SetAppStatus(alice.serverID, []command.AppStatus{
		{AppID: "app-1", StatusCode: command.ContainerStatusActive},
	}, nil, time.Now())
	c := e.capture(map[command.Type]any{
		command.TypeAppRevisions: map[string]any{"revisions": []map[string]any{{"hash": "abc"}}},
		command.TypeImageTags:    command.ImageTagsResponse{Image: "nginx", Tags: []string{"1.27", "latest"}},
	}, command.TypeAppRename, command.TypeAppDelete, command.TypeAppRollback, command.TypeAppRevisions,
		command.TypeImageTags, command.TypeAppControl)
	s := e.session(t, alice)
	srv := alice.serverID

	var status struct {
		Status string `json:"status"`
	}
	ok(t, s, "rename_app", map[string]any{"server_id": srv, "app_id": "app-1", "name": "shop"}, &status)
	var rename command.RenameAppRequest
	c.decode(t, command.TypeAppRename, &rename)
	if status.Status != "succeeded" || rename.AppID != "app-1" || rename.Name != "shop" {
		t.Fatalf("rename_app = %+v, sent %+v", status, rename)
	}

	ok(t, s, "delete_app", map[string]any{"server_id": srv, "app_id": "app-1", "delete_volumes": true}, &status)
	var del command.DeleteAppRequest
	c.decode(t, command.TypeAppDelete, &del)
	if del.AppID != "app-1" || !del.DeleteVolumes || del.DeleteBackups {
		t.Fatalf("delete_app sent %+v", del)
	}

	ok(t, s, "rollback_app", map[string]any{"server_id": srv, "app_id": "app-1", "revision": "abc123"}, &status)
	var rb command.RollbackAppRequest
	c.decode(t, command.TypeAppRollback, &rb)
	if rb.AppID != "app-1" || rb.Hash != "abc123" {
		t.Fatalf("rollback_app sent %+v", rb)
	}

	var revs struct {
		Result struct {
			Revisions []struct {
				Hash string `json:"hash"`
			} `json:"revisions"`
		} `json:"result"`
	}
	ok(t, s, "get_app_revisions", map[string]any{"server_id": srv, "app_id": "app-1"}, &revs)
	if len(revs.Result.Revisions) != 1 || revs.Result.Revisions[0].Hash != "abc" {
		t.Fatalf("get_app_revisions = %+v", revs)
	}

	var tags struct {
		Result command.ImageTagsResponse `json:"result"`
	}
	ok(t, s, "get_image_tags", map[string]any{"server_id": srv, "image": "  nginx "}, &tags)
	var img command.ImageTagsRequest
	c.decode(t, command.TypeImageTags, &img)
	if img.Image != "nginx" || len(tags.Result.Tags) != 2 {
		t.Fatalf("get_image_tags sent %+v, answered %+v", img, tags)
	}

	var ctl struct {
		Status    string             `json:"status"`
		AppStatus *command.AppStatus `json:"app_status"`
	}
	ok(t, s, "control_app", map[string]any{"server_id": srv, "app_id": "app-1", "action": "update"}, &ctl)
	var ctlReq command.ControlAppRequest
	c.decode(t, command.TypeAppControl, &ctlReq)
	if ctlReq.Action != command.ControlUpdate || ctl.AppStatus == nil || ctl.AppStatus.AppID != "app-1" {
		t.Fatalf("control_app sent %+v, answered %+v", ctlReq, ctl)
	}
}

// TestToolsRefuseBadInput: malformed arguments are refused before anything
// is dispatched.
func TestToolsRefuseBadInput(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	s := e.session(t, alice)
	srv := alice.serverID
	files := []map[string]any{{"name": "compose.yml", "content": compose}}
	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list_apps", map[string]any{"server_id": ""}, "server_id is required"},
		{"get_app", map[string]any{"server_id": srv, "app_id": ""}, "app_id is required"},
		{"get_app", map[string]any{"server_id": srv, "app_id": "../etc"}, "invalid app_id"},
		{"control_app", map[string]any{"server_id": srv, "app_id": "app-1", "action": "kill"}, "action must be"},
		{"rename_app", map[string]any{"server_id": srv, "app_id": "app-1", "name": "  "}, "name is required"},
		{"rollback_app", map[string]any{"server_id": srv, "app_id": "app-1", "revision": ""}, "revision is required"},
		{"get_image_tags", map[string]any{"server_id": srv, "image": " "}, "image is required"},
		{"get_app_status", map[string]any{"server_id": srv, "app_id": "../etc"}, "invalid app_id"},
		{"get_app_logs", map[string]any{"server_id": srv, "app_id": "app-1", "service": "--follow"}, "invalid service name"},
		{"save_app", map[string]any{"server_id": srv, "app_id": "../etc", "name": "web", "files": files}, "invalid app_id"},
		{"save_app", map[string]any{"server_id": srv, "name": " ", "files": files}, "name is required"},
		{"save_app", map[string]any{"server_id": srv, "name": "web", "source": map[string]any{"repo_url": "https://git.example/x.git", "branch": " "}}, "repo_url and branch are required"},
		{"save_app", map[string]any{"server_id": srv, "name": "web", "files": []map[string]any{
			{"name": "compose.yml", "content": compose}, {"name": "key.pem", "content": "PRIVATE", "encrypted": true},
		}}, "secret values cannot be sent over MCP"},
		{"save_app", map[string]any{"server_id": srv, "name": "web", "files": []map[string]any{
			{"name": "compose.yml", "content": compose}, {"name": "key.pem", "content": "<encrypted>x"},
		}}, "a plain file can't start with <encrypted>"},
		{"save_app", map[string]any{"server_id": srv, "name": "web", "files": []map[string]any{
			{"name": "compose.yml", "content": compose}, {"name": "key.pem", "content": "<encrypted>", "encrypted": true},
		}}, "no secret is stored under this name"},
		{"create_domain", map[string]any{"server_id": srv, "name": "example.com", "tls": "certificate"}, "tls must be auto or off"},
		{"create_domain", map[string]any{"server_id": srv, "name": "not a domain", "tls": "auto"}, ""},
		{"delete_domain", map[string]any{"server_id": srv, "name": " "}, ""},
		{"get_command_result", map[string]any{"request_id": ""}, "request_id is required"},
		{"get_secret_request", map[string]any{"request_id": ""}, "request_id is required"},
	} {
		text, isErr := call(t, s, c.tool, c.args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s %v = %q (isError %v), want an error with %q", c.tool, c.args, text, isErr, c.want)
		}
	}
	if n := e.agent.total(); n != 0 {
		t.Fatalf("the agent received %d commands for refused input", n)
	}
}

// TestGetAppStatus: get_app_status reads the latest report only; without
// one it says so instead of answering an empty list as the truth.
func TestGetAppStatus(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	s := e.session(t, alice)
	var none struct {
		Apps []command.AppStatus `json:"apps"`
		Note string              `json:"note"`
	}
	ok(t, s, "get_app_status", map[string]any{"server_id": alice.serverID}, &none)
	if none.Apps == nil || len(none.Apps) != 0 || !strings.Contains(none.Note, "offline") {
		t.Fatalf("no report = %+v", none)
	}
	e.deps.StatusCache.SetAppStatus(alice.serverID, []command.AppStatus{
		{AppID: "app-1", StatusCode: command.ContainerStatusStopped},
	}, nil, time.Now())
	var all struct {
		Apps []command.AppStatus `json:"apps"`
		Note string              `json:"note"`
	}
	ok(t, s, "get_app_status", map[string]any{"server_id": alice.serverID}, &all)
	if len(all.Apps) != 1 || all.Apps[0].AppID != "app-1" || all.Note != "" {
		t.Fatalf("every app = %+v", all)
	}
	var one struct {
		AppStatus *command.AppStatus `json:"app_status"`
		Note      string             `json:"note"`
	}
	ok(t, s, "get_app_status", map[string]any{"server_id": alice.serverID, "app_id": "app-1"}, &one)
	if one.AppStatus == nil || one.AppStatus.StatusCode != command.ContainerStatusStopped || one.Note != "" {
		t.Fatalf("app-1 = %+v", one)
	}
	one = struct {
		AppStatus *command.AppStatus `json:"app_status"`
		Note      string             `json:"note"`
	}{}
	ok(t, s, "get_app_status", map[string]any{"server_id": alice.serverID, "app_id": "app-2"}, &one)
	if one.AppStatus != nil || !strings.Contains(one.Note, "no status for this app") {
		t.Fatalf("an app the report lacks = %+v", one)
	}
}

// TestGetAppViewsSourceBackupsAndSecretFiles: get_app shows a git source
// (never a token), the effective backups, and a secret file only as the
// keep placeholder; a config that is not JSON reads as {}.
func TestGetAppViewsSourceBackupsAndSecretFiles(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.agent.on(command.TypeAppGet, func(json.RawMessage) reply {
		return reply{Payload: command.GetAppResponse{
			App: command.AppPayload{
				AppID: "app-1", Config: []byte("not json"),
				Files: []command.ContentItem{{Name: "key.pem", Content: []byte("ciphertext"), Encrypted: true}},
				Source: &command.SourcePayload{RepoURL: "https://git.example/x.git", Branch: "main",
					ComposePath: "deploy/compose.yml", AutoUpdate: true, PollSeconds: 300, Token: []byte("tok")},
			},
			Backups: []recipe.EffectiveBackup{{Name: "db", Type: "sqlite", Enabled: true, Storage: "local"}},
		}}
	})
	text, isErr := call(t, e.session(t, alice), "get_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1"})
	if isErr {
		t.Fatal(text)
	}
	if strings.Contains(text, "tok") || strings.Contains(text, "ciphertext") {
		t.Fatalf("get_app leaks a secret: %s", text)
	}
	var out struct {
		Config map[string]any `json:"config"`
		Files  []struct {
			Name, Content string
			Encrypted     bool
		} `json:"files"`
		Source  map[string]any `json:"source"`
		Backups []struct {
			Name string `json:"name"`
		} `json:"backups"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Config == nil || len(out.Config) != 0 {
		t.Fatalf("config = %v, want {}", out.Config)
	}
	if len(out.Files) != 1 || out.Files[0].Content != "<encrypted>" || !out.Files[0].Encrypted {
		t.Fatalf("files = %+v", out.Files)
	}
	if out.Source["repo_url"] != "https://git.example/x.git" || out.Source["branch"] != "main" ||
		out.Source["compose_path"] != "deploy/compose.yml" || out.Source["auto_update"] != true || out.Source["poll_seconds"] != 300.0 {
		t.Fatalf("source = %v", out.Source)
	}
	if len(out.Backups) != 1 || out.Backups[0].Name != "db" {
		t.Fatalf("backups = %+v", out.Backups)
	}
}

// TestGetAppAnswerWithoutApp: an app.get answer that does not describe an
// app is an error, never an empty app a save could be built from.
func TestGetAppAnswerWithoutApp(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	text, isErr := call(t, e.session(t, alice), "get_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1"})
	if !isErr || !strings.Contains(text, "did not describe the app") {
		t.Fatalf("get_app on an empty answer = %q (isError %v)", text, isErr)
	}
}

// TestSaveAppSourceAndRedirects: a git-sourced save keeps a stored token
// with the placeholder, and its redirects ride the config's ingress.
func TestSaveAppSourceAndRedirects(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	if err := e.deps.ServerDomainRepository.Upsert(dbtest.Ctx(t), alice.serverID,
		model.RegisteredDomain{Name: "*.example.test", TLS: model.DomainTLSOff}); err != nil {
		t.Fatal(err)
	}
	var sent command.SaveAppRequest
	e.agent.on(command.TypeAppSave, func(raw json.RawMessage) reply {
		_ = codec.DecodePayload(raw, &sent)
		return reply{Payload: command.SaveAppResponse{AppID: sent.App.AppID, Revision: "rev-1"}}
	})
	var out struct {
		Status string `json:"status"`
		AppID  string `json:"app_id"`
	}
	ok(t, e.session(t, alice), "save_app", map[string]any{
		"server_id": alice.serverID, "name": "site",
		"source": map[string]any{"repo_url": " https://git.example/x.git ", "branch": "main", "auto_update": true, "poll_seconds": 600},
		"ingress": map[string]any{
			"domains":   []map[string]any{{"domain": "a.example.test", "upstream_port": 8080}},
			"redirects": []map[string]any{{"domain": " www.example.test ", "to": "https://a.example.test", "code": 301}},
		},
	}, &out)
	if out.Status != "succeeded" || out.AppID == "" {
		t.Fatalf("save_app = %+v", out)
	}
	src := sent.App.Source
	if src == nil || src.RepoURL != "https://git.example/x.git" || src.Branch != "main" || !src.AutoUpdate ||
		src.PollSeconds != 600 || string(src.Token) != command.EncryptedPlaceholder {
		t.Fatalf("source sent = %+v", src)
	}
	var cfg struct {
		Source  map[string]any `json:"source"`
		Ingress model.Ingress  `json:"ingress"`
	}
	if err := json.Unmarshal(sent.App.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Source["token_set"] != false || cfg.Source["repo_url"] != "https://git.example/x.git" {
		t.Fatalf("config source = %v: a new app has no stored token", cfg.Source)
	}
	if len(cfg.Ingress.Redirects) != 1 || cfg.Ingress.Redirects[0].Domain != "www.example.test" || cfg.Ingress.Redirects[0].Code != 301 {
		t.Fatalf("config ingress = %+v", cfg.Ingress)
	}
}

// TestSaveAppKeepNeedsTheStoredSecrets: a keep placeholder on an edit is
// checked against the app's stored secrets; when they cannot be read (the
// agent fails, or answers too late), nothing is saved.
func TestSaveAppKeepNeedsTheStoredSecrets(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	s := e.session(t, alice)
	args := map[string]any{
		"server_id": alice.serverID, "app_id": "app-1", "name": "web",
		"files":     []map[string]any{{"name": "compose.yml", "content": compose}},
		"variables": []map[string]any{{"name": "TOKEN", "value": "<encrypted>", "encrypted": true}},
	}
	e.agent.on(command.TypeAppGet, func(json.RawMessage) reply { return reply{Err: "disk on fire"} })
	if text, isErr := call(t, s, "save_app", args); !isErr || !strings.Contains(text, "could not read the app's stored secrets: disk on fire") {
		t.Fatalf("save_app with a failing read = %q (isError %v)", text, isErr)
	}
	e.agent.on(command.TypeAppGet, func(json.RawMessage) reply { return reply{Payload: map[string]any{}} })
	e.agent.delay(command.TypeAppGet, wait)
	if text, isErr := call(t, s, "save_app", args); !isErr || !strings.Contains(text, "in time") {
		t.Fatalf("save_app with a slow read = %q (isError %v)", text, isErr)
	}
	if n := e.agent.count(command.TypeAppSave); n != 0 {
		t.Fatalf("%d saves were sent", n)
	}
}

// TestDomainTools: the domain tools send their commands with the trimmed
// name and the TLS mode asked for.
func TestDomainTools(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	c := e.capture(map[command.Type]any{
		command.TypeDomainList: command.ListDomainsResponse{Domains: []command.Domain{{Name: "example.com"}}},
	}, command.TypeDomainList, command.TypeDomainCreate, command.TypeDomainDelete)
	s := e.session(t, alice)
	var list struct {
		Status string                      `json:"status"`
		Result command.ListDomainsResponse `json:"result"`
	}
	ok(t, s, "list_domains", map[string]any{"server_id": alice.serverID}, &list)
	if list.Status != "succeeded" || len(list.Result.Domains) != 1 || list.Result.Domains[0].Name != "example.com" {
		t.Fatalf("list_domains = %+v", list)
	}
	var status struct {
		Status string `json:"status"`
	}
	ok(t, s, "create_domain", map[string]any{"server_id": alice.serverID, "name": " example.com ", "tls": "off"}, &status)
	var created command.CreateDomainRequest
	c.decode(t, command.TypeDomainCreate, &created)
	if status.Status != "succeeded" || created.Name != "example.com" || created.TLS != model.DomainTLSOff {
		t.Fatalf("create_domain = %+v, sent %+v", status, created)
	}
	ok(t, s, "delete_domain", map[string]any{"server_id": alice.serverID, "name": "example.com"}, &status)
	var deleted command.DeleteDomainRequest
	c.decode(t, command.TypeDomainDelete, &deleted)
	if deleted.Name != "example.com" {
		t.Fatalf("delete_domain sent %+v", deleted)
	}
}

// TestDockerLists: list_networks and list_volumes answer the agent's lists.
func TestDockerLists(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.capture(map[command.Type]any{
		command.TypeNetworkList: map[string]any{"networks": []map[string]any{{"name": "bridge"}}},
		command.TypeVolumeList:  map[string]any{"volumes": []map[string]any{{"name": "data"}}},
	}, command.TypeNetworkList, command.TypeVolumeList)
	s := e.session(t, alice)
	for tool, want := range map[string]string{"list_networks": "bridge", "list_volumes": "data"} {
		text, isErr := call(t, s, tool, map[string]any{"server_id": alice.serverID})
		if isErr || !strings.Contains(text, `"status":"succeeded"`) || !strings.Contains(text, want) {
			t.Errorf("%s = %q (isError %v)", tool, text, isErr)
		}
	}
}

// TestListSharedVariables: the org's variables, plus a server's own with
// server_id; a secret is shown by name only.
func TestListSharedVariables(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	ctx := dbtest.Ctx(t)
	srv := alice.serverID
	for _, v := range []model.SharedVariable{
		{ID: util.GenerateID(), OrgID: alice.orgID, Name: "SMTP_HOST", Value: "mail.example", Description: "relay"},
		{ID: util.GenerateID(), OrgID: alice.orgID, Name: "SMTP_PASS", IsSecret: true},
		{ID: util.GenerateID(), OrgID: alice.orgID, ServerID: &srv, Name: "SMTP_HOST", Value: "local"},
	} {
		save := model.SharedVariableSave{Variable: v, Create: true, Bump: []string{srv}}
		if v.IsSecret {
			save.Ciphertexts = map[string]string{srv: "CT"}
		}
		if err := e.deps.SharedVariableRepository.Save(ctx, save); err != nil {
			t.Fatal(err)
		}
	}
	type sharedVar struct {
		Name, Value, Description string
		Secret                   bool
	}
	var orgOnly struct {
		Org    []sharedVar  `json:"org"`
		Server *[]sharedVar `json:"server"`
	}
	s := e.session(t, alice)
	ok(t, s, "list_shared_variables", map[string]any{}, &orgOnly)
	if orgOnly.Server != nil || len(orgOnly.Org) != 2 {
		t.Fatalf("org only = %+v", orgOnly)
	}
	if v := orgOnly.Org[0]; v.Name != "SMTP_HOST" || v.Value != "mail.example" || v.Description != "relay" || v.Secret {
		t.Fatalf("plain = %+v", v)
	}
	if v := orgOnly.Org[1]; v.Name != "SMTP_PASS" || v.Value != "" || !v.Secret {
		t.Fatalf("secret = %+v", v)
	}
	var both struct {
		Org    []sharedVar `json:"org"`
		Server []sharedVar `json:"server"`
	}
	ok(t, s, "list_shared_variables", map[string]any{"server_id": srv}, &both)
	if len(both.Org) != 2 || len(both.Server) != 1 || both.Server[0].Value != "local" {
		t.Fatalf("with server_id = %+v", both)
	}
}
