package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto/cryptotest"
)

// backupCompose declares a sqlite backup on a volume, a files backup through
// a service, and a custom backup run by a profiled service that references
// its storage through WF_BACKUP_ variables.
const backupCompose = `services:
  web:
    image: example/web
    volumes: [data:/data, ./media:/app/media]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
    environment:
      BUCKET: ${WF_BACKUP_REPLICA_BUCKET}
      PREFIX: ${WF_BACKUP_REPLICA_PREFIX}
      KEY: ${WF_BACKUP_REPLICA_SECRET_ACCESS_KEY}
volumes:
  data:
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: app.db
    - name: media
      type: files
      service: web
      paths: [/app/media]
    - name: replica
      type: custom
      service: litestream
`

// saveConfig saves app appID with compose and the given config blob.
func saveConfig(t *testing.T, r *Repository, appID, compose, config string) ([]string, error) {
	t.Helper()
	_, warnings, err := r.save(command.AppPayload{
		AppID:  appID,
		Config: []byte(config),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
	})
	return warnings, err
}

const plainConfig = `{"name":"demo","files":[{"filename":"compose.yml"}]}`

func withBackups(overrides string) string {
	return `{"name":"demo","files":[{"filename":"compose.yml"}],"backups":` + overrides + `}`
}

func TestKeepBackups(t *testing.T) {
	prev := []byte(`{"name":"a","backups":[{"name":"db","enabled":true,"storage":"s3"}]}`)
	got := keepBackups([]byte(`{"name":"b"}`), prev)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil || m["name"] != "b" || m["backups"] == nil {
		t.Fatalf("kept = %s", got)
	}
	// A blob with its own list (even empty) wins; junk passes through.
	for _, next := range []string{`{"name":"b","backups":[]}`, `not json`, `null`} {
		if got := keepBackups([]byte(next), prev); string(got) != next {
			t.Errorf("%s -> %s", next, got)
		}
	}
	if got := keepBackups([]byte(`{"name":"b"}`), []byte(`{"name":"a"}`)); string(got) != `{"name":"b"}` {
		t.Fatalf("nothing to keep: %s", got)
	}
}

// The app editor doesn't know the backups: a save keeps the stored list,
// drops overrides of backups the compose file no longer has (a warning),
// and keeps a backup added in the UI.
func TestSaveKeepsAndCleansBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(
		`[{"name":"database","enabled":true,"storage":"hetzner"},{"name":"media","every":"6h"},`+
			`{"name":"audit","type":"sqlite","volume":"data","path":"audit.db"}]`)); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	withoutMedia := strings.Replace(backupCompose, `    - name: media
      type: files
      service: web
      paths: [/app/media]
`, "", 1)
	warnings, err := saveConfig(t, r, "app-1", withoutMedia, plainConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `no longer has the backup "media"`) {
		t.Fatalf("warnings = %v", warnings)
	}
	got, err := r.readOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []recipe.BackupConfig{
		{Name: "database", Enabled: true, Storage: "hetzner"},
		{Name: "audit", Type: recipe.TypeSQLite, Volume: "data", Path: "audit.db"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %+v", got)
	}
	// The cleaned list is what was committed: nothing is left to commit.
	head, _ := gitHead(dir)
	if again, err := gitCommitAll(dir, "noop"); err != nil || again != head {
		t.Fatalf("the save left changes behind: %v", err)
	}
}

