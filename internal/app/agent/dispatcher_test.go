package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	return NewDispatcher(dockercompose.NewRepository(cfg, log), nil, log)
}

// fakeIngress is a minimal port.IngressManager test double: it counts reloads
// and returns canned warnings, standing in for the real Caddy manager.
type fakeIngress struct {
	reloads  int
	warnings []string
}

func (f *fakeIngress) Reload(_ context.Context) []string { f.reloads++; return f.warnings }
func (f *fakeIngress) Enabled() bool                     { return true }

func envelope(typ string, payload []byte) *proto.RequestEnvelope {
	return &proto.RequestEnvelope{
		Base:      &proto.BaseMessage{AgentId: "agent-1", MessageId: "req-1"},
		RequestId: "req-1",
		Type:      typ,
		Payload:   payload,
	}
}

func TestDispatchUnsupportedTypeReturnsError(t *testing.T) {
	d := newTestDispatcher(t)
	resp := d.Dispatch(context.Background(), envelope("nope.nothing", nil))
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("unsupported type must fail, got %+v", resp)
	}
	if resp.RequestId != "req-1" {
		t.Fatalf("error response must stay correlated, got %q", resp.RequestId)
	}
}

func TestDispatchMalformedPayloadsReturnCorrelatedErrors(t *testing.T) {
	d := newTestDispatcher(t)
	bad := []byte(`{"this is": not json`)

	for _, typ := range []command.Type{
		command.TypeAppSave,
		command.TypeAppControl,
		command.TypeAppDelete,
		command.TypeAppRename,
		command.TypeRegistryCreate,
		command.TypeRegistryDelete,
		command.TypeNetworkCreate,
		command.TypeNetworkDelete,
		command.TypeVolumeList,
		command.TypeVolumeCreate,
		command.TypeVolumeDelete,
		command.TypeAgentUpdate,
	} {
		resp := d.Dispatch(context.Background(), envelope(string(typ), bad))
		if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
			t.Errorf("%s: malformed payload must fail", typ)
		}
		if resp.RequestId != "req-1" || resp.Type != string(typ) {
			t.Errorf("%s: response not correlated: %+v", typ, resp)
		}
	}
}

func TestDispatchListAppsOnEmptyDataDir(t *testing.T) {
	d := newTestDispatcher(t)
	resp := d.Dispatch(context.Background(), envelope(string(command.TypeAppsList), []byte(`{}`)))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("listing an empty data dir should succeed: %+v", resp)
	}
}

func TestDispatchGetAppUnknownIDFails(t *testing.T) {
	d := newTestDispatcher(t)
	resp := d.Dispatch(context.Background(),
		envelope(string(command.TypeAppGet), []byte(`{"app_id":"missing"}`)))
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("get of unknown app should fail: %+v", resp)
	}
}

