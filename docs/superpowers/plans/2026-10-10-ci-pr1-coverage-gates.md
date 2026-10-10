# CI PR 1 — CI Workflow and Coverage Gates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every PR runs gofmt, vet, race-enabled Go tests, web lint/tests/build and the installer suite in CI; changed lines must be ≥80% covered per language; total coverage may not drop below a committed baseline; releases ship only commits that passed CI.

**Architecture:** A stdlib-only Go command `tools/diffcov` parses `git diff --unified=0`, the Go coverprofile and the web lcov file, and runs two checks: the diff gate (default mode) and the ratchet (`diffcov ratchet`). The web app gets Vitest (jsdom, v8 coverage → `web/coverage/lcov.info`) with its first real tests. A new `ci.yml` runs `go`, `web` and `installer` in parallel, then a `coverage` job that downloads both reports and runs diffcov. `release.yml` switches from `push` to `workflow_run` on `ci`.

**Tech Stack:** Go 1.25 (stdlib only for the tool), GitHub Actions, pnpm 10, Node 22, Vitest 5 + @vitest/coverage-v8, Testing Library, jsdom.

**Spec:** `docs/superpowers/specs/2026-10-10-ci-and-api-contracts-design.md`, section "PR 1 — CI and coverage gates" only. PR 2 (OpenAPI) and PR 3 (contract suite) get their own plans.

**Measured at plan time (HEAD `6a5504d`):** `go test -covermode=atomic -coverprofile=/tmp/c.out ./... && go tool cover -func=/tmp/c.out | tail -1` → **59.4%** (5514 statements, 3276 covered; the same with `-race`). `go test -race ./...` passes. `gofmt -l .` lists exactly one file: `internal/app/web/handler/user/handler.go` (one extra blank line at line 86). `bash scripts/install_test.sh` passes. `pnpm --dir web run lint` passes. Expected after this PR: Go ≈ 61.4% (tools/diffcov adds ~339 statements at ~94%), web ≈ 5.4%. The baseline is measured in Task 4, not copied from here.

## Global Constraints

- Go module is `winterflow`, `go 1.25.3` in `go.mod`; no `toolchain` line. CI installs Go from `go-version-file: go.mod`.
- `tools/diffcov` is **stdlib only**: no new `require` lines in `go.mod`; `go mod tidy` must leave `go.mod`/`go.sum` unchanged.
- Diff gate threshold: `--min 80`, per language; a language with zero executable changed lines passes.
- Excluded paths, verbatim from the spec: `*_test.go`, `*.test.ts(x)`, `*.pb.go`, `web/src/components/ui/**`, `cmd/**/main.go`, `tools/**`, `web/src/main.tsx`.
- Ratchet: fail if a total is more than **0.1** points below its baseline; at **≥1.0** above, print exactly `::notice::raise coverage-baseline.json <lang> to <value>`. Nothing is auto-committed.
- `coverage-baseline.json` shape: `{ "go": <float>, "web": <float> }`; values measured at the end of this PR.
- Web dev deps, pinned exact (all MIT): `vitest@5.0.3`, `@vitest/coverage-v8@5.0.3`, `@testing-library/react@16.3.3`, `@testing-library/dom@10.4.2` (required peer of the other two Testing Library packages), `@testing-library/user-event@14.6.7`, `@testing-library/jest-dom@7.0.1`, `jsdom@30.1.2`. Vitest 5 needs Node `^22.12`, jsdom 30 needs Node `^22.22.2`; `setup-node` with `node-version: 22` resolves the latest 22.x.
- CI uses pnpm **10** (`pnpm/action-setup@v4`, `version: 10`) with `--frozen-lockfile`. If your local `pnpm --version` is not 10.x, run every lockfile-changing command as `npx -y pnpm@10 …` so the lockfile is written by the same major CI reads it with.
- Reuse the action versions already in `release.yml`: `actions/checkout@v4`, `actions/setup-go@v5`, `actions/setup-node@v4`, `pnpm/action-setup@v4`. Artifacts use GitHub's own `actions/upload-artifact@v4` / `actions/download-artifact@v4`. No other third-party actions.
- CI must not need network beyond GitHub, the npm registry and the Go module proxy.
- Never touch `web/src/components/ui/**` and never remove a dependency from `web/package.json` (vendored shadcn/ui is intentional).
- `web/dist/.gitkeep` must stay committed. `vite build` deletes it; after any local `pnpm --dir web run build`, run `git checkout -- web/dist/.gitkeep` and confirm `git status` shows no deletion.
- No env vars are added, so `.env.example` / `web/.env.example` stay unchanged.
- Commits: Conventional Commits, imperative; each message ends with a blank line then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Do not push.
- The PR description must flag the `release.yml` trigger change (push → `workflow_run`) for the user's explicit confirmation before merge.

## Review Focus

1. **A coverage report that is empty or missing** (tests crashed before writing, artifact path wrong) must fail the job with exit 2. It must not pass as "no executable changed lines". Pinned by `TestParseGoProfileErrors`, `TestParseLcovErrors` and the `empty profile` / `missing lcov` cases of `TestRunGateInputErrors` (Task 1).
2. **The PR base SHA is not in the clone** (shallow checkout, wrong ref). `git diff` must fail loudly with a hint about `fetch-depth: 0`. An empty diff that passes would be wrong. Pinned by the `unknown base` case of `TestRunGateInputErrors` (Task 1).
3. **A changed file the report doesn't know about** (a new Go package, an lcov path-prefix mismatch such as `src/x.ts` vs `web/src/x.ts`) must show in the output under "no coverage data". It must not vanish silently. Pinned by `TestEvaluate` (`internal/new.go` in `NoData`) and the repo-relative mapping assertions in `TestParseLcov` (Task 1).
4. **Unusual diff content**: an added line whose text starts with `++ ` renders as `+++ …`, and a path can contain spaces (git appends a TAB) or quotes (git C-quotes it). Each must still map to the right file and line. Pinned by `TestParseDiff` and `TestParseDiffPathQuoting` (Task 1).
5. **Release from the wrong run or commit.** A PR whose head branch is also named `main` (for example from a fork) completes `ci` with `head_branch == main`. It must not release. A release must tag the SHA CI tested, not whatever main points at later. Pinned by the `if:`/`ref:` grep assertions in Task 5 Step 3.

---

## File Map

| Path | Action | Responsibility |
|---|---|---|
| `tools/diffcov/diff.go` | create | `ParseDiff`: added line numbers per file from `git diff --unified=0` |
| `tools/diffcov/goprofile.go` | create | `ParseGoProfile`, `GoProfile.Line/Has/Total`, `modulePath` |
| `tools/diffcov/lcov.go` | create | `ParseLcov`, `Lcov.Line/Has/Total` |
| `tools/diffcov/classify.go` | create | `Language`, `Excluded`, `LangGo`/`LangWeb` |
| `tools/diffcov/gate.go` | create | `Coverage` interface, `Result`, `Evaluate`, `Report` |
| `tools/diffcov/ratchet.go` | create (Task 2) | `LoadBaseline`, `Percent`, `CheckRatchet`, `RunRatchet`, `runRatchet` |
| `tools/diffcov/main.go` | create (Task 1), modify (Task 2) | flags, `git diff` exec, `run` dispatch |
| `tools/diffcov/*_test.go` | create | unit tests per file + `main_test.go` end-to-end against a temp git repo |
| `web/package.json`, `web/pnpm-lock.yaml` | modify | test dev deps + `test` / `test:coverage` scripts |
| `web/vite.config.ts` | modify | `test` block: jsdom, setup file, v8 coverage → lcov |
| `web/src/test/setup.ts` | create | jest-dom matchers + `cleanup` |
| `web/src/lib/volumes.ts` | create | `volumeNameError`, `shortVolumeName`, `groupVolumes` (moved out of the page) |
| `web/src/lib/volumes.test.ts` | create | pure helper tests |
| `web/src/pages/settings-volumes.tsx` | modify | import the helpers; use `groupVolumes` |
| `web/src/pages/settings-volumes.test.tsx` | create | `VolumesCard` render/interaction tests |
| `web/eslint.config.js`, `web/.gitignore` | modify | ignore `coverage/` |
| `internal/app/web/handler/user/handler.go` | modify | gofmt (remove the extra blank line) |
| `Makefile`, `.gitignore` | modify | `test`, `web-test`, `cover`, `diffcov` targets; ignore `coverage.out` |
| `coverage-baseline.json` | create | measured ratchet baseline |
| `.github/workflows/ci.yml` | create | go / web / installer / coverage jobs |
| `.github/workflows/release.yml` | modify | `workflow_run` gating on `ci` |
| `AGENTS.md` | modify | document the test commands and the gates |

---

### Task 1: `tools/diffcov` — the diff-coverage gate

