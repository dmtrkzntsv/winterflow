# x-winterflow in the Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `x-winterflow` Winterflow's native compose format for every app: one shared parser validates the whole block, save-app refuses what the format refuses, the agent allocates host ports for expose entries and applies the block at every deploy through a generated `.winterflow/compose.winterflow.yml` passed as the last `-f`, ingress hostnames can point at an http expose entry, and the app editor gets a declared-variables form, a Ports panel and an expose picker for hostnames.

**Architecture:** A new domain package `internal/domain/compose/xwinterflow` decodes each compose file once (sharing the YAML trees with `recipe.ParseComposeNodes`, so backups and the rest of the block come from one parse), validates expose/variables/overrides/files/backups, checks values, pins and ingress targets, and renders the override file (`Render`). The agent (`internal/infra/orchestrator/docker_compose`) allocates host ports at every commit (save, rollback, source update) from `EXPOSE_PORT_RANGE` under one server-wide lock with an in-memory index, stores them in the committed `.winterflow/config.json` under `ports`, checks declared values again at deploy (secrets after decryption) and writes the override file next to `storage.env`; the API's `CheckSave` runs the same parser for 400s; `BuildConfig` resolves `expose` targets to `127.0.0.1:<host>`; `app.get` returns the declared variables and the expose entries with their host ports for the web editor.

**Tech Stack:** Go 1.27 (`go.yaml.in/yaml/v3` v3.0.5 node API, go-git, chi), Docker Compose CLI (`!override` needs compose v2.24.4+; verified with v5.5.1), React 19 + Vite + Vitest + Testing Library, vendored shadcn/ui, pnpm.

**Spec:** docs/superpowers/specs/2026-10-14-x-winterflow-catalog-design.md (Sub-project 1)

**Base:** every snippet below was applied, task by task, to a copy of this branch at `48fc607` and its tests run there (Go: `go test ./...` and `go vet ./...` green, diff coverage of changed lines 92% Go / 100% web; web: `tsc -b`, ESLint and all 1691 Vitest tests green; the integration test of Task 9 deployed nginx:alpine and reached it on its allocated port). New files are given whole; edits to existing files are given as unified diffs against the previous task's tree (`git apply` them, or make the same edits by hand, then `gofmt -w` the file).

## Global Constraints

- Generated file: exactly `.winterflow/compose.winterflow.yml` (`xwinterflow.OverrideFile`), gitignored (in `derivedArtifacts`, so `.gitignore` + `neverCommitted`), passed as the last `-f` by `composeArgsFor` and `run.sh`; it holds `${NAME}` references, never values.
- Expose binds: `http` -> `127.0.0.1:<host>:<port>`; `tcp`/`udp` -> `<host>:<port>/<proto>` (every interface). Entry id `<container>:<port>/<protocol>`; duplicates within a block are invalid; protocol defaults to `http`.
- `EXPOSE_PORT_RANGE` default `20000-29999` (agent, standalone), documented in `.env.example` in the same task that adds the getter.
- Ports live in the committed `.winterflow/config.json` under `ports`: `{"app:8080/http": {"host": 20013, "pinned": false}}`. A save whose config has no `ports` key keeps the stored pins; with the key, its pinned entries are the pins.
- Allocation is collision-free across apps (one server-wide `portMu` + in-memory index built from every app's config.json, pruned in `forgetApp`); a listen probe skips ports something else holds; ports the app already holds are never probed.
- `IngressDomain`: exactly one of `expose` (`<service>:<port>` of an `http` entry) or `upstream_port`.
- Variable names `^[A-Z][A-Z0-9_]*$`; `WF_*` and `COMPOSE_PROJECT_NAME` reserved; nothing reads `_*` names (none exist in the code: `grep -rn '_APP_ID' --include=*.go .` finds only `WF_APP_ID`).
- `pattern`: RE2, at most 512 bytes, matched against the whole value (`^(?:p)$`); `description` at most 1000 bytes (a YAML comment fallback is cut to 1000 bytes on a rune boundary).
- A plain value may not contain `$`; secrets may (`.env.secrets` is written with every value quoted and `$` escaped).
- A non-external top-level volume with a literal `name:` (no `$`): `ModeTemplate` error, `ModeApp` warning.
- No backward compatibility: unknown `x-winterflow` keys (including the retired `volumes`) are errors; no data migration.
- No per-app exec in polled paths: allocation and the override file use files read at save/deploy; the probe is a `net.Listen` for ports new to the app only.
- Secret values never reach the API or `compose.winterflow.yml`; the agent's errors name the variable, never a secret value.
- Fail closed: every changed route keeps `webutil.RequireServerAccess`; no new route.
- Coverage gate: >=80% of changed lines per language (`make diffcov DIFFCOV_BASE=main`); `api/openapi.yaml` regenerated with `make openapi` whenever a response or request type changes.
- Never touch `web/src/components/ui/` (vendored shadcn). After `pnpm --dir web run build`, restore `web/dist/.gitkeep` (`touch web/dist/.gitkeep`).
- Commit messages: Conventional Commits, ending with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` (the `-m ... -m ...` form below does that).

## How to run things

- One Go package: `go test ./internal/domain/compose/xwinterflow/`; one test: add `-run TestName`. The orchestrator package takes ~45 s (`go test ./internal/infra/orchestrator/docker_compose/`); with `-race` ~50 s.
- Contract suite: `go test ./internal/app/web/contract/ -run TestSaveAppXWinterflow` (a partial `-run` skips the route guard unless `WF_CONTRACT_GUARD=require`); full: `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/`.
- OpenAPI: `make openapi`, then `go test ./internal/app/web/ -run TestOpenAPIUpToDate`.
- Web: `CI=true pnpm --dir web install --frozen-lockfile` once; one file: `CI=true pnpm --dir web exec vitest run src/lib/declared-variables.test.ts`; all: `CI=true pnpm --dir web run test` (~4 min); `pnpm --dir web exec tsc -b`; `pnpm --dir web run lint`.
- Diff coverage: commit, then `make diffcov DIFFCOV_BASE=main`.

## Spec decisions this plan makes

- **"A pin that another app holds is a 400":** the API holds no index of other apps' ports (allocation lives on the agent), so save-app 400s a pin's syntax, a pin for no expose entry and two entries pinned to one port; a pin another app holds (or the probe finds busy) is refused by the agent with `ErrPortTaken` (a `model.Rejected` error naming the other app), before anything is committed, and arrives as the save's failed result.
- **When a port is allocated:** at every commit that can change the entries (save, draft or not; rollback; git source update), because `config.json` is committed there; the probe runs only for ports new to the app, so a redeploy never trips on its own containers.
- **Index "built at start":** built on the first allocation after a start from every app's `config.json` (file reads only), then kept in step under `portMu`.
- **Overrides' `environment`:** a list is turned into a mapping and merged (compose merges environment as a map); every other list, at any depth, gets `!override`. Expose entries are appended to the service's ports (to the overridden list when the overrides set `ports`).
- **"With a value":** a non-empty plain value or a non-empty decrypted secret; the editor does not send a declared variable left empty, so the body literal stands. A declared `encrypted` variable sent as plain is a 400.
- **Merging several compose files' blocks:** a later file's variable, override key or backup replaces an earlier one of the same name; expose entries and files are merged.
- **Editor source of declared variables and expose entries:** `app.get` (the agent's parse, which covers git-sourced apps); no YAML parser is added to the web bundle.
- **Source update:** a port sync failure there is logged, not fatal (the source lock is already written; the deploy then reports the missing port), so an upstream change is never stranded uncommitted.

## Review Focus

1. **A redeploy probing its own ports** (the app's containers hold them, so a naive probe calls them busy and moves or refuses them) - pinned by `TestSaveKeepsPortsWithoutProbingThem` (Task 4).
2. **A save from a client that doesn't know about ports dropping the user's pins** (Settings saving a colour goes through `buildSavePayload`; template instances render configs without `ports`) - pinned by the "a save without a ports key keeps the pin" part of `TestPinnedPorts` (Task 4) and `sends only the pins` in `app-editor-io-expose.test.ts` (Task 8).
3. **Rollback to a revision whose port another app took since** (silent double binding, or a half-applied rollback) - pinned by `TestRemovedEntriesAndRollback` and `TestRollbackRefusedByAPinAnotherAppHolds` (Task 4).
4. **Concurrent saves of different apps on the distributed agent, and an agent restart** (two apps handed one port; the index forgotten) - pinned by `TestPortsDoNotCollideBetweenApps` (Task 4; run the package with `-race`).
5. **Values leaking or being re-interpolated** (a value in the override file, a secret with `$` expanded by compose, a list-form `environment:` replaced instead of merged) - pinned by `TestRenderNotes` and the `overrides` golden (Task 2), `TestDeployWritesTheOverrideFileLast` and `TestSecretsWithDollarStayLiteral` (Task 5).

---
### Task 1: The x-winterflow parser

**Files:**
- Modify: `internal/domain/backup/recipe/compose.go` (split `ParseCompose` into `DecodeFiles` + `ParseComposeNodes`)
- Test: `internal/domain/backup/recipe/recipe_test.go` (append one test)
- Create: `internal/domain/compose/xwinterflow/xwinterflow.go`, `internal/domain/compose/xwinterflow/values.go`
- Test: `internal/domain/compose/xwinterflow/xwinterflow_test.go`, `internal/domain/compose/xwinterflow/values_test.go`

**Interfaces:**
- Consumes: `recipe.Compose`, `model.Rejected`, `varref.References(data []byte, into map[string]bool)`.
- Produces:
  - `recipe.DecodeFiles(files ...[]byte) []*yaml.Node` (nil entry = not valid YAML), `recipe.ParseComposeNodes(docs ...*yaml.Node) (recipe.Compose, error)`; `recipe.ParseCompose` keeps its signature.
  - `xwinterflow.Parse(mode xwinterflow.Mode, files ...[]byte) (xwinterflow.Spec, error)`; `xwinterflow.ModeApp`, `xwinterflow.ModeTemplate`.
  - `type Spec struct { Declared bool; Expose []Expose; Variables []DeclaredVariable; Overrides map[string]*yaml.Node; Files []string; Compose recipe.Compose; Warnings []string }`, `(Spec) Variable(name string) (DeclaredVariable, bool)`, `(Spec) HTTPTargets() map[string]bool`.
  - `type Expose struct { Container string; Port int; Protocol string }` with `ID()` (`app:8080/http`), `Target()` (`app:8080`), `Bind()` (`127.0.0.1` | `0.0.0.0`); constants `ProtocolHTTP`, `ProtocolTCP`, `ProtocolUDP`, `MaxPattern` (512), `MaxDescription` (1000).
  - `type DeclaredVariable struct { Name, Pattern, Description string; Required, Encrypted bool; Default *string; Services []string }` (JSON: `name`, `required`, `encrypted`, `pattern,omitempty`, `description,omitempty`, `default`, `services`).
  - `(Spec) CheckValues(plain map[string]string, secret map[string]bool) error`, `(Spec) CheckSecretValues(secrets map[string]string) error`.
  - `xwinterflow.ErrInvalid` (format errors) and `xwinterflow.ErrValue` (value errors), both matching `model.ErrRejected` (400).
  - Unexported node helpers later tasks use inside the package: `value(m, key)`, `mapping()`, `scalar(v)`, `setKey(m, key, val)`, `matches(v, val)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/domain/backup/recipe/recipe_test.go`:

````go
// DecodeFiles decodes each file once; ParseComposeNodes reads the trees
// (the x-winterflow parser shares them) and marks an undecodable one.
func TestParseComposeNodesSharesDecodedFiles(t *testing.T) {
	docs := DecodeFiles([]byte(compose), []byte("services: [\n"))
	if len(docs) != 2 || docs[0] == nil || docs[1] != nil {
		t.Fatalf("decoded = %v", docs)
	}
	c, err := ParseComposeNodes(docs...)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Unparsed || len(c.Recipe) != 3 || !c.Volumes["data"] {
		t.Fatalf("compose = %+v", c)
	}
}
````

Create `internal/domain/compose/xwinterflow/xwinterflow_test.go`:

````go
package xwinterflow

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

const notes = `
services:
  app:
    image: ghcr.io/example/notes:latest
    environment:
      # Public URL the browser uses, e.g. https://notes.example.com
      BASE_URL: "http://localhost:8080"
      # Secret used to sign sessions.
      SESSION_SECRET: "changeme"
      LANGUAGE: "en"
    volumes:
      - data:/var/lib/notes
  db:
    image: postgres:16
    environment:
      - POSTGRES_USER=notes
      # the database password
      - POSTGRES_PASSWORD=notes
      - LANGUAGE
    volumes:
      - pg:/var/lib/postgresql/data
volumes:
  data:
  pg:
x-winterflow:
  expose:
    - container: app
      port: 8080
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL:       { required: true, pattern: "^https?://.+" }
    SESSION_SECRET: { required: true, encrypted: true }
    LANGUAGE:       { pattern: "^[a-z]{2}$", description: "Two-letter UI language" }
    POSTGRES_PASSWORD: { encrypted: true }
    DB_PASSWORD:    { encrypted: true }
  overrides:
    services:
      app:
        environment:
          DATABASE_URL: postgres://notes:${DB_PASSWORD}@db:5432/notes
  files:
    - config.yml
  backups:
    - name: db
      type: files
      volume: pg
      every: 24h
`

func parse(t *testing.T, mode Mode, files ...string) Spec {
	t.Helper()
	raw := make([][]byte, len(files))
	for i, f := range files {
		raw[i] = []byte(f)
	}
	s, err := Parse(mode, raw...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func str(s string) *string { return &s }

func TestParse(t *testing.T) {
	s := parse(t, ModeApp, notes)
	if !s.Declared {
		t.Fatal("block not seen")
	}
	wantExpose := []Expose{{"app", 8080, ProtocolHTTP}, {"db", 5432, ProtocolTCP}}
	if !reflect.DeepEqual(s.Expose, wantExpose) {
		t.Fatalf("expose = %+v", s.Expose)
	}
	if s.Expose[0].ID() != "app:8080/http" || s.Expose[0].Target() != "app:8080" || s.Expose[0].Bind() != "127.0.0.1" || s.Expose[1].Bind() != "0.0.0.0" {
		t.Fatalf("ids = %s %s %s", s.Expose[0].ID(), s.Expose[0].Target(), s.Expose[1].Bind())
	}
	want := []DeclaredVariable{
		{Name: "BASE_URL", Required: true, Pattern: "^https?://.+", Description: "Public URL the browser uses, e.g. https://notes.example.com",
			Default: str("http://localhost:8080"), Services: []string{"app"}},
		{Name: "SESSION_SECRET", Required: true, Encrypted: true, Description: "Secret used to sign sessions.", Services: []string{"app"}},
		{Name: "LANGUAGE", Pattern: "^[a-z]{2}$", Description: "Two-letter UI language", Default: str("en"), Services: []string{"app", "db"}},
		{Name: "POSTGRES_PASSWORD", Encrypted: true, Description: "the database password", Services: []string{"db"}},
		{Name: "DB_PASSWORD", Encrypted: true, Services: []string{}},
	}
	if !reflect.DeepEqual(s.Variables, want) {
		t.Fatalf("variables =\n%+v\nwant\n%+v", s.Variables, want)
	}
	if s.Overrides["app"] == nil || len(s.Overrides) != 1 {
		t.Fatalf("overrides = %v", s.Overrides)
	}
	if !reflect.DeepEqual(s.Files, []string{"config.yml"}) {
		t.Fatalf("files = %v", s.Files)
	}
	if len(s.Compose.Recipe) != 1 || s.Compose.Recipe[0].Name != "db" || !s.Compose.Volumes["pg"] {
		t.Fatalf("compose = %+v", s.Compose)
	}
	if len(s.Warnings) != 0 {
		t.Fatalf("warnings = %v (DB_PASSWORD is used by an override)", s.Warnings)
	}
	if v, ok := s.Variable("LANGUAGE"); !ok || v.Pattern != "^[a-z]{2}$" {
		t.Fatalf("Variable = %+v %v", v, ok)
	}
	if _, ok := s.Variable("NOPE"); ok {
		t.Fatal("unknown variable found")
	}
	if got := s.HTTPTargets(); !reflect.DeepEqual(got, map[string]bool{"app:8080": true}) {
		t.Fatalf("http targets = %v", got)
	}
}

func TestParseWithoutBlock(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app:\n    image: nginx\n")
	if s.Declared || len(s.Expose)+len(s.Variables)+len(s.Overrides)+len(s.Files) != 0 {
		t.Fatalf("spec = %+v", s)
	}
	s = parse(t, ModeApp, "services: [\n")
	if !s.Compose.Unparsed || s.Declared {
		t.Fatalf("an unparsable file: %+v", s)
	}
	if s = parse(t, ModeApp, ""); s.Declared {
		t.Fatalf("an empty file: %+v", s)
	}
}

// A later file's variable and override keys replace the earlier file's; an
// expose entry repeated in the override file stays once.
func TestParseMergesFiles(t *testing.T) {
	main := "services:\n  app:\n    image: a\n    environment: { A: one }\nx-winterflow:\n  expose: [{ container: app, port: 80 }]\n  variables:\n    A: { required: true }\n  overrides:\n    services:\n      app: { command: [one], user: root }\n"
	over := "services:\n  app:\n    environment: [B=two]\nx-winterflow:\n  expose: [{ container: app, port: 80 }, { container: app, port: 53, protocol: udp }]\n  variables:\n    A: { pattern: 'o.e' }\n    B: {}\n  overrides:\n    services:\n      app: { command: [two] }\n"
	s := parse(t, ModeApp, main, over)
	if len(s.Expose) != 2 || s.Expose[1].ID() != "app:53/udp" {
		t.Fatalf("expose = %+v", s.Expose)
	}
	if len(s.Variables) != 2 || s.Variables[0].Required || s.Variables[0].Pattern != "o.e" || *s.Variables[1].Default != "two" {
		t.Fatalf("variables = %+v", s.Variables)
	}
	app := s.Overrides["app"]
	if got := value(app, "command").Content[0].Value; got != "two" || value(app, "user").Value != "root" {
		t.Fatalf("override = %v / %v", got, value(app, "user"))
	}
}

func TestParseRules(t *testing.T) {
	const body = "services:\n  app:\n    image: a\n    environment:\n      LANG: en\n"
	cases := []struct {
		name, x, field string
	}{
		{"not a mapping", "x-winterflow: [1]\n", "x-winterflow: must be a mapping"},
		{"unknown key", "x-winterflow:\n  volumes: {}\n", "x-winterflow.volumes: unknown key"},
		{"expose not a list", "x-winterflow:\n  expose: {}\n", "x-winterflow.expose: must be a list"},
		{"expose unknown key", "x-winterflow:\n  expose: [{ container: app, port: 80, host: 1 }]\n", "x-winterflow.expose[0].host: unknown key"},
		{"expose unknown service", "x-winterflow:\n  expose: [{ container: web, port: 80 }]\n", `x-winterflow.expose[0].container: "web" is not a service`},
		{"expose port range", "x-winterflow:\n  expose: [{ container: app, port: 70000 }]\n", "x-winterflow.expose[0].port: 70000 is not a port"},
		{"expose port type", "x-winterflow:\n  expose: [{ container: app, port: http }]\n", "cannot unmarshal !!str `http` into int"},
		{"expose protocol", "x-winterflow:\n  expose: [{ container: app, port: 80, protocol: sctp }]\n", `x-winterflow.expose[0].protocol: "sctp" is not http, tcp or udp`},
		{"expose duplicate", "x-winterflow:\n  expose: [{ container: app, port: 80 }, { container: app, port: 80, protocol: http }]\n", "x-winterflow.expose[1]: app:80/http is listed twice"},
		{"variables not a mapping", "x-winterflow:\n  variables: [A]\n", "x-winterflow.variables: must be a mapping"},
		{"variable name", "x-winterflow:\n  variables:\n    lang: {}\n", "x-winterflow.variables.lang: a name is upper-case"},
		{"variable reserved WF_", "x-winterflow:\n  variables:\n    WF_APP_ID: {}\n", "x-winterflow.variables.WF_APP_ID: WF_APP_ID is reserved"},
		{"variable reserved project", "x-winterflow:\n  variables:\n    COMPOSE_PROJECT_NAME: {}\n", "COMPOSE_PROJECT_NAME is reserved"},
		{"variable unknown key", "x-winterflow:\n  variables:\n    LANG: { default: en }\n", "x-winterflow.variables.LANG.default: unknown key"},
		{"variable bad type", "x-winterflow:\n  variables:\n    LANG: { required: maybe }\n", "cannot unmarshal !!str `maybe` into bool"},
		{"pattern too long", "x-winterflow:\n  variables:\n    LANG: { pattern: '" + strings.Repeat("a", 513) + "' }\n", "x-winterflow.variables.LANG.pattern: at most 512 bytes"},
		{"pattern not RE2", "x-winterflow:\n  variables:\n    LANG: { pattern: '(?=a)' }\n", "x-winterflow.variables.LANG.pattern: error parsing regexp"},
		{"description too long", "x-winterflow:\n  variables:\n    LANG: { description: '" + strings.Repeat("d", 1001) + "' }\n", "x-winterflow.variables.LANG.description: at most 1000 bytes"},
		{"default fails pattern", "x-winterflow:\n  variables:\n    LANG: { pattern: '[a-z]{3}' }\n", `x-winterflow.variables.LANG.pattern: the compose file's own value "en" does not match [a-z]{3}`},
		{"overrides other than services", "x-winterflow:\n  overrides:\n    volumes: {}\n", "x-winterflow.overrides.volumes: only services can be overridden"},
		{"overrides unknown service", "x-winterflow:\n  overrides:\n    services:\n      web: { user: root }\n", `x-winterflow.overrides.services.web: "web" is not a service`},
		{"override not a mapping", "x-winterflow:\n  overrides:\n    services:\n      app: [1]\n", "x-winterflow.overrides.services.app: must be a mapping"},
		{"files not a list", "x-winterflow:\n  files: config.yml\n", "x-winterflow.files: must be a list"},
		{"file outside", "x-winterflow:\n  files: [../etc/passwd]\n", `x-winterflow.files[0]: "../etc/passwd" is not a path`},
		{"file absolute", "x-winterflow:\n  files: [/etc/passwd]\n", `x-winterflow.files[0]: "/etc/passwd" is not a path`},
		{"backups still checked", "x-winterflow:\n  backups: [{ name: Bad, type: files }]\n", "x-winterflow.backups[0].name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(ModeApp, []byte(body+tc.x))
			if err == nil || !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("err = %v, want %q", err, tc.field)
			}
		})
	}
}

func TestParseVolumeNameRule(t *testing.T) {
	const named = "services:\n  app:\n    image: a\nvolumes:\n  data:\n    name: notes-data\n  shared:\n    name: media\n    external: true\n  scoped:\n    name: notes-${WF_APP_ID}\n  plain: {}\n"
	s := parse(t, ModeApp, named)
	if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], `volumes.data: the name "notes-data" would be shared`) {
		t.Fatalf("app warnings = %v", s.Warnings)
	}
	if _, err := Parse(ModeTemplate, []byte(named)); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "volumes.data") {
		t.Fatalf("template err = %v", err)
	}
}

func TestParseUnusedVariableWarns(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app:\n    image: a\nx-winterflow:\n  variables:\n    LONELY: {}\n")
	if len(s.Warnings) != 1 || s.Warnings[0] != "x-winterflow.variables.LONELY: no service environment and no override uses it" {
		t.Fatalf("warnings = %v", s.Warnings)
	}
	if s.Variables[0].Default != nil || len(s.Variables[0].Services) != 0 {
		t.Fatalf("variable = %+v", s.Variables[0])
	}
}

func TestDescriptionFromComment(t *testing.T) {
	long := strings.Repeat("é", 600) // 1200 bytes
	s := parse(t, ModeApp, "services:\n  app:\n    image: a\n    environment:\n      # "+long+"\n      NOTE: x\nx-winterflow:\n  variables:\n    NOTE: {}\n")
	d := s.Variables[0].Description
	if len(d) > MaxDescription || !strings.HasPrefix(long, d) || d == "" {
		t.Fatalf("description cut to %d bytes", len(d))
	}
}
````

Create `internal/domain/compose/xwinterflow/values_test.go`:

