package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"winterflow/internal/app/web/mcp/tools"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/secretreq"
	"winterflow/internal/infra/bootstrap"
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
	// An https WEB_URL wins over the address the client used.
	asked = askVia(t, e2, bearer{token: alice2.token, host: "homelab:8080"}, alice2)
	if !strings.HasPrefix(asked.URL, "https://wf.example.test/secrets/wfx_") {
		t.Fatalf("https WEB_URL, LAN Host: url = %q", asked.URL)
	}
	// Without WEB_URL the link is on the address the client used.
	asked = askVia(t, e, bearer{token: alice.token, host: "homelab:8080"}, alice)
	if !strings.HasPrefix(asked.URL, "http://homelab:8080/secrets/wfx_") {
		t.Fatalf("without WEB_URL: url = %q", asked.URL)
	}
	for _, names := range [][]string{{"lower"}, {"WF_X"}, {}} {
		if text, isErr := call(t, e2.session(t, alice2), "request_secrets", map[string]any{"server_id": alice2.serverID, "names": names}); !isErr {
			t.Errorf("names %v = %s, want an error", names, text)
		}
	}
}

// askVia calls request_secrets for u's server over a session sending rt.
func askVia(t *testing.T, e *env, rt bearer, u user) askedSecret {
	t.Helper()
	var out askedSecret
	ok(t, e.sessionVia(t, rt), "request_secrets", map[string]any{"server_id": u.serverID, "names": []string{"API_KEY"}}, &out)
	return out
}

// TestRequestSecretsLoopbackWebURL: the installer's default WEB_URL,
// http://localhost:<port>, opens nothing on the user's laptop, so the link
// is built on the address the MCP client reached Winterflow at; https only
// over TLS or when the local proxy (a loopback peer) says so.
func TestRequestSecretsLoopbackWebURL(t *testing.T) {
	t.Setenv("WEB_URL", "http://localhost:8080")
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	if got := askVia(t, e, bearer{token: alice.token, host: "homelab:8080"}, alice).URL; !strings.HasPrefix(got, "http://homelab:8080/secrets/wfx_") {
		t.Fatalf("LAN Host: url = %q", got)
	}
	// The test client's peer is loopback, as a local proxy's is.
	xfp := http.Header{"X-Forwarded-Proto": {"https"}}
	if got := askVia(t, e, bearer{token: alice.token, host: "wf.example.test", header: xfp}, alice).URL; !strings.HasPrefix(got, "https://wf.example.test/secrets/wfx_") {
		t.Fatalf("X-Forwarded-Proto from loopback: url = %q", got)
	}
}

// TestRequestSecretsExistingApp: a request for an existing app may name a
// secret the app does not have yet, and needs no agent: the save that uses
// it is the binding check.
func TestRequestSecretsExistingApp(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	var saved struct {
		Result command.SaveAppResponse `json:"result"`
	}
	ok(t, e.session(t, alice), "save_app", map[string]any{"server_id": alice.serverID, "name": "web",
		"files": []map[string]any{{"name": "compose.yml", "content": compose}}, "draft": true}, &saved)
	before := e.agent.total()
	askSecrets(t, e, alice, map[string]any{"app_id": saved.Result.AppID, "names": []string{"NEW_SECRET"}})
	if n := e.agent.total() - before; n != 0 {
		t.Fatalf("request_secrets sent the agent %d commands", n)
	}
}

