package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/internal/infra/transport/grpc/proto"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

func TestDispatchStorageCommands(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	d := NewDispatcher(dockercompose.NewRepository(config.NewServerConfig("standalone"), log), nil, log)
	ok := proto.ResponseCode_RESPONSE_CODE_SUCCESS
	disk := model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: t.TempDir()}}

	resp := dispatchJSON(t, d, command.TypeStorageTest, command.TestStorageRequest{Storage: disk})
	var tested command.TestStorageResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &tested) != nil || !tested.OK {
		t.Fatalf("storage.test = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeStorageSave, command.SaveStorageRequest{Storage: disk})
	var saved command.SaveStorageResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &saved) != nil || saved.Storage.Name != "disk" {
		t.Fatalf("storage.save = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeStorageList, command.ListStoragesRequest{})
	var listed command.ListStoragesResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &listed) != nil || len(listed.Storages) != 1 ||
		listed.Storages[0].Location != disk.Local.Path || listed.Storages[0].Apps == nil {
		t.Fatalf("storage.list = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeStorageDelete, command.DeleteStorageRequest{Name: "disk"})
	var deleted command.DeleteStorageResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &deleted) != nil || deleted.Name != "disk" {
		t.Fatalf("storage.delete = %+v %s", resp.Base, resp.Payload)
	}
	// A failed save is a command error carrying the reason.
	missing := disk
	missing.Local = &model.LocalStorage{Path: disk.Local.Path + "/gone"}
	resp = dispatchJSON(t, d, command.TypeStorageSave, command.SaveStorageRequest{Storage: missing})
	if resp.Base.ResponseCode == ok || !strings.Contains(resp.Base.Detail, "is the disk mounted") {
		t.Fatalf("failed save = %+v", resp.Base)
	}

	// Invalid payloads never reach the orchestrator.
	for typ, req := range map[command.Type]any{
		command.TypeStorageTest:   command.TestStorageRequest{Storage: model.Storage{Name: "../x", Type: model.StorageLocal}},
		command.TypeStorageSave:   command.SaveStorageRequest{Storage: model.Storage{Name: "x", Type: "ftp"}},
		command.TypeStorageDelete: command.DeleteStorageRequest{Name: "a/b"},
	} {
		resp := dispatchJSON(t, d, typ, req)
		if resp.Base.ResponseCode == ok || !strings.HasPrefix(resp.Base.Detail, "invalid payload: ") {
			t.Errorf("%s: %+v", typ, resp.Base)
		}
	}
}
