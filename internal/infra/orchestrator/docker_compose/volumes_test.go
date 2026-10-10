package dockercompose

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

func testLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

const volumeLSOut = `{"Driver":"local","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.volume=data","Name":"wf-app-1_data"}
{"Driver":"local","Labels":"com.docker.compose.project=wf-gone,com.docker.compose.volume=db","Name":"wf-gone_db"}
{"Driver":"local","Labels":"com.docker.compose.project=twillingate-cloud,com.docker.compose.volume=pgdata","Name":"twillingate-cloud_pgdata"}
{"Driver":"local","Labels":"","Name":"plain"}
{"Driver":"local","Labels":"com.docker.volume.anonymous=","Name":"0f9c6dc6d5ad"}
not json
`

const psOut = `{"Names":"a","Mounts":"wf-app-1_data,/srv/host"}
{"Names":"b","Mounts":"plain"}
{"Names":"c","Mounts":""}
`

func TestParseVolumeLS(t *testing.T) {
	vols := parseVolumeLS([]byte(volumeLSOut), testLog())
	if len(vols) != 5 {
		t.Fatalf("want 5 volumes (malformed line skipped), got %d", len(vols))
	}
	by := map[string]command.Volume{}
	for _, v := range vols {
		by[v.Name] = v
	}
	if v := by["wf-app-1_data"]; v.AppID != "app-1" || v.Project != "wf-app-1" || v.Driver != "local" {
		t.Errorf("wf volume: %+v", v)
	}
	if v := by["twillingate-cloud_pgdata"]; v.AppID != "" || v.Project != "twillingate-cloud" {
		t.Errorf("foreign compose volume: %+v", v)
	}
	if v := by["0f9c6dc6d5ad"]; !v.Anonymous {
		t.Errorf("anonymous: %+v", v)
	}
	if v := by["plain"]; v.AppID != "" || v.Project != "" || v.Anonymous {
		t.Errorf("plain: %+v", v)
	}
}

func TestParseMountedVolumes(t *testing.T) {
	m := parseMountedVolumes([]byte(psOut))
	if !m["wf-app-1_data"] || !m["plain"] {
		t.Fatalf("mounted = %v", m)
	}
	if m[""] {
		t.Fatal("empty Mounts must not produce an empty name")
	}
}

func TestAnnotateVolumes(t *testing.T) {
	vols := parseVolumeLS([]byte(volumeLSOut), testLog())
	annotateVolumes(vols, parseMountedVolumes([]byte(psOut)), func(id string) bool { return id == "app-1" })
	by := map[string]command.Volume{}
	for _, v := range vols {
		by[v.Name] = v
	}
	if v := by["wf-app-1_data"]; !v.InUse || v.Orphan {
		t.Errorf("app-1 data: %+v", v)
	}
	if v := by["wf-gone_db"]; v.InUse || !v.Orphan {
		t.Errorf("gone db must be orphan: %+v", v)
	}
	if v := by["twillingate-cloud_pgdata"]; v.Orphan {
		t.Errorf("non-winterflow project is never orphan: %+v", v)
	}
	if v := by["plain"]; !v.InUse {
		t.Errorf("plain is mounted by a non-compose container: %+v", v)
	}
}

