package dockercompose

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/backup/store"
	"winterflow/pkg/crypto/cryptotest"
)

func localStorage(name, path string) model.Storage {
	return model.Storage{Name: name, Type: model.StorageLocal, Local: &model.LocalStorage{Path: path}}
}

// fakeComposeHooks records the storage.save hooks: which projects are up and
// which apps were brought up again.
type fakeComposeHooks struct {
	mu        sync.Mutex
	up        map[string]bool
	psErr     error
	failUp    map[string]error
	redeploys []string
}

func (f *fakeComposeHooks) install(r *Repository) {
	r.runningProjects = func(context.Context) (map[string]bool, error) { return f.up, f.psErr }
	r.redeploy = func(_ context.Context, appID string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.redeploys = append(f.redeploys, appID)
		return f.failUp[appID]
	}
}

func TestSaveStorageLocal(t *testing.T) {
	r := newTestRepo(t)
	dir := t.TempDir()
	resp, err := r.SaveStorage(context.Background(), localStorage("disk", dir+"/"), true)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Storage.Name != "disk" || resp.Storage.Location != dir || len(resp.Redeployed) != 0 || len(resp.Warnings) != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	// The probe is gone; the storage is stored normalized.
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("probe left behind: %v", entries)
	}
	s, ok, _ := r.storages.Get("disk")
	if !ok || s.Local.Path != dir {
		t.Fatalf("stored = %+v", s)
	}
}

func TestSaveStorageRefusesWhatFailsItsTest(t *testing.T) {
	r := newTestRepo(t)
	missing := filepath.Join(t.TempDir(), "usb")
	_, err := r.SaveStorage(context.Background(), localStorage("disk", missing), true)
	if err == nil || !strings.Contains(err.Error(), "the connection test failed, nothing was saved") || !strings.Contains(err.Error(), "is the disk mounted") {
		t.Fatalf("err = %v", err)
	}
	if list, _ := r.storages.List(); len(list) != 0 {
		t.Fatalf("stored after a failed test: %+v", list)
	}
	// Inside the agent's own data folder: refused before any write.
	_, err = r.SaveStorage(context.Background(), localStorage("disk", filepath.Join(r.cfg.GetAgentDataDir(), "backups")), true)
	if err == nil || !strings.Contains(err.Error(), "inside winterflow's data folder") {
		t.Fatalf("data dir: %v", err)
	}
	if _, err := r.SaveStorage(context.Background(), localStorage("Bad", t.TempDir()), true); err == nil {
		t.Fatal("an invalid storage must be refused")
	}
}

// An add never replaces a storage (nor its secret), an edit never creates
// one, and an edit can't change the type the apps' references rely on.
func TestSaveStorageCreateAndEditModes(t *testing.T) {
	r := newTestRepo(t)
	docker := &fakeComposeHooks{up: map[string]bool{"wf-a-shop": true}}
	docker.install(r)
	storageApp(t, r, "a-shop", "Shop", "disk", false)
	first, second := t.TempDir(), t.TempDir()
	ctx := context.Background()

	if _, err := r.SaveStorage(ctx, localStorage("disk", first), false); err == nil ||
		err.Error() != "there is no storage disk on this server" {
		t.Fatalf("edit of a missing storage: %v", err)
	}
	if list, _ := r.storages.List(); len(list) != 0 {
		t.Fatalf("an edit created %+v", list)
	}
	if _, err := r.SaveStorage(ctx, localStorage("disk", first), true); err != nil {
		t.Fatal(err)
	}
	docker.redeploys = nil
	if _, err := r.SaveStorage(ctx, localStorage("disk", second), true); err == nil ||
		err.Error() != "a storage named disk already exists on this server" {
		t.Fatalf("add over an existing storage: %v", err)
	}
	s3 := model.Storage{Name: "disk", Type: model.StorageS3, S3: &model.S3Storage{
		Endpoint: "https://s3.example.test", Bucket: "backups", AccessKeyID: "AKIA", SecretAccessKey: model.EncryptedSecret,
	}}
	if _, err := r.SaveStorage(ctx, s3, false); err == nil ||
		err.Error() != "the type of an existing storage can't change — delete it and add it again" {
		t.Fatalf("type change: %v", err)
	}
	if s, _, _ := r.storages.Get("disk"); s.Type != model.StorageLocal || s.Local.Path != first {
		t.Fatalf("a refused save replaced the storage: %+v", s)
	}
	if len(docker.redeploys) != 0 {
		t.Fatalf("a refused save redeployed %v", docker.redeploys)
	}
	if _, err := r.SaveStorage(ctx, localStorage("disk", second), false); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := r.storages.Get("disk"); s.Local.Path != second {
		t.Fatalf("edit not stored: %+v", s)
	}
}

