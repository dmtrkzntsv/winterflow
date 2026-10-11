package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
)

// configJSON is `docker compose --profile '*' config --format json` of an
// app wf-app-1 whose project directory is /data/apps-data/app-1 (relative
// binds resolved against it; a git-sourced app's against source/).
const configJSON = `{
  "name": "wf-app-1",
  "services": {
    "web": {
      "image": "example/web",
      "volumes": [
        {"type": "volume", "source": "data", "target": "/data", "volume": {}},
        {"type": "bind", "source": "/data/apps-data/app-1/media", "target": "/app/media", "bind": {}},
        {"type": "bind", "source": "/srv/shared", "target": "/srv/shared", "read_only": true, "bind": {}},
        {"type": "bind", "source": "/data/apps-data/app-1/settings.json", "target": "/app/settings.json", "bind": {}},
        {"type": "tmpfs", "target": "/tmp/cache"},
        {"type": "volume", "source": "ext", "target": "/ext", "volume": {}},
        {"type": "volume", "source": "named", "target": "/named", "volume": {}},
        {"type": "volume", "target": "/anon", "volume": {}},
        {"type": "npipe", "source": "x", "target": "/pipe"}
      ]
    },
    "worker": {
      "image": "example/web",
      "volumes": [{"type": "bind", "source": "/data/apps-data/app-1/source/uploads", "target": "/uploads"}]
    },
    "nc": {
      "image": "nextcloud",
      "volumes": [
        {"type": "volume", "source": "html", "target": "/var/www/html", "volume": {}},
        {"type": "bind", "source": "/mnt/nas/nc", "target": "/var/www/html/data", "bind": {}},
        {"type": "volume", "source": "named", "target": "/var/www/html/data/deep", "volume": {}}
      ]
    },
    "ncskip": {
      "image": "nextcloud",
      "volumes": [
        {"type": "volume", "source": "html", "target": "/var/www/html", "volume": {}},
        {"type": "tmpfs", "target": "/var/www/html/tmp"},
        {"type": "bind", "source": "/data/apps-data/app-1/settings.json", "target": "/var/www/html/config/settings.json", "bind": {}},
        {"type": "volume", "target": "/var/www/html/anon", "volume": {}},
        {"type": "bind", "source": "/mnt/nas/nc", "target": "/var/www/html/data", "bind": {}},
        {"type": "volume", "source": "html", "target": "/var/www/htmlx", "volume": {}}
      ]
    }
  },
  "volumes": {
    "html": {"name": "wf-app-1_html"},
    "data": {"name": "wf-app-1_data"},
    "ext": {"name": "ext", "external": true},
    "named": {"name": "custom-name"}
  }
}`