**Files:**
- Create: `tools/diffcov/diff.go`, `tools/diffcov/goprofile.go`, `tools/diffcov/lcov.go`, `tools/diffcov/classify.go`, `tools/diffcov/gate.go`, `tools/diffcov/main.go`
- Test: `tools/diffcov/diff_test.go`, `tools/diffcov/goprofile_test.go`, `tools/diffcov/lcov_test.go`, `tools/diffcov/classify_test.go`, `tools/diffcov/gate_test.go`, `tools/diffcov/main_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces (package `main` in `tools/diffcov`, used by Task 2):
  - `func ParseDiff(r io.Reader) (map[string][]int, error)`
  - `type Block struct{ StartLine, EndLine int; Stmts, Count int }`; `type GoProfile map[string][]Block`
  - `func ParseGoProfile(r io.Reader, module string) (GoProfile, error)`; methods `Line(file string, line int) (executable, covered bool)`, `Has(file string) bool`, `Total() (covered, total int)` (statements)
  - `func modulePath(goMod string) (string, error)`
  - `type Lcov map[string]map[int]int`; `func ParseLcov(r io.Reader, root string) (Lcov, error)`; methods `Line`, `Has`, `Total() (covered, total int)` (lines)
  - `const LangGo = "go"`, `LangWeb = "web"`; `func Language(p string) string`; `func Excluded(p string) bool`
  - `type Coverage interface{ Line(string, int) (bool, bool); Has(string) bool }`
  - `type Result struct{ Lang string; Covered, Total int; Uncovered, NoData []string }` with `Percent() float64`, `Pass(min float64) bool`
  - `func Evaluate(changed map[string][]int, reports map[string]Coverage) []Result` (always `[go, web]`)
  - `func Report(w io.Writer, results []Result, min float64, github bool) bool`
  - `func run(args []string, stdout, stderr io.Writer) int`; `func runGate(...) int`; `func loadGoProfile(file, goMod string) (GoProfile, error)`; `func loadLcov(file, root string) (Lcov, error)`
- CLI: `go run ./tools/diffcov --base <ref> [--go coverage.out] [--lcov web/coverage/lcov.info] [--lcov-root web] [--gomod go.mod] [--min 80] [--github]`. Exit codes: 0 = pass, 1 = below `--min`, 2 = usage or input error.

Semantics fixed here (they resolve spec gaps):
- Go: a changed line is executable if it lies in `[StartLine, EndLine]` of a block with `Stmts > 0`. It is covered if any such block has `Count > 0`. Duplicate blocks keep the max count.
- Web: only `web/src/**` `.ts/.tsx/.js/.jsx` files (not `.d.ts`) count, which matches Vitest's coverage `include`. Vitest writes `SF:` paths relative to `web/`, so `--lcov-root web` (the default) prefixes them.
- A changed file in a language whose report has no record of it is listed under "no coverage data". This does not fail the gate. It happens for types-only files, which have no blocks or DA lines.
- `git diff` is run as `git -c core.quotePath=false diff --unified=0 --no-color --no-ext-diff -M <base>...HEAD`.

- [ ] **Step 1: Write the failing diff-parser test**

Create `tools/diffcov/diff_test.go`:

```go
package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDiff(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/internal/a.go b/internal/a.go",
		"index 1111111..2222222 100644",
		"--- a/internal/a.go",
		"+++ b/internal/a.go",
		"@@ -3,0 +4,2 @@ func A() {",
		"+\tx := 1",
		"+\ty := 2",
		"@@ -10 +12 @@ func B() {",
		"-\told()",
		"+\tnew()",
		"@@ -20,3 +21,0 @@",
		"-\tgone()",
		"-\tgone()",
		"-\tgone()",
		"diff --git a/web/src/b.tsx b/web/src/b.tsx",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/web/src/b.tsx",
		"@@ -0,0 +1,3 @@",
		"+++ this added line starts with two pluses",
		"+line two",
		"+line three",
		"diff --git a/old.go b/old.go",
		"deleted file mode 100644",
		"--- a/old.go",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-package old",
		"-",
		"diff --git a/img.png b/img.png",
		"Binary files a/img.png and b/img.png differ",
	}, "\n")

	got, err := ParseDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"internal/a.go": {4, 5, 12},
		"web/src/b.tsx": {1, 2, 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseDiffPathQuoting(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/dir/with space.go b/dir/with space.go",
		"--- a/dir/with space.go\t",
		"+++ b/dir/with space.go\t",
		"@@ -1 +1 @@",
		"+package dir",
		`diff --git "a/q\"uote.go" "b/q\"uote.go"`,
		`--- "a/q\"uote.go"`,
		`+++ "b/q\"uote.go"`,
		"@@ -0,0 +7 @@",
		"+x",
	}, "\n")

	got, err := ParseDiff(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{
		"dir/with space.go": {1},
		`q"uote.go`:         {7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseDiffBadHunk(t *testing.T) {
	in := "diff --git a/x.go b/x.go\n+++ b/x.go\n@@ garbage @@\n"
	if _, err := ParseDiff(strings.NewReader(in)); err == nil {
		t.Fatal("want error for malformed hunk header")
	}
}

func TestParseDiffEmpty(t *testing.T) {
	got, err := ParseDiff(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./tools/diffcov/ -run ParseDiff`
Expected: FAIL, build error `undefined: ParseDiff`.

- [ ] **Step 3: Implement the diff parser**

Create `tools/diffcov/diff.go`:

```go
package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// hunkRE matches a unified-diff hunk header and captures the new-side start
// line and (optional) line count: "@@ -10,2 +12,3 @@".
var hunkRE = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ParseDiff reads `git diff --unified=0` output and returns the added line
// numbers per repo-relative file path (new side). Deleted files and pure
// deletions contribute nothing. "+++ " is only treated as a file header
// between "diff --git" and the first hunk, so an added line whose content
// starts with "++ " is still counted as an added line.
func ParseDiff(r io.Reader) (map[string][]int, error) {
	out := map[string][]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var file string
	inHeader := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			inHeader, file = true, ""
		case inHeader && strings.HasPrefix(line, "+++ "):
			name, err := diffPath(strings.TrimPrefix(line, "+++ "))
			if err != nil {
				return nil, err
			}
			file = name
		case strings.HasPrefix(line, "@@ "):
			inHeader = false
			if file == "" {
				continue
			}
			m := hunkRE.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("bad hunk header in %s: %q", file, line)
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			for i := 0; i < count; i++ {
				out[file] = append(out[file], start+i)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read diff: %w", err)
	}
	return out, nil
}

// diffPath turns the target of a "+++ " header into a repo-relative path.
// It returns "" for /dev/null (a deleted file). Git appends a tab to names
// containing spaces and C-quotes names with special characters.
func diffPath(s string) (string, error) {
	s = strings.TrimSuffix(s, "\t")
	if s == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		u, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("unquote diff path %s: %w", s, err)
		}
		s = u
	}
	return strings.TrimPrefix(s, "b/"), nil
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./tools/diffcov/ -run ParseDiff -v`
Expected: PASS (`TestParseDiff`, `TestParseDiffPathQuoting`, `TestParseDiffBadHunk`, `TestParseDiffEmpty`).

- [ ] **Step 5: Write the failing coverprofile test**

Create `tools/diffcov/goprofile_test.go`:

```go
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
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./tools/diffcov/ -run 'GoProfile|ModulePath'`
Expected: FAIL, `undefined: ParseGoProfile` / `undefined: modulePath`.

- [ ] **Step 7: Implement the coverprofile parser**

Create `tools/diffcov/goprofile.go`:

```go
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Block is one coverprofile block, reduced to the line range it spans.
type Block struct {
	StartLine, EndLine int
	Stmts, Count       int
}

// GoProfile maps a repo-relative file path to its coverage blocks.
type GoProfile map[string][]Block

// blockRE matches "path/file.go:12.34,15.2 3 1".
var blockRE = regexp.MustCompile(`^(.+\.go):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$`)

// ParseGoProfile reads a `go test -coverprofile` file. module is the Go
// module path (from go.mod); it is stripped so paths match git's
// repo-relative ones. Duplicate blocks (same file and position, as merged
// profiles produce) keep the highest count. An empty profile (no mode line,
// or no blocks) is an error: it means the tests didn't run, and treating it
// as "nothing executable" would pass the gate silently.
func ParseGoProfile(r io.Reader, module string) (GoProfile, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	type key struct {
		file     string
		position string
	}
	seen := map[key]int{} // index into p[file]
	p := GoProfile{}
	sawMode := false
	blocks := 0
	prefix := module + "/"
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode: ") {
			sawMode = true
			continue
		}
		m := blockRE.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("bad coverprofile line %q", line)
		}
		file := strings.TrimPrefix(m[1], prefix)
		start, _ := strconv.Atoi(m[2])
		end, _ := strconv.Atoi(m[4])
		stmts, _ := strconv.Atoi(m[6])
		count, _ := strconv.Atoi(m[7])
		k := key{file, m[2] + "." + m[3] + "," + m[4] + "." + m[5]}
		if i, ok := seen[k]; ok {
			if count > p[file][i].Count {
				p[file][i].Count = count
			}
			continue
		}
		seen[k] = len(p[file])
		p[file] = append(p[file], Block{StartLine: start, EndLine: end, Stmts: stmts, Count: count})
		blocks++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read coverprofile: %w", err)
	}
	if !sawMode || blocks == 0 {
		return nil, errors.New("coverprofile is empty (did the tests run?)")
	}
	return p, nil
}

// Line reports whether a line of file is executable (inside a block with at
// least one statement) and, if so, whether any such block ran.
func (p GoProfile) Line(file string, line int) (executable, covered bool) {
	for _, b := range p[file] {
		if b.Stmts == 0 || line < b.StartLine || line > b.EndLine {
			continue
		}
		executable = true
		if b.Count > 0 {
			return true, true
		}
	}
	return executable, false
}

// Has reports whether the profile has any block for file.
func (p GoProfile) Has(file string) bool { return len(p[file]) > 0 }

// Total returns covered and total statements across the profile — the same
// figure `go tool cover -func` prints as "total:".
func (p GoProfile) Total() (covered, total int) {
	for _, blocks := range p {
		for _, b := range blocks {
			total += b.Stmts
			if b.Count > 0 {
				covered += b.Stmts
			}
		}
	}
	return covered, total
}

// modulePath reads the module path from a go.mod file.
func modulePath(goMod string) (string, error) {
	f, err := os.Open(goMod)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s: no module line", goMod)
}
```

- [ ] **Step 8: Run it to verify it passes**

Run: `go test ./tools/diffcov/ -run 'GoProfile|ModulePath' -v`
Expected: PASS.

- [ ] **Step 9: Write the failing lcov test**

Create `tools/diffcov/lcov_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

const sampleLcov = `TN:
SF:src/lib/volumes.ts
FN:5,volumeNameError
DA:5,3
DA:6,0
DA:7,2,abcdef
LF:3
LH:2
end_of_record
TN:
SF:src/pages/x.tsx
DA:1,0
end_of_record
SF:src/pages/x.tsx
DA:1,1
DA:2,0
end_of_record
SF:src/types.ts
end_of_record
`

func TestParseLcov(t *testing.T) {
	l, err := ParseLcov(strings.NewReader(sampleLcov), "web")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		file          string
		line          int
		exec, covered bool
	}{
		{"web/src/lib/volumes.ts", 5, true, true},
		{"web/src/lib/volumes.ts", 6, true, false},
		{"web/src/lib/volumes.ts", 7, true, true}, // checksum field ignored
		{"web/src/lib/volumes.ts", 8, false, false},
		{"web/src/pages/x.tsx", 1, true, true}, // repeated record: max hits
		{"web/src/pages/x.tsx", 2, true, false},
		{"src/lib/volumes.ts", 5, false, false}, // only repo-relative paths
	}
	for _, c := range cases {
		exec, cov := l.Line(c.file, c.line)
		if exec != c.exec || cov != c.covered {
			t.Errorf("%s:%d = (%v,%v), want (%v,%v)", c.file, c.line, exec, cov, c.exec, c.covered)
		}
	}
	if !l.Has("web/src/types.ts") || l.Has("web/src/missing.ts") {
		t.Error("Has: wrong file membership")
	}
	cov, total := l.Total()
	if cov != 3 || total != 5 {
		t.Fatalf("Total = %d/%d, want 3/5", cov, total)
	}
}

func TestParseLcovAbsolutePathKept(t *testing.T) {
	l, err := ParseLcov(strings.NewReader("SF:/abs/a.ts\nDA:1,1\nend_of_record\n"), "web")
	if err != nil {
		t.Fatal(err)
	}
	if !l.Has("/abs/a.ts") {
		t.Fatalf("got %v", l)
	}
}

func TestParseLcovErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":     "",
		"no files":  "TN:\n",
		"orphan DA": "DA:1,1\n",
		"bad DA":    "SF:a.ts\nDA:x,1\n",
		"short DA":  "SF:a.ts\nDA:1\n",
	} {
		if _, err := ParseLcov(strings.NewReader(in), "web"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
```

- [ ] **Step 10: Run it to verify it fails**

Run: `go test ./tools/diffcov/ -run Lcov`
Expected: FAIL, `undefined: ParseLcov`.

- [ ] **Step 11: Implement the lcov parser**

Create `tools/diffcov/lcov.go`:

```go
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// Lcov maps a repo-relative file path to line → hit count, from DA records.
type Lcov map[string]map[int]int

// ParseLcov reads an lcov tracefile. Vitest writes SF paths relative to the
// web project root, so root ("web") is joined in front of relative paths to
// make them repo-relative. Repeated records for a file or line keep the
// highest hit count. A tracefile with no SF record is an error (the coverage
// run didn't happen or wrote nothing).
func ParseLcov(r io.Reader, root string) (Lcov, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	out := Lcov{}
	var file string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "SF:"):
			sf := strings.TrimPrefix(line, "SF:")
			if !path.IsAbs(sf) {
				sf = path.Join(root, sf)
			}
			file = sf
			if out[file] == nil {
				out[file] = map[int]int{}
			}
		case strings.HasPrefix(line, "DA:"):
			if file == "" {
				return nil, fmt.Errorf("lcov DA record outside a file: %q", line)
			}
			parts := strings.Split(strings.TrimPrefix(line, "DA:"), ",")
			if len(parts) < 2 {
				return nil, fmt.Errorf("bad lcov DA record %q", line)
			}
			n, err1 := strconv.Atoi(parts[0])
			hits, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bad lcov DA record %q", line)
			}
			if prev, ok := out[file][n]; !ok || hits > prev {
				out[file][n] = hits
			}
		case line == "end_of_record":
			file = ""
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read lcov: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("lcov has no files (did the web tests run with --coverage?)")
	}
	return out, nil
}

// Line reports whether a line has a DA record and, if so, whether it ran.
func (l Lcov) Line(file string, line int) (executable, covered bool) {
	hits, ok := l[file][line]
	return ok, ok && hits > 0
}

// Has reports whether the tracefile has a record for file.
func (l Lcov) Has(file string) bool { _, ok := l[file]; return ok }

// Total returns covered and total lines across every DA record.
func (l Lcov) Total() (covered, total int) {
	for _, lines := range l {
		for _, hits := range lines {
			total++
			if hits > 0 {
				covered++
			}
		}
	}
	return covered, total
}
```

- [ ] **Step 12: Run it to verify it passes**

Run: `go test ./tools/diffcov/ -run Lcov -v`
Expected: PASS.

- [ ] **Step 13: Write the failing classifier test**

Create `tools/diffcov/classify_test.go`:

```go
package main

import "testing"

func TestLanguage(t *testing.T) {
	cases := map[string]string{
		"internal/app/web/routes.go":                         LangGo,
		"web/embed.go":                                       LangGo,
		"web/src/pages/settings-volumes.tsx":                 LangWeb,
		"web/src/lib/volumes.ts":                             LangWeb,
		"web/vite.config.ts":                                 "", // outside the coverage include
		"web/src/vite-env.d.ts":                              "",
		"README.md":                                          "",
		"web/src/index.css":                                  "",
		".github/workflows/ci.yml":                           "",
		"internal/app/web/routes_test.go":                    "",
		"web/src/lib/volumes.test.ts":                        "",
		"web/src/pages/settings-volumes.test.tsx":            "",
		"internal/infra/transport/grpc/proto/hub.pb.go":      "",
		"internal/infra/transport/grpc/proto/hub_grpc.pb.go": "",
		"web/src/components/ui/button.tsx":                   "",
		"web/src/components/app-status-badge.tsx":            LangWeb,
		"cmd/standalone/main.go":                             "",
		"cmd/standalone/serve.go":                            LangGo,
		"tools/diffcov/main.go":                              "",
		"web/src/main.tsx":                                   "",
		"src/lib/volumes.ts":                                 "", // TS outside web/
	}
	for p, want := range cases {
		if got := Language(p); got != want {
			t.Errorf("Language(%q) = %q, want %q", p, got, want)
		}
	}
}
```

- [ ] **Step 14: Run it to verify it fails**

Run: `go test ./tools/diffcov/ -run Language`
Expected: FAIL, `undefined: Language` / `undefined: LangGo`.

- [ ] **Step 15: Implement the classifier**

Create `tools/diffcov/classify.go`:

```go
package main

import (
	"path"
	"strings"
)

// Language names used in reports and the baseline file.
const (
	LangGo  = "go"
	LangWeb = "web"
)

// Language returns which coverage report owns a repo-relative path: "go"
// for .go files, "web" for TypeScript/JavaScript sources under web/src/
// (what Vitest's coverage includes; .d.ts files hold no code), "" for
// anything else (docs, config, assets) or an excluded path.
func Language(p string) string {
	if Excluded(p) {
		return ""
	}
	switch ext := path.Ext(p); {
	case ext == ".go":
		return LangGo
	case strings.HasPrefix(p, "web/src/") && !strings.HasSuffix(p, ".d.ts") &&
		(ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx"):
		return LangWeb
	}
	return ""
}

// Excluded reports whether a path is exempt from the diff-coverage gate:
// tests, generated protobuf code, vendored shadcn/ui, binary entry points,
// the coverage tooling itself, and the SPA bootstrap.
func Excluded(p string) bool {
	switch {
	case strings.HasSuffix(p, "_test.go"),
		strings.HasSuffix(p, ".test.ts"),
		strings.HasSuffix(p, ".test.tsx"),
		strings.HasSuffix(p, ".pb.go"),
		strings.HasPrefix(p, "web/src/components/ui/"),
		strings.HasPrefix(p, "tools/"),
		p == "web/src/main.tsx",
		strings.HasPrefix(p, "cmd/") && path.Base(p) == "main.go":
		return true
	}
	return false
}
```

- [ ] **Step 16: Run it to verify it passes**

Run: `go test ./tools/diffcov/ -run Language -v`
Expected: PASS.

- [ ] **Step 17: Write the failing gate test**

Create `tools/diffcov/gate_test.go`:

```go
package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func testReports(t *testing.T) map[string]Coverage {
	t.Helper()
	gp, err := ParseGoProfile(strings.NewReader(`mode: atomic
winterflow/internal/a.go:10.1,12.2 2 1
winterflow/internal/a.go:13.1,14.2 1 0
`), "winterflow")
	if err != nil {
		t.Fatal(err)
	}
	lc, err := ParseLcov(strings.NewReader(`SF:src/x.tsx
DA:1,1
DA:2,0
end_of_record
`), "web")
	if err != nil {
		t.Fatal(err)
	}
	return map[string]Coverage{LangGo: gp, LangWeb: lc}
}

func TestEvaluate(t *testing.T) {
	changed := map[string][]int{
		"internal/a.go":      {14, 10, 11, 20}, // 20 is outside every block
		"internal/new.go":    {1, 2},           // not in the profile at all
		"internal/a_test.go": {1},              // excluded
		"web/src/x.tsx":      {1, 2, 3},        // 3 has no DA record
		"docs/readme.md":     {1},              // no language
	}
	got := Evaluate(changed, testReports(t))
	want := []Result{
		{Lang: LangGo, Covered: 2, Total: 3, Uncovered: []string{"internal/a.go:14"}, NoData: []string{"internal/new.go"}},
		{Lang: LangWeb, Covered: 1, Total: 2, Uncovered: []string{"web/src/x.tsx:2"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestEvaluateSkipsMissingReport(t *testing.T) {
	reports := testReports(t)
	delete(reports, LangWeb)
	got := Evaluate(map[string][]int{"web/src/x.tsx": {2}}, reports)
	if got[1].Total != 0 || len(got[1].NoData) != 0 {
		t.Fatalf("web without a report should be skipped, got %+v", got[1])
	}
}

func TestResultPass(t *testing.T) {
	cases := []struct {
		covered, total int
		want           bool
	}{
		{0, 0, true},  // nothing executable passes
		{4, 5, true},  // exactly 80%
		{3, 4, false}, // 75%
		{79, 100, false},
		{80, 100, true},
		{0, 1, false},
	}
	for _, c := range cases {
		r := Result{Covered: c.covered, Total: c.total}
		if got := r.Pass(80); got != c.want {
			t.Errorf("Pass(80) for %d/%d = %v, want %v", c.covered, c.total, got, c.want)
		}
	}
}

func TestReport(t *testing.T) {
	results := []Result{
		{Lang: LangGo, Covered: 3, Total: 4, Uncovered: []string{"internal/a.go:14"}, NoData: []string{"internal/new.go"}},
		{Lang: LangWeb},
	}
	var buf bytes.Buffer
	if Report(&buf, results, 80, true) {
		t.Fatal("Report = pass, want fail (go at 75%)")
	}
	out := buf.String()
	for _, want := range []string{
		"go   3/4 changed lines covered (75.0%)  FAIL",
		"web  no executable changed lines  PASS",
		"  internal/a.go:14",
		"  internal/new.go",
		"::warning file=internal/a.go,line=14::changed line not covered by tests",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if !Report(&buf, []Result{{Lang: LangGo, Covered: 4, Total: 5}, {Lang: LangWeb}}, 80, false) {
		t.Fatalf("Report = fail, want pass:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "::warning") {
		t.Error("annotations printed without github flag")
	}
}
```

- [ ] **Step 18: Run it to verify it fails**

Run: `go test ./tools/diffcov/ -run 'Evaluate|ResultPass|Report'`
Expected: FAIL, `undefined: Evaluate` / `undefined: Result` / `undefined: Coverage`.

- [ ] **Step 19: Implement evaluation and the report**

Create `tools/diffcov/gate.go`:

```go
package main

import (
	"fmt"
	"io"
	"sort"
)

// Coverage answers, for one language's report, whether a changed line is
// executable and covered, and whether the report knows the file at all.
type Coverage interface {
	Line(file string, line int) (executable, covered bool)
	Has(file string) bool
}

// Result is one language's diff-coverage outcome.
type Result struct {
	Lang      string
	Covered   int      // executable changed lines that ran
	Total     int      // executable changed lines
	Uncovered []string // "file:line", sorted
	NoData    []string // changed files the report doesn't know, sorted
}

// Percent is Covered/Total in percent; 100 when nothing is executable.
func (r Result) Percent() float64 {
	if r.Total == 0 {
		return 100
	}
	return float64(r.Covered) * 100 / float64(r.Total)
}

// Pass reports whether the result meets min percent. A language with no
// executable changed lines passes.
func (r Result) Pass(min float64) bool {
	return r.Total == 0 || float64(r.Covered)*100 >= min*float64(r.Total)
}

// Evaluate classifies every changed line by language and checks it against
// that language's report. A nil report skips the language (Total stays 0).
func Evaluate(changed map[string][]int, reports map[string]Coverage) []Result {
	results := map[string]*Result{LangGo: {Lang: LangGo}, LangWeb: {Lang: LangWeb}}
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		lang := Language(f)
		rep := reports[lang]
		if lang == "" || rep == nil {
			continue
		}
		res := results[lang]
		if !rep.Has(f) {
			res.NoData = append(res.NoData, f)
			continue
		}
		lines := append([]int(nil), changed[f]...)
		sort.Ints(lines)
		for _, n := range lines {
			exec, cov := rep.Line(f, n)
			if !exec {
				continue
			}
			res.Total++
			if cov {
				res.Covered++
			} else {
				res.Uncovered = append(res.Uncovered, fmt.Sprintf("%s:%d", f, n))
			}
		}
	}
	return []Result{*results[LangGo], *results[LangWeb]}
}

// Report prints the gate summary and returns true when every language
// passes. With github set it also emits workflow annotations so uncovered
// lines show up inline on the PR diff.
func Report(w io.Writer, results []Result, min float64, github bool) bool {
	ok := true
	fmt.Fprintf(w, "diff coverage (min %.1f%%)\n", min)
	for _, r := range results {
		status := "PASS"
		if !r.Pass(min) {
			status, ok = "FAIL", false
		}
		if r.Total == 0 {
			fmt.Fprintf(w, "  %-3s  no executable changed lines  %s\n", r.Lang, status)
			continue
		}
		fmt.Fprintf(w, "  %-3s  %d/%d changed lines covered (%.1f%%)  %s\n",
			r.Lang, r.Covered, r.Total, r.Percent(), status)
	}
	for _, r := range results {
		if len(r.Uncovered) > 0 {
			fmt.Fprintf(w, "uncovered changed %s lines:\n", r.Lang)
			for _, u := range r.Uncovered {
				fmt.Fprintf(w, "  %s\n", u)
			}
		}
		if len(r.NoData) > 0 {
			fmt.Fprintf(w, "changed %s files with no coverage data (types only, excluded from coverage, or never loaded by a test):\n", r.Lang)
			for _, f := range r.NoData {
				fmt.Fprintf(w, "  %s\n", f)
			}
		}
	}
	if github {
		for _, r := range results {
			for _, u := range r.Uncovered {
				file, line := splitFileLine(u)
				fmt.Fprintf(w, "::warning file=%s,line=%s::changed line not covered by tests\n", file, line)
			}
		}
	}
	return ok
}

// splitFileLine splits "path:line" at the last colon.
func splitFileLine(s string) (file, line string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
```

- [ ] **Step 20: Run it to verify it passes**

Run: `go test ./tools/diffcov/ -run 'Evaluate|ResultPass|Report' -v`
Expected: PASS.

- [ ] **Step 21: Write the failing end-to-end test of the command**

Create `tools/diffcov/main_test.go`. It builds a throwaway git repo in a temp dir and calls `run` the way CI does. `t.Chdir` needs Go ≥1.24, which `go.mod` satisfies.

```go
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
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
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
```

- [ ] **Step 22: Run it to verify it fails**

Run: `go test ./tools/diffcov/ -run RunGate`
Expected: FAIL, `undefined: run`.

- [ ] **Step 23: Implement the command**

Create `tools/diffcov/main.go`:

```go
// Command diffcov is CI's diff-coverage gate:
//
//	diffcov --base <ref> --go coverage.out --lcov web/coverage/lcov.info [--min 80] [--github]
//
// checks that the lines added since <ref> (git diff <ref>...HEAD) are at
// least --min percent covered, per language. Stdlib only. Exit status:
// 0 pass, 1 coverage below the bar, 2 usage or input error.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runGate(args, stdout, stderr)
}

func runGate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diffcov", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "base ref or SHA; lines added in <base>...HEAD are checked (required)")
	goProfile := fs.String("go", "", "Go coverprofile (go test -coverprofile); empty skips Go")
	lcovPath := fs.String("lcov", "", "web lcov tracefile; empty skips web")
	lcovRoot := fs.String("lcov-root", "web", "directory lcov SF paths are relative to")
	goMod := fs.String("gomod", "go.mod", "go.mod to read the module path from")
	min := fs.Float64("min", 80, "minimum percent of executable changed lines covered, per language")
	github := fs.Bool("github", false, "also print GitHub Actions annotations for uncovered lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *base == "" || (*goProfile == "" && *lcovPath == "") {
		fmt.Fprintln(stderr, "diffcov: --base and at least one of --go/--lcov are required")
		return 2
	}

	reports, err := loadReports(*goProfile, *goMod, *lcovPath, *lcovRoot)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov:", err)
		return 2
	}
	diff, err := gitDiff(*base)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov:", err)
		return 2
	}
	changed, err := ParseDiff(bytes.NewReader(diff))
	if err != nil {
		fmt.Fprintln(stderr, "diffcov:", err)
		return 2
	}
	if !Report(stdout, Evaluate(changed, reports), *min, *github) {
		return 1
	}
	return 0
}

// loadReports parses whichever reports were given; an empty path leaves
// that language out of the map.
func loadReports(goProfile, goMod, lcovPath, lcovRoot string) (map[string]Coverage, error) {
	reports := map[string]Coverage{}
	if goProfile != "" {
		p, err := loadGoProfile(goProfile, goMod)
		if err != nil {
			return nil, err
		}
		reports[LangGo] = p
	}
	if lcovPath != "" {
		l, err := loadLcov(lcovPath, lcovRoot)
		if err != nil {
			return nil, err
		}
		reports[LangWeb] = l
	}
	return reports, nil
}

func loadGoProfile(file, goMod string) (GoProfile, error) {
	module, err := modulePath(goMod)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := ParseGoProfile(f, module)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

func loadLcov(file, root string) (Lcov, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	l, err := ParseLcov(f, root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return l, nil
}

// gitDiff returns the zero-context diff of HEAD against its merge base with
// base. Renames are detected so a moved file only counts its edited lines.
func gitDiff(base string) ([]byte, error) {
	cmd := exec.Command("git", "-c", "core.quotePath=false", "diff",
		"--unified=0", "--no-color", "--no-ext-diff", "-M", base+"...HEAD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s...HEAD: %v: %s (is the base fetched? CI needs fetch-depth: 0)",
			base, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return out, nil
}
```

- [ ] **Step 24: Run the whole package, race-enabled, plus vet and gofmt**

Run: `go test -race -cover ./tools/diffcov/ && go vet ./tools/diffcov/ && gofmt -l tools/`
Expected: `ok  winterflow/tools/diffcov … coverage: ~93% of statements`; vet prints nothing; gofmt prints nothing.

- [ ] **Step 25: Smoke-test against the real repo**

Run:
```bash
go test -covermode=atomic -coverprofile=/tmp/pr1.out ./... >/dev/null
go run ./tools/diffcov --base HEAD~4 --go /tmp/pr1.out; echo "exit=$?"
```
Expected: a report of the form `  go   N/M changed lines covered (P%)  PASS|FAIL`, then a list of `file:line` entries under "uncovered changed go lines" and `exit=0` or `exit=1`. It must not be `exit=2`. Then run `go run ./tools/diffcov --base does-not-exist --go /tmp/pr1.out; echo "exit=$?"`. Expected: a `git diff does-not-exist...HEAD: … (is the base fetched? …)` message and `exit=2`.

- [ ] **Step 26: Confirm stdlib-only and commit**

Run: `go mod tidy && git diff --exit-code go.mod go.sum`
Expected: exit 0 (no module changes).

```bash
git add tools/diffcov
git commit -m "$(printf 'feat(tools): add diffcov, a diff-coverage gate for Go and web\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 2: `diffcov ratchet` — total coverage vs `coverage-baseline.json`

**Files:**
- Create: `tools/diffcov/ratchet.go`
- Modify: `tools/diffcov/main.go` (package doc + `run` dispatch)
- Test: `tools/diffcov/ratchet_test.go`, `tools/diffcov/main_test.go` (append)

**Interfaces:**
- Consumes (Task 1): `GoProfile.Total()`, `Lcov.Total()`, `loadGoProfile(file, goMod string)`, `loadLcov(file, root string)`, `LangGo`, `LangWeb`, `run(args, stdout, stderr) int`; test helpers `write(t, dir, name, content)` from `main_test.go`.
- Produces:
  - `func LoadBaseline(r io.Reader) (map[string]float64, error)`. Both `"go"` and `"web"` keys are required.
  - `func Percent(covered, total int) float64`. Returns 0 when `total == 0`.
  - `func CheckRatchet(total, baseline float64) (ok, raise bool)`. Compares values rounded to tenths: `ok` = total ≥ baseline − 0.1, `raise` = total ≥ baseline + 1.0.
  - `func RunRatchet(w io.Writer, totals, baseline map[string]float64) bool`
  - CLI: `go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info [--baseline coverage-baseline.json] [--lcov-root web] [--gomod go.mod]`. Exit codes 0/1/2 as in Task 1. Output lines are `  go    59.4% (baseline 59.4)  PASS`, plus `::error::…` on regression or `::notice::raise coverage-baseline.json <lang> to <value>` when a bump is due.
- The Go total equals `go tool cover -func … | tail -1` (statement-weighted). The web total is DA lines with hits > 0 divided by all DA lines.

- [ ] **Step 1: Write the failing ratchet tests**

Create `tools/diffcov/ratchet_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoadBaseline(t *testing.T) {
	got, err := LoadBaseline(strings.NewReader(`{ "go": 59.4, "web": 0.0 }`))
	if err != nil {
		t.Fatal(err)
	}
	if got[LangGo] != 59.4 || got[LangWeb] != 0 {
		t.Fatalf("got %v", got)
	}
	for name, in := range map[string]string{
		"missing web": `{"go": 59.4}`,
		"null go":     `{"go": null, "web": 1}`,
		"typo":        `{"golang": 59.4, "web": 1}`,
		"not json":    `go=59.4`,
	} {
		if _, err := LoadBaseline(strings.NewReader(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestCheckRatchet(t *testing.T) {
	cases := []struct {
		total, base float64
		ok, raise   bool
	}{
		{59.4, 59.4, true, false},
		{59.3, 59.4, true, false},  // 0.1 below: tolerated
		{59.25, 59.4, true, false}, // rounds to 59.3
		{59.2, 59.4, false, false}, // more than 0.1 below
		{60.3, 59.4, true, false},  // +0.9: no bump yet
		{60.4, 59.4, true, true},   // +1.0: bump
		{5.43, 0.0, true, true},
		{0, 0, true, false},
	}
	for _, c := range cases {
		ok, raise := CheckRatchet(c.total, c.base)
		if ok != c.ok || raise != c.raise {
			t.Errorf("CheckRatchet(%v, %v) = (%v,%v), want (%v,%v)", c.total, c.base, ok, raise, c.ok, c.raise)
		}
	}
}

func TestPercent(t *testing.T) {
	if Percent(0, 0) != 0 || Percent(1, 4) != 25 {
		t.Fatal("Percent wrong")
	}
}

func TestRunRatchet(t *testing.T) {
	var buf bytes.Buffer
	ok := RunRatchet(&buf,
		map[string]float64{LangGo: 58.0, LangWeb: 6.04},
		map[string]float64{LangGo: 59.4, LangWeb: 0.0})
	if ok {
		t.Fatal("want failure: go regressed")
	}
	out := buf.String()
	for _, want := range []string{
		"go    58.0% (baseline 59.4)  FAIL",
		"web    6.0% (baseline 0.0)  PASS",
		"::error::go coverage 58.0% is below the baseline 59.4%",
		"::notice::raise coverage-baseline.json web to 6.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
```

Append to `tools/diffcov/main_test.go`:

```go

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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./tools/diffcov/ -run 'Baseline|Ratchet|Percent'`
Expected: FAIL, `undefined: LoadBaseline` / `undefined: CheckRatchet` / `undefined: RunRatchet` / `undefined: Percent`.

- [ ] **Step 3: Implement the ratchet**

Create `tools/diffcov/ratchet.go`:

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
)

// Ratchet tolerances, in tenths of a percentage point: a total may sit at
// most 0.1 below its baseline, and a gain of 1.0 or more asks for a bump.
const (
	ratchetSlack  = 1
	ratchetNotice = 10
)

// LoadBaseline reads coverage-baseline.json ({"go": 59.4, "web": 0.0}).
// Both keys are required so a typo can't silently disable a check.
func LoadBaseline(r io.Reader) (map[string]float64, error) {
	var raw map[string]*float64
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse baseline: %w", err)
	}
	out := map[string]float64{}
	for _, lang := range []string{LangGo, LangWeb} {
		v, ok := raw[lang]
		if !ok || v == nil {
			return nil, fmt.Errorf("baseline has no %q value", lang)
		}
		out[lang] = *v
	}
	return out, nil
}

// Percent returns covered/total in percent, 0 for an empty total.
func Percent(covered, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(covered) * 100 / float64(total)
}

// tenths rounds a percentage to tenths of a point, the precision
// `go tool cover` prints and the baseline file stores.
func tenths(x float64) int { return int(math.Round(x * 10)) }

// CheckRatchet compares one language's total with its baseline. It fails
// when the total is more than 0.1 below the baseline, and asks for a
// baseline bump (raise=true) when it is at least 1.0 above.
func CheckRatchet(total, baseline float64) (ok, raise bool) {
	t, b := tenths(total), tenths(baseline)
	return t >= b-ratchetSlack, t >= b+ratchetNotice
}

// RunRatchet prints each language's total against its baseline, with
// GitHub annotations for failures and suggested bumps, and returns true
// when no total regressed.
func RunRatchet(w io.Writer, totals, baseline map[string]float64) bool {
	pass := true
	fmt.Fprintln(w, "coverage totals")
	for _, lang := range []string{LangGo, LangWeb} {
		total, base := totals[lang], baseline[lang]
		ok, raise := CheckRatchet(total, base)
		status := "PASS"
		if !ok {
			status, pass = "FAIL", false
		}
		fmt.Fprintf(w, "  %-3s  %5.1f%% (baseline %.1f)  %s\n", lang, total, base, status)
		switch {
		case !ok:
			fmt.Fprintf(w, "::error::%s coverage %.1f%% is below the baseline %.1f%% in coverage-baseline.json; add tests\n", lang, total, base)
		case raise:
			fmt.Fprintf(w, "::notice::raise coverage-baseline.json %s to %.1f\n", lang, float64(tenths(total))/10)
		}
	}
	return pass
}

func runRatchet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diffcov ratchet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	goProfile := fs.String("go", "", "Go coverprofile (required)")
	lcovPath := fs.String("lcov", "", "web lcov tracefile (required)")
	lcovRoot := fs.String("lcov-root", "web", "directory lcov SF paths are relative to")
	goMod := fs.String("gomod", "go.mod", "go.mod to read the module path from")
	baselinePath := fs.String("baseline", "coverage-baseline.json", "baseline file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *goProfile == "" || *lcovPath == "" {
		fmt.Fprintln(stderr, "diffcov ratchet: --go and --lcov are required")
		return 2
	}
	gp, err := loadGoProfile(*goProfile, *goMod)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov ratchet:", err)
		return 2
	}
	lc, err := loadLcov(*lcovPath, *lcovRoot)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov ratchet:", err)
		return 2
	}
	f, err := os.Open(*baselinePath)
	if err != nil {
		fmt.Fprintln(stderr, "diffcov ratchet:", err)
		return 2
	}
	defer f.Close()
	baseline, err := LoadBaseline(f)
	if err != nil {
		fmt.Fprintf(stderr, "diffcov ratchet: %s: %v\n", *baselinePath, err)
		return 2
	}
	totals := map[string]float64{
		LangGo:  Percent(gp.Total()),
		LangWeb: Percent(lc.Total()),
	}
	if !RunRatchet(stdout, totals, baseline) {
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Wire the subcommand into `main.go`**

In `tools/diffcov/main.go`, replace the package doc comment:

```go
// Command diffcov is CI's diff-coverage gate:
//
//	diffcov --base <ref> --go coverage.out --lcov web/coverage/lcov.info [--min 80] [--github]
//
// checks that the lines added since <ref> (git diff <ref>...HEAD) are at
// least --min percent covered, per language. Stdlib only. Exit status:
// 0 pass, 1 coverage below the bar, 2 usage or input error.
```

with:

```go
// Command diffcov is CI's coverage gate. It has two modes:
//
//	diffcov --base <ref> --go coverage.out --lcov web/coverage/lcov.info [--min 80] [--github]
//
// checks that the lines added since <ref> (git diff <ref>...HEAD) are at
// least --min percent covered, per language, and
//
//	diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info [--baseline coverage-baseline.json]
//
// checks the total Go and web coverage against the committed baseline.
// Stdlib only. Exit status: 0 pass, 1 coverage below the bar, 2 usage or
// input error.
```

and replace:

```go
func run(args []string, stdout, stderr io.Writer) int {
	return runGate(args, stdout, stderr)
}
```

with:

```go
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "ratchet" {
		return runRatchet(args[1:], stdout, stderr)
	}
	return runGate(args, stdout, stderr)
}
```

- [ ] **Step 5: Run the package to verify it passes**

Run: `go test -race -cover ./tools/diffcov/ && go vet ./tools/diffcov/ && gofmt -l tools/`
Expected: `ok … coverage: ~94% of statements`; vet and gofmt print nothing.

- [ ] **Step 6: Cross-check the Go total against `go tool cover`**

Run:
```bash
go test -covermode=atomic -coverprofile=/tmp/pr1.out ./... >/dev/null
go tool cover -func=/tmp/pr1.out | tail -1
printf 'SF:src/x.ts\nDA:1,1\nend_of_record\n' > /tmp/pr1.lcov
echo '{"go": 0, "web": 0}' > /tmp/pr1-base.json
go run ./tools/diffcov ratchet --go /tmp/pr1.out --lcov /tmp/pr1.lcov --baseline /tmp/pr1-base.json
```
Expected: the `go` percentage printed by the ratchet equals the `total:` figure from `go tool cover` to one decimal (≈61.4% now that `tools/diffcov` is in the tree). The ratchet also prints `::notice::raise coverage-baseline.json go to <same value>` and exits 0.

- [ ] **Step 7: Commit**

```bash
git add tools/diffcov
git commit -m "$(printf 'feat(tools): add diffcov ratchet for total coverage baselines\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 3: Web test runner (Vitest) and the first tests

**Files:**
- Modify: `web/package.json`, `web/pnpm-lock.yaml` (via pnpm), `web/vite.config.ts`, `web/eslint.config.js:9`, `web/.gitignore`, `web/src/pages/settings-volumes.tsx`, `AGENTS.md:54`
- Create: `web/src/test/setup.ts`, `web/src/lib/volumes.ts`
- Test: `web/src/pages/settings-volumes.test.tsx`, `web/src/lib/volumes.test.ts`

**Interfaces:**
- Consumes: existing `VolumesCard({ docker }: { docker: Docker })` from `web/src/pages/settings-volumes.tsx`; `Docker`/`Volume` types from `web/src/context/use-docker.ts`; `ProfileContext`/`ProfileContextValue` from `web/src/context/profile-context-base.ts`; `AppsContext`/`AppsContextValue` from `web/src/context/apps-context-base.ts`.
- Produces:
  - `web/src/lib/volumes.ts`: `volumeNameError(name: string): string`, `shortVolumeName(v: Volume): string`, `type VolumeGroup = { label: string; volumes: Volume[] }`, `groupVolumes(volumes: Volume[]): VolumeGroup[]`
  - npm scripts `test` (`vitest run`) and `test:coverage` (`vitest run --coverage`). Coverage writes `web/coverage/lcov.info` with `SF:` paths relative to `web/` (e.g. `SF:src/lib/volumes.ts`). Task 4's CI and Makefile, and diffcov's `--lcov-root web` default, rely on this.

Notes. `VolumesCard` takes `docker` as a prop, so the test passes a fake `Docker`; it does not mock the `useDocker` hook. `useProfile`/`useApps` read React contexts, so the test wraps the card in the real `ProfileContext.Provider` / `AppsContext.Provider` plus a `MemoryRouter` (for `<Link>`). It does not use `vi.mock`. The helpers move to `web/src/lib/volumes.ts` because exporting non-components from a component file trips `react-refresh/only-export-components`, and `web/src/lib/` is where the repo keeps pure helpers.

- [ ] **Step 1: Add the dev dependencies with pnpm 10**

Run (from the repo root):
```bash
npx -y pnpm@10 --dir web add -D vitest@5.0.3 @vitest/coverage-v8@5.0.3 @testing-library/react@16.3.3 @testing-library/dom@10.4.2 @testing-library/user-event@14.6.7 @testing-library/jest-dom@7.0.1 jsdom@30.1.2
```
Expected: `Done … using pnpm v10.x`. `git diff web/package.json` shows only the seven additions under `devDependencies` (exact versions, no caret). `git diff --stat web/pnpm-lock.yaml` shows insertions only (about 900 lines); no existing package's version changes.

- [ ] **Step 2: Add the scripts**

In `web/package.json`, replace:

```json
    "lint": "eslint .",
    "preview": "vite preview"
```

with:

```json
    "lint": "eslint .",
    "preview": "vite preview",
    "test": "vitest run",
    "test:coverage": "vitest run --coverage"
```

- [ ] **Step 3: Configure Vitest in `web/vite.config.ts`**

Replace the whole file with:

```ts
/// <reference types="vitest/config" />
import { fileURLToPath, URL } from "node:url";

import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // Bind on all interfaces so other machines (e.g. over Tailscale) can reach
    // the dev server, not just localhost on this host.
    host: true,
    // Vite rejects requests whose Host header isn't allow-listed; permit access
    // via the Tailscale hostname.
    allowedHosts: true,
  },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  // Vitest (pnpm test / test:coverage). Coverage feeds CI's diff gate and
  // ratchet through coverage/lcov.info; vendored shadcn/ui is excluded.
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    coverage: {
      provider: "v8",
      reporter: ["text", "lcov"],
      reportsDirectory: "./coverage",
      include: ["src/**"],
      exclude: ["src/components/ui/**", "src/main.tsx", "**/*.d.ts"],
    },
  },
});
```

Create `web/src/test/setup.ts`:

```ts
// Vitest setup: jest-dom matchers (toBeInTheDocument, …) and a DOM reset
// between tests.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
});
```

- [ ] **Step 4: Keep coverage output out of lint and git**

In `web/eslint.config.js`, replace `globalIgnores(['dist']),` with `globalIgnores(['dist', 'coverage']),`. Without this, `eslint .` would lint the generated `coverage/lcov-report/*.js`.

In `web/.gitignore`, after the `dist-ssr` line add:

```
# vitest --coverage output (lcov.info feeds CI's coverage gate)
coverage
```

- [ ] **Step 5: Write the `VolumesCard` tests against the current code**

These tests pin today's behaviour before the refactor, so they pass on the first run.

Create `web/src/pages/settings-volumes.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import type { Docker, Volume } from "@/context/use-docker";
import {
  ProfileContext,
  type ProfileContextValue,
} from "@/context/profile-context-base";
import { VolumesCard } from "@/pages/settings-volumes";

const HASH = "fedcba9876543210".repeat(4);

const VOLUMES: Volume[] = [
  { name: "wf-a1_data", app_id: "a1", project: "wf-a1", in_use: true, orphan: false, anonymous: false },
  { name: "wf-gone_db", app_id: "gone", project: "wf-gone", in_use: false, orphan: true, anonymous: false },
  { name: "shared", in_use: false, orphan: false, anonymous: false },
  { name: HASH, in_use: false, orphan: false, anonymous: true },
];

// fakeDocker stubs only what VolumesCard calls; the rest of Docker is unused.
function fakeDocker(over: Partial<Docker> = {}): Docker {
  return {
    listVolumes: vi.fn().mockResolvedValue(VOLUMES),
    createVolume: vi.fn().mockResolvedValue(undefined),
    deleteVolume: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Docker;
}

function renderCard(docker: Docker, { isAdmin = true } = {}) {
  const profile = { isAdmin } as ProfileContextValue;
  const apps = {
    apps: [{ id: "a1", name: "Nextcloud" }],
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <ProfileContext.Provider value={profile}>
        <AppsContext.Provider value={apps}>
          <VolumesCard docker={docker} />
        </AppsContext.Provider>
      </ProfileContext.Provider>
    </MemoryRouter>,
  );
}

describe("VolumesCard", () => {
  it("lists named volumes grouped, with owners and the orphan count", async () => {
    renderCard(fakeDocker());

    expect(await screen.findByText("Winterflow apps")).toBeInTheDocument();
    expect(screen.getByText("Other volumes")).toBeInTheDocument();
    expect(screen.queryByText("Anonymous")).not.toBeInTheDocument();

    // Short names, full name kept in the title.
    expect(screen.getByTitle("wf-a1_data")).toHaveTextContent("data");
    expect(screen.getByTitle("shared")).toHaveTextContent("shared");
    // The owning app links to its page (once per layout: desktop + phone).
    expect(screen.getAllByRole("link", { name: "Nextcloud" })[0]).toHaveAttribute(
      "href",
      "/app/a1",
    );
    expect(screen.getByText(/3 volumes/)).toBeInTheDocument();
    expect(screen.getByText(/1 orphaned/)).toBeInTheDocument();
  });

  it("shows anonymous volumes only when switched on", async () => {
    const user = userEvent.setup();
    renderCard(fakeDocker());

    await user.click(await screen.findByRole("switch"));

    expect(screen.getByText("Anonymous")).toBeInTheDocument();
    expect(screen.getByTitle(HASH)).toHaveTextContent(`${HASH.slice(0, 12)}…`);
    expect(screen.getByText(/4 volumes/)).toBeInTheDocument();
  });

  it("deletes an unused volume after confirmation and reloads", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker();
    renderCard(docker);

    // A mounted volume offers no delete button.
    await screen.findByText("Winterflow apps");
    expect(
      screen.queryByRole("button", { name: "Delete volume wf-a1_data" }),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Delete volume shared" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Delete volume" }));

    expect(docker.deleteVolume).toHaveBeenCalledWith("shared");
    expect(docker.listVolumes).toHaveBeenCalledTimes(2);
  });

  it("hides create and delete from non-admins", async () => {
    renderCard(fakeDocker(), { isAdmin: false });

    await screen.findByText("Winterflow apps");
    expect(
      screen.queryByRole("button", { name: /Create volume/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /^Delete volume/ }),
    ).not.toBeInTheDocument();
  });

  it("validates the name before creating", async () => {
    const user = userEvent.setup();
    const docker = fakeDocker();
    renderCard(docker);

    await user.click(await screen.findByRole("button", { name: /Create volume/ }));
    const dialog = await screen.findByRole("dialog");
    const create = within(dialog).getByRole("button", { name: "Create" });
    expect(create).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Name"), "-bad");
    expect(within(dialog).getByText(/Start with a letter or digit/)).toBeInTheDocument();
    expect(create).toBeDisabled();

    await user.clear(within(dialog).getByLabelText("Name"));
    await user.type(within(dialog).getByLabelText("Name"), "media");
    await user.click(create);

    expect(docker.createVolume).toHaveBeenCalledWith("media");
  });

  it("says so when the list can't be loaded", async () => {
    renderCard(
      fakeDocker({ listVolumes: vi.fn().mockRejectedValue(new Error("agent offline")) }),
    );

    expect(await screen.findByText("Couldn't load volumes.")).toBeInTheDocument();
  });
});
```

- [ ] **Step 6: Run them; they must pass before any refactor**

Run: `pnpm --dir web test`
Expected: `Test Files  1 passed (1)`, `Tests  6 passed (6)`. If Radix dialogs complain about missing browser APIs, the setup is wrong: jsdom 30 provides what these components need. Fix the setup; do not change the tests.

- [ ] **Step 7: Write the failing helper tests**

Create `web/src/lib/volumes.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import type { Volume } from "@/context/use-docker";
import { groupVolumes, shortVolumeName, volumeNameError } from "@/lib/volumes";

const HASH = "0123456789abcdef".repeat(4); // 64 hex chars

function vol(over: Partial<Volume> & { name: string }): Volume {
  return { in_use: false, orphan: false, anonymous: false, ...over };
}

describe("volumeNameError", () => {
  it("accepts docker-style names", () => {
    expect(volumeNameError("data")).toBe("");
    expect(volumeNameError("my_vol-1.2")).toBe("");
    expect(volumeNameError("9lives")).toBe("");
  });

  it("rejects a leading symbol or a forbidden character", () => {
    expect(volumeNameError("-data")).toMatch(/Start with a letter or digit/);
    expect(volumeNameError("my vol")).toMatch(/Start with a letter or digit/);
    expect(volumeNameError("a/b")).toMatch(/Start with a letter or digit/);
  });

  it("caps the length at 255", () => {
    expect(volumeNameError("a".repeat(255))).toBe("");
    expect(volumeNameError("a".repeat(256))).toBe("Use at most 255 characters.");
  });
});

describe("shortVolumeName", () => {
  it("drops the compose project prefix of an app volume", () => {
    expect(
      shortVolumeName(vol({ name: "wf-abc_data", app_id: "abc", project: "wf-abc" })),
    ).toBe("data");
  });

  it("keeps the full name when the prefix doesn't match", () => {
    expect(
      shortVolumeName(vol({ name: "other_data", app_id: "abc", project: "wf-abc" })),
    ).toBe("other_data");
  });

  it("keeps a project prefix when the volume has no app", () => {
    expect(shortVolumeName(vol({ name: "proj_data", project: "proj" }))).toBe(
      "proj_data",
    );
  });

  it("shortens an anonymous hash to docker's 12-char id", () => {
    expect(shortVolumeName(vol({ name: HASH, anonymous: true }))).toBe(
      `${HASH.slice(0, 12)}…`,
    );
  });

  it("leaves a named anonymous volume alone", () => {
    expect(shortVolumeName(vol({ name: "scratch", anonymous: true }))).toBe(
      "scratch",
    );
  });
});

describe("groupVolumes", () => {
  it("splits app, other and anonymous volumes in that order", () => {
    const a = vol({ name: "wf-1_db", app_id: "1", project: "wf-1" });
    const o = vol({ name: "plain" });
    const n = vol({ name: HASH, anonymous: true });
    const b = vol({ name: "wf-2_db", app_id: "2", project: "wf-2" });
    expect(groupVolumes([n, a, o, b])).toEqual([
      { label: "Winterflow apps", volumes: [a, b] },
      { label: "Other volumes", volumes: [o] },
      { label: "Anonymous", volumes: [n] },
    ]);
  });

  it("drops empty groups", () => {
    const o = vol({ name: "plain" });
    expect(groupVolumes([o])).toEqual([{ label: "Other volumes", volumes: [o] }]);
    expect(groupVolumes([])).toEqual([]);
  });

  it("files an anonymous volume that carries an app label under the app", () => {
    const v = vol({ name: HASH, anonymous: true, app_id: "1" });
    expect(groupVolumes([v])).toEqual([{ label: "Winterflow apps", volumes: [v] }]);
  });
});
```

- [ ] **Step 8: Run them to verify they fail**

Run: `pnpm --dir web test`
Expected: FAIL. `src/lib/volumes.test.ts` cannot resolve `@/lib/volumes` (`Failed to resolve import "@/lib/volumes"` / `Cannot find module`). The VolumesCard file still passes.

- [ ] **Step 9: Move the helpers to `web/src/lib/volumes.ts`**

Create `web/src/lib/volumes.ts`:

```ts
import type { Volume } from "@/context/use-docker";

const VOLUME_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;

// volumeNameError mirrors the agent's ValidateVolumeName so the dialog can say
// what's wrong before a round-trip; "" means valid.
export function volumeNameError(name: string): string {
  if (name.length > 255) return "Use at most 255 characters.";
  if (!VOLUME_NAME_RE.test(name)) {
    return "Start with a letter or digit; use only letters, digits, dots, dashes and underscores.";
  }
  return "";
}

// shortVolumeName drops the compose project prefix ("wf-<id>_data" → "data"):
// the App column already names the owner, so the key is what tells rows
// apart. Callers keep the full name in a title and in confirmations.
export function shortVolumeName(v: Volume): string {
  // Anonymous volumes are 64-hex hashes; 12 chars is Docker's own short id.
  if (v.anonymous && /^[0-9a-f]{64}$/.test(v.name)) {
    return `${v.name.slice(0, 12)}…`;
  }
  const prefix = v.app_id && v.project ? `${v.project}_` : "";
  return prefix && v.name.startsWith(prefix)
    ? v.name.slice(prefix.length)
    : v.name;
}

export type VolumeGroup = { label: string; volumes: Volume[] };

// groupVolumes splits the (backend name-sorted) list so Winterflow's volumes
// don't drown in whatever else runs on the host. Order within a group is
// preserved; empty groups are dropped.
export function groupVolumes(volumes: Volume[]): VolumeGroup[] {
  const groups: VolumeGroup[] = [
    { label: "Winterflow apps", volumes: volumes.filter((v) => v.app_id) },
    {
      label: "Other volumes",
      volumes: volumes.filter((v) => !v.app_id && !v.anonymous),
    },
    {
      label: "Anonymous",
      volumes: volumes.filter((v) => !v.app_id && v.anonymous),
    },
  ];
  return groups.filter((g) => g.volumes.length > 0);
}
```

In `web/src/pages/settings-volumes.tsx`, make three edits.

(a) Replace the block from `const VOLUME_NAME_RE` through the closing `}` of `shortVolumeName`, which sits right after `import { cn } from "@/lib/utils";`:

```tsx
import { cn } from "@/lib/utils";

const VOLUME_NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;

// volumeNameError mirrors the agent's ValidateVolumeName so the dialog can say
// what's wrong before a round-trip; "" means valid.
function volumeNameError(name: string): string {
  if (name.length > 255) return "Use at most 255 characters.";
  if (!VOLUME_NAME_RE.test(name)) {
    return "Start with a letter or digit; use only letters, digits, dots, dashes and underscores.";
  }
  return "";
}

// shortVolumeName drops the compose project prefix ("wf-<id>_data" → "data"):
// the App column already names the owner, so the key is what tells rows
// apart. Callers keep the full name in a title and in confirmations.
function shortVolumeName(v: Volume): string {
  // Anonymous volumes are 64-hex hashes; 12 chars is Docker's own short id.
  if (v.anonymous && /^[0-9a-f]{64}$/.test(v.name)) {
    return `${v.name.slice(0, 12)}…`;
  }
  const prefix = v.app_id && v.project ? `${v.project}_` : "";
  return prefix && v.name.startsWith(prefix)
    ? v.name.slice(prefix.length)
    : v.name;
}
```

with:

```tsx
import { cn } from "@/lib/utils";
import {
  groupVolumes,
  shortVolumeName,
  volumeNameError,
} from "@/lib/volumes";
```

(b) Replace:

```tsx
  // The backend sorts by name; group here so Winterflow's volumes don't
  // drown in whatever else runs on the host.
  const groups = (
    [
      ["Winterflow apps", visible.filter((v) => v.app_id)],
      ["Other volumes", visible.filter((v) => !v.app_id && !v.anonymous)],
      ["Anonymous", visible.filter((v) => !v.app_id && v.anonymous)],
    ] as const
  ).filter(([, list]) => list.length > 0);
```

with:

```tsx
  const groups = groupVolumes(visible);
```

(c) Replace:

```tsx
                  {groups.map(([label, list]) => (
                    <Fragment key={label}>
                      {groups.length > 1 && groupRow(label)}
                      {rows(list)}
                    </Fragment>
                  ))}
```

with:

```tsx
                  {groups.map(({ label, volumes }) => (
                    <Fragment key={label}>
                      {groups.length > 1 && groupRow(label)}
                      {rows(volumes)}
                    </Fragment>
                  ))}
```

The `Volume` type import in the page stays; `VolumeStatus`/`VolumeOwner`/`DeleteVolumeDialog` still use it.

- [ ] **Step 10: Run all web tests, then coverage**

Run: `pnpm --dir web test`
Expected: `Test Files  2 passed (2)`, `Tests  17 passed (17)`.

Run: `pnpm --dir web run test:coverage && head -2 web/coverage/lcov.info && grep -c '^SF:src/components/ui/' web/coverage/lcov.info`
Expected: the text table ends with an `All files` row of about 5.4% lines, and `settings-volumes.tsx` shows ≥90% lines. The file starts `TN:` / `SF:src/…` (relative paths). The `components/ui` count is `0`.

- [ ] **Step 11: Lint, type-check and build; restore the dist placeholder**

Run:
```bash
pnpm --dir web run lint
pnpm --dir web run build
git checkout -- web/dist/.gitkeep
git status --short web/dist
```
Expected: lint has no errors (the `baseline-browser-mapping` staleness notice is pre-existing and harmless). `tsc -b` type-checks the test files too, and the build ends `✓ built in …`. `git status --short web/dist` prints nothing.

- [ ] **Step 12: Verify the lockfile is what CI's frozen install accepts**

Run: `npx -y pnpm@10 --dir web install --frozen-lockfile`
Expected: `Done …` with no `ERR_PNPM_OUTDATED_LOCKFILE`.

- [ ] **Step 13: Document the frontend tests in `AGENTS.md`**

In `AGENTS.md` (Testing Guidelines), replace the line:

```
The frontend currently relies on type safety plus ESLint; if you introduce Vitest or Playwright, add scripts to `web/package.json` and document fixtures in this guide. 
```

with:

```
Frontend unit tests use Vitest + Testing Library on jsdom (`pnpm --dir web test`; `test:coverage` also writes `web/coverage/lcov.info`). Put `*.test.ts(x)` beside the code, keep pure helpers in `web/src/lib/` and test them directly, and render components inside their real context providers (`ProfileContext`, `AppsContext`, `MemoryRouter`) with fake objects for agent-bound APIs — see `web/src/pages/settings-volumes.test.tsx`. 
```

- [ ] **Step 14: Commit**

```bash
git add web/package.json web/pnpm-lock.yaml web/vite.config.ts web/eslint.config.js web/.gitignore web/src/test/setup.ts web/src/lib/volumes.ts web/src/lib/volumes.test.ts web/src/pages/settings-volumes.tsx web/src/pages/settings-volumes.test.tsx AGENTS.md
git status --short   # must show nothing else staged; web/dist/.gitkeep untouched
git commit -m "$(printf 'test(web): add Vitest with coverage and the first volume tests\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 4: gofmt fix, Makefile targets, baseline and the `ci` workflow

**Files:**
- Modify: `internal/app/web/handler/user/handler.go:86` (gofmt), `Makefile`, `.gitignore`, `AGENTS.md:37-43` and the Testing Guidelines
- Create: `coverage-baseline.json`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: Task 1/2 CLI (`go run ./tools/diffcov --base … --go … --lcov … --min 80 --github`; `go run ./tools/diffcov ratchet --go … --lcov … --baseline coverage-baseline.json`); Task 3 scripts (`pnpm --dir web run test:coverage` → `web/coverage/lcov.info`).
- Produces: workflow `name: ci` (Task 5's `workflow_run` matches on this exact name). Jobs `go`, `web`, `installer`, `coverage`. Artifacts `go-coverage` (`coverage.out`) and `web-coverage` (`lcov.info`). Make targets `test`, `web-test`, `cover`, `diffcov`.

- [ ] **Step 1: Prove the gofmt check fails today**

Run: `gofmt -l $(git ls-files '*.go')`
Expected: prints exactly `internal/app/web/handler/user/handler.go`.

- [ ] **Step 2: Fix it**

Run: `gofmt -w internal/app/web/handler/user/handler.go && gofmt -l $(git ls-files '*.go') && git diff --stat internal/app/web/handler/user/handler.go`
Expected: the `gofmt -l` prints nothing; the diff is `1 deletion(-)` (the blank line before `// ChangePassword verifies…`).

- [ ] **Step 3: Add the Makefile targets**

In `Makefile`, replace:

```make
.PHONY: build lint standalone api web web-build third-party-licenses
```

with:

```make
.PHONY: build lint standalone api web web-build third-party-licenses test web-test cover diffcov
```

and after the `lint:` recipe (after the line `	pnpm --dir web run lint`) insert:

```make

# Go tests exactly as CI runs them; coverage.out feeds `make cover` and
# `make diffcov`.
test:
	$(GO) test -race -covermode=atomic -coverprofile=coverage.out ./...

# Web unit tests (Vitest) with coverage into web/coverage/lcov.info.
web-test:
	pnpm --dir web run test:coverage

# Total Go and web coverage against coverage-baseline.json (CI's ratchet).
cover: test web-test
	$(GO) run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info

# CI's diff-coverage gate: lines changed since origin/main must be at least
# 80% covered, per language. `git fetch origin main` first if it is stale.
diffcov: test web-test
	$(GO) run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info
```

(Recipe lines start with a TAB.)

In `.gitignore`, after the `*.sqlite*` line add:

```
# coverage reports (make test)
/coverage.out
```

- [ ] **Step 4: Measure the baseline with CI's Go version**

CI installs the Go version from `go.mod` (1.25.3); coverage instrumentation can differ slightly between Go releases. Run:

```bash
echo '{ "go": 0.0, "web": 0.0 }' > coverage-baseline.json
GOTOOLCHAIN=go1.25.3 make cover
```
Expected: both suites pass. The ratchet prints `go    ~61.4% (baseline 0.0)  PASS` and `web    ~5.4% (baseline 0.0)  PASS`, followed by two lines of the form `::notice::raise coverage-baseline.json <lang> to <value>`. Run `GOTOOLCHAIN=go1.25.3 make test && go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info` once more. The `go` value must be identical; if it moves by more than 0.1, a timing-dependent test path makes the ratchet flaky, so stop and report it instead of committing.

Write the two printed values into `coverage-baseline.json` in this exact shape. The numbers below are examples; use yours:

```json
{ "go": 61.4, "web": 5.4 }
```

Then run `make cover`. Expected: both `PASS`, no `::notice::` and no `::error::` lines, exit 0.

- [ ] **Step 5: Run the diff gate locally**

Run: `git fetch origin main && make diffcov; echo "exit=$?"`
Expected: `exit=0`. `go` prints `no executable changed lines  PASS`: `tools/**` and `*_test.go` are excluded, and the only other Go change is the deleted blank line in `handler.go`. `web` prints about `15/15 changed lines covered (100.0%)  PASS` for `settings-volumes.tsx` and `lib/volumes.ts`. `web/src/test/setup.ts` is listed under "no coverage data", which is expected because Vitest never covers setup files. If `exit=1`, add tests for the listed lines; do not lower `--min`.

- [ ] **Step 6: Create `.github/workflows/ci.yml`**

```yaml
# CI for every pull request and every push to main: gofmt, vet, race-enabled
# Go tests, web lint/tests/build, the installer's dry-run suite, and the
# coverage gates (tools/diffcov). release.yml waits for this workflow's
# success on main before it publishes anything.
name: ci

on:
  pull_request:
  push:
    branches:
      - main

permissions:
  contents: read

concurrency:
  # PRs: a newer push to the branch cancels the run in progress. main: one
  # group per commit, so every push finishes and can trigger its release.
  group: ci-${{ github.event_name == 'pull_request' && github.ref || github.sha }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: gofmt
        run: |
          unformatted="$(gofmt -l $(git ls-files '*.go'))"
          if [ -n "$unformatted" ]; then
            echo "::error::gofmt -w these files:"
            echo "$unformatted"
            exit 1
          fi

      - name: go vet
        run: go vet ./...

      - name: go test
        run: go test -race -covermode=atomic -coverprofile=coverage.out ./...

      - uses: actions/upload-artifact@v4
        with:
          name: go-coverage
          path: coverage.out
          if-no-files-found: error

  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: pnpm/action-setup@v4
        with:
          version: 10

      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: pnpm
          cache-dependency-path: web/pnpm-lock.yaml

      - name: Install
        run: pnpm --dir web install --frozen-lockfile

      - name: Lint
        run: pnpm --dir web run lint

      - name: Test
        run: pnpm --dir web run test:coverage

      - name: Build
        run: pnpm --dir web run build

      - uses: actions/upload-artifact@v4
        with:
          name: web-coverage
          path: web/coverage/lcov.info
          if-no-files-found: error

  installer:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: install.sh dry-run suite
        run: bash scripts/install_test.sh

  coverage:
    needs: [go, web]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          # The diff gate runs `git diff <base>...HEAD`, which needs the
          # merge base in the clone.
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - uses: actions/download-artifact@v4
        with:
          name: go-coverage

      - uses: actions/download-artifact@v4
        with:
          name: web-coverage
          path: web/coverage

      - name: Diff coverage (changed lines >= 80%)
        if: github.event_name == 'pull_request'
        env:
          BASE_SHA: ${{ github.event.pull_request.base.sha }}
        run: >-
          go run ./tools/diffcov --base "$BASE_SHA" --min 80 --github
          --go coverage.out --lcov web/coverage/lcov.info

      - name: Coverage ratchet (totals vs coverage-baseline.json)
        if: ${{ !cancelled() }}
        run: >-
          go run ./tools/diffcov ratchet --baseline coverage-baseline.json
          --go coverage.out --lcov web/coverage/lcov.info
```

- [ ] **Step 7: Lint the workflow**

Run: `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= .github/workflows/ci.yml; echo "exit=$?"`
Expected: no findings, `exit=0`. This fetches actionlint through the Go proxy for local use only; CI does not use it.

Run: `grep -n "^name: ci$" .github/workflows/ci.yml`
Expected: one hit on line 5. Task 5's `workflow_run` refers to this name.

- [ ] **Step 8: Document the commands and gates in `AGENTS.md`**

In `AGENTS.md` under "Build, Test, and Development Commands", after the line ``- `pnpm --dir web run lint` – runs the ESLint suite configured for React 19.`` insert:

```
- `make test` – Go tests exactly as CI runs them (`-race`, coverage into `coverage.out`); `make web-test` – Vitest with coverage into `web/coverage/lcov.info`.
- `make cover` – total Go and web coverage against `coverage-baseline.json` (CI's ratchet); `make diffcov` – CI's gate: lines changed since `origin/main` must be ≥80% covered per language.
```

In "Testing Guidelines", after the frontend line added in Task 3, insert:

```
CI (`.github/workflows/ci.yml`, run by every PR) enforces gofmt, `go vet`, `go test -race`, web lint/test/build and `scripts/install_test.sh`, plus two coverage gates from `tools/diffcov`: changed lines must be ≥80% covered per language (tests, `*.pb.go`, `cmd/*/main.go`, `tools/`, `web/src/components/ui/` and `web/src/main.tsx` are exempt), and total coverage may not fall more than 0.1 below `coverage-baseline.json`. When CI prints `::notice::raise coverage-baseline.json …`, bump the file in the same PR. 
```