func TestTestStorageReportsInline(t *testing.T) {
	r := newTestRepo(t)
	res, err := r.TestStorage(context.Background(), localStorage("disk", t.TempDir()))
	if err != nil || !res.OK || res.Error != "" {
		t.Fatalf("ok case = %+v, %v", res, err)
	}
	res, err = r.TestStorage(context.Background(), localStorage("disk", "/nonexistent-wf-test"))
	if err != nil || res.OK || !strings.Contains(res.Error, "does not exist") {
		t.Fatalf("failing case = %+v, %v", res, err)
	}
	if list, _ := r.storages.List(); len(list) != 0 {
		t.Fatal("a test stored something")
	}
}

func TestSaveStorageS3KeepsTheStoredSecret(t *testing.T) {
	r, pub := newSecretRepo(t)
	backend := s3mem.New()
	if err := backend.CreateBucket("backups"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	s3 := func(secret string) model.Storage {
		return model.Storage{Name: "minio", Type: model.StorageS3, S3: &model.S3Storage{
			Endpoint: srv.URL, Bucket: "backups", Prefix: "wf", AccessKeyID: "AKIA", SecretAccessKey: secret, PathStyle: true,
		}}
	}
	ctx := context.Background()

	// No stored secret yet: the placeholder can't be resolved.
	res, _ := r.TestStorage(ctx, s3(model.EncryptedSecret))
	if res.OK || !strings.Contains(res.Error, "enter the secret access key: minio has none stored") {
		t.Fatalf("placeholder without a stored secret = %+v", res)
	}
	ct := cryptotest.Encrypt(t, pub, "s3-plain")
	resp, err := r.SaveStorage(ctx, s3(ct), true)
	if err != nil {
		t.Fatal(err)
	}
	// The response masks the secret; the store keeps the ciphertext.
	if resp.Storage.S3.SecretAccessKey != model.EncryptedSecret || resp.Storage.Location != srv.URL+"/backups/wf/" {
		t.Fatalf("resp = %+v", resp.Storage)
	}
	if s, _, _ := r.storages.Get("minio"); s.S3.SecretAccessKey != ct {
		t.Fatal("the ciphertext was not stored")
	}
	// Editing without re-entering the secret keeps it (test and save).
	if res, _ := r.TestStorage(ctx, s3(model.EncryptedSecret)); !res.OK {
		t.Fatalf("test with the stored secret = %+v", res)
	}
	edited := s3(model.EncryptedSecret)
	edited.S3.Prefix = "tenants/"
	if _, err := r.SaveStorage(ctx, edited, false); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := r.storages.Get("minio"); s.S3.SecretAccessKey != ct || s.S3.Prefix != "tenants/" {
		t.Fatalf("after edit = %+v", s.S3)
	}
	// Garbage instead of ciphertext fails before anything is written.
	if _, err := r.SaveStorage(ctx, s3("not-ciphertext"), false); err == nil || !strings.Contains(err.Error(), "decrypt the secret access key") {
		t.Fatalf("garbage secret: %v", err)
	}
	// A wrong bucket fails the test and keeps the stored storage.
	bad := s3(model.EncryptedSecret)
	bad.S3.Bucket = "missing"
	if _, err := r.SaveStorage(ctx, bad, false); err == nil {
		t.Fatal("a missing bucket must fail the save")
	}
	if s, _, _ := r.storages.Get("minio"); s.S3.Bucket != "backups" {
		t.Fatalf("a failed save replaced the storage: %+v", s.S3)
	}
}

// storageApp saves an app referencing storage name (or none) and marks it
// deployed at HEAD; draft adds an undeployed commit on top.
func storageApp(t *testing.T, r *Repository, appID, appName, storage string, draft bool) {
	t.Helper()
	compose := "services: {}\n"
	if storage != "" {
		compose = "x: ${" + model.StorageVarName(storage, model.FieldPath) + "}\n"
	}
	head := savedApp(t, r, appID, appName, compose)
	if err := writeDeployedMark(r.appDataDir(appID), head); err != nil {
		t.Fatal(err)
	}
	if draft {
		savedApp(t, r, appID, appName, compose+"# draft\n")
	}
}

func TestSaveStorageRedeploysRunningUsers(t *testing.T) {
	r := newTestRepo(t)
	storageApp(t, r, "a-running", "Running", "disk", false)
	storageApp(t, r, "b-stopped", "Stopped", "disk", false)
	storageApp(t, r, "c-draft", "Drafty", "disk", true)
	storageApp(t, r, "d-broken", "Broken", "disk", false)
	storageApp(t, r, "e-other", "Other", "", false)
	docker := &fakeComposeHooks{
		up:     map[string]bool{"wf-a-running": true, "wf-c-draft": true, "wf-d-broken": true, "wf-e-other": true},
		failUp: map[string]error{"d-broken": errors.New("pull access denied")},
	}
	docker.install(r)

	resp, err := r.SaveStorage(context.Background(), localStorage("disk", t.TempDir()), true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resp.Storage.Apps, []string{"a-running", "b-stopped", "c-draft", "d-broken"}) {
		t.Fatalf("apps = %v", resp.Storage.Apps)
	}
	if !reflect.DeepEqual(resp.Redeployed, []string{"a-running"}) {
		t.Fatalf("redeployed = %v", resp.Redeployed)
	}
	// A stopped app is not started; a draft is not deployed behind the
	// user's back; a failed redeploy is a warning.
	if !reflect.DeepEqual(docker.redeploys, []string{"a-running", "d-broken"}) {
		t.Fatalf("brought up = %v", docker.redeploys)
	}
	if len(resp.Warnings) != 2 || !strings.Contains(resp.Warnings[0], "Drafty has undeployed changes") ||
		resp.Warnings[1] != "Broken: redeploy failed: pull access denied" {
		t.Fatalf("warnings = %q", resp.Warnings)
	}

	// docker ps failing redeploys nothing and says so.
	docker.psErr, docker.redeploys = errors.New("daemon down"), nil
	resp, err = r.SaveStorage(context.Background(), localStorage("disk", t.TempDir()), false)
	if err != nil || len(resp.Redeployed) != 0 || len(docker.redeploys) != 0 ||
		len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "none was redeployed: daemon down") {
		t.Fatalf("ps failure: %+v, %v, %v", resp, err, docker.redeploys)
	}
}