func testModel(t *testing.T) composeModel {
	t.Helper()
	var m composeModel
	if err := json.Unmarshal([]byte(configJSON), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func isSettingsFile(p string) bool { return strings.HasSuffix(p, "/settings.json") }

func TestResolveBackup(t *testing.T) {
	m := testModel(t)
	for _, tc := range []struct {
		name string
		b    recipe.EffectiveBackup
		want helperPlan
	}{
		{"sqlite on a volume, the project prefix applied",
			recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Volume: "data", Path: "app.db"},
			helperPlan{Mounts: []helperMount{{Volume: "wf-app-1_data"}}, Paths: []helperPath{{Sub: "app.db", Entry: "app.db"}}}},
		{"a whole volume, name: honored",
			recipe.EffectiveBackup{Name: "all", Type: recipe.TypeFiles, Volume: "named"},
			helperPlan{Mounts: []helperMount{{Volume: "custom-name"}}, Paths: []helperPath{{}}}},
		{"files on an external volume",
			recipe.EffectiveBackup{Name: "ext", Type: recipe.TypeFiles, Volume: "ext", Paths: []string{"a", "b/c"}},
			helperPlan{Mounts: []helperMount{{Volume: "ext"}}, Paths: []helperPath{{Sub: "a", Entry: "a"}, {Sub: "b/c", Entry: "b/c"}}}},
		{"sqlite through a service's volume",
			recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Service: "web", Path: "/data/sub/app.db"},
			helperPlan{Mounts: []helperMount{{Volume: "wf-app-1_data"}}, Paths: []helperPath{{Sub: "sub/app.db", Entry: "data/sub/app.db"}}}},
		{"files spanning a relative bind, an absolute bind and a volume; one mount each",
			recipe.EffectiveBackup{Name: "media", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/app/media", "/srv/shared/x", "/data/cfg", "/app/media/sub"}},
			helperPlan{
				Mounts: []helperMount{{Bind: "/data/apps-data/app-1/media"}, {Bind: "/srv/shared"}, {Volume: "wf-app-1_data"}},
				Paths: []helperPath{{Mount: 0, Entry: "app/media"}, {Mount: 1, Sub: "x", Entry: "srv/shared/x"},
					{Mount: 2, Sub: "cfg", Entry: "data/cfg"}, {Mount: 0, Sub: "sub", Entry: "app/media/sub"}}}},
		{"a git-sourced app's bind into source/",
			recipe.EffectiveBackup{Name: "up", Type: recipe.TypeFiles, Service: "worker", Paths: []string{"/uploads"}},
			helperPlan{Mounts: []helperMount{{Bind: "/data/apps-data/app-1/source/uploads"}}, Paths: []helperPath{{Entry: "uploads"}}}},
		// A mount nested below a listed folder is a source of its own.
		{"a nested bind and volume ride along (the Nextcloud layout)",
			recipe.EffectiveBackup{Name: "nc", Type: recipe.TypeFiles, Service: "nc", Paths: []string{"/var/www/html"}},
			helperPlan{
				Mounts: []helperMount{{Volume: "wf-app-1_html"}, {Bind: "/mnt/nas/nc"}, {Volume: "custom-name"}},
				Paths: []helperPath{{Mount: 0, Entry: "var/www/html"}, {Mount: 1, Entry: "var/www/html/data"},
					{Mount: 2, Entry: "var/www/html/data/deep"}}}},
		{"a nested path the user listed too is not added twice",
			recipe.EffectiveBackup{Name: "nc", Type: recipe.TypeFiles, Service: "nc", Paths: []string{"/var/www/html", "/var/www/html/data/deep", "/var/www/html/data/x"}},
			helperPlan{
				Mounts: []helperMount{{Volume: "wf-app-1_html"}, {Bind: "/mnt/nas/nc"}, {Volume: "custom-name"}},
				Paths: []helperPath{{Mount: 0, Entry: "var/www/html"}, {Mount: 1, Entry: "var/www/html/data"},
					{Mount: 2, Entry: "var/www/html/data/deep"}, {Mount: 1, Sub: "x", Entry: "var/www/html/data/x"}}}},
		// The dump and the restore rely on a path coming before every path below it.
		{"a nested path listed before its parent follows it",
			recipe.EffectiveBackup{Name: "nc", Type: recipe.TypeFiles, Service: "nc", Paths: []string{"/var/www/html/data/deep", "/var/www/html"}},
			helperPlan{
				Mounts: []helperMount{{Volume: "custom-name"}, {Volume: "wf-app-1_html"}, {Bind: "/mnt/nas/nc"}},
				Paths:  []helperPath{{Mount: 1, Entry: "var/www/html"}, {Mount: 2, Entry: "var/www/html/data"}, {Mount: 0, Entry: "var/www/html/data/deep"}}}},
		{"a sibling folder with the same prefix is not nested",
			recipe.EffectiveBackup{Name: "nc", Type: recipe.TypeFiles, Service: "ncskip", Paths: []string{"/var/www/html/data"}},
			helperPlan{Mounts: []helperMount{{Bind: "/mnt/nas/nc"}}, Paths: []helperPath{{Entry: "var/www/html/data"}}}},
	} {
		got, err := resolveBackup(m, tc.b, isSettingsFile)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v, %v\nwant %+v", tc.name, got, err, tc.want)
		}
	}
	if src := (helperPath{Mount: 2, Sub: "a/b"}).Src(); src != "/src/2/a/b" {
		t.Fatalf("src = %s", src)
	}
	if src := (helperPath{Mount: 0}).Src(); src != "/src/0" {
		t.Fatalf("src = %s", src)
	}
}

