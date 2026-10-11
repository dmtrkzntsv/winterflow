package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

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
    }
  },
  "volumes": {
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
