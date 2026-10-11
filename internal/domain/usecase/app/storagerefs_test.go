package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

func TestSaveAppChecksStorageReferences(t *testing.T) {
	ss := newFakeServerStorages(model.StorageSummary{Name: "hetzner", Type: model.StorageS3, Location: "https://s3/b/"})
	ctx := context.Background()
	save := func(p command.AppPayload) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		p.Config = []byte(`{"name":"demo"}`)
		_, err := newStoragesUseCase(disp, ss).SaveApp(ctx, "u", "srv-1", model.App{Name: "demo"}, p, false)
		return disp, err
	}
	file := func(name, content string) command.ContentItem {
		return command.ContentItem{Name: name, Content: []byte(content)}
	}

	for name, p := range map[string]command.AppPayload{
		"known storage": {Files: []command.ContentItem{file("compose.yml", "x: ${WF_STORAGE_HETZNER_BUCKET}")}},
		"no references": {Files: []command.ContentItem{file("compose.yml", "services: {}")}},
		// Only the files compose loads count: compose.yaml wins over compose.yml.
		"shadowed file":      {Files: []command.ContentItem{file("compose.yaml", "services: {}"), file("compose.yml", "${WF_STORAGE_GONE_PATH}")}},
		"not a compose file": {Files: []command.ContentItem{file("compose.yml", "services: {}"), file("notes.md", "${WF_STORAGE_GONE_PATH}")}},
		// A git-sourced app's compose file is checked by the agent.
		"git-sourced": {Files: []command.ContentItem{file("compose.yml", "${WF_STORAGE_GONE_PATH}")}, Source: &command.SourcePayload{RepoURL: "https://x", Branch: "main"}},
		"secret file": {Files: []command.ContentItem{{Name: "compose.yml", Encrypted: true, Content: []byte("${WF_STORAGE_GONE_PATH}")}}},
	} {
		if disp, err := save(p); err != nil || len(disp.all) != 1 {
			t.Errorf("%s: err %v, dispatches %d", name, err, len(disp.all))
		}
	}

	for name, tc := range map[string]struct {
		p    command.AppPayload
		want string
	}{
		"unknown storage": {command.AppPayload{Files: []command.ContentItem{file("compose.yml", "${WF_STORAGE_GONE_PATH}")}},
			`${WF_STORAGE_GONE_PATH}: there is no storage "gone" on this server`},
		"override file": {command.AppPayload{Files: []command.ContentItem{file("compose.yml", ""), file("compose.override.yml", "$WF_STORAGE_GONE_TYPE")}},
			`there is no storage "gone"`},
		"backup reference": {command.AppPayload{Files: []command.ContentItem{{ID: "compose.yml", Content: []byte("${WF_BACKUP_DB_PATH}")}}},
			`there is no backup "db" in this app`},
	} {
		disp, err := save(tc.p)
		if !errors.Is(err, model.ErrStorageRef) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(disp.all) != 0 {
			t.Errorf("%s: reached the agent", name)
		}
	}
}

func TestSaveAppRefusesReservedVariables(t *testing.T) {
	disp := &recordingDispatcher{}
	_, err := newStoragesUseCase(disp, nil).SaveApp(context.Background(), "u", "srv-1", model.App{Name: "demo"},
		command.AppPayload{Config: []byte(`{}`), Variables: []command.ContentItem{{Name: "PORT"}, {Name: "WF_STORAGE_HETZNER_BUCKET"}}}, false)
	if !errors.Is(err, model.ErrReservedVariable) || len(disp.all) != 0 {
		t.Fatalf("err = %v, dispatches %d", err, len(disp.all))
	}
}

// Without the index the API can't check; the agent does at deploy.
func TestSaveAppStorageCheckNeedsTheIndex(t *testing.T) {
	disp := &recordingDispatcher{}
	_, err := newStoragesUseCase(disp, nil).SaveApp(context.Background(), "u", "srv-1", model.App{Name: "demo"},
		command.AppPayload{Config: []byte(`{}`), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte("${WF_STORAGE_GONE_PATH}")}}}, false)
	if err != nil || len(disp.all) != 1 {
		t.Fatalf("err = %v, dispatches %d", err, len(disp.all))
	}
}

type failingStorageIndex struct{ *fakeServerStorages }

func (failingStorageIndex) ListForServer(context.Context, string) ([]model.StorageSummary, error) {
	return nil, errors.New("db down")
}

func TestSaveAppStorageCheckIndexError(t *testing.T) {
	disp := &recordingDispatcher{}
	_, err := newStoragesUseCase(disp, failingStorageIndex{newFakeServerStorages()}).SaveApp(context.Background(), "u", "srv-1", model.App{Name: "demo"},
		command.AppPayload{Config: []byte(`{}`), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte("${WF_STORAGE_X_PATH}")}}}, false)
	if err == nil || errors.Is(err, model.ErrStorageRef) || len(disp.all) != 0 {
		t.Fatalf("err = %v, dispatches %d", err, len(disp.all))
	}
}
