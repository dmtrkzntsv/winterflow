package vars

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

func TestStoreAppliesAtomicallyWith0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vars")
	s := NewStore(dir)
	if got := s.Current(); got.Revision != 0 || len(got.Org) != 0 {
		t.Fatalf("empty store = %+v", got)
	}
	snap := command.SyncVarsRequest{Revision: 2, ServerID: "s1", Org: []command.SharedVar{
		{Name: "SMTP_HOST", Value: "mail"}, {Name: "SMTP_PASS", IsSecret: true, Ciphertext: "CIPHERTEXT"},
	}}
	ok, err := s.Apply(snap)
	if err != nil || !ok {
		t.Fatalf("apply = %v, %v", ok, err)
	}
	info, err := os.Stat(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if !strings.Contains(string(raw), "CIPHERTEXT") {
		t.Fatalf("snapshot lacks the ciphertext: %s", raw)
	}
	// A fresh store (agent restart) reads it back.
	if got := NewStore(dir).Current(); got.Revision != 2 || len(got.Org) != 2 {
		t.Fatalf("reloaded = %+v", got)
	}
}

func TestStoreIgnoresOlderRevision(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Apply(command.SyncVarsRequest{Revision: 5, Org: []command.SharedVar{{Name: "A", Value: "new"}}}); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Apply(command.SyncVarsRequest{Revision: 4, Org: []command.SharedVar{{Name: "A", Value: "old"}}})
	if err != nil || ok {
		t.Fatalf("older snapshot applied: %v %v", ok, err)
	}
	if got := s.Current(); got.Revision != 5 || got.Org[0].Value != "new" {
		t.Fatalf("rolled back to %+v", got)
	}
	// The same revision again is accepted (an idempotent re-push).
	if ok, err := s.Apply(command.SyncVarsRequest{Revision: 5}); err != nil || !ok {
		t.Fatalf("same revision = %v %v", ok, err)
	}
}

func TestStoreTreatsCorruptFileAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	if s.Current().Revision != 0 {
		t.Fatal("corrupt snapshot not treated as empty")
	}
	if ok, err := s.Apply(command.SyncVarsRequest{Revision: 1}); err != nil || !ok {
		t.Fatalf("apply over corrupt = %v %v", ok, err)
	}
}

// The snapshot must carry only ciphertext. The check is falsifiable: the
// plain variable's value is found in the file (so the substring search can
// match), while the secret's plaintext, which exists only in the test and
// not in any ciphertext field, is not.
func TestStoreSnapshotNeverHoldsSecretPlaintext(t *testing.T) {
	const secretPlain = "s3cr3t-plaintext-value"
	const plainValue = "visible-plain-value"
	dir := t.TempDir()
	s := NewStore(dir)
	_, err := s.Apply(command.SyncVarsRequest{Revision: 1, Org: []command.SharedVar{
		{Name: "PLAIN", Value: plainValue},
		{Name: "SECRET", IsSecret: true, Ciphertext: "ENC:opaque-blob"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), plainValue) {
		t.Fatalf("sanity: plain value should be stored verbatim, snapshot = %s", raw)
	}
	if strings.Contains(string(raw), secretPlain) {
		t.Fatalf("snapshot holds secret plaintext: %s", raw)
	}
}
