package dockercompose

import (
	"context"
	"crypto/ecdsa"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/crypto/cryptotest"
)

// sharedApp saves an app whose compose file is compose and whose own plain
// variables are vars (NAME=value pairs), without deploying.
func sharedApp(t *testing.T, r *Repository, appID, compose string, vars map[string]string) {
	t.Helper()
	var items []command.ContentItem
	var meta []string
	for name, v := range vars {
		items = append(items, command.ContentItem{Name: name, Content: []byte(v)})
		meta = append(meta, `{"name":"`+name+`","is_encrypted":false}`)
	}
	cfg := `{"name":"Shop App","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[` + strings.Join(meta, ",") + `]}`
	if _, err := r.saveWithoutDeploy(command.AppPayload{
		AppID: appID, Config: []byte(cfg),
		Files:     []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
		Variables: items,
	}); err != nil {
		t.Fatal(err)
	}
}

func syncVars(t *testing.T, r *Repository, snap command.SyncVarsRequest) command.SyncVarsResponse {
	t.Helper()
	resp, err := r.SyncVars(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func testSnapshot(pub *ecdsa.PublicKey, t *testing.T, rev int64, host, pass string) command.SyncVarsRequest {
	return command.SyncVarsRequest{Revision: rev, ServerID: "s1", ServerName: "box", OrgID: "o1",
		Org: []command.SharedVar{
			{Name: "SMTP_HOST", Value: host},
			{Name: "SMTP_PASS", IsSecret: true, Ciphertext: cryptotest.Encrypt(t, pub, pass)},
			{Name: "UNUSED", Value: "nobody references me"},
			{Name: "PORT", Value: "9999"}, // the app defines PORT itself
		},
		Server: []command.SharedVar{{Name: "BASE", Value: "shop.example"}},
	}
}

const shopCompose = "services:\n  web:\n    image: shop\n    environment:\n      HOST: ${SMTP_HOST}\n      PASS: ${SMTP_PASS:-none}\n      PORT: ${PORT}\n      DIR: ${WF_APP_DIR}\n"

func readEnvFile(t *testing.T, path string) (map[string]string, os.FileMode) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	return parseEnv(raw), info.Mode().Perm()
}

func TestRenderWritesOnlyReferencedSharedValues(t *testing.T) {
	r, pub := newSecretRepo(t)
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "8080", "URL": "https://${BASE}"})
	dir := r.appDataDir("app-1")

	plain, mode := readEnvFile(t, filepath.Join(dir, envSharedRel))
	if mode != 0o644 {
		t.Fatalf(".env.shared mode = %v", mode)
	}
	absDir, _ := filepath.Abs(dir)
	absData, _ := filepath.Abs(r.cfg.GetAgentDataDir())
	for name, want := range map[string]string{
		"SMTP_HOST": "mail.example", "BASE": "shop.example", // BASE is referenced from the app's .env
		"WF_SERVER_ID": "s1", "WF_SERVER_NAME": "box", "WF_ORG_ID": "o1", "WF_APP_ID": "app-1",
		"WF_APP_SLUG": slugify("Shop App"), "WF_APP_DIR": absDir, "WF_DATA_DIR": absData,
	} {
		if plain[name] != want {
			t.Errorf(".env.shared %s = %q, want %q", name, plain[name], want)
		}
	}
	if _, ok := plain["WF_TZ"]; !ok {
		t.Error("WF_TZ missing")
	}
	for _, never := range []string{"UNUSED", "PORT", "SMTP_PASS"} {
		if _, ok := plain[never]; ok {
			t.Errorf(".env.shared carries %s", never)
		}
	}
	secrets, mode := readEnvFile(t, filepath.Join(dir, envSharedSecretsRel))
	if mode != 0o600 || !reflect.DeepEqual(secrets, map[string]string{"SMTP_PASS": "s3cret"}) {
		t.Fatalf(".env.shared.secrets = %v (%v)", secrets, mode)
	}
	if got := r.StaleApps(); len(got) != 0 {
		t.Fatalf("a freshly rendered app is stale: %v", got)
	}
	// No secret referenced any more -> the secrets file goes away.
	sharedApp(t, r, "app-1", "services: {}\n", nil)
	if _, err := os.Stat(filepath.Join(dir, envSharedSecretsRel)); !os.IsNotExist(err) {
		t.Fatalf(".env.shared.secrets survived: %v", err)
	}
}