// A save whose compose file makes the backups invalid is refused and leaves
// nothing behind: an existing app keeps its last commit, a new one is gone.
func TestSaveRefusesInvalidBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	head, _ := gitHead(dir)
	bad := strings.Replace(backupCompose, "volume: data", "volume: gone", 1)
	_, err := saveConfig(t, r, "app-1", bad, plainConfig)
	if !errors.Is(err, recipe.ErrInvalid) || !strings.Contains(err.Error(), `x-winterflow.backups[0].volume: "gone" is not a volume`) {
		t.Fatalf("err = %v", err)
	}
	if now, _ := gitHead(dir); now != head {
		t.Fatal("a refused save committed")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "compose.yml"))
	if string(raw) != backupCompose {
		t.Fatal("a refused save left its compose file in the worktree")
	}
	// An override the compose file can't satisfy is refused too.
	_, err = saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true}]`))
	if err == nil || !strings.Contains(err.Error(), "backups[0].storage: an enabled backup needs a storage") {
		t.Fatalf("err = %v", err)
	}
	if _, err := saveConfig(t, r, "app-2", bad, plainConfig); err == nil {
		t.Fatal("a new app with invalid backups was saved")
	}
	if _, err := os.Stat(r.appDataDir("app-2")); !os.IsNotExist(err) {
		t.Fatal("a refused new app left its folder")
	}
}

func TestMaterializeStorageEnvBackupVariables(t *testing.T) {
	r, pub := newSecretRepo(t)
	storageFoldersExist(t, r)
	withStorages(t, r, func(p string) string { return cryptotest.Encrypt(t, pub, p) })
	dir := r.appDataDir("app-1")
	read := func() string {
		t.Helper()
		if err := materialize(context.Background(), r, dir); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, storageEnvRel))
		return string(raw)
	}
	// Off: every field empty (its service sits behind an off profile).
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "WF_BACKUP_REPLICA_BUCKET=\nWF_BACKUP_REPLICA_PREFIX=\nWF_BACKUP_REPLICA_SECRET_ACCESS_KEY=\n" {
		t.Fatalf("off = %q", got)
	}
	// On: the storage's values, the prefix narrowed to the backup's folder.
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"hetzner"}]`)); err != nil {
		t.Fatal(err)
	}
	want := "WF_BACKUP_REPLICA_BUCKET=tenants\nWF_BACKUP_REPLICA_PREFIX=wf/app-1/replica/\nWF_BACKUP_REPLICA_SECRET_ACCESS_KEY=s3-plain\n"
	if got := read(); got != want {
		t.Fatalf("on = %q", got)
	}
	// On, with a storage the server doesn't have: the deploy is refused.
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"gone"}]`)); err != nil {
		t.Fatal(err)
	}
	err := materialize(context.Background(), r, dir)
	if !errors.Is(err, model.ErrStorageRef) || !strings.Contains(err.Error(), `the backup replica uses the storage "gone", which this server doesn't have`) {
		t.Fatalf("missing storage: %v", err)
	}
	// A backup the app doesn't declare.
	writeFileT(t, dir, "compose.yml", backupCompose+"# ${WF_BACKUP_OTHER_TYPE}\n")
	if err := materialize(context.Background(), r, dir); err == nil || !strings.Contains(err.Error(), `there is no backup "other" in this app`) {
		t.Fatalf("undeclared: %v", err)
	}
}

func TestComposeProfiles(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	writeFileT(t, dir, envRel, "COMPOSE_PROFILES=extra, wf-backup-replica,debug\n")
	off := []recipe.EffectiveBackup{{Name: "replica", Type: recipe.TypeCustom}}
	if got := r.composeProfiles(dir, off); got != nil {
		t.Fatalf("no custom backup on: %v", got)
	}
	on := []recipe.EffectiveBackup{{Name: "replica", Type: recipe.TypeCustom, Enabled: true}, {Name: "db", Type: recipe.TypeSQLite, Enabled: true}}
	if got := r.composeProfiles(dir, on); !reflect.DeepEqual(got, []string{"debug", "extra", "wf-backup-replica"}) {
		t.Fatalf("profiles = %v", got)
	}
	// .env.secrets wins over .env.
	writeFileT(t, dir, envSecretsRel, "COMPOSE_PROFILES=secret\n")
	if got := r.composeProfiles(dir, on); !reflect.DeepEqual(got, []string{"secret", "wf-backup-replica"}) {
		t.Fatalf("profiles = %v", got)
	}
}

func TestOffServices(t *testing.T) {
	comp := recipe.Compose{Services: map[string][]string{
		"litestream": {"wf-backup-replica"},
		"shared":     {"wf-backup-a", "wf-backup-b"},
		"user":       {"wf-backup-c", "extra"},
	}}
	backups := []recipe.EffectiveBackup{
		{Name: "replica", Type: recipe.TypeCustom, Service: "litestream"},
		{Name: "a", Type: recipe.TypeCustom, Service: "shared", Enabled: true},
		{Name: "b", Type: recipe.TypeCustom, Service: "shared"},
		{Name: "c", Type: recipe.TypeCustom, Service: "user"},
		{Name: "db", Type: recipe.TypeSQLite},
	}
	if got := offServices(comp, backups, []string{"wf-backup-a", "extra"}); !reflect.DeepEqual(got, []string{"litestream"}) {
		t.Fatalf("off = %v", got)
	}
}