func TestListStorages(t *testing.T) {
	r, pub := newSecretRepo(t)
	withStorages(t, r, func(p string) string { return cryptotest.Encrypt(t, pub, p) })
	storageApp(t, r, "app-1", "Shop", "hetzner", false)
	list, err := r.ListStorages(context.Background())
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	byName := map[string]int{}
	for i, s := range list {
		byName[s.Name] = i
		if s.S3 != nil && s.S3.SecretAccessKey != model.EncryptedSecret {
			t.Fatalf("%s: secret left the agent", s.Name)
		}
	}
	if h := list[byName["hetzner"]]; !reflect.DeepEqual(h.Apps, []string{"app-1"}) || h.Location != "https://s3.example.com/tenants/wf/" {
		t.Fatalf("hetzner = %+v", h)
	}
	if d := list[byName["backup-disk"]]; len(d.Apps) != 0 || d.Location != "/mnt/usb" {
		t.Fatalf("backup-disk = %+v", d)
	}
}

func TestDeleteStorage(t *testing.T) {
	r := newTestRepo(t)
	if err := r.storages.Put(localStorage("disk", "/mnt/usb")); err != nil {
		t.Fatal(err)
	}
	if err := r.storages.Put(localStorage("spare", "/mnt/spare")); err != nil {
		t.Fatal(err)
	}
	storageApp(t, r, "app-1", "Shop", "disk", false)
	storageApp(t, r, "app-2", "Blog", "disk", false)

	err := r.DeleteStorage(context.Background(), "disk")
	if err == nil || err.Error() != "disk is used by Shop, Blog: remove its WF_STORAGE_ variables from those apps first" {
		t.Fatalf("in use: %v", err)
	}
	if err := r.DeleteStorage(context.Background(), "spare"); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteStorage(context.Background(), "spare"); err == nil || !strings.Contains(err.Error(), "there is no storage spare") {
		t.Fatalf("missing: %v", err)
	}
	if _, ok, _ := r.storages.Get("disk"); !ok {
		t.Fatal("a refused delete removed the storage")
	}
}