````go
package xwinterflow

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func TestCheckValues(t *testing.T) {
	s := parse(t, ModeApp, notes)
	ok := map[string]string{"BASE_URL": "https://notes.example.com", "LANGUAGE": "de"}
	secrets := map[string]bool{"SESSION_SECRET": true}
	if err := s.CheckValues(ok, secrets); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		plain  map[string]string
		secret map[string]bool
		want   string
	}{
		{"required missing", map[string]string{}, secrets, "variable BASE_URL: a value is required"},
		{"required empty", map[string]string{"BASE_URL": ""}, secrets, "variable BASE_URL: a value is required"},
		{"required secret missing", ok, map[string]bool{}, "variable SESSION_SECRET: a value is required"},
		{"pattern", map[string]string{"BASE_URL": "notes.example.com"}, secrets, `variable BASE_URL: "notes.example.com" does not match ^https?://.+`},
		{"dollar", map[string]string{"BASE_URL": "https://x/$HOME"}, secrets, "variable BASE_URL: a plain value can't contain $"},
		{"secret sent plain", map[string]string{"BASE_URL": "https://x", "SESSION_SECRET": "s"}, map[string]bool{}, "variable SESSION_SECRET: it is a secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.CheckValues(tc.plain, tc.secret)
			if !errors.Is(err, ErrValue) || !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	// A declared plain variable given as a secret is checked after decryption.
	if err := s.CheckValues(map[string]string{"BASE_URL": "https://x"}, map[string]bool{"SESSION_SECRET": true, "LANGUAGE": true}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSecretValues(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app:\n    image: a\n    environment: { PIN: x, TOKEN: y }\nx-winterflow:\n  variables:\n    PIN: { encrypted: true, pattern: '[0-9]{4}' }\n    TOKEN: { encrypted: true, required: true }\n")
	if err := s.CheckSecretValues(map[string]string{"PIN": "1234", "TOKEN": "t$k"}); err != nil {
		t.Fatal(err)
	}
	err := s.CheckSecretValues(map[string]string{"PIN": "12a4", "TOKEN": "t"})
	if !errors.Is(err, ErrValue) || !strings.Contains(err.Error(), "variable PIN: the secret value does not match [0-9]{4}") || strings.Contains(err.Error(), "12a4") {
		t.Fatalf("err = %v", err)
	}
	if err := s.CheckSecretValues(map[string]string{"TOKEN": ""}); !errors.Is(err, ErrValue) || !strings.Contains(err.Error(), "TOKEN: a value is required") {
		t.Fatalf("err = %v", err)
	}
}
````

- [ ] **Step 2: Run them, expect FAIL**

Run: `go test ./internal/domain/backup/recipe/ ./internal/domain/compose/xwinterflow/`
Expected: FAIL to build: `undefined: DecodeFiles`, `undefined: ParseComposeNodes`; and for the new package `undefined: Parse` / `undefined: ModeApp` (no non-test files yet).

- [ ] **Step 3: Implement**

Patch `internal/domain/backup/recipe/compose.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/domain/backup/recipe/compose.go b/internal/domain/backup/recipe/compose.go
index 0a16c26..b66a5a6 100644
--- a/internal/domain/backup/recipe/compose.go
+++ b/internal/domain/backup/recipe/compose.go
@@ -38,8 +38,32 @@ var keepKeys = map[string]bool{"recent": true, "daily": true, "monthly": true}
 // decoded. The recipe is checked only for its shape here (keys, types, duplicate names);
 // Validate checks the values.
 func ParseCompose(files ...[]byte) (Compose, error) {
+	return ParseComposeNodes(DecodeFiles(files...)...)
+}
+
+// DecodeFiles decodes each compose file once into its YAML node tree; a
+// file that is not valid YAML is nil.
+func DecodeFiles(files ...[]byte) []*yaml.Node {
+	out := make([]*yaml.Node, len(files))
+	for i, raw := range files {
+		var n yaml.Node
+		if yaml.Unmarshal(raw, &n) == nil {
+			out[i] = &n
+		}
+	}
+	return out
+}
+
+// ParseComposeNodes is ParseCompose over files DecodeFiles decoded: the
+// x-winterflow parser (internal/domain/compose/xwinterflow) reads the same
+// trees, so a compose file is parsed once. A nil tree sets Unparsed.
+func ParseComposeNodes(docs ...*yaml.Node) (Compose, error) {
 	out := Compose{Volumes: map[string]bool{}, Services: map[string][]string{}, Mounts: map[string][]Mount{}}
-	for _, raw := range files {
+	for _, node := range docs {
+		if node == nil {
+			out.Unparsed = true
+			continue
+		}
 		var doc struct {
 			Services map[string]struct {
 				Profiles []string    `yaml:"profiles"`
@@ -48,7 +72,7 @@ func ParseCompose(files ...[]byte) (Compose, error) {
 			Volumes     map[string]any `yaml:"volumes"`
 			XWinterflow yaml.Node      `yaml:"x-winterflow"`
 		}
-		if err := yaml.Unmarshal(raw, &doc); err != nil {
+		if err := node.Decode(&doc); err != nil {
 			var typeErr *yaml.TypeError
 			if !errors.As(err, &typeErr) {
 				out.Unparsed = true
@@ -85,8 +109,8 @@ func ParseCompose(files ...[]byte) (Compose, error) {
 	return out, nil
 }
 
-// parseRecipe decodes x-winterflow.backups. Other x-winterflow keys are left
-// to later features.
+// parseRecipe decodes x-winterflow.backups. The other x-winterflow keys are
+// the xwinterflow package's.
 func parseRecipe(x *yaml.Node) ([]BackupConfig, error) {
 	if x.Kind == 0 || x.Tag == "!!null" {
 		return nil, nil
````

Create `internal/domain/compose/xwinterflow/xwinterflow.go`:

````go
// Package xwinterflow parses and validates the x-winterflow block of an
// app's compose files: exposed ports, declared variables, overrides, extra
// files and the backup recipe (internal/domain/backup/recipe, read from the
// same decoded files, so a compose file is parsed once). The API (save-app
// 400s) and the agent (save and deploy) share it; Render turns a Spec into
// the compose override file the agent passes last.
package xwinterflow

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/varref"
)

// Mode picks how the rules that differ between apps and templates apply.
type Mode int

const (
	// ModeApp warns about a volume with a literal name.
	ModeApp Mode = iota
	// ModeTemplate refuses it: two installs would share one volume.
	ModeTemplate
)

// Protocols of an expose entry.
const (
	ProtocolHTTP = "http"
	ProtocolTCP  = "tcp"
	ProtocolUDP  = "udp"
)

// Limits of the format.
const (
	MaxPattern     = 512
	MaxDescription = 1000
	maxPath        = 1024
)

// ErrInvalid marks an x-winterflow block that can't be used; the message
// names the field. It matches model.ErrRejected (a 400).
var ErrInvalid = model.Rejected("invalid x-winterflow")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Expose is one container port published on the host.
type Expose struct {
	Container string
	Port      int
	Protocol  string
}

// Target is the entry as an ingress names it: <container>:<port>.
func (e Expose) Target() string { return e.Container + ":" + strconv.Itoa(e.Port) }

// ID identifies the entry, <container>:<port>/<protocol>: the key of its
// host port in config.json's ports.
func (e Expose) ID() string { return e.Target() + "/" + e.Protocol }

// Bind is the host address the entry is published on: loopback for http
// (only the ingress reaches it), every interface for tcp and udp.
func (e Expose) Bind() string {
	if e.Protocol == ProtocolHTTP {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

// DeclaredVariable is one x-winterflow.variables entry, with what the
// services' environments say about it.
type DeclaredVariable struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Encrypted   bool   `json:"encrypted"`
	Pattern     string `json:"pattern,omitempty"`
	Description string `json:"description,omitempty"`
	// Default is the body literal of the first service whose environment
	// has the key: nil when none has one, and always for a secret.
	Default *string `json:"default"`
	// Services are the services whose environment has the key, in file
	// order: the ones a value is applied to.
	Services []string `json:"services"`
}

// Spec is what an app's compose files declare under x-winterflow, plus the
// compose model backups need.
type Spec struct {
	// Declared is set when a file has an x-winterflow block.
	Declared  bool
	Expose    []Expose
	Variables []DeclaredVariable
	// Overrides are the services' override fragments (service name ->
	// mapping), merged in by Render.
	Overrides map[string]*yaml.Node
	Files     []string
	Compose   recipe.Compose
	// Warnings are rules an app may break (a literal volume name, a
	// variable nothing uses).
	Warnings []string
}

var (
	nameRE       = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	exposeKeys   = map[string]bool{"container": true, "port": true, "protocol": true}
	variableKeys = map[string]bool{"required": true, "encrypted": true, "pattern": true, "description": true}
)

// Parse decodes the compose files once, in the order compose loads them
// (model.DetectComposeFiles, or a git-sourced app's compose_path), and
// validates their x-winterflow blocks. A later file's variable, override
// key or backup replaces an earlier file's of the same name; expose entries
// and files are merged. A file that is not valid YAML is skipped and sets
// Compose.Unparsed, as recipe.ParseCompose does.
func Parse(mode Mode, files ...[]byte) (Spec, error) {
	docs := recipe.DecodeFiles(files...)
	comp, err := recipe.ParseComposeNodes(docs...)
	if err != nil {
		return Spec{}, err
	}
	s := Spec{Compose: comp, Overrides: map[string]*yaml.Node{}}
	var envs []envKey
	for _, doc := range docs {
		root := rootMapping(doc)
		if root == nil {
			continue
		}
		envs = append(envs, envKeys(root)...)
		if err := s.checkVolumes(root, mode); err != nil {
			return Spec{}, err
		}
		x := value(root, "x-winterflow")
		if x == nil || x.Tag == "!!null" {
			continue
		}
		if x.Kind != yaml.MappingNode {
			return Spec{}, invalid("x-winterflow", "must be a mapping")
		}
		s.Declared = true
		if err := s.read(x); err != nil {
			return Spec{}, err
		}
	}
	if err := s.resolve(envs); err != nil {
		return Spec{}, err
	}
	return s, nil
}

// Variable is the declared variable name.
func (s Spec) Variable(name string) (DeclaredVariable, bool) {
	i := slices.IndexFunc(s.Variables, func(v DeclaredVariable) bool { return v.Name == name })
	if i < 0 {
		return DeclaredVariable{}, false
	}
	return s.Variables[i], true
}

// HTTPTargets are the targets (<container>:<port>) of the http entries: the
// ones an ingress hostname may point at.
func (s Spec) HTTPTargets() map[string]bool {
	out := map[string]bool{}
	for _, e := range s.Expose {
		if e.Protocol == ProtocolHTTP {
			out[e.Target()] = true
		}
	}
	return out
}

// read merges one file's x-winterflow block into s.
func (s *Spec) read(x *yaml.Node) error {
	for i := 0; i+1 < len(x.Content); i += 2 {
		key, val := x.Content[i].Value, x.Content[i+1]
		var err error
		switch key {
		case "expose":
			err = s.readExpose(val)
		case "variables":
			err = s.readVariables(val)
		case "overrides":
			err = s.readOverrides(val)
		case "files":
			err = s.readFiles(val)
		case "backups":
			// recipe.ParseComposeNodes read it.
		default:
			err = invalid("x-winterflow."+key, "unknown key: use expose, variables, overrides, files or backups")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Spec) readExpose(list *yaml.Node) error {
	if list.Tag == "!!null" {
		return nil
	}
	if list.Kind != yaml.SequenceNode {
		return invalid("x-winterflow.expose", "must be a list of {container, port, protocol}")
	}
	seen := map[string]bool{}
	for i, item := range list.Content {
		field := fmt.Sprintf("x-winterflow.expose[%d]", i)
		if err := checkKeys(item, exposeKeys, field); err != nil {
			return err
		}
		var raw struct {
			Container string `yaml:"container"`
			Port      int    `yaml:"port"`
			Protocol  string `yaml:"protocol"`
		}
		if err := item.Decode(&raw); err != nil {
			return invalid(field, "%s", yamlError(err))
		}
		e := Expose{Container: raw.Container, Port: raw.Port, Protocol: raw.Protocol}
		if e.Protocol == "" {
			e.Protocol = ProtocolHTTP
		}
		if _, ok := s.Compose.Services[e.Container]; !ok {
			return invalid(field+".container", "%q is not a service of this compose file", e.Container)
		}
		if e.Port < 1 || e.Port > 65535 {
			return invalid(field+".port", "%d is not a port from 1 to 65535", e.Port)
		}
		if e.Protocol != ProtocolHTTP && e.Protocol != ProtocolTCP && e.Protocol != ProtocolUDP {
			return invalid(field+".protocol", "%q is not http, tcp or udp", e.Protocol)
		}
		if seen[e.ID()] {
			return invalid(field, "%s is listed twice", e.ID())
		}
		seen[e.ID()] = true
		if !slices.Contains(s.Expose, e) { // an earlier file's entry stays once
			s.Expose = append(s.Expose, e)
		}
	}
	return nil
}

func (s *Spec) readVariables(m *yaml.Node) error {
	if m.Tag == "!!null" {
		return nil
	}
	if m.Kind != yaml.MappingNode {
		return invalid("x-winterflow.variables", "must be a mapping of NAME: {required, encrypted, pattern, description}")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		name, def := m.Content[i].Value, m.Content[i+1]
		field := "x-winterflow.variables." + name
		if !nameRE.MatchString(name) {
			return invalid(field, "a name is upper-case letters, digits and _, starting with a letter")
		}
		if strings.HasPrefix(name, "WF_") || name == "COMPOSE_PROJECT_NAME" {
			return invalid(field, "%s is reserved", name)
		}
		v := DeclaredVariable{Name: name}
		if def.Tag != "!!null" {
			if err := checkKeys(def, variableKeys, field); err != nil {
				return err
			}
			var raw struct {
				Required    bool   `yaml:"required"`
				Encrypted   bool   `yaml:"encrypted"`
				Pattern     string `yaml:"pattern"`
				Description string `yaml:"description"`
			}
			if err := def.Decode(&raw); err != nil {
				return invalid(field, "%s", yamlError(err))
			}
			v.Required, v.Encrypted, v.Pattern, v.Description = raw.Required, raw.Encrypted, raw.Pattern, raw.Description
		}
		if len(v.Pattern) > MaxPattern {
			return invalid(field+".pattern", "at most %d bytes", MaxPattern)
		}
		if _, err := compilePattern(v.Pattern); err != nil {
			return invalid(field+".pattern", "%v", err)
		}
		if len(v.Description) > MaxDescription {
			return invalid(field+".description", "at most %d bytes", MaxDescription)
		}
		if j := slices.IndexFunc(s.Variables, func(o DeclaredVariable) bool { return o.Name == name }); j >= 0 {
			s.Variables[j] = v
		} else {
			s.Variables = append(s.Variables, v)
		}
	}
	return nil
}

func (s *Spec) readOverrides(m *yaml.Node) error {
	if m.Tag == "!!null" {
		return nil
	}
	if m.Kind != yaml.MappingNode {
		return invalid("x-winterflow.overrides", "must be a mapping with services")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if key := m.Content[i].Value; key != "services" {
			return invalid("x-winterflow.overrides."+key, "only services can be overridden")
		}
		svcs := m.Content[i+1]
		if svcs.Tag == "!!null" {
			continue
		}
		if svcs.Kind != yaml.MappingNode {
			return invalid("x-winterflow.overrides.services", "must be a mapping of service names")
		}
		for j := 0; j+1 < len(svcs.Content); j += 2 {
			name, body := svcs.Content[j].Value, svcs.Content[j+1]
			field := "x-winterflow.overrides.services." + name
			if _, ok := s.Compose.Services[name]; !ok {
				return invalid(field, "%q is not a service of this compose file", name)
			}
			if body.Kind != yaml.MappingNode {
				return invalid(field, "must be a mapping of the service's keys")
			}
			merged := mapping()
			if prev := s.Overrides[name]; prev != nil {
				merged.Content = append(merged.Content, prev.Content...)
			}
			for k := 0; k+1 < len(body.Content); k += 2 {
				setKey(merged, body.Content[k].Value, body.Content[k+1])
			}
			s.Overrides[name] = merged
		}
	}
	return nil
}

func (s *Spec) readFiles(list *yaml.Node) error {
	if list.Tag == "!!null" {
		return nil
	}
	var files []string
	if list.Kind != yaml.SequenceNode || list.Decode(&files) != nil {
		return invalid("x-winterflow.files", "must be a list of paths relative to the compose file")
	}
	for i, f := range files {
		if f == "" || len(f) > maxPath || strings.HasPrefix(f, "/") || path.Clean(f) != f ||
			f == "." || f == ".." || strings.HasPrefix(f, "../") || hasControl(f) {
			return invalid(fmt.Sprintf("x-winterflow.files[%d]", i), "%q is not a path relative to the compose file, like config.yml", f)
		}
		if !slices.Contains(s.Files, f) {
			s.Files = append(s.Files, f)
		}
	}
	return nil
}

// checkVolumes applies the volume-name rule to one file's top-level
// volumes: one that is not external and has a literal name: would be
// shared by every install of the file.
func (s *Spec) checkVolumes(root *yaml.Node, mode Mode) error {
	vols := value(root, "volumes")
	if vols == nil || vols.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(vols.Content); i += 2 {
		key, def := vols.Content[i].Value, vols.Content[i+1]
		name := value(def, "name")
		if name == nil || name.Kind != yaml.ScalarNode || name.Value == "" || strings.Contains(name.Value, "$") {
			continue
		}
		if ext := value(def, "external"); ext != nil && (ext.Value == "true" || ext.Kind == yaml.MappingNode) {
			continue
		}
		msg := fmt.Sprintf("volumes.%s: the name %q would be shared by every install of this compose file: drop name: (each app gets its own volume) or mark it external: true to share it on purpose", key, name.Value)
		if mode == ModeTemplate {
			return fmt.Errorf("%w: %s", ErrInvalid, msg)
		}
		s.Warnings = append(s.Warnings, msg)
	}
	return nil
}

// envKey is one entry of a service's environment.
type envKey struct {
	service string
	name    string
	literal *string // nil: a bare list NAME, a null or a non-scalar value
	comment string  // the YAML comment right above it
}

// envKeys lists the environment entries of every service of one file, in
// document order, map and list forms alike.
func envKeys(root *yaml.Node) []envKey {
	svcs := value(root, "services")
	if svcs == nil || svcs.Kind != yaml.MappingNode {
		return nil
	}
	var out []envKey
	for i := 0; i+1 < len(svcs.Content); i += 2 {
		svc := svcs.Content[i].Value
		env := value(svcs.Content[i+1], "environment")
		if env == nil {
			continue
		}
		switch env.Kind {
		case yaml.MappingNode:
			for j := 0; j+1 < len(env.Content); j += 2 {
				k, v := env.Content[j], env.Content[j+1]
				e := envKey{service: svc, name: k.Value, comment: comment(k.HeadComment)}
				if v.Kind == yaml.ScalarNode && v.Tag != "!!null" {
					lit := v.Value
					e.literal = &lit
				}
				out = append(out, e)
			}
		case yaml.SequenceNode:
			for _, item := range env.Content {
				if item.Kind != yaml.ScalarNode {
					continue
				}
				name, lit, ok := strings.Cut(item.Value, "=")
				e := envKey{service: svc, name: name, comment: comment(item.HeadComment)}
				if ok {
					e.literal = &lit
				}
				out = append(out, e)
			}
		}
	}
	return out
}

// resolve fills each variable's services, default and description from the
// environment entries, then checks the rules that need them.
func (s *Spec) resolve(envs []envKey) error {
	used := s.overrideRefs()
	for i := range s.Variables {
		v := &s.Variables[i]
		v.Services = []string{}
		for _, e := range envs {
			if e.name != v.Name || slices.Contains(v.Services, e.service) {
				continue
			}
			if len(v.Services) == 0 {
				if !v.Encrypted && e.literal != nil {
					lit := *e.literal
					v.Default = &lit
				}
				if v.Description == "" {
					v.Description = truncate(e.comment, MaxDescription)
				}
			}
			v.Services = append(v.Services, e.service)
		}
		field := "x-winterflow.variables." + v.Name
		if v.Default != nil && !matches(*v, *v.Default) {
			return invalid(field+".pattern", "the compose file's own value %q does not match %s", *v.Default, v.Pattern)
		}
		if len(v.Services) == 0 && !used[v.Name] {
			s.Warnings = append(s.Warnings, field+": no service environment and no override uses it")
		}
	}
	return nil
}

// overrideRefs are the variables the overrides interpolate.
func (s *Spec) overrideRefs() map[string]bool {
	refs := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			varref.References([]byte(n.Value), refs)
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	for _, n := range s.Overrides {
		walk(n)
	}
	return refs
}

// compilePattern compiles a pattern to match the whole value ("" matches
// anything).
func compilePattern(p string) (*regexp.Regexp, error) {
	return regexp.Compile(`^(?:` + p + `)$`)
}

// matches reports whether val satisfies v's pattern.
func matches(v DeclaredVariable, val string) bool {
	if v.Pattern == "" {
		return true
	}
	re, err := compilePattern(v.Pattern)
	return err == nil && re.MatchString(val)
}

// rootMapping is a decoded file's top-level mapping (nil: empty, not YAML,
// or not a mapping).
func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	if m := doc.Content[0]; m.Kind == yaml.MappingNode {
		return m
	}
	return nil
}

// value is the value of key in the mapping m (nil when absent).
func value(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// checkKeys refuses keys outside allowed in the mapping m.
func checkKeys(m *yaml.Node, allowed map[string]bool, field string) error {
	if m.Kind != yaml.MappingNode {
		return invalid(field, "must be a mapping")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i].Value; !allowed[k] {
			return invalid(field+"."+k, "unknown key")
		}
	}
	return nil
}

// yamlError drops the "yaml: unmarshal errors:" preamble.
func yamlError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:")
	return strings.Join(strings.Fields(msg), " ")
}

// comment is a head comment without its # marks, lines joined by spaces.
func comment(c string) string {
	var parts []string
	for _, line := range strings.Split(c, "\n") {
		if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

// truncate cuts s to at most n bytes on a rune boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// mapping is a new empty mapping node.
func mapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }

// scalar is a new string node.
func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// setKey sets key in the mapping m to val, replacing an existing entry.
func setKey(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalar(key), val)
}
````

Create `internal/domain/compose/xwinterflow/values.go`:

````go
package xwinterflow

import (
	"fmt"
	"strings"

	"winterflow/internal/domain/model"
)

// ErrValue marks a declared variable's value that breaks its rules: a
// required one without a value, a value failing its pattern, a plain value
// with $. It matches model.ErrRejected.
var ErrValue = model.Rejected("invalid variable value")

func valueError(name, format string, args ...any) error {
	return fmt.Errorf("%w: variable %s: %s", ErrValue, name, fmt.Sprintf(format, args...))
}

// CheckValues checks a save's values of the declared variables: plain are
// the plain values by name, secret the names that carry a secret
// (ciphertext to the API). A required variable needs a non-empty plain
// value or a secret; an encrypted one may not be plain; a plain value must
// match the pattern and may not contain $ (compose interpolates .env). A
// secret's pattern is checked once it is decrypted (CheckSecretValues).
func (s Spec) CheckValues(plain map[string]string, secret map[string]bool) error {
	for _, v := range s.Variables {
		if secret[v.Name] {
			continue
		}
		val := plain[v.Name]
		switch {
		case val == "" && v.Required:
			return valueError(v.Name, "a value is required")
		case val == "":
		case v.Encrypted:
			return valueError(v.Name, "it is a secret: save its value as a secret")
		case strings.Contains(val, "$"):
			return valueError(v.Name, "a plain value can't contain $ (make the variable secret to use one)")
		case !matches(v, val):
			return valueError(v.Name, "%q does not match %s", val, v.Pattern)
		}
	}
	return nil
}

// CheckSecretValues checks decrypted secret values (by name) against their
// variables: a required one may not be empty, and each must match its
// pattern. The value never appears in the error.
func (s Spec) CheckSecretValues(secrets map[string]string) error {
	for _, v := range s.Variables {
		val, ok := secrets[v.Name]
		switch {
		case !ok:
		case val == "" && v.Required:
			return valueError(v.Name, "a value is required")
		case val != "" && !matches(v, val):
			return valueError(v.Name, "the secret value does not match %s", v.Pattern)
		}
	}
	return nil
}
````

- [ ] **Step 4: Run, expect PASS**

Run: `gofmt -l internal/domain && go vet ./internal/domain/compose/xwinterflow/ && go test -cover ./internal/domain/backup/recipe/ ./internal/domain/compose/xwinterflow/ ./internal/domain/template/ ./internal/domain/usecase/app/`
Expected: no gofmt output; `ok` for each package; xwinterflow coverage about 95%.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/backup/recipe/compose.go internal/domain/backup/recipe/recipe_test.go internal/domain/compose/xwinterflow/
git commit -m "feat(xwinterflow): parse and validate the x-winterflow block" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 2: Render the override file

**Files:**
- Create: `internal/domain/compose/xwinterflow/render.go`
- Test: `internal/domain/compose/xwinterflow/render_test.go`, golden files `internal/domain/compose/xwinterflow/testdata/render/{notes,variables,expose,overrides}.yml`

**Interfaces:**
- Consumes: Task 1's `Spec`, `Expose`, `value`, `mapping`, `scalar`, `setKey`.
- Produces: `xwinterflow.OverrideFile = ".winterflow/compose.winterflow.yml"`; `xwinterflow.Render(spec Spec, hasValue func(name string) bool, hostPort func(e Expose) int) ([]byte, error)` - nil when there is nothing to apply; an error naming the entry when `hostPort` returns 0.

The golden files below were checked with the real CLI (`docker compose -f compose.yml --env-file .env --env-file .env.secrets -f .winterflow/compose.winterflow.yml config`, compose v5.5.1): `!override` replaced the body's `ports`, `command` and nested `healthcheck.test`; a list-form `environment:` merged with the map form; a secret `s3$cret` in `.env.secrets` stayed literal.

- [ ] **Step 1: Write the failing test and its golden files**

Create `internal/domain/compose/xwinterflow/render_test.go`:

````go
package xwinterflow

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/render")

// golden compares got with testdata/render/<name>.yml (rewritten by -update).
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "render", name+".yml")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func render(t *testing.T, s Spec, values []string, ports map[string]int) []byte {
	t.Helper()
	has := map[string]bool{}
	for _, v := range values {
		has[v] = true
	}
	out, err := Render(s, func(n string) bool { return has[n] }, func(e Expose) int { return ports[e.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRenderNotes(t *testing.T) {
	s := parse(t, ModeApp, notes)
	out := render(t, s, []string{"BASE_URL", "SESSION_SECRET", "LANGUAGE", "DB_PASSWORD"},
		map[string]int{"app:8080/http": 20013, "db:5432/tcp": 20014})
	golden(t, "notes", out)
	if strings.Contains(string(out), "notes.example.com") || strings.Contains(string(out), "changeme") {
		t.Fatalf("a value leaked into the override file:\n%s", out)
	}
}

// A variable without a value emits nothing: the body literal stands.
func TestRenderVariables(t *testing.T) {
	s := parse(t, ModeApp, notes)
	s.Expose, s.Overrides = nil, nil
	golden(t, "variables", render(t, s, []string{"LANGUAGE"}, nil))
}

func TestRenderExpose(t *testing.T) {
	s := parse(t, ModeApp, `
services:
  web: { image: a, ports: ["8080:80"] }
  dns: { image: b }
x-winterflow:
  expose:
    - { container: web, port: 80 }
    - { container: dns, port: 53, protocol: tcp }
    - { container: dns, port: 53, protocol: udp }
`)
	golden(t, "expose", render(t, s, nil, map[string]int{"web:80/http": 20000, "dns:53/tcp": 5353, "dns:53/udp": 5353}))
}

// Lists in overrides replace the body's (!override); an environment list
// merges as a mapping; expose entries join an overridden ports list.
func TestRenderOverrides(t *testing.T) {
	s := parse(t, ModeApp, `
services:
  app:
    image: a
    command: ["serve"]
    ports: ["9000:9000"]
    environment: [MODE=dev]
    healthcheck: { test: ["CMD", "true"] }
x-winterflow:
  expose: [{ container: app, port: 8080 }]
  variables:
    MODE: {}
  overrides:
    services:
      app:
        command: &cmd ["serve", "--port", "8080"]
        entrypoint: *cmd
        ports: ["127.0.0.1:9000:9000"]
        environment:
          - MODE=prod
          - PASSTHROUGH
        healthcheck:
          test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
        labels:
          app.id: ${WF_APP_ID}
`)
	golden(t, "overrides", render(t, s, []string{"MODE"}, map[string]int{"app:8080/http": 20001}))
}

func TestRenderNothing(t *testing.T) {
	out, err := Render(parse(t, ModeApp, "services:\n  app: { image: a }\n"), func(string) bool { return true }, func(Expose) int { return 1 })
	if err != nil || out != nil {
		t.Fatalf("no block: %q %v", out, err)
	}
	s := parse(t, ModeApp, "services:\n  app: { image: a, environment: { A: x } }\nx-winterflow:\n  variables:\n    A: {}\n")
	if out := render(t, s, nil, nil); out != nil {
		t.Fatalf("no value: %s", out)
	}
}

func TestRenderNeedsHostPort(t *testing.T) {
	s := parse(t, ModeApp, "services:\n  app: { image: a }\nx-winterflow:\n  expose: [{ container: app, port: 80 }]\n")
	_, err := Render(s, func(string) bool { return false }, func(Expose) int { return 0 })
	if err == nil || !strings.Contains(err.Error(), "app:80/http has no host port yet") {
		t.Fatalf("err = %v", err)
	}
}
````

Create `internal/domain/compose/xwinterflow/testdata/render/notes.yml`:

````yaml
# Generated by winterflow from x-winterflow at every deploy. Do not edit.
services:
  app:
    environment:
      BASE_URL: ${BASE_URL}
      SESSION_SECRET: ${SESSION_SECRET}
      LANGUAGE: ${LANGUAGE}
      DATABASE_URL: postgres://notes:${DB_PASSWORD}@db:5432/notes
    ports:
      - "127.0.0.1:20013:8080"
  db:
    environment:
      LANGUAGE: ${LANGUAGE}
    ports:
      - "20014:5432/tcp"
````

Create `internal/domain/compose/xwinterflow/testdata/render/variables.yml`:

````yaml
# Generated by winterflow from x-winterflow at every deploy. Do not edit.
services:
  app:
    environment:
      LANGUAGE: ${LANGUAGE}
  db:
    environment:
      LANGUAGE: ${LANGUAGE}
````

Create `internal/domain/compose/xwinterflow/testdata/render/expose.yml`:

````yaml
# Generated by winterflow from x-winterflow at every deploy. Do not edit.
services:
  dns:
    ports:
      - "5353:53/tcp"
      - "5353:53/udp"
  web:
    ports:
      - "127.0.0.1:20000:80"
````

Create `internal/domain/compose/xwinterflow/testdata/render/overrides.yml`:

````yaml
# Generated by winterflow from x-winterflow at every deploy. Do not edit.
services:
  app:
    environment:
      MODE: prod
      PASSTHROUGH:
    command: !override ["serve", "--port", "8080"]
    entrypoint: !override ["serve", "--port", "8080"]
    ports: !override ["127.0.0.1:9000:9000", "127.0.0.1:20001:8080"]
    healthcheck:
      test: !override ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
    labels:
      app.id: ${WF_APP_ID}
````

- [ ] **Step 2: Run it, expect FAIL**

Run: `go test ./internal/domain/compose/xwinterflow/ -run TestRender`
Expected: FAIL to build: `undefined: Render`.

- [ ] **Step 3: Implement**

Create `internal/domain/compose/xwinterflow/render.go`:

````go
package xwinterflow

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// OverrideFile is where the agent writes Render's output, relative to the
// app folder: gitignored, regenerated at every deploy, passed as the last -f.
const OverrideFile = ".winterflow/compose.winterflow.yml"

// Render is the compose override file that applies spec: every declared
// variable with a value (hasValue) sets its environment key to ${NAME} in
// each service that has the key; the overrides are merged in, every list
// under !override so it replaces the body's (an environment list becomes a
// mapping instead, so it merges like the body's); each expose entry adds its
// host port (hostPort) to the service's ports, on 127.0.0.1 for http and on
// every interface for tcp/udp. The file holds ${NAME} references, never
// values: compose interpolates them from the env files it already loads.
// nil when there is nothing to apply.
func Render(spec Spec, hasValue func(name string) bool, hostPort func(e Expose) int) ([]byte, error) {
	services := map[string]*yaml.Node{}
	service := func(name string) *yaml.Node {
		if services[name] == nil {
			services[name] = mapping()
		}
		return services[name]
	}
	for _, v := range spec.Variables {
		if !hasValue(v.Name) {
			continue
		}
		for _, svc := range v.Services {
			setKey(child(service(svc), "environment"), v.Name, scalar("${"+v.Name+"}"))
		}
	}
	for name, body := range spec.Overrides {
		out := service(name)
		for i := 0; i+1 < len(body.Content); i += 2 {
			key, val := body.Content[i].Value, body.Content[i+1]
			if key == "environment" {
				env := child(out, "environment")
				for _, kv := range envPairs(val) {
					setKey(env, kv.name, kv.value)
				}
				continue
			}
			setKey(out, key, overridden(val))
		}
	}
	for _, e := range spec.Expose {
		host := hostPort(e)
		if host == 0 {
			return nil, fmt.Errorf("%s has no host port yet: save the app to allocate one", e.ID())
		}
		svc := service(e.Container)
		ports := value(svc, "ports")
		if ports == nil {
			ports = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			setKey(svc, "ports", ports)
		}
		ports.Content = append(ports.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: publish(e, host), Style: yaml.DoubleQuotedStyle})
	}
	if len(services) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	root, svcs := mapping(), mapping()
	setKey(root, "services", svcs)
	for _, n := range names {
		setKey(svcs, n, services[n])
	}
	var buf bytes.Buffer
	buf.WriteString("# Generated by winterflow from x-winterflow at every deploy. Do not edit.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// publish is the ports: entry that publishes e on host.
func publish(e Expose, host int) string {
	if e.Protocol == ProtocolHTTP {
		return fmt.Sprintf("127.0.0.1:%d:%d", host, e.Port)
	}
	return fmt.Sprintf("%d:%d/%s", host, e.Port, e.Protocol)
}

// child is the mapping under key in m, created when absent.
func child(m *yaml.Node, key string) *yaml.Node {
	if c := value(m, key); c != nil && c.Kind == yaml.MappingNode {
		return c
	}
	c := mapping()
	setKey(m, key, c)
	return c
}

// envPairs are an environment's entries: a mapping's pairs, or a list's
// NAME=value items (a bare NAME has no value, as in compose: it is taken
// from the environment).
func envPairs(env *yaml.Node) []envPair {
	var out []envPair
	switch env.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(env.Content); i += 2 {
			out = append(out, envPair{env.Content[i].Value, overridden(env.Content[i+1])})
		}
	case yaml.SequenceNode:
		for _, item := range env.Content {
			name, val, ok := strings.Cut(item.Value, "=")
			v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
			if ok {
				v = scalar(val)
			}
			out = append(out, envPair{name, v})
		}
	}
	return out
}

// envPair is one environment entry.
type envPair struct {
	name  string
	value *yaml.Node
}

// overridden copies n with every list tagged !override, so compose
// replaces the body's list instead of appending to it. Aliases are
// resolved: the copy stands alone.
func overridden(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return overridden(n.Alias)
	}
	c := *n
	c.Anchor = ""
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, k := range n.Content {
		c.Content[i] = overridden(k)
	}
	if c.Kind == yaml.SequenceNode {
		c.Tag = "!override"
	}
	return &c
}
````

- [ ] **Step 4: Run, expect PASS**

Run: `go test ./internal/domain/compose/xwinterflow/` (do not pass `-update`: the golden files above are the expected output).
Expected: `ok`. If a golden differs, fix `Render`, not the golden.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/compose/xwinterflow/render.go internal/domain/compose/xwinterflow/render_test.go internal/domain/compose/xwinterflow/testdata/
git commit -m "feat(xwinterflow): render the compose override file" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: Ports model, ingress expose targets and BuildConfig

**Files:**
- Create: `internal/domain/model/ports.go`, `internal/domain/compose/xwinterflow/targets.go`
- Modify: `internal/domain/model/ingress.go` (`IngressDomain.Expose`, exactly-one rule), `internal/infra/ingress/caddy/config.go` (`AppIngress.Ports`, expose resolution), `internal/infra/ingress/caddy/manager.go` (`readApps` reads `ports`), `api/openapi.yaml` (regenerated)
- Test: `internal/domain/model/ingress_test.go`, `internal/domain/model/ports_test.go`, `internal/domain/compose/xwinterflow/targets_test.go`, `internal/infra/ingress/caddy/expose_test.go`

**Interfaces:**
- Consumes: Task 1's `Spec.HTTPTargets`, `Expose.ID`.
- Produces:
  - `model.PortsKey = "ports"`; `type PortAllocation struct { Host int; Pinned bool }` (JSON `host`, `pinned`, both always written); `model.ParsePorts(config []byte) (ports map[string]PortAllocation, present bool, err error)` (host outside 1-65535 or bad JSON -> `model.ErrPortsInvalid`, a 400); `model.HTTPPorts(map[string]PortAllocation) map[string]int` (`"app:8080/http"` -> `"app:8080"`); `model.ValidExposeTarget(string) bool`.
  - `model.IngressDomain{Domain string; Expose string; UpstreamPort int; SSL bool}` (JSON `domain`, `expose,omitempty`, `upstream_port,omitempty`, `ssl`); `Ingress.Validate` refuses both or neither.
  - `(Spec) CheckTargets(ing *model.Ingress) error` (`model.ErrIngressInvalid`), `(Spec) CheckPins(ports map[string]model.PortAllocation) error` (`model.ErrPortsInvalid`; unpinned entries ignored).
  - `caddy.AppIngress.Ports map[string]int`; `BuildConfig` proxies an expose target to `127.0.0.1:<Ports[target]>` and skips (warning) one without a port.

- [ ] **Step 1: Write the failing tests**

Patch `internal/domain/model/ingress_test.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/domain/model/ingress_test.go b/internal/domain/model/ingress_test.go
index b7f720c..14eadfd 100644
--- a/internal/domain/model/ingress_test.go
+++ b/internal/domain/model/ingress_test.go
@@ -1,6 +1,7 @@
 package model
 
 import (
+	"encoding/json"
 	"strings"
 	"testing"
 )
@@ -41,6 +42,10 @@ func TestValidateRejects(t *testing.T) {
 		{"port zero", func(i *Ingress) { i.Domains[0].UpstreamPort = 0 }, "port"},
 		{"port too big", func(i *Ingress) { i.Domains[0].UpstreamPort = 70000 }, "port"},
 		{"dup within app", func(i *Ingress) { i.Domains[1].Domain = i.Domains[0].Domain }, "duplicate"},
+		{"expose and port", func(i *Ingress) { i.Domains[0].Expose = "app:8080" }, "not both"},
+		{"expose without port", func(i *Ingress) { i.Domains[0].UpstreamPort, i.Domains[0].Expose = 0, "app" }, "not <service>:<port>"},
+		{"expose port too big", func(i *Ingress) { i.Domains[0].UpstreamPort, i.Domains[0].Expose = 0, "app:70000" }, "not <service>:<port>"},
+		{"expose bad service", func(i *Ingress) { i.Domains[0].UpstreamPort, i.Domains[0].Expose = 0, "-app:80" }, "not <service>:<port>"},
 		{"dup route vs redirect", func(i *Ingress) { i.Redirects[0].Domain = i.Domains[0].Domain }, "duplicate"},
 		{"relative target", func(i *Ingress) { i.Redirects[0].To = "/nowhere" }, "absolute"},
 		{"ftp target", func(i *Ingress) { i.Redirects[0].To = "ftp://x.com" }, "absolute"},
@@ -69,6 +74,19 @@ func TestValidateRejects(t *testing.T) {
 	}
 }
 
+// A hostname may point at an http expose entry instead of a port.
+func TestValidateAcceptsExposeTarget(t *testing.T) {
+	ing := validIngress()
+	ing.Domains[0].UpstreamPort, ing.Domains[0].Expose = 0, "app:8080"
+	if err := ing.Validate(); err != nil {
+		t.Fatal(err)
+	}
+	raw, _ := json.Marshal(ing.Domains[0])
+	if string(raw) != `{"domain":"blog.example.com","expose":"app:8080","ssl":true}` {
+		t.Fatalf("json = %s", raw)
+	}
+}
+
 func TestValidateAcceptsSingleLabelHost(t *testing.T) {
 	// Dev setups use hosts like "localhost"; ACME won't issue for them, but
 	// ssl:false single labels are legitimate.
````

Create `internal/domain/model/ports_test.go`:

````go
package model

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParsePorts(t *testing.T) {
	ports, present, err := ParsePorts([]byte(`{"ports":{"app:8080/http":{"host":20013,"pinned":false},"db:5432/tcp":{"host":5432,"pinned":true}}}`))
	if err != nil || !present {
		t.Fatalf("present=%v err=%v", present, err)
	}
	want := map[string]PortAllocation{"app:8080/http": {Host: 20013}, "db:5432/tcp": {Host: 5432, Pinned: true}}
	if !reflect.DeepEqual(ports, want) {
		t.Fatalf("ports = %+v", ports)
	}
	if got := HTTPPorts(ports); !reflect.DeepEqual(got, map[string]int{"app:8080": 20013}) {
		t.Fatalf("http = %v", got)
	}
	for _, blob := range []string{`{}`, `{"ports":null}`} {
		if ports, present, err := ParsePorts([]byte(blob)); err != nil || present || len(ports) != 0 {
			t.Fatalf("%s: %v %v %v", blob, ports, present, err)
		}
	}
	for blob, want := range map[string]string{
		`{"ports":{"app:80/http":{"host":0}}}`:     "app:80/http: 0 is not a port",
		`{"ports":{"app:80/http":{"host":70000}}}`: "70000 is not a port",
		`{"ports":[1]}`: "cannot unmarshal",
		`not json`:      "invalid character",
	} {
		if _, _, err := ParsePorts([]byte(blob)); !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", blob, err)
		}
	}
}
````

Create `internal/domain/compose/xwinterflow/targets_test.go`:

````go
package xwinterflow

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

func TestCheckTargets(t *testing.T) {
	s := parse(t, ModeApp, notes)
	ok := &model.Ingress{Domains: []model.IngressDomain{{Domain: "notes.example.com", Expose: "app:8080"}, {Domain: "old.example.com", UpstreamPort: 9000}}}
	if err := s.CheckTargets(ok); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckTargets(nil); err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{
		"db:5432":  "db:5432 is not an http expose entry",
		"app:9999": "app:9999 is not an http expose entry",
	} {
		ing := &model.Ingress{Domains: []model.IngressDomain{{Domain: "notes.example.com", Expose: target}}}
		if err := s.CheckTargets(ing); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: err = %v", target, err)
		}
	}
}

func TestCheckPins(t *testing.T) {
	s := parse(t, ModeApp, notes)
	if err := s.CheckPins(map[string]model.PortAllocation{"db:5432/tcp": {Host: 5432, Pinned: true}, "gone:1/http": {Host: 1}}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]map[string]model.PortAllocation{
		"web:80/http is not an expose entry":                    {"web:80/http": {Host: 8080, Pinned: true}},
		"app:8080/http and db:5432/tcp are both pinned to port": {"app:8080/http": {Host: 5432, Pinned: true}, "db:5432/tcp": {Host: 5432, Pinned: true}},
	}
	for want, pins := range cases {
		if err := s.CheckPins(pins); !errors.Is(err, model.ErrPortsInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %q", err, want)
		}
	}
}
````

Create `internal/infra/ingress/caddy/expose_test.go`:

````go
package caddy

import (
	"strings"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/ingress/domains"
)

// An expose target proxies to the host port allocated for it; one without
// an allocation is skipped with a warning, never routed to port 0.
func TestBuildConfigResolvesExposeTargets(t *testing.T) {
	apps := []AppIngress{{
		AppID: "notes",
		Ingress: model.Ingress{Domains: []model.IngressDomain{
			{Domain: "notes.example.com", Expose: "app:8080", SSL: true},
			{Domain: "admin.example.com", Expose: "admin:9000", SSL: true},
		}},
		Ports: map[string]int{"app:8080": 20013},
	}}
	raw, warnings, err := BuildConfig(apps, []domains.Registration{reg("*.example.com", model.DomainTLSAuto)}, opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "app notes: domain admin.example.com: admin:9000 has no host port yet, skipping") {
		t.Fatalf("warnings = %v", warnings)
	}
	cfg := string(raw)
	if !strings.Contains(cfg, `"dial":"127.0.0.1:20013"`) || strings.Contains(cfg, `127.0.0.1:0"`) {
		t.Fatalf("config = %s", cfg)
	}
}

// The manager reads each app's allocations from the same config.json.
func TestReadAppsCarriesHTTPPorts(t *testing.T) {
	m, dataDir := testManager(t, false)
	writeApp(t, dataDir, "notes", `{"name":"Notes","ingress":{"domains":[{"domain":"notes.example.com","expose":"app:8080","ssl":true}]},"ports":{"app:8080/http":{"host":20013,"pinned":false},"db:5432/tcp":{"host":20014,"pinned":false}}}`)
	writeApp(t, dataDir, "broken", `{"ingress":{"domains":[]},"ports":{"x:1/http":{"host":0}}}`)
	apps, warnings, err := m.readApps()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]AppIngress{}
	for _, a := range apps {
		byID[a.AppID] = a
	}
	if p := byID["notes"].Ports; len(p) != 1 || p["app:8080"] != 20013 {
		t.Fatalf("ports = %v", p)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "app broken: unreadable ports") {
		t.Fatalf("warnings = %v", warnings)
	}
}
````

- [ ] **Step 2: Run them, expect FAIL**

Run: `go test ./internal/domain/model/ ./internal/domain/compose/xwinterflow/ ./internal/infra/ingress/caddy/`
Expected: FAIL to build: `unknown field Expose in struct literal of type IngressDomain`, `undefined: ParsePorts`, `undefined: PortAllocation`, `s.CheckTargets undefined`, `unknown field Ports in struct literal of type AppIngress`.

- [ ] **Step 3: Implement**

Patch `internal/domain/model/ingress.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/domain/model/ingress.go b/internal/domain/model/ingress.go
index 3688790..c3b5b2d 100644
--- a/internal/domain/model/ingress.go
+++ b/internal/domain/model/ingress.go
@@ -5,6 +5,7 @@ import (
 	"fmt"
 	"net/url"
 	"regexp"
+	"strconv"
 	"strings"
 )
 
@@ -23,18 +24,35 @@ type Ingress struct {
 	Redirects []IngressRedirect `json:"redirects"`
 }
 
-// IngressDomain maps a hostname to a host port the app's compose file
-// publishes (ideally bound to 127.0.0.1). SSL no longer decides anything for
-// the proxy: TLS follows the registered domain covering the hostname (see
-// RegisteredDomain). It still loads from old configs, picks the auto/off
-// mode when the agent first registers existing hostnames, and picks the
-// link scheme on app cards.
+// IngressDomain maps a hostname to the app: Expose names one of its http
+// expose entries (<service>:<port> of x-winterflow.expose, proxied to the
+// host port the agent allocated for it), UpstreamPort a host port the
+// compose file publishes itself (ideally bound to 127.0.0.1). Exactly one
+// is set. SSL no longer decides anything for the proxy: TLS follows the
+// registered domain covering the hostname (see RegisteredDomain). It still
+// picks the auto/off mode when the agent first registers existing
+// hostnames, and the link scheme on app cards.
 type IngressDomain struct {
 	Domain       string `json:"domain"`
-	UpstreamPort int    `json:"upstream_port"`
+	Expose       string `json:"expose,omitempty"`
+	UpstreamPort int    `json:"upstream_port,omitempty"`
 	SSL          bool   `json:"ssl"`
 }
 
+// exposeTargetRe is an expose target: a compose service name and a port.
+var exposeTargetRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*:([0-9]{1,5})$`)
+
+// ValidExposeTarget reports whether s is <service>:<port> with a port from
+// 1 to 65535.
+func ValidExposeTarget(s string) bool {
+	m := exposeTargetRe.FindStringSubmatch(s)
+	if m == nil {
+		return false
+	}
+	port, err := strconv.Atoi(m[1])
+	return err == nil && port >= 1 && port <= 65535
+}
+
 // IngressRedirect is either a domain-level redirect (Path == "", preserves the
 // request URI; its source hostname must be covered by a registered domain
 // like a route's, and SSL is legacy as on IngressDomain) or a path rule
