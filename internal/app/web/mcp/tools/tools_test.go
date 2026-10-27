package tools_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"winterflow/internal/app/web/mcp"
	"winterflow/internal/domain/command"
)

// TestToolsListNames is the golden list of tool names.
func TestToolsListNames(t *testing.T) {
	e := newEnv(t, nil)
	s := e.session(t, e.newUser(t, "a@example.test"))
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	want := []string{
		"control_app", "create_domain", "delete_app", "delete_domain", "get_app", "get_app_logs",
		"get_app_revisions", "get_app_status", "get_command_result", "get_image_tags", "list_apps",
		"list_domains", "list_networks", "list_servers", "list_shared_variables", "list_volumes",
		"rename_app", "rollback_app", "save_app",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v\nwant    %v", names, want)
	}
}

func TestListServersOnlyOwnOrg(t *testing.T) {
	e := newEnv(t, nil)
	alice, bob := e.newUser(t, "alice@example.test"), e.newUser(t, "bob@example.test")
	var out struct {
		Servers []struct {
			ID     string `json:"id"`
			Online bool   `json:"online"`
		} `json:"servers"`
	}
	ok(t, e.session(t, alice), "list_servers", nil, &out)
	if len(out.Servers) != 1 || out.Servers[0].ID != alice.serverID {
		t.Fatalf("alice's servers = %+v, want only %s (bob's is %s)", out.Servers, alice.serverID, bob.serverID)
	}
}

func TestOtherOrgServerForbidden(t *testing.T) {
	e := newEnv(t, nil)
	alice, bob := e.newUser(t, "alice@example.test"), e.newUser(t, "bob@example.test")
	s := e.session(t, alice)
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"list_apps", map[string]any{}},
		{"get_app", map[string]any{"app_id": "app-1"}},
		{"save_app", map[string]any{"name": "web", "files": []map[string]any{{"name": "compose.yml", "content": "services: {}\n"}}}},
		{"control_app", map[string]any{"app_id": "app-1", "action": "stop"}},
		{"delete_app", map[string]any{"app_id": "app-1"}},
		{"get_app_status", map[string]any{}},
		{"get_app_logs", map[string]any{"app_id": "app-1"}},
		{"list_domains", map[string]any{}},
		{"list_networks", map[string]any{}},
		{"get_image_tags", map[string]any{"image": "nginx"}},
		{"list_shared_variables", map[string]any{}},
		{"rename_app", map[string]any{"app_id": "app-1", "name": "web"}},
		{"rollback_app", map[string]any{"app_id": "app-1", "revision": "abc"}},
		{"get_app_revisions", map[string]any{"app_id": "app-1"}},
		{"create_domain", map[string]any{"name": "example.com", "tls": "auto"}},
		{"delete_domain", map[string]any{"name": "example.com"}},
		{"list_volumes", map[string]any{}},
	} {
		c.args["server_id"] = bob.serverID
		text, isErr := call(t, s, c.tool, c.args)
		if !isErr || !strings.Contains(text, "forbidden") {
			t.Errorf("%s on another org's server = %q (isError %v), want forbidden", c.tool, text, isErr)
		}
	}
	if n := e.agent.total(); n != 0 {
		t.Fatalf("the agent received %d commands for another org's server", n)
	}
}

const compose = "services:\n  web:\n    image: nginx\n"

func TestSaveAppWaitsForResult(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	var out struct {
		Status    string `json:"status"`
		RequestID string `json:"request_id"`
		Result    struct {
			AppID    string `json:"app_id"`
			Revision string `json:"revision"`
		} `json:"result"`
		AppStatus json.RawMessage `json:"app_status"`
		Note      string          `json:"note"`
	}
	ok(t, e.session(t, alice), "save_app", map[string]any{
		"server_id": alice.serverID, "name": "web",
		"files":     []map[string]any{{"name": "compose.yml", "content": compose}},
		"variables": []map[string]any{{"name": "PORT", "value": "8080"}},
	}, &out)
	if out.Status != "succeeded" || out.RequestID == "" || out.Result.AppID == "" || out.Result.Revision != "rev-1" {
		t.Fatalf("save_app = %+v", out)
	}
	if string(out.AppStatus) != "null" || !strings.Contains(out.Note, "get_app_status") {
		t.Fatalf("status part = %s / %q", out.AppStatus, out.Note)
	}
	// The outcome was recorded: get_command_result answers it.
	var rec struct {
		Status string `json:"status"`
	}
	ok(t, e.session(t, alice), "get_command_result", map[string]any{"request_id": out.RequestID}, &rec)
	if rec.Status != "succeeded" {
		t.Fatalf("get_command_result = %+v", rec)
	}
}

