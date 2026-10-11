package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto/cryptotest"
)

const storageCompose = `services:
  app:
    image: litestream/litestream
    environment:
      BUCKET: ${WF_STORAGE_HETZNER_BUCKET}
      KEY_ID: ${WF_STORAGE_HETZNER_ACCESS_KEY_ID}
      SECRET: ${WF_STORAGE_HETZNER_SECRET_ACCESS_KEY}
      DIR: $WF_STORAGE_BACKUP_DISK_PATH
`

// withStorages registers an s3 storage "hetzner" (secret "s3-plain") and a
// local "backup-disk", plus an unreferenced "other".
func withStorages(t *testing.T, r *Repository, encrypt func(string) string) {
	t.Helper()
	for _, s := range []model.Storage{
		{Name: "hetzner", Type: model.StorageS3, S3: &model.S3Storage{Endpoint: "https://s3.example.com", Region: "fsn1",
			Bucket: "tenants", Prefix: "wf/", AccessKeyID: "AKIA", SecretAccessKey: encrypt("s3-plain")}},
		{Name: "backup-disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb"}},
		{Name: "other", Type: model.StorageS3, S3: &model.S3Storage{Endpoint: "https://o.example.com", Bucket: "other-bucket",
			AccessKeyID: "OTHER", SecretAccessKey: encrypt("other-plain")}},
	} {
		if err := r.storages.Put(s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMaterializeStorageEnv(t *testing.T) {
	r, pub := newSecretRepo(t)
	withStorages(t, r, func(p string) string { return cryptotest.Encrypt(t, pub, p) })
	savedApp(t, r, "app-1", "demo", storageCompose)
	dir := r.appDataDir("app-1")

	if err := r.materializeStorageEnv(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, storageEnvRel))
	if err != nil {
		t.Fatal(err)
	}
	want := "WF_STORAGE_BACKUP_DISK_PATH=/mnt/usb\n" +
		"WF_STORAGE_HETZNER_ACCESS_KEY_ID=AKIA\n" +
		"WF_STORAGE_HETZNER_BUCKET=tenants\n" +
		"WF_STORAGE_HETZNER_SECRET_ACCESS_KEY=s3-plain\n"
	if string(raw) != want {
		t.Fatalf("storage.env =\n%s\nwant\n%s", raw, want)
	}
	// Only what the compose file mentions: never another storage's key.
	if strings.Contains(string(raw), "OTHER") || strings.Contains(string(raw), "other-plain") {
		t.Fatal("an unreferenced storage leaked into the app")
	}
	info, _ := os.Stat(filepath.Join(dir, storageEnvRel))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}

	// The plaintext never enters the app's history, even after a rollback
	// restored a .gitignore that doesn't list it.
	writeFileT(t, dir, gitignoreRel, ".env.secrets\n")
	if _, err := gitCommitAll(dir, "old gitignore"); err != nil {
		t.Fatal(err)
	}
	if tracked(t, dir, storageEnvRel) {
		t.Fatal("storage.env was committed")
	}

	// No references any more: the file goes away.
	writeFileT(t, dir, "compose.yml", "services: {}\n")
	if err := r.materializeStorageEnv(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, storageEnvRel)); !os.IsNotExist(err) {
		t.Fatal("storage.env kept without references")
	}
	if err := r.materializeStorageEnv(dir); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

// tracked reports whether rel is in HEAD's tree.
func tracked(t *testing.T, dir, rel string) bool {
	t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := commit.Tree()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.File(rel)
	return err == nil
}

func TestMaterializeStorageEnvRefusesUnknownStorage(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x: ${WF_STORAGE_GONE_BUCKET}\n")
	err := r.materializeStorageEnv(r.appDataDir("app-1"))
	if !errors.Is(err, model.ErrStorageRef) || !strings.Contains(err.Error(), `there is no storage "gone" on this server`) {
		t.Fatalf("err = %v", err)
	}
	// A deploy stops before docker is ever invoked.
	_, err = r.SaveApp(context.Background(), command.AppPayload{
		AppID:  "app-2",
		Config: []byte(`{"name":"two","files":[{"filename":"compose.yml"}]}`),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte("x: ${WF_BACKUP_DB_PATH}\n")}},
	})
	if !errors.Is(err, model.ErrStorageRef) || !strings.Contains(err.Error(), `there is no backup "db" in this app`) {
		t.Fatalf("SaveApp err = %v", err)
	}
}

func TestMaterializeStorageEnvUndecryptableSecret(t *testing.T) {
	r, _ := newSecretRepo(t)
	withStorages(t, r, func(string) string { return "not-ciphertext" })
	savedApp(t, r, "app-1", "demo", storageCompose)
	err := r.materializeStorageEnv(r.appDataDir("app-1"))
	if err == nil || !strings.Contains(err.Error(), "decrypt the secret access key of storage hetzner") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.appDataDir("app-1"), storageEnvRel)); !os.IsNotExist(err) {
		t.Fatal("a failed resolution wrote storage.env")
	}
}