func TestResolveBackupErrors(t *testing.T) {
	m := testModel(t)
	for _, tc := range []struct {
		b    recipe.EffectiveBackup
		want string
	}{
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/etc/app"}},
			"backup a: /etc/app is not on a mount of service web: the container's own files are lost"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/app/settings.json"}},
			"is a single-file bind mount of service web (/data/apps-data/app-1/settings.json), which can't be replaced from another container: mount its directory instead"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/tmp/cache/x"}},
			"/tmp/cache/x is on a tmpfs mount of service web"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/anon"}},
			"is on an anonymous volume of service web"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/pipe"}},
			"is on a npipe mount"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeSQLite, Service: "web", Path: "/data"},
			"/data is the mount itself, not the database file inside it"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "api", Paths: []string{"/x"}},
			`compose has no service "api"`},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Volume: "gone"},
			`compose has no volume "gone"`},
	} {
		if _, err := resolveBackup(m, tc.b, isSettingsFile); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.b, err, tc.want)
		}
	}
}

// fakeComposeConfig answers `docker compose config` with out (or err) and
// counts the calls.
type fakeComposeConfig struct {
	out   string
	err   error
	calls [][]string
}

func (f *fakeComposeConfig) install(r *Repository) {
	r.composeOutput = func(_ context.Context, _ string, args []string) ([]byte, error) {
		f.calls = append(f.calls, args)
		return []byte(f.out), f.err
	}
}

// The model comes from one compose config per commit: cached until HEAD or
// a derived env file changes, forgotten with the app.
func TestAppComposeModelIsCached(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	fake := &fakeComposeConfig{out: configJSON}
	fake.install(r)
	dir := r.appDataDir("app-1")
	cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
	if err != nil {
		t.Fatal(err)
	}
	resolve := func() map[string]helperPlan {
		t.Helper()
		plans, err := r.resolveBackups(context.Background(), "app-1", r.appComposeFiles(dir), cfg.backups)
		if err != nil {
			t.Fatal(err)
		}
		return plans
	}
	plans := resolve()
	if len(plans) != 1 || plans["database"].Mounts[0].Volume != "wf-app-1_data" {
		t.Fatalf("plans = %+v", plans)
	}
	if args := strings.Join(fake.calls[0], " "); !strings.Contains(args, "--profile * config --format json") {
		t.Fatalf("args = %s", args)
	}
	resolve()
	if len(fake.calls) != 1 {
		t.Fatalf("compose config ran %d times for one commit", len(fake.calls))
	}
	writeFileT(t, dir, storageEnvRel, "WF_STORAGE_X_TYPE=local\n")
	resolve()
	if len(fake.calls) != 2 {
		t.Fatal("a changed storage.env must re-read the model")
	}
	writeFileT(t, dir, "notes.txt", "x")
	if _, err := gitCommitAll(dir, "another"); err != nil {
		t.Fatal(err)
	}
	resolve()
	if len(fake.calls) != 3 {
		t.Fatal("a new commit must re-read the model")
	}
	r.forgetApp("app-1")
	resolve()
	if len(fake.calls) != 4 {
		t.Fatal("forgetApp must drop the model")
	}
	// Nothing enabled to resolve: no exec at all.
	if _, err := r.resolveBackups(context.Background(), "app-1", nil, []recipe.EffectiveBackup{{Name: "x", Type: recipe.TypeSQLite}}); err != nil || len(fake.calls) != 4 {
		t.Fatalf("resolved a disabled backup: %v, %d calls", err, len(fake.calls))
	}
	fake.out = "not json"
	r.forgetApp("app-1")
	if _, err := r.resolveBackups(context.Background(), "app-1", nil, cfg.backups); err == nil {
		t.Fatal("bad compose config output passed")
	}
	fake.err = errors.New("exit status 1: no such service")
	if _, err := r.resolveBackups(context.Background(), "app-1", nil, cfg.backups); err == nil || !strings.Contains(err.Error(), "no such service") {
		t.Fatalf("err = %v", err)
	}
}