- [ ] **Step 9: Commit**

```bash
git add internal/app/web/handler/user/handler.go Makefile .gitignore coverage-baseline.json .github/workflows/ci.yml AGENTS.md
git status --short   # coverage.out and web/coverage/ must NOT appear (ignored)
git commit -m "$(printf 'ci: add CI workflow with diff-coverage gate and coverage ratchet\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

---

### Task 5: Gate releases on a green `ci` run

**Files:**
- Modify: `.github/workflows/release.yml` (trigger, job `if:`, checkout `ref:`, drop the `Test` step)

**Interfaces:**
- Consumes: workflow `name: ci` from Task 4.
- Produces: `release.yml` runs on `workflow_run` (`workflows: [ci]`, `types: [completed]`, `branches: [main]`) and on `workflow_dispatch`. It releases only when `workflow_run.conclusion == 'success'` and `workflow_run.event == 'push'`, and it checks out `workflow_run.head_sha`.

`workflow_run` only fires for workflow files on the default branch. After merge, the merge push runs `ci`, and its completion triggers the new `release.yml`. The merge commit itself is no longer released directly by `push`.

- [ ] **Step 1: Record the current trigger (it must change)**

Run: `grep -n -A3 "^on:" .github/workflows/release.yml && grep -n "go test" .github/workflows/release.yml`
Expected: shows `push:` / `branches:` / `- main`, and `run: go test ./...`.

- [ ] **Step 2: Replace `.github/workflows/release.yml`**

Replace the whole file with:

```yaml
# Publishes a GitHub release with prebuilt binaries for every push to main
# that passed CI: it runs when the `ci` workflow (ci.yml) completes
# successfully for a push to main, and releases exactly the commit CI tested.
# Versions are calver: vYYMMDD.{build} (UTC date, build number counting up
# from 1 within the day) — computed and tagged here automatically.
# Asset naming and platforms live in .goreleaser.yaml.
name: release

