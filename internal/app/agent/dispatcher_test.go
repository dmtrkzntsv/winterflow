package agent

import (
	"context"
	"encoding/json"
	"os"
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
