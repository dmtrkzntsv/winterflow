package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// mustWriteRevision stores a revision directly through the repository's
// writeRevision helper, failing the test on error.
func mustWriteRevision(t *testing.T, r *Repository, appID string, rev string, config []byte, vars, files []resolvedItem) string {
	t.Helper()
	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), appID, rev)
	if err := r.writeRevision(revDir, config, vars, files); err != nil {
		t.Fatalf("writeRevision: %v", err)
	}
	return revDir
}

func TestWriteAndReadRevisionRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	config := []byte(`{
		"name": "demo",
		"files":[{"filename":"secret.env","is_encrypted":true},{"filename":"compose.yml","is_encrypted":false}],
		"variables":[{"name":"API_KEY","is_encrypted":true},{"name":"PORT","is_encrypted":false}]
	}`)
	vars := []resolvedItem{
		{name: "API_KEY", content: []byte("topsecret")},
		{name: "PORT", content: []byte("8080")},
	}
	files := []resolvedItem{
		{name: "compose.yml", content: []byte("services: {}")},
		{name: "secret.env", content: []byte("KEY=VALUE")},
		{name: "conf/nested.txt", content: []byte("nested")},
	}
	revDir := mustWriteRevision(t, r, "demo", "1", config, vars, files)

	payload, err := r.readRevision(revDir, "demo")
	if err != nil {
		t.Fatalf("readRevision: %v", err)
	}
	if payload.AppID != "demo" {
		t.Errorf("AppID = %q, want demo", payload.AppID)
	}
	if !reflect.DeepEqual(payload.Config, config) {
		t.Errorf("config not preserved")
	}

	gotVars := map[string]command.ContentItem{}
	for _, v := range payload.Variables {
		gotVars[v.Name] = v
	}
	if v := gotVars["PORT"]; string(v.Content) != "8080" || v.Encrypted {
		t.Errorf("PORT = %+v, want plaintext 8080", v)
	}
	if v := gotVars["API_KEY"]; string(v.Content) != command.EncryptedPlaceholder || !v.Encrypted {
		t.Errorf("API_KEY = %+v, want masked placeholder", v)
	}

	gotFiles := map[string]command.ContentItem{}
	for _, f := range payload.Files {
		gotFiles[f.Name] = f
	}
	if f := gotFiles["compose.yml"]; string(f.Content) != "services: {}" || f.Encrypted {
		t.Errorf("compose.yml = %+v, want plaintext", f)
	}
	if f := gotFiles["secret.env"]; string(f.Content) != command.EncryptedPlaceholder || !f.Encrypted {
		t.Errorf("secret.env = %+v, want masked placeholder", f)
	}
	if f := gotFiles["conf/nested.txt"]; string(f.Content) != "nested" {
		t.Errorf("nested file = %+v, want content preserved with relative path", f)
	}
}