func TestDeleteStorageFailsClosed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	r := newTestRepo(t)
	if err := r.storages.Put(localStorage("disk", "/mnt/usb")); err != nil {
		t.Fatal(err)
	}
	storageApp(t, r, "app-1", "Shop", "disk", false)
	compose := filepath.Join(r.appDataDir("app-1"), "compose.yml")
	if err := os.Chmod(compose, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(compose, 0o644) })
	err := r.DeleteStorage(context.Background(), "disk")
	if err == nil || !strings.Contains(err.Error(), "the apps using it can't be checked") {
		t.Fatalf("err = %v", err)
	}
	// The listing still answers, without that app.
	if list, err := r.ListStorages(context.Background()); err != nil || len(list) != 1 || len(list[0].Apps) != 0 {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestExplainWrite(t *testing.T) {
	noHost := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "wf-backups.127.0.0.1", IsNotFound: true}}
	vhost := func(pathStyle bool) model.Storage {
		return model.Storage{Type: model.StorageS3, S3: &model.S3Storage{Endpoint: "http://127.0.0.1:9000", Bucket: "wf-backups", PathStyle: pathStyle}}
	}
	denied := &fs.PathError{Op: "open", Path: "x", Err: syscall.EACCES}
	readOnly := &fs.PathError{Op: "open", Path: "x", Err: syscall.EROFS}
	cases := []struct {
		s    model.Storage
		err  error
		want string
	}{
		{localStorage("d", "/srv/backups"), readOnly, "may only write under /mnt and /media"},
		{localStorage("d", "/srv/backups"), denied, "docs/recipes/backup-storage.md"},
		{localStorage("d", "/mnt/usb"), denied, "writable by the user winterflow runs as"},
		{localStorage("d", "/media/disk"), readOnly, "writable by the user winterflow runs as"},
		{localStorage("d", "/srv/backups"), errors.New("disk full"), "write a test file: disk full"},
		{model.Storage{Type: model.StorageS3}, denied, "write a test file"},
		// Virtual-host addressing on an endpoint without wildcard DNS.
		{vhost(false), &url.Error{Op: "Post", URL: "http://wf-backups.127.0.0.1:9000/", Err: noHost},
			"the bucket's hostname wf-backups.127.0.0.1 doesn't resolve — try path-style addressing (write a test file: Post"},
		{vhost(true), &url.Error{Op: "Post", URL: "http://127.0.0.1:9000/", Err: noHost}, "write a test file: Post"},
		{vhost(false), &net.DNSError{Err: "no such host", Name: "s3.example.test", IsNotFound: true}, "write a test file: lookup s3.example.test"},
	}
	for _, tc := range cases {
		if got := explainWrite(tc.s, tc.err).Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%v / %v: %q, want %q", tc.s.Local, tc.err, got, tc.want)
		}
	}
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		path, dir string
		want      bool
	}{
		{"/var/lib/wf", "/var/lib/wf", true}, {"/var/lib/wf/x", "/var/lib/wf", true},
		{"/var/lib/wf2", "/var/lib/wf", false}, {"/var/lib", "/var/lib/wf", false}, {"/mnt", "/var/lib/wf", false},
	} {
		if got := within(c.path, c.dir); got != c.want {
			t.Errorf("within(%q, %q) = %v", c.path, c.dir, got)
		}
	}
}

// TestComposeProjectsUp runs the real `docker ps` path against a fake
// docker binary.
func TestComposeProjectsUp(t *testing.T) {
	r := newTestRepo(t)
	bin := t.TempDir()
	writeFileT(t, bin, "docker", `#!/bin/sh
[ -n "$FAIL" ] && exit 1
echo '{"ID":"a","Names":"wf-app1-web-1","State":"exited","Labels":"com.docker.compose.project=wf-app1"}'
echo '{"ID":"b","Names":"wf-app2-web-1","State":"running","Labels":"com.docker.compose.project=wf-app2"}'
`)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	up, err := r.composeProjectsUp(context.Background())
	if err != nil || !reflect.DeepEqual(up, map[string]bool{"wf-app1": true, "wf-app2": true}) {
		t.Fatalf("up = %v, %v", up, err)
	}
	t.Setenv("FAIL", "1")
	if _, err := r.composeProjectsUp(context.Background()); err == nil {
		t.Fatal("a failing docker ps must fail")
	}
}

func TestHasDraft(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "never-marked", "x", "services: {}\n")
	if r.hasDraft("never-marked") {
		t.Fatal("an app without a deployed mark has no known draft")
	}
	storageApp(t, r, "clean", "x", "", false)
	storageApp(t, r, "ahead", "x", "", true)
	if r.hasDraft("clean") || !r.hasDraft("ahead") {
		t.Fatal("hasDraft")
	}
}