@@ -81,7 +99,14 @@ func (i *Ingress) Validate() error {
 		if err := claim(d.Domain, "domain"); err != nil {
 			return err
 		}
-		if d.UpstreamPort < 1 || d.UpstreamPort > 65535 {
+		switch {
+		case d.Expose != "" && d.UpstreamPort != 0:
+			return fmt.Errorf("domain %q: set expose or upstream_port, not both", d.Domain)
+		case d.Expose != "":
+			if !ValidExposeTarget(d.Expose) {
+				return fmt.Errorf("domain %q: expose %q is not <service>:<port>", d.Domain, d.Expose)
+			}
+		case d.UpstreamPort < 1 || d.UpstreamPort > 65535:
 			return fmt.Errorf("domain %q: upstream port %d out of range 1-65535", d.Domain, d.UpstreamPort)
 		}
 	}
````

Create `internal/domain/model/ports.go`:

````go
package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PortsKey is the key of an app's committed config.json holding the host
// port of each x-winterflow expose entry, keyed by the entry's id
// (<container>:<port>/<protocol>).
const PortsKey = "ports"

// PortAllocation is the host port of one expose entry. Pinned ports are the
// user's choice; the agent allocated the others from EXPOSE_PORT_RANGE.
type PortAllocation struct {
	Host   int  `json:"host"`
	Pinned bool `json:"pinned"`
}

// ErrPortsInvalid marks a ports map that can't be used (a 400).
var ErrPortsInvalid = rejected("invalid ports")

// ParsePorts reads the ports key of an app config blob. present reports
// whether the blob has the key at all: a save without it keeps the stored
// pins (an editor that doesn't know about ports must not drop them).
func ParsePorts(config []byte) (ports map[string]PortAllocation, present bool, err error) {
	var probe struct {
		Ports *map[string]PortAllocation `json:"ports"`
	}
	if err := json.Unmarshal(config, &probe); err != nil {
		return nil, false, fmt.Errorf("%w: %v", ErrPortsInvalid, err)
	}
	if probe.Ports == nil {
		return map[string]PortAllocation{}, false, nil
	}
	for id, p := range *probe.Ports {
		if p.Host < 1 || p.Host > 65535 {
			return nil, true, fmt.Errorf("%w: %s: %d is not a port from 1 to 65535", ErrPortsInvalid, id, p.Host)
		}
	}
	return *probe.Ports, true, nil
}

// HTTPPorts maps each http entry's ingress target (<container>:<port>) to
// its host port.
func HTTPPorts(ports map[string]PortAllocation) map[string]int {
	out := map[string]int{}
	for id, p := range ports {
		if target, ok := strings.CutSuffix(id, "/http"); ok {
			out[target] = p.Host
		}
	}
	return out
}
````

Create `internal/domain/compose/xwinterflow/targets.go`:

````go
package xwinterflow

import (
	"fmt"
	"sort"

	"winterflow/internal/domain/model"
)

// CheckTargets refuses an ingress hostname whose expose target is not an
// http expose entry of the app (tcp/udp entries never take hostnames).
func (s Spec) CheckTargets(ing *model.Ingress) error {
	if ing == nil {
		return nil
	}
	targets := s.HTTPTargets()
	for _, d := range ing.Domains {
		if d.Expose != "" && !targets[d.Expose] {
			return fmt.Errorf("%w: domain %q: %s is not an http expose entry of this app", model.ErrIngressInvalid, d.Domain, d.Expose)
		}
	}
	return nil
}

// CheckPins refuses pins the app can't take: one for an id that is no
// expose entry of it, or two entries pinned to the same host port.
func (s Spec) CheckPins(ports map[string]model.PortAllocation) error {
	ids := map[string]bool{}
	for _, e := range s.Expose {
		ids[e.ID()] = true
	}
	keys := make([]string, 0, len(ports))
	for id := range ports {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	byHost := map[int]string{}
	for _, id := range keys {
		p := ports[id]
		if !p.Pinned {
			continue
		}
		if !ids[id] {
			return fmt.Errorf("%w: %s is not an expose entry of this app", model.ErrPortsInvalid, id)
		}
		if other, dup := byHost[p.Host]; dup {
			return fmt.Errorf("%w: %s and %s are both pinned to port %d", model.ErrPortsInvalid, other, id, p.Host)
		}
		byHost[p.Host] = id
	}
	return nil
}
````

Patch `internal/infra/ingress/caddy/config.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/ingress/caddy/config.go b/internal/infra/ingress/caddy/config.go
index 7187e46..24f42f6 100644
--- a/internal/infra/ingress/caddy/config.go
+++ b/internal/infra/ingress/caddy/config.go
@@ -18,11 +18,14 @@ import (
 )
 
 // AppIngress is one app's committed ingress plus its display name (from the
-// same config.json), which names the app in the domain delete refusal.
+// same config.json), which names the app in the domain delete refusal, and
+// the host ports of its http expose entries by target (<service>:<port>,
+// model.HTTPPorts of the same config.json's ports).
 type AppIngress struct {
 	AppID   string
 	Name    string
 	Ingress model.Ingress
+	Ports   map[string]int
 }
 
 // label is how messages name the app: its display name, else its id.
@@ -336,7 +339,16 @@ func BuildConfig(apps []AppIngress, regs []domains.Registration, opts Options) (
 			}
 		}
 		for _, d := range a.Ingress.Domains {
-			place(d.Domain, proxyRoute(d.Domain, d.UpstreamPort), true)
+			port := d.UpstreamPort
+			if d.Expose != "" {
+				host, ok := a.Ports[d.Expose]
+				if !ok {
+					warnings = append(warnings, fmt.Sprintf("app %s: domain %s: %s has no host port yet, skipping", a.AppID, d.Domain, d.Expose))
+					continue
+				}
+				port = host
+			}
+			place(d.Domain, proxyRoute(d.Domain, port), true)
 		}
 		for _, r := range a.Ingress.Redirects {
 			if r.Path == "" {
````

Patch `internal/infra/ingress/caddy/manager.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/ingress/caddy/manager.go b/internal/infra/ingress/caddy/manager.go
index b8b009a..efbe2c7 100644
--- a/internal/infra/ingress/caddy/manager.go
+++ b/internal/infra/ingress/caddy/manager.go
@@ -114,7 +114,11 @@ func (m *Manager) readApps() ([]AppIngress, []string, error) {
 			Name string `json:"name"`
 		}
 		_ = json.Unmarshal(raw, &meta) // ParseIngress already proved it is JSON
-		out = append(out, AppIngress{AppID: appID, Name: meta.Name, Ingress: *ing})
+		ports, _, err := model.ParsePorts(raw)
+		if err != nil {
+			warnings = append(warnings, "app "+appID+": unreadable ports, its expose targets are skipped: "+err.Error())
+		}
+		out = append(out, AppIngress{AppID: appID, Name: meta.Name, Ingress: *ing, Ports: model.HTTPPorts(ports)})
 	}
 	return out, warnings, nil
 }
````

Then regenerate the OpenAPI document (the `IngressDomain` schema gains `expose`, and `upstream_port` is no longer required):

```bash
make openapi
git diff --stat api/openapi.yaml   # expect: 1 file changed, 4 insertions(+), 1 deletion(-)
```

- [ ] **Step 4: Run, expect PASS**

Run: `go test ./internal/domain/model/ ./internal/domain/compose/xwinterflow/ ./internal/infra/ingress/caddy/ ./internal/app/web/ ./internal/domain/template/ ./internal/domain/usecase/...`
Expected: `ok` for each (`TestOpenAPIUpToDate` in `./internal/app/web/` passes with the regenerated file).

- [ ] **Step 5: Commit**

```bash
git add internal/domain/model/ingress.go internal/domain/model/ingress_test.go internal/domain/model/ports.go internal/domain/model/ports_test.go internal/domain/compose/xwinterflow/targets.go internal/domain/compose/xwinterflow/targets_test.go internal/infra/ingress/caddy/config.go internal/infra/ingress/caddy/manager.go internal/infra/ingress/caddy/expose_test.go api/openapi.yaml
git commit -m "feat: ingress hostnames can target an http expose entry" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 4: Host port allocation on the agent

**Files:**
- Modify: `pkg/config/config.go` (`GetExposePortRange`), `.env.example` (`EXPOSE_PORT_RANGE`, Agent extras section)
- Create: `internal/infra/orchestrator/docker_compose/ports.go`
- Modify: `internal/infra/orchestrator/docker_compose/repository.go` (fields `portMu`, `portIndex`, `probePort`; `forgetApp` calls `forgetPorts`; layout comment), `internal/infra/orchestrator/docker_compose/backupconfig.go` (`appBackupConfig.spec`, `appBackups` and `cleanBackups` parse with `xwinterflow.Parse`), `internal/infra/orchestrator/docker_compose/operations.go` (`save`, `rollbackWithoutDeploy`, `refreshSourceWithoutDeploy`)
- Test: `pkg/config/config_test.go`, `internal/infra/orchestrator/docker_compose/ports_test.go`

**Interfaces:**
- Consumes: `xwinterflow.Parse`, `Spec.Expose`, `Spec.CheckTargets`, `Expose.ID/Bind`, `model.ParsePorts`, `model.ParseIngress`, `model.PortAllocation`, `model.ErrPortsInvalid`.
- Produces:
  - `(*config.ServerConfig).GetExposePortRange() (low, high int)`; `config.DefaultExposePortLow = 20000`, `config.DefaultExposePortHigh = 29999`.
  - Agent: `ErrPortTaken` (matches `model.ErrRejected`); `(*Repository).appSpec(dir) (xwinterflow.Spec, error)`, `readPorts(dir) (map[string]model.PortAllocation, error)`, `pinnedOnly(map) map`, `syncPorts(appID, dir string, entries []xwinterflow.Expose, prev, pins map[string]model.PortAllocation) error`, `writePorts(dir, ports) error`, `forgetPorts(appID)`, `applyExpose(appID, dir string, config []byte, spec xwinterflow.Spec, prev, pins map[string]model.PortAllocation) error`, `restorePorts(appID, dir string) error`, `probeListen(bind string, port int, protocol string) error`; field `probePort` (tests replace it).
  - `appBackupConfig.spec xwinterflow.Spec` (filled by `appBackups`); `cleanBackups(dir) (xwinterflow.Spec, []string, error)` (warnings include `spec.Warnings`).

- [ ] **Step 1: Write the failing tests**

Patch `pkg/config/config_test.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/pkg/config/config_test.go b/pkg/config/config_test.go
index cd2d406..7676a04 100644
--- a/pkg/config/config_test.go
+++ b/pkg/config/config_test.go
@@ -191,6 +191,23 @@ func TestGetBackupRetentionDays(t *testing.T) {
 	}
 }
 
+func TestGetExposePortRange(t *testing.T) {
+	c := NewServerConfig("standalone")
+	for v, want := range map[string][2]int{
+		"": {20000, 29999}, "30000-30099": {30000, 30099}, " 1 - 65535 ": {1, 65535}, "5000-5000": {5000, 5000},
+		"30000": {20000, 29999}, "a-b": {20000, 29999}, "0-10": {20000, 29999}, "10-70000": {20000, 29999}, "300-200": {20000, 29999},
+	} {
+		t.Setenv("EXPOSE_PORT_RANGE", v)
+		if lo, hi := c.GetExposePortRange(); lo != want[0] || hi != want[1] {
+			t.Errorf("EXPOSE_PORT_RANGE=%q: %d-%d, want %d-%d", v, lo, hi, want[0], want[1])
+		}
+	}
+	unsetenv(t, "EXPOSE_PORT_RANGE")
+	if lo, hi := c.GetExposePortRange(); lo != 20000 || hi != 29999 {
+		t.Fatalf("unset: %d-%d", lo, hi)
+	}
+}
+
 func TestCertPathsDeriveFromCertDir(t *testing.T) {
 	c := NewServerConfig("distributed")
 	t.Setenv("HUB_CERT_DIR", "/etc/wf/certs")
````

Create `internal/infra/orchestrator/docker_compose/ports_test.go`:

````go
package dockercompose

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

const exposeCompose = `services:
  app:
    image: nginx
  db:
    image: postgres
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
`

// exposePayload is an app with exposeCompose (or compose) and extra config
// keys (a JSON object's inner part, e.g. `"ports":{}`).
func exposePayload(appID, compose, extra string) command.AppPayload {
	cfg := `{"name":"` + appID + `","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[]`
	if extra != "" {
		cfg += "," + extra
	}
	return command.AppPayload{
		AppID:  appID,
		Config: []byte(cfg + "}"),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
	}
}

// portRepo is a test repository allocating from 31000-31009 with a probe
// that refuses the ports in busy (and records what it was asked).
func portRepo(t *testing.T, busy ...int) (*Repository, *[]int) {
	t.Helper()
	t.Setenv("EXPOSE_PORT_RANGE", "31000-31009")
	r := newTestRepo(t)
	var mu sync.Mutex
	var probed []int
	r.probePort = func(_ string, port int, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		probed = append(probed, port)
		for _, b := range busy {
			if b == port {
				return errors.New("address already in use")
			}
		}
		return nil
	}
	return r, &probed
}

func portsOf(t *testing.T, r *Repository, appID string) map[string]model.PortAllocation {
	t.Helper()
	ports, err := r.readPorts(r.appDataDir(appID))
	if err != nil {
		t.Fatal(err)
	}
	return ports
}

func TestSavedExposeEntriesGetPortsFromTheRange(t *testing.T) {
	r, _ := portRepo(t, 31000)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	want := map[string]model.PortAllocation{"app:8080/http": {Host: 31001}, "db:5432/tcp": {Host: 31002}}
	if got := portsOf(t, r, "app-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ports = %+v", got)
	}
	// Committed: redeploys and rollbacks keep them.
	head, _ := gitHead(r.appDataDir("app-1"))
	if err := gitRestore(r.appDataDir("app-1"), head); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("committed ports = %+v", got)
	}
}

// A redeploy keeps the app's ports and never probes them: its own
// containers hold them.
func TestSaveKeepsPortsWithoutProbingThem(t *testing.T) {
	r, probed := portRepo(t)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	first := portsOf(t, r, "app-1")
	*probed = nil
	r.probePort = func(string, int, string) error { return errors.New("held by the app's own container") }
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); !reflect.DeepEqual(got, first) {
		t.Fatalf("ports moved: %+v -> %+v", first, got)
	}
}

func TestPortsDoNotCollideBetweenApps(t *testing.T) {
	r, _ := portRepo(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.saveWithoutDeploy(exposePayload(fmt.Sprintf("app-%d", i), exposeCompose, ""))
		}()
	}
	wg.Wait()
	seen := map[int]string{}
	for i := range 4 {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		for id, p := range portsOf(t, r, fmt.Sprintf("app-%d", i)) {
			if other, dup := seen[p.Host]; dup {
				t.Fatalf("port %d given to %s and app-%d %s", p.Host, other, i, id)
			}
			seen[p.Host] = fmt.Sprintf("app-%d %s", i, id)
		}
	}
	// A restarted agent rebuilds the index from disk.
	fresh := NewRepository(r.cfg, r.log)
	fresh.probePort = r.probePort
	if _, err := fresh.saveWithoutDeploy(exposePayload("app-9", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	for _, p := range portsOf(t, fresh, "app-9") {
		if owner, dup := seen[p.Host]; dup {
			t.Fatalf("port %d reused after a restart, held by %s", p.Host, owner)
		}
	}
}

func TestRangeExhausted(t *testing.T) {
	r, _ := portRepo(t, 31000, 31001, 31002, 31003, 31004, 31005, 31006, 31007, 31008)
	_, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ""))
	if err == nil || !strings.Contains(err.Error(), "db:5432/tcp: no free port left in EXPOSE_PORT_RANGE 31000-31009") {
		t.Fatalf("err = %v", err)
	}
	if r.appExists("app-1") {
		t.Fatal("a refused first save left the app behind")
	}
}

