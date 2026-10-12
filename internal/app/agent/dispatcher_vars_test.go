package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