func TestProbeStorageDoesNotWaitForAHungMount(t *testing.T) {
	release := make(chan struct{})
	old, oldTimeout := openStorage, storageProbeTimeout
	openStorage = func(model.Storage, string) (store.Store, error) {
		<-release // a hard NFS mount: the syscall never returns
		return nil, errors.New("released")
	}
	storageProbeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { close(release); openStorage, storageProbeTimeout = old, oldTimeout })

	r := newTestRepo(t)
	start := time.Now()
	res, err := r.TestStorage(context.Background(), localStorage("disk", t.TempDir()))
	if err != nil || res.OK || res.Error != "disk did not answer within 50ms" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	if _, err := r.SaveStorage(context.Background(), localStorage("disk", t.TempDir()), true); err == nil ||
		!strings.Contains(err.Error(), "nothing was saved") || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("save: %v", err)
	}
	if list, _ := r.storages.List(); len(list) != 0 {
		t.Fatalf("stored: %+v", list)
	}
}

func TestSaveStorageBudgetSkipsLaterRedeploys(t *testing.T) {
	r := newTestRepo(t)
	storageApp(t, r, "a-slow", "Slow", "disk", false)
	storageApp(t, r, "b-late", "Late", "disk", false)
	storageApp(t, r, "c-later", "Later", "disk", false)
	r.runningProjects = func(context.Context) (map[string]bool, error) {
		return map[string]bool{"wf-a-slow": true, "wf-b-late": true, "wf-c-later": true}, nil
	}
	var started []string
	r.redeploy = func(ctx context.Context, appID string) error {
		started = append(started, appID)
		<-ctx.Done() // a redeploy that takes longer than the whole budget
		return ctx.Err()
	}
	old, oldMin := storageSaveBudget, storageRedeployMinBudget
	storageSaveBudget, storageRedeployMinBudget = 200*time.Millisecond, 0
	t.Cleanup(func() { storageSaveBudget, storageRedeployMinBudget = old, oldMin })

	start := time.Now()
	resp, err := r.SaveStorage(context.Background(), localStorage("disk", t.TempDir()), true)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	if !reflect.DeepEqual(started, []string{"a-slow"}) || len(resp.Redeployed) != 0 {
		t.Fatalf("started = %v, redeployed = %v", started, resp.Redeployed)
	}
	want := []string{
		"Slow: redeploy failed: context deadline exceeded",
		"Late: not redeployed: time budget exhausted — redeploy Late to use the new settings",
		"Later: not redeployed: time budget exhausted — redeploy Later to use the new settings",
	}
	if !reflect.DeepEqual(resp.Warnings, want) {
		t.Fatalf("warnings = %q", resp.Warnings)
	}
}

// A redeploy is only started with storageRedeployMinBudget of the save's
// budget left: one cut short half-way would leave the app down.
func TestSaveStorageStartsNoRedeployWithoutEnoughBudget(t *testing.T) {
	r := newTestRepo(t)
	storageApp(t, r, "a-first", "First", "disk", false)
	storageApp(t, r, "b-second", "Second", "disk", false)
	r.runningProjects = func(context.Context) (map[string]bool, error) {
		return map[string]bool{"wf-a-first": true, "wf-b-second": true}, nil
	}
	var started []string
	r.redeploy = func(ctx context.Context, appID string) error {
		started = append(started, appID)
		time.Sleep(1100 * time.Millisecond) // uses up the budget's slack
		return nil
	}
	old, oldMin := storageSaveBudget, storageRedeployMinBudget
	storageSaveBudget, storageRedeployMinBudget = 30*time.Second, 29*time.Second
	t.Cleanup(func() { storageSaveBudget, storageRedeployMinBudget = old, oldMin })

	resp, err := r.SaveStorage(context.Background(), localStorage("disk", t.TempDir()), true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(started, []string{"a-first"}) || !reflect.DeepEqual(resp.Redeployed, []string{"a-first"}) {
		t.Fatalf("started = %v, redeployed = %v", started, resp.Redeployed)
	}
	want := []string{"Second: not redeployed: time budget exhausted — redeploy Second to use the new settings"}
	if !reflect.DeepEqual(resp.Warnings, want) {
		t.Fatalf("warnings = %q", resp.Warnings)
	}

	// The default keeps two minutes in reserve of an eight minute budget.
	if oldMin != 2*time.Minute || old != 8*time.Minute {
		t.Fatalf("defaults: budget %v, minimum %v", old, oldMin)
	}
}