func TestSaveAppRejectsPlainSecret(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	s := e.session(t, alice)
	text, isErr := call(t, s, "save_app", map[string]any{
		"server_id": alice.serverID, "name": "web",
		"files":     []map[string]any{{"name": "compose.yml", "content": compose}},
		"variables": []map[string]any{{"name": "TOKEN", "value": "hunter2", "encrypted": true}},
	})
	if !isErr || text != "secret values cannot be sent over MCP; use request_secrets" {
		t.Fatalf("save_app with a plain secret = %q (isError %v)", text, isErr)
	}
	// A declared secret sent plain is refused by the save checks, and a
	// required one left out points at request_secrets.
	declared := "services:\n  web:\n    image: nginx\n    environment:\n      TOKEN: ${TOKEN}\n" +
		"x-winterflow:\n  variables:\n    TOKEN:\n      required: true\n      encrypted: true\n"
	text, isErr = call(t, s, "save_app", map[string]any{
		"server_id": alice.serverID, "name": "web",
		"files": []map[string]any{{"name": "compose.yml", "content": declared}},
	})
	if !isErr || !strings.HasSuffix(text, "Use request_secrets to ask the user for it.") {
		t.Fatalf("save_app without a required secret = %q (isError %v)", text, isErr)
	}
	if n := e.agent.count(command.TypeAppSave); n != 0 {
		t.Fatalf("the agent received %d saves", n)
	}
}

func TestSlowCommandReturnsRunningThenResult(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.agent.delay(command.TypeAppControl, wait+700*time.Millisecond)
	s := e.session(t, alice)
	var running struct {
		Status    string `json:"status"`
		RequestID string `json:"request_id"`
		Hint      string `json:"hint"`
	}
	ok(t, s, "control_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1", "action": "restart"}, &running)
	if running.Status != "running" || running.RequestID == "" || running.Hint != "call get_command_result" {
		t.Fatalf("slow control_app = %+v", running)
	}
	var done struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	ok(t, s, "get_command_result", map[string]any{"request_id": running.RequestID}, &done)
	if done.Status != "succeeded" {
		t.Fatalf("get_command_result = %+v", done)
	}
	// Another org cannot read it.
	bob := e.newUser(t, "bob@example.test")
	var other struct {
		Status string `json:"status"`
	}
	ok(t, e.session(t, bob), "get_command_result", map[string]any{"request_id": running.RequestID}, &other)
	if other.Status != "unknown" {
		t.Fatalf("another org's get_command_result = %+v", other)
	}
}

func TestFailedCommandIsToolError(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.agent.on(command.TypeAppControl, func(json.RawMessage) reply { return reply{Err: "no such app"} })
	text, isErr := call(t, e.session(t, alice), "control_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1", "action": "stop"})
	if !isErr || !strings.Contains(text, "no such app") {
		t.Fatalf("failed control_app = %q (isError %v)", text, isErr)
	}
}

// managed reports every app as a live template instance.
type managed struct{}

func (managed) LiveInstanceForApp(context.Context, string, string) (string, bool, error) {
	return "shop", true, nil
}
func (managed) RetireForApp(context.Context, string, string) error { return nil }