func TestSyncDoesNotRenderButFlagsStale(t *testing.T) {
	r, pub := newSecretRepo(t)
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "8080"})
	before, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), envSharedRel))

	// Re-encrypting the same secret and changing an unreferenced name: fresh.
	snap := testSnapshot(pub, t, 2, "mail.example", "s3cret")
	snap.Org[2].Value = "changed but unreferenced"
	syncVars(t, r, snap)
	if got := r.StaleApps(); len(got) != 0 {
		t.Fatalf("stale after a no-op change: %v", got)
	}
	// Rotating the secret and the host: stale, files untouched.
	resp := syncVars(t, r, testSnapshot(pub, t, 3, "relay.example", "n3w"))
	if resp.Revision != 3 || len(resp.Undecryptable) != 0 {
		t.Fatalf("sync response = %+v", resp)
	}
	after, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), envSharedRel))
	if string(before) != string(after) {
		t.Fatal("vars.sync rendered files")
	}
	if got := r.StaleApps(); !reflect.DeepEqual(got, map[string][]string{"app-1": {"SMTP_HOST", "SMTP_PASS"}}) {
		t.Fatalf("stale = %v", got)
	}
	if r.VarsRevision() != 3 {
		t.Fatalf("revision = %d", r.VarsRevision())
	}
	// An older snapshot is ignored.
	if resp := syncVars(t, r, testSnapshot(pub, t, 2, "old.example", "old")); resp.Revision != 3 {
		t.Fatalf("older snapshot applied: %+v", resp)
	}
}

func TestStaleIgnoresUnreferencedBuiltinsOnUpgrade(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "plain-app", "services: {}\n", nil)
	sharedApp(t, r, "smtp-app", shopCompose, map[string]string{"PORT": "1"})
	// Upgrade: the apps were deployed by an agent without shared variables.
	for _, id := range []string{"plain-app", "smtp-app"} {
		_ = os.Remove(filepath.Join(r.appDataDir(id), envSharedRel))
		_ = os.Remove(filepath.Join(r.appDataDir(id), envSharedSecretsRel))
	}
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	got := r.StaleApps()
	if _, ok := got["plain-app"]; ok {
		t.Fatalf("an app referencing nothing is stale: %v", got)
	}
	if !reflect.DeepEqual(got["smtp-app"], []string{"SMTP_HOST", "SMTP_PASS", "WF_APP_DIR"}) {
		t.Fatalf("smtp-app stale = %v", got["smtp-app"])
	}
}

func TestSyncReportsUndecryptableByScope(t *testing.T) {
	r, _ := newSecretRepo(t)
	other := cryptotest.AgentKey(t, t.TempDir()) // a different server's key
	resp := syncVars(t, r, command.SyncVarsRequest{Revision: 1,
		Org:    []command.SharedVar{{Name: "TOKEN", IsSecret: true, Ciphertext: cryptotest.Encrypt(t, other, "x")}},
		Server: []command.SharedVar{{Name: "LOST", IsSecret: true}}, // missing, not undecryptable
	})
	if !reflect.DeepEqual(resp.Undecryptable, []command.VarRef{{Scope: "org", Name: "TOKEN"}}) {
		t.Fatalf("undecryptable = %+v", resp.Undecryptable)
	}
}

func TestRefsCacheFollowsHeadAndSourceLock(t *testing.T) {
	r, _ := newSecretRepo(t)
	sharedApp(t, r, "app-1", "x: ${FIRST}\n", nil)
	if refs := r.appRefs("app-1"); !refs["FIRST"] {
		t.Fatalf("refs = %v", refs)
	}
	dir := r.appDataDir("app-1")
	// Edited on disk without a commit: HEAD is unchanged, so the cache holds.
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("x: ${SECOND}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if refs := r.appRefs("app-1"); !refs["FIRST"] || refs["SECOND"] {
		t.Fatalf("cache not used: %v", refs)
	}
	// A moved source.lock is part of the key: rescanned.
	if err := writeSourceLock(dir, "abc123"); err != nil {
		t.Fatal(err)
	}
	if refs := r.appRefs("app-1"); !refs["SECOND"] || refs["FIRST"] {
		t.Fatalf("source.lock change not noticed: %v", refs)
	}
	// A new commit (save) is a new key too.
	sharedApp(t, r, "app-1", "x: ${THIRD}\n", nil)
	if refs := r.appRefs("app-1"); !refs["THIRD"] || refs["SECOND"] {
		t.Fatalf("HEAD change not noticed: %v", refs)
	}
	r.forgetApp("app-1")
	r.refMu.Lock()
	_, cached := r.refCache["app-1"]
	r.refMu.Unlock()
	if cached {
		t.Fatal("forgetApp kept the refs cache entry")
	}
}

