package catalog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	feed "winterflow/internal/domain/catalog"
)

// fixture is a winterflow-apps checkout in miniature: two installable
// apps (actual-budget with a logo; plausible with merge keys, extra files
// and no logo) and one folder per kind of skip.
const fixture = "testdata/winterflow-apps"

var at = time.Date(2026, 10, 14, 20, 5, 43, 0, time.UTC)

func options(t *testing.T, apps string) Options {
	t.Helper()
	return Options{
		Apps: apps, Out: t.TempDir(), BaseURL: "https://winterflow.io/catalog/",
		SourceCommit: "49bf28d", Now: func() time.Time { return at.Add(time.Second / 2) },
	}
}

// copyFixture is a writable copy of the fixture.
func copyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readFeed(t *testing.T, out string) (feed.Feed, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(out, FeedFile))
	if err != nil {
		t.Fatal(err)
	}
	f, err := feed.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return f, raw
}

func TestBuildTheFixture(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	o := options(t, fixture)
	o.SigningKey = key
	rep, err := Build(o)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Apps, []string{"actual-budget", "plausible"}) {
		t.Fatalf("apps = %v", rep.Apps)
	}
	wantSkips := []Skip{
		{"coming-soon", "on the waiting list"},
		{"gitea", "no metadata.json"},
		{"no-compose", "no compose/compose.yml"},
		{"traefik-ingress", "an agent extension (expose), not an app"},
	}
	if !reflect.DeepEqual(rep.Skipped, wantSkips) {
		t.Fatalf("skipped = %+v", rep.Skipped)
	}
	// Only the missing logo: the merged environments declare their
	// variables, and the Caddyfile's ${UPSTREAM} is not compose's.
	if !reflect.DeepEqual(rep.Warnings, []string{"plausible: no logo"}) {
		t.Fatalf("warnings = %q", rep.Warnings)
	}

	f, raw := readFeed(t, o.Out)
	if f.Format != feed.FormatVersion || f.SourceCommit != "49bf28d" || !f.GeneratedAt.Equal(at) || len(f.Apps) != 2 {
		t.Fatalf("feed = %+v", f)
	}
	sig, err := os.ReadFile(filepath.Join(o.Out, SigFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := feed.Verify(pub, raw, sig); err != nil {
		t.Fatal(err)
	}
	budget, plausible := f.Apps[0], f.Apps[1]
	if budget.Slug != "actual-budget" || budget.Name != "Actual Budget" || budget.License != "MIT" ||
		budget.LogoURL != "https://winterflow.io/catalog/logos/actual-budget.svg" ||
		!reflect.DeepEqual(budget.AlternativeTo, []string{"YNAB", "Mint"}) || budget.HomepageURL != "https://actualbudget.org" {
		t.Fatalf("actual-budget = %+v", budget)
	}
	if plausible.LogoURL != "" || len(plausible.Files) != 3 ||
		!strings.Contains(plausible.Files["clickhouse/logs.xml"], "<level>warning</level>") || plausible.Files["Caddyfile"] == "" {
		t.Fatalf("plausible = %+v", plausible)
	}
	for _, e := range f.Apps {
		if _, _, err := e.Check(); err != nil {
			t.Fatalf("%s does not pass the sync's check: %v", e.Slug, err)
		}
	}
	logo, err := os.ReadFile(filepath.Join(o.Out, LogoDir, "actual-budget.svg"))
	if err != nil || !bytes.HasPrefix(logo, []byte("<svg")) {
		t.Fatalf("logo = %q, %v", logo, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(o.Out, LogoDir)); len(entries) != 1 {
		t.Fatalf("logos = %v", entries)
	}

	// The same tree builds the same bytes.
	again := o
	again.Out = t.TempDir()
	if _, err := Build(again); err != nil {
		t.Fatal(err)
	}
	if _, raw2 := readFeed(t, again.Out); !bytes.Equal(raw, raw2) {
		t.Fatal("two builds of one tree differ")
	}
}

// Without a key no .sig is written, and one left from an earlier signed
// build is removed with the logos it no longer lists.
func TestBuildUnsigned(t *testing.T) {
	o := options(t, fixture)
	for _, f := range []string{SigFile, LogoDir + "/gone.png"} {
		p := filepath.Join(o.Out, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Build(o); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{SigFile, LogoDir + "/gone.png"} {
		if _, err := os.Stat(filepath.Join(o.Out, filepath.FromSlash(f))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s: %v", f, err)
		}
	}
	readFeed(t, o.Out)
}

// Every invalid app is listed, and nothing is written.
func TestBuildListsEveryBadApp(t *testing.T) {
	apps := copyFixture(t)
	if err := os.Remove(filepath.Join(apps, "apps/plausible/compose/Caddyfile")); err != nil {
		t.Fatal(err)
	}
	budget := filepath.Join(apps, "apps/actual-budget/compose/compose.yml")
	raw, _ := os.ReadFile(budget)
	if err := os.WriteFile(budget, bytes.Replace(raw, []byte("volumes:\n  data:\n"), []byte("volumes:\n  data: { name: budget }\n"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(apps, "apps/renamed/compose"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"apps/README.md":                     "not an app folder",
		"apps/insecure/metadata.json":        `{"name": "Insecure", "homepage_url": "http://insecure.test"}`,
		"apps/insecure/compose/compose.yml":  "services: {app: {image: x}}\n",
		"apps/warned/metadata.json":          `{"name": "Warned"}`,
		"apps/warned/compose/compose.yml":    "services: {app: {image: x}}\nx-winterflow: {variables: {UNUSED: {}}}\n",
		"apps/renamed/metadata.json":         `{"slug": "other", "name": "Renamed"}`,
		"apps/renamed/compose/compose.yml":   "services: {app: {image: x}}\n",
		"apps/traefik-ingress/metadata.json": `{"name": "Traefik", "extension": "expose"`,
	} {
		p := filepath.Join(apps, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := options(t, apps)
	rep, err := Build(o)
	var be *BuildError
	if !errors.As(err, &be) || len(be.Problems) != 5 {
		t.Fatalf("err = %v", err)
	}
	for i, want := range []string{
		"actual-budget: invalid x-winterflow: volumes.data: the name \"budget\" would be shared",
		"insecure: invalid catalog: homepage_url: \"http://insecure.test\" is not an https URL",
		"plausible: x-winterflow.files: Caddyfile: no such file or directory",
		"renamed: metadata.json: slug \"other\" is not the folder name",
		"traefik-ingress: metadata.json: unexpected end of JSON input",
	} {
		if !strings.HasPrefix(be.Problems[i], want) {
			t.Errorf("problem %d = %q, want %q", i, be.Problems[i], want)
		}
	}
	if !strings.HasPrefix(err.Error(), "5 apps are invalid:\n  actual-budget: ") {
		t.Fatalf("message = %q", err)
	}
	// The valid apps' warnings are still reported.
	if !reflect.DeepEqual(rep.Warnings, []string{"warned: no logo", "warned: x-winterflow.variables.UNUSED: no service environment and no override uses it"}) {
		t.Fatalf("warnings = %q", rep.Warnings)
	}
	if entries, _ := os.ReadDir(o.Out); len(entries) != 0 {
		t.Fatalf("a failed build wrote %v", entries)
	}
}

func TestBuildRefuses(t *testing.T) {
	o := options(t, fixture)
	o.BaseURL = "http://winterflow.io/catalog/"
	if _, err := Build(o); err == nil || !strings.Contains(err.Error(), "--base-url") {
		t.Fatalf("an http base URL: %v", err)
	}
	o = options(t, t.TempDir())
	if _, err := Build(o); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no apps folder: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(o.Apps, "apps", "gitea"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(o); err == nil || err.Error() != "no installable app" {
		t.Fatalf("nothing installable: %v", err)
	}
}

func TestCLI(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	seed := base64.StdEncoding.EncodeToString(key.Seed())
	env := map[string]string{"CATALOG_SIGNING_KEY": seed, "BAD_KEY": "not base64"}
	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := Main(args, &stdout, &stderr, func(k string) string { return env[k] })
		return code, stdout.String(), stderr.String()
	}
	out := t.TempDir()
	code, stdout, stderr := run("build", "--apps", fixture, "--out", out, "--base-url", "https://winterflow.io/catalog", "--sign-key-env", "CATALOG_SIGNING_KEY")
	if code != 0 || !strings.Contains(stdout, "skipped gitea: no metadata.json\n") || !strings.Contains(stdout, "warning: plausible: no logo\n") ||
		!strings.HasSuffix(stdout, "wrote catalog.json: 2 apps, signed\n") || stderr != "" {
		t.Fatalf("build = %d\n%s\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, SigFile)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		code int
		err  string
	}{
		{[]string{"build", "--apps", fixture, "--out", out, "--base-url", "https://x.test", "--sign-key-env", "UNSET"}, 1, "UNSET is not set"},
		{[]string{"build", "--apps", fixture, "--out", out, "--base-url", "https://x.test", "--sign-key-env", "BAD_KEY"}, 1, "BAD_KEY: catalog signing key"},
		{[]string{"build", "--apps", t.TempDir(), "--out", out, "--base-url", "https://x.test"}, 1, "catalog build: open"},
		{[]string{"build", "--apps", fixture}, 2, "usage:"},
		{[]string{"build", "--nope"}, 2, "flag provided but not defined"},
		{nil, 2, "usage:"},
		{[]string{"publish"}, 2, "usage:"},
	} {
		code, _, stderr := run(c.args...)
		if code != c.code || !strings.Contains(stderr, c.err) {
			t.Errorf("%v = %d, %q", c.args, code, stderr)
		}
	}
	code, stdout, _ = run("keygen")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if code != 0 || len(lines) != 2 || !strings.HasPrefix(lines[0], "CATALOG_PUBLIC_KEY=") || !strings.HasPrefix(lines[1], "CATALOG_SIGNING_KEY=") {
		t.Fatalf("keygen = %d %q", code, stdout)
	}
	pub, err := feed.ParsePublicKey(strings.TrimPrefix(lines[0], "CATALOG_PUBLIC_KEY="))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := feed.ParsePrivateKey(strings.TrimPrefix(lines[1], "CATALOG_SIGNING_KEY="))
	if err != nil || !pub.Equal(priv.Public()) {
		t.Fatalf("keygen pair: %v", err)
	}
}