func TestGetAppReportsStorageVariables(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", storageCompose+"      B: ${WF_BACKUP_DB_PATH}\n")
	resp, err := r.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []command.StorageVariable{
		{Name: "WF_STORAGE_BACKUP_DISK_PATH", Storage: "backup-disk"},
		{Name: "WF_STORAGE_HETZNER_ACCESS_KEY_ID", Storage: "hetzner"},
		{Name: "WF_STORAGE_HETZNER_BUCKET", Storage: "hetzner"},
		{Name: "WF_STORAGE_HETZNER_SECRET_ACCESS_KEY", Storage: "hetzner", Secret: true},
	}
	if !reflect.DeepEqual(resp.StorageVariables, want) {
		t.Fatalf("storage variables = %+v", resp.StorageVariables)
	}
	// Reported even before the storages exist: they are names, not values.
	resp, _ = r.GetApp(context.Background(), "app-1")
	for _, v := range resp.App.Variables {
		if strings.HasPrefix(v.Name, "WF_STORAGE_") {
			t.Fatalf("a storage variable leaked into the user's variables: %+v", v)
		}
	}
}

// The committed layout ignores storage.env.
func TestGitignoreListsStorageEnv(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "services: {}\n")
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), gitignoreRel))
	if !strings.Contains(string(raw), storageEnvRel+"\n") {
		t.Fatalf(".gitignore = %q", raw)
	}
}

// A storage value can never inject a line (a second variable, a comment) or an
// expansion into storage.env: it is refused or read back verbatim.
func TestMarshalStorageEnvRefusesLineBreaks(t *testing.T) {
	for _, bad := range []string{"/mnt/a\nWF_STORAGE_EVIL_PATH=/etc", "/mnt/a\rB=1", "/mnt/a\x00b"} {
		out, err := marshalStorageEnv([]resolvedItem{{name: "WF_STORAGE_DISK_PATH", content: []byte(bad)}})
		if err == nil || !strings.Contains(err.Error(), "WF_STORAGE_DISK_PATH contains a line break") {
			t.Fatalf("%q: err = %v, out = %q", bad, err, out)
		}
		if out != nil {
			t.Fatalf("%q: partial output %q", bad, out)
		}
	}
}

func TestMaterializeStorageEnvRefusesInjectedLine(t *testing.T) {
	r := newTestRepo(t)
	if err := r.storages.Put(model.Storage{Name: "disk", Type: model.StorageLocal,
		Local: &model.LocalStorage{Path: "/mnt/a\nWF_STORAGE_EVIL_PATH=/etc"}}); err != nil {
		t.Skipf("the registry already refuses this path: %v", err)
	}
	savedApp(t, r, "app-1", "demo", "x: ${WF_STORAGE_DISK_PATH}\n")
	err := r.materializeStorageEnv(r.appDataDir("app-1"))
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.appDataDir("app-1"), storageEnvRel)); !os.IsNotExist(err) {
		t.Fatal("a refused value still wrote storage.env")
	}
}

func TestMarshalStorageEnvQuoting(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"/mnt/usb":               "/mnt/usb",
		"https://s3.example.com": "https://s3.example.com",
		"wf/":                    "wf/",
		"AbC+/9=":                "AbC+/9=",
		"/mnt/my disk":           "'/mnt/my disk'",
		"/mnt/$HOME":             "'/mnt/$HOME'",
		"/mnt/a#b":               "'/mnt/a#b'",
		`/mnt/a\b"c`:             `'/mnt/a\b"c'`,
		"/mnt/it's":              `"/mnt/it's"`,
		`/mnt/a\`:                `"/mnt/a\\"`,
		`\`:                      `"\\"`,
		`/mnt/it's $X \ "q"`:     `"/mnt/it's \$X \\ \"q\""`,
	}
	for in, want := range cases {
		out, err := marshalStorageEnv([]resolvedItem{{name: "WF_STORAGE_D_PATH", content: []byte(in)}})
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := string(out); got != "WF_STORAGE_D_PATH="+want+"\n" {
			t.Errorf("%q => %q, want %q", in, got, "WF_STORAGE_D_PATH="+want+"\n")
		}
	}
}

// The quoting is checked against the real consumer: docker compose must read
// every awkward value back verbatim, with no expansion and no extra variable.
func TestStorageEnvRoundTripsThroughDockerCompose(t *testing.T) {
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose is not available")
	}
	values := []string{
		"/mnt/usb", "", "/mnt/my disk", "/mnt/$HOME/${USER}", "/mnt/a#b # c", `/mnt/a\nb\\c"d`,
		"/mnt/it's", `/mnt/it's $HOME \ "q" #h`, "  padded  ", "a=b=c", "$(touch pwned)",
		`/mnt/a\`, `\`, `/mnt/my disk\`,
	}
	dir := t.TempDir()
	writeFileT(t, dir, "compose.yml", "services:\n  a:\n    image: x\n    environment:\n      V: \"${V}\"\n      EVIL: \"${WF_STORAGE_EVIL_PATH:-unset}\"\n")
	for _, v := range values {
		out, err := marshalStorageEnv([]resolvedItem{{name: "V", content: []byte(v)}})
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		writeFileT(t, dir, "e.env", string(out))
		cmd := exec.Command("docker", "compose", "-f", "compose.yml", "--env-file", "e.env", "config", "--format", "json")
		cmd.Dir = dir
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%q: docker compose config: %v: %s", v, err, raw)
		}
		var cfg struct {
			Services map[string]struct {
				Environment map[string]string `json:"environment"`
			} `json:"services"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("%q: %v: %s", v, err, raw)
		}
		// config re-escapes a literal $ as $$ when it prints; a surviving
		// expansion would show up as the expanded text instead.
		env := cfg.Services["a"].Environment
		if got := strings.ReplaceAll(env["V"], "$$", "$"); got != v || env["EVIL"] != "unset" {
			t.Errorf("%q read back as V=%q EVIL=%q", v, env["V"], env["EVIL"])
		}
	}
}
