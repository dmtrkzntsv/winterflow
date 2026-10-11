package storages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func s3(name, secret string) model.Storage {
	return model.Storage{Name: name, Type: model.StorageS3, S3: &model.S3Storage{
		Endpoint: "https://s3.example.com/", Bucket: "backups", Prefix: "wf", AccessKeyID: "AKIA", SecretAccessKey: secret,
	}}
}

func TestRegistryPutGetListDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "storages")
	r := NewRegistry(dir)
	if got, err := r.List(); err != nil || got != nil {
		t.Fatalf("missing dir: %v, %v", got, err)
	}
	if _, ok, err := r.Get("none"); ok || err != nil {
		t.Fatalf("get missing: %v, %v", ok, err)
	}
	if err := r.Put(s3("hetzner", "CIPHERTEXT")); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb/"}}); err != nil {
		t.Fatal(err)
	}

	got, err := r.List()
	if err != nil || len(got) != 2 || got[0].Name != "disk" || got[1].Name != "hetzner" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	// Stored normalized; the secret stays ciphertext at rest.
	if got[0].Local.Path != "/mnt/usb" || got[1].S3.Prefix != "wf/" || got[1].S3.Endpoint != "https://s3.example.com" {
		t.Fatalf("not normalized: %+v %+v", got[0].Local, got[1].S3)
	}
	s, ok, err := r.Get("hetzner")
	if !ok || err != nil || s.S3.SecretAccessKey != "CIPHERTEXT" {
		t.Fatalf("get = %+v, %v, %v", s, ok, err)
	}

	// Files are 0600 in a 0700 folder; no temp file is left.
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", info.Mode())
	}
	info, _ = os.Stat(filepath.Join(dir, "hetzner.json"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "hetzner.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("temp file left")
	}

	// Replacing keeps one file.
	if err := r.Put(s3("hetzner", "NEW")); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := r.Get("hetzner"); s.S3.SecretAccessKey != "NEW" {
		t.Fatalf("replace = %+v", s.S3)
	}

	existed, err := r.Delete("hetzner")
	if !existed || err != nil {
		t.Fatalf("delete = %v, %v", existed, err)
	}
	if existed, err := r.Delete("hetzner"); existed || err != nil {
		t.Fatalf("second delete = %v, %v", existed, err)
	}
	if got, _ := r.List(); len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestRegistryRefusesBadInput(t *testing.T) {
	r := NewRegistry(t.TempDir())
	if err := r.Put(s3("Bad", "C")); err == nil {
		t.Fatal("an invalid storage must not be stored")
	}
	if err := r.Put(s3("x", model.EncryptedSecret)); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("placeholder stored: %v", err)
	}
	if _, _, err := r.Get("../etc"); err == nil {
		t.Fatal("get must validate the name")
	}
	if _, err := r.Delete("../etc"); err == nil {
		t.Fatal("delete must validate the name")
	}
}

// TestRegistrySkipsDamagedFiles: one broken file must not hide the others.
func TestRegistrySkipsDamagedFiles(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	if err := r.Put(s3("good", "C")); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"broken.json":  "{",
		"renamed.json": `{"name":"other","type":"local","local":{"path":"/mnt"}}`,
		"invalid.json": `{"name":"invalid","type":"ftp"}`,
		"notes.txt":    "ignored",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := r.List()
	if err != nil || len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if _, ok, err := r.Get("broken"); ok || err == nil {
		t.Fatalf("get of a damaged file = %v, %v", ok, err)
	}
}

func TestRegistryUnreadableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "storages")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(file)
	if _, err := r.List(); err == nil {
		t.Fatal("a storages path that is a file must fail the listing")
	}
	if err := r.Put(s3("x", "C")); err == nil {
		t.Fatal("put under a file must fail")
	}
	if r.Dir() != file {
		t.Fatal("Dir")
	}
}
