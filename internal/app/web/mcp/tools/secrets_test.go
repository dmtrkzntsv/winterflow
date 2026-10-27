package tools_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/transport/codec"
	"winterflow/pkg/crypto/cryptotest"
)

// secretCompose declares API_KEY a required secret.
const secretCompose = "services:\n  web:\n    image: nginx\n    environment:\n      API_KEY: ${API_KEY}\n" +
	"x-winterflow:\n  variables:\n    API_KEY: { required: true, encrypted: true }\n"

// askedSecret is a request_secrets answer.
type askedSecret struct {
	RequestID string `json:"request_id"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

// askSecrets runs request_secrets as u and returns the answer and the link's
// raw token.
func askSecrets(t *testing.T, e *env, u user, args map[string]any) (askedSecret, string) {
	t.Helper()
	args["server_id"] = u.serverID
	var out askedSecret
	ok(t, e.session(t, u), "request_secrets", args, &out)
	i := strings.Index(out.URL, "/secrets/")
	if out.RequestID == "" || out.ExpiresAt == "" || i < 0 || !strings.HasPrefix(out.URL[i+len("/secrets/"):], "wfx_") {
		t.Fatalf("request_secrets = %+v", out)
	}
	return out, out.URL[i+len("/secrets/"):]
}

// submit fills the link as u, encrypting each value.
func (e *env) submit(t *testing.T, u user, raw string, plain map[string]string) map[string]string {
	t.Helper()
	cts := map[string]string{}
	for name, v := range plain {
		cts[name] = cryptotest.Ciphertext(t, v)
	}
	if err := e.deps.SecretRequests.Submit(dbtest.Ctx(t), u.id, raw, cts); err != nil {
		t.Fatal(err)
	}
	return cts
}

// secretStatus is get_secret_request's status.
func secretStatus(t *testing.T, e *env, u user, id string) string {
	t.Helper()
	var out struct {
		Status string   `json:"status"`
		Names  []string `json:"names"`
	}
	ok(t, e.session(t, u), "get_secret_request", map[string]any{"request_id": id}, &out)
	return out.Status
}

// savedVariables records the variables of every app.save the agent gets.
func savedVariables(e *env) func() []command.ContentItem {
	var (
		mu   sync.Mutex
		last []command.ContentItem
	)
	e.agent.on(command.TypeAppSave, func(raw json.RawMessage) reply {
		var in command.SaveAppRequest
		_ = codec.DecodePayload(raw, &in)
		mu.Lock()
		last = in.App.Variables
		mu.Unlock()
		return reply{Payload: command.SaveAppResponse{AppID: in.App.AppID, Revision: "rev-1"}}
	})
	return func() []command.ContentItem {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

func TestRequestSecretsLink(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	t.Setenv("WEB_URL", "https://wf.example.test/")
	e2 := newEnv(t, nil)
	alice2 := e2.newUser(t, "alice@example.test")
	asked, raw := askSecrets(t, e2, alice2, map[string]any{"names": []string{"API_KEY"}, "notes": map[string]string{"API_KEY": "from the dashboard"}})
	if asked.URL != "https://wf.example.test/secrets/"+raw {
		t.Fatalf("url = %q", asked.URL)
	}
	if st := secretStatus(t, e2, alice2, asked.RequestID); st != "pending" {
		t.Fatalf("status = %s", st)
	}
	// Without WEB_URL the link is a path, and the answer says so.
	text, isErr := call(t, e.session(t, alice), "request_secrets", map[string]any{"server_id": alice.serverID, "names": []string{"API_KEY"}})
	if isErr || !strings.Contains(text, `"url":"/secrets/wfx_`) || !strings.Contains(text, "WEB_URL") {
		t.Fatalf("without WEB_URL = %s", text)
	}
	for _, names := range [][]string{{"lower"}, {"WF_X"}, {}} {
		if text, isErr := call(t, e2.session(t, alice2), "request_secrets", map[string]any{"server_id": alice2.serverID, "names": names}); !isErr {
			t.Errorf("names %v = %s, want an error", names, text)
		}
	}
}

// TestRequestSecretsExistingApp: an existing app's request names only its
// secret variables, read from the agent.
func TestRequestSecretsExistingApp(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	s := e.session(t, alice)
	var saved struct {
		Result command.SaveAppResponse `json:"result"`
	}
	ok(t, s, "save_app", map[string]any{"server_id": alice.serverID, "name": "web",
		"files": []map[string]any{{"name": "compose.yml", "content": compose}}, "draft": true}, &saved)
	appID := saved.Result.AppID
	e.agent.on(command.TypeAppGet, func(json.RawMessage) reply {
		return reply{Payload: command.GetAppResponse{App: command.AppPayload{
			AppID: appID, Config: []byte(`{}`), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
			Variables: []command.ContentItem{{Name: "TOKEN", Content: []byte("<encrypted>"), Encrypted: true}},
		}}}
	})
	text, isErr := call(t, s, "request_secrets", map[string]any{"server_id": alice.serverID, "app_id": appID, "names": []string{"OTHER"}})
	if !isErr || !strings.Contains(text, "OTHER is not a secret variable") {
		t.Fatalf("an undeclared name = %q (isError %v)", text, isErr)
	}
	askSecrets(t, e, alice, map[string]any{"app_id": appID, "names": []string{"TOKEN"}})
}