func TestPinnedPorts(t *testing.T) {
	r, _ := portRepo(t, 6000)
	pinned := `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, pinned)); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1")["db:5432/tcp"]; got != (model.PortAllocation{Host: 5432, Pinned: true}) {
		t.Fatalf("pin = %+v", got)
	}
	// A save without a ports key (Settings saving a colour) keeps the pin.
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if !portsOf(t, r, "app-1")["db:5432/tcp"].Pinned {
		t.Fatal("a save without ports dropped the pin")
	}
	// Another app can't pin it, nor take it from the range.
	_, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, pinned))
	if !errors.Is(err, ErrPortTaken) || !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), "port 5432 is used by the app app-1") {
		t.Fatalf("pin held by another app: %v", err)
	}
	// A pin the probe finds busy refuses the save and keeps the last commit.
	head, _ := gitHead(r.appDataDir("app-1"))
	_, err = r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"db:5432/tcp":{"host":6000,"pinned":true}}`))
	if !errors.Is(err, ErrPortTaken) || !strings.Contains(err.Error(), "db:5432/tcp: port 6000 is busy on this server") {
		t.Fatalf("busy pin: %v", err)
	}
	if now, _ := gitHead(r.appDataDir("app-1")); now != head || portsOf(t, r, "app-1")["db:5432/tcp"].Host != 5432 {
		t.Fatal("a refused save changed the app")
	}
	// Unpinning keeps the port.
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{}`)); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1")["db:5432/tcp"]; got != (model.PortAllocation{Host: 5432}) {
		t.Fatalf("unpinned = %+v", got)
	}
	// Two entries can't share a pin.
	_, err = r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"app:8080/http":{"host":7000,"pinned":true},"db:5432/tcp":{"host":7000,"pinned":true}}`))
	if !errors.Is(err, model.ErrPortsInvalid) {
		t.Fatalf("shared pin: %v", err)
	}
	// A malformed ports key is refused before anything is written.
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"db:5432/tcp":{"host":0,"pinned":true}}`)); !errors.Is(err, model.ErrPortsInvalid) {
		t.Fatalf("malformed: %v", err)
	}
}

// Entries the compose file drops lose their port; a rollback brings the
// revision's ports back, or new ones where another app took them since.
func TestRemovedEntriesAndRollback(t *testing.T) {
	r, _ := portRepo(t)
	v1, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); len(got) != 0 {
		t.Fatalf("after removing the entries: %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), configRel))
	if strings.Contains(string(raw), `"ports"`) {
		t.Fatalf("config keeps an empty ports key: %s", raw)
	}
	if _, err := r.rollbackWithoutDeploy("app-1", v1); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-1"); got["app:8080/http"].Host != 31000 || got["db:5432/tcp"].Host != 31001 {
		t.Fatalf("after rollback: %+v", got)
	}
	// Another app takes 31000 while app-1 is rolled forward and back again.
	v3, _ := gitHead(r.appDataDir("app-1"))
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rollbackWithoutDeploy("app-1", v3); err != nil {
		t.Fatal(err)
	}
	mine, theirs := portsOf(t, r, "app-1"), portsOf(t, r, "app-2")
	if mine["app:8080/http"].Host == theirs["app:8080/http"].Host || mine["app:8080/http"].Host == 0 {
		t.Fatalf("rollback took app-2's port: %+v vs %+v", mine, theirs)
	}
}

// A rollback to a revision whose pin another app holds is refused and
// leaves the app as it was.
func TestRollbackRefusedByAPinAnotherAppHolds(t *testing.T) {
	r, _ := portRepo(t)
	v1, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`)); err != nil {
		t.Fatal(err)
	}
	head, _ := gitHead(r.appDataDir("app-1"))
	if _, err := r.rollbackWithoutDeploy("app-1", v1); !errors.Is(err, ErrPortTaken) {
		t.Fatalf("err = %v", err)
	}
	if now, _ := gitHead(r.appDataDir("app-1")); now != head {
		t.Fatal("a refused rollback committed")
	}
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), "compose.yml"))
	if strings.Contains(string(raw), "x-winterflow") {
		t.Fatalf("worktree left at the refused revision:\n%s", raw)
	}
}

func TestForgetAppFreesItsPorts(t *testing.T) {
	r, _ := portRepo(t)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(r.appDataDir("app-1")); err != nil {
		t.Fatal(err)
	}
	r.forgetApp("app-1")
	if _, err := r.saveWithoutDeploy(exposePayload("app-2", exposeCompose, "")); err != nil {
		t.Fatal(err)
	}
	if got := portsOf(t, r, "app-2")["app:8080/http"].Host; got != 31000 {
		t.Fatalf("app-2 got %d, want the freed 31000", got)
	}
}

// An ingress hostname may only point at an http expose entry of the app.
func TestSaveRefusesANonHTTPExposeTarget(t *testing.T) {
	r, _ := portRepo(t)
	ing := `"ingress":{"domains":[{"domain":"db.example.com","expose":"db:5432","ssl":true}],"redirects":[]}`
	_, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ing))
	if !errors.Is(err, model.ErrIngressInvalid) || !strings.Contains(err.Error(), "db:5432 is not an http expose entry") {
		t.Fatalf("err = %v", err)
	}
	ok := `"ingress":{"domains":[{"domain":"app.example.com","expose":"app:8080","ssl":true}],"redirects":[]}`
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", exposeCompose, ok)); err != nil {
		t.Fatal(err)
	}
}

// An x-winterflow block the format refuses refuses the save.
func TestSaveRefusesAnInvalidBlock(t *testing.T) {
	r, _ := portRepo(t)
	_, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: a }\nx-winterflow:\n  volumes: {}\n", ""))
	if err == nil || !strings.Contains(err.Error(), "x-winterflow.volumes: unknown key") || r.appExists("app-1") {
		t.Fatalf("err = %v", err)
	}
}

// A git-sourced app's upstream gaining an expose entry gets its port with
// the source update's commit.
func TestSourceUpdateAllocatesPorts(t *testing.T) {
	r, _ := portRepo(t)
	up, commit := newUpstream(t)
	commit("compose.yml", "services:\n  app: { image: nginx }\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "")); err != nil {
		t.Fatal(err)
	}
	commit("compose.yml", exposeCompose)
	changed, _, err := r.refreshSourceWithoutDeploy("git-app")
	if err != nil || !changed {
		t.Fatalf("refresh = %v %v", changed, err)
	}
	if got := portsOf(t, r, "git-app"); got["app:8080/http"].Host != 31000 {
		t.Fatalf("ports = %+v", got)
	}
	var cfg map[string]json.RawMessage
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("git-app"), configRel))
	if json.Unmarshal(raw, &cfg) != nil || cfg["ports"] == nil {
		t.Fatalf("config = %s", raw)
	}
}

func TestProbeListen(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := probeListen("127.0.0.1", port, "http"); err == nil {
		t.Fatal("a held tcp port probed free")
	}
	l.Close()
	if err := probeListen("127.0.0.1", port, "tcp"); err != nil {
		t.Fatalf("a free port probed busy: %v", err)
	}
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := probeListen("127.0.0.1", c.LocalAddr().(*net.UDPAddr).Port, "udp"); err == nil {
		t.Fatal("a held udp port probed free")
	}
}
````

- [ ] **Step 2: Run them, expect FAIL**

Run: `go test ./pkg/config/ ./internal/infra/orchestrator/docker_compose/ -run 'ExposePortRange|Ports|Pinned|Rollback|Forget|Expose|InvalidBlock|SourceUpdate|ProbeListen|Removed|Range'`
Expected: FAIL to build: `c.GetExposePortRange undefined`, `r.probePort undefined`, `r.readPorts undefined`, `undefined: ErrPortTaken`, `undefined: probeListen`.

- [ ] **Step 3: Implement**

Patch `pkg/config/config.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/pkg/config/config.go b/pkg/config/config.go
index 44ea78b..1c6015e 100644
--- a/pkg/config/config.go
+++ b/pkg/config/config.go
@@ -174,6 +174,26 @@ func (c *ServerConfig) GetBackupRetentionDays() int {
 	return 180
 }
 
+// Default EXPOSE_PORT_RANGE bounds.
+const (
+	DefaultExposePortLow  = 20000
+	DefaultExposePortHigh = 29999
+)
+
+// GetExposePortRange is the range the agent allocates host ports for
+// x-winterflow expose entries from (EXPOSE_PORT_RANGE, "low-high", default
+// 20000-29999). A malformed value, one outside 1-65535 or with low above
+// high, is the default. Read by standalone and agent.
+func (c *ServerConfig) GetExposePortRange() (low, high int) {
+	lo, hi, ok := strings.Cut(os.Getenv("EXPOSE_PORT_RANGE"), "-")
+	l, errL := strconv.Atoi(strings.TrimSpace(lo))
+	h, errH := strconv.Atoi(strings.TrimSpace(hi))
+	if !ok || errL != nil || errH != nil || l < 1 || h > 65535 || l > h {
+		return DefaultExposePortLow, DefaultExposePortHigh
+	}
+	return l, h
+}
+
 // GetAppsDir holds the rendered, ready-to-run deployments:
 // {dataDir}/apps/{appID}/.
 func (c *ServerConfig) GetAppsDir() string {
````

Patch `.env.example` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/.env.example b/.env.example
index 2243baf..15eb6ea 100644
--- a/.env.example
+++ b/.env.example
@@ -137,6 +137,14 @@ HUB_CERT_EXT_PATH=data/ext.cnf
 # (larger values are capped). Default 180.
 #BACKUP_RETENTION_DAYS=180
 
+# Host ports the agent allocates for x-winterflow expose entries
+# (standalone/agent), "low-high". An http entry is published on 127.0.0.1 (the
+# ingress proxies to it), a tcp/udp entry on every interface. Allocations are
+# stored in each app's .winterflow/config.json and kept across redeploys and
+# rollbacks; a port the user pins may be outside the range. Default
+# 20000-29999.
+#EXPOSE_PORT_RANGE=20000-29999
+
 # --- Monitoring -----------------------------------------------------------------
 
 # Resource metrics. Agent and standalone: sample host and container CPU,
````

Create `internal/infra/orchestrator/docker_compose/ports.go`:

````go
package dockercompose

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
	"winterflow/pkg/fsutil"
)

// ErrPortTaken marks a pinned host port another app holds, or one the
// listen probe finds busy: the save is refused (a 400 when the API sees it
// first).
var ErrPortTaken = model.Rejected("port taken")

// portOwner is the app and expose entry holding a host port.
type portOwner struct {
	appID string
	entry string
}

// appSpec parses the x-winterflow block of the compose files the app in
// dir runs (a git-sourced app's included).
func (r *Repository) appSpec(dir string) (xwinterflow.Spec, error) {
	contents, err := composeContents(dir, r.appComposeFiles(dir))
	if err != nil {
		return xwinterflow.Spec{}, fmt.Errorf("read compose files: %w", err)
	}
	return xwinterflow.Parse(xwinterflow.ModeApp, contents...)
}

// readPorts is the ports key of the app's config.json (none without one).
func (r *Repository) readPorts(dir string) (map[string]model.PortAllocation, error) {
	raw, err := os.ReadFile(filepath.Join(dir, configRel))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]model.PortAllocation{}, nil
	}
	if err != nil {
		return nil, err
	}
	ports, _, err := model.ParsePorts(raw)
	return ports, err
}

// pinnedOnly is the pinned subset of ports.
func pinnedOnly(ports map[string]model.PortAllocation) map[string]model.PortAllocation {
	out := map[string]model.PortAllocation{}
	for id, p := range ports {
		if p.Pinned {
			out[id] = p
		}
	}
	return out
}

// loadPortIndexLocked builds the index of every app's host ports from the
// committed config.json files, once per process (file reads, no exec).
// The caller holds portMu.
func (r *Repository) loadPortIndexLocked() {
	if r.portIndex != nil {
		return
	}
	r.portIndex = map[int]portOwner{}
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if err != nil {
		return // no apps yet; an unreadable folder leaves the probe as the guard
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ports, err := r.readPorts(filepath.Join(r.cfg.GetAppsDataDir(), e.Name()))
		if err != nil {
			r.log.Warn("read app ports", "app_id", e.Name(), "error", err)
			continue
		}
		for id, p := range ports {
			r.portIndex[p.Host] = portOwner{appID: e.Name(), entry: id}
		}
	}
}

// syncPorts gives every expose entry of the app in dir its host port and
// stores them in config.json under ports (no key when there are none).
// Pins come first: refused when another app holds the port, or when the
// port is new to the app and the listen probe finds it busy. Then each
// entry keeps its previous port (prev: what the app had before this
// change, or what a rolled-back revision had) unless another app holds it
// now or, new to the app, the probe finds it busy. The rest get the first
// free port of EXPOSE_PORT_RANGE. Entries the compose file no longer has
// lose their port. One server-wide lock serializes allocations and keeps
// the index of every app's ports in step; ports the app already holds are
// never probed (its own containers hold them).
func (r *Repository) syncPorts(appID, dir string, entries []xwinterflow.Expose, prev, pins map[string]model.PortAllocation) error {
	r.portMu.Lock()
	defer r.portMu.Unlock()
	r.loadPortIndexLocked()
	owned := func(host int) bool { o, ok := r.portIndex[host]; return ok && o.appID == appID }
	holder := func(host int) (string, bool) { o, ok := r.portIndex[host]; return o.appID, ok && o.appID != appID }
	next := map[string]model.PortAllocation{}
	used := map[int]bool{}
	for _, e := range entries {
		pin, ok := pins[e.ID()]
		if !ok || !pin.Pinned {
			continue
		}
		if other, taken := holder(pin.Host); taken {
			return fmt.Errorf("%w: %s: port %d is used by the app %s", ErrPortTaken, e.ID(), pin.Host, r.appName(r.appDataDir(other), other))
		}
		if used[pin.Host] {
			return fmt.Errorf("%w: two entries are pinned to port %d", model.ErrPortsInvalid, pin.Host)
		}
		if !owned(pin.Host) {
			if err := r.probePort(e.Bind(), pin.Host, e.Protocol); err != nil {
				return fmt.Errorf("%w: %s: port %d is busy on this server: %v", ErrPortTaken, e.ID(), pin.Host, err)
			}
		}
		next[e.ID()] = model.PortAllocation{Host: pin.Host, Pinned: true}
		used[pin.Host] = true
	}
	for _, e := range entries {
		p, ok := prev[e.ID()]
		if _, done := next[e.ID()]; done || !ok || used[p.Host] {
			continue
		}
		if _, taken := holder(p.Host); taken {
			continue
		}
		if !owned(p.Host) && r.probePort(e.Bind(), p.Host, e.Protocol) != nil {
			continue
		}
		next[e.ID()] = model.PortAllocation{Host: p.Host}
		used[p.Host] = true
	}
	low, high := r.cfg.GetExposePortRange()
	for _, e := range entries {
		if _, done := next[e.ID()]; done {
			continue
		}
		host := 0
		for port := low; port <= high && host == 0; port++ {
			if _, held := r.portIndex[port]; held || used[port] {
				continue
			}
			if r.probePort(e.Bind(), port, e.Protocol) == nil {
				host = port
			}
		}
		if host == 0 {
			return fmt.Errorf("%s: no free port left in EXPOSE_PORT_RANGE %d-%d", e.ID(), low, high)
		}
		next[e.ID()] = model.PortAllocation{Host: host}
		used[host] = true
	}
	if err := r.writePorts(dir, next); err != nil {
		return err
	}
	for host, o := range r.portIndex {
		if o.appID == appID {
			delete(r.portIndex, host)
		}
	}
	for id, p := range next {
		r.portIndex[p.Host] = portOwner{appID: appID, entry: id}
	}
	return nil
}

// writePorts replaces the ports key of the app's config.json (none: the
// key is removed); every other key is kept, an unchanged file not rewritten.
func (r *Repository) writePorts(dir string, ports map[string]model.PortAllocation) error {
	cfg, raw, err := r.readAppConfig(dir)
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		if _, ok := cfg[model.PortsKey]; !ok {
			return nil
		}
		delete(cfg, model.PortsKey)
	} else {
		cfg[model.PortsKey] = ports
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if bytes.Equal(out, raw) {
		return nil
	}
	return fsutil.WriteFile(filepath.Join(dir, configRel), out, 0o644)
}

// forgetPorts drops an app's ports from the index (the app is deleted).
func (r *Repository) forgetPorts(appID string) {
	r.portMu.Lock()
	defer r.portMu.Unlock()
	for host, o := range r.portIndex {
		if o.appID == appID {
			delete(r.portIndex, host)
		}
	}
}

// probeListen reports whether port is free on bind by listening there for
// a moment: a program or a container's docker-proxy already holding it
// makes it fail.
func probeListen(bind string, port int, protocol string) error {
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	if protocol == xwinterflow.ProtocolUDP {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return err
		}
		return c.Close()
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.Close()
}

// applyExpose checks the ingress of a save against the app's expose
// entries (a hostname may only point at an http one) and syncs its ports.
func (r *Repository) applyExpose(appID, dir string, config []byte, spec xwinterflow.Spec, prev, pins map[string]model.PortAllocation) error {
	ing, err := model.ParseIngress(config)
	if err != nil {
		return fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
	}
	if err := spec.CheckTargets(ing); err != nil {
		return err
	}
	return r.syncPorts(appID, dir, spec.Expose, prev, pins)
}

// restorePorts re-syncs the ports of a revision a rollback just restored
// into dir. On a refusal the worktree goes back to HEAD (with source/ at
// HEAD's lock): nothing of the rollback is committed.
func (r *Repository) restorePorts(appID, dir string) error {
	spec, err := r.appSpec(dir)
	if err == nil {
		var restored map[string]model.PortAllocation
		if restored, err = r.readPorts(dir); err == nil {
			err = r.syncPorts(appID, dir, spec.Expose, restored, pinnedOnly(restored))
		}
	}
	if err == nil {
		return nil
	}
	if head, herr := gitHead(dir); herr == nil {
		if rerr := gitRestore(dir, head); rerr != nil {
			r.log.Warn("undo a refused rollback", "dir", dir, "error", rerr)
		}
		r.restoreSource(appID, dir)
	}
	return fmt.Errorf("rollback: %w", err)
}
````

Patch `internal/infra/orchestrator/docker_compose/repository.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/repository.go b/internal/infra/orchestrator/docker_compose/repository.go
index 6cd8f14..b9a2e06 100644
--- a/internal/infra/orchestrator/docker_compose/repository.go
+++ b/internal/infra/orchestrator/docker_compose/repository.go
@@ -10,6 +10,7 @@
 //	  .winterflow/config.json    --   committed app config blob
 //	  .winterflow/secrets.json   --   committed, ECIES ciphertext only
 //	  .winterflow/source.lock    --   committed upstream SHA (git-sourced apps)
+//	                                    (config.json's ports: the expose entries' host ports)
 //	  compose.yml, <files...>    --   committed verbatim
 //	  .env                       --   committed plain variables
 //	  .env.secrets, source/      --   gitignored deploy artifacts
@@ -139,6 +140,14 @@ type Repository struct {
 	mountMu    sync.Mutex
 	mountCache map[string]mountCacheEntry
 
+	// portMu serializes host port allocations (see syncPorts); portIndex
+	// maps every allocated host port to its app and expose entry, built
+	// from the apps' config.json on first use and pruned in forgetApp.
+	// probePort checks a port is free (replaced in tests).
+	portMu    sync.Mutex
+	portIndex map[int]portOwner
+	probePort func(bind string, port int, protocol string) error
+
 	// hostStat stats a bind source on the host (replaced in tests).
 	hostStat func(path string) (fs.FileInfo, error)
 	// statFlight holds the host stats still running, one per path: a dead
@@ -182,6 +191,7 @@ func NewRepository(cfg *config.ServerConfig, log *logger.Logger) *Repository {
 		runCompose:       dockerIn,
 		composeOutput:    dockerOutputIn,
 		mountCache:       make(map[string]mountCacheEntry),
+		probePort:        probeListen,
 		hostStat:         os.Stat,
 		runHelper:        runDockerHelper,
 		runRestoreHelper: runDockerRestoreHelper,
@@ -220,6 +230,7 @@ func (r *Repository) forgetApp(appID string) {
 	r.mountMu.Lock()
 	delete(r.mountCache, appID)
 	r.mountMu.Unlock()
+	r.forgetPorts(appID)
 	r.restoreMu.Lock()
 	delete(r.restores, appID)
 	delete(r.ops, appID)
````

Patch `internal/infra/orchestrator/docker_compose/backupconfig.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/backupconfig.go b/internal/infra/orchestrator/docker_compose/backupconfig.go
index e650f8e..087792b 100644
--- a/internal/infra/orchestrator/docker_compose/backupconfig.go
+++ b/internal/infra/orchestrator/docker_compose/backupconfig.go
@@ -18,6 +18,7 @@ import (
 	"time"
 
 	"winterflow/internal/domain/backup/recipe"
+	"winterflow/internal/domain/compose/xwinterflow"
 	"winterflow/pkg/fsutil"
 )
 
@@ -30,8 +31,9 @@ const stopOffTimeout = 2 * time.Minute
 const backupsKey = "backups"
 
 // appBackupConfig is what an app's compose files and config.json say about
-// its backups.
+// its backups, with the x-winterflow spec they were read from (one parse).
 type appBackupConfig struct {
+	spec      xwinterflow.Spec
 	compose   recipe.Compose
 	overrides []recipe.BackupConfig
 	backups   []recipe.EffectiveBackup
@@ -83,8 +85,8 @@ func (r *Repository) appBackups(dir string, files []string) (appBackupConfig, er
 	contents, readErr := composeContents(dir, files)
 	var parseErr error
 	if readErr == nil {
-		cfg.compose, parseErr = recipe.ParseCompose(contents...)
-		cfg.composeOK = parseErr == nil
+		cfg.spec, parseErr = xwinterflow.Parse(xwinterflow.ModeApp, contents...)
+		cfg.compose, cfg.composeOK = cfg.spec.Compose, parseErr == nil
 	}
 	overrides, overridesErr := r.readOverrides(dir)
 	cfg.overrides, cfg.overridesOK = overrides, overridesErr == nil
@@ -209,31 +211,30 @@ func applyBackupDefaults(cfg []byte) []byte {
 	return out
 }
 
-// cleanBackups validates the app's backups after a save wrote its files and
-// drops overrides of backups the recipe no longer has (warnings).
-func (r *Repository) cleanBackups(dir string) ([]string, error) {
-	contents, err := composeContents(dir, r.appComposeFiles(dir))
+// cleanBackups validates the app's x-winterflow block and backups after a
+// save wrote its files and drops overrides of backups the recipe no longer
+// has (warnings, with the block's own). The spec it parsed is returned for
+// the rest of the save.
+func (r *Repository) cleanBackups(dir string) (xwinterflow.Spec, []string, error) {
+	spec, err := r.appSpec(dir)
 	if err != nil {
-		return nil, fmt.Errorf("read compose files: %w", err)
-	}
-	comp, err := recipe.ParseCompose(contents...)
-	if err != nil {
-		return nil, err
+		return xwinterflow.Spec{}, nil, err
 	}
+	comp := spec.Compose
 	overrides, err := r.readOverrides(dir)
 	if err != nil {
-		return nil, err
+		return xwinterflow.Spec{}, nil, err
 	}
 	kept, warnings := recipe.Clean(comp, overrides)
 	if _, err := recipe.Effective(comp, kept); err != nil {
-		return nil, err
+		return xwinterflow.Spec{}, nil, err
 	}
 	if len(kept) != len(overrides) || (len(kept) > 0 && !reflect.DeepEqual(kept, overrides)) {
 		if err := r.writeOverrides(dir, kept); err != nil {
-			return nil, err
+			return xwinterflow.Spec{}, nil, err
 		}
 	}
-	return warnings, nil
+	return spec, append(warnings, spec.Warnings...), nil
 }
 
 // backupProfiles are the compose profiles of the app's enabled custom
````

Patch `internal/infra/orchestrator/docker_compose/operations.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/operations.go b/internal/infra/orchestrator/docker_compose/operations.go
index 2e5629c..d215550 100644
--- a/internal/infra/orchestrator/docker_compose/operations.go
+++ b/internal/infra/orchestrator/docker_compose/operations.go
@@ -98,6 +98,11 @@ func (r *Repository) saveWithoutDeploy(app command.AppPayload) (string, error) {
 // writers of the app's files); the files are then written and committed in one
 // step under it.
 func (r *Repository) save(app command.AppPayload, forDeploy bool) (string, []string, error) {
+	// The pins the save carries: without a ports key, the stored ones stay.
+	pins, pinsSent, err := model.ParsePorts(app.Config)
+	if err != nil {
+		return "", nil, err
+	}
 	dir := r.appDataDir(app.AppID)
 	_, statErr := os.Stat(dir)
 	created := errors.Is(statErr, fs.ErrNotExist) // this save creates the app's folder
@@ -127,14 +132,26 @@ func (r *Repository) save(app command.AppPayload, forDeploy bool) (string, []str
 		return "", nil, fmt.Errorf("init app repo: %w", err)
 	}
 
+	prevPorts, err := r.readPorts(dir)
+	if err != nil {
+		return "", nil, err
+	}
+	if !pinsSent {
+		pins = pinnedOnly(prevPorts)
+	}
 	store, err := r.writeAppStore(dir, app)
 	if err != nil {
 		return "", nil, fmt.Errorf("write app store: %w", err)
 	}
 
-	// The backups must be valid against the compose file being saved (for
-	// a git-sourced app, the one just pulled); nothing is committed if not.
-	warnings, err := r.cleanBackups(dir)
+	// The x-winterflow block and the backups must be valid against the
+	// compose file being saved (for a git-sourced app, the one just pulled),
+	// an ingress hostname may only point at one of its http expose entries,
+	// and every expose entry gets its host port; nothing is committed if not.
+	spec, warnings, err := r.cleanBackups(dir)
+	if err == nil {
+		err = r.applyExpose(app.AppID, dir, app.Config, spec, prevPorts, pins)
+	}
 	if err != nil {
 		restoreSource := r.undoSave(dir, created, savedPaths(app))
 		unlock()
@@ -336,6 +353,11 @@ func (r *Repository) rollbackWithoutDeploy(appID, hash string) (string, error) {
 			}
 		}
 	}
+	// The restored revision's ports come back where no other app took them
+	// meanwhile (a pin another app holds refuses the rollback).
+	if err := r.restorePorts(appID, dir); err != nil {
+		return "", err
+	}
 	short := hash
 	if len(short) > 8 {
 		short = short[:8]
@@ -478,6 +500,16 @@ func (r *Repository) refreshSourceWithoutDeploy(appID string) (bool, string, err
 	// it just wrote makes the next poll see nothing new.
 	unlock, _ := r.lockApp(context.Background(), appID)
 	defer unlock()
+	// The upstream compose file may add or drop expose entries. A failure
+	// here is left for the deploy to report: the source lock is already
+	// written, so a poll that gave up would never commit this update.
+	if spec, err := r.appSpec(dir); err != nil {
+		r.log.Warn("source update: read x-winterflow", "app_id", appID, "error", err)
+	} else if ports, err := r.readPorts(dir); err != nil {
+		r.log.Warn("source update: read ports", "app_id", appID, "error", err)
+	} else if err := r.syncPorts(appID, dir, spec.Expose, ports, pinnedOnly(ports)); err != nil {
+		r.log.Warn("source update: allocate ports", "app_id", appID, "error", err)
+	}
 	if _, err := gitCommitAll(dir, "update source to "+short); err != nil {
 		return false, "", fmt.Errorf("commit source update: %w", err)
 	}
````

- [ ] **Step 4: Run, expect PASS**

Run: `gofmt -l internal pkg && go vet ./internal/infra/orchestrator/docker_compose/ && go test ./pkg/config/ && go test -race ./internal/infra/orchestrator/docker_compose/ ./internal/app/agent/`
Expected: no gofmt output; `ok` for each (the orchestrator package with `-race` takes ~50 s).

- [ ] **Step 5: Commit**

```bash
git add pkg/config/config.go pkg/config/config_test.go .env.example internal/infra/orchestrator/docker_compose/ports.go internal/infra/orchestrator/docker_compose/ports_test.go internal/infra/orchestrator/docker_compose/repository.go internal/infra/orchestrator/docker_compose/backupconfig.go internal/infra/orchestrator/docker_compose/operations.go
git commit -m "feat(agent): allocate host ports for expose entries" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: Apply the block at every deploy

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/override.go`
- Modify: `internal/infra/orchestrator/docker_compose/gitrepo.go` (`derivedArtifacts`), `operations.go` (`composeArgsFor` last `-f`, `composeUp` writes the file), `lifecycle.go` (`renderDeployEnv` writes it), `runhelper.go` (`run.sh` line), `mounts.go` (`composeModelKey` hashes it), `store.go` (`.env.secrets` written with `marshalSharedEnv`)
- Test: `internal/infra/orchestrator/docker_compose/override_test.go`

**Interfaces:**
- Consumes: `xwinterflow.Render`, `xwinterflow.OverrideFile`, `Spec.CheckValues`, `Spec.CheckSecretValues`, Task 4's `readPorts`, `appBackupConfig.spec`, `exposePayload`/`portRepo` test helpers (ports_test.go), `newSecretRepo`, `newUpstream`, `sourcePayload`, `writeFileT`, `gitTracked`.
- Produces: `composeOverrideRel` (= `xwinterflow.OverrideFile`), `(*Repository).writeComposeOverride(dir string, spec xwinterflow.Spec) error`, `envFileValues(dir, rel string) map[string]string`. Every compose invocation built by `composeArgsFor` ends its `-f` list with `.winterflow/compose.winterflow.yml` when the file exists; `run.sh` does the same.

- [ ] **Step 1: Write the failing test**

Create `internal/infra/orchestrator/docker_compose/override_test.go`:

````go
package dockercompose

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/pkg/crypto/cryptotest"
)

const varsCompose = `services:
  app:
    image: nginx
    environment:
      BASE_URL: http://localhost:8080
      LANGUAGE: en
x-winterflow:
  expose:
    - { container: app, port: 8080 }
  variables:
    BASE_URL: { required: true, pattern: "https?://.+" }
    LANGUAGE: {}
    PIN: { encrypted: true, pattern: "[0-9]{4}" }
  overrides:
    services:
      app:
        environment:
          PIN_CODE: ${PIN}
`

// recordCompose replaces docker compose with a recorder of its arguments.
func recordCompose(r *Repository) *[][]string {
	var calls [][]string
	r.runCompose = func(_ context.Context, _ string, args []string) error {
		calls = append(calls, args)
		return nil
	}
	return &calls
}

// lastF is the last -f value of args.
func lastF(args []string) string {
	last := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-f" {
			last = args[i+1]
		}
	}
	return last
}

func TestDeployWritesTheOverrideFileLast(t *testing.T) {
	r, pub := newSecretRepo(t)
	t.Setenv("EXPOSE_PORT_RANGE", "31000-31009")
	r.probePort = func(string, int, string) error { return nil }
	p := exposePayload("app-1", varsCompose, "")
	p.Variables = []command.ContentItem{
		{Name: "BASE_URL", Content: []byte("https://notes.example.com")},
		{Name: "PIN", Encrypted: true, Content: []byte(cryptotest.Encrypt(t, pub, "1234"))},
	}
	if _, err := r.saveWithoutDeploy(p); err != nil {
		t.Fatal(err)
	}
	calls := recordCompose(r)
	if err := r.composeUp(context.Background(), "app-1"); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	got, err := os.ReadFile(filepath.Join(dir, composeOverrideRel))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Generated by winterflow from x-winterflow at every deploy. Do not edit.\n" +
		"services:\n  app:\n    environment:\n      BASE_URL: ${BASE_URL}\n      PIN_CODE: ${PIN}\n    ports:\n      - \"127.0.0.1:31000:8080\"\n"
	if string(got) != want {
		t.Fatalf("override file:\n%s", got)
	}
	if strings.Contains(string(got), "1234") || strings.Contains(string(got), "notes.example.com") {
		t.Fatal("a value reached the override file")
	}
	up := (*calls)[0]
	if lastF(up) != composeOverrideRel || !strings.Contains(strings.Join(up, " "), "-f compose.yml") {
		t.Fatalf("up args = %v", up)
	}
	// Derived state: gitignored and never committed.
	gi, _ := os.ReadFile(filepath.Join(dir, gitignoreRel))
	if !strings.Contains(string(gi), composeOverrideRel) {
		t.Fatalf(".gitignore = %s", gi)
	}
	if _, err := gitCommitAll(dir, "after deploy"); err != nil {
		t.Fatal(err)
	}
	head, _ := gitHead(dir)
	if gitTracked(dir, head, composeOverrideRel) {
		t.Fatal("the override file was committed")
	}
	// The manual-run helper passes it last too.
	script, _ := os.ReadFile(filepath.Join(dir, runRel))
	if !strings.Contains(string(script), "if [ -f "+composeOverrideRel+" ]; then set -- -f "+composeOverrideRel+` "$@"; fi`) {
		t.Fatalf("run.sh:\n%s", script)
	}
}

// A value that breaks its variable fails the deploy, naming the variable;
// a secret's value is checked after decryption and never shown.
func TestDeployChecksDeclaredValues(t *testing.T) {
	r, pub := newSecretRepo(t)
	t.Setenv("EXPOSE_PORT_RANGE", "31000-31009")
	r.probePort = func(string, int, string) error { return nil }
	calls := recordCompose(r)
	cases := []struct {
		name string
		vars []command.ContentItem
		want string
	}{
		{"required", nil, "variable BASE_URL: a value is required"},
		{"pattern", []command.ContentItem{{Name: "BASE_URL", Content: []byte("notes")}}, `variable BASE_URL: "notes" does not match`},
		{"secret pattern", []command.ContentItem{
			{Name: "BASE_URL", Content: []byte("https://x")},
			{Name: "PIN", Encrypted: true, Content: []byte(cryptotest.Encrypt(t, pub, "12ab"))},
		}, "variable PIN: the secret value does not match [0-9]{4}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := exposePayload("app-1", varsCompose, "")
			p.Variables = tc.vars
			if _, err := r.saveWithoutDeploy(p); err != nil {
				t.Fatal(err)
			}
			*calls = nil
			err := r.composeUp(context.Background(), "app-1")
			if !errors.Is(err, xwinterflow.ErrValue) || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "12ab") {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(*calls) != 0 {
				t.Fatalf("compose ran: %v", *calls)
			}
		})
	}
}

// Without an x-winterflow block nothing is written, and a file a previous
// deploy wrote goes.
func TestDeployWithoutBlockRemovesTheOverrideFile(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	writeFileT(t, dir, composeOverrideRel, "services: {}\n")
	calls := recordCompose(r)
	if err := r.composeUp(context.Background(), "app-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, composeOverrideRel)); !os.IsNotExist(err) {
		t.Fatalf("stale override file kept: %v", err)
	}
	if lastF((*calls)[0]) != "compose.yml" {
		t.Fatalf("args = %v", (*calls)[0])
	}
}

// A git-sourced app's x-winterflow (in source/) is applied the same way.
func TestDeployGitSourcedAppAppliesItsBlock(t *testing.T) {
	r := newTestRepo(t)
	t.Setenv("EXPOSE_PORT_RANGE", "31000-31009")
	r.probePort = func(string, int, string) error { return nil }
	up, commit := newUpstream(t)
	commit("deploy/compose.yml", "services:\n  web: { image: nginx }\nx-winterflow:\n  expose: [{ container: web, port: 80 }, { container: web, port: 53, protocol: udp }]\n")
	if _, err := r.saveWithoutDeploy(sourcePayload(up, "deploy/compose.yml")); err != nil {
		t.Fatal(err)
	}
	calls := recordCompose(r)
	if err := r.composeUp(context.Background(), "git-app"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(r.appDataDir("git-app"), composeOverrideRel))
	if !strings.Contains(string(got), `"127.0.0.1:31000:80"`) || !strings.Contains(string(got), `"31001:53/udp"`) {
		t.Fatalf("override file:\n%s", got)
	}
	args := strings.Join((*calls)[0], " ")
	if !strings.Contains(args, "-f source/deploy/compose.yml") || lastF((*calls)[0]) != composeOverrideRel {
		t.Fatalf("args = %s", args)
	}
}

// .env.secrets quotes every value with $ escaped: compose reads a secret
// like pa$word literally.
func TestSecretsWithDollarStayLiteral(t *testing.T) {
	r, pub := newSecretRepo(t)
	p := exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")
	p.Variables = []command.ContentItem{{Name: "TOKEN", Encrypted: true, Content: []byte(cryptotest.Encrypt(t, pub, `pa$word "x"`))}}
	if _, err := r.saveWithoutDeploy(p); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), envSecretsRel))
	if string(raw) != `TOKEN="pa\$word \"x\""`+"\n" {
		t.Fatalf(".env.secrets = %q", raw)
	}
	if got := parseEnv(raw)["TOKEN"]; got != `pa$word "x"` {
		t.Fatalf("read back %q", got)
	}
}

