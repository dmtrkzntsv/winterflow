package command

import (
	"encoding/json"
	"errors"
	"testing"

	"winterflow/internal/domain/model"
)

func TestStorageRequestsValidate(t *testing.T) {
	good := model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/backup"}}
	bad := model.Storage{Name: "Disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/backup"}}
	if err := (TestStorageRequest{good}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (SaveStorageRequest{Storage: good}).Validate(); err != nil {
		t.Fatal(err)
	}
	if (TestStorageRequest{bad}).Validate() == nil || (SaveStorageRequest{Storage: bad}).Validate() == nil {
		t.Fatal("an invalid storage must be refused")
	}
	if err := (DeleteStorageRequest{Name: "disk"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if (DeleteStorageRequest{Name: "../x"}).Validate() == nil {
		t.Fatal("an invalid name must be refused")
	}
}

// The request bodies are the storage itself, not a wrapper object.
func TestStorageRequestIsFlat(t *testing.T) {
	var req SaveStorageRequest
	raw := `{"name":"hetzner","type":"s3","create":true,"s3":{"endpoint":"https://s3.example.com","bucket":"b","access_key_id":"A","secret_access_key":"CT"}}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil || req.Name != "hetzner" || req.S3.SecretAccessKey != "CT" || !req.Create {
		t.Fatalf("decoded = %+v, %v", req, err)
	}
}

// The index row is the storage as the agent reported it.
func TestStorageInfoSummary(t *testing.T) {
	info := StorageInfo{Storage: model.Storage{Name: "d", Type: model.StorageLocal}, Location: "/mnt"}
	if got := info.Summary(); got != (model.StorageSummary{Name: "d", Type: model.StorageLocal, Location: "/mnt"}) {
		t.Fatalf("summary = %+v", got)
	}
}

func TestContentItemName(t *testing.T) {
	if n := (ContentItem{ID: "id", Name: "name"}).ItemName(); n != "name" {
		t.Fatalf("name = %q", n)
	}
	if n := (ContentItem{ID: "id"}).ItemName(); n != "id" {
		t.Fatalf("fallback = %q", n)
	}
}

func TestSaveAppRequestRefusesReservedVariables(t *testing.T) {
	ok := SaveAppRequest{App: AppPayload{AppID: "a1", Variables: []ContentItem{{Name: "PORT"}}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []ContentItem{{Name: "WF_STORAGE_X_PATH"}, {ID: "WF_BACKUP_DB_PATH"}} {
		r := SaveAppRequest{App: AppPayload{AppID: "a1", Variables: []ContentItem{v}}}
		if err := r.Validate(); !errors.Is(err, model.ErrReservedVariable) {
			t.Errorf("%+v: err = %v", v, err)
		}
	}
	if (SaveAppRequest{App: AppPayload{AppID: "../x"}}).Validate() == nil {
		t.Fatal("a bad app id must still be refused")
	}
}