func TestDispatchDockerAndLifecycleFilesystemPaths(t *testing.T) {
	d := newTestDispatcher(t)
	ctx := context.Background()

	// registry.list reads ~/.docker/config.json — point DOCKER_CONFIG at a
	// fixture instead of touching the host.
	dockerDir := t.TempDir()
	if err := os.WriteFile(dockerDir+"/config.json",
		[]byte(`{"auths":{"registry.example.com":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", dockerDir)

	resp := d.Dispatch(ctx, envelope(string(command.TypeRegistryList), []byte(`{}`)))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("registry.list: %+v", resp)
	}
	var regs command.ListRegistriesResponse
	if err := json.Unmarshal(resp.Payload, &regs); err != nil || len(regs.Registries) != 1 {
		t.Fatalf("registries payload = %s (%v)", resp.Payload, err)
	}

	// apps.status over an empty data dir: success, no apps.
	resp = d.Dispatch(ctx, envelope(string(command.TypeAppsStatus), []byte(`{}`)))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("apps.status: %+v", resp)
	}

	// app.logs for a never-deployed app: success with no entries.
	resp = d.Dispatch(ctx, envelope(string(command.TypeAppLogs), []byte(`{"app_id":"ghost","tail":10}`)))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("app.logs: %+v", resp)
	}

	// app.delete with nothing on disk: cleanly a no-op.
	resp = d.Dispatch(ctx, envelope(string(command.TypeAppDelete), []byte(`{"app_id":"ghost"}`)))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("app.delete: %+v", resp)
	}
}

func TestDispatchImageTags(t *testing.T) {
	d := newTestDispatcher(t)
	// Empty image is a validation error surfaced as a correlated error reply.
	resp := d.Dispatch(context.Background(), envelope(string(command.TypeImageTags), []byte(`{"image":""}`)))
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("empty image should fail: %+v", resp)
	}
	if resp.RequestId != "req-1" {
		t.Fatalf("error reply not correlated: %+v", resp)
	}
}

func TestDispatchDraftSaveSkipsDeploy(t *testing.T) {
	d := newTestDispatcher(t)
	// Deliberately broken compose: a draft save must still succeed because it
	// never touches docker; a full save on the same payload would fail at
	// compose up.
	body := []byte(`{"draft":true,"app":{"app_id":"a1","config":"e30=","files":[{"name":"compose.yml","content":"bm90IHZhbGlkIHlhbWw="}],"variables":[]}}`)
	resp := d.Dispatch(context.Background(), envelope(string(command.TypeAppSave), body))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("draft save should not deploy: %+v", resp)
	}
	var saved command.SaveAppResponse
	if err := json.Unmarshal(resp.Payload, &saved); err != nil || saved.Revision == "" {
		t.Fatalf("draft save must return the commit hash: %s (%v)", resp.Payload, err)
	}
}

// TestDispatcherReloadsIngressAfterMutations proves reloadIngress fires only
// on a successful mutation: a good draft save triggers exactly one reload, a
// failed delete of a nonexistent app triggers none.
func TestDispatcherReloadsIngressAfterMutations(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ing := &fakeIngress{}
	d := NewDispatcher(dockercompose.NewRepository(cfg, log), ing, log)

	// Draft save never touches docker, so it succeeds cleanly and must reload.
	body := []byte(`{"draft":true,"app":{"app_id":"a1","config":"e30=","files":[{"name":"compose.yml","content":"bm90IHZhbGlkIHlhbWw="}],"variables":[]}}`)
	resp := d.Dispatch(context.Background(), envelope(string(command.TypeAppSave), body))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("draft save should succeed: %+v", resp)
	}
	if ing.reloads != 1 {
		t.Fatalf("successful save must reload ingress exactly once, got %d", ing.reloads)
	}

	// Rolling back an app that was never saved fails at the orchestrator
	// level (no such app/hash), so no reload should happen.
	resp = d.Dispatch(context.Background(),
		envelope(string(command.TypeAppRollback), []byte(`{"app_id":"missing","hash":"deadbeef"}`)))
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("rollback of a nonexistent app unexpectedly succeeded: %+v", resp)
	}
	if ing.reloads != 1 {
		t.Fatalf("failed rollback must not reload ingress, got %d reloads", ing.reloads)
	}
}

// TestSaveResponseCarriesIngressWarnings proves app.save attaches the ingress
// manager's warnings to the response payload on a successful save.
func TestSaveResponseCarriesIngressWarnings(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	ing := &fakeIngress{warnings: []string{"w1"}}
	d := NewDispatcher(dockercompose.NewRepository(cfg, log), ing, log)

	body := []byte(`{"draft":true,"app":{"app_id":"a1","config":"e30=","files":[{"name":"compose.yml","content":"bm90IHZhbGlkIHlhbWw="}],"variables":[]}}`)
	resp := d.Dispatch(context.Background(), envelope(string(command.TypeAppSave), body))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("draft save should succeed: %+v", resp)
	}
	var saved command.SaveAppResponse
	if err := json.Unmarshal(resp.Payload, &saved); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if len(saved.Warnings) != 1 || saved.Warnings[0] != "w1" {
		t.Fatalf("warnings = %v, want [w1]", saved.Warnings)
	}
}

// TestDispatcherNilIngress proves a nil ingress manager (feature disabled, or
// existing tests that don't care about ingress) is a safe no-op.
func TestDispatcherNilIngress(t *testing.T) {
	d := newTestDispatcher(t)
	body := []byte(`{"draft":true,"app":{"app_id":"a1","config":"e30=","files":[{"name":"compose.yml","content":"bm90IHZhbGlkIHlhbWw="}],"variables":[]}}`)
	resp := d.Dispatch(context.Background(), envelope(string(command.TypeAppSave), body))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("draft save with nil ingress should succeed: %+v", resp)
	}
}

func TestDispatchAppDeleteEchoesVolumesDeleted(t *testing.T) {
	d := newTestDispatcher(t)
	t.Setenv("PATH", t.TempDir()) // no docker: the sweep runs and only warns
	if err := os.MkdirAll(os.Getenv("AGENT_DATA_DIR")+"/apps-data/app-1", 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	resp := d.Dispatch(ctx, envelope(string(command.TypeAppDelete), []byte(`{"app_id":"app-1","delete_volumes":true}`)))
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("app.delete: %+v", resp)
	}
	var out command.DeleteAppResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		t.Fatal(err)
	}
	if !out.VolumesDeleted || len(out.Warnings) == 0 {
		t.Fatalf("honoured volume deletion must be echoed with warnings: %+v", out)
	}

	// Not requested, or nothing to sweep: never claimed.
	for name, body := range map[string]string{
		"not requested": `{"app_id":"app-2"}`,
		"missing app":   `{"app_id":"ghost","delete_volumes":true}`,
	} {
		_ = os.MkdirAll(os.Getenv("AGENT_DATA_DIR")+"/apps-data/app-2", 0o755)
		resp = d.Dispatch(ctx, envelope(string(command.TypeAppDelete), []byte(body)))
		out = command.DeleteAppResponse{}
		if err := json.Unmarshal(resp.Payload, &out); err != nil || out.VolumesDeleted {
			t.Fatalf("%s: volumes_deleted must be false: %s (%v)", name, resp.Payload, err)
		}
	}
}

// Every command that turns a payload id into a path or a docker CLI arg must
// be refused at the dispatcher before the orchestrator sees it:
// filepath.Base("..") is "..", so appDataDir("..") is the agent data dir
// itself, and a volume name like "-rf" would parse as a flag.
func TestDispatchRejectsPathLikeIDsAndBadVolumeNames(t *testing.T) {
	d := newTestDispatcher(t)
	ctx := context.Background()
	dataDir := os.Getenv("AGENT_DATA_DIR")
	// A real app that a traversal id could reach (delete "a/.." would wipe
	// the whole data dir).
	body := []byte(`{"draft":true,"app":{"app_id":"app-1","config":"e30=","files":[{"name":"compose.yml","content":"eDogMQo="}],"variables":[]}}`)
	if resp := d.Dispatch(ctx, envelope(string(command.TypeAppSave), body)); resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("seed save: %+v", resp)
	}
	t.Setenv("PATH", t.TempDir()) // no docker: nothing may reach the daemon either
	before := dirListing(t, dataDir)

	appRequests := map[command.Type]func(id string) any{
		command.TypeAppSave: func(id string) any {
			return command.SaveAppRequest{App: command.AppPayload{AppID: id, Config: []byte(`{"name":"x"}`)}}
		},
		command.TypeAppGet:       func(id string) any { return command.GetAppRequest{AppID: id} },
		command.TypeAppRevisions: func(id string) any { return command.GetRevisionsRequest{AppID: id} },
		command.TypeAppRollback:  func(id string) any { return command.RollbackAppRequest{AppID: id, Hash: "abc"} },
		command.TypeAppControl: func(id string) any {
			return command.ControlAppRequest{AppID: id, Action: command.ControlStop}
		},
		command.TypeAppDelete: func(id string) any { return command.DeleteAppRequest{AppID: id, DeleteVolumes: true} },
		command.TypeAppRename: func(id string) any { return command.RenameAppRequest{AppID: id, Name: "n"} },
		command.TypeAppLogs:   func(id string) any { return command.GetLogsRequest{AppID: id} },
	}
	volumeRequests := map[command.Type]func(name string) any{
		command.TypeVolumeCreate: func(n string) any { return command.CreateVolumeRequest{Name: n} },
		command.TypeVolumeDelete: func(n string) any { return command.DeleteVolumeRequest{Name: n} },
	}

	expectRejected := func(typ command.Type, req any) {
		t.Helper()
		payload, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		resp := d.Dispatch(ctx, envelope(string(typ), payload))
		if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
			t.Errorf("%s %s must fail", typ, payload)
		} else if !strings.HasPrefix(resp.Base.Detail, "invalid payload: ") {
			t.Errorf("%s %s must fail validation, got %q", typ, payload, resp.Base.Detail)
		}
	}
	for typ, mk := range appRequests {
		for _, id := range []string{"", ".", "..", "/", "a/..", "x/../..", "a/b", `a\b`} {
			expectRejected(typ, mk(id))
		}
	}
	for typ, mk := range volumeRequests {
		for _, name := range []string{"", "-rf", "--all", "../x", "a/b", ".x"} {
			expectRejected(typ, mk(name))
		}
	}
	if after := dirListing(t, dataDir); after != before {
		t.Errorf("rejected requests touched the data dir:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// dirListing is a recursive name listing of dir (relative, sorted).
func dirListing(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return strings.Join(out, "\n")
}
