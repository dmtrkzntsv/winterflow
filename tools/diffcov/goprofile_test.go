package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleProfile = `mode: atomic
winterflow/internal/a.go:10.20,12.3 2 1
winterflow/internal/a.go:12.3,15.2 3 0
winterflow/internal/a.go:20.1,20.30 0 0
winterflow/internal/b.go:5.1,6.2 1 0
winterflow/internal/b.go:5.1,6.2 1 4
`

func TestParseGoProfile(t *testing.T) {
	p, err := ParseGoProfile(strings.NewReader(sampleProfile), "winterflow")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		file          string
		line          int
		exec, covered bool
	}{
		{"internal/a.go", 9, false, false},  // before any block
		{"internal/a.go", 10, true, true},   // first line of a covered block
		{"internal/a.go", 12, true, true},   // shared boundary: one covering block suffices
		{"internal/a.go", 13, true, false},  // inside an uncovered block
		{"internal/a.go", 15, true, false},  // last line of the uncovered block
		{"internal/a.go", 16, false, false}, // between blocks (comment, blank)
		{"internal/a.go", 20, false, false}, // zero-statement block isn't executable
		{"internal/b.go", 5, true, true},    // duplicate block: highest count wins
		{"internal/c.go", 1, false, false},  // file not in profile
	}
	for _, c := range cases {
		exec, cov := p.Line(c.file, c.line)
		if exec != c.exec || cov != c.covered {
			t.Errorf("%s:%d = (%v,%v), want (%v,%v)", c.file, c.line, exec, cov, c.exec, c.covered)
		}
	}
	if !p.Has("internal/a.go") || p.Has("internal/c.go") {
		t.Error("Has: wrong file membership")
	}
}

func TestGoProfileTotal(t *testing.T) {
	p, err := ParseGoProfile(strings.NewReader(sampleProfile), "winterflow")
	if err != nil {
		t.Fatal(err)
	}
	// Statements: a.go 2 (covered) + 3 + 0, b.go 1 (deduped, covered).
	cov, total := p.Total()
	if cov != 3 || total != 6 {
		t.Fatalf("Total = %d/%d, want 3/6", cov, total)
	}
}

func TestParseGoProfileErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":     "",
		"mode only": "mode: atomic\n",
		"no mode":   "winterflow/a.go:1.1,2.2 1 1\n",
		"garbage":   "mode: set\nnot a block\n",
	} {
		if _, err := ParseGoProfile(strings.NewReader(in), "winterflow"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestModulePath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(f, []byte("// comment\nmodule example.com/x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := modulePath(f)
	if err != nil || got != "example.com/x" {
		t.Fatalf("modulePath = %q, %v", got, err)
	}
	if _, err := modulePath(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("want error for missing go.mod")
	}
}