// The backups' cached compose model is re-read when the override file
// changes.
func TestComposeModelKeyCoversTheOverrideFile(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.saveWithoutDeploy(exposePayload("app-1", "services:\n  app: { image: nginx }\n", "")); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	before := r.composeModelKey(dir)
	writeFileT(t, dir, composeOverrideRel, "services: {}\n")
	if r.composeModelKey(dir) == before {
		t.Fatal("key ignores the override file")
	}
}

// TestRunHelperPassesTheOverrideFileLast runs the script with a fake docker.
func TestRunHelperPassesTheOverrideFileLast(t *testing.T) {
	dir, bin := t.TempDir(), t.TempDir()
	writeFileT(t, bin, "docker", "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, runRel, string(runHelperScript("wf-x", []string{"compose.yml"}, nil)))
	if err := os.Chmod(filepath.Join(dir, runRel), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, composeOverrideRel, "services: {}\n")
	writeFileT(t, dir, envSecretsRel, "")
	cmd := exec.Command(filepath.Join(dir, runRel), "up", "-d")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run.sh: %v: %s", err, out)
	}
	got := strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " ")
	if want := "compose --project-name wf-x -f compose.yml --env-file .env --env-file .env.secrets -f .winterflow/compose.winterflow.yml up -d"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
````

- [ ] **Step 2: Run it, expect FAIL**

Run: `go test ./internal/infra/orchestrator/docker_compose/ -run 'Override|Deploy|Dollar|ModelKey|RunHelperPasses'`
Expected: FAIL to build: `undefined: composeOverrideRel`.

- [ ] **Step 3: Implement**

Create `internal/infra/orchestrator/docker_compose/override.go`:

````go
package dockercompose

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"winterflow/internal/domain/compose/xwinterflow"
)

// composeOverrideRel is the compose override file rendered from the app's
// x-winterflow block at every deploy (xwinterflow.Render): derived state,
// gitignored, passed as the last -f. It holds ${NAME} references, never
// values.
const composeOverrideRel = xwinterflow.OverrideFile

// envFileValues reads one of the app's env files (none when missing).
func envFileValues(dir, rel string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return map[string]string{}
	}
	return parseEnv(raw)
}

// writeComposeOverride checks the declared variables' values, then writes
// the app's override file, or removes it when the compose files have no
// x-winterflow block or nothing to apply. The API checked the plain values
// at save; only the agent sees the decrypted secrets (.env.secrets, so call
// it after materializeSecrets) and a git-sourced app's compose file: a value
// that breaks its variable fails the deploy, naming the variable. An
// unchanged file is not rewritten.
func (r *Repository) writeComposeOverride(dir string, spec xwinterflow.Spec) error {
	path := filepath.Join(dir, composeOverrideRel)
	var content []byte
	if spec.Declared {
		plain, secrets := envFileValues(dir, envRel), envFileValues(dir, envSecretsRel)
		secretNames := map[string]bool{}
		for name := range secrets {
			secretNames[name] = true
		}
		if err := spec.CheckValues(plain, secretNames); err != nil {
			return err
		}
		if err := spec.CheckSecretValues(secrets); err != nil {
			return err
		}
		ports, err := r.readPorts(dir)
		if err != nil {
			return err
		}
		content, err = xwinterflow.Render(spec,
			func(name string) bool { return plain[name] != "" || secrets[name] != "" },
			func(e xwinterflow.Expose) int { return ports[e.ID()].Host })
		if err != nil {
			return err
		}
	}
	if content == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}
````

Patch `internal/infra/orchestrator/docker_compose/gitrepo.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/gitrepo.go b/internal/infra/orchestrator/docker_compose/gitrepo.go
index ba37b8c..51e290f 100644
--- a/internal/infra/orchestrator/docker_compose/gitrepo.go
+++ b/internal/infra/orchestrator/docker_compose/gitrepo.go
@@ -45,7 +45,7 @@ func gitEnsure(dir string) error {
 // .gitignore lists them, and every commit excludes them (neverCommitted)
 // whatever .gitignore says — a rollback restores an old .gitignore that may
 // not list them.
-var derivedArtifacts = []string{envSecretsRel, envSharedRel, envSharedSecretsRel, deployedRel, runRel, storageEnvRel}
+var derivedArtifacts = []string{envSecretsRel, envSharedRel, envSharedSecretsRel, deployedRel, runRel, storageEnvRel, composeOverrideRel}
 
 // tempArtifacts are the temp files of the agent's own atomic writes in
 // .winterflow/ (fsutil.WriteFile's config.json.tmp): a crash mid-write
````

Patch `internal/infra/orchestrator/docker_compose/operations.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/operations.go b/internal/infra/orchestrator/docker_compose/operations.go
index d215550..6ebff1d 100644
--- a/internal/infra/orchestrator/docker_compose/operations.go
+++ b/internal/infra/orchestrator/docker_compose/operations.go
@@ -654,7 +654,8 @@ func (r *Repository) composeArgs(appID string, verb ...string) []string {
 
 // composeArgsFor is composeArgs with the app's compose files already
 // resolved and the profiles to activate (--profile; none: compose's own
-// COMPOSE_PROFILES handling).
+// COMPOSE_PROFILES handling). The x-winterflow override file, when a deploy
+// wrote one, is the last -f.
 func (r *Repository) composeArgsFor(appID string, files, profiles []string, verb ...string) []string {
 	args := []string{"compose", "--project-name", projectName(appID)}
 	dir := r.appDataDir(appID)
@@ -672,6 +673,10 @@ func (r *Repository) composeArgsFor(appID string, files, profiles []string, verb
 			args = append(args, "--env-file", rel)
 		}
 	}
+	// The x-winterflow override file goes last, over every compose file.
+	if _, err := os.Stat(filepath.Join(dir, composeOverrideRel)); err == nil {
+		args = append(args, "-f", composeOverrideRel)
+	}
 	for _, p := range profiles {
 		args = append(args, "--profile", p)
 	}
@@ -729,6 +734,11 @@ func (r *Repository) composeUp(ctx context.Context, appID string) error {
 	if err := r.materializeStorageEnv(ctx, dir, files, cfg.backups); err != nil {
 		return err
 	}
+	// The x-winterflow block is applied through its override file, built
+	// from the files just read and the env files just written.
+	if err := r.writeComposeOverride(dir, cfg.spec); err != nil {
+		return err
+	}
 	// Every enabled sqlite and files backup must reach its data through the
 	// app's own mounts.
 	if _, err := r.resolveBackups(ctx, appID, files, cfg.backups); err != nil {
````

Patch `internal/infra/orchestrator/docker_compose/lifecycle.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/lifecycle.go b/internal/infra/orchestrator/docker_compose/lifecycle.go
index aec44ff..bf5936c 100644
--- a/internal/infra/orchestrator/docker_compose/lifecycle.go
+++ b/internal/infra/orchestrator/docker_compose/lifecycle.go
@@ -41,8 +41,9 @@ func (r *Repository) deploy(ctx context.Context, appID string) error {
 	return nil
 }
 
-// renderDeployEnv writes every derived env file a deploy reads: the app's
-// secrets, the shared variables and the storage variables. deploy (via
+// renderDeployEnv writes every derived file a deploy reads: the app's
+// secrets, the shared variables, the storage variables and the
+// x-winterflow override file. deploy (via
 // composeUp) writes them again; this is for a compose command that runs
 // before it (UpdateApp's pull).
 func (r *Repository) renderDeployEnv(ctx context.Context, appID string) error {
@@ -55,7 +56,10 @@ func (r *Repository) renderDeployEnv(ctx context.Context, appID string) error {
 	if err != nil {
 		return err
 	}
-	return r.materializeStorageEnv(ctx, dir, files, cfg.backups)
+	if err := r.materializeStorageEnv(ctx, dir, files, cfg.backups); err != nil {
+		return err
+	}
+	return r.writeComposeOverride(dir, cfg.spec)
 }
 
 // StartApp brings an app up.
````

Patch `internal/infra/orchestrator/docker_compose/runhelper.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/runhelper.go b/internal/infra/orchestrator/docker_compose/runhelper.go
index c53a3b0..326416e 100644
--- a/internal/infra/orchestrator/docker_compose/runhelper.go
+++ b/internal/infra/orchestrator/docker_compose/runhelper.go
@@ -24,7 +24,9 @@ const managedEnvVar = "COMPOSE_PROJECT_NAME"
 // shell). The env files are added in the order winterflow passes them
 // (composeArgsFor), each optional one only when it exists: they are
 // prepended to "$@" last-first, so compose sees .env.shared
-// .env.shared.secrets .env .winterflow/storage.env .env.secrets.
+// .env.shared.secrets .env .winterflow/storage.env .env.secrets, and then,
+// when a deploy wrote it, -f .winterflow/compose.winterflow.yml as the last
+// compose file.
 func runHelperScript(project string, files, profiles []string) []byte {
 	cmd := "exec docker compose --project-name " + project
 	for _, f := range files {
@@ -38,6 +40,7 @@ func runHelperScript(project string, files, profiles []string) []byte {
 # Runs docker compose with the same flags winterflow uses, so a manual run
 # controls the same stack. Usage: .winterflow/run.sh up -d | down | logs -f
 cd "$(dirname "$0")/.." || exit 1
+if [ -f ` + composeOverrideRel + ` ]; then set -- -f ` + composeOverrideRel + ` "$@"; fi
 if [ -f ` + envSecretsRel + ` ]; then set -- --env-file ` + envSecretsRel + ` "$@"; fi
 if [ -f ` + storageEnvRel + ` ]; then set -- --env-file ` + storageEnvRel + ` "$@"; fi
 set -- --env-file ` + envRel + ` "$@"
````

Patch `internal/infra/orchestrator/docker_compose/mounts.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/mounts.go b/internal/infra/orchestrator/docker_compose/mounts.go
index 26f056f..d2f9aa5 100644
--- a/internal/infra/orchestrator/docker_compose/mounts.go
+++ b/internal/infra/orchestrator/docker_compose/mounts.go
@@ -288,7 +288,8 @@ func (r *Repository) appComposeModel(ctx context.Context, appID string, files []
 }
 
 // composeModelKey changes whenever what compose config reads may have:
-// the commit and the derived env files. "" when there is no commit.
+// the commit, the derived env files and the x-winterflow override file.
+// "" when there is no commit.
 func (r *Repository) composeModelKey(dir string) string {
 	head, err := gitHead(dir)
 	if err != nil {
@@ -296,7 +297,7 @@ func (r *Repository) composeModelKey(dir string) string {
 	}
 	h := sha256.New()
 	h.Write([]byte(head))
-	for _, rel := range []string{envSharedRel, envSharedSecretsRel, storageEnvRel, envSecretsRel} {
+	for _, rel := range []string{envSharedRel, envSharedSecretsRel, storageEnvRel, envSecretsRel, composeOverrideRel} {
 		raw, _ := os.ReadFile(filepath.Join(dir, rel))
 		h.Write([]byte{0})
 		h.Write(raw)
````

Patch `internal/infra/orchestrator/docker_compose/store.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/store.go b/internal/infra/orchestrator/docker_compose/store.go
index f0290cf..724c66e 100644
--- a/internal/infra/orchestrator/docker_compose/store.go
+++ b/internal/infra/orchestrator/docker_compose/store.go
@@ -270,7 +270,8 @@ func (r *Repository) managedPlainFiles(dir string) []string {
 
 // materializeSecrets decrypts the store with the agent's private key into the
 // gitignored deploy outputs: .env.secrets for variables (removed when there
-// are none) and each secret file at its path. Undecryptable entries are
+// are none; every value quoted with $ escaped, so compose reads a secret
+// like pa$word literally) and each secret file at its path. Undecryptable entries are
 // logged and skipped so one broken secret doesn't block a deploy.
 func (r *Repository) materializeSecrets(dir string, s secretStore) error {
 	var vars []resolvedItem
@@ -287,7 +288,7 @@ func (r *Repository) materializeSecrets(dir string, s secretStore) error {
 		if err := os.Remove(envSecretsPath); err != nil && !os.IsNotExist(err) {
 			return err
 		}
-	} else if err := os.WriteFile(envSecretsPath, marshalEnv(vars), 0o600); err != nil {
+	} else if err := os.WriteFile(envSecretsPath, marshalSharedEnv(vars), 0o600); err != nil {
 		return err
 	}
 
````

- [ ] **Step 4: Run, expect PASS**

Run: `gofmt -l internal && go test -race ./internal/infra/orchestrator/docker_compose/ ./internal/app/agent/`
Expected: `ok` for both (the existing env-file, run.sh and secrets tests keep passing: without the override file nothing in the arguments changes, and `parseEnv` reads the quoted `.env.secrets` back unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/orchestrator/docker_compose/
git commit -m "feat(agent): apply x-winterflow through compose.winterflow.yml at every deploy" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 6: app.get, save-app 400s, OpenAPI and contract tests

**Files:**
- Modify: `internal/domain/command/app.go` (`ExposedPort`, three `GetAppResponse` fields), `internal/infra/orchestrator/docker_compose/lifecycle.go` (`GetApp`), `internal/infra/orchestrator/docker_compose/ports.go` (`exposedPorts`), `internal/domain/usecase/app/usecase.go` (`CheckSave` parses once), `internal/domain/usecase/app/storages.go` (`requireStorageRefs` takes the spec; `payloadCompose`, `checkDeclared`), `internal/domain/usecase/app/backups.go` (`checkBackups` takes `recipe.Compose`), `api/openapi.yaml` (regenerated)
- Test: `internal/infra/orchestrator/docker_compose/ports_test.go` (append), `internal/domain/usecase/app/xwinterflow_test.go`, `internal/app/web/contract/xwinterflow_test.go`

**Interfaces:**
- Consumes: Tasks 1-4 (`Parse`, `CheckValues`, `CheckPins`, `CheckTargets`, `ParsePorts`, `readPorts`, `appBackupConfig.spec`).
- Produces:
  - `command.ExposedPort{ID, Container string; Port int; Protocol string; HostPort int; Bind string; Pinned bool}` (JSON `id`, `container`, `port`, `protocol`, `host_port`, `bind`, `pinned`).
  - `command.GetAppResponse.DeclaredVariables []xwinterflow.DeclaredVariable` (`declared_variables`), `.Expose []ExposedPort` (`expose`), `.XWinterflowWarnings []string` (`x_winterflow_warnings`), all `omitempty`. The web (Tasks 7-8) reads these names.
  - Usecase: `payloadCompose(p command.AppPayload) [][]byte`, `checkDeclared(spec *xwinterflow.Spec, p command.AppPayload) error`, `requireStorageRefs(ctx, serverID string, p command.AppPayload, spec *xwinterflow.Spec, contents [][]byte, isNew bool) error`, `checkBackups(ctx, serverID string, comp recipe.Compose, config []byte, isNew bool)`.
  - Save-app answers 400 (via `model.ErrRejected`) for: an invalid block, a missing required value, a plain value failing its pattern or holding `$`, a secret variable sent plain, a pin for no entry or two entries on one port, a malformed `ports`, an ingress `expose` that is not an http entry, `expose` with `upstream_port`. A git-sourced app's block is left to the agent (only the pins' syntax is checked).

- [ ] **Step 1: Write the failing tests**

Patch `internal/infra/orchestrator/docker_compose/ports_test.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/ports_test.go b/internal/infra/orchestrator/docker_compose/ports_test.go
index 65f366e..810ba1c 100644
--- a/internal/infra/orchestrator/docker_compose/ports_test.go
+++ b/internal/infra/orchestrator/docker_compose/ports_test.go
@@ -1,6 +1,7 @@
 package dockercompose
 
 import (
+	"context"
 	"encoding/json"
 	"errors"
 	"fmt"
@@ -359,3 +360,27 @@ func TestProbeListen(t *testing.T) {
 		t.Fatal("a held udp port probed free")
 	}
 }
+
+// app.get shows the declared variables and each expose entry with its host
+// port, bind address and pin.
+func TestGetAppShowsExposeAndDeclaredVariables(t *testing.T) {
+	r, _ := portRepo(t)
+	compose := exposeCompose + "  variables:\n    LONELY: {}\n"
+	if _, err := r.saveWithoutDeploy(exposePayload("app-1", compose, `"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}`)); err != nil {
+		t.Fatal(err)
+	}
+	resp, err := r.GetApp(context.Background(), "app-1")
+	if err != nil {
+		t.Fatal(err)
+	}
+	want := []command.ExposedPort{
+		{ID: "app:8080/http", Container: "app", Port: 8080, Protocol: "http", HostPort: 31000, Bind: "127.0.0.1"},
+		{ID: "db:5432/tcp", Container: "db", Port: 5432, Protocol: "tcp", HostPort: 5432, Bind: "0.0.0.0", Pinned: true},
+	}
+	if !reflect.DeepEqual(resp.Expose, want) {
+		t.Fatalf("expose = %+v", resp.Expose)
+	}
+	if len(resp.DeclaredVariables) != 1 || resp.DeclaredVariables[0].Name != "LONELY" || len(resp.XWinterflowWarnings) != 1 {
+		t.Fatalf("variables = %+v, warnings = %v", resp.DeclaredVariables, resp.XWinterflowWarnings)
+	}
+}
````

Create `internal/domain/usecase/app/xwinterflow_test.go`:

````go
package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

const declaredCompose = `services:
  app:
    image: nginx
    environment:
      BASE_URL: http://localhost
  db:
    image: postgres
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL: { required: true, pattern: "https?://.+" }
    TOKEN: { encrypted: true }
`

// The x-winterflow checks the API can make: the block, the declared
// variables' plain values, the pins and the ingress targets. Each refusal
// is a 400 before anything is dispatched.
func TestSaveAppChecksXWinterflow(t *testing.T) {
	ctx := context.Background()
	save := func(compose, config string, vars ...command.ContentItem) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		p := command.AppPayload{Config: []byte(config), Variables: vars,
			Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}}}
		_, err := newStoragesUseCase(disp, nil).SaveApp(ctx, "u", "srv-1", model.App{Name: "demo"}, p, false)
		return disp, err
	}
	plain := func(name, value string) command.ContentItem {
		return command.ContentItem{Name: name, Content: []byte(value)}
	}
	ok := plain("BASE_URL", "https://notes.example.com")
	secret := command.ContentItem{Name: "TOKEN", Encrypted: true, Content: []byte("CIPHERTEXT")}

	if disp, err := save(declaredCompose, `{"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}}`, ok, secret); err != nil || len(disp.all) != 1 {
		t.Fatalf("valid save: %v, %d dispatches", err, len(disp.all))
	}
	cases := []struct {
		name, compose, config string
		vars                  []command.ContentItem
		want                  string
	}{
		{"invalid block", "services: {}\nx-winterflow:\n  volumes: {}\n", `{}`, nil, "x-winterflow.volumes: unknown key"},
		{"required missing", declaredCompose, `{}`, nil, "variable BASE_URL: a value is required"},
		{"pattern", declaredCompose, `{}`, []command.ContentItem{plain("BASE_URL", "notes")}, `variable BASE_URL: "notes" does not match`},
		{"dollar", declaredCompose, `{}`, []command.ContentItem{plain("BASE_URL", "https://x/$y")}, "variable BASE_URL: a plain value can't contain $"},
		{"secret sent plain", declaredCompose, `{}`, []command.ContentItem{ok, plain("TOKEN", "t")}, "variable TOKEN: it is a secret"},
		{"pin of no entry", declaredCompose, `{"ports":{"web:80/http":{"host":80,"pinned":true}}}`, []command.ContentItem{ok}, "web:80/http is not an expose entry"},
		{"malformed pin", declaredCompose, `{"ports":{"db:5432/tcp":{"host":0,"pinned":true}}}`, []command.ContentItem{ok}, "0 is not a port"},
		{"tcp ingress target", declaredCompose, `{"ingress":{"domains":[{"domain":"db.example.com","expose":"db:5432","ssl":true}],"redirects":[]}}`,
			[]command.ContentItem{ok}, "db:5432 is not an http expose entry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disp, err := save(tc.compose, tc.config, tc.vars...)
			if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(disp.all) != 0 {
				t.Fatal("a refused save reached the agent")
			}
		})
	}
}

// A git-sourced app's compose file is upstream: the agent checks it; the
// pins' syntax is still checked here.
func TestSaveAppLeavesAGitSourcedBlockToTheAgent(t *testing.T) {
	src := &command.SourcePayload{RepoURL: "https://x", Branch: "main"}
	disp := &recordingDispatcher{}
	p := command.AppPayload{Config: []byte(`{"ingress":{"domains":[{"domain":"a.example.com","expose":"web:80","ssl":true}],"redirects":[]}}`), Source: src}
	if _, err := newStoragesUseCase(disp, nil).SaveApp(context.Background(), "u", "srv-1", model.App{Name: "demo"}, p, false); err != nil || len(disp.all) != 1 {
		t.Fatalf("err = %v, dispatches %d", err, len(disp.all))
	}
	p.Config = []byte(`{"ports":{"web:80/http":{"host":70000,"pinned":true}}}`)
	if _, err := newStoragesUseCase(disp, nil).SaveApp(context.Background(), "u", "srv-1", model.App{Name: "demo"}, p, false); !errors.Is(err, model.ErrPortsInvalid) {
		t.Fatalf("err = %v", err)
	}
}
````

Create `internal/app/web/contract/xwinterflow_test.go`:

````go
package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/compose/xwinterflow"
	"winterflow/internal/domain/model"
)

const xwfCompose = `services:
  app:
    image: nginx
    environment:
      BASE_URL: http://localhost
  db:
    image: postgres
x-winterflow:
  expose:
    - { container: app, port: 8080 }
    - { container: db, port: 5432, protocol: tcp }
  variables:
    BASE_URL: { required: true, pattern: "https?://.+" }
`

// save-app answers 400 for what x-winterflow refuses, before the agent sees
// anything; a valid save with an expose target and a pin goes through.
func TestSaveAppXWinterflow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	registerDomain(t, owner, sid, "*.example.test", model.DomainTLSAuto)
	save := func(config string, vars ...happ.ContentItemRequest) *response {
		return owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "notes", Icon: "box", Color: "#112233"}, Config: json.RawMessage(config),
			Files:     []happ.ContentItemRequest{{Name: "compose.yml", Content: xwfCompose}},
			Variables: vars,
		}})
	}
	ok := happ.ContentItemRequest{Name: "BASE_URL", Content: "https://notes.example.test"}
	for want, r := range map[string]*response{
		"variable BASE_URL: a value is required":                               save(`{}`),
		`variable BASE_URL: \"notes\" does not match`:                          save(`{}`, happ.ContentItemRequest{Name: "BASE_URL", Content: "notes"}),
		"variable BASE_URL: a plain value can't contain $":                     save(`{}`, happ.ContentItemRequest{Name: "BASE_URL", Content: "https://x/$y"}),
		"db:5432 is not an http expose entry of this app":                      save(`{"ingress":{"domains":[{"domain":"db.example.test","expose":"db:5432","ssl":true}],"redirects":[]}}`, ok),
		"web:80/http is not an expose entry of this app":                       save(`{"ports":{"web:80/http":{"host":8080,"pinned":true}}}`, ok),
		"domain \\\"a.example.test\\\": set expose or upstream_port, not both": save(`{"ingress":{"domains":[{"domain":"a.example.test","expose":"app:8080","upstream_port":80,"ssl":true}],"redirects":[]}}`, ok),
	} {
		r.expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), want) {
			t.Errorf("want %q in %s", want, r.Body)
		}
	}
	if n := len(e.agent.seen(command.TypeAppSave)); n != 0 {
		t.Fatalf("%d refused save(s) reached the agent", n)
	}
	var saved command.SaveAppResponse
	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: sid, App: model.App{Name: "notes", Icon: "box", Color: "#112233"},
		Config:    json.RawMessage(`{"ingress":{"domains":[{"domain":"notes.example.test","expose":"app:8080","ssl":true}],"redirects":[]},"ports":{"db:5432/tcp":{"host":5432,"pinned":true}}}`),
		Files:     []happ.ContentItemRequest{{Name: "compose.yml", Content: xwfCompose}},
		Variables: []happ.ContentItemRequest{ok},
	}}, &saved)
	if apps := listApps(t, owner, sid); len(apps) != 1 || len(apps[0].Domains) != 1 || apps[0].Domains[0].Domain != "notes.example.test" {
		t.Fatalf("apps = %+v", apps)
	}
}