// composeCalls records the compose commands a deploy runs.
type composeCalls struct {
	mu   sync.Mutex
	args [][]string
}

func (c *composeCalls) install(r *Repository) {
	r.runCompose = func(_ context.Context, _ string, args []string) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.args = append(c.args, args)
		return nil
	}
}

// A deploy activates the enabled custom backups' profiles (with the user's)
// and removes the service of a custom backup that was turned off.
func TestComposeUpProfiles(t *testing.T) {
	r, pub := newSecretRepo(t)
	storageFoldersExist(t, r)
	withStorages(t, r, func(p string) string { return cryptotest.Encrypt(t, pub, p) })
	var calls composeCalls
	calls.install(r)
	if _, err := r.SaveApp(context.Background(), command.AppPayload{AppID: "app-1",
		Config: []byte(withBackups(`[{"name":"replica","enabled":true,"storage":"hetzner"}]`)),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupCompose)}}}); err != nil {
		t.Fatal(err)
	}
	up := strings.Join(calls.args[0], " ")
	if !strings.Contains(up, "--env-file .winterflow/storage.env --profile wf-backup-replica up -d --remove-orphans") || len(calls.args) != 1 {
		t.Fatalf("compose calls = %v", calls.args)
	}
	script, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), runRel))
	if !strings.Contains(string(script), "--profile 'wf-backup-replica'") {
		t.Fatalf("run.sh = %s", script)
	}
	calls.args = nil
	if _, err := r.SaveApp(context.Background(), command.AppPayload{AppID: "app-1",
		Config: []byte(withBackups(`[]`)),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupCompose)}}}); err != nil {
		t.Fatal(err)
	}
	if len(calls.args) != 2 || strings.Contains(strings.Join(calls.args[0], " "), "--profile") {
		t.Fatalf("compose calls = %v", calls.args)
	}
	if rm := strings.Join(calls.args[1], " "); !strings.HasSuffix(rm, "--profile wf-backup-replica rm --force --stop litestream") {
		t.Fatalf("rm = %s", rm)
	}
}

func TestGetAppReportsBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"hetzner"}]`)); err != nil {
		t.Fatal(err)
	}
	resp, err := r.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Backups) != 3 || resp.Backups[2].Name != "replica" || !resp.Backups[2].Enabled || resp.BackupsError != "" {
		t.Fatalf("backups = %+v (%s)", resp.Backups, resp.BackupsError)
	}
	var replica []command.StorageVariable
	for _, v := range resp.StorageVariables {
		if v.Backup == "replica" {
			replica = append(replica, v)
		}
	}
	if len(replica) != 3 || replica[0].Storage != "hetzner" || !replica[2].Secret {
		t.Fatalf("backup variables = %+v", replica)
	}
	// A recipe broken outside a save (a git-sourced app's upstream) is
	// reported, not hidden.
	writeFileT(t, r.appDataDir("app-1"), "compose.yml", strings.Replace(backupCompose, "volume: data", "volume: gone", 1))
	resp, _ = r.GetApp(context.Background(), "app-1")
	if !strings.Contains(resp.BackupsError, `"gone" is not a volume`) || resp.Backups != nil {
		t.Fatalf("broken recipe: %+v %q", resp.Backups, resp.BackupsError)
	}
}

// An enabled backup uses its storage: listed among the users, and the
// storage can't be removed meanwhile.
func TestStorageUsersIncludeBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","storage":"nas"}]`)); err != nil {
		t.Fatal(err)
	}
	users, err := r.storageUsers()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(users["disk"], []string{"app-1"}) || users["nas"] != nil {
		t.Fatalf("users = %v", users)
	}
	if err := r.storages.Save(localStorage("disk", t.TempDir()), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteStorage(context.Background(), "disk"); err == nil || !strings.Contains(err.Error(), "disk is used by demo") {
		t.Fatalf("delete = %v", err)
	}
	// An app whose backups can't be read blocks the delete (fail closed).
	writeFileT(t, r.appDataDir("app-1"), "compose.yml", strings.Replace(backupCompose, "volume: data", "volume: gone", 1))
	if err := r.DeleteStorage(context.Background(), "disk"); err == nil || !strings.Contains(err.Error(), "can't be checked") {
		t.Fatalf("delete = %v", err)
	}
}