func TestSaveAppSecretsFrom(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	vars := savedVariables(e)
	asked, raw := askSecrets(t, e, alice, map[string]any{"names": []string{"API_KEY", "DB_PASSWORD"}})
	// Not submitted yet: the save is refused and nothing is sent.
	args := map[string]any{"server_id": alice.serverID, "name": "web", "secrets_from": asked.RequestID,
		"files":     []map[string]any{{"name": "compose.yml", "content": secretCompose}},
		"variables": []map[string]any{{"name": "DB_PASSWORD", "value": "<encrypted>", "encrypted": true}, {"name": "PORT", "value": "80"}}}
	if text, isErr := call(t, e.session(t, alice), "save_app", args); !isErr || !strings.Contains(text, "not submitted") {
		t.Fatalf("save before the submission = %q", text)
	}
	cts := e.submit(t, alice, raw, map[string]string{"API_KEY": "key-1", "DB_PASSWORD": "pw-1"})
	if st := secretStatus(t, e, alice, asked.RequestID); st != "submitted" {
		t.Fatalf("status = %s", st)
	}
	var out struct {
		Status string `json:"status"`
	}
	ok(t, e.session(t, alice), "save_app", args, &out)
	got := map[string]command.ContentItem{}
	for _, v := range vars() {
		got[v.Name] = v
	}
	// API_KEY (declared by the compose file, left out of variables) and
	// DB_PASSWORD (sent as a keep placeholder) carry the ciphertexts.
	for name, ct := range cts {
		if v := got[name]; string(v.Content) != ct || !v.Encrypted {
			t.Errorf("%s = %+v, want the submitted ciphertext as a secret", name, v)
		}
	}
	if string(got["PORT"].Content) != "80" || len(got) != 3 {
		t.Fatalf("variables = %+v", got)
	}
	if st := secretStatus(t, e, alice, asked.RequestID); st != "used" {
		t.Fatalf("status after the save = %s", st)
	}
	// Used once: a second save with it is refused.
	if text, isErr := call(t, e.session(t, alice), "save_app", args); !isErr || !strings.Contains(text, "used already") {
		t.Fatalf("second save = %q", text)
	}
}

func TestSaveAppSecretsFromOtherOrg(t *testing.T) {
	e := newEnv(t, nil)
	alice, bob := e.newUser(t, "alice@example.test"), e.newUser(t, "bob@example.test")
	asked, raw := askSecrets(t, e, bob, map[string]any{"names": []string{"API_KEY"}})
	e.submit(t, bob, raw, map[string]string{"API_KEY": "bob's"})
	text, isErr := call(t, e.session(t, alice), "save_app", map[string]any{
		"server_id": alice.serverID, "name": "web", "secrets_from": asked.RequestID,
		"files": []map[string]any{{"name": "compose.yml", "content": secretCompose}},
	})
	if !isErr || !strings.Contains(text, "no such secret request") {
		t.Fatalf("another org's request = %q (isError %v)", text, isErr)
	}
	if text, isErr := call(t, e.session(t, alice), "get_secret_request", map[string]any{"request_id": asked.RequestID}); !isErr {
		t.Fatalf("another org's get_secret_request = %s", text)
	}
	if n := e.agent.count(command.TypeAppSave); n != 0 {
		t.Fatalf("the agent received %d saves", n)
	}
	if st := secretStatus(t, e, bob, asked.RequestID); st != "submitted" {
		t.Fatalf("bob's request = %s", st)
	}
}

func TestSaveAppSecretsFromFailedCheckKeepsSubmitted(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	asked, raw := askSecrets(t, e, alice, map[string]any{"names": []string{"API_KEY"}})
	e.submit(t, alice, raw, map[string]string{"API_KEY": "key-1"})
	s := e.session(t, alice)
	for _, c := range []struct {
		what string
		args map[string]any
		want string
	}{
		{"the save does not declare it a secret", map[string]any{
			"files": []map[string]any{{"name": "compose.yml", "content": compose}},
		}, "does not declare it a secret"},
		{"sent as a plain variable", map[string]any{
			"files":     []map[string]any{{"name": "compose.yml", "content": secretCompose}},
			"variables": []map[string]any{{"name": "API_KEY", "value": "plain"}},
		}, "send it with encrypted: true"},
		{"a save-app check refuses it", map[string]any{
			"files":   []map[string]any{{"name": "compose.yml", "content": secretCompose}},
			"ingress": map[string]any{"domains": []map[string]any{{"domain": "app.uncovered.test", "upstream_port": 8080}}},
		}, "uncovered.test"},
		{"another server", map[string]any{"server_id": "00000000-0000-7000-8000-000000000999",
			"files": []map[string]any{{"name": "compose.yml", "content": secretCompose}},
		}, "forbidden"},
	} {
		args := map[string]any{"server_id": alice.serverID, "name": "web", "secrets_from": asked.RequestID}
		for k, v := range c.args {
			args[k] = v
		}
		text, isErr := call(t, s, "save_app", args)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s: %q (isError %v), want %q", c.what, text, isErr, c.want)
		}
	}
	if n := e.agent.count(command.TypeAppSave); n != 0 {
		t.Fatalf("the agent received %d saves", n)
	}
	if st := secretStatus(t, e, alice, asked.RequestID); st != "submitted" {
		t.Fatalf("after refused saves = %s, want submitted (a retry may use it)", st)
	}
	// The retry goes through.
	var out struct{ Status string }
	ok(t, s, "save_app", map[string]any{"server_id": alice.serverID, "name": "web", "secrets_from": asked.RequestID,
		"files": []map[string]any{{"name": "compose.yml", "content": secretCompose}}}, &out)
	if st := secretStatus(t, e, alice, asked.RequestID); st != "used" {
		t.Fatalf("after the retry = %s", st)
	}
}
