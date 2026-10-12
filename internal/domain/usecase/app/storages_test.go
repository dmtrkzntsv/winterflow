package app

import (
	"context"
	"reflect"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

func newStoragesUseCase(d port.CommandDispatcher, ss port.ServerStorageRepository) *UseCase {
	return NewUseCase(&Deps{
		CommandDispatcher:       d,
		AppRepository:           noopAppRepo{},
		ServerStorageRepository: ss,
		Log:                     logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
	})
}

var disk = model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb"}}

func TestListServerStoragesReconcilesTheIndex(t *testing.T) {
	disp := &recordingDispatcher{}
	ss := newFakeServerStorages(model.StorageSummary{Name: "stale", Type: model.StorageLocal, Location: "/mnt/old"})
	uc := newStoragesUseCase(disp, ss)
	if _, err := uc.ListServerStorages(context.Background(), "u1", "srv-1"); err != nil {
		t.Fatal(err)
	}
	in := disp.last()
	if in.Type != command.TypeStorageList || in.AgentID != "srv-1" || in.UserID != "u1" ||
		in.Payload.(command.ListStoragesRequest).RegistryOnly {
		t.Fatalf("dispatch = %+v", in)
	}
	in.OnResult(port.CommandResult{Success: true, Payload: []byte(
		`{"storages":[{"name":"hetzner","type":"s3","s3":{"endpoint":"https://s3.example.com","bucket":"b","prefix":"","access_key_id":"A","secret_access_key":"<encrypted>","region":"","path_style":false},"location":"https://s3.example.com/b/","apps":["a1"]}]}`)})
	want := []model.StorageSummary{{Name: "hetzner", Type: model.StorageS3, Location: "https://s3.example.com/b/"}}
	if !reflect.DeepEqual(ss.rows["srv-1"], want) {
		t.Fatalf("index = %+v", ss.rows["srv-1"])
	}
	// Failures and undecodable answers leave the index alone.
	in.OnResult(port.CommandResult{Error: "agent down"})
	in.OnResult(port.CommandResult{Success: true, Payload: []byte("{")})
	if !reflect.DeepEqual(ss.rows["srv-1"], want) {
		t.Fatalf("index changed: %+v", ss.rows["srv-1"])
	}
}

// The system's own sync (no user) asks for the registry alone: it never
// shows which apps use a storage.
func TestListServerStoragesSyncSkipsTheApps(t *testing.T) {
	disp := &recordingDispatcher{}
	if _, err := newStoragesUseCase(disp, newFakeServerStorages()).ListServerStorages(context.Background(), "", "srv-1"); err != nil {
		t.Fatal(err)
	}
	if !disp.last().Payload.(command.ListStoragesRequest).RegistryOnly {
		t.Fatalf("dispatch = %+v", disp.last())
	}
}

func TestSaveStorageUpsertsOnConfirmation(t *testing.T) {
	disp := &recordingDispatcher{}
	ss := newFakeServerStorages()
	uc := newStoragesUseCase(disp, ss)
	if _, err := uc.SaveStorage(context.Background(), "u1", "srv-1", command.SaveStorageRequest{Storage: disk}); err != nil {
		t.Fatal(err)
	}
	in := disp.last()
	if in.Type != command.TypeStorageSave || in.Payload.(command.SaveStorageRequest).Name != "disk" {
		t.Fatalf("dispatch = %+v", in)
	}
	in.OnResult(port.CommandResult{Error: "connection test failed"})
	in.OnResult(port.CommandResult{Success: true, Payload: []byte("{")})
	if len(ss.upserted) != 0 {
		t.Fatal("a failed save entered the index")
	}
	in.OnResult(port.CommandResult{Success: true, Payload: []byte(`{"storage":{"name":"disk","type":"local","local":{"path":"/mnt/usb"},"location":"/mnt/usb","apps":[]},"redeployed":[]}`)})
	if len(ss.upserted) != 1 || ss.upserted[0] != (model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb"}) {
		t.Fatalf("upserted = %+v", ss.upserted)
	}
}

func TestTestAndDeleteStorage(t *testing.T) {
	disp := &recordingDispatcher{}
	ss := newFakeServerStorages()
	uc := newStoragesUseCase(disp, ss)
	if _, err := uc.TestStorage(context.Background(), "u1", "srv-1", command.TestStorageRequest{Storage: disk}); err != nil {
		t.Fatal(err)
	}
	if in := disp.last(); in.Type != command.TypeStorageTest || in.OnResult != nil {
		t.Fatalf("a test must not touch the index: %+v", in)
	}
	if _, err := uc.DeleteStorage(context.Background(), "u1", "srv-1", "disk"); err != nil {
		t.Fatal(err)
	}
	in := disp.last()
	in.OnResult(port.CommandResult{Error: "disk is used by Shop"})
	if len(ss.deleted) != 0 {
		t.Fatal("a refused delete left the index")
	}
	in.OnResult(port.CommandResult{Success: true})
	if !reflect.DeepEqual(ss.deleted, []string{"disk"}) {
		t.Fatalf("deleted = %v", ss.deleted)
	}
}

// Without the index (old wiring) everything still dispatches.
func TestStoragesWithoutIndex(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newStoragesUseCase(disp, nil)
	got, err := uc.ServerStorages(context.Background(), "srv-1")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("server storages = %v, %v", got, err)
	}
	_, _ = uc.ListServerStorages(context.Background(), "u1", "srv-1")
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"storages":[]}`)})
	_, _ = uc.SaveStorage(context.Background(), "u1", "srv-1", command.SaveStorageRequest{Storage: disk})
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{}`)})
	_, _ = uc.DeleteStorage(context.Background(), "u1", "srv-1", "disk")
	disp.last().OnResult(port.CommandResult{Success: true})

	ss := newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb"})
	if got, _ := newStoragesUseCase(disp, ss).ServerStorages(context.Background(), "srv-1"); len(got) != 1 {
		t.Fatalf("server storages = %v", got)
	}
}