func TestInstanceManagedAppError(t *testing.T) {
	e := newEnv(t, managed{})
	alice := e.newUser(t, "alice@example.test")
	s := e.session(t, alice)
	text, isErr := call(t, s, "save_app", map[string]any{
		"server_id": alice.serverID, "app_id": "app-1", "name": "web",
		"files": []map[string]any{{"name": "compose.yml", "content": compose}},
	})
	if !isErr || text != mcp.ManagedAppMessage {
		t.Fatalf("save_app on a managed app = %q (isError %v)", text, isErr)
	}
	text, isErr = call(t, s, "rollback_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1", "revision": "abc"})
	if !isErr || text != mcp.ManagedAppMessage {
		t.Fatalf("rollback_app on a managed app = %q (isError %v)", text, isErr)
	}
	if n := e.agent.total(); n != 0 {
		t.Fatalf("the agent received %d commands", n)
	}
}

func TestSecretsFromNotYetSupported(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	text, isErr := call(t, e.session(t, alice), "save_app", map[string]any{
		"server_id": alice.serverID, "name": "web", "secrets_from": "req-1",
		"files": []map[string]any{{"name": "compose.yml", "content": compose}},
	})
	if !isErr || !strings.Contains(text, "secrets_from") {
		t.Fatalf("save_app with secrets_from = %q (isError %v)", text, isErr)
	}
}

func TestGetAppDecodesFiles(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.agent.on(command.TypeAppGet, func(json.RawMessage) reply {
		return reply{Payload: command.GetAppResponse{App: command.AppPayload{
			AppID: "app-1", Config: []byte(`{"name":"web"}`),
			Files:     []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
			Variables: []command.ContentItem{{Name: "TOKEN", Content: []byte("ciphertext"), Encrypted: true}},
		}}}
	})
	var out struct {
		Config map[string]any `json:"config"`
		Files  []struct {
			Name, Content string
		} `json:"files"`
		Variables []struct {
			Name, Value string
			Encrypted   bool
		} `json:"variables"`
	}
	ok(t, e.session(t, alice), "get_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1"}, &out)
	if out.Config["name"] != "web" || len(out.Files) != 1 || out.Files[0].Content != compose {
		t.Fatalf("get_app = %+v", out)
	}
	if len(out.Variables) != 1 || out.Variables[0].Value != "<encrypted>" || !out.Variables[0].Encrypted {
		t.Fatalf("get_app variables = %+v: a secret must read <encrypted>", out.Variables)
	}
}

func TestGetAppLogsFiltersService(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	e.agent.on(command.TypeAppLogs, func(json.RawMessage) reply {
		return reply{Payload: command.GetLogsResponse{AppID: "app-1", Logs: []command.LogEntry{
			{Message: "from web", Container: "wf-app-1-web-1"},
			{Message: "from db", Container: "wf-app-1-db-1"},
			{Message: "from app-db", Container: "wf-app-1-app-db-1"},
		}}}
	})
	var out struct {
		Result struct {
			Logs []command.LogEntry `json:"logs"`
		} `json:"result"`
	}
	ok(t, e.session(t, alice), "get_app_logs", map[string]any{"server_id": alice.serverID, "app_id": "app-1", "service": "db"}, &out)
	if len(out.Result.Logs) != 1 || out.Result.Logs[0].Message != "from db" {
		t.Fatalf("get_app_logs = %+v", out.Result.Logs)
	}
}

// storedToken makes app.get report app-1 with the secret variable TOKEN
// and the secret file certs/key.pem stored.
func storedToken(e *env) {
	e.agent.on(command.TypeAppGet, func(json.RawMessage) reply {
		return reply{Payload: command.GetAppResponse{App: command.AppPayload{
			AppID: "app-1", Config: []byte(`{}`),
			Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)},
				{Name: "certs/key.pem", Content: []byte("<encrypted>"), Encrypted: true}},
			Variables: []command.ContentItem{{Name: "TOKEN", Content: []byte("<encrypted>"), Encrypted: true}},
		}}}
	})
}