// TestSaveAppSecretsFromConcurrent: two saves racing with one secrets_from
// send the agent one app.save; the other is told the request is in use.
func TestSaveAppSecretsFromConcurrent(t *testing.T) {
	e := newEnv(t, nil)
	alice := e.newUser(t, "alice@example.test")
	storedToken(e)
	// The keep check's app.get holds the first save (and its claim) while
	// the second asks.
	e.agent.delay(command.TypeAppGet, 300*time.Millisecond)
	asked, raw := askSecrets(t, e, alice, map[string]any{"names": []string{"API_KEY"}})
	e.submit(t, alice, raw, map[string]string{"API_KEY": "key-1"})
	args := map[string]any{"server_id": alice.serverID, "app_id": "app-1", "name": "web", "secrets_from": asked.RequestID,
		"files":     []map[string]any{{"name": "compose.yml", "content": secretCompose}},
		"variables": []map[string]any{{"name": "TOKEN", "value": "<encrypted>", "encrypted": true}}}
	type answer struct {
		text  string
		isErr bool
	}
	answers := make(chan answer, 2)
	for _, s := range []*sdk.ClientSession{e.session(t, alice), e.session(t, alice)} {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			res, err := s.CallTool(ctx, &sdk.CallToolParams{Name: "save_app", Arguments: args})
			if err != nil {
				answers <- answer{err.Error(), true}
				return
			}
			answers <- answer{res.Content[0].(*sdk.TextContent).Text, res.IsError}
		}()
	}
	var oks, inUse int
	for range 2 {
		a := <-answers
		switch {
		case !a.isErr:
			oks++
		case strings.Contains(a.text, "already being used"):
			inUse++
		default:
			t.Errorf("unexpected answer: %s", a.text)
		}
	}
	if oks != 1 || inUse != 1 {
		t.Fatalf("%d saves succeeded and %d were told it is in use, want 1 and 1", oks, inUse)
	}
	if n := e.agent.count(command.TypeAppSave); n != 1 {
		t.Fatalf("the agent received %d saves, want 1", n)
	}
	if st := secretStatus(t, e, alice, asked.RequestID); st != "used" {
		t.Fatalf("status = %s", st)
	}
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

// failingUse fails every MarkUsed, as a database that stays down after the
// save was sent.
type failingUse struct {
	port.SecretRequestRepository
}

func (failingUse) MarkUsed(context.Context, string, string, time.Time) (bool, error) {
	return false, errors.New("database is locked")
}

// testClock is a settable clock.
type testClock struct {
	mu  sync.Mutex
	off time.Duration
}

func (c *testClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return time.Now().Add(c.off) }
func (c *testClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.off += d
}

// TestSaveAppSecretsFromMarkUsedFails: a save was sent but its request
// could not be marked used; the request was marked dispatched before the
// send, so once the claim's deadline passed, a second save with it is
// still refused and the agent gets the ciphertexts once.
func TestSaveAppSecretsFromMarkUsedFails(t *testing.T) {
	clock := &testClock{}
	e := newEnvWith(t, nil, func(d *tools.Deps) {
		d.SecretRequests = failingUse{d.SecretRequests}
		d.Now = clock.Now
	})
	alice := e.newUser(t, "alice@example.test")
	asked, raw := askSecrets(t, e, alice, map[string]any{"names": []string{"API_KEY"}})
	e.submit(t, alice, raw, map[string]string{"API_KEY": "key-1"})
	args := map[string]any{"server_id": alice.serverID, "name": "web", "secrets_from": asked.RequestID,
		"files": []map[string]any{{"name": "compose.yml", "content": secretCompose}}}
	s := e.session(t, alice)
	text, isErr := call(t, s, "save_app", args)
	if !isErr || !strings.Contains(text, "could not be marked used") {
		t.Fatalf("save whose MarkUsed failed = %q (isError %v)", text, isErr)
	}
	if n := e.agent.count(command.TypeAppSave); n != 1 {
		t.Fatalf("the agent received %d saves, want 1", n)
	}
	clock.add(secretreq.ClaimTTL + time.Second)
	if st := secretStatus(t, e, alice, asked.RequestID); st != "used" {
		t.Fatalf("status after the claim's deadline = %s, want used", st)
	}
	if text, isErr := call(t, s, "save_app", args); !isErr || !strings.Contains(text, "used already") {
		t.Fatalf("second save after the claim's deadline = %q (isError %v)", text, isErr)
	}
	if n := e.agent.count(command.TypeAppSave); n != 1 {
		t.Fatalf("the agent received %d saves, want 1", n)
	}
}