func TestSortVolumes(t *testing.T) {
	vols := []command.Volume{
		{Name: "z-anon", Anonymous: true},
		{Name: "b-plain"},
		{Name: "x", AppID: "b"},
		{Name: "a-plain"},
		{Name: "y", AppID: "a"},
	}
	sortVolumes(vols)
	got := []string{}
	for _, v := range vols {
		got = append(got, v.Name)
	}
	want := []string{"y", "x", "a-plain", "b-plain", "z-anon"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestCreateDeleteVolumeRejectBadNames(t *testing.T) {
	r := newTestRepo(t)
	for _, n := range []string{"", "-rf", "../x"} {
		if err := r.CreateVolume(t.Context(), n); err == nil {
			t.Errorf("create %q must fail validation", n)
		}
		if err := r.DeleteVolume(t.Context(), n); err == nil {
			t.Errorf("delete %q must fail validation", n)
		}
	}
}

func TestAppExistsUsesAppDir(t *testing.T) {
	r := newTestRepo(t)
	if err := os.MkdirAll(r.appDataDir("app-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !r.appExists("app-1") || r.appExists("nope") {
		t.Fatal("appExists must reflect apps-data dirs")
	}
}

func TestComposeDownVerb(t *testing.T) {
	if got := strings.Join(composeDownVerb(false), " "); got != "down --remove-orphans" {
		t.Fatalf("keep volumes: %q", got)
	}
	if got := strings.Join(composeDownVerb(true), " "); got != "down -v --remove-orphans" {
		t.Fatalf("delete volumes: %q", got)
	}
}

func TestProjectLabelFilter(t *testing.T) {
	if got := projectLabelFilter("app-1"); got != "label=com.docker.compose.project=wf-app-1" {
		t.Fatalf("filter = %q", got)
	}
	// app ids are path-cleaned like projectName
	if got := projectLabelFilter("../evil"); got != "label=com.docker.compose.project=wf-evil" {
		t.Fatalf("filter must use filepath.Base: %q", got)
	}
}

// Without Docker the sweep commands fail; delete must still succeed, remove
// the dir, and report what it could not clean up as warnings, not errors.
func TestDeleteAppWithVolumesNeverFailsOnCleanup(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")
	t.Setenv("PATH", t.TempDir()) // no docker binary → every exec fails
	warnings, swept, err := r.DeleteApp(context.Background(), "app-1", true)
	if err != nil {
		t.Fatalf("delete must not fail on cleanup errors: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("failed cleanup must be reported as warnings")
	}
	if !swept {
		t.Fatal("the sweep ran, so volumesDeleted must be reported")
	}
	if r.appExists("app-1") {
		t.Fatal("app dir must be removed")
	}
}

// A delete for an app the agent does not have must not sweep: otherwise a
// member could destroy orphan volumes an admin deliberately kept.
func TestDeleteAppMissingNeverSweeps(t *testing.T) {
	r := newTestRepo(t)
	t.Setenv("PATH", t.TempDir()) // any docker exec would yield a warning
	warnings, swept, err := r.DeleteApp(context.Background(), "missing", true)
	if err != nil {
		t.Fatalf("deleting a missing app is a no-op: %v", err)
	}
	if len(warnings) != 0 || swept {
		t.Fatalf("no sweep must run for a missing app: warnings=%v swept=%v", warnings, swept)
	}
}

// "." and ".." survive filepath.Base and would make RemoveAll wipe the whole
// apps-data dir (or the data dir itself, certs included).
func TestDeleteAppRejectsTraversalIDs(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")
	for _, id := range []string{"", ".", "..", "/", "a/.."} {
		if _, _, err := r.DeleteApp(context.Background(), id, false); err == nil {
			t.Errorf("DeleteApp(%q) must fail", id)
		}
	}
	if !r.appExists("app-1") {
		t.Fatal("other apps must survive a rejected delete")
	}
}

func TestSweepErrorsIncludeStderr(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'daemon is down' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	r := newTestRepo(t)
	warnings := r.sweepAppVolumes(context.Background(), "app-1")
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "daemon is down") {
		t.Fatalf("warnings must carry stderr: %v", warnings)
	}
	if _, err := r.ListVolumes(context.Background()); err == nil || !strings.Contains(err.Error(), "daemon is down") {
		t.Fatalf("ListVolumes error must carry stderr: %v", err)
	}
}

// Every orchestrator entry point that takes an app id from a command payload
// must refuse a path-like id before touching disk: filepath.Base("..") is
// "..", so appDataDir("..") is the agent data dir itself.
func TestEntryPointsRejectPathLikeAppIDs(t *testing.T) {
	ctx := context.Background()
	for _, id := range []string{"", ".", "..", "/", "a/..", "x/../..", "a/b"} {
		r := newTestRepo(t)
		dataDir := filepath.Dir(r.cfg.GetAppsDataDir())
		before := dirListing(t, dataDir)

		calls := map[string]func() error{
			"SaveApp": func() error {
				_, err := r.SaveApp(ctx, command.AppPayload{AppID: id, Config: []byte(`{"name":"x"}`)})
				return err
			},
			"SaveAppDraft": func() error {
				_, err := r.SaveAppDraft(ctx, command.AppPayload{AppID: id, Config: []byte(`{"name":"x"}`)})
				return err
			},
			"RenameApp":  func() error { return r.RenameApp(ctx, id, "n") },
			"StartApp":   func() error { return r.StartApp(ctx, id) },
			"StopApp":    func() error { return r.StopApp(ctx, id) },
			"RestartApp": func() error { return r.RestartApp(ctx, id) },
			"UpdateApp":  func() error { return r.UpdateApp(ctx, id) },
			"Rollback":   func() error { _, err := r.Rollback(ctx, id, "abc"); return err },
			"Revisions":  func() error { _, _, err := r.Revisions(ctx, id); return err },
			"GetApp":     func() error { _, err := r.GetApp(ctx, id); return err },
			"GetLogs":    func() error { _, err := r.GetLogs(ctx, command.GetLogsRequest{AppID: id}); return err },
		}
		for name, call := range calls {
			if err := call(); err == nil {
				t.Errorf("%s(%q) must fail", name, id)
			}
		}
		if r.DeployedRevision(id) != "" {
			t.Errorf("DeployedRevision(%q) must be empty", id)
		}
		if after := dirListing(t, dataDir); after != before {
			t.Errorf("id %q touched disk outside apps-data:\nbefore:\n%s\nafter:\n%s", id, before, after)
		}
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