func TestSaveAppKeepPlaceholderRules(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	storedToken(e)
	s := e.session(t, alice)
	save := func(appID string, vars, files []map[string]any) (string, bool) {
		args := map[string]any{"server_id": alice.serverID, "name": "web",
			"files": append([]map[string]any{{"name": "compose.yml", "content": compose}}, files...), "variables": vars}
		if appID != "" {
			args["app_id"] = appID
		}
		return call(t, s, "save_app", args)
	}
	keep := func(name string) []map[string]any {
		return []map[string]any{{"name": name, "value": "<encrypted>", "encrypted": true}}
	}
	for _, c := range []struct {
		what        string
		appID       string
		vars, files []map[string]any
		wantInError string
	}{
		{"a create keeps nothing", "", keep("TOKEN"), nil, "TOKEN: no secret is stored"},
		{"a renamed secret keeps nothing", "app-1", keep("API_TOKEN"), nil, "API_TOKEN: no secret is stored"},
		{"a renamed secret file keeps nothing", "app-1", nil,
			[]map[string]any{{"name": "certs/new.pem", "content": "<encrypted>", "encrypted": true}}, "certs/new.pem: no secret is stored"},
		{"a plain value can't pose as the placeholder", "app-1",
			[]map[string]any{{"name": "PORT", "value": "<encrypted>"}}, nil, "PORT: a plain value can't start with <encrypted>"},
	} {
		text, isErr := save(c.appID, c.vars, c.files)
		if !isErr || !strings.Contains(text, c.wantInError) {
			t.Errorf("%s: %q (isError %v), want %q", c.what, text, isErr, c.wantInError)
		}
		if isErr && strings.Contains(c.wantInError, "no secret is stored") && !strings.Contains(text, "use request_secrets to ask the user for it") {
			t.Errorf("%s: %q does not point at request_secrets", c.what, text)
		}
	}
	if n := e.agent.count(command.TypeAppSave); n != 0 {
		t.Fatalf("the agent received %d saves", n)
	}
	// Keeping the stored secret and the stored file goes through.
	text, isErr := save("app-1", keep("TOKEN"), []map[string]any{{"name": "certs/key.pem", "content": "<encrypted>", "encrypted": true}})
	if isErr {
		t.Fatalf("keeping stored secrets: %s", text)
	}
	if n := e.agent.count(command.TypeAppSave); n != 1 {
		t.Fatalf("the agent received %d saves, want 1", n)
	}
}

// bigApp makes app.get answer an app whose compose file alone is past the
// stored result's cap.
func bigApp(e *env) string {
	big := compose + "# " + strings.Repeat("x", 2<<20) + "\n"
	e.agent.on(command.TypeAppGet, func(json.RawMessage) reply {
		return reply{Payload: command.GetAppResponse{App: command.AppPayload{
			AppID: "app-1", Config: []byte(`{}`), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(big)}},
		}}}
	})
	return big
}

// TestGetAppLargeApp: get_app shows a large app whole when it waited for
// the answer itself, and refuses the stored copy, cut to fit, instead of
// showing an empty app.
func TestGetAppLargeApp(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	big := bigApp(e)
	s := e.session(t, alice)
	var out struct {
		Files []struct{ Name, Content string } `json:"files"`
	}
	ok(t, s, "get_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1"}, &out)
	if len(out.Files) != 1 || out.Files[0].Content != big {
		t.Fatalf("get_app of a large app: %d files", len(out.Files))
	}

	e.agent.delay(command.TypeAppGet, wait+700*time.Millisecond)
	var running struct {
		Status    string `json:"status"`
		RequestID string `json:"request_id"`
	}
	ok(t, s, "get_app", map[string]any{"server_id": alice.serverID, "app_id": "app-1"}, &running)
	if running.Status != "running" {
		t.Fatalf("slow get_app = %+v", running)
	}
	// A 2 MiB answer takes a while under -race: ask again while it runs.
	var text string
	var isErr bool
	for i := 0; i < 5; i++ {
		text, isErr = call(t, s, "get_command_result", map[string]any{"request_id": running.RequestID})
		if isErr || !strings.Contains(text, `"status":"running"`) {
			break
		}
	}
	if !isErr || !strings.Contains(text, "too large to show over MCP") {
		t.Fatalf("get_command_result of a large app.get = %.200q (isError %v)", text, isErr)
	}
}
