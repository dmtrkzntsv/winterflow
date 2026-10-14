package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/grpc/proto"
)

func TestDispatchVarsSyncStoresSnapshot(t *testing.T) {
	d := newTestDispatcher(t)
	resp := dispatchJSON(t, d, command.TypeVarsSync, command.SyncVarsRequest{Revision: 4, ServerID: "s1",
		Org: []command.SharedVar{{Name: "SMTP_HOST", Value: "mail"}}})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("vars.sync failed: %s", resp.Base.Detail)
	}
	var out command.SyncVarsResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil || out.Revision != 4 || out.Undecryptable == nil {
		t.Fatalf("response = %+v, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("AGENT_DATA_DIR"), "vars", "snapshot.json")); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	bad := dispatchJSON(t, d, command.TypeVarsSync, command.SyncVarsRequest{Revision: 5,
		Org: []command.SharedVar{{Name: "WF_SNEAKY", Value: "x"}}})
	if bad.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(bad.Base.Detail, "reserved") {
		t.Fatalf("a reserved name reached the store: %+v", bad.Base)
	}
}

func TestDispatchVarsReload(t *testing.T) {
	d := newTestDispatcher(t)
	resp := dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatal("vars.reload without a worker must fail")
	}
	w := NewReloadWorker(&fakeReloader{}, func(bus.EventKind, []byte) error { return nil }, testLog())
	d.WithReloads(w)
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{Names: []string{"SMTP_PASS"}})
	var out command.ReloadVarsResponse
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS || json.Unmarshal(resp.Payload, &out) != nil || !out.Queued {
		t.Fatalf("queued reload = %+v %s", resp.Base, resp.Payload)
	}
	w.mu.Lock()
	queued := w.pending.names["SMTP_PASS"]
	w.mu.Unlock()
	if !queued {
		t.Fatal("the request was not queued on the worker")
	}
	if bad := dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"../x"}}); bad.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatal("path-like app id accepted")
	}
}

func TestDispatchAppDeleteForgetsQueuedReload(t *testing.T) {
	d := newTestDispatcher(t)
	w := NewReloadWorker(&fakeReloader{}, func(bus.EventKind, []byte) error { return nil }, testLog())
	d.WithReloads(w)
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{"ghost"}})
	// Deleting an app with nothing on disk is a clean no-op success.
	resp := dispatchJSON(t, d, command.TypeAppDelete, command.DeleteAppRequest{AppID: "ghost"})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("app.delete: %+v", resp.Base)
	}
	w.mu.Lock()
	queued := w.pending.apps["ghost"]
	w.mu.Unlock()
	if queued {
		t.Fatal("a deleted app kept its queued reload")
	}
}

func TestDispatchAppsStatusCarriesVarsRevision(t *testing.T) {
	d := newTestDispatcher(t)
	dispatchJSON(t, d, command.TypeVarsSync, command.SyncVarsRequest{Revision: 7})
	resp := dispatchJSON(t, d, command.TypeAppsStatus, command.GetAppsStatusRequest{})
	var out command.GetAppsStatusResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil || out.VarsRevision == nil || *out.VarsRevision != 7 {
		t.Fatalf("apps.status = %s, %v", resp.Payload, err)
	}
}

func TestDispatchVarsReloadOneAppAnswersItsOutcome(t *testing.T) {
	d := newTestDispatcher(t)
	f := &fakeReloader{stale: map[string][]string{"a": {"SMTP_PASS"}, "b": {"X"}},
		failWith: map[string]error{"b": errors.New("compose up failed")}, stopped: map[string]bool{"c": true}}
	w := NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	d.WithReloads(w)

	resp := dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"a"}})
	var out command.ReloadVarsResponse
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS || json.Unmarshal(resp.Payload, &out) != nil ||
		out.AppID != "a" || out.Skipped || len(out.Names) != 1 || out.Names[0] != "SMTP_PASS" {
		t.Fatalf("reloaded = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"b"}})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "compose up failed") {
		t.Fatalf("failed reload = %+v", resp.Base)
	}
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"c"}})
	out = command.ReloadVarsResponse{}
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS || json.Unmarshal(resp.Payload, &out) != nil || !out.Skipped {
		t.Fatalf("stopped app = %+v %s", resp.Base, resp.Payload)
	}

	// A reload that outlives the bound answers the bound.
	f.block = make(chan struct{})
	f.stale["a"] = []string{"SMTP_PASS"}
	d.reloadWait = 50 * time.Millisecond
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"a"}})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "did not end within 50ms") {
		t.Fatalf("slow reload = %+v", resp.Base)
	}
	close(f.block)
}