func TestListRevisionsFromDisk(t *testing.T) {
	r := newTestRepo(t)

	// Missing app dir is not an error, just empty.
	revs, err := r.listRevisions("nope")
	if err != nil || revs != nil {
		t.Errorf("missing dir: revs=%v err=%v, want nil,nil", revs, err)
	}

	appDir := path.Join(r.cfg.GetAppsTemplatesDir(), "demo")
	for _, name := range []string{"2", "10", "1", "junk"} {
		if err := os.MkdirAll(path.Join(appDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A stray file must be ignored.
	if err := os.WriteFile(path.Join(appDir, "5"), []byte("file, not dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	revs, err = r.listRevisions("demo")
	if err != nil {
		t.Fatalf("listRevisions: %v", err)
	}
	if want := []uint32{1, 2, 10}; !reflect.DeepEqual(revs, want) {
		t.Errorf("listRevisions = %v, want %v", revs, want)
	}
}

func TestRenderSubstitutesVariables(t *testing.T) {
	r := newTestRepo(t)
	files := []resolvedItem{
		{name: "compose.yml", content: []byte("image: app:${TAG}")},
		{name: "conf/app.conf", content: []byte("port=${PORT:-9090}")},
	}
	revDir := mustWriteRevision(t, r, "demo", "1", []byte(`{}`), nil, files)

	vars := []resolvedItem{{name: "TAG", content: []byte("v1")}}
	if err := r.render(revDir, "demo", vars); err != nil {
		t.Fatalf("render: %v", err)
	}

	runDir := path.Join(r.cfg.GetAppsDir(), "demo")
	got, err := os.ReadFile(path.Join(runDir, "compose.yml"))
	if err != nil {
		t.Fatalf("read rendered compose.yml: %v", err)
	}
	if string(got) != "image: app:v1" {
		t.Errorf("compose.yml = %q, want image: app:v1", got)
	}
	got, err = os.ReadFile(path.Join(runDir, "conf", "app.conf"))
	if err != nil {
		t.Fatalf("read rendered nested file: %v", err)
	}
	if string(got) != "port=9090" {
		t.Errorf("app.conf = %q, want default-substituted port=9090", got)
	}

	if !r.appRunDirExists("demo") {
		t.Error("appRunDirExists = false after render, want true")
	}
	if r.appRunDirExists("ghost") {
		t.Error("appRunDirExists = true for unknown app, want false")
	}
}

func TestRenderFailsOnMissingMandatoryVariable(t *testing.T) {
	r := newTestRepo(t)
	files := []resolvedItem{
		{name: "compose.yml", content: []byte("secret: ${WF_TEST_ABSENT_VAR:?required}")},
	}
	revDir := mustWriteRevision(t, r, "demo", "1", []byte(`{}`), nil, files)

	if err := r.render(revDir, "demo", nil); err == nil {
		t.Error("expected error for unset mandatory variable")
	}
}

func TestPruneRevisionsRemovesOldest(t *testing.T) {
	r := newTestRepo(t)
	appDir := path.Join(r.cfg.GetAppsTemplatesDir(), "demo")
	for _, rev := range []string{"1", "2", "3", "4", "5"} {
		if err := os.MkdirAll(path.Join(appDir, rev), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	r.pruneRevisions("demo", []uint32{1, 2, 3, 4, 5})

	revs, err := r.listRevisions("demo")
	if err != nil {
		t.Fatalf("listRevisions: %v", err)
	}
	if want := []uint32{3, 4, 5}; !reflect.DeepEqual(revs, want) {
		t.Errorf("revisions after prune = %v, want %v", revs, want)
	}
}

func TestPreviousValuesReadsLatestRevision(t *testing.T) {
	r := newTestRepo(t)

	// No revisions: empty maps, not nil.
	vars, files := r.previousValues("nope")
	if len(vars) != 0 || len(files) != 0 {
		t.Errorf("expected empty maps for unknown app, got %v %v", vars, files)
	}

	mustWriteRevision(t, r, "demo", "1", []byte(`{}`),
		[]resolvedItem{{name: "OLD", content: []byte("old-value")}},
		nil)
	mustWriteRevision(t, r, "demo", "2", []byte(`{}`),
		[]resolvedItem{{name: "API_KEY", content: []byte("stored-secret")}},
		[]resolvedItem{{name: "secret.env", content: []byte("KEY=VALUE")}})

	vars, files = r.previousValues("demo")
	if vars["API_KEY"] != "stored-secret" {
		t.Errorf("vars = %v, want API_KEY from latest revision", vars)
	}
	if _, ok := vars["OLD"]; ok {
		t.Errorf("vars = %v, must not include older revision values", vars)
	}
	if files["secret.env"] != "KEY=VALUE" {
		t.Errorf("files = %v, want secret.env content", files)
	}
}

func TestListApps(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	// Missing root dir: empty, no error.
	apps, err := r.ListApps(ctx)
	if err != nil {
		t.Fatalf("ListApps (no dir): %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("ListApps (no dir) = %v, want empty", apps)
	}

	cfg, _ := json.Marshal(model.App{ID: "stale-id", Name: "My App", Icon: "box"})
	mustWriteRevision(t, r, "app-1", "1", cfg, nil, nil)
	mustWriteRevision(t, r, "app-1", "2", cfg, nil, nil)

	// App dir with no revision subdirs is skipped.
	if err := os.MkdirAll(path.Join(r.cfg.GetAppsTemplatesDir(), "empty-app"), 0o755); err != nil {
		t.Fatal(err)
	}
	// App with an unparseable config is skipped.
	mustWriteRevision(t, r, "broken-app", "1", []byte("not json"), nil, nil)
	// Stray file at the root is skipped.
	if err := os.WriteFile(path.Join(r.cfg.GetAppsTemplatesDir(), "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	apps, err = r.ListApps(ctx)
	if err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("ListApps = %+v, want exactly one app", apps)
	}
	if apps[0].ID != "app-1" {
		t.Errorf("app ID = %q, want dir name app-1 (authoritative over config)", apps[0].ID)
	}
	if apps[0].Name != "My App" || apps[0].Icon != "box" {
		t.Errorf("app metadata = %+v, want name/icon from config.json", apps[0])
	}
}

func TestGetApp(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.GetApp(ctx, "nope", 0); err == nil || !strings.Contains(err.Error(), "no revisions") {
		t.Errorf("GetApp unknown app err = %v, want no-revisions error", err)
	}

	mustWriteRevision(t, r, "demo", "1", []byte(`{"name":"one"}`), nil, nil)
	mustWriteRevision(t, r, "demo", "2", []byte(`{"name":"two"}`), nil, nil)

	// Revision 0 resolves to the latest.
	resp, err := r.GetApp(ctx, "demo", 0)
	if err != nil {
		t.Fatalf("GetApp latest: %v", err)
	}
	if resp.Revision != 2 || string(resp.App.Config) != `{"name":"two"}` {
		t.Errorf("latest = rev %d config %s, want rev 2 config two", resp.Revision, resp.App.Config)
	}
	if want := []uint32{1, 2}; !reflect.DeepEqual(resp.AvailableRevisions, want) {
		t.Errorf("AvailableRevisions = %v, want %v", resp.AvailableRevisions, want)
	}

	// Explicit older revision.
	resp, err = r.GetApp(ctx, "demo", 1)
	if err != nil {
		t.Fatalf("GetApp rev 1: %v", err)
	}
	if resp.Revision != 1 || string(resp.App.Config) != `{"name":"one"}` {
		t.Errorf("rev 1 = rev %d config %s", resp.Revision, resp.App.Config)
	}

	// Nonexistent revision.
	if _, err := r.GetApp(ctx, "demo", 99); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("GetApp rev 99 err = %v, want not-found error", err)
	}
}

func TestRenameApp(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if err := r.RenameApp(ctx, "nope", "x"); err == nil {
		t.Error("expected error renaming app with no revisions")
	}

	mustWriteRevision(t, r, "demo", "1", []byte(`{"name":"old","icon":"box"}`), nil, nil)
	mustWriteRevision(t, r, "demo", "2", []byte(`{"name":"old","icon":"box"}`), nil, nil)

	if err := r.RenameApp(ctx, "demo", "renamed"); err != nil {
		t.Fatalf("RenameApp: %v", err)
	}

	raw, err := os.ReadFile(path.Join(r.cfg.GetAppsTemplatesDir(), "demo", "2", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("updated config is not valid JSON: %v", err)
	}
	if cfg["name"] != "renamed" || cfg["icon"] != "box" {
		t.Errorf("config after rename = %v, want name updated and other fields kept", cfg)
	}

	// Only the latest revision is touched.
	raw, _ = os.ReadFile(path.Join(r.cfg.GetAppsTemplatesDir(), "demo", "1", "config.json"))
	if !strings.Contains(string(raw), `"old"`) {
		t.Errorf("older revision was modified: %s", raw)
	}

	// Malformed latest config is an error.
	mustWriteRevision(t, r, "broken", "1", []byte("not json"), nil, nil)
	if err := r.RenameApp(ctx, "broken", "x"); err == nil {
		t.Error("expected error for unparseable config")
	}
}

func TestGetAppsStatusWithoutRunningApps(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	// Missing templates root: empty, no error.
	statuses, err := r.GetAppsStatus(ctx)
	if err != nil {
		t.Fatalf("GetAppsStatus (no dir): %v", err)
	}
	if len(statuses) != 0 {
		t.Errorf("GetAppsStatus (no dir) = %v, want empty", statuses)
	}

	// An app that has stored revisions but no rendered run dir yields Unknown
	// without shelling out to docker (composePS short-circuits on the missing
	// directory).
	mustWriteRevision(t, r, "demo", "1", []byte(`{}`), nil, nil)
	if err := os.WriteFile(path.Join(r.cfg.GetAppsTemplatesDir(), "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	statuses, err = r.GetAppsStatus(ctx)
	if err != nil {
		t.Fatalf("GetAppsStatus: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("GetAppsStatus = %+v, want one entry", statuses)
	}
	if statuses[0].AppID != "demo" || statuses[0].StatusCode != command.ContainerStatusUnknown {
		t.Errorf("status = %+v, want demo/unknown", statuses[0])
	}
	if len(statuses[0].Containers) != 0 {
		t.Errorf("containers = %+v, want none", statuses[0].Containers)
	}
}

func TestSaveAppRequiresAppID(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.SaveApp(context.Background(), command.AppPayload{}); err == nil {
		t.Error("expected error for empty app_id")
	}
}

func TestControlActionsWithoutRevisionsFail(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	// No run dir and no stored revisions: redeployLatest must refuse without
	// ever reaching docker.
	if err := r.StartApp(ctx, "ghost"); err == nil || !strings.Contains(err.Error(), "no revisions") {
		t.Errorf("StartApp err = %v, want no-revisions error", err)
	}
	if err := r.RestartApp(ctx, "ghost"); err == nil || !strings.Contains(err.Error(), "no revisions") {
		t.Errorf("RestartApp err = %v, want no-revisions error", err)
	}
	if err := r.UpdateApp(ctx, "ghost"); err == nil || !strings.Contains(err.Error(), "no revisions") {
		t.Errorf("UpdateApp err = %v, want no-revisions error", err)
	}
}

func TestStopAppMissingIsNoop(t *testing.T) {
	r := newTestRepo(t)
	if err := r.StopApp(context.Background(), "ghost"); err != nil {
		t.Errorf("StopApp on missing app = %v, want nil", err)
	}
}

func TestDeleteAppRemovesStoredRevisions(t *testing.T) {
	r := newTestRepo(t)
	mustWriteRevision(t, r, "demo", "1", []byte(`{}`), nil, nil)

	// No run dir exists, so compose down is a no-op and no docker is invoked.
	if err := r.DeleteApp(context.Background(), "demo"); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if _, err := os.Stat(path.Join(r.cfg.GetAppsTemplatesDir(), "demo")); !os.IsNotExist(err) {
		t.Errorf("templates dir still present after delete (stat err = %v)", err)
	}
}

func TestProjectName(t *testing.T) {
	if got := projectName("my-app"); got != "wf-my-app" {
		t.Errorf("projectName = %q, want wf-my-app", got)
	}
	// Path components are stripped so an app id can never escape into another
	// compose project namespace.
	if got := projectName("../evil"); got != "wf-evil" {
		t.Errorf("projectName traversal = %q, want wf-evil", got)
	}
}

func TestListRevisionsPropagatesReadErrors(t *testing.T) {
	r := newTestRepo(t)
	// The app "dir" is a regular file: ReadDir fails with ENOTDIR, which is not
	// a not-exist and must surface.
	if err := os.MkdirAll(r.cfg.GetAppsTemplatesDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path.Join(r.cfg.GetAppsTemplatesDir(), "demo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.listRevisions("demo"); err == nil {
		t.Error("expected error when app path is not a directory")
	}
	// The same failure must propagate through GetApp and RenameApp.
	if _, err := r.GetApp(context.Background(), "demo", 0); err == nil {
		t.Error("expected GetApp to propagate listRevisions error")
	}
	if err := r.RenameApp(context.Background(), "demo", "x"); err == nil {
		t.Error("expected RenameApp to propagate listRevisions error")
	}
	if err := r.StartApp(context.Background(), "demo"); err == nil {
		t.Error("expected StartApp (redeploy) to propagate listRevisions error")
	}
}

func TestListAppsAndStatusPropagateRootErrors(t *testing.T) {
	r := newTestRepo(t)
	// apps_templates itself is a regular file: ReadDir fails with ENOTDIR.
	if err := os.WriteFile(r.cfg.GetAppsTemplatesDir(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ListApps(context.Background()); err == nil {
		t.Error("expected ListApps error when templates root is not a directory")
	}
	if _, err := r.GetAppsStatus(context.Background()); err == nil {
		t.Error("expected GetAppsStatus error when templates root is not a directory")
	}
}

func TestReadRevisionMissingConfig(t *testing.T) {
	r := newTestRepo(t)
	revDir := t.TempDir()
	if _, err := r.readRevision(revDir, "demo"); err == nil {
		t.Error("expected error when config.json is missing")
	}
}

func TestRenameAppMissingConfig(t *testing.T) {
	r := newTestRepo(t)
	// Revision dir exists but has no config.json.
	if err := os.MkdirAll(path.Join(r.cfg.GetAppsTemplatesDir(), "demo", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.RenameApp(context.Background(), "demo", "x"); err == nil {
		t.Error("expected error when latest revision has no config.json")
	}
}

func TestRenderWithoutFilesDirIsNoop(t *testing.T) {
	r := newTestRepo(t)
	// A revision without a files/ subdir renders nothing but is not an error.
	revDir := path.Join(r.cfg.GetAppsTemplatesDir(), "demo", "1")
	if err := os.MkdirAll(revDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.render(revDir, "demo", nil); err != nil {
		t.Errorf("render without files dir = %v, want nil", err)
	}
}

func TestWriteRevisionFailsWhenPathBlocked(t *testing.T) {
	r := newTestRepo(t)
	// A regular file sits where the revision dir must be created.
	blocked := path.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.writeRevision(path.Join(blocked, "1"), []byte(`{}`), nil, nil); err == nil {
		t.Error("expected error when revision path cannot be created")
	}

	// A file item with an empty name resolves to the files/ directory itself
	// and must fail rather than clobber it.
	revDir := path.Join(t.TempDir(), "1")
	err := r.writeRevision(revDir, []byte(`{}`), nil, []resolvedItem{{name: "", content: []byte("x")}})
	if err == nil {
		t.Error("expected error for file item with empty name")
	}
}

func TestRenderFailsWhenRunRootBlocked(t *testing.T) {
	r := newTestRepo(t)
	revDir := mustWriteRevision(t, r, "demo", "1", []byte(`{}`), nil,
		[]resolvedItem{{name: "compose.yml", content: []byte("services: {}")}})

	// A regular file occupies the apps/ root, so the run dir cannot be created.
	if err := os.WriteFile(r.cfg.GetAppsDir(), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.render(revDir, "demo", nil); err == nil {
		t.Error("expected error when run dir cannot be created")
	}
}

func TestToContainerStatuses(t *testing.T) {
	got := toContainerStatuses([]composePS{
		{ID: "abc", Name: "web", State: "running", ExitCode: 0},
		{ID: "def", Name: "db", State: "exited", ExitCode: 137},
	})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ContainerID != "abc" || got[0].Name != "web" || got[0].StatusCode != command.ContainerStatusActive {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].StatusCode != command.ContainerStatusProblematic || got[1].ExitCode != 137 {
		t.Errorf("second = %+v", got[1])
	}
}