// get-app carries the declared variables and the expose entries with their
// host ports (validated against api/openapi.yaml like every answer).
func TestGetAppXWinterflow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	appID := saveApp(t, owner, sid, "notes", nil)
	def := "http://localhost"
	e.agent.on(command.TypeAppGet, func(raw json.RawMessage) reply {
		in := decode[command.GetAppRequest](raw)
		return reply{Payload: command.GetAppResponse{
			App: command.AppPayload{AppID: in.AppID, Config: []byte(`{}`), Variables: []command.ContentItem{}, Files: []command.ContentItem{}},
			DeclaredVariables: []xwinterflow.DeclaredVariable{
				{Name: "BASE_URL", Required: true, Pattern: "https?://.+", Default: &def, Services: []string{"app"}},
				{Name: "TOKEN", Encrypted: true, Services: []string{}},
			},
			Expose: []command.ExposedPort{
				{ID: "app:8080/http", Container: "app", Port: 8080, Protocol: "http", HostPort: 20000, Bind: "127.0.0.1"},
				{ID: "db:5432/tcp", Container: "db", Port: 5432, Protocol: "tcp", HostPort: 5432, Bind: "0.0.0.0", Pinned: true},
			},
			XWinterflowWarnings: []string{"x-winterflow.variables.TOKEN: no service environment and no override uses it"},
		}}
	})
	var got command.GetAppResponse
	owner.succeed(t, call{Method: http.MethodGet, Path: "/api/v1/app/get-app", Query: url.Values{"server_id": {sid}, "app_id": {appID}}}, &got)
	if len(got.DeclaredVariables) != 2 || got.DeclaredVariables[1].Default != nil || len(got.Expose) != 2 || got.Expose[1].HostPort != 5432 {
		t.Fatalf("get-app = %+v", got)
	}
}
````

- [ ] **Step 2: Run them, expect FAIL**

Run: `go test ./internal/infra/orchestrator/docker_compose/ -run TestGetAppShowsExpose && go test ./internal/domain/usecase/app/ -run 'XWinterflow|GitSourcedBlock' && go test ./internal/app/web/contract/ -run XWinterflow`
Expected: FAIL to build: `undefined: command.ExposedPort`, `unknown field DeclaredVariables in struct literal of type command.GetAppResponse`.

- [ ] **Step 3: Implement**

Patch `internal/domain/command/app.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/domain/command/app.go b/internal/domain/command/app.go
index b5d0f01..9678d4e 100644
--- a/internal/domain/command/app.go
+++ b/internal/domain/command/app.go
@@ -2,6 +2,7 @@ package command
 
 import (
 	"winterflow/internal/domain/backup/recipe"
+	"winterflow/internal/domain/compose/xwinterflow"
 	"winterflow/internal/domain/model"
 )
 
@@ -132,6 +133,28 @@ type GetAppResponse struct {
 	BackupTargets *recipe.Targets `json:"backup_targets,omitempty"`
 	// RetentionDays is the server's BACKUP_RETENTION_DAYS (0: no ceiling).
 	RetentionDays int `json:"retention_days,omitempty"`
+	// DeclaredVariables are the variables the compose files declare under
+	// x-winterflow (the editor's form), Expose their expose entries with
+	// the host ports the agent allocated, XWinterflowWarnings what the block
+	// gets away with (a literal volume name, an unused variable). Empty
+	// when the compose files can't be read (BackupsError says why).
+	DeclaredVariables   []xwinterflow.DeclaredVariable `json:"declared_variables,omitempty"`
+	Expose              []ExposedPort                  `json:"expose,omitempty"`
+	XWinterflowWarnings []string                       `json:"x_winterflow_warnings,omitempty"`
+}
+
+// ExposedPort is one x-winterflow expose entry with its host port.
+type ExposedPort struct {
+	ID        string `json:"id"` // <container>:<port>/<protocol>, the key of its pin
+	Container string `json:"container"`
+	Port      int    `json:"port"`
+	Protocol  string `json:"protocol"` // http | tcp | udp
+	// HostPort is 0 until a save allocates one.
+	HostPort int `json:"host_port"`
+	// Bind is 127.0.0.1 for http (reached through the ingress), 0.0.0.0
+	// for tcp and udp (reached on the server's address).
+	Bind   string `json:"bind"`
+	Pinned bool   `json:"pinned"`
 }
 
 // --- app.revisions --------------------------------------------------------
````

Patch `internal/infra/orchestrator/docker_compose/lifecycle.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/lifecycle.go b/internal/infra/orchestrator/docker_compose/lifecycle.go
index bf5936c..2ad2540 100644
--- a/internal/infra/orchestrator/docker_compose/lifecycle.go
+++ b/internal/infra/orchestrator/docker_compose/lifecycle.go
@@ -323,6 +323,17 @@ func (r *Repository) GetApp(ctx context.Context, appID string) (command.GetAppRe
 		t := cfg.compose.Targets()
 		resp.BackupTargets = &t
 	}
+	// What the x-winterflow block declares: the editor's variables form
+	// and ports panel.
+	if cfg.composeOK {
+		resp.DeclaredVariables = cfg.spec.Variables
+		resp.XWinterflowWarnings = cfg.spec.Warnings
+		ports, err := r.readPorts(dir)
+		if err != nil {
+			r.log.Warn("read app ports", "app_id", appID, "error", err)
+		}
+		resp.Expose = exposedPorts(cfg.spec.Expose, ports)
+	}
 	resp.RetentionDays = r.cfg.GetBackupRetentionDays()
 	return resp, nil
 }
````