// A deploy is refused when an enabled backup can't reach its data.
func TestComposeUpRefusesAnUnreachableBackup(t *testing.T) {
	r := newTestRepo(t)
	var calls composeCalls
	calls.install(r)
	(&fakeComposeConfig{out: configJSON}).install(r)
	_, err := r.SaveApp(context.Background(), command.AppPayload{AppID: "app-1",
		Config: []byte(withBackups(`[{"name":"media","enabled":true,"storage":"disk","paths":["/etc/app"]}]`)),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupCompose)}}})
	if err == nil || !strings.Contains(err.Error(), "/etc/app is not on a mount of service web") {
		t.Fatalf("err = %v", err)
	}
	if len(calls.args) != 0 {
		t.Fatalf("compose up ran: %v", calls.args)
	}
}

// Nested tmpfs, anonymous and single-file mounts hold nothing a folder copy
// can carry: they are skipped with a warning, not refused.
func TestResolveBackupSkipsUnbackableNestedMounts(t *testing.T) {
	m := testModel(t)
	b := recipe.EffectiveBackup{Name: "nc", Type: recipe.TypeFiles, Service: "ncskip", Paths: []string{"/var/www/html"}}
	got, err := resolveBackup(m, b, isSettingsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := (helperPlan{Mounts: []helperMount{{Volume: "wf-app-1_html"}, {Bind: "/mnt/nas/nc"}},
		Paths: []helperPath{{Mount: 0, Entry: "var/www/html"}, {Mount: 1, Entry: "var/www/html/data"}}}); !reflect.DeepEqual(got.Mounts, want.Mounts) || !reflect.DeepEqual(got.Paths, want.Paths) {
		t.Fatalf("plan = %+v", got)
	}
	if len(got.Warnings) != 3 ||
		!strings.Contains(got.Warnings[0], "/var/www/html/tmp is on a tmpfs mount of service ncskip") ||
		!strings.Contains(got.Warnings[1], "/var/www/html/config/settings.json is a single-file bind mount") ||
		!strings.Contains(got.Warnings[2], "/var/www/html/anon is on an anonymous volume") ||
		!strings.HasSuffix(got.Warnings[0], "skipped, it lies below /var/www/html") {
		t.Fatalf("warnings = %q", got.Warnings)
	}
	// Listed on its own, the same mount is still an error.
	b.Paths = []string{"/var/www/html/tmp"}
	if _, err := resolveBackup(m, b, isSettingsFile); err == nil || !strings.Contains(err.Error(), "tmpfs mount") {
		t.Fatalf("err = %v", err)
	}
}

func resolveWithStat(t *testing.T, r *Repository) (map[string]helperPlan, error) {
	t.Helper()
	(&fakeComposeConfig{out: configJSON}).install(r)
	return r.resolveBackups(context.Background(), "app-1", nil, []recipe.EffectiveBackup{
		{Name: "media", Type: recipe.TypeFiles, Enabled: true, Service: "web", Paths: []string{"/app/media", "/app/settings.json"}}})
}

// Bind sources are stat'ed once each, only those a plan looks at, and a
// settings file among them is refused.
func TestResolveBackupsStatsBindSources(t *testing.T) {
	r := newTestRepo(t)
	var mu sync.Mutex
	var seen []string
	r.hostStat = func(p string) (fs.FileInfo, error) {
		mu.Lock()
		seen = append(seen, p)
		mu.Unlock()
		if strings.HasSuffix(p, "settings.json") {
			return os.Stat(os.Args[0]) // a regular file
		}
		return nil, os.ErrNotExist
	}
	_, err := resolveWithStat(t, r)
	if err == nil || !strings.Contains(err.Error(), "single-file bind mount") {
		t.Fatalf("err = %v", err)
	}
	sort.Strings(seen)
	if want := []string{"/data/apps-data/app-1/media", "/data/apps-data/app-1/settings.json"}; !slices.Equal(seen, want) {
		t.Fatalf("stat'ed %v, want %v", seen, want)
	}
}

// A bind source on a hung share fails the resolution at the deadline instead
// of blocking the command pipeline.
func TestResolveBackupsHungBindSource(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	r := newTestRepo(t)
	r.hostStat = func(string) (fs.FileInfo, error) {
		<-release
		return nil, os.ErrNotExist
	}
	r.storageLimits.folder = 50 * time.Millisecond
	start := time.Now()
	_, err := resolveWithStat(t, r)
	if err == nil || err.Error() != "the folder /data/apps-data/app-1/media did not answer within 50ms (is the share reachable?)" {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.resolveBackups(ctx, "app-1", nil, []recipe.EffectiveBackup{
		{Name: "media", Type: recipe.TypeFiles, Enabled: true, Service: "web", Paths: []string{"/app/media"}}}); err == nil ||
		!strings.Contains(err.Error(), "was not checked: context canceled") {
		t.Fatalf("cancelled: %v", err)
	}
}

// A killed docker CLI whose plugin child keeps the pipes open must not hold
// the command past its deadline.
func TestDockerCommandHasAWaitDelay(t *testing.T) {
	cmd := dockerCommand(context.Background(), "/tmp", []string{"compose", "config"})
	if cmd.WaitDelay != dockerWaitDelay || cmd.Dir != "/tmp" || cmd.Args[0] != "docker" {
		t.Fatalf("cmd = %+v", cmd)
	}
}

// Asking again about a bind source whose stat hangs does not start another
// stat: a dead share leaks at most one blocked goroutine per path. Once the
// stat returns, the path is stat'ed afresh.
func TestHungBindSourceIsStatedOnce(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	r := newTestRepo(t)
	r.hostStat = func(string) (fs.FileInfo, error) {
		calls.Add(1)
		<-release
		return nil, os.ErrNotExist
	}
	r.storageLimits.folder = 20 * time.Millisecond
	paths := map[string]bool{"/mnt/dead": true}
	for range 5 {
		if _, err := r.statRegularFiles(context.Background(), paths); err == nil || !strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("err = %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("stat started %d times for one hung path", got)
	}
	close(release)
	r.storageLimits.folder = 5 * time.Second
	got, err := r.statRegularFiles(context.Background(), paths)
	if err != nil || got["/mnt/dead"] || calls.Load() > 2 {
		t.Fatalf("after the share answered: %v, %v, %d stats", got, err, calls.Load())
	}
	r.statMu.Lock()
	left := len(r.statFlight)
	r.statMu.Unlock()
	if left != 0 {
		t.Fatalf("%d stats still registered", left)
	}
}

// A volume mounted at two container paths, both listed, is one place: it is
// archived (and restored) once, or the second restore would swap the first
// one's data back out.
func TestResolveBackupArchivesAVolumeListedTwiceOnce(t *testing.T) {
	m := testModel(t)
	b := recipe.EffectiveBackup{Name: "nc", Type: recipe.TypeFiles, Service: "ncskip", Paths: []string{"/var/www/html", "/var/www/htmlx"}}
	got, err := resolveBackup(m, b, isSettingsFile)
	if err != nil {
		t.Fatal(err)
	}
	var whole []string
	for _, p := range got.Paths {
		if p.Mount == 0 && p.Sub == "" {
			whole = append(whole, p.Entry)
		}
	}
	if !reflect.DeepEqual(whole, []string{"var/www/html"}) {
		t.Fatalf("the volume is listed as %q in %+v", whole, got.Paths)
	}
}