on:
  workflow_run:
    workflows:
      - ci
    types:
      - completed
    branches:
      - main
  workflow_dispatch:

permissions:
  contents: write

# One release at a time: parallel runs would compute the same next build
# number and race on the tag push.
concurrency:
  group: release
  cancel-in-progress: false

jobs:
  goreleaser:
    # Only a green CI run of a push to main releases. The event check matters:
    # a pull request whose head branch is also named "main" (e.g. from a
    # fork) completes ci with head_branch == main too.
    if: >-
      github.event_name == 'workflow_dispatch' ||
      (github.event.workflow_run.conclusion == 'success' &&
       github.event.workflow_run.event == 'push')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          # The commit CI tested (workflow_run runs on the default branch's
          # latest commit otherwise); a manual dispatch uses the chosen ref.
          ref: ${{ github.event.workflow_run.head_sha || github.sha }}
          # All history and tags: needed to count today's builds and for the
          # changelog between releases.
          fetch-depth: 0

      - uses: pnpm/action-setup@v4
        with:
          version: 10

      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: pnpm
          cache-dependency-path: web/pnpm-lock.yaml

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Tag calver release
        run: |
          git config user.name "github-actions[bot]"
          git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
          today="$(date -u +%y%m%d)"
          last="$(git tag -l "v${today}.*" | sed "s/^v${today}\.//" | sort -n | tail -1)"
          tag="v${today}.$(( ${last:-0} + 1 ))"
          echo "Releasing ${tag}"
          git tag "$tag"
          git push origin "$tag"

      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 3: Verify the gating and lint**

