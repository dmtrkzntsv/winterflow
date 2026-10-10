package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// write creates a file (and its directories) under dir.
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// git runs a git command in dir with a fixed identity.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	// Isolate from the developer's global config (signing, hooks, templates).
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gateRepo builds a repo whose second commit adds lines 3-5 to a.go and a
// new web file (the first commit is tagged "base"), then chdirs into it.
func gateRepo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "go.mod", "module example.com/m\n")
	write(t, dir, "a.go", "package m\n\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "tag", "base")
	write(t, dir, "a.go", "package m\n\nfunc A() int {\n\treturn 1\n}\n")
	write(t, dir, "web/src/x.ts", "export const x = 1;\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "change")
	t.Chdir(dir)
}

func TestRunGate(t *testing.T) {
	gateRepo(t)
	write(t, ".", "web/coverage/lcov.info", "SF:src/x.ts\nDA:1,1\nend_of_record\n")

	// a.go's function body (lines 3-5) never ran: Go fails, web passes.
	write(t, ".", "cover.out", "mode: atomic\nexample.com/m/a.go:3.14,5.2 1 0\n")
	var out, errb bytes.Buffer
	code := run([]string{"--base", "base", "--go", "cover.out", "--lcov", "web/coverage/lcov.info"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "a.go:3") || !strings.Contains(out.String(), "web  1/1") {
		t.Fatalf("unexpected report:\n%s", out.String())
	}

	// Once it ran, the gate passes.
	write(t, ".", "cover.out", "mode: atomic\nexample.com/m/a.go:3.14,5.2 1 2\n")
	out.Reset()
	if code := run([]string{"--base", "base", "--go", "cover.out", "--lcov", "web/coverage/lcov.info"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out.String())
	}
}

func TestRunGateInputErrors(t *testing.T) {
	gateRepo(t)
	write(t, ".", "cover.out", "mode: atomic\nexample.com/m/a.go:3.14,5.2 1 0\n")
	write(t, ".", "empty.out", "mode: atomic\n")
	cases := map[string][]string{
		"no base":         {"--go", "cover.out"},
		"no reports":      {"--base", "base"},
		"unknown base":    {"--base", "nope", "--go", "cover.out"},
		"missing profile": {"--base", "base", "--go", "missing.out"},
		"empty profile":   {"--base", "base", "--go", "empty.out"},
		"missing lcov":    {"--base", "base", "--lcov", "missing.info"},
		"bad flag":        {"--bogus"},
	}
	for name, args := range cases {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%s: exit %d, want 2\n%s%s", name, code, out.String(), errb.String())
		}
	}
}

func TestRunRatchetCommand(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	write(t, dir, "go.mod", "module example.com/m\n")
	write(t, dir, "cover.out", "mode: atomic\nexample.com/m/a.go:1.1,2.2 3 1\nexample.com/m/a.go:3.1,4.2 1 0\n")
	write(t, dir, "lcov.info", "SF:src/x.ts\nDA:1,1\nDA:2,0\nend_of_record\n")

	write(t, dir, "base.json", `{"go": 75.0, "web": 50.0}`)
	var out, errb bytes.Buffer
	args := []string{"ratchet", "--go", "cover.out", "--lcov", "lcov.info", "--baseline", "base.json"}
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0\n%s%s", code, out.String(), errb.String())
	}

	write(t, dir, "base.json", `{"go": 80.0, "web": 50.0}`)
	out.Reset()
	if code := run(args, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1 (go 75.0 < 80.0)\n%s", code, out.String())
	}

	for name, a := range map[string][]string{
		"missing baseline": {"ratchet", "--go", "cover.out", "--lcov", "lcov.info", "--baseline", "nope.json"},
		"no lcov":          {"ratchet", "--go", "cover.out"},
		"bad profile":      {"ratchet", "--go", "lcov.info", "--lcov", "lcov.info", "--baseline", "base.json"},
	} {
		if code := run(a, &out, &errb); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
}

func TestRunGateAllNoData(t *testing.T) {
	gateRepo(t)
	// The report knows neither changed file (e.g. a wrong --lcov-root or
	// absolute lcov paths): that is an input error, not a pass.
	write(t, ".", "cover.out", "mode: atomic\nexample.com/m/other.go:1.1,2.2 1 1\n")
	write(t, ".", "lcov.info", "SF:/abs/elsewhere/y.ts\nDA:1,1\nend_of_record\n")
	for name, args := range map[string][]string{
		"go":  {"--base", "base", "--go", "cover.out"},
		"web": {"--base", "base", "--lcov", "lcov.info"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("%s: exit %d, want 2\n%s%s", name, code, out.String(), errb.String())
		}
		if !strings.Contains(errb.String(), "no coverage data for any changed "+name+" file") {
			t.Errorf("%s: stderr missing hint: %q", name, errb.String())
		}
	}

	// Some changed files with data, others without: today's behaviour.
	write(t, ".", "cover.out", "mode: atomic\nexample.com/m/a.go:3.14,5.2 1 1\n")
	var out, errb bytes.Buffer
	if code := run([]string{"--base", "base", "--go", "cover.out"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, want 0\n%s%s", code, out.String(), errb.String())
	}
}
