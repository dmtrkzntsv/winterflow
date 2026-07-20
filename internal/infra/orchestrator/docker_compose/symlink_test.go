package dockercompose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My App!":        "my-app",
		"grafana":        "grafana",
		"  Weird--Name ": "weird-name",
		"Ünïcode Näme":   "n-code-n-me", // non-ascii runs become separators
		"":               "app",
		"---":            "app",
		"UPPER_case.2":   "upper-case-2",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func symlinkEnv(t *testing.T) (appsDir string) {
	t.Helper()
	root := t.TempDir()
	appsDir = filepath.Join(root, "apps")
	if err := os.MkdirAll(filepath.Join(root, "apps-data", "id-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "apps-data", "id-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	return appsDir
}

func TestEnsureAppSymlinkCreatesRelativeLink(t *testing.T) {
	appsDir := symlinkEnv(t)

	link, err := ensureAppSymlink(appsDir, "apps-data", "id-1", "My App")
	if err != nil {
		t.Fatal(err)
	}
	if link != "my-app" {
		t.Fatalf("link = %q", link)
	}
	target, err := os.Readlink(filepath.Join(appsDir, "my-app"))
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join("..", "apps-data", "id-1") {
		t.Fatalf("target = %q (must be relative)", target)
	}
	// The link resolves to the real dir.
	if fi, err := os.Stat(filepath.Join(appsDir, "my-app")); err != nil || !fi.IsDir() {
		t.Fatalf("link does not resolve: %v", err)
	}
}

func TestEnsureAppSymlinkRenameSwapsLink(t *testing.T) {
	appsDir := symlinkEnv(t)
	if _, err := ensureAppSymlink(appsDir, "apps-data", "id-1", "old-name"); err != nil {
		t.Fatal(err)
	}
	link, err := ensureAppSymlink(appsDir, "apps-data", "id-1", "new-name")
	if err != nil {
		t.Fatal(err)
	}
	if link != "new-name" {
		t.Fatalf("link = %q", link)
	}
	if _, err := os.Lstat(filepath.Join(appsDir, "old-name")); !os.IsNotExist(err) {
		t.Fatal("old link should be gone after rename")
	}
}

func TestEnsureAppSymlinkCollisionGetsSuffix(t *testing.T) {
	appsDir := symlinkEnv(t)
	if _, err := ensureAppSymlink(appsDir, "apps-data", "id-1", "web"); err != nil {
		t.Fatal(err)
	}
	link, err := ensureAppSymlink(appsDir, "apps-data", "id-2", "web")
	if err != nil {
		t.Fatal(err)
	}
	// Suffix is appID[:8]; "id-2" is shorter, so the whole id is used.
	if link != "web-id-2" {
		t.Fatalf("collision link = %q, want web-id-2", link)
	}
	// Re-ensuring the same app+name is idempotent (no extra suffixing).
	again, err := ensureAppSymlink(appsDir, "apps-data", "id-1", "web")
	if err != nil || again != "web" {
		t.Fatalf("idempotent ensure = %q, %v", again, err)
	}
}

func TestRemoveAppSymlink(t *testing.T) {
	appsDir := symlinkEnv(t)
	_, _ = ensureAppSymlink(appsDir, "apps-data", "id-1", "gone")
	_, _ = ensureAppSymlink(appsDir, "apps-data", "id-2", "stays")

	if err := removeAppSymlink(appsDir, "id-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(appsDir, "gone")); !os.IsNotExist(err) {
		t.Fatal("id-1 link should be removed")
	}
	if _, err := os.Lstat(filepath.Join(appsDir, "stays")); err != nil {
		t.Fatal("id-2 link must survive")
	}
}

func TestHealAppSymlinks(t *testing.T) {
	appsDir := symlinkEnv(t)
	// Dangling link to a deleted app + missing link for a live one.
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "apps-data", "deleted-app"), filepath.Join(appsDir, "zombie")); err != nil {
		t.Fatal(err)
	}

	err := healAppSymlinks(appsDir, "apps-data", map[string]string{
		"id-1": "Alpha",
		"id-2": "Beta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(appsDir, "zombie")); !os.IsNotExist(err) {
		t.Fatal("dangling link should be pruned")
	}
	for _, name := range []string{"alpha", "beta"} {
		if fi, err := os.Stat(filepath.Join(appsDir, name)); err != nil || !fi.IsDir() {
			t.Fatalf("missing healed link %q: %v", name, err)
		}
	}
}