Run:
```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= .github/workflows/release.yml; echo "lint=$?"
grep -c "^  push:" .github/workflows/release.yml
grep -c "go test" .github/workflows/release.yml
grep -n "workflow_run.conclusion == 'success'" .github/workflows/release.yml
grep -n "workflow_run.event == 'push'" .github/workflows/release.yml
grep -n 'ref: ${{ github.event.workflow_run.head_sha || github.sha }}' .github/workflows/release.yml
grep -n -A1 "workflows:" .github/workflows/release.yml
```
Expected: `lint=0`; `0`; `0`; one hit each for the conclusion check, the event check and the `ref:` line; `workflows:` followed by `- ci`.

- [ ] **Step 4: Full local run of everything CI runs**

```bash
gofmt -l $(git ls-files '*.go')            # prints nothing
go vet ./...
make test                                  # go test -race with coverage
npx -y pnpm@10 --dir web install --frozen-lockfile
pnpm --dir web run lint
make web-test
pnpm --dir web run build && git checkout -- web/dist/.gitkeep
bash scripts/install_test.sh               # ALL TESTS PASSED
go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info
go run ./tools/diffcov ratchet --go coverage.out --lcov web/coverage/lcov.info
git status --short                         # clean apart from ignored outputs
```
Expected: every command exits 0, and the two diffcov runs print `PASS` for both languages.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "$(printf 'ci(release): release only commits that passed CI\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>')"
```

- [ ] **Step 6: Note for the PR description (do not push from this plan)**

When the branch is pushed and the PR opened, its body must include a section like this. It flags the trigger change for the user's explicit confirmation before merge:

> **Release trigger change — please confirm before merging.** `release.yml` no longer runs on `push` to `main`. It now runs when the `ci` workflow completes successfully for a push to `main` (`workflow_run`), checks out the exact SHA CI tested, and no longer runs its own `go test`. `workflow_dispatch` still works. Consequence: a push to main whose CI fails is not released.

It must also list the suites run (AGENTS.md asks for that): `go test -race ./...`, `pnpm --dir web test`, `bash scripts/install_test.sh`, and both diffcov modes.

---

## Self-Review

**Spec coverage (PR 1 section):**
- `ci.yml` triggers (`pull_request` any base, `push` to main), concurrency per ref with PR-only cancel → Task 4 Step 6. For main, the group is per SHA so no push run is cancelled (each one gates a release).
- go job: setup-go from go.mod, `gofmt -l` empty, `go vet`, `go test -race -covermode=atomic -coverprofile=coverage.out ./...`, upload artifact → Task 4 Steps 1–2 and 6.
- web job: pnpm 10, node 22, frozen install, lint, `test:coverage`, build, upload lcov → Task 3 + Task 4 Step 6.
- installer job → Task 4 Step 6.
- coverage job (needs go+web, downloads artifacts, diff gate on PRs only, ratchet always; base = `github.event.pull_request.base.sha`, fetch-depth 0) → Task 4 Step 6.
- diffcov inputs, `git diff --unified=0 <base>...HEAD`, Go block rule, lcov DA rule, exclusions, per-language output and uncovered list, exit 1 below `--min`, zero-line language passes, unit tests for diff/coverprofile/lcov/exclusions/threshold → Task 1.
- Ratchet file, Go total = `go tool cover -func` total, web total from lcov, −0.1 fail, +1.0 notice text, no auto-commit, measured values → Task 2 + Task 4 Step 4.
- Web runner deps, scripts, config (jsdom, v8, text+lcov, include/exclude), first tests on the volume helpers and `VolumesCard`, `.gitkeep` kept → Task 3.
- Makefile `test`, `web-test`, `cover`, `diffcov` → Task 4 Step 3.
- Release gating (`workflow_run` on `ci`, completed, main, success condition, head_sha checkout, no `go test`, `workflow_dispatch` kept, PR-body flag) → Task 5.

**Placeholder scan:** every code step contains the full file or an exact old/new replacement. The only value left to the implementer is the measured baseline (Task 4 Step 4), as the spec requires.

**Type consistency:** `ParseDiff`, `ParseGoProfile(r, module)`, `ParseLcov(r, root)`, `Coverage`, `Evaluate(changed, reports)`, `Report(w, results, min, github)`, `run/runGate/runRatchet`, `loadGoProfile(file, goMod)`, `loadLcov(file, root)`, `Percent(covered, total)`, `CheckRatchet(total, baseline) (ok, raise)`, `RunRatchet(w, totals, baseline)` and `LangGo`/`LangWeb` are used with the same names and signatures in Tasks 1 and 2. On the web side, `groupVolumes` returns `{ label, volumes }`, matching the page edit and the test. All Go code in this plan was compiled and its tests run (`go test -race`), both at the Task 1 state and the final state. The web config and tests were run with the pinned versions (17 tests pass; lint, `tsc -b` and build are green; frozen install accepts the lockfile).

**Review Focus:** all five lines have a pinning test or check in their owning task (Tasks 1 and 5).
