package dockercompose

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

// danglingOut is `docker volume ls -q --filter dangling=true`: volumes no
// container (running or stopped) references.
const danglingOut = "wf-gone_db\ntwillingate-cloud_pgdata\n0f9c6dc6d5ad\n"

func danglingSet(out string) map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(out) {
		m[n] = true
	}
	return m
}

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

func TestAnnotateVolumes(t *testing.T) {
	vols := parseVolumeLS([]byte(volumeLSOut), testLog())
	annotateVolumes(vols, danglingSet(danglingOut), func(id string) bool { return id == "app-1" })
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
		t.Errorf("plain is not dangling, so it is in use: %+v", v)
	}
	if v := by["0f9c6dc6d5ad"]; v.InUse {
		t.Errorf("dangling anonymous volume is not in use: %+v", v)
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
	if want := []string{"a-plain", "b-plain", "x", "y", "z-anon"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
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
	res, err := r.DeleteApp(context.Background(), "app-1", true)
	if err != nil {
		t.Fatalf("delete must not fail on cleanup errors: %v", err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("failed cleanup must be reported as warnings")
	}
	if !res.VolumesDeleted {
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
	res, err := r.DeleteApp(context.Background(), "missing", true)
	if err != nil {
		t.Fatalf("deleting a missing app is a no-op: %v", err)
	}
	if len(res.Warnings) != 0 || res.VolumesDeleted {
		t.Fatalf("no sweep must run for a missing app: %+v", res)
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
	warnings := r.sweepAppVolumes(context.Background(), "app-1", true)
	if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), "daemon is down") {
		t.Fatalf("warnings must carry stderr: %v", warnings)
	}
	if _, err := r.ListVolumes(context.Background()); err == nil || !strings.Contains(err.Error(), "daemon is down") {
		t.Fatalf("ListVolumes error must carry stderr: %v", err)
	}
}

// fakeDocker installs a docker stub that appends its argv to a log and
// prints canned output for `volume ls` / `ps`; it returns the log path.
func fakeDocker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"case \"$1 $2\" in\n" +
		"  \"volume ls\") printf 'wf-app-1_data\\nwf-app-1_old\\n' ;;\n" +
		"  \"ps -aq\") echo c1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return log
}

func dockerCalls(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// After a clean `compose down` no container can hold the volumes, so the
// sweep skips the container probe and removes all labelled volumes in one
// exec. Only a failed down triggers the leftover-container cleanup.
func TestSweepAppVolumesExecs(t *testing.T) {
	r := newTestRepo(t)
	filter := projectLabelFilter("app-1")
	rmVolumes := "volume rm wf-app-1_data wf-app-1_old"

	log := fakeDocker(t)
	if w := r.sweepAppVolumes(context.Background(), "app-1", false); len(w) != 0 {
		t.Fatalf("warnings: %v", w)
	}
	if got, want := dockerCalls(t, log), []string{"volume ls -q --filter " + filter, rmVolumes}; !slices.Equal(got, want) {
		t.Fatalf("down ok: calls = %q, want %q", got, want)
	}

	log = fakeDocker(t)
	if w := r.sweepAppVolumes(context.Background(), "app-1", true); len(w) != 0 {
		t.Fatalf("warnings: %v", w)
	}
	want := []string{"ps -aq --filter " + filter, "rm -f c1", "volume ls -q --filter " + filter, rmVolumes}
	if got := dockerCalls(t, log); !slices.Equal(got, want) {
		t.Fatalf("down failed: calls = %q, want %q", got, want)
	}
}