Patch `internal/infra/orchestrator/docker_compose/ports.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/infra/orchestrator/docker_compose/ports.go b/internal/infra/orchestrator/docker_compose/ports.go
index bf96dd7..01fc7e6 100644
--- a/internal/infra/orchestrator/docker_compose/ports.go
+++ b/internal/infra/orchestrator/docker_compose/ports.go
@@ -11,6 +11,7 @@ import (
 	"path/filepath"
 	"strconv"
 
+	"winterflow/internal/domain/command"
 	"winterflow/internal/domain/compose/xwinterflow"
 	"winterflow/internal/domain/model"
 	"winterflow/pkg/fsutil"
@@ -264,3 +265,17 @@ func (r *Repository) restorePorts(appID, dir string) error {
 	}
 	return fmt.Errorf("rollback: %w", err)
 }
+
+// exposedPorts is app.get's view of the expose entries with their host
+// ports (0 while unallocated).
+func exposedPorts(entries []xwinterflow.Expose, ports map[string]model.PortAllocation) []command.ExposedPort {
+	out := make([]command.ExposedPort, 0, len(entries))
+	for _, e := range entries {
+		p := ports[e.ID()]
+		out = append(out, command.ExposedPort{
+			ID: e.ID(), Container: e.Container, Port: e.Port, Protocol: e.Protocol,
+			HostPort: p.Host, Bind: e.Bind(), Pinned: p.Pinned,
+		})
+	}
+	return out
+}
````

Patch `internal/domain/usecase/app/usecase.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/domain/usecase/app/usecase.go b/internal/domain/usecase/app/usecase.go
index af96f14..6c7c5bb 100644
--- a/internal/domain/usecase/app/usecase.go
+++ b/internal/domain/usecase/app/usecase.go
@@ -7,6 +7,7 @@ import (
 	"strings"
 	"time"
 	"winterflow/internal/domain/command"
+	"winterflow/internal/domain/compose/xwinterflow"
 	"winterflow/internal/domain/model"
 	"winterflow/internal/domain/port"
 	"winterflow/pkg/logger"
@@ -462,6 +463,24 @@ func (uc *UseCase) CheckSave(ctx context.Context, serverID string, app model.App
 		return CheckedSave{}, err
 	}
 
+	// The compose files in the request, parsed once: their x-winterflow
+	// block, the declared variables' values, the pins and the ingress
+	// targets are checked here. A git-sourced app's compose file is
+	// upstream: the agent checks it at save and deploy.
+	var spec *xwinterflow.Spec
+	var contents [][]byte
+	if payload.Source == nil {
+		contents = payloadCompose(payload)
+		s, err := xwinterflow.Parse(xwinterflow.ModeApp, contents...)
+		if err != nil {
+			return CheckedSave{}, err
+		}
+		spec = &s
+	}
+	if err := checkDeclared(spec, payload); err != nil {
+		return CheckedSave{}, err
+	}
+
 	// Ingress rides the config blob. Parse + validate + conflict-check here so
 	// a bad config is a synchronous 400, not an async agent failure. ing==nil
 	// (no "ingress" key) means the feature is untouched: skip everything.
@@ -473,6 +492,11 @@ func (uc *UseCase) CheckSave(ctx context.Context, serverID string, app model.App
 		if err := ing.Validate(); err != nil {
 			return CheckedSave{}, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
 		}
+		if spec != nil {
+			if err := spec.CheckTargets(ing); err != nil {
+				return CheckedSave{}, err
+			}
+		}
 		if err := uc.requireCovered(ctx, serverID, app.ID, ing); err != nil {
 			return CheckedSave{}, err
 		}
@@ -492,7 +516,7 @@ func (uc *UseCase) CheckSave(ctx context.Context, serverID string, app model.App
 		}
 	}
 
-	if err := uc.requireStorageRefs(ctx, serverID, payload, isNew); err != nil {
+	if err := uc.requireStorageRefs(ctx, serverID, payload, spec, contents, isNew); err != nil {
 		return CheckedSave{}, err
 	}
 	return CheckedSave{serverID: serverID, app: app, req: req, ing: ing}, nil
````

Patch `internal/domain/usecase/app/storages.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/domain/usecase/app/storages.go b/internal/domain/usecase/app/storages.go
index 02375ed..6d6b44f 100644
--- a/internal/domain/usecase/app/storages.go
+++ b/internal/domain/usecase/app/storages.go
@@ -5,32 +5,23 @@ import (
 	"encoding/json"
 
 	"winterflow/internal/domain/command"
+	"winterflow/internal/domain/compose/xwinterflow"
 	"winterflow/internal/domain/model"
 	"winterflow/internal/domain/port"
 )
 
-// requireStorageRefs refuses a save whose compose file (in the request)
-// declares invalid backups, or references a WF_STORAGE_ variable of a
-// storage the server doesn't have or a WF_BACKUP_ variable of a backup the
-// app doesn't declare. The compose files are picked the way the agent picks
-// them (model.DetectComposeFiles). A git-sourced app's compose file is not
-// in the request: the agent checks it at save and deploy. Without the
-// storage index (old wiring) the agent's storage check is the only one.
-func (uc *UseCase) requireStorageRefs(ctx context.Context, serverID string, p command.AppPayload, isNew bool) error {
-	if p.Source != nil {
+// requireStorageRefs refuses a save whose compose file (in the request,
+// parsed into spec from contents) declares invalid backups, or references a
+// WF_STORAGE_ variable of a storage the server doesn't have or a WF_BACKUP_
+// variable of a backup the app doesn't declare. A git-sourced app's compose
+// file is not in the request (spec nil): the agent checks it at save and
+// deploy. Without the storage index (old wiring) the agent's storage check
+// is the only one.
+func (uc *UseCase) requireStorageRefs(ctx context.Context, serverID string, p command.AppPayload, spec *xwinterflow.Spec, contents [][]byte, isNew bool) error {
+	if spec == nil {
 		return nil
 	}
-	files := map[string][]byte{}
-	for _, f := range p.Files {
-		if !f.Encrypted {
-			files[f.ItemName()] = f.Content
-		}
-	}
-	var contents [][]byte
-	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok }) {
-		contents = append(contents, files[name])
-	}
-	backupDeclared, err := uc.checkBackups(ctx, serverID, contents, p.Config, isNew)
+	backupDeclared, err := uc.checkBackups(ctx, serverID, spec.Compose, p.Config, isNew)
 	if err != nil {
 		return err
 	}
@@ -53,6 +44,46 @@ func (uc *UseCase) requireStorageRefs(ctx context.Context, serverID string, p co
 	return err
 }
 
+// payloadCompose is the contents of the request's compose files, picked the
+// way the agent picks them (model.DetectComposeFiles).
+func payloadCompose(p command.AppPayload) [][]byte {
+	files := map[string][]byte{}
+	for _, f := range p.Files {
+		if !f.Encrypted {
+			files[f.ItemName()] = f.Content
+		}
+	}
+	var contents [][]byte
+	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok }) {
+		contents = append(contents, files[name])
+	}
+	return contents
+}
+
+// checkDeclared refuses a save whose values break the variables the compose
+// files declare (a required one missing, a plain value failing its pattern
+// or holding $) or whose pins the app can't take; the pins' syntax is
+// checked even without a compose file in the request. Secret values are
+// ciphertext here: the agent checks their pattern once decrypted.
+func checkDeclared(spec *xwinterflow.Spec, p command.AppPayload) error {
+	pins, _, err := model.ParsePorts(p.Config)
+	if err != nil || spec == nil {
+		return err
+	}
+	if err := spec.CheckPins(pins); err != nil {
+		return err
+	}
+	plain, secret := map[string]string{}, map[string]bool{}
+	for _, v := range p.Variables {
+		if v.Encrypted {
+			secret[v.ItemName()] = true
+		} else {
+			plain[v.ItemName()] = string(v.Content)
+		}
+	}
+	return spec.CheckValues(plain, secret)
+}
+
 // ServerStorages reads the server's storages from the server_storages index
 // (no agent round-trip).
 func (uc *UseCase) ServerStorages(ctx context.Context, serverID string) ([]model.StorageSummary, error) {
````

Patch `internal/domain/usecase/app/backups.go` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/internal/domain/usecase/app/backups.go b/internal/domain/usecase/app/backups.go
index b6f455f..d716a8a 100644
--- a/internal/domain/usecase/app/backups.go
+++ b/internal/domain/usecase/app/backups.go
@@ -20,16 +20,13 @@ import (
 const backupRunWait = command.BackupRunWait + 5*time.Minute
 
 // checkBackups validates the backups a save makes when its compose file is
-// in the request: the recipe, the request's backups list when its config
-// carries one (an enabled backup's storage must be on the server), and
-// returns which backup names WF_BACKUP_ references may use. The app editor
-// doesn't send the backups list (the agent keeps the stored one), so for an
-// edit without it a name outside the recipe is left to the agent.
-func (uc *UseCase) checkBackups(ctx context.Context, serverID string, contents [][]byte, config []byte, isNew bool) (func(string) bool, error) {
-	comp, err := recipe.ParseCompose(contents...)
-	if err != nil {
-		return nil, err
-	}
+// in the request (comp, parsed from it): the recipe, the request's backups
+// list when its config carries one (an enabled backup's storage must be on
+// the server), and returns which backup names WF_BACKUP_ references may use.
+// The app editor doesn't send the backups list (the agent keeps the stored
+// one), so for an edit without it a name outside the recipe is left to the
+// agent.
+func (uc *UseCase) checkBackups(ctx context.Context, serverID string, comp recipe.Compose, config []byte, isNew bool) (func(string) bool, error) {
 	var cfg struct {
 		Backups  *[]recipe.BackupConfig `json:"backups"`
 		Instance *struct {
````

Then regenerate the OpenAPI document (new components `DeclaredVariable` and `ExposedPort`; `GetAppResponse` gains `declared_variables`, `expose`, `x_winterflow_warnings`):

```bash
make openapi
git diff --stat api/openapi.yaml   # expect: 1 file changed, 66 insertions(+)
```

- [ ] **Step 4: Run, expect PASS**

Run: `go vet ./... && go test ./internal/domain/... ./internal/infra/orchestrator/docker_compose/ ./internal/app/agent/ && WF_CONTRACT_GUARD=require go test ./internal/app/web/...`
Expected: `ok` everywhere, including `TestOpenAPIUpToDate`, `TestParity` (its transcript doesn't change: the default fake agent answers as before) and the contract route guard. If Docker is available, also `make test-pg TEST_PG_ARGS='-run "XWinterflow|TestParity"'`.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/command/app.go internal/infra/orchestrator/docker_compose/lifecycle.go internal/infra/orchestrator/docker_compose/ports.go internal/infra/orchestrator/docker_compose/ports_test.go internal/domain/usecase/app/ internal/app/web/contract/xwinterflow_test.go api/openapi.yaml
git commit -m "feat: save-app checks x-winterflow; app.get returns its variables and ports" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Web - the declared variables form

**Files:**
- Modify: `web/src/types/app-config.ts` (`DeclaredVariable`, `AppEditorState.declared`), `web/src/context/apps-context-base.ts` (`AppDetailPayload.declared_variables`, `x_winterflow_warnings`), `web/src/lib/app-editor-io.ts` (`stateFromDetail`, `editorErrors` `decl:<NAME>`, `buildSavePayload` drops empty declared rows), `web/src/components/variables-editor.tsx` (`hidden` prop), `web/src/components/app-editor.tsx` (form first, "Other variables" below)
- Create: `web/src/lib/declared-variables.ts`, `web/src/components/declared-variables-form.tsx`
- Test: `web/src/lib/declared-variables.test.ts`, `web/src/lib/app-editor-io-declared.test.ts`, `web/src/components/declared-variables-form.test.tsx`, `web/src/components/app-editor.test.tsx` (one new describe)

**Interfaces:**
- Consumes: `app.get`'s `declared_variables` (Task 6), `holdsStoredSecret`, `ENCRYPTED`, `invalidAttrs`, `FieldMessage`, `localId`.
- Produces: TS `DeclaredVariable` (`name`, `required`, `encrypted`, `pattern?`, `description?`, `default: string | null`, `services: string[] | null`); `AppEditorState.declared?: DeclaredVariable[]`; `matchesPattern(pattern, value): boolean`, `declaredRow(state, v)`, `declaredValueError(v, value, held): string`, `setDeclaredValue(state, v, value): AppEditorState`; `<DeclaredVariablesForm state onChange errors />` (`role="group"`, label "Declared variables"); `VariablesEditor` prop `hidden?: ReadonlySet<string>`; error key `decl:<NAME>`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/lib/declared-variables.test.ts`:

````ts
import { describe, expect, it } from "vitest";

import { emptyEditorState } from "@/lib/app-editor-io";
import { declaredRow, declaredValueError, matchesPattern, setDeclaredValue } from "@/lib/declared-variables";
import type { DeclaredVariable } from "@/types/app-config";

const BASE: DeclaredVariable = { name: "BASE_URL", required: true, encrypted: false, pattern: "https?://.+", default: "http://localhost", services: ["app"] };
const TOKEN: DeclaredVariable = { name: "TOKEN", required: false, encrypted: true, pattern: "[0-9]{4}", default: null, services: ["app"] };

describe("matchesPattern", () => {
  it("matches the whole value, and leaves patterns JavaScript can't compile to the server", () => {
    expect(matchesPattern("[a-z]{2}", "en")).toBe(true);
    expect(matchesPattern("[a-z]{2}", "eng")).toBe(false);
    expect(matchesPattern("a|b", "ab")).toBe(false);
    expect(matchesPattern(undefined, "anything")).toBe(true);
    expect(matchesPattern("(", "x")).toBe(true);
  });
});

describe("declaredValueError", () => {
  it("mirrors the server's rules", () => {
    expect(declaredValueError(BASE, "", false)).toBe("Enter a value: this variable is required.");
    expect(declaredValueError(BASE, "notes", false)).toBe("Must match https?://.+.");
    expect(declaredValueError(BASE, "https://x/$y", false)).toBe("A plain value can't contain $.");
    expect(declaredValueError(BASE, "https://notes.example.com", false)).toBe("");
    expect(declaredValueError(TOKEN, "", false)).toBe("");
    expect(declaredValueError(TOKEN, "pa$1", false)).toBe("Must match [0-9]{4}.");
    expect(declaredValueError(TOKEN, "1234", false)).toBe("");
    expect(declaredValueError({ ...TOKEN, required: true }, "<encrypted>", true)).toBe("");
  });
});

describe("setDeclaredValue", () => {
  it("adds the variable's row once, secret when the variable is, then updates it", () => {
    let s = setDeclaredValue(emptyEditorState(), TOKEN, "12");
    const row = declaredRow(s, TOKEN);
    expect(row).toMatchObject({ name: "TOKEN", is_encrypted: true });
    s = setDeclaredValue(s, TOKEN, "1234");
    expect(s.config.variables).toHaveLength(1);
    expect(s.variables[row!.id]).toBe("1234");
  });

  it("turns an existing plain row of a secret variable secret", () => {
    const start = emptyEditorState();
    start.config.variables = [{ id: "v", name: "TOKEN", is_encrypted: false }];
    start.variables = { v: "old" };
    const s = setDeclaredValue(start, TOKEN, "1234");
    expect(s.config.variables[0]).toMatchObject({ id: "v", is_encrypted: true });
    expect(s.variables.v).toBe("1234");
  });
});
````

Create `web/src/lib/app-editor-io-declared.test.ts`:

````ts
import { describe, expect, it, vi } from "vitest";

import { buildSavePayload, editorErrors, stateFromDetail } from "@/lib/app-editor-io";
import { setDeclaredValue } from "@/lib/declared-variables";
import type { DeclaredVariable } from "@/types/app-config";

vi.mock("@/lib/ecies", async (original) => ({
  ...(await original<typeof import("@/lib/ecies")>()),
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

const b64 = (s: string) => btoa(s);
const BASE: DeclaredVariable = { name: "BASE_URL", required: true, encrypted: false, pattern: "https?://.+", default: "http://localhost", services: ["app"] };
const LANG: DeclaredVariable = { name: "LANGUAGE", required: false, encrypted: false, default: "en", services: ["app"] };
const TOKEN: DeclaredVariable = { name: "TOKEN", required: true, encrypted: true, default: null, services: ["app"] };

function loaded() {
  return stateFromDetail("app-1", {
    app: {
      app_id: "app-1",
      config: b64(JSON.stringify({ name: "notes", files: [{ filename: "compose.yml" }], variables: [{ name: "TOKEN", is_encrypted: true }, { name: "EXTRA" }] })),
      files: [{ name: "compose.yml", content: b64("services: {}") }],
      variables: [
        { name: "TOKEN", content: b64("<encrypted>"), encrypted: true },
        { name: "EXTRA", content: b64("1") },
      ],
    },
    declared_variables: [BASE, LANG, TOKEN],
    revision: 1,
    available_revisions: null,
  });
}

describe("app editor io: declared variables", () => {
  it("reads them from app.get (none from an older agent)", () => {
    expect(loaded().declared).toEqual([BASE, LANG, TOKEN]);
    const old = stateFromDetail("app-1", { app: { app_id: "app-1", config: b64("{}"), files: null, variables: null }, revision: 1, available_revisions: null });
    expect(old.declared).toEqual([]);
  });

  it("names a missing required value and a value breaking its rule, by decl:<NAME>", () => {
    const s = loaded();
    expect(editorErrors(s)).toEqual({ "decl:BASE_URL": "Enter a value: this variable is required." });
    expect(editorErrors(setDeclaredValue(s, BASE, "notes"))).toEqual({ "decl:BASE_URL": "Must match https?://.+." });
    expect(editorErrors(setDeclaredValue(s, BASE, "https://notes.example.com"))).toEqual({});
  });

  it("sends a declared variable only with a value: empty, the compose file's own stands", async () => {
    const s = setDeclaredValue(setDeclaredValue(loaded(), BASE, "https://notes.example.com"), LANG, "");
    const payload = await buildSavePayload(s, async () => "PUB", "app-1");
    expect(payload.variables).toEqual([
      { name: "TOKEN", encrypted: true, content: "<encrypted>" },
      { name: "EXTRA", encrypted: false, content: "1" },
      { name: "BASE_URL", encrypted: false, content: "https://notes.example.com" },
    ]);
    expect(payload.config.variables.map((v) => v.name)).toEqual(["TOKEN", "EXTRA", "BASE_URL"]);
  });
});
````

Create `web/src/components/declared-variables-form.test.tsx`:

````tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { DeclaredVariablesForm } from "@/components/declared-variables-form";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState, DeclaredVariable } from "@/types/app-config";

const DECLARED: DeclaredVariable[] = [
  { name: "BASE_URL", required: true, encrypted: false, pattern: "https?://.+", description: "Public URL the browser uses", default: "http://localhost:8080", services: ["app"] },
  { name: "SESSION_SECRET", required: true, encrypted: true, default: null, services: ["app"] },
];

function Harness({ start, errors }: { start: AppEditorState; errors?: FieldErrors }) {
  const [state, setState] = useState(start);
  return (
    <>
      <DeclaredVariablesForm state={state} onChange={setState} errors={errors} />
      <output aria-label="state">{JSON.stringify({ vars: state.config.variables, values: state.variables })}</output>
    </>
  );
}
const state = () => JSON.parse(screen.getByRole("status", { name: "state" }).textContent ?? "{}");

describe("DeclaredVariablesForm", () => {
  it("shows each variable with its description, default as placeholder and required marker", () => {
    render(<Harness start={{ ...emptyEditorState(), declared: DECLARED }} />);
    const base = screen.getByLabelText(/BASE_URL/);
    expect(base).toHaveAttribute("placeholder", "http://localhost:8080");
    expect(base).toHaveAttribute("type", "text");
    expect(screen.getByText("Public URL the browser uses")).toBeInTheDocument();
    expect(screen.getAllByLabelText("required")).toHaveLength(2);
    expect(screen.getByLabelText(/SESSION_SECRET/)).toHaveAttribute("type", "password");
  });

  it("checks the pattern as you type and stores the value as a variable", async () => {
    const user = userEvent.setup();
    render(<Harness start={{ ...emptyEditorState(), declared: DECLARED }} />);
    await user.type(screen.getByLabelText(/BASE_URL/), "notes");
    expect(screen.getByText("Must match https?://.+.")).toBeInTheDocument();
    await user.clear(screen.getByLabelText(/BASE_URL/));
    await user.type(screen.getByLabelText(/BASE_URL/), "https://notes.example.com");
    expect(screen.queryByText(/Must match/)).not.toBeInTheDocument();
    await user.type(screen.getByLabelText(/SESSION_SECRET/), "s3cret");
    expect(state().vars).toMatchObject([
      { name: "BASE_URL", is_encrypted: false },
      { name: "SESSION_SECRET", is_encrypted: true },
    ]);
    expect(Object.values(state().values)).toEqual(["https://notes.example.com", "s3cret"]);
  });

  it("masks a stored secret and replaces it on request", async () => {
    const user = userEvent.setup();
    const start: AppEditorState = {
      ...emptyEditorState(),
      declared: DECLARED,
      config: { ...emptyEditorState().config, variables: [{ id: "s", name: "SESSION_SECRET", is_encrypted: true, storedAs: "SESSION_SECRET" }] },
      variables: { s: "<encrypted>" },
    };
    render(<Harness start={start} />);
    expect(screen.getByText("Secret · set")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("<encrypted>")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Replace SESSION_SECRET" }));
    expect(screen.getByLabelText(/SESSION_SECRET/)).toHaveAttribute("placeholder", "Leave empty to keep the current value");
  });

  it("shows a refused save's message and renders nothing without declared variables", () => {
    const { unmount } = render(<Harness start={{ ...emptyEditorState(), declared: DECLARED }} errors={{ "decl:BASE_URL": "Enter a value: this variable is required." }} />);
    expect(screen.getByText("Enter a value: this variable is required.")).toBeInTheDocument();
    unmount();
    render(<Harness start={emptyEditorState()} />);
    expect(screen.queryByRole("group", { name: "Declared variables" })).not.toBeInTheDocument();
  });
});
````

Patch `web/src/components/app-editor.test.tsx` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/components/app-editor.test.tsx b/web/src/components/app-editor.test.tsx
index c100adc..7fe1d0f 100644
--- a/web/src/components/app-editor.test.tsx
+++ b/web/src/components/app-editor.test.tsx
@@ -82,6 +82,29 @@ describe("AppEditor storage variables", () => {
   });
 });
 
+describe("AppEditor declared variables", () => {
+  it("shows the declared variables' form first and lists only the others below", () => {
+    const s = emptyEditorState();
+    const state: AppEditorState = {
+      ...s,
+      declared: [{ name: "BASE_URL", required: true, encrypted: false, default: null, services: ["app"] }],
+      config: {
+        ...s.config,
+        variables: [
+          { id: "a", name: "BASE_URL", is_encrypted: false },
+          { id: "b", name: "EXTRA", is_encrypted: false },
+        ],
+      },
+      variables: { a: "https://x", b: "1" },
+    };
+    render(<Harness initial={state} />);
+    expect(screen.getByRole("group", { name: "Declared variables" })).toBeInTheDocument();
+    expect(screen.getByRole("heading", { name: "Other variables" })).toBeInTheDocument();
+    const names = screen.getAllByRole("textbox", { name: "Variable name" }).map((el) => (el as HTMLInputElement).value);
+    expect(names).toEqual(["EXTRA"]);
+  });
+});
+
 describe("AppEditor", () => {
   it("shows only what runs: no name, icon, colour or description", () => {
     render(<Harness initial={emptyEditorState()} />);
````

- [ ] **Step 2: Run them, expect FAIL**

Run: `CI=true pnpm --dir web exec vitest run src/lib/declared-variables.test.ts src/lib/app-editor-io-declared.test.ts src/components/declared-variables-form.test.tsx src/components/app-editor.test.tsx`
Expected: FAIL: `Failed to resolve import "@/lib/declared-variables"` / `"@/components/declared-variables-form"`, and the AppEditor test can't find the "Declared variables" group.

- [ ] **Step 3: Implement**

Patch `web/src/types/app-config.ts` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/types/app-config.ts b/web/src/types/app-config.ts
index 9ca0444..85a97e4 100644
--- a/web/src/types/app-config.ts
+++ b/web/src/types/app-config.ts
@@ -78,6 +78,19 @@ export type StorageVariable = {
   secret: boolean;
 };
 
+// DeclaredVariable is a variable the compose files declare under
+// x-winterflow (app.get's declared_variables): the editor shows these first,
+// as a form. default is the compose file's own value (never a secret's).
+export type DeclaredVariable = {
+  name: string;
+  required: boolean;
+  encrypted: boolean;
+  pattern?: string;
+  description?: string;
+  default: string | null;
+  services: string[] | null;
+};
+
 // Editor working state: the config metadata plus content maps keyed by id.
 export type AppEditorState = {
   config: AppConfig;
@@ -88,6 +101,9 @@ export type AppEditorState = {
   // Read-only rows from the last app.get: provided by the server's
   // storages, never saved with the app's variables.
   storageVariables: StorageVariable[];
+  // The variables the compose files declare (last app.get; none for a new
+  // app): their values are ordinary variables, edited through a form.
+  declared?: DeclaredVariable[];
 };
 
 let counter = 0;
````

Patch `web/src/context/apps-context-base.ts` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/context/apps-context-base.ts b/web/src/context/apps-context-base.ts
index 85e5957..3305363 100644
--- a/web/src/context/apps-context-base.ts
+++ b/web/src/context/apps-context-base.ts
@@ -2,7 +2,7 @@ import { createContext } from "react";
 
 import type { ContainerStatusWire } from "@/lib/app-status";
 import type { BackupHealth, BackupTargets, EffectiveBackup } from "@/lib/backups";
-import type { StorageVariable } from "@/types/app-config";
+import type { DeclaredVariable, StorageVariable } from "@/types/app-config";
 
 export type AppDomain = {
   domain: string;
@@ -165,6 +165,9 @@ export type AppDetailPayload = {
   backups_base?: string;
   backup_targets?: BackupTargets | null;
   retention_days?: number;
+  // What the compose files' x-winterflow block declares (app.get).
+  declared_variables?: DeclaredVariable[] | null;
+  x_winterflow_warnings?: string[] | null;
   revision: number;
   available_revisions: number[] | null;
 };
````

Create `web/src/lib/declared-variables.ts`:

````ts
// The x-winterflow variables form's rules, mirroring the agent's
// (internal/domain/compose/xwinterflow CheckValues): a required variable
// needs a value, a plain value can't hold $ and must match the pattern. The
// API and the agent are the boundary; this is feedback as you type.
import { ENCRYPTED } from "@/lib/ecies";
import { localId, type AppEditorState, type AppVariableMeta, type DeclaredVariable } from "@/types/app-config";

// matchesPattern tests value against an RE2 pattern as the agent does (the
// whole value); a pattern JavaScript can't compile is left to the server.
export function matchesPattern(pattern: string | undefined, value: string): boolean {
  if (!pattern) return true;
  try {
    return new RegExp(`^(?:${pattern})$`).test(value);
  } catch {
    return true;
  }
}

// declaredRow is the variable row holding v's value (by name).
export function declaredRow(state: AppEditorState, v: DeclaredVariable): AppVariableMeta | undefined {
  return state.config.variables.find((row) => row.name.trim() === v.name);
}

// declaredValueError is what's wrong with value for v ("" when nothing):
// held says a stored secret stands for it (left as is or empty).
export function declaredValueError(v: DeclaredVariable, value: string, held: boolean): string {
  if (held && (value === "" || value === ENCRYPTED)) return "";
  if (value === "") return v.required ? "Enter a value: this variable is required." : "";
  if (!v.encrypted && value.includes("$")) return "A plain value can't contain $.";
  if (!matchesPattern(v.pattern, value)) return `Must match ${v.pattern}.`;
  return "";
}

// setDeclaredValue sets v's value, adding its row (secret when v is) the
// first time; a row of a secret variable becomes secret.
export function setDeclaredValue(state: AppEditorState, v: DeclaredVariable, value: string): AppEditorState {
  const row = declaredRow(state, v);
  if (row) {
    const variables = v.encrypted && !row.is_encrypted
      ? state.config.variables.map((r) => (r.id === row.id ? { ...r, is_encrypted: true } : r))
      : state.config.variables;
    return { ...state, config: { ...state.config, variables }, variables: { ...state.variables, [row.id]: value } };
  }
  const id = localId("var");
  return {
    ...state,
    config: { ...state.config, variables: [...state.config.variables, { id, name: v.name, is_encrypted: v.encrypted }] },
    variables: { ...state.variables, [id]: value },
  };
}
````

Create `web/src/components/declared-variables-form.tsx`:

````tsx
import { Lock } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FieldMessage } from "@/components/form-field";
import { holdsStoredSecret } from "@/lib/app-editor-io";
import { declaredRow, declaredValueError, setDeclaredValue } from "@/lib/declared-variables";
import { ENCRYPTED } from "@/lib/ecies";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import type { AppEditorState } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // errors holds decl:<NAME> messages (editorErrors), shown after a refused
  // save; a value that breaks its rule is flagged as it is typed anyway.
  errors?: FieldErrors;
};

// DeclaredVariablesForm edits the variables the compose files declare under
// x-winterflow: one field each, labelled with its name and description,
// the compose file's own value as the placeholder (left empty, it stands),
// a required marker, and the pattern checked as you type. A stored secret
// reads "Secret · set" with Replace, like the free-form rows.
export function DeclaredVariablesForm({ state, onChange, errors = {} }: Props) {
  const declared = state.declared ?? [];
  if (declared.length === 0) return null;
  return (
    <div className="space-y-4" role="group" aria-label="Declared variables">
      {declared.map((v) => {
        const row = declaredRow(state, v);
        const value = row ? (state.variables[row.id] ?? "") : "";
        const held = row !== undefined && holdsStoredSecret(row, row.name);
        const id = `decl-${v.name}`;
        const live = value === "" ? "" : declaredValueError(v, value, held);
        const message = errors[`decl:${v.name}`] || live;
        return (
          <div key={v.name} className="space-y-1.5">
            <label htmlFor={id} className="flex items-center gap-1.5 text-sm font-medium">
              <code className="font-mono">{v.name}</code>
              {v.required ? (
                <span className="text-destructive" aria-label="required">
                  *
                </span>
              ) : null}
              {v.encrypted ? <Lock className="size-3 text-muted-foreground" aria-label="secret" /> : null}
            </label>
            {v.description ? <p className="text-xs text-muted-foreground">{v.description}</p> : null}
            {held && value === ENCRYPTED ? (
              <div className="flex items-center gap-2 text-sm">
                <span className="text-muted-foreground">Secret · set</span>
                <Button size="sm" variant="outline" className="h-7" aria-label={`Replace ${v.name}`} onClick={() => onChange(setDeclaredValue(state, v, ""))}>
                  Replace
                </Button>
              </div>
            ) : (
              <Input
                id={id}
                value={value}
                type={v.encrypted ? "password" : "text"}
                autoComplete="off"
                placeholder={held ? "Leave empty to keep the current value" : (v.default ?? "")}
                {...invalidAttrs(id, message, "font-mono")}
                onChange={(e) => onChange(setDeclaredValue(state, v, e.target.value))}
              />
            )}
            <FieldMessage id={id} message={message} />
          </div>
        );
      })}
    </div>
  );
}
````

Patch `web/src/lib/app-editor-io.ts` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/lib/app-editor-io.ts b/web/src/lib/app-editor-io.ts
index 06fe9bd..fb7ab39 100644
--- a/web/src/lib/app-editor-io.ts
+++ b/web/src/lib/app-editor-io.ts
@@ -3,6 +3,7 @@
 // editor state, validating it (editorErrors), and building the app.save
 // payload (encrypting touched secrets, passing the "<encrypted>" placeholder
 // through so the agent preserves stored values).
+import { declaredRow, declaredValueError } from "@/lib/declared-variables";
 import { validHostname } from "@/lib/domains";
 import { ENCRYPTED, encryptSecret } from "@/lib/ecies";
 import type { FieldErrors } from "@/lib/field-errors";
@@ -138,6 +139,7 @@ export function stateFromDetail(
     files,
     variables,
     storageVariables: d.storage_variables ?? [],
+    declared: d.declared_variables ?? [],
   };
 }
 
@@ -200,7 +202,7 @@ function redirectTargetError(r: IngressRedirect): string {
 
 // editorErrors names every problem in the editor's state by field: name,
 // repo_url, branch, files, file:<id>, file:<id>:content, var:<id>,
-// var:<id>:value, domain:<id>, port:<id>, redirect-host:<id>,
+// var:<id>:value, decl:<NAME>, domain:<id>, port:<id>, redirect-host:<id>,
 // redirect-path:<id>, redirect-to:<id>. Ingress rows are
 // read in the order the server's Validate reads them: addresses, then
 // redirects, each claiming its hostname; a path rule needs a hostname
@@ -228,6 +230,12 @@ export function editorErrors(state: AppEditorState): FieldErrors {
     if (renamedStoredSecret(v, v.name, value)) out[`var:${v.id}:value`] = RENAMED_VAR;
     else if (placeholderMisuse(v, v.name, value)) out[`var:${v.id}:value`] = PLACEHOLDER_REFUSED;
   }
+  for (const d of state.declared ?? []) {
+    const row = declaredRow(state, d);
+    const value = row ? (state.variables[row.id] ?? "") : "";
+    const message = declaredValueError(d, value, row !== undefined && holdsStoredSecret(row, row.name));
+    if (message) out[`decl:${d.name}`] = message;
+  }
 
   const ing = config.ingress;
   if (!ing) return out;
@@ -324,18 +332,24 @@ export async function buildSavePayload(
     if (renamedStoredSecret(v, v.name, value)) throw new Error(`${v.name.trim() || "A variable"}: ${RENAMED_VAR}`);
     if (placeholderMisuse(v, v.name, value)) throw new Error(`${v.name.trim() || "A variable"}: ${PLACEHOLDER_REFUSED}`);
   }
+  // A declared variable left empty is not sent: the compose file's own
+  // value stands.
+  const declaredNames = new Set((state.declared ?? []).map((d) => d.name));
+  const sentVariables = state.config.variables.filter(
+    (v) => !(declaredNames.has(v.name.trim()) && (state.variables[v.id] ?? "") === "" && !holdsStoredSecret(v, v.name)),
+  );
   const fileKept = (f: AppFileMeta) => keepsStored(f, f.filename, state.files[f.id] ?? "");
   const varKept = (v: AppVariableMeta) => keepsStored(v, v.name, state.variables[v.id] ?? "");
   const needsKey =
     state.config.files.some((f) => f.is_encrypted && !fileKept(f)) ||
-    state.config.variables.some((v) => v.is_encrypted && !varKept(v));
+    sentVariables.some((v) => v.is_encrypted && !varKept(v));
   const publicKey = needsKey ? await getPublicKey() : "";
 
   const files = await Promise.all(
     state.config.files.map((f) => mapItem(f.filename, state.files[f.id] ?? "", f.is_encrypted, fileKept(f), publicKey)),
   );
   const variables = await Promise.all(
-    state.config.variables.map((v) => mapItem(v.name, state.variables[v.id] ?? "", v.is_encrypted, varKept(v), publicKey)),
+    sentVariables.map((v) => mapItem(v.name, state.variables[v.id] ?? "", v.is_encrypted, varKept(v), publicKey)),
   );
 
   const config = {
@@ -347,7 +361,7 @@ export async function buildSavePayload(
       filename: f.filename.trim(),
       is_encrypted: f.is_encrypted,
     })),
-    variables: state.config.variables.map((v) => ({
+    variables: sentVariables.map((v) => ({
       name: v.name.trim(),
       is_encrypted: v.is_encrypted,
     })),
````

Patch `web/src/components/variables-editor.tsx` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/components/variables-editor.tsx b/web/src/components/variables-editor.tsx
index 8251107..d69b250 100644
--- a/web/src/components/variables-editor.tsx
+++ b/web/src/components/variables-editor.tsx
@@ -22,6 +22,9 @@ type Props = {
   errors?: FieldErrors;
   // emptyNote says "No variables yet." when there are none.
   emptyNote?: boolean;
+  // hidden are names another part of the editor edits (the declared
+  // variables' form): their rows are not listed here.
+  hidden?: ReadonlySet<string>;
 };
 
 // VariablesEditor edits an app's variables: name, value (masked while
@@ -29,11 +32,12 @@ type Props = {
 // secret is never shown, nor its placeholder: it reads "Secret · set" with
 // Replace, which opens an empty field (left empty, or Keep current, keeps
 // the stored value). Secret values are encrypted by the save, never here.
-export function VariablesEditor({ state, onChange, shared, errors = {}, emptyNote = true }: Props) {
+export function VariablesEditor({ state, onChange, shared, errors = {}, emptyNote = true, hidden }: Props) {
   const [revealed, setRevealed] = useState<Record<string, boolean>>({});
   // replaced: the row whose new-value field Replace just opened (focused).
   const replaced = useRef<string | null>(null);
   const vars = state.config.variables;
+  const shown = hidden ? vars.filter((v) => !hidden.has(v.name.trim())) : vars;
   // updateMeta changes a row's name; renamed away from the name the agent
   // holds its secret under, a stored secret starts empty and needs a new
   // value (renamed back while still empty, it keeps the stored one).
@@ -59,13 +63,13 @@ export function VariablesEditor({ state, onChange, shared, errors = {}, emptyNot
 
   return (
     <div className="space-y-3">
-      {vars.length === 0 ? (
+      {shown.length === 0 ? (
         emptyNote ? (
           <EmptyNote>No variables yet.</EmptyNote>
         ) : null
       ) : (
         <div className="divide-y rounded-md border">
-          {vars.map((v) => {
+          {shown.map((v) => {
             const value = state.variables[v.id] ?? "";
             const held = holdsStoredSecret(v, v.name);
             const kept = held && value === ENCRYPTED;
````

Patch `web/src/components/app-editor.tsx` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/components/app-editor.tsx b/web/src/components/app-editor.tsx
index 8b4977a..a160acf 100644
--- a/web/src/components/app-editor.tsx
+++ b/web/src/components/app-editor.tsx
@@ -7,6 +7,7 @@ import { EditorSection } from "@/components/editor-section";
 import { FilesSection } from "@/components/files-section";
 import { GitSourceFields } from "@/components/git-source-fields";
 import { ImageVersions } from "@/components/image-versions";
+import { DeclaredVariablesForm } from "@/components/declared-variables-form";
 import { AddressRows, RedirectRows } from "@/components/ingress-editor";
 import { SharedVariablesHint } from "@/components/shared-variables-hint";
 import { VariablesEditor } from "@/components/variables-editor";
@@ -59,6 +60,7 @@ export function AppEditor({ state, onChange, errors = {}, refused = 0 }: Props)
     onRefresh: refresh,
     errors,
   };
+  const declaredNames = new Set((state.declared ?? []).map((d) => d.name));
   const hasImages = state.config.files.some(
     (f) => isComposeFile(f.filename) && imageRefs(state.files[f.id] ?? "").length > 0,
   );
@@ -88,12 +90,15 @@ export function AppEditor({ state, onChange, errors = {}, refused = 0 }: Props)
           </>
         }
       >
+        <DeclaredVariablesForm state={state} onChange={onChange} errors={errors} />
+        {declaredNames.size > 0 ? <h3 className="pt-2 text-sm font-medium">Other variables</h3> : null}
         <VariablesEditor
           state={state}
           onChange={onChange}
           shared={shared}
           errors={errors}
-          emptyNote={state.storageVariables.length === 0}
+          emptyNote={state.storageVariables.length === 0 && declaredNames.size === 0}
+          hidden={declaredNames}
         />
         <SharedVariablesHint shared={shared} />
         <StorageVariableRows vars={state.storageVariables} />
````

- [ ] **Step 4: Run, expect PASS**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint && CI=true pnpm --dir web exec vitest run src/lib src/components/declared-variables-form.test.tsx src/components/app-editor.test.tsx src/components/variables-editor.test.tsx src/components/app-editor-panel.test.tsx`
Expected: no type or lint errors; all listed tests pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/types/app-config.ts web/src/context/apps-context-base.ts web/src/lib/declared-variables.ts web/src/lib/declared-variables.test.ts web/src/lib/app-editor-io.ts web/src/lib/app-editor-io-declared.test.ts web/src/components/declared-variables-form.tsx web/src/components/declared-variables-form.test.tsx web/src/components/variables-editor.tsx web/src/components/app-editor.tsx web/src/components/app-editor.test.tsx
git commit -m "feat(web): a form for the variables x-winterflow declares" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 8: Web - the Ports panel, pins and the expose picker for hostnames

**Files:**
- Modify: `web/src/types/app-config.ts` (`ExposedPort`, `PortPin`, `exposeTarget`, `IngressDomain.expose`, `AppConfig.ports`, `AppEditorState.expose`), `web/src/context/apps-context-base.ts` (`AppDetailPayload.expose`), `web/src/lib/app-editor-io.ts` (read `ports` and `expose`, `pin:<id>` and expose-target errors, send pins and `expose`), `web/src/components/ingress-editor.tsx` ("Send to" picker), `web/src/components/app-editor.tsx` (Ports section)
- Create: `web/src/components/ports-panel.tsx`
- Test: `web/src/components/ports-panel.test.tsx`, `web/src/components/ingress-expose.test.tsx`, `web/src/lib/app-editor-io-expose.test.ts`, `web/src/components/app-editor.test.tsx` (one new describe)

**Interfaces:**
- Consumes: `app.get`'s `expose` (Task 6), config `ports` (Task 4), `IngressDomain.expose` (Task 3), `CopyField`, `NativeSelect`, `Switch`, `activeServer.capabilities.server_ip`.
- Produces: TS `ExposedPort` (`id`, `container`, `port`, `protocol: "http" | "tcp" | "udp"`, `host_port`, `bind`, `pinned`), `PortPin` (`host: number | ""`, `pinned`), `exposeTarget(e): string`; `AppConfig.ports?: Record<string, PortPin>`; `AppEditorState.expose?: ExposedPort[]`; `IngressDomain.expose?: string`; `<PortsPanel state onChange serverIp? errors? />` (`<ul aria-label="Ports">`); `buildSavePayload` always sends `config.ports` with the pinned entries only, and a domain as `{domain, expose, ssl}` or `{domain, upstream_port, ssl}`; error keys `pin:<id>` and `port:<id>` ("Pick one of the app's web ports.").

- [ ] **Step 1: Write the failing tests**

Create `web/src/components/ports-panel.test.tsx`:

````tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { PortsPanel } from "@/components/ports-panel";
import { emptyEditorState } from "@/lib/app-editor-io";
import type { FieldErrors } from "@/lib/field-errors";
import type { AppEditorState, ExposedPort } from "@/types/app-config";

const EXPOSE: ExposedPort[] = [
  { id: "app:8080/http", container: "app", port: 8080, protocol: "http", host_port: 20013, bind: "127.0.0.1", pinned: false },
  { id: "dns:53/udp", container: "dns", port: 53, protocol: "udp", host_port: 5353, bind: "0.0.0.0", pinned: true },
];

function Harness({ errors, serverIp = "192.168.1.10" }: { errors?: FieldErrors; serverIp?: string }) {
  const [state, setState] = useState<AppEditorState>({
    ...emptyEditorState(),
    expose: EXPOSE,
    config: { ...emptyEditorState().config, ports: { "app:8080/http": { host: 20013, pinned: false }, "dns:53/udp": { host: 5353, pinned: true } } },
  });
  return (
    <>
      <PortsPanel state={state} onChange={setState} serverIp={serverIp} errors={errors} />
      <output aria-label="ports">{JSON.stringify(state.config.ports)}</output>
    </>
  );
}
const ports = () => JSON.parse(screen.getByRole("status", { name: "ports" }).textContent ?? "{}");

describe("PortsPanel", () => {
  it("shows each entry's port, where it listens, and a raw entry's address to copy", () => {
    render(<Harness />);
    const rows = screen.getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("app:8080");
    expect(rows[0]).toHaveTextContent("localhost");
    expect(rows[0]).toHaveTextContent("port 20013");
    expect(rows[1]).toHaveTextContent("LAN");
    expect(screen.getByLabelText("Address of dns:53/udp")).toHaveValue("192.168.1.10:5353");
    expect(screen.queryByLabelText("Address of app:8080/http")).not.toBeInTheDocument();
  });

  it("pins a port at its current number, edits and unpins it", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    await user.click(screen.getByRole("switch", { name: "Pin port of app:8080/http" }));
    expect(ports()["app:8080/http"]).toEqual({ host: 20013, pinned: true });
    const input = screen.getByLabelText("Pinned port of app:8080/http");
    await user.clear(input);
    await user.type(input, "8080");
    expect(ports()["app:8080/http"]).toEqual({ host: 8080, pinned: true });
    await user.click(screen.getByRole("switch", { name: "Pin port of dns:53/udp" }));
    expect(ports()["dns:53/udp"]).toEqual({ host: 5353, pinned: false });
  });

  it("shows a pin's error and nothing without expose entries", () => {
    const { unmount } = render(<Harness errors={{ "pin:dns:53/udp": "Enter a port from 1 to 65535." }} />);
    expect(screen.getByText("Enter a port from 1 to 65535.")).toBeInTheDocument();
    unmount();
    render(<PortsPanel state={emptyEditorState()} onChange={() => {}} />);
    expect(screen.queryByRole("list", { name: "Ports" })).not.toBeInTheDocument();
  });
});
````

Create `web/src/components/ingress-expose.test.tsx`:

````tsx
import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { AddressRows } from "@/components/ingress-editor";
import type { AppEditorState, AppIngress, ExposedPort } from "@/types/app-config";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));

const EXPOSE: ExposedPort[] = [
  { id: "app:8080/http", container: "app", port: 8080, protocol: "http", host_port: 20013, bind: "127.0.0.1", pinned: false },
  { id: "admin:9000/http", container: "admin", port: 9000, protocol: "http", host_port: 20014, bind: "127.0.0.1", pinned: false },
  { id: "db:5432/tcp", container: "db", port: 5432, protocol: "tcp", host_port: 20015, bind: "0.0.0.0", pinned: false },
];

function Harness({ ingress, expose = EXPOSE }: { ingress: AppIngress; expose?: ExposedPort[] }) {
  const [state, setState] = useState<AppEditorState>({
    config: { id: "app-1", name: "x", files: [], variables: [], ingress },
    files: {},
    variables: {},
    storageVariables: [],
    expose,
  });
  return (
    <MemoryRouter>
      <AddressRows state={state} onChange={setState} appId="app-1" serverId="srv-1" registrations={[]} onRefresh={() => {}} />
      <output data-testid="ingress">{JSON.stringify(state.config.ingress)}</output>
    </MemoryRouter>
  );
}
const domains = () => JSON.parse(screen.getByTestId("ingress").textContent ?? "{}").domains;

describe("address rows: expose targets", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ success: true, data: { available: true, covered: true, claims: null } }))));
  });
  afterEach(() => vi.unstubAllGlobals());

  it("offers the app's web ports, never a tcp one, and a port of the compose file's own", async () => {
    const user = userEvent.setup({ delay: null });
    render(<Harness ingress={{ domains: [], redirects: [] }} />);
    await user.click(screen.getByRole("button", { name: /Add hostname/ }));
    const select = screen.getByRole("combobox", { name: "Send to" });
    expect([...select.querySelectorAll("option")].map((o) => o.textContent)).toEqual(["app:8080", "admin:9000", "Port…"]);
    expect(domains()[0]).toMatchObject({ expose: "app:8080" });
    expect(screen.queryByLabelText("Port")).not.toBeInTheDocument();
    await user.selectOptions(select, "admin:9000");
    expect(domains()[0]).toMatchObject({ expose: "admin:9000", upstream_port: "" });
    await user.selectOptions(select, "Port…");
    expect(domains()[0].expose).toBeUndefined();
    await user.type(screen.getByLabelText("Port"), "8088");
    expect(domains()[0]).toMatchObject({ upstream_port: 8088 });
  });

  it("keeps the port field alone for an app without expose entries", () => {
    render(<Harness ingress={{ domains: [{ id: "d1", domain: "a.example.com", upstream_port: 8080, ssl: true }], redirects: [] }} expose={[]} />);
    expect(screen.queryByRole("combobox", { name: "Send to" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("Port")).toHaveValue(8080);
  });
});
````

Create `web/src/lib/app-editor-io-expose.test.ts`:

````ts
import { describe, expect, it } from "vitest";

import { buildSavePayload, editorErrors, stateFromDetail } from "@/lib/app-editor-io";

const b64 = (s: string) => btoa(s);

function loaded() {
  return stateFromDetail("app-1", {
    app: {
      app_id: "app-1",
      config: b64(
        JSON.stringify({
          name: "notes",
          files: [{ filename: "compose.yml" }],
          variables: [],
          ingress: { domains: [{ domain: "notes.example.com", expose: "app:8080", ssl: true }], redirects: [] },
          ports: { "app:8080/http": { host: 20013, pinned: false }, "db:5432/tcp": { host: 5432, pinned: true } },
        }),
      ),
      files: [{ name: "compose.yml", content: b64("services: {}") }],
      variables: [],
    },
    expose: [
      { id: "app:8080/http", container: "app", port: 8080, protocol: "http", host_port: 20013, bind: "127.0.0.1", pinned: false },
      { id: "db:5432/tcp", container: "db", port: 5432, protocol: "tcp", host_port: 5432, bind: "0.0.0.0", pinned: true },
    ],
    revision: 1,
    available_revisions: null,
  });
}

describe("app editor io: expose and pins", () => {
  it("reads the expose entries, the ports and an expose target", () => {
    const s = loaded();
    expect(s.expose).toHaveLength(2);
    expect(s.config.ports?.["db:5432/tcp"]).toEqual({ host: 5432, pinned: true });
    expect(s.config.ingress?.domains[0]).toMatchObject({ domain: "notes.example.com", expose: "app:8080" });
    expect(editorErrors(s)).toEqual({});
  });

  it("sends only the pins, and an expose target without a port", async () => {
    const payload = await buildSavePayload(loaded(), async () => "PUB", "app-1");
    expect(payload.config.ports).toEqual({ "db:5432/tcp": { host: 5432, pinned: true } });
    expect(payload.config.ingress?.domains).toEqual([{ domain: "notes.example.com", expose: "app:8080", ssl: true }]);
  });

  it("refuses a target that is not a web port and a bad or shared pin", () => {
    const s = loaded();
    s.config.ingress!.domains[0].expose = "db:5432";
    s.config.ports = { "app:8080/http": { host: 5432, pinned: true }, "db:5432/tcp": { host: 5432, pinned: true }, "x:1/tcp": { host: "", pinned: true } };
    const id = s.config.ingress!.domains[0].id;
    expect(editorErrors(s)).toEqual({
      [`port:${id}`]: "Pick one of the app's web ports.",
      "pin:db:5432/tcp": "app:8080/http is pinned to this port already.",
      "pin:x:1/tcp": "Enter a port from 1 to 65535.",
    });
  });
});
````

Patch `web/src/components/app-editor.test.tsx` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/components/app-editor.test.tsx b/web/src/components/app-editor.test.tsx
index 7fe1d0f..f03b677 100644
--- a/web/src/components/app-editor.test.tsx
+++ b/web/src/components/app-editor.test.tsx
@@ -105,6 +105,22 @@ describe("AppEditor declared variables", () => {
   });
 });
 
+describe("AppEditor ports", () => {
+  it("shows the Ports section only for an app with expose entries", () => {
+    servers = { activeServer: { id: "s1", features: {} }, loading: false };
+    const state: AppEditorState = {
+      ...emptyEditorState(),
+      expose: [{ id: "app:8080/http", container: "app", port: 8080, protocol: "http", host_port: 20013, bind: "127.0.0.1", pinned: false }],
+    };
+    const { unmount } = render(<Harness initial={state} />);
+    expect(screen.getByRole("heading", { name: "Ports" })).toBeInTheDocument();
+    expect(screen.getByRole("list", { name: "Ports" })).toHaveTextContent("port 20013");
+    unmount();
+    render(<Harness initial={emptyEditorState()} />);
+    expect(screen.queryByRole("heading", { name: "Ports" })).not.toBeInTheDocument();
+  });
+});
+
 describe("AppEditor", () => {
   it("shows only what runs: no name, icon, colour or description", () => {
     render(<Harness initial={emptyEditorState()} />);
````

- [ ] **Step 2: Run them, expect FAIL**

Run: `CI=true pnpm --dir web exec vitest run src/components/ports-panel.test.tsx src/components/ingress-expose.test.tsx src/lib/app-editor-io-expose.test.ts src/components/app-editor.test.tsx`
Expected: FAIL: `Failed to resolve import "@/components/ports-panel"`; no "Send to" combobox; `payload.config.ports` undefined.

- [ ] **Step 3: Implement**

Patch `web/src/types/app-config.ts` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/types/app-config.ts b/web/src/types/app-config.ts
index 85a97e4..ee1d58e 100644
--- a/web/src/types/app-config.ts
+++ b/web/src/types/app-config.ts
@@ -38,10 +38,35 @@ export type AppSourceConfig = {
 export type IngressDomain = {
   id: string; // localId, editor-only — stripped on save
   domain: string;
+  // expose names an http expose entry (<service>:<port>) the hostname
+  // proxies to; without it, upstream_port is a port the compose file
+  // publishes itself.
+  expose?: string;
   upstream_port: number | ""; // "" while the field is empty in the editor
   ssl: boolean;
 };
 
+// ExposedPort is an x-winterflow expose entry with the host port the agent
+// allocated (app.get's expose; host_port 0 until the first save).
+export type ExposedPort = {
+  id: string; // <container>:<port>/<protocol>
+  container: string;
+  port: number;
+  protocol: "http" | "tcp" | "udp";
+  host_port: number;
+  bind: string;
+  pinned: boolean;
+};
+
+// PortPin is an entry of the config's ports: the host port of an expose
+// entry, pinned when the user chose it.
+export type PortPin = { host: number | ""; pinned: boolean };
+
+// exposeTarget is how an ingress hostname names an expose entry.
+export function exposeTarget(e: ExposedPort): string {
+  return `${e.container}:${e.port}`;
+}
+
 export type IngressRedirect = {
   id: string;
   domain: string;
@@ -67,6 +92,9 @@ export type AppConfig = {
   variables: AppVariableMeta[];
   source?: AppSourceConfig;
   ingress?: AppIngress;
+  // ports are the stored host ports by expose entry id; only the pinned
+  // ones are sent (a save without them unpins).
+  ports?: Record<string, PortPin>;
 };
 
 // StorageVariable is a WF_STORAGE_ variable the app's compose files
@@ -104,6 +132,8 @@ export type AppEditorState = {
   // The variables the compose files declare (last app.get; none for a new
   // app): their values are ordinary variables, edited through a form.
   declared?: DeclaredVariable[];
+  // The expose entries with their host ports (last app.get).
+  expose?: ExposedPort[];
 };
 
 let counter = 0;
````

Patch `web/src/context/apps-context-base.ts` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/context/apps-context-base.ts b/web/src/context/apps-context-base.ts
index 3305363..7ec41b0 100644
--- a/web/src/context/apps-context-base.ts
+++ b/web/src/context/apps-context-base.ts
@@ -2,7 +2,7 @@ import { createContext } from "react";
 
 import type { ContainerStatusWire } from "@/lib/app-status";
 import type { BackupHealth, BackupTargets, EffectiveBackup } from "@/lib/backups";
-import type { DeclaredVariable, StorageVariable } from "@/types/app-config";
+import type { DeclaredVariable, ExposedPort, StorageVariable } from "@/types/app-config";
 
 export type AppDomain = {
   domain: string;
@@ -167,6 +167,7 @@ export type AppDetailPayload = {
   retention_days?: number;
   // What the compose files' x-winterflow block declares (app.get).
   declared_variables?: DeclaredVariable[] | null;
+  expose?: ExposedPort[] | null;
   x_winterflow_warnings?: string[] | null;
   revision: number;
   available_revisions: number[] | null;
````

Patch `web/src/lib/app-editor-io.ts` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/lib/app-editor-io.ts b/web/src/lib/app-editor-io.ts
index fb7ab39..ea4ac5b 100644
--- a/web/src/lib/app-editor-io.ts
+++ b/web/src/lib/app-editor-io.ts
@@ -10,6 +10,7 @@ import type { FieldErrors } from "@/lib/field-errors";
 import type { AppDetailPayload } from "@/context/apps-context-base";
 import {
   DEFAULT_COMPOSE,
+  exposeTarget,
   isComposeFile,
   localId,
   type AppEditorState,
@@ -18,6 +19,7 @@ import {
   type AppVariableMeta,
   type IngressDomain,
   type IngressRedirect,
+  type PortPin,
 } from "@/types/app-config";
 
 export function emptyEditorState(): AppEditorState {
@@ -102,10 +104,11 @@ export function stateFromDetail(
   const ingress = cfgIngress
     ? {
         domains: (cfgIngress.domains ?? []).map((dom) => {
-          const v = dom as { domain?: string; upstream_port?: number; ssl?: boolean };
+          const v = dom as { domain?: string; expose?: string; upstream_port?: number; ssl?: boolean };
           return {
             id: localId("dom"),
             domain: v.domain ?? "",
+            ...(v.expose ? { expose: v.expose } : {}),
             upstream_port: v.upstream_port ?? ("" as const),
             ssl: v.ssl ?? false,
           };
@@ -135,11 +138,13 @@ export function stateFromDetail(
       variables: varMetas,
       source: cfgSource,
       ingress,
+      ports: (cfg as { ports?: Record<string, PortPin> }).ports ?? {},
     },
     files,
     variables,
     storageVariables: d.storage_variables ?? [],
     declared: d.declared_variables ?? [],
+    expose: d.expose ?? [],
   };
 }
 
@@ -161,7 +166,7 @@ export function newAddressRow(): IngressDomain {
 export function pruneBlankAddresses(state: AppEditorState): AppEditorState {
   const ing = state.config.ingress;
   if (!ing) return state;
-  const domains = ing.domains.filter((d) => d.domain.trim() !== "" || d.upstream_port !== "");
+  const domains = ing.domains.filter((d) => d.domain.trim() !== "" || d.upstream_port !== "" || Boolean(d.expose));
   const redirects = ing.redirects.filter((r) => r.domain.trim() !== "" || r.path.trim() !== "" || r.to.trim() !== "");
   const ingress = domains.length > 0 || redirects.length > 0 ? { domains, redirects } : undefined;
   return { ...state, config: { ...state.config, ingress } };
@@ -230,6 +235,14 @@ export function editorErrors(state: AppEditorState): FieldErrors {
     if (renamedStoredSecret(v, v.name, value)) out[`var:${v.id}:value`] = RENAMED_VAR;
     else if (placeholderMisuse(v, v.name, value)) out[`var:${v.id}:value`] = PLACEHOLDER_REFUSED;
   }
+  const pinned = new Map<number, string>();
+  for (const [id, p] of Object.entries(config.ports ?? {})) {
+    if (!p.pinned) continue;
+    const host = Number(p.host);
+    if (p.host === "" || !Number.isInteger(host) || host < 1 || host > 65535) out[`pin:${id}`] = "Enter a port from 1 to 65535.";
+    else if (pinned.has(host)) out[`pin:${id}`] = `${pinned.get(host)} is pinned to this port already.`;
+    else pinned.set(host, id);
+  }
   for (const d of state.declared ?? []) {
     const row = declaredRow(state, d);
     const value = row ? (state.variables[row.id] ?? "") : "";
@@ -246,9 +259,14 @@ export function editorErrors(state: AppEditorState): FieldErrors {
     seen.add(host);
     return "";
   };
+  const webTargets = new Set((state.expose ?? []).filter((e) => e.protocol === "http").map(exposeTarget));
   for (const d of ing.domains) {
     const host = claim(d.domain);
     if (host) out[`domain:${d.id}`] = host;
+    if (d.expose) {
+      if (!webTargets.has(d.expose)) out[`port:${d.id}`] = "Pick one of the app's web ports.";
+      continue;
+    }
     const port = Number(d.upstream_port);
     if (d.upstream_port === "" || !Number.isInteger(port) || port < 1 || port > 65535)
       out[`port:${d.id}`] = "Enter a port from 1 to 65535.";
@@ -365,14 +383,21 @@ export async function buildSavePayload(
       name: v.name.trim(),
       is_encrypted: v.is_encrypted,
     })),
+    // Only the pins: the agent keeps every other port, and a save without
+    // an entry unpins it.
+    ports: Object.fromEntries(
+      Object.entries(state.config.ports ?? {})
+        .filter(([, p]) => p.pinned)
+        .map(([id, p]) => [id, { host: Number(p.host), pinned: true }]),
+    ),
     ...(state.config.ingress
       ? {
           ingress: {
-            domains: state.config.ingress.domains.map((d) => ({
-              domain: d.domain.trim(),
-              upstream_port: Number(d.upstream_port),
-              ssl: d.ssl,
-            })),
+            domains: state.config.ingress.domains.map((d) =>
+              d.expose
+                ? { domain: d.domain.trim(), expose: d.expose, ssl: d.ssl }
+                : { domain: d.domain.trim(), upstream_port: Number(d.upstream_port), ssl: d.ssl },
+            ),
             redirects: state.config.ingress.redirects.map((r) => ({
               domain: r.domain.trim(),
               ...(r.path ? { path: r.path.trim() } : {}),
````

Create `web/src/components/ports-panel.tsx`:

````tsx
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { CopyField } from "@/components/copy-field";
import { FieldMessage } from "@/components/form-field";
import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
import type { AppEditorState, ExposedPort } from "@/types/app-config";

type Props = {
  state: AppEditorState;
  onChange: (next: AppEditorState) => void;
  // serverIp is the server's address (its server_ip capability): a tcp or
  // udp entry is reached there.
  serverIp?: string;
  // errors holds pin:<id> messages (editorErrors).
  errors?: FieldErrors;
};

// PortsPanel lists the app's expose entries: the host port the server gave
// each (allocated on the first save), where it listens (localhost: only the
// built-in proxy reaches an http entry; LAN: a tcp/udp entry listens on
// every interface, with its address to copy), and Pin port, which keeps a
// port of the user's choice.
export function PortsPanel({ state, onChange, serverIp, errors = {} }: Props) {
  const entries = state.expose ?? [];
  if (entries.length === 0) return null;
  const ports = state.config.ports ?? {};
  const setPin = (e: ExposedPort, pin: { host: number | ""; pinned: boolean }) =>
    onChange({ ...state, config: { ...state.config, ports: { ...ports, [e.id]: pin } } });
  return (
    <ul aria-label="Ports" className="divide-y rounded-md border">
      {entries.map((e) => {
        const pin = ports[e.id];
        const pinned = pin?.pinned ?? false;
        const host = pinned ? pin.host : e.host_port;
        const lan = e.protocol !== "http";
        const pinId = `pin-${e.id}`;
        const error = errors[`pin:${e.id}`];
        return (
          <li key={e.id} className="space-y-2 px-3 py-2 text-sm">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <code className="font-mono">
                {e.container}:{e.port}
              </code>
              <span className="text-xs uppercase text-muted-foreground">{e.protocol}</span>
              <span className="rounded-md border px-1.5 text-xs" title={lan ? "Listens on every interface" : "Only the built-in proxy reaches it"}>
                {lan ? "LAN" : "localhost"}
              </span>
              <span className="ml-auto text-muted-foreground">
                {e.host_port ? `port ${e.host_port}` : "port allocated on deploy"}
              </span>
              <label className="flex items-center gap-2 text-xs">
                <Switch
                  aria-label={`Pin port of ${e.id}`}
                  checked={pinned}
                  onCheckedChange={(on) => setPin(e, { host: host || "", pinned: on })}
                />
                Pin port
              </label>
            </div>
            {pinned ? (
              <Input
                id={pinId}
                type="number"
                inputMode="numeric"
                aria-label={`Pinned port of ${e.id}`}
                value={pin.host}
                {...invalidAttrs(pinId, error, "h-8 w-32")}
                onChange={(ev) => setPin(e, { host: ev.target.value === "" ? "" : Number(ev.target.value), pinned: true })}
              />
            ) : null}
            <FieldMessage id={pinId} message={error} />
            {lan && serverIp && e.host_port ? <CopyField label={`Address of ${e.id}`} value={`${serverIp}:${e.host_port}`} /> : null}
          </li>
        );
      })}
    </ul>
  );
}
````

Patch `web/src/components/ingress-editor.tsx` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/components/ingress-editor.tsx b/web/src/components/ingress-editor.tsx
index d3a570c..68fb9fc 100644
--- a/web/src/components/ingress-editor.tsx
+++ b/web/src/components/ingress-editor.tsx
@@ -4,12 +4,20 @@ import { Button } from "@/components/ui/button";
 import { Input } from "@/components/ui/input";
 import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
 import { FieldMessage } from "@/components/form-field";
+import { NativeSelect } from "@/components/native-select";
 import { HostnameField } from "@/components/hostname-field";
 import { EmptyNote } from "@/components/page-states";
 import { newAddressRow } from "@/lib/app-editor-io";
 import type { RegisteredDomain } from "@/lib/domains";
 import { invalidAttrs, type FieldErrors } from "@/lib/field-errors";
-import { localId, type AppEditorState, type AppIngress, type IngressDomain, type IngressRedirect } from "@/types/app-config";
+import {
+  exposeTarget,
+  localId,
+  type AppEditorState,
+  type AppIngress,
+  type IngressDomain,
+  type IngressRedirect,
+} from "@/types/app-config";
 
 export type IngressRowsProps = {
   state: AppEditorState;
@@ -58,6 +66,8 @@ export function AddressRows({
   const setIngress = (next: AppIngress) => onChange({ ...state, config: { ...state.config, ingress: next } });
   const update = (id: string, patch: Partial<IngressDomain>) =>
     setIngress({ ...ing, domains: ing.domains.map((d) => (d.id === id ? { ...d, ...patch } : d)) });
+  // The app's web ports (http expose entries) a hostname can be sent to.
+  const targets = (state.expose ?? []).filter((e) => e.protocol === "http").map(exposeTarget);
 
   return (
     <div className="space-y-3">
@@ -88,17 +98,35 @@ export function AddressRows({
             }}
             onReturn={onRefresh}
           >
-            <Input
-              id={portId}
-              value={d.upstream_port}
-              type="number"
-              inputMode="numeric"
-              placeholder="8088"
-              aria-label="Port"
-              title="The port the compose file publishes on 127.0.0.1"
-              {...invalidAttrs(portId, portError, "w-28 shrink-0")}
-              onChange={(e) => update(d.id, { upstream_port: e.target.value === "" ? "" : Number(e.target.value) })}
-            />
+            {targets.length > 0 || d.expose ? (
+              <NativeSelect
+                aria-label="Send to"
+                value={d.expose ?? ""}
+                {...invalidAttrs(portId, d.expose ? portError : undefined, "w-40 shrink-0")}
+                onChange={(e) => update(d.id, e.target.value ? { expose: e.target.value, upstream_port: "" } : { expose: undefined })}
+              >
+                {targets.map((t) => (
+                  <option key={t} value={t}>
+                    {t}
+                  </option>
+                ))}
+                {d.expose && !targets.includes(d.expose) ? <option value={d.expose}>{d.expose}</option> : null}
+                <option value="">Port…</option>
+              </NativeSelect>
+            ) : null}
+            {d.expose ? null : (
+              <Input
+                id={portId}
+                value={d.upstream_port}
+                type="number"
+                inputMode="numeric"
+                placeholder="8088"
+                aria-label="Port"
+                title="The port the compose file publishes on 127.0.0.1"
+                {...invalidAttrs(portId, portError, "w-28 shrink-0")}
+                onChange={(e) => update(d.id, { upstream_port: e.target.value === "" ? "" : Number(e.target.value) })}
+              />
+            )}
             <Button
               size="icon"
               variant="ghost"
@@ -111,12 +139,16 @@ export function AddressRows({
           </HostnameField>
         );
       })}
-      {ing.domains.length > 0 ? (
+      {ing.domains.some((d) => !d.expose) ? (
         <p className="text-xs text-muted-foreground">
           The compose file must publish each port on 127.0.0.1, e.g. <code>127.0.0.1:8088:80</code>.
         </p>
       ) : null}
-      <Button size="sm" variant="outline" onClick={() => setIngress({ ...ing, domains: [...ing.domains, newAddressRow()] })}>
+      <Button
+        size="sm"
+        variant="outline"
+        onClick={() => setIngress({ ...ing, domains: [...ing.domains, { ...newAddressRow(), ...(targets[0] ? { expose: targets[0] } : {}) }] })}
+      >
         <Plus className="size-4" /> {addLabel}
       </Button>
     </div>
````

Patch `web/src/components/app-editor.tsx` (apply with `git apply`, or make the same edits by hand):

````diff
diff --git a/web/src/components/app-editor.tsx b/web/src/components/app-editor.tsx
index a160acf..6036a83 100644
--- a/web/src/components/app-editor.tsx
+++ b/web/src/components/app-editor.tsx
@@ -9,6 +9,7 @@ import { GitSourceFields } from "@/components/git-source-fields";
 import { ImageVersions } from "@/components/image-versions";
 import { DeclaredVariablesForm } from "@/components/declared-variables-form";
 import { AddressRows, RedirectRows } from "@/components/ingress-editor";
+import { PortsPanel } from "@/components/ports-panel";
 import { SharedVariablesHint } from "@/components/shared-variables-hint";
 import { VariablesEditor } from "@/components/variables-editor";
 import { useServers } from "@/context/use-servers";
@@ -104,6 +105,12 @@ export function AppEditor({ state, onChange, errors = {}, refused = 0 }: Props)
         <StorageVariableRows vars={state.storageVariables} />
       </EditorSection>
 
+      {(state.expose ?? []).length > 0 ? (
+        <EditorSection title="Ports" description="The ports the compose file exposes, on the server's own port numbers.">
+          <PortsPanel state={state} onChange={onChange} serverIp={activeServer?.capabilities?.server_ip} errors={errors} />
+        </EditorSection>
+      ) : null}
+
       {/* activeServer is null while get-servers is in flight: render
           nothing until it resolves so a server with the proxy doesn't
           flash the "no proxy" line. Without the proxy there is no Domains
````

`app-editor-panel.tsx` needs no change: its `panelErrors` already hides `port:` keys when the Domains section is not shown, and `pin:`/`decl:` keys always show (their sections always render when they have entries).

- [ ] **Step 4: Run, expect PASS**

Run: `pnpm --dir web exec tsc -b && pnpm --dir web run lint && CI=true pnpm --dir web run test`
Expected: no type or lint errors; the whole Vitest suite passes (~4 minutes).

- [ ] **Step 5: Commit**

```bash
git add web/src/types/app-config.ts web/src/context/apps-context-base.ts web/src/lib/app-editor-io.ts web/src/lib/app-editor-io-expose.test.ts web/src/components/ports-panel.tsx web/src/components/ports-panel.test.tsx web/src/components/ingress-editor.tsx web/src/components/ingress-expose.test.tsx web/src/components/app-editor.tsx web/src/components/app-editor.test.tsx
git commit -m "feat(web): ports panel with pins, and hostnames sent to an expose entry" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: Integration test, docs and final verification

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/xwinterflow_integration_test.go` (build tag `integration`)
- Modify: `CLAUDE.md` (one architecture paragraph)

**Interfaces:**
- Consumes: everything above; `portsOf` (ports_test.go, same package), `projectName`.
- Produces: nothing new for code; the docs describe the feature for later sub-projects (templates reuse `xwinterflow.Parse(ModeTemplate, ...)`).

- [ ] **Step 1: Write the integration test** (needs Docker and the `nginx:alpine` image; it publishes on 127.0.0.1 in 31200-31299)

Create `internal/infra/orchestrator/docker_compose/xwinterflow_integration_test.go`:

````go
//go:build integration

package dockercompose

import (
	"context"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// TestDeployXWinterflow deploys a compose file with an x-winterflow block
// for real (build tag integration, needs Docker and nginx:alpine): the http
// entry answers on 127.0.0.1 at its allocated port, and the declared
// variable's value reaches the container through the override file. Run:
//
//	go test -tags integration -run TestDeployXWinterflow ./internal/infra/orchestrator/docker_compose/
func TestDeployXWinterflow(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	t.Setenv("EXPOSE_PORT_RANGE", "31200-31299")
	repo := NewRepository(config.NewServerConfig("standalone"), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	const compose = `services:
  web:
    image: nginx:alpine
    environment:
      GREETING: hello
x-winterflow:
  expose:
    - { container: web, port: 80 }
  variables:
    GREETING: { pattern: "[a-z]+" }
`
	app := command.AppPayload{
		AppID:     "itg-xwf",
		Config:    []byte(`{"name":"itg-xwf","files":[{"filename":"compose.yml"}],"variables":[{"name":"GREETING"}]}`),
		Files:     []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
		Variables: []command.ContentItem{{Name: "GREETING", Content: []byte("howdy")}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	t.Cleanup(func() { _, _ = repo.DeleteApp(context.Background(), app.AppID, false, false) })
	if _, err := repo.SaveApp(ctx, app); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	host := portsOf(t, repo, app.AppID)["web:80/http"].Host
	if host < 31200 || host > 31299 {
		t.Fatalf("host port = %d", host)
	}
	var code int
	for i := 0; i < 20 && code != http.StatusOK; i++ {
		if resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(host) + "/"); err == nil {
			code = resp.StatusCode
			resp.Body.Close()
		} else {
			time.Sleep(250 * time.Millisecond)
		}
	}
	if code != http.StatusOK {
		t.Fatalf("GET 127.0.0.1:%d = %d", host, code)
	}
	out, err := exec.CommandContext(ctx, "docker", "exec", projectName(app.AppID)+"-web-1", "printenv", "GREETING").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "howdy" {
		t.Fatalf("GREETING in the container = %q (%v)", out, err)
	}
}
````

- [ ] **Step 2: Run it** (with Docker; it fails without the earlier tasks, and this is the end-to-end proof that they work together)

Run: `docker image inspect nginx:alpine >/dev/null 2>&1 || docker pull nginx:alpine; go test -tags integration -run TestDeployXWinterflow ./internal/infra/orchestrator/docker_compose/`
Expected: `ok` (about 2 s with the image present). Afterwards `docker ps -a --format '{{.Names}}' | grep itg-xwf` prints nothing (the test deletes the app). Without Docker, record that it was skipped and still run `go vet -tags integration ./internal/infra/orchestrator/docker_compose/` (must pass).

- [ ] **Step 3: Document the feature in CLAUDE.md**

In `CLAUDE.md`, insert this paragraph immediately before the line starting `**Monitoring (metrics):**`:

````markdown
**x-winterflow:** the compose block every app may carry, parsed once by `internal/domain/compose/xwinterflow` (`Parse(ModeApp|ModeTemplate, files...)`, reading the same YAML trees as `recipe.ParseComposeNodes`): `expose` (`container`, `port`, `protocol` http|tcp|udp, id `<container>:<port>/<protocol>`), `variables` (`required`, `encrypted`, `pattern` RE2 <=512 B matched whole, `description` <=1000 B or the YAML comment above the first `environment:` key; default = that key's body literal, never for a secret; names `^[A-Z][A-Z0-9_]*$`, `WF_*` and `COMPOSE_PROJECT_NAME` reserved), `overrides.services`, `files`, `backups`; unknown keys are errors; a non-external volume with a literal `name:` warns in an app and fails a template. Save-app 400s on an invalid block, a missing required value, a plain value failing its pattern or holding `$`, a secret variable sent plain, a pin for no entry, and an ingress `expose` that is not an http entry (a git-sourced app's block is checked by the agent). Host ports: allocated by the agent (`syncPorts`, `ports.go`) at save (drafts too), rollback and source update from `EXPOSE_PORT_RANGE` (default 20000-29999) under one server-wide lock with an in-memory index (built from every config.json on first use, pruned in `forgetApp`), stored in the committed `.winterflow/config.json` under `ports` (`{"app:8080/http":{"host":20013,"pinned":false}}`); a save's config `ports` carries the pins (no key keeps them); ports the app holds are never probed, new ones get a listen probe; a pin another app holds is `ErrPortTaken`. At every deploy (`composeUp`, `renderDeployEnv`) the agent checks the values again (secrets after decryption) and writes the gitignored `.winterflow/compose.winterflow.yml` (`xwinterflow.Render`: `NAME: ${NAME}` in each service whose environment has a declared variable with a value; overrides with lists under `!override`, environment merged; expose ports `127.0.0.1:<host>:<port>` or `<host>:<port>/<proto>`), passed as the last `-f` by `composeArgsFor` and `run.sh`. `IngressDomain` takes `expose` (resolved by `BuildConfig` to `127.0.0.1:<host>`) or `upstream_port`, exactly one. `.env.secrets` quotes every value with `$` escaped. `app.get` returns `declared_variables` and `expose` (host port, bind, pinned) for the editor's variables form and Ports panel.
````

- [ ] **Step 4: Final verification**

Run each, from the repository root:

```bash
gofmt -l ./cmd ./internal ./pkg ./tools          # expect no output
go vet ./... && go vet -tags integration ./internal/infra/orchestrator/docker_compose/
go test ./...                                     # expect every package ok
WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/
make openapi && git diff --exit-code api/openapi.yaml   # expect no diff: committed in Tasks 3 and 6
pnpm --dir web run lint
CI=true pnpm --dir web run test
CI=true pnpm --dir web run build && touch web/dist/.gitkeep && git status --short web/dist   # expect nothing to commit
make diffcov DIFFCOV_BASE=main                    # expect go and web both PASS (>=80%)
```

With Docker, also: `make test-pg` (contract suite and repositories on Postgres; `TestParity` must match on both databases).

- [ ] **Step 5: Manual standalone smoke check** (with Docker)

```bash
make build
mkdir -p /tmp/wf-smoke
AGENT_DATA_DIR=/tmp/wf-smoke/data DATABASE_URL=sqlite:///tmp/wf-smoke/wf.sqlite HUB_CERT_DIR=/tmp/wf-smoke/certs \
  API_PORT=18080 WEB_URL=http://localhost:18080 JWT_SECRET=$(openssl rand -hex 32) REGION=local LOG_LEVEL=info \
  ./bin/standalone serve
```

In a browser at http://localhost:18080: claim the instance, then New app -> Compose, name it `smoke`, and paste:

```yaml
services:
  web:
    image: nginx:alpine
    environment:
      # Shown as the field's description
      GREETING: hello
x-winterflow:
  expose:
    - { container: web, port: 80 }
    - { container: web, port: 8443, protocol: tcp }
  variables:
    GREETING: { required: true, pattern: "[a-z]+" }
```

Check, in order:
1. Saving straight away is refused with "variable GREETING: a value is required" (the API's 400; a new app has no form yet, its compose file was never parsed by the agent).
2. Add a variable `GREETING` = `howdy` with "Add variable" and save: the app is created and deployed.
3. Open the app's Configuration tab: Variables shows GREETING first, as a form field with "Shown as the field's description", placeholder `hello` and a required marker, and not in the free-form list below; typing `Howdy` shows "Must match [a-z]+."; put back `howdy` (no save needed).
4. The Ports section lists `web:80` (localhost) and `web:8443` (LAN, with a copyable `<server ip>:<port>`), each on a port in 20000-29999; `curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:<the web:80 port>/` prints 200.
5. `cat /tmp/wf-smoke/data/apps-data/*/.winterflow/compose.winterflow.yml` shows `GREETING: ${GREETING}` and the two ports, never `howdy`; `git -C /tmp/wf-smoke/data/apps-data/<app id> ls-files | grep compose.winterflow` prints nothing; `grep compose.winterflow /tmp/wf-smoke/data/apps-data/<app id>/.winterflow/run.sh` prints the `set -- -f` line; `docker exec wf-<app id>-web-1 printenv GREETING` prints `howdy`.
6. Pin `web:8443` to 28443 and Save: `.winterflow/config.json` shows `"web:8443/tcp":{"host":28443,"pinned":true}`; change the app's colour in Settings, save, and check the pin is still there.
7. Delete the app (with its volumes), stop the server, `rm -rf /tmp/wf-smoke`.

Record the outcome (or that Docker was unavailable) in the PR description, with the suites run.

- [ ] **Step 6: Commit**

```bash
git add internal/infra/orchestrator/docker_compose/xwinterflow_integration_test.go CLAUDE.md
git commit -m "docs: x-winterflow in CLAUDE.md; integration test of a deploy" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