func TestWithStaleAttachesNames(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	got := r.withStale([]command.AppStatus{{AppID: "app-1"}, {AppID: "other"}})
	if !reflect.DeepEqual(got[0].StaleVars, []string{"SMTP_HOST", "SMTP_PASS"}) || got[1].StaleVars != nil {
		t.Fatalf("withStale = %+v", got)
	}
}

// Ruling P6: a draft deploys nothing, so it neither renders the shared files
// nor clears the stale flag.
func TestDraftSaveDoesNotRenderOrClearStale(t *testing.T) {
	r, pub := newSecretRepo(t)
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "8080"})
	path := filepath.Join(r.appDataDir("app-1"), envSharedRel)
	before, _ := os.ReadFile(path)
	syncVars(t, r, testSnapshot(pub, t, 2, "relay.example", "s3cret"))
	want := map[string][]string{"app-1": {"SMTP_HOST"}}
	if got := r.StaleApps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stale before draft = %v", got)
	}
	cfg := `{"name":"Shop App","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[{"name":"PORT","is_encrypted":false}]}`
	if _, err := r.SaveAppDraft(context.Background(), command.AppPayload{
		AppID: "app-1", Config: []byte(cfg),
		Files:     []command.ContentItem{{Name: "compose.yml", Content: []byte(shopCompose + "# draft\n")}},
		Variables: []command.ContentItem{{Name: "PORT", Content: []byte("8081")}},
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("a draft save rendered .env.shared")
	}
	if got := r.StaleApps(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stale after draft = %v", got)
	}
	// Deploying (the non-draft save path) renders and clears it.
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "8080"})
	if got := r.StaleApps(); len(got) != 0 {
		t.Fatalf("stale after deploy save = %v", got)
	}
}

// Ruling P9b: a recompute racing a render must not resurrect the flag the
// render clears.
func TestRenderAndRecomputeDoNotRace(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			r.RecomputeStale()
		}
	}()
	for i := 0; i < 20; i++ {
		if err := r.renderShared("app-1"); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if got := r.StaleApps(); len(got) != 0 {
		t.Fatalf("stale after render = %v", got)
	}
}

// A commit outside a deploy (rename, or the .gitignore guard folding into
// one) moves HEAD: that only invalidates the refs cache, and the app stays
// fresh unless a referenced value really changed.
func TestHeadMoveOutsideDeployOnlyRescans(t *testing.T) {
	r, pub := newSecretRepo(t)
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	sharedApp(t, r, "app-1", shopCompose+"      SLUG: ${WF_APP_SLUG}\n", map[string]string{"PORT": "1"})
	head, _ := gitHead(r.appDataDir("app-1"))
	if err := r.RenameApp(context.Background(), "app-1", "Other Name"); err != nil {
		t.Fatal(err)
	}
	if moved, _ := gitHead(r.appDataDir("app-1")); moved == head {
		t.Fatal("rename did not commit")
	}
	r.RecomputeStale()
	// The slug is referenced and changed with the rename; nothing else did.
	if got := r.StaleApps(); !reflect.DeepEqual(got, map[string][]string{"app-1": {"WF_APP_SLUG"}}) {
		t.Fatalf("stale after rename = %v", got)
	}
	r.refMu.Lock()
	key := r.refCache["app-1"].key
	r.refMu.Unlock()
	if want, _ := refKey(r.appDataDir("app-1")); key != want {
		t.Fatalf("refs cache key = %q, want %q", key, want)
	}
}