// lossyDispatch sends an app.save, then answers an error, as a bus whose
// PUBLISH went through but whose reply was lost.
type lossyDispatch struct {
	port.CommandDispatcher
}

func (l lossyDispatch) Dispatch(ctx context.Context, in port.DispatchInput) (string, error) {
	if _, err := l.CommandDispatcher.Dispatch(ctx, in); err != nil {
		return "", err
	}
	if in.Type == command.TypeAppSave {
		return "", errors.New("redis: i/o timeout")
	}
	return "", nil
}

// newLossyEnv is newEnv whose app saves are sent but answer an error.
func newLossyEnv(t *testing.T) *env {
	t.Helper()
	return newEnvCore(t, nil, func(d *tools.Deps, core *bootstrap.Deps) {
		d.Apps = appuc.NewUseCase(&appuc.Deps{
			CommandDispatcher: lossyDispatch{core.CommandDispatcher}, AppRepository: core.AppRepository,
			AppDomainRepository: core.AppDomainRepository, ServerDomainRepository: core.ServerDomainRepository,
			ServerStorageRepository: core.ServerStorageRepository, Servers: core.ServerRepository, Log: testLog,
		})
	})
}

// TestSaveAppSecretsFromAmbiguousSend: a save whose send answered an error
// after the request was marked dispatched may have reached the agent: the
// request is used, never released, so its ciphertexts go out once.
func TestSaveAppSecretsFromAmbiguousSend(t *testing.T) {
	e := newLossyEnv(t)
	alice := e.newUser(t, "alice@example.test")
	asked, raw := askSecrets(t, e, alice, map[string]any{"names": []string{"API_KEY"}})
	e.submit(t, alice, raw, map[string]string{"API_KEY": "key-1"})
	args := map[string]any{"server_id": alice.serverID, "name": "web", "secrets_from": asked.RequestID,
		"files": []map[string]any{{"name": "compose.yml", "content": secretCompose}}}
	s := e.session(t, alice)
	text, isErr := call(t, s, "save_app", args)
	if !isErr || !strings.Contains(text, "secret request "+asked.RequestID+" is used") {
		t.Fatalf("save whose send answered an error = %q (isError %v)", text, isErr)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.agent.count(command.TypeAppSave) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := e.agent.count(command.TypeAppSave); n != 1 {
		t.Fatalf("the agent received %d saves, want 1", n)
	}
	if st := secretStatus(t, e, alice, asked.RequestID); st != "used" {
		t.Fatalf("status after an ambiguous send = %s, want used", st)
	}
	if text, isErr := call(t, s, "save_app", args); !isErr || !strings.Contains(text, "used already") {
		t.Fatalf("second save = %q (isError %v)", text, isErr)
	}
	if n := e.agent.count(command.TypeAppSave); n != 1 {
		t.Fatalf("the agent received %d saves, want 1", n)
	}
}

// TestSaveAppAmbiguousSendNamesTheApp: a create whose send answered an
// error may have reached the agent, so the error names the id assigned to
// it, the one the agent got.
func TestSaveAppAmbiguousSendNamesTheApp(t *testing.T) {
	e := newLossyEnv(t)
	alice := e.newUser(t, "alice@example.test")
	got := make(chan string, 1)
	e.agent.on(command.TypeAppSave, func(raw json.RawMessage) reply {
		var in command.SaveAppRequest
		_ = codec.DecodePayload(raw, &in)
		got <- in.App.AppID
		return reply{Payload: command.SaveAppResponse{AppID: in.App.AppID}}
	})
	text, isErr := call(t, e.session(t, alice), "save_app", map[string]any{"server_id": alice.serverID, "name": "web",
		"files": []map[string]any{{"name": "compose.yml", "content": compose}}})
	if !isErr {
		t.Fatalf("save whose send answered an error = %q", text)
	}
	var appID string
	select {
	case appID = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the agent received no save")
	}
	if appID == "" || !strings.Contains(text, "pass app_id "+appID) {
		t.Fatalf("error = %q, want it to name app_id %q", text, appID)
	}
}
