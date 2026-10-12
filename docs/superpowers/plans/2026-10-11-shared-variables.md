# Shared Variables Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Org-level and server-level shared variables and secrets that every app on the matching servers can reference as `${NAME}`, delivered to agents as an encrypted snapshot, rendered into per-app gitignored env files, flagged stale when they change, and reloaded on demand.

**Architecture:** The API stores plain values and per-server ECIES ciphertexts (browser fan-out, no API key) in two new tables plus `servers.vars_revision`, and pushes the full effective set to each affected agent as `vars.sync` (on save/delete, on agent connect, on revision drift reported with `apps.status`, and on manual resync). The agent keeps it as `{AGENT_DATA_DIR}/vars/snapshot.json`, renders only the names an app references into `.env.shared` / `.env.shared.secrets` wherever it already materializes secrets, computes a per-app stale set in memory that rides the existing `apps.status` report, and runs `vars.reload` on one background worker that reports each app as a `vars.reloaded` agent event. The web gets a server Variables tab, an `/org/variables` page, a stale badge with Reload on the app page and a read-only shared list in the editor.

**Tech Stack:** Go 1.25 (chi, sqlc over SQLite `modernc.org/sqlite` / Postgres `pgx/v5`, go-git, `docker compose` CLI), React 19 + Vite + Vitest + Testing Library, vendored shadcn/ui, pnpm.

**Spec:** docs/superpowers/specs/2026-10-11-shared-variables-design.md

**Base:** branch `feat/shared-variables` (HEAD `8ba3802`, the spec commit, on top of `7703dd7` = sqlc migration). Every diff below is against that base plus the earlier tasks.

## Global Constraints

- Run `gofmt -w` on every Go file you touch before committing (the code blocks below are hand-aligned; gofmt is the authority).
- Web: this worktree may have no `web/node_modules`; run `pnpm --dir web install --frozen-lockfile` once before the first web command.
- Go builds and tests run with a private temp dir: `export GOTMPDIR=$HOME/.cache/wf-gotmp-<task>; mkdir -p $GOTMPDIR` before any `go` command (use the task number for `<task>`).
- Precedence, most specific wins: `built-ins < org < server < app`; shadowing crosses the plain/secret boundary.
- Names must match `^[A-Za-z_][A-Za-z0-9_]*$`; `COMPOSE_PROJECT_NAME` and anything starting with `WF_` are rejected with 400 on save; names are at most 255 characters (the column width).
- Built-ins, computed by the agent, read-only: `WF_SERVER_ID`, `WF_SERVER_NAME`, `WF_ORG_ID`, `WF_APP_ID`, `WF_APP_SLUG`, `WF_APP_DIR` (absolute app folder), `WF_DATA_DIR` (`AGENT_DATA_DIR`), `WF_TZ` (host timezone). Always written to `.env.shared`.
- Naming: agent side and wire say "vars" (folder `{AGENT_DATA_DIR}/vars/`, commands `vars.sync` / `vars.reload`, SSE events `vars.*`, column `servers.vars_revision`); database, routes and UI say "shared variables" (tables `shared_variables` / `shared_secret_ciphertexts`, routes `…/list-shared-variables` etc., UI "Variables" tab and "Org variables" page); per-app files `.env.shared`, `.env.shared.secrets`.
- Compose env-file order, later overrides earlier, missing files omitted: `--env-file .env.shared --env-file .env.shared.secrets --env-file .env --env-file .env.secrets` (in `composeArgs` and `.winterflow/run.sh`).
- `.env.shared` is 0644, `.env.shared.secrets` 0600 (removed when empty), `vars/snapshot.json` 0600 written atomically (temp file, fsync, rename); a snapshot with a lower revision than the stored one is ignored; `snapshot.json` never holds plaintext.
- Rendering happens only on paths that already call `materializeSecrets` (save, start, redeploy/update, rollback, source auto-update) and in `vars.reload` — never on `vars.sync`.
- No API route ever returns a secret value; secrets are listed as `<encrypted>` (`command.EncryptedPlaceholder`); ciphertexts are never returned either. Plaintext exists only in 0600 per-app files and is never logged (warnings name the variable, never its value).
- Coverage rules: every `ciphertexts` key must be a server of the caller's org (else 403); a new value must cover every keyed server in scope (else 400 naming the uncovered ids); a gap fill (every key currently missing that secret) never overwrites; a save without `ciphertexts` keeps every stored ciphertext; rename and plain↔secret changes are refused (delete and recreate).
- Server key change: when an agent reports a `public_key` different from the stored one, delete every `shared_secret_ciphertexts` row for that server and bump its `vars_revision`.
- Save/delete answer 200 once the DB write commits; nothing waits on agents. `vars_revision` is bumped for every affected server; `reload: true` follows each successful `vars.sync` with `vars.reload {names: [name]}`.
- Reload worker: one goroutine per agent, one app at a time under the per-app lock, re-render then `compose up -d`, running apps only, 10-minute deadline per app, coalescing, deleting an app drops its queued work, exits on ctx cancel; never runs inside the dispatcher.
- Security: every server-addressed route uses `webutil.RequireServerAccess`; every `server_id` in a body (including `ciphertexts` keys) is ownership-checked; 403 tests per route. Docker/git CLI args stay `[]string`.
- Resources: no process forks on sync or stale detection; reference scans are cached keyed by HEAD + `source.lock`; SSE fan-out only on change (`status.Cache.SetAppStatus`); the one `docker ps` in the reload path is not polled.
- Persistence: per-engine SQL only (`internal/infra/db/sqlc/query/{sqlite,pgsql}`, `migrations/{sqlite,pgsql}`), `make sqlc` after query/migration changes, never edit `internal/infra/db/sqlc/gen` by hand, keep `.sql` files ASCII, `Tx.WithTx` with `Tx.Querier(ctx)` inside `fn`.
- Routes live in `Routes(g api.Guards) []api.Route` tables; run `make openapi` after any route or response-type change; new SSE notification types need an `api.EventPayload` in `handler/notification/routes.go`; every new route needs a happy-path contract test and a `probes` entry.
- No new env var (so `.env.example` is unchanged). `WF_TZ` is read from `/etc/timezone` or the `/etc/localtime` link, not from `$TZ`, to keep it that way.
- Never delete anything under `web/src/components/ui/` (vendored shadcn) or its package.json deps. After `pnpm --dir web run build`, keep `web/dist/.gitkeep` (restore with `git checkout web/dist/.gitkeep` if the build removed it).
- Commits: Conventional Commits, each message ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- CI gates: ≥80% diff coverage per language (`make diffcov DIFFCOV_BASE=main`) and the coverage ratchet.
- Migration number: `0010_shared_variables.sql` is the next free number on this branch. PRs #12, #13 and #14 each also claim 0010; renumber to the next free number at merge time (rename both files; `make sqlc`).

## Review Focus

1. **Rollback to a revision older than shared variables.** The restored `.gitignore` lacks `.env.shared*`; the rollback commit must not record the plaintext `.env.shared.secrets` in app history. Expected: never committed. Pinned in Task 5 (`TestCommitNeverRecordsSharedEnvFiles`, which restores an old `.gitignore` then commits).
2. **Upgrading an agent with existing apps.** Apps that have no `.env.shared` yet and reference no shared name must not all flip to "stale" (built-ins don't count unless referenced); an app that references an org value must. Pinned in Task 6 (`TestStaleIgnoresUnreferencedBuiltinsOnUpgrade`).
3. **Secret values containing `$`, quotes, backslashes or newlines** (a PEM key, `pa$word`). Compose interpolates unquoted and double-quoted env-file values, so the literal value must still reach the container. Pinned in Task 5 (`TestMarshalSharedEnvRoundTripsAwkwardValues`; the format was checked against `docker compose config` v5.5.1: `"pa\$word"` → `pa$word`, `"a\\b"` → `a\b`).
4. **Reloading an app that has an undeployed draft.** `compose up` runs on the worktree, so a reload would silently deploy the draft. Expected: that app is skipped with an error that names the draft. Pinned in Task 7 (`TestReloadRefusesUndeployedDraft`).
5. **An agent that is offline or predates shared variables.** A save while the agent is offline must not raise a "sync failed" (a bounced `agent not connected` stays pending). An old agent (no `vars_revision` in `apps.status`) must never be drift-pushed. A behind agent is drift-pushed at most once per revision. Pinned in Task 9 (`TestSyncNotConnectedStaysPending`) and Task 10 (`TestDriftPushesOncePerRevision`, `TestOldAgentIsNeverDriftPushed`).

## Decisions on the spec's open items

1. **Can the heartbeat carry `vars_revision`?** `proto.AgentHeartbeat` carries only `BaseMessage`, so adding a field would need `make grpc` and a hub change. No proto change is needed: the agent already pushes `apps.status` every 30 s through `AgentEvent` (hub → `events:<region>` → `eventSink`; standalone publishes straight onto the in-process queue). `command.GetAppsStatusResponse` gains `VarsRevision *int64` (`vars_revision`, nil from old agents). The API's `eventSink` compares it with `servers.vars_revision` and drift-pushes once per (server, revision). Connect-time sync reuses the `domainsync.go` online/heartbeat/retry driver unchanged.
2. **How do `vars.reloaded` and the stale set reach the API?** The source auto-update poller reports nothing upstream (`RunSourcePoller` only logs), so it has no channel to reuse. The stale set rides `apps.status` as `AppStatus.StaleVars []string` (`stale_vars`). `status.Cache.SetAppStatus` already DeepEquals the app list, so a stale change fans out as `apps_status` only when it changes. `vars.reloaded` is a new agent-initiated event kind (`bus.EventVarsReloaded`) sent through the same `EventSink` the status reporter uses (gRPC `SendEvent` in distributed mode, the in-process events queue in standalone). `eventSink` fans it out to the org as the `vars.reloaded` SSE notification.
3. **Migration number:** `0010` (see Global Constraints; renumber at merge against #12–#14).

## Spec deviations (and why)

- **Resync and reload answer 200, not 202.** They can address several servers, and the route table's 202 shape (`api.Route.Accepted`) carries exactly one `request_id` and one `operation_result`. They return `{servers: [...]}` (the servers dispatched to); results arrive as `vars.synced` / `vars.sync_failed` / `vars.reloaded` events. The contract suite validates a 202 against the single-request shape, so a multi-server 202 can't be documented.
- **`vars.sync` reply: `undecryptable` is `[{scope, name}]`, not `[name]`.** An org secret and a server secret can share a name. With bare names the API would drop a good server-scope ciphertext along with a bad org one. The `vars.synced` SSE payload still lists plain names.
- **A secret with no ciphertext for the target server is still sent in `vars.sync`, with an empty `ciphertext`.** Without it the agent couldn't log the spec's "referenced but missing" warning, because it would never learn the name exists.
- **Restart does not render.** `RestartApp` runs `compose restart`, which neither calls `materializeSecrets` today nor re-reads env files. Rendering there would mark the app fresh while its containers keep the old values. Reload is the action that applies new values.
- **There was no per-app lock.** The spec says reloads run "under the existing per-app lock", but none exists, and the distributed agent runs commands concurrently (`go a.handleRequest`). Task 7 adds `Repository.lockApp` and takes it in every public app mutator and in the reload.
- **Names the app defines itself are excluded from rendering and the stale check.** The app value wins anyway (precedence), so rendering the shared copy would only cause stale noise.
- **UI copy is inline English,** like every existing settings page (`settings-domains.tsx`, `org-members.tsx`). `web/src/locales/en.ts` holds only the login strings today, and adding `t()` to two pages alone would be inconsistent.
- **The per-row sync state from the API is `synced` / `pending`.** It compares the agent's last reported revision, kept in the in-memory status cache like liveness, with `servers.vars_revision`. `failed` is overlaid in the browser from `vars.sync_failed`, as the spec's "from SSE" says. No column is added.

---

## File map

| File | Task | Change |
|---|---|---|
| `internal/domain/model/shared_variable.go` (+ `_test`) | 1 | new: name rule, `SharedVariable`, `SharedVariableSave`, `ServerKey`, `ServerVarsInfo`, errors |
| `internal/domain/model/notification.go` | 1 | `vars.synced`, `vars.sync_failed`, `vars.reloaded` types + payloads |
| `internal/domain/command/command.go`, `vars.go` (+ `_test`), `app.go` | 1 | `vars.sync`/`vars.reload` types and payloads; `AppStatus.StaleVars`; `GetAppsStatusResponse.VarsRevision` |
| `internal/infra/transport/bus/bus.go` | 1 | `EventVarsReloaded` |
| `internal/infra/db/migrations/{sqlite,pgsql}/0010_shared_variables.sql` | 2 | new tables + `servers.vars_revision` |
| `internal/infra/db/sqlc/query/{sqlite,pgsql}/shared_variables.sql`, `sqlc/gen/*` (generated), `sqlc/shapes_test.go` | 2 | queries |
| `internal/domain/port/sharedvar.go` | 3 | `SharedVariableRepository` |
| `internal/infra/db/repository/sharedvar/{repo,sqlite,pgsql,repo_test}.go` | 3 | repository |
| `internal/infra/vars/{store,resolve,refs}.go` (+ tests) | 4 | agent snapshot store, resolver, reference scanner |
| `internal/infra/orchestrator/docker_compose/{envfile,store,gitrepo,operations,runhelper}.go` (+ tests) | 5 | env-file wiring, gitignore guard, quoting |
| `internal/infra/orchestrator/docker_compose/shared.go` (+ `shared_test.go`), `repository.go`, `store.go` | 6 | render, refs cache, stale, `SyncVars`, `VarsRevision` |
| `internal/infra/orchestrator/docker_compose/{applock,reload}.go` (+ tests), `lifecycle.go`, `operations.go` | 7 | per-app lock, reload candidates, reload |
| `internal/app/agent/{reload_worker,dispatcher,reporter}.go` (+ tests), `cmd/agent/main.go`, `internal/infra/bootstrap/standalone.go` | 8 | worker, commands, wiring |
| `internal/domain/usecase/sharedvars/{usecase,views,sync}.go` (+ `usecase_test.go`) | 9 | API usecase |
| `internal/domain/service/status/cache.go` (+ test), `internal/infra/bootstrap/{varssync,events,core,deps,domainsync}.go` (+ tests) | 10 | drivers + wiring |
| `internal/app/web/handler/sharedvars/{handler,routes}.go`, `handler/notification/routes.go`, `internal/app/web/routes.go`, `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`, `internal/app/web/contract/{shared_variables_test,fakeagent_test,matrix_test,parity_test}.go`, `contract/testdata/parity.golden.json`, `CLAUDE.md` | 11 | HTTP surface + contract |
| `web/src/lib/shared-variables.ts` (+ test) | 12 | types, API client, `encryptForServers`, helpers |
| `web/src/components/{variables-table,shared-variable-dialog}.tsx` (+ tests) | 13 | shared components |
| `web/src/lib/app-status.ts` (+ test), `web/src/context/{apps-context-base.ts,apps-context.tsx}`, `web/src/pages/settings-variables.tsx` (+ test), `settings.tsx`, `settings.test.tsx` | 14 | stale map in the apps context; server Variables tab |
| `web/src/pages/org-variables.tsx` (+ test), `main.tsx`, `components/nav-user.tsx` | 15 | org page + nav |
| `web/src/context/{apps-context-base.ts,apps-context.tsx}`, `pages/app-details.tsx`, `components/{stale-vars-badge,shared-variables-hint}.tsx` (+ tests), `components/app-editor.tsx` | 16 | `reloadSharedVars`, app page badge, editor list and hints |

---
### Task 1: Domain types: name rule, commands, payloads, event kind

**Files:**
- Create: `internal/domain/model/shared_variable.go`, `internal/domain/model/shared_variable_test.go`
- Create: `internal/domain/command/vars.go`, `internal/domain/command/vars_test.go`
- Modify: `internal/domain/command/command.go` (type constants), `internal/domain/command/app.go` (`AppStatus`, `GetAppsStatusResponse`)
- Modify: `internal/domain/model/notification.go`
- Modify: `internal/infra/transport/bus/bus.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (exact names later tasks use):
  - `model.ValidateSharedVariableName(name string) error`, `model.SharedVariableNameMax = 255`
  - `model.SharedScopeOrg = "org"`, `model.SharedScopeServer = "server"`
  - `model.SharedVariable{ID, OrgID string; ServerID *string; Name string; IsSecret bool; Value, Description string; UpdatedAt time.Time; UpdatedBy string; CipherServers []string}` with `func (SharedVariable) Scope() string`
  - `model.SharedVariableSave{Variable SharedVariable; Create bool; Ciphertexts map[string]string; ReplaceCiphertexts bool; Bump []string}`
  - `model.ServerKey{ServerID, Name, PublicKey string}`, `model.ServerVarsInfo{ServerID, OrgID, Name string; Revision int64}`
  - `model.ErrSharedVariableNotFound`, `model.ErrServerNotFound`
  - `model.NotificationVarsSynced = "vars.synced"`, `model.NotificationVarsSyncFailed = "vars.sync_failed"`, `model.NotificationVarsReloaded = "vars.reloaded"`; `model.VarsSyncedPayload{ServerID string; Revision int64; Undecryptable []string}`, `model.VarsSyncFailedPayload{ServerID, Error string}`
  - `command.TypeVarsSync = "vars.sync"`, `command.TypeVarsReload = "vars.reload"`
  - `command.SharedVar{Name string; IsSecret bool; Value, Ciphertext string}`, `command.VarRef{Scope, Name string}`
  - `command.SyncVarsRequest{Revision int64; ServerID, ServerName, OrgID string; Org, Server []SharedVar}` + `Validate() error`; `command.SyncVarsResponse{Revision int64; Undecryptable []VarRef}`
  - `command.ReloadVarsRequest{Names, AppIDs []string}` + `Validate() error`; `command.ReloadVarsResponse{Queued bool}`
  - `command.VarsReloadedPayload{ServerID, AppID string; OK bool; Error string; Names []string}`
  - `command.AppStatus.StaleVars []string` (`stale_vars,omitempty`); `command.GetAppsStatusResponse.VarsRevision *int64` (`vars_revision,omitempty`)
  - `bus.EventVarsReloaded = "vars.reloaded"`

- [ ] **Step 1: Write the failing tests**

`internal/domain/model/shared_variable_test.go`:

```go
package model

import (
	"strings"
	"testing"
)

func TestValidateSharedVariableName(t *testing.T) {
	for _, ok := range []string{"SMTP_HOST", "_x", "a1", "smtp_pass", "Wf_lower_is_not_reserved"} {
		if err := ValidateSharedVariableName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	bad := map[string]string{
		"":                     "required",
		"1ABC":                 "letters, digits",
		"A-B":                  "letters, digits",
		"A B":                  "letters, digits",
		"COMPOSE_PROJECT_NAME": "reserved",
		"WF_APP_ID":            "reserved",
		"WF_":                  "reserved",
		strings.Repeat("A", 256): "longer than 255",
	}
	for name, want := range bad {
		err := ValidateSharedVariableName(name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want it to mention %q", name, err, want)
		}
	}
	if err := ValidateSharedVariableName(strings.Repeat("A", 255)); err != nil {
		t.Errorf("255 characters rejected: %v", err)
	}
}

func TestSharedVariableScope(t *testing.T) {
	srv := "s1"
	if (SharedVariable{}).Scope() != SharedScopeOrg || (SharedVariable{ServerID: &srv}).Scope() != SharedScopeServer {
		t.Fatal("scope wrong")
	}
}
```

`internal/domain/command/vars_test.go`:

```go
package command

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSyncVarsRequestValidate(t *testing.T) {
	ok := SyncVarsRequest{Revision: 3, ServerID: "s", Org: []SharedVar{
		{Name: "SMTP_HOST", Value: "mail"},
		{Name: "SMTP_PASS", IsSecret: true, Ciphertext: "CT"},
		{Name: "MISSING", IsSecret: true}, // no copy for this server yet
	}, Server: []SharedVar{{Name: "SMTP_HOST", Value: "local"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	bad := map[string]SyncVarsRequest{
		"negative revision": {Revision: -1},
		"reserved name":     {Org: []SharedVar{{Name: "WF_X", Value: "1"}}},
		"bad name":          {Server: []SharedVar{{Name: "1X", Value: "1"}}},
		"duplicate":         {Org: []SharedVar{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}}},
		"secret with value": {Org: []SharedVar{{Name: "A", IsSecret: true, Value: "plain"}}},
		"plain with ct":     {Org: []SharedVar{{Name: "A", Ciphertext: "CT"}}},
	}
	for name, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestReloadVarsRequestValidate(t *testing.T) {
	if err := (ReloadVarsRequest{AppIDs: []string{"a1"}, Names: []string{"SMTP_PASS"}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (ReloadVarsRequest{AppIDs: []string{"../x"}}).Validate(); err == nil {
		t.Fatal("path-like app id accepted")
	}
	if err := (ReloadVarsRequest{Names: []string{"bad name"}}).Validate(); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestVarsWireShapes(t *testing.T) {
	raw, _ := json.Marshal(SyncVarsRequest{Revision: 2, ServerID: "s", ServerName: "box", OrgID: "o",
		Org: []SharedVar{{Name: "A", Value: "1"}}, Server: []SharedVar{{Name: "B", IsSecret: true, Ciphertext: "CT"}}})
	for _, want := range []string{`"revision":2`, `"server_name":"box"`, `"org_id":"o"`, `"org":[{"name":"A","is_secret":false,"value":"1"}]`, `"ciphertext":"CT"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("sync request %s lacks %s", raw, want)
		}
	}
	raw, _ = json.Marshal(GetAppsStatusResponse{Apps: []AppStatus{{AppID: "a", StaleVars: []string{"X"}}}})
	if !strings.Contains(string(raw), `"stale_vars":["X"]`) || strings.Contains(string(raw), "vars_revision") {
		t.Errorf("status without revision = %s", raw)
	}
	rev := int64(0)
	raw, _ = json.Marshal(GetAppsStatusResponse{Apps: []AppStatus{}, VarsRevision: &rev})
	if !strings.Contains(string(raw), `"vars_revision":0`) {
		t.Errorf("a zero revision must still be sent: %s", raw)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-1; mkdir -p $GOTMPDIR; go test ./internal/domain/model/ ./internal/domain/command/`
Expected: FAIL: `undefined: ValidateSharedVariableName`, `undefined: SyncVarsRequest`, `unknown field StaleVars`.

- [ ] **Step 3: Implement**

`internal/domain/model/shared_variable.go`:

```go
package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SharedVariableNameMax is the longest accepted shared variable name (the
// column width).
const SharedVariableNameMax = 255

var sharedVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateSharedVariableName enforces the rule for org- and server-level
// variable names: an env-var identifier that is not reserved.
// COMPOSE_PROJECT_NAME only ever travels as --project-name, and every WF_
// name belongs to the agent's read-only built-ins.
func ValidateSharedVariableName(name string) error {
	switch {
	case name == "":
		return errors.New("name is required")
	case len(name) > SharedVariableNameMax:
		return fmt.Errorf("name is longer than %d characters", SharedVariableNameMax)
	case !sharedVariableName.MatchString(name):
		return fmt.Errorf("invalid name %q: use letters, digits and _, not starting with a digit", name)
	case name == "COMPOSE_PROJECT_NAME" || strings.HasPrefix(name, "WF_"):
		return fmt.Errorf("%s is reserved", name)
	}
	return nil
}

// Shared variable scopes as reported to clients.
const (
	SharedScopeOrg    = "org"
	SharedScopeServer = "server"
)

// SharedVariable is one org- or server-level variable. A secret carries no
// value here: its per-server ECIES ciphertexts live beside it, and
// CipherServers names the servers that hold one.
type SharedVariable struct {
	ID            string
	OrgID         string
	ServerID      *string // nil = org scope
	Name          string
	IsSecret      bool
	Value         string // plain variables only
	Description   string
	UpdatedAt     time.Time
	UpdatedBy     string
	CipherServers []string
}

// Scope is SharedScopeOrg or SharedScopeServer.
func (v SharedVariable) Scope() string {
	if v.ServerID == nil {
		return SharedScopeOrg
	}
	return SharedScopeServer
}

// SharedVariableSave is one atomic write: the variable row (inserted when
// Create, else value/description/updated_* updated), the ciphertexts to
// upsert (after deleting every stored one when ReplaceCiphertexts, i.e. a
// new value), and the servers whose vars_revision goes up.
type SharedVariableSave struct {
	Variable           SharedVariable
	Create             bool
	Ciphertexts        map[string]string // server_id -> ciphertext
	ReplaceCiphertexts bool
	Bump               []string
}

// ServerKey is one org server's ECIES public key; PublicKey is "" for a
// server that has never reported one (pending).
type ServerKey struct {
	ServerID  string
	Name      string
	PublicKey string
}

// ServerVarsInfo is what a vars.sync needs to know about its target.
type ServerVarsInfo struct {
	ServerID string
	OrgID    string
	Name     string
	Revision int64
}

var (
	// ErrSharedVariableNotFound is returned for an unknown variable id.
	ErrSharedVariableNotFound = errors.New("shared variable not found")
	// ErrServerNotFound is returned for an unknown server id.
	ErrServerNotFound = errors.New("server not found")
)
```

`internal/domain/model/notification.go`: add to the `NotificationType` const block, after `NotificationAppsStatus`:

```go
	// NotificationVarsSynced reports that a server's agent stored a
	// shared-variables snapshot (Payload is VarsSyncedPayload).
	NotificationVarsSynced NotificationType = "vars.synced"
	// NotificationVarsSyncFailed reports that an agent refused a snapshot
	// (Payload is VarsSyncFailedPayload).
	NotificationVarsSyncFailed NotificationType = "vars.sync_failed"
	// NotificationVarsReloaded is one app's shared-variable reload outcome
	// (Payload is command.VarsReloadedPayload).
	NotificationVarsReloaded NotificationType = "vars.reloaded"
```

and below `ServerStatusPayload`:

```go
// VarsSyncedPayload is the payload of a vars.synced notification:
// Undecryptable names the secrets the agent could not decrypt (their
// ciphertexts were dropped, so they now show as missing on this server).
type VarsSyncedPayload struct {
	ServerID      string   `json:"server_id"`
	Revision      int64    `json:"revision"`
	Undecryptable []string `json:"undecryptable"`
}

// VarsSyncFailedPayload is the payload of a vars.sync_failed notification.
type VarsSyncFailedPayload struct {
	ServerID string `json:"server_id"`
	Error    string `json:"error"`
}
```

`internal/domain/command/command.go`: add to the `Type` const block, before `// Container images.`:

```go
	// Shared variables (org- and server-level).
	TypeVarsSync   Type = "vars.sync"   // store the server's full shared-variables snapshot
	TypeVarsReload Type = "vars.reload" // re-render and compose up the selected apps
```

`internal/domain/command/vars.go`:

```go
package command

import (
	"fmt"

	"winterflow/internal/domain/model"
)

// SharedVar is one entry of a vars.sync snapshot. A plain entry carries
// Value; a secret carries Ciphertext (ECIES for the target server's key),
// or nothing when the browser has not encrypted it for this server yet —
// the agent then treats the name as missing and warns when an app uses it.
type SharedVar struct {
	Name       string `json:"name"`
	IsSecret   bool   `json:"is_secret"`
	Value      string `json:"value,omitempty"`
	Ciphertext string `json:"ciphertext,omitempty"`
}

// VarRef names one variable of a snapshot by scope ("org" or "server").
type VarRef struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
}

// SyncVarsRequest is vars.sync: the target server's full effective set of
// org- and server-level variables at Revision. The agent stores it as its
// snapshot (ignoring one older than the stored one) and renders nothing.
type SyncVarsRequest struct {
	Revision   int64       `json:"revision"`
	ServerID   string      `json:"server_id"`
	ServerName string      `json:"server_name"`
	OrgID      string      `json:"org_id"`
	Org        []SharedVar `json:"org"`
	Server     []SharedVar `json:"server"`
}

// Validate refuses names the agent would never render and malformed
// entries, so a bad snapshot is rejected before it replaces a good one.
func (r SyncVarsRequest) Validate() error {
	if r.Revision < 0 {
		return fmt.Errorf("invalid revision %d", r.Revision)
	}
	for _, scope := range []struct {
		name string
		vars []SharedVar
	}{{"org", r.Org}, {"server", r.Server}} {
		seen := map[string]bool{}
		for _, v := range scope.vars {
			if err := model.ValidateSharedVariableName(v.Name); err != nil {
				return fmt.Errorf("%s variable: %w", scope.name, err)
			}
			if seen[v.Name] {
				return fmt.Errorf("duplicate %s variable %s", scope.name, v.Name)
			}
			seen[v.Name] = true
			if v.IsSecret && v.Value != "" {
				return fmt.Errorf("secret %s carries a plain value", v.Name)
			}
			if !v.IsSecret && v.Ciphertext != "" {
				return fmt.Errorf("plain variable %s carries a ciphertext", v.Name)
			}
		}
	}
	return nil
}

// SyncVarsResponse reports the revision the agent now holds and the
// secrets it holds but cannot decrypt; the API drops those ciphertexts, so
// the names show as missing on this server.
type SyncVarsResponse struct {
	Revision      int64    `json:"revision"`
	Undecryptable []VarRef `json:"undecryptable"`
}

// ReloadVarsRequest is vars.reload: re-render and `compose up -d` running
// apps. AppIDs selects those apps; else Names selects the stale apps whose
// stale names include one of them; neither = every stale running app.
type ReloadVarsRequest struct {
	Names  []string `json:"names,omitempty"`
	AppIDs []string `json:"app_ids,omitempty"`
}

// Validate refuses app ids that are not single path elements and names
// that cannot be shared variables.
func (r ReloadVarsRequest) Validate() error {
	for _, id := range r.AppIDs {
		if err := validateAppID(id); err != nil {
			return err
		}
	}
	for _, n := range r.Names {
		if err := model.ValidateSharedVariableName(n); err != nil {
			return err
		}
	}
	return nil
}

// ReloadVarsResponse acknowledges a queued reload; each app's outcome
// arrives later as a vars.reloaded event.
type ReloadVarsResponse struct {
	Queued bool `json:"queued"`
}

// VarsReloadedPayload is one app's reload outcome: the body of the agent's
// vars.reloaded event and, once the API sets ServerID, the payload of the
// vars.reloaded SSE notification.
type VarsReloadedPayload struct {
	ServerID string   `json:"server_id"`
	AppID    string   `json:"app_id"`
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	Names    []string `json:"names"`
}
```

`internal/domain/command/app.go`: replace the `AppStatus` and `GetAppsStatusResponse` types with:

```go
type AppStatus struct {
	AppID      string              `json:"app_id"`
	StatusCode ContainerStatusCode `json:"status_code"`
	Containers []ContainerStatus   `json:"containers"`
	// StaleVars names the shared variables whose rendered values differ from
	// the agent's current snapshot; empty = fresh. A stale app needs a
	// vars.reload (or its next deploy) to pick the new values up.
	StaleVars []string `json:"stale_vars,omitempty"`
}

type GetAppsStatusRequest struct{}

type GetAppsStatusResponse struct {
	Apps []AppStatus `json:"apps"`
	// VarsRevision is the agent's shared-variables snapshot revision. Nil
	// from agents that predate shared variables: the API never drift-pushes
	// vars.sync to those.
	VarsRevision *int64 `json:"vars_revision,omitempty"`
}
```

`internal/infra/transport/bus/bus.go`: add to the `EventKind` const block:

```go
	// EventVarsReloaded reports one app's shared-variable reload outcome
	// (payload: command.VarsReloadedPayload without server_id).
	EventVarsReloaded EventKind = "vars.reloaded"
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/domain/... ./internal/infra/transport/... && go vet ./internal/domain/... ./internal/infra/transport/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/ && git add internal/domain internal/infra/transport/bus/bus.go
git commit -m "feat(domain): shared variable types, vars.sync/vars.reload commands

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Migration and sqlc queries

**Files:**
- Create: `internal/infra/db/migrations/sqlite/0010_shared_variables.sql`, `internal/infra/db/migrations/pgsql/0010_shared_variables.sql`
- Create: `internal/infra/db/sqlc/query/sqlite/shared_variables.sql`, `internal/infra/db/sqlc/query/pgsql/shared_variables.sql`
- Generated (by `make sqlc`, never by hand): `internal/infra/db/sqlc/gen/{sqlite,pgsql}/*`
- Modify: `internal/infra/db/sqlc/shapes_test.go`
- Test: `internal/infra/db/migrate` and `internal/infra/db` existing suites (the runner applies the new file on both engines)

**Interfaces:**
- Consumes: nothing.
- Produces (generated, identical on both engines — the SQL below was run through sqlc v1.30 against these migrations and both engines emitted identical shapes):
  - `sqlitegen.SharedVariable{ID, OrgID string; ServerID *string; Name string; IsSecret bool; Value *string; Description string; UpdatedAt types.DateTime; UpdatedBy string}`; `sqlitegen.SharedSecretCiphertext`; `sqlitegen.Server` gains `VarsRevision int64`
  - queries: `ListOrgSharedVariables(ctx, orgID string) ([]SharedVariable, error)`, `ListServerSharedVariables(ctx, serverID *string)`, `GetSharedVariable(ctx, id string)`, `GetOrgSharedVariableByName(ctx, GetOrgSharedVariableByNameParams{OrgID, Name})`, `GetServerSharedVariableByName(ctx, GetServerSharedVariableByNameParams{ServerID *string; Name string})`, `InsertSharedVariable(ctx, InsertSharedVariableParams)`, `UpdateSharedVariable(ctx, UpdateSharedVariableParams{Value *string; Description string; UpdatedAt types.DateTime; UpdatedBy, ID string})`, `DeleteSharedVariable(ctx, id string)`, `ListCiphertextServersByOrg(ctx, orgID string) ([]ListCiphertextServersByOrgRow{VariableID, ServerID}, error)`, `ListSharedCiphertextsForServer(ctx, serverID string) ([]ListSharedCiphertextsForServerRow{VariableID, Ciphertext}, error)`, `UpsertSharedCiphertext(ctx, UpsertSharedCiphertextParams{VariableID, ServerID, Ciphertext})`, `DeleteSharedCiphertextsByVariable(ctx, variableID string)`, `DeleteSharedCiphertext(ctx, DeleteSharedCiphertextParams{VariableID, ServerID})`, `DeleteSharedCiphertextsByServer(ctx, serverID string)`, `BumpServerVarsRevision(ctx, serverID string)`, `GetServerVarsInfo(ctx, serverID string) (GetServerVarsInfoRow{ServerID, OrganizationID, Name string; VarsRevision int64}, error)`, `ListOrgServers(ctx, organizationID string) ([]ListOrgServersRow{ServerID, Name}, error)`, `ListOrgPublicKeys(ctx, organizationID string) ([]ListOrgPublicKeysRow{ServerID, Value}, error)`

- [ ] **Step 1: Write the failing shape check**

Append inside the `var (...)` block of `internal/infra/db/sqlc/shapes_test.go` (keep alphabetical order where it is already alphabetical; position does not matter to the compiler):

```go
	_ = sqlitegen.SharedVariable(pgsqlgen.SharedVariable{})
	_ = pgsqlgen.SharedVariable(sqlitegen.SharedVariable{})
	_ = sqlitegen.SharedSecretCiphertext(pgsqlgen.SharedSecretCiphertext{})
	_ = pgsqlgen.SharedSecretCiphertext(sqlitegen.SharedSecretCiphertext{})
	_ = sqlitegen.GetOrgSharedVariableByNameParams(pgsqlgen.GetOrgSharedVariableByNameParams{})
	_ = pgsqlgen.GetOrgSharedVariableByNameParams(sqlitegen.GetOrgSharedVariableByNameParams{})
	_ = sqlitegen.GetServerSharedVariableByNameParams(pgsqlgen.GetServerSharedVariableByNameParams{})
	_ = pgsqlgen.GetServerSharedVariableByNameParams(sqlitegen.GetServerSharedVariableByNameParams{})
	_ = sqlitegen.InsertSharedVariableParams(pgsqlgen.InsertSharedVariableParams{})
	_ = pgsqlgen.InsertSharedVariableParams(sqlitegen.InsertSharedVariableParams{})
	_ = sqlitegen.UpdateSharedVariableParams(pgsqlgen.UpdateSharedVariableParams{})
	_ = pgsqlgen.UpdateSharedVariableParams(sqlitegen.UpdateSharedVariableParams{})
	_ = sqlitegen.ListCiphertextServersByOrgRow(pgsqlgen.ListCiphertextServersByOrgRow{})
	_ = pgsqlgen.ListCiphertextServersByOrgRow(sqlitegen.ListCiphertextServersByOrgRow{})
	_ = sqlitegen.ListSharedCiphertextsForServerRow(pgsqlgen.ListSharedCiphertextsForServerRow{})
	_ = pgsqlgen.ListSharedCiphertextsForServerRow(sqlitegen.ListSharedCiphertextsForServerRow{})
	_ = sqlitegen.UpsertSharedCiphertextParams(pgsqlgen.UpsertSharedCiphertextParams{})
	_ = pgsqlgen.UpsertSharedCiphertextParams(sqlitegen.UpsertSharedCiphertextParams{})
	_ = sqlitegen.DeleteSharedCiphertextParams(pgsqlgen.DeleteSharedCiphertextParams{})
	_ = pgsqlgen.DeleteSharedCiphertextParams(sqlitegen.DeleteSharedCiphertextParams{})
	_ = sqlitegen.GetServerVarsInfoRow(pgsqlgen.GetServerVarsInfoRow{})
	_ = pgsqlgen.GetServerVarsInfoRow(sqlitegen.GetServerVarsInfoRow{})
	_ = sqlitegen.ListOrgServersRow(pgsqlgen.ListOrgServersRow{})
	_ = pgsqlgen.ListOrgServersRow(sqlitegen.ListOrgServersRow{})
	_ = sqlitegen.ListOrgPublicKeysRow(pgsqlgen.ListOrgPublicKeysRow{})
	_ = pgsqlgen.ListOrgPublicKeysRow(sqlitegen.ListOrgPublicKeysRow{})
```

- [ ] **Step 2: Run it to verify it fails**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-2; mkdir -p $GOTMPDIR; go test ./internal/infra/db/sqlc/`
Expected: FAIL: `undefined: sqlitegen.SharedVariable` (and the other new types).

- [ ] **Step 3: Write the migrations and queries**

`internal/infra/db/migrations/sqlite/0010_shared_variables.sql`:

```sql
-- Org- and server-level shared variables. Plain values live here; a secret
-- keeps one ECIES ciphertext per server it was encrypted for (browser
-- fan-out; the API never sees plaintext). servers.vars_revision counts the
-- changes that affect a server, so a behind agent is pushed again.
-- Renumber this file at merge time if another branch took 0010.

ALTER TABLE servers ADD COLUMN vars_revision INTEGER NOT NULL DEFAULT 0;

CREATE TABLE shared_variables (
    id VARCHAR(36) PRIMARY KEY,
    org_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36),
    name VARCHAR(255) NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    value TEXT,
    description TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    updated_by VARCHAR(36) NOT NULL DEFAULT '',
    UNIQUE (org_id, server_id, name),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

-- NULLs are distinct in a UNIQUE constraint, so org scope (server_id NULL)
-- needs its own partial index.
CREATE UNIQUE INDEX idx_shared_variables_org_name ON shared_variables (org_id, name) WHERE server_id IS NULL;

CREATE TABLE shared_secret_ciphertexts (
    variable_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    ciphertext TEXT NOT NULL,
    PRIMARY KEY (variable_id, server_id),
    FOREIGN KEY (variable_id) REFERENCES shared_variables (id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_shared_secret_ciphertexts_server ON shared_secret_ciphertexts (server_id);
```

`internal/infra/db/migrations/pgsql/0010_shared_variables.sql` — identical text except the id columns are `UUID` (as every other Postgres id column): `id UUID PRIMARY KEY`, `org_id UUID NOT NULL`, `server_id UUID`, and in the second table `variable_id UUID NOT NULL`, `server_id UUID NOT NULL`. `updated_at` stays `TIMESTAMP` and `updated_by` stays `VARCHAR(36)` (it may be ''), so the generated types match SQLite's. Write it out in full:

```sql
-- Org- and server-level shared variables. Plain values live here; a secret
-- keeps one ECIES ciphertext per server it was encrypted for (browser
-- fan-out; the API never sees plaintext). servers.vars_revision counts the
-- changes that affect a server, so a behind agent is pushed again.
-- Renumber this file at merge time if another branch took 0010.

ALTER TABLE servers ADD COLUMN vars_revision INTEGER NOT NULL DEFAULT 0;

CREATE TABLE shared_variables (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    server_id UUID,
    name VARCHAR(255) NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    value TEXT,
    description TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    updated_by VARCHAR(36) NOT NULL DEFAULT '',
    UNIQUE (org_id, server_id, name),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

-- NULLs are distinct in a UNIQUE constraint, so org scope (server_id NULL)
-- needs its own partial index.
CREATE UNIQUE INDEX idx_shared_variables_org_name ON shared_variables (org_id, name) WHERE server_id IS NULL;

CREATE TABLE shared_secret_ciphertexts (
    variable_id UUID NOT NULL,
    server_id UUID NOT NULL,
    ciphertext TEXT NOT NULL,
    PRIMARY KEY (variable_id, server_id),
    FOREIGN KEY (variable_id) REFERENCES shared_variables (id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE
);

CREATE INDEX idx_shared_secret_ciphertexts_server ON shared_secret_ciphertexts (server_id);
```

`internal/infra/db/sqlc/query/sqlite/shared_variables.sql`:

```sql
-- Org- and server-level shared variables and their per-server ciphertexts.

-- name: ListOrgSharedVariables :many
SELECT * FROM shared_variables WHERE org_id = ? AND server_id IS NULL ORDER BY name ASC;

-- name: ListServerSharedVariables :many
SELECT * FROM shared_variables WHERE server_id = ? ORDER BY name ASC;

-- name: GetSharedVariable :one
SELECT * FROM shared_variables WHERE id = ?;

-- name: GetOrgSharedVariableByName :one
SELECT * FROM shared_variables WHERE org_id = ? AND server_id IS NULL AND name = ?;

-- name: GetServerSharedVariableByName :one
SELECT * FROM shared_variables WHERE server_id = ? AND name = ?;

-- name: InsertSharedVariable :exec
INSERT INTO shared_variables (id, org_id, server_id, name, is_secret, value, description, updated_at, updated_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateSharedVariable :exec
UPDATE shared_variables SET value = ?, description = ?, updated_at = ?, updated_by = ? WHERE id = ?;

-- name: DeleteSharedVariable :exec
DELETE FROM shared_variables WHERE id = ?;

-- name: ListCiphertextServersByOrg :many
SELECT c.variable_id, c.server_id
FROM shared_secret_ciphertexts AS c
JOIN shared_variables AS v ON v.id = c.variable_id
WHERE v.org_id = ?
ORDER BY c.variable_id ASC, c.server_id ASC;

-- name: ListSharedCiphertextsForServer :many
SELECT variable_id, ciphertext FROM shared_secret_ciphertexts WHERE server_id = ?;

-- name: UpsertSharedCiphertext :exec
INSERT INTO shared_secret_ciphertexts (variable_id, server_id, ciphertext)
VALUES (?, ?, ?)
ON CONFLICT (variable_id, server_id) DO UPDATE SET ciphertext = excluded.ciphertext;

-- name: DeleteSharedCiphertextsByVariable :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = ?;

-- name: DeleteSharedCiphertext :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = ? AND server_id = ?;

-- name: DeleteSharedCiphertextsByServer :exec
DELETE FROM shared_secret_ciphertexts WHERE server_id = ?;

-- name: BumpServerVarsRevision :exec
UPDATE servers SET vars_revision = vars_revision + 1 WHERE server_id = ?;

-- name: GetServerVarsInfo :one
SELECT server_id, organization_id, name, vars_revision FROM servers WHERE server_id = ?;

-- name: ListOrgServers :many
SELECT server_id, name FROM servers WHERE organization_id = ? ORDER BY created_at ASC, server_id ASC;

-- name: ListOrgPublicKeys :many
SELECT c.server_id, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id = ? AND c.name = 'public_key';
```

`internal/infra/db/sqlc/query/pgsql/shared_variables.sql`:

```sql
-- Org- and server-level shared variables and their per-server ciphertexts.

-- name: ListOrgSharedVariables :many
SELECT * FROM shared_variables WHERE org_id = $1 AND server_id IS NULL ORDER BY name ASC;

-- name: ListServerSharedVariables :many
SELECT * FROM shared_variables WHERE server_id = $1 ORDER BY name ASC;

-- name: GetSharedVariable :one
SELECT * FROM shared_variables WHERE id = $1;

-- name: GetOrgSharedVariableByName :one
SELECT * FROM shared_variables WHERE org_id = $1 AND server_id IS NULL AND name = $2;

-- name: GetServerSharedVariableByName :one
SELECT * FROM shared_variables WHERE server_id = $1 AND name = $2;

-- name: InsertSharedVariable :exec
INSERT INTO shared_variables (id, org_id, server_id, name, is_secret, value, description, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: UpdateSharedVariable :exec
UPDATE shared_variables SET value = $1, description = $2, updated_at = $3, updated_by = $4 WHERE id = $5;

-- name: DeleteSharedVariable :exec
DELETE FROM shared_variables WHERE id = $1;

-- name: ListCiphertextServersByOrg :many
SELECT c.variable_id, c.server_id
FROM shared_secret_ciphertexts AS c
JOIN shared_variables AS v ON v.id = c.variable_id
WHERE v.org_id = $1
ORDER BY c.variable_id ASC, c.server_id ASC;

-- name: ListSharedCiphertextsForServer :many
SELECT variable_id, ciphertext FROM shared_secret_ciphertexts WHERE server_id = $1;

-- name: UpsertSharedCiphertext :exec
INSERT INTO shared_secret_ciphertexts (variable_id, server_id, ciphertext)
VALUES ($1, $2, $3)
ON CONFLICT (variable_id, server_id) DO UPDATE SET ciphertext = EXCLUDED.ciphertext;

-- name: DeleteSharedCiphertextsByVariable :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = $1;

-- name: DeleteSharedCiphertext :exec
DELETE FROM shared_secret_ciphertexts WHERE variable_id = $1 AND server_id = $2;

-- name: DeleteSharedCiphertextsByServer :exec
DELETE FROM shared_secret_ciphertexts WHERE server_id = $1;

-- name: BumpServerVarsRevision :exec
UPDATE servers SET vars_revision = vars_revision + 1 WHERE server_id = $1;

-- name: GetServerVarsInfo :one
SELECT server_id, organization_id, name, vars_revision FROM servers WHERE server_id = $1;

-- name: ListOrgServers :many
SELECT server_id, name FROM servers WHERE organization_id = $1 ORDER BY created_at ASC, server_id ASC;

-- name: ListOrgPublicKeys :many
SELECT c.server_id, c.value
FROM server_capabilities AS c
JOIN servers AS s ON s.server_id = c.server_id
WHERE s.organization_id = $1 AND c.name = 'public_key';
```

Then regenerate: `make sqlc` (needs a C compiler; takes about a minute the first time).

- [ ] **Step 4: Run the checks to verify they pass**

Run: `go build ./... && go test ./internal/infra/db/... && make sqlc-check`
Expected: PASS (the migration runner applies 0010 on SQLite in `internal/infra/db` tests; `sqlc-check` reports the committed code is fresh). If Docker is available also run `make test-pg`: every Postgres test database runs the migrations, 0010 included. Expected: PASS. `TestEnginesShareVersions` and `TestVersionsAreASequence` in `internal/infra/db/migrate` already check that both engines carry 0010.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/db/migrations internal/infra/db/sqlc
git commit -m "feat(db): shared_variables tables and servers.vars_revision

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 3: Shared variable repository (both engines)

**Files:**
- Create: `internal/domain/port/sharedvar.go`
- Create: `internal/infra/db/repository/sharedvar/repo.go`, `sqlite.go`, `pgsql.go`, `repo_test.go`

**Interfaces:**
- Consumes: Task 1 model types; Task 2 generated queries.
- Produces: `port.SharedVariableRepository` and its implementation `sharedvar.New(tx *backend.TxManager, driver string, log *logger.Logger) (*sharedvar.Repo, error)`:

```go
type SharedVariableRepository interface {
	ListOrg(ctx context.Context, orgID string) ([]model.SharedVariable, error)          // org scope, by name, CipherServers filled
	ListServer(ctx context.Context, serverID string) ([]model.SharedVariable, error)    // server scope, by name, CipherServers filled
	Get(ctx context.Context, id string) (model.SharedVariable, error)                   // model.ErrSharedVariableNotFound
	FindByName(ctx context.Context, orgID string, serverID *string, name string) (model.SharedVariable, bool, error)
	Save(ctx context.Context, s model.SharedVariableSave) error                          // one transaction
	Delete(ctx context.Context, id string, bump []string) error                          // cascades ciphertexts; bumps
	CiphertextsForServer(ctx context.Context, serverID string) (map[string]string, error) // variable_id -> ciphertext
	DropCiphertexts(ctx context.Context, serverID string, variableIDs []string) error
	PurgeServerCiphertexts(ctx context.Context, serverID string) error                   // all rows of the server + bump
	ServerVarsInfo(ctx context.Context, serverID string) (model.ServerVarsInfo, error)   // model.ErrServerNotFound
	OrgServerKeys(ctx context.Context, orgID string) ([]model.ServerKey, error)          // every org server, created order; PublicKey "" = pending
}
```

- [ ] **Step 1: Write the failing tests**

`internal/infra/db/repository/sharedvar/repo_test.go`:

```go
package sharedvar

import (
	"errors"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/types"
)

var (
	org1, org2       = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2, srv3 = dbtest.ID(11), dbtest.ID(12), dbtest.ID(13)
)

// seedServer inserts a server of orgID (the org must exist).
func seedServer(t *testing.T, d *db.DB, orgID, serverID, name string) {
	t.Helper()
	dbtest.Exec(t, d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, ?, ?)`,
		serverID, orgID, name, types.NewDateTime())
}

func setKey(t *testing.T, d *db.DB, serverID, key string) {
	t.Helper()
	dbtest.Exec(t, d, `INSERT INTO server_capabilities (server_id, name, value, updated_at) VALUES (?, 'public_key', ?, ?)`,
		serverID, key, types.NewDateTime())
}

// newRepo: org1 owns srv1 (keyed) and srv2 (pending); org2 owns srv3.
func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
	// Same-second created_at ties break on server_id, so srv1 lists first.
	seedServer(t, d, org1, srv1, "alpha")
	seedServer(t, d, org1, srv2, "beta")
	seedServer(t, d, org2, srv3, "gamma")
	setKey(t, d, srv1, "KEY-1")
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func revision(t *testing.T, d *db.DB, serverID string) string {
	t.Helper()
	return dbtest.QueryString(t, d, `SELECT CAST(vars_revision AS TEXT) FROM servers WHERE server_id = ?`, serverID)
}

func ptr(s string) *string { return &s }

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestSaveListGetAndBump(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	plain := model.SharedVariable{ID: dbtest.ID(101), OrgID: org1, Name: "SMTP_HOST", Value: "mail.example", Description: "relay", UpdatedBy: "u1"}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: plain, Create: true, Bump: []string{srv1, srv2}}); err != nil {
		t.Fatal(err)
	}
	secret := model.SharedVariable{ID: dbtest.ID(102), OrgID: org1, Name: "SMTP_PASS", IsSecret: true}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: secret, Create: true,
		Ciphertexts: map[string]string{srv1: "CT-1"}, Bump: []string{srv1}}); err != nil {
		t.Fatal(err)
	}
	local := model.SharedVariable{ID: dbtest.ID(103), OrgID: org1, ServerID: ptr(srv1), Name: "SMTP_HOST", Value: "local"}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: local, Create: true, Bump: []string{srv1}}); err != nil {
		t.Fatal(err)
	}

	got, err := r.ListOrg(ctx, org1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "SMTP_HOST" || got[0].Value != "mail.example" || got[0].Description != "relay" ||
		got[0].UpdatedBy != "u1" || got[0].ServerID != nil || got[1].Name != "SMTP_PASS" || got[1].Value != "" ||
		len(got[1].CipherServers) != 1 || got[1].CipherServers[0] != srv1 {
		t.Fatalf("org list = %+v", got)
	}
	if got[0].UpdatedAt.IsZero() || got[0].UpdatedAt.Location() != time.UTC {
		t.Fatalf("updated_at = %v", got[0].UpdatedAt)
	}
	srvVars, err := r.ListServer(ctx, srv1)
	if err != nil || len(srvVars) != 1 || srvVars[0].Value != "local" || srvVars[0].Scope() != model.SharedScopeServer {
		t.Fatalf("server list = %+v, %v", srvVars, err)
	}
	if empty, err := r.ListServer(ctx, srv2); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty server list = %#v, %v", empty, err)
	}
	g, err := r.Get(ctx, secret.ID)
	if err != nil || !g.IsSecret || len(g.CipherServers) != 1 {
		t.Fatalf("get = %+v, %v", g, err)
	}
	if _, err := r.Get(ctx, dbtest.ID(999)); !errors.Is(err, model.ErrSharedVariableNotFound) {
		t.Fatalf("missing get err = %v", err)
	}
	if v, ok, err := r.FindByName(ctx, org1, nil, "SMTP_HOST"); err != nil || !ok || v.ID != plain.ID {
		t.Fatalf("find org = %+v %v %v", v, ok, err)
	}
	if v, ok, err := r.FindByName(ctx, org1, ptr(srv1), "SMTP_HOST"); err != nil || !ok || v.ID != local.ID {
		t.Fatalf("find server = %+v %v %v", v, ok, err)
	}
	if _, ok, err := r.FindByName(ctx, org1, nil, "NOPE"); err != nil || ok {
		t.Fatalf("find missing = %v %v", ok, err)
	}
	if revision(t, d, srv1) != "3" || revision(t, d, srv2) != "1" || revision(t, d, srv3) != "0" {
		t.Fatalf("revisions = %s %s %s", revision(t, d, srv1), revision(t, d, srv2), revision(t, d, srv3))
	}
}

func TestOrgScopeNamesAreUnique(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	save := func(id int, serverID *string) error {
		return r.Save(ctx, model.SharedVariableSave{Create: true, Variable: model.SharedVariable{
			ID: dbtest.ID(id), OrgID: org1, ServerID: serverID, Name: "DUP", Value: "x"}})
	}
	if err := save(201, nil); err != nil {
		t.Fatal(err)
	}
	if err := save(202, nil); err == nil {
		t.Fatal("a second org-scope DUP was accepted (partial unique index missing)")
	}
	if err := save(203, ptr(srv1)); err != nil {
		t.Fatalf("server scope may reuse an org name: %v", err)
	}
	if err := save(204, ptr(srv1)); err == nil {
		t.Fatal("a second DUP on the same server was accepted")
	}
	if err := save(205, ptr(srv2)); err != nil {
		t.Fatalf("another server may reuse the name: %v", err)
	}
}

func TestUpdateReplaceAndGapFill(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	v := model.SharedVariable{ID: dbtest.ID(301), OrgID: org1, Name: "TOKEN", IsSecret: true}
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, Create: true, Ciphertexts: map[string]string{srv1: "OLD-1"}}); err != nil {
		t.Fatal(err)
	}
	// Gap fill adds a row and keeps the existing one.
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, Ciphertexts: map[string]string{srv2: "NEW-2"}}); err != nil {
		t.Fatal(err)
	}
	cts, _ := r.CiphertextsForServer(ctx, srv1)
	if cts[v.ID] != "OLD-1" {
		t.Fatalf("gap fill overwrote srv1: %v", cts)
	}
	// A new value replaces every row.
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v, ReplaceCiphertexts: true, Ciphertexts: map[string]string{srv1: "ROT-1"}}); err != nil {
		t.Fatal(err)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv2); len(cts) != 0 {
		t.Fatalf("replace left srv2's copy: %v", cts)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv1); cts[v.ID] != "ROT-1" {
		t.Fatalf("replace = %v", cts)
	}
	// A description-only update keeps the ciphertexts.
	v.Description = "rotated monthly"
	if err := r.Save(ctx, model.SharedVariableSave{Variable: v}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx, v.ID)
	if got.Description != "rotated monthly" || len(got.CipherServers) != 1 {
		t.Fatalf("after description edit = %+v", got)
	}
}

func TestDeleteDropPurgeAndCascade(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	a := model.SharedVariable{ID: dbtest.ID(401), OrgID: org1, Name: "A", IsSecret: true}
	b := model.SharedVariable{ID: dbtest.ID(402), OrgID: org1, ServerID: ptr(srv2), Name: "B", IsSecret: true}
	for _, s := range []model.SharedVariableSave{
		{Variable: a, Create: true, Ciphertexts: map[string]string{srv1: "A1", srv2: "A2"}},
		{Variable: b, Create: true, Ciphertexts: map[string]string{srv2: "B2"}},
	} {
		if err := r.Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.DropCiphertexts(ctx, srv1, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv1); len(cts) != 0 {
		t.Fatalf("drop left %v", cts)
	}
	before := revision(t, d, srv2)
	if err := r.PurgeServerCiphertexts(ctx, srv2); err != nil {
		t.Fatal(err)
	}
	if cts, _ := r.CiphertextsForServer(ctx, srv2); len(cts) != 0 {
		t.Fatalf("purge left %v", cts)
	}
	if revision(t, d, srv2) == before {
		t.Fatal("purge did not bump the revision")
	}
	if err := r.Delete(ctx, a.ID, []string{srv1, srv2}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, a.ID); !errors.Is(err, model.ErrSharedVariableNotFound) {
		t.Fatalf("deleted variable still there: %v", err)
	}
	// Removing a server drops its server-scope variables.
	dbtest.Exec(t, d, `DELETE FROM servers WHERE server_id = ?`, srv2)
	if _, err := r.Get(ctx, b.ID); !errors.Is(err, model.ErrSharedVariableNotFound) {
		t.Fatalf("server-scope variable survived its server: %v", err)
	}
}

func TestServerInfoAndKeys(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	info, err := r.ServerVarsInfo(ctx, srv1)
	if err != nil || info.OrgID != org1 || info.Name != "alpha" || info.Revision != 0 {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if _, err := r.ServerVarsInfo(ctx, dbtest.ID(998)); !errors.Is(err, model.ErrServerNotFound) {
		t.Fatalf("missing server err = %v", err)
	}
	keys, err := r.OrgServerKeys(ctx, org1)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ServerID != srv1 || keys[0].PublicKey != "KEY-1" || keys[0].Name != "alpha" ||
		keys[1].ServerID != srv2 || keys[1].PublicKey != "" {
		t.Fatalf("keys = %+v", keys)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-3; mkdir -p $GOTMPDIR; go test ./internal/infra/db/repository/sharedvar/`
Expected: FAIL: `undefined: New`, `undefined: Repo`.

- [ ] **Step 3: Implement**

`internal/domain/port/sharedvar.go`:

```go
package port

import (
	"context"

	"winterflow/internal/domain/model"
)

// SharedVariableRepository persists org- and server-level shared variables,
// their per-server secret ciphertexts (the API never holds plaintext) and
// each server's vars_revision.
type SharedVariableRepository interface {
	// ListOrg returns the org-scope variables, sorted by name.
	ListOrg(ctx context.Context, orgID string) ([]model.SharedVariable, error)
	// ListServer returns the server-scope variables of serverID, sorted by name.
	ListServer(ctx context.Context, serverID string) ([]model.SharedVariable, error)
	// Get returns one variable; model.ErrSharedVariableNotFound when unknown.
	Get(ctx context.Context, id string) (model.SharedVariable, error)
	// FindByName looks a name up in one scope (serverID nil = org scope).
	FindByName(ctx context.Context, orgID string, serverID *string, name string) (model.SharedVariable, bool, error)
	// Save writes a variable, its ciphertexts and the revision bumps in one
	// transaction.
	Save(ctx context.Context, s model.SharedVariableSave) error
	// Delete removes a variable (its ciphertexts cascade) and bumps the
	// given servers' revisions, in one transaction.
	Delete(ctx context.Context, id string, bump []string) error
	// CiphertextsForServer maps variable id -> ciphertext for serverID.
	CiphertextsForServer(ctx context.Context, serverID string) (map[string]string, error)
	// DropCiphertexts deletes serverID's copies of the given variables.
	DropCiphertexts(ctx context.Context, serverID string, variableIDs []string) error
	// PurgeServerCiphertexts deletes every ciphertext of serverID (its key
	// changed) and bumps its revision.
	PurgeServerCiphertexts(ctx context.Context, serverID string) error
	// ServerVarsInfo returns the server's org, name and vars_revision;
	// model.ErrServerNotFound when unknown.
	ServerVarsInfo(ctx context.Context, serverID string) (model.ServerVarsInfo, error)
	// OrgServerKeys lists every server of the org in creation order with its
	// public_key capability ("" when it never reported one).
	OrgServerKeys(ctx context.Context, orgID string) ([]model.ServerKey, error)
}
```

`internal/infra/db/repository/sharedvar/repo.go`:

```go
// Package sharedvar implements port.SharedVariableRepository over the
// shared_variables and shared_secret_ciphertexts tables (and
// servers.vars_revision).
package sharedvar

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
	"winterflow/pkg/logger"
)

// Row and parameter types are SQLite's; pgsql.go converts Postgres's
// identically shaped ones.
type (
	varRow         = sqlitegen.SharedVariable
	insertParams   = sqlitegen.InsertSharedVariableParams
	updateParams   = sqlitegen.UpdateSharedVariableParams
	orgNameParams  = sqlitegen.GetOrgSharedVariableByNameParams
	srvNameParams  = sqlitegen.GetServerSharedVariableByNameParams
	ctServerRow    = sqlitegen.ListCiphertextServersByOrgRow
	ctRow          = sqlitegen.ListSharedCiphertextsForServerRow
	upsertCtParams = sqlitegen.UpsertSharedCiphertextParams
	deleteCtParams = sqlitegen.DeleteSharedCiphertextParams
	infoRow        = sqlitegen.GetServerVarsInfoRow
	orgServerRow   = sqlitegen.ListOrgServersRow
	pubKeyRow      = sqlitegen.ListOrgPublicKeysRow
)

type querier interface {
	ListOrgSharedVariables(ctx context.Context, db backend.DBTX, orgID string) ([]varRow, error)
	ListServerSharedVariables(ctx context.Context, db backend.DBTX, serverID *string) ([]varRow, error)
	GetSharedVariable(ctx context.Context, db backend.DBTX, id string) (varRow, error)
	GetOrgSharedVariableByName(ctx context.Context, db backend.DBTX, p orgNameParams) (varRow, error)
	GetServerSharedVariableByName(ctx context.Context, db backend.DBTX, p srvNameParams) (varRow, error)
	InsertSharedVariable(ctx context.Context, db backend.DBTX, p insertParams) error
	UpdateSharedVariable(ctx context.Context, db backend.DBTX, p updateParams) error
	DeleteSharedVariable(ctx context.Context, db backend.DBTX, id string) error
	ListCiphertextServersByOrg(ctx context.Context, db backend.DBTX, orgID string) ([]ctServerRow, error)
	ListSharedCiphertextsForServer(ctx context.Context, db backend.DBTX, serverID string) ([]ctRow, error)
	UpsertSharedCiphertext(ctx context.Context, db backend.DBTX, p upsertCtParams) error
	DeleteSharedCiphertextsByVariable(ctx context.Context, db backend.DBTX, variableID string) error
	DeleteSharedCiphertext(ctx context.Context, db backend.DBTX, p deleteCtParams) error
	DeleteSharedCiphertextsByServer(ctx context.Context, db backend.DBTX, serverID string) error
	BumpServerVarsRevision(ctx context.Context, db backend.DBTX, serverID string) error
	GetServerVarsInfo(ctx context.Context, db backend.DBTX, serverID string) (infoRow, error)
	ListOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]orgServerRow, error)
	ListOrgPublicKeys(ctx context.Context, db backend.DBTX, orgID string) ([]pubKeyRow, error)
}

// Repo is the shared variables repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.SharedVariableRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("sharedvar repository: unknown database driver %q", driver)
}

func (r *Repo) ListOrg(ctx context.Context, orgID string) ([]model.SharedVariable, error) {
	rows, err := r.q.ListOrgSharedVariables(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	return r.withCipherServers(ctx, orgID, rows)
}

func (r *Repo) ListServer(ctx context.Context, serverID string) ([]model.SharedVariable, error) {
	id := serverID
	rows, err := r.q.ListServerSharedVariables(ctx, r.tx.Querier(ctx), &id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []model.SharedVariable{}, nil
	}
	return r.withCipherServers(ctx, rows[0].OrgID, rows)
}

func (r *Repo) Get(ctx context.Context, id string) (model.SharedVariable, error) {
	row, err := r.q.GetSharedVariable(ctx, r.tx.Querier(ctx), id)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SharedVariable{}, model.ErrSharedVariableNotFound
	}
	if err != nil {
		return model.SharedVariable{}, err
	}
	out, err := r.withCipherServers(ctx, row.OrgID, []varRow{row})
	if err != nil {
		return model.SharedVariable{}, err
	}
	return out[0], nil
}

func (r *Repo) FindByName(ctx context.Context, orgID string, serverID *string, name string) (model.SharedVariable, bool, error) {
	var (
		row varRow
		err error
	)
	if serverID == nil {
		row, err = r.q.GetOrgSharedVariableByName(ctx, r.tx.Querier(ctx), orgNameParams{OrgID: orgID, Name: name})
	} else {
		row, err = r.q.GetServerSharedVariableByName(ctx, r.tx.Querier(ctx), srvNameParams{ServerID: serverID, Name: name})
	}
	if errors.Is(err, sql.ErrNoRows) {
		return model.SharedVariable{}, false, nil
	}
	if err != nil {
		return model.SharedVariable{}, false, err
	}
	if row.OrgID != orgID {
		return model.SharedVariable{}, false, nil
	}
	out, err := r.withCipherServers(ctx, orgID, []varRow{row})
	if err != nil {
		return model.SharedVariable{}, false, err
	}
	return out[0], true, nil
}

func (r *Repo) Save(ctx context.Context, s model.SharedVariableSave) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		db := r.tx.Querier(ctx)
		v := s.Variable
		at := v.UpdatedAt
		if at.IsZero() {
			at = time.Now()
		}
		updatedAt := types.DateTime(at.UTC())
		var value *string
		if !v.IsSecret {
			val := v.Value
			value = &val
		}
		if s.Create {
			if err := r.q.InsertSharedVariable(ctx, db, insertParams{
				ID: v.ID, OrgID: v.OrgID, ServerID: v.ServerID, Name: v.Name, IsSecret: v.IsSecret,
				Value: value, Description: v.Description, UpdatedAt: updatedAt, UpdatedBy: v.UpdatedBy,
			}); err != nil {
				return err
			}
		} else if err := r.q.UpdateSharedVariable(ctx, db, updateParams{
			Value: value, Description: v.Description, UpdatedAt: updatedAt, UpdatedBy: v.UpdatedBy, ID: v.ID,
		}); err != nil {
			return err
		}
		if s.ReplaceCiphertexts {
			if err := r.q.DeleteSharedCiphertextsByVariable(ctx, db, v.ID); err != nil {
				return err
			}
		}
		servers := make([]string, 0, len(s.Ciphertexts))
		for id := range s.Ciphertexts {
			servers = append(servers, id)
		}
		sort.Strings(servers)
		for _, serverID := range servers {
			if err := r.q.UpsertSharedCiphertext(ctx, db, upsertCtParams{VariableID: v.ID, ServerID: serverID, Ciphertext: s.Ciphertexts[serverID]}); err != nil {
				return err
			}
		}
		return r.bump(ctx, s.Bump)
	})
}

func (r *Repo) Delete(ctx context.Context, id string, bump []string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteSharedVariable(ctx, r.tx.Querier(ctx), id); err != nil {
			return err
		}
		return r.bump(ctx, bump)
	})
}

func (r *Repo) CiphertextsForServer(ctx context.Context, serverID string) (map[string]string, error) {
	rows, err := r.q.ListSharedCiphertextsForServer(ctx, r.tx.Querier(ctx), serverID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.VariableID] = row.Ciphertext
	}
	return out, nil
}

func (r *Repo) DropCiphertexts(ctx context.Context, serverID string, variableIDs []string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		for _, id := range variableIDs {
			if err := r.q.DeleteSharedCiphertext(ctx, r.tx.Querier(ctx), deleteCtParams{VariableID: id, ServerID: serverID}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) PurgeServerCiphertexts(ctx context.Context, serverID string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := r.q.DeleteSharedCiphertextsByServer(ctx, r.tx.Querier(ctx), serverID); err != nil {
			return err
		}
		return r.bump(ctx, []string{serverID})
	})
}

func (r *Repo) ServerVarsInfo(ctx context.Context, serverID string) (model.ServerVarsInfo, error) {
	row, err := r.q.GetServerVarsInfo(ctx, r.tx.Querier(ctx), serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ServerVarsInfo{}, model.ErrServerNotFound
	}
	if err != nil {
		return model.ServerVarsInfo{}, err
	}
	return model.ServerVarsInfo{ServerID: row.ServerID, OrgID: row.OrganizationID, Name: row.Name, Revision: row.VarsRevision}, nil
}

func (r *Repo) OrgServerKeys(ctx context.Context, orgID string) ([]model.ServerKey, error) {
	servers, err := r.q.ListOrgServers(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	keys, err := r.q.ListOrgPublicKeys(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	byServer := make(map[string]string, len(keys))
	for _, k := range keys {
		byServer[k.ServerID] = k.Value
	}
	out := make([]model.ServerKey, 0, len(servers))
	for _, s := range servers {
		out = append(out, model.ServerKey{ServerID: s.ServerID, Name: s.Name, PublicKey: byServer[s.ServerID]})
	}
	return out, nil
}

// bump raises vars_revision for each server, inside the caller's tx.
func (r *Repo) bump(ctx context.Context, servers []string) error {
	for _, id := range servers {
		if err := r.q.BumpServerVarsRevision(ctx, r.tx.Querier(ctx), id); err != nil {
			return err
		}
	}
	return nil
}

// withCipherServers hydrates rows and fills each one's CipherServers from
// one query over the org's ciphertext index.
func (r *Repo) withCipherServers(ctx context.Context, orgID string, rows []varRow) ([]model.SharedVariable, error) {
	cts, err := r.q.ListCiphertextServersByOrg(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	byVar := map[string][]string{}
	for _, c := range cts {
		byVar[c.VariableID] = append(byVar[c.VariableID], c.ServerID)
	}
	out := make([]model.SharedVariable, 0, len(rows))
	for _, row := range rows {
		v := hydrate(row)
		if servers := byVar[row.ID]; servers != nil {
			v.CipherServers = servers
		}
		out = append(out, v)
	}
	return out, nil
}

func hydrate(row varRow) model.SharedVariable {
	v := model.SharedVariable{
		ID: row.ID, OrgID: row.OrgID, ServerID: row.ServerID, Name: row.Name, IsSecret: row.IsSecret,
		Description: row.Description, UpdatedAt: row.UpdatedAt.Time().UTC(), UpdatedBy: row.UpdatedBy,
		CipherServers: []string{},
	}
	if row.Value != nil {
		v.Value = *row.Value
	}
	return v
}
```

`internal/infra/db/repository/sharedvar/sqlite.go`:

```go
package sharedvar

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) ListOrgSharedVariables(ctx context.Context, db backend.DBTX, orgID string) ([]varRow, error) {
	return sqlitegen.New(db).ListOrgSharedVariables(ctx, orgID)
}

func (sqliteQuerier) ListServerSharedVariables(ctx context.Context, db backend.DBTX, serverID *string) ([]varRow, error) {
	return sqlitegen.New(db).ListServerSharedVariables(ctx, serverID)
}

func (sqliteQuerier) GetSharedVariable(ctx context.Context, db backend.DBTX, id string) (varRow, error) {
	return sqlitegen.New(db).GetSharedVariable(ctx, id)
}

func (sqliteQuerier) GetOrgSharedVariableByName(ctx context.Context, db backend.DBTX, p orgNameParams) (varRow, error) {
	return sqlitegen.New(db).GetOrgSharedVariableByName(ctx, p)
}

func (sqliteQuerier) GetServerSharedVariableByName(ctx context.Context, db backend.DBTX, p srvNameParams) (varRow, error) {
	return sqlitegen.New(db).GetServerSharedVariableByName(ctx, p)
}

func (sqliteQuerier) InsertSharedVariable(ctx context.Context, db backend.DBTX, p insertParams) error {
	return sqlitegen.New(db).InsertSharedVariable(ctx, p)
}

func (sqliteQuerier) UpdateSharedVariable(ctx context.Context, db backend.DBTX, p updateParams) error {
	return sqlitegen.New(db).UpdateSharedVariable(ctx, p)
}

func (sqliteQuerier) DeleteSharedVariable(ctx context.Context, db backend.DBTX, id string) error {
	return sqlitegen.New(db).DeleteSharedVariable(ctx, id)
}

func (sqliteQuerier) ListCiphertextServersByOrg(ctx context.Context, db backend.DBTX, orgID string) ([]ctServerRow, error) {
	return sqlitegen.New(db).ListCiphertextServersByOrg(ctx, orgID)
}

func (sqliteQuerier) ListSharedCiphertextsForServer(ctx context.Context, db backend.DBTX, serverID string) ([]ctRow, error) {
	return sqlitegen.New(db).ListSharedCiphertextsForServer(ctx, serverID)
}

func (sqliteQuerier) UpsertSharedCiphertext(ctx context.Context, db backend.DBTX, p upsertCtParams) error {
	return sqlitegen.New(db).UpsertSharedCiphertext(ctx, p)
}

func (sqliteQuerier) DeleteSharedCiphertextsByVariable(ctx context.Context, db backend.DBTX, variableID string) error {
	return sqlitegen.New(db).DeleteSharedCiphertextsByVariable(ctx, variableID)
}

func (sqliteQuerier) DeleteSharedCiphertext(ctx context.Context, db backend.DBTX, p deleteCtParams) error {
	return sqlitegen.New(db).DeleteSharedCiphertext(ctx, p)
}

func (sqliteQuerier) DeleteSharedCiphertextsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).DeleteSharedCiphertextsByServer(ctx, serverID)
}

func (sqliteQuerier) BumpServerVarsRevision(ctx context.Context, db backend.DBTX, serverID string) error {
	return sqlitegen.New(db).BumpServerVarsRevision(ctx, serverID)
}

func (sqliteQuerier) GetServerVarsInfo(ctx context.Context, db backend.DBTX, serverID string) (infoRow, error) {
	return sqlitegen.New(db).GetServerVarsInfo(ctx, serverID)
}

func (sqliteQuerier) ListOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]orgServerRow, error) {
	return sqlitegen.New(db).ListOrgServers(ctx, orgID)
}

func (sqliteQuerier) ListOrgPublicKeys(ctx context.Context, db backend.DBTX, orgID string) ([]pubKeyRow, error) {
	return sqlitegen.New(db).ListOrgPublicKeys(ctx, orgID)
}
```

`internal/infra/db/repository/sharedvar/pgsql.go`:

```go
package sharedvar

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func varRows(rows []pgsqlgen.SharedVariable) []varRow {
	out := make([]varRow, len(rows))
	for i, row := range rows {
		out[i] = varRow(row)
	}
	return out
}

func (pgsqlQuerier) ListOrgSharedVariables(ctx context.Context, db backend.DBTX, orgID string) ([]varRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgSharedVariables(ctx, orgID)
	return varRows(rows), err
}

func (pgsqlQuerier) ListServerSharedVariables(ctx context.Context, db backend.DBTX, serverID *string) ([]varRow, error) {
	rows, err := pgsqlgen.New(db).ListServerSharedVariables(ctx, serverID)
	return varRows(rows), err
}

func (pgsqlQuerier) GetSharedVariable(ctx context.Context, db backend.DBTX, id string) (varRow, error) {
	row, err := pgsqlgen.New(db).GetSharedVariable(ctx, id)
	return varRow(row), err
}

func (pgsqlQuerier) GetOrgSharedVariableByName(ctx context.Context, db backend.DBTX, p orgNameParams) (varRow, error) {
	row, err := pgsqlgen.New(db).GetOrgSharedVariableByName(ctx, pgsqlgen.GetOrgSharedVariableByNameParams(p))
	return varRow(row), err
}

func (pgsqlQuerier) GetServerSharedVariableByName(ctx context.Context, db backend.DBTX, p srvNameParams) (varRow, error) {
	row, err := pgsqlgen.New(db).GetServerSharedVariableByName(ctx, pgsqlgen.GetServerSharedVariableByNameParams(p))
	return varRow(row), err
}

func (pgsqlQuerier) InsertSharedVariable(ctx context.Context, db backend.DBTX, p insertParams) error {
	return pgsqlgen.New(db).InsertSharedVariable(ctx, pgsqlgen.InsertSharedVariableParams(p))
}

func (pgsqlQuerier) UpdateSharedVariable(ctx context.Context, db backend.DBTX, p updateParams) error {
	return pgsqlgen.New(db).UpdateSharedVariable(ctx, pgsqlgen.UpdateSharedVariableParams(p))
}

func (pgsqlQuerier) DeleteSharedVariable(ctx context.Context, db backend.DBTX, id string) error {
	return pgsqlgen.New(db).DeleteSharedVariable(ctx, id)
}

func (pgsqlQuerier) ListCiphertextServersByOrg(ctx context.Context, db backend.DBTX, orgID string) ([]ctServerRow, error) {
	rows, err := pgsqlgen.New(db).ListCiphertextServersByOrg(ctx, orgID)
	out := make([]ctServerRow, len(rows))
	for i, row := range rows {
		out[i] = ctServerRow(row)
	}
	return out, err
}

func (pgsqlQuerier) ListSharedCiphertextsForServer(ctx context.Context, db backend.DBTX, serverID string) ([]ctRow, error) {
	rows, err := pgsqlgen.New(db).ListSharedCiphertextsForServer(ctx, serverID)
	out := make([]ctRow, len(rows))
	for i, row := range rows {
		out[i] = ctRow(row)
	}
	return out, err
}

func (pgsqlQuerier) UpsertSharedCiphertext(ctx context.Context, db backend.DBTX, p upsertCtParams) error {
	return pgsqlgen.New(db).UpsertSharedCiphertext(ctx, pgsqlgen.UpsertSharedCiphertextParams(p))
}

func (pgsqlQuerier) DeleteSharedCiphertextsByVariable(ctx context.Context, db backend.DBTX, variableID string) error {
	return pgsqlgen.New(db).DeleteSharedCiphertextsByVariable(ctx, variableID)
}

func (pgsqlQuerier) DeleteSharedCiphertext(ctx context.Context, db backend.DBTX, p deleteCtParams) error {
	return pgsqlgen.New(db).DeleteSharedCiphertext(ctx, pgsqlgen.DeleteSharedCiphertextParams(p))
}

func (pgsqlQuerier) DeleteSharedCiphertextsByServer(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).DeleteSharedCiphertextsByServer(ctx, serverID)
}

func (pgsqlQuerier) BumpServerVarsRevision(ctx context.Context, db backend.DBTX, serverID string) error {
	return pgsqlgen.New(db).BumpServerVarsRevision(ctx, serverID)
}

func (pgsqlQuerier) GetServerVarsInfo(ctx context.Context, db backend.DBTX, serverID string) (infoRow, error) {
	row, err := pgsqlgen.New(db).GetServerVarsInfo(ctx, serverID)
	return infoRow(row), err
}

func (pgsqlQuerier) ListOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]orgServerRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgServers(ctx, orgID)
	out := make([]orgServerRow, len(rows))
	for i, row := range rows {
		out[i] = orgServerRow(row)
	}
	return out, err
}

func (pgsqlQuerier) ListOrgPublicKeys(ctx context.Context, db backend.DBTX, orgID string) ([]pubKeyRow, error) {
	rows, err := pgsqlgen.New(db).ListOrgPublicKeys(ctx, orgID)
	out := make([]pubKeyRow, len(rows))
	for i, row := range rows {
		out[i] = pubKeyRow(row)
	}
	return out, err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/infra/db/repository/sharedvar/ -race && go vet ./internal/infra/db/...`
Expected: PASS on SQLite. With Docker: `make test-pg TEST_PG_ARGS='-run "TestSaveListGetAndBump|TestOrgScopeNamesAreUnique|TestUpdateReplaceAndGapFill|TestDeleteDropPurgeAndCascade|TestServerInfoAndKeys"'` (the script always runs the contract suite and `internal/infra/db/...`; extra args go to `go test`) → PASS on Postgres. Without Docker, note in the PR that Postgres ran in CI only.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/port/sharedvar.go internal/infra/db/repository/sharedvar
git commit -m "feat(db): shared variable repository for SQLite and Postgres

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 4: Agent vars package: snapshot store, resolver, reference scanner

**Files:**
- Create: `internal/infra/vars/store.go`, `internal/infra/vars/store_test.go`
- Create: `internal/infra/vars/resolve.go`, `internal/infra/vars/resolve_test.go`
- Create: `internal/infra/vars/refs.go`, `internal/infra/vars/refs_test.go`

**Interfaces:**
- Consumes: `command.SyncVarsRequest`, `command.SharedVar`, `command.VarRef` (Task 1).
- Produces (package `vars`, import path `winterflow/internal/infra/vars`; importers alias it `agentvars` because `vars` is a common local name):
  - `vars.NewStore(dir string) *Store`; `(*Store).Current() command.SyncVarsRequest`; `(*Store).Apply(snap command.SyncVarsRequest) (bool, error)`; snapshot path `filepath.Join(dir, "snapshot.json")`
  - `vars.ScopeBuiltin = "builtin"`, `vars.ScopeOrg = "org"`, `vars.ScopeServer = "server"`
  - `vars.Value{Value string; Secret bool; Scope string}`, `vars.Decrypter func(ciphertext string) (string, error)`
  - `vars.Resolution{Values map[string]Value; Undecryptable []command.VarRef; Missing []command.VarRef}`
  - `vars.Resolve(snap command.SyncVarsRequest, builtins map[string]string, decrypt Decrypter) Resolution`
  - `vars.ServerBuiltins(snap command.SyncVarsRequest, dataDir, tz string) map[string]string` (WF_SERVER_ID, WF_SERVER_NAME, WF_ORG_ID, WF_DATA_DIR, WF_TZ)
  - `vars.AppBuiltins(appID, slug, appDir string) map[string]string` (WF_APP_ID, WF_APP_SLUG, WF_APP_DIR)
  - `vars.References(data []byte, into map[string]bool)`

- [ ] **Step 1: Write the failing tests**

`internal/infra/vars/store_test.go`:

```go
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
	if !strings.Contains(string(raw), "CIPHERTEXT") || strings.Contains(string(raw), "plaintext") {
		t.Fatalf("snapshot = %s", raw)
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
```

`internal/infra/vars/resolve_test.go`:

```go
package vars

import (
	"errors"
	"reflect"
	"testing"

	"winterflow/internal/domain/command"
)

// fakeDecrypt "decrypts" ENC:<x> to <x> and fails on anything else.
func fakeDecrypt(ct string) (string, error) {
	if len(ct) > 4 && ct[:4] == "ENC:" {
		return ct[4:], nil
	}
	return "", errors.New("bad ciphertext")
}

func TestResolvePrecedenceAndShadowing(t *testing.T) {
	snap := command.SyncVarsRequest{
		Org: []command.SharedVar{
			{Name: "SMTP_HOST", Value: "org-mail"},
			{Name: "SMTP_PASS", IsSecret: true, Ciphertext: "ENC:org-pass"},
			{Name: "BASE", Value: "org.example"},
			{Name: "TOKEN", Value: "org-plain"},
		},
		Server: []command.SharedVar{
			{Name: "SMTP_HOST", Value: "local-mail"},                       // server beats org
			{Name: "BASE", IsSecret: true, Ciphertext: "ENC:secret-base"}, // a server secret shadows an org plain value
			{Name: "SMTP_PASS", Value: "plain-pass"},                       // and a server plain shadows an org secret
		},
	}
	res := Resolve(snap, map[string]string{"WF_SERVER_ID": "s1"}, fakeDecrypt)
	want := map[string]Value{
		"WF_SERVER_ID": {Value: "s1", Scope: ScopeBuiltin},
		"SMTP_HOST":    {Value: "local-mail", Scope: ScopeServer},
		"SMTP_PASS":    {Value: "plain-pass", Scope: ScopeServer},
		"BASE":         {Value: "secret-base", Secret: true, Scope: ScopeServer},
		"TOKEN":        {Value: "org-plain", Scope: ScopeOrg},
	}
	if !reflect.DeepEqual(res.Values, want) {
		t.Fatalf("values = %+v", res.Values)
	}
	if len(res.Undecryptable) != 0 || len(res.Missing) != 0 {
		t.Fatalf("unexpected problems: %+v", res)
	}
}

func TestResolveSkipsAndReportsBrokenSecrets(t *testing.T) {
	snap := command.SyncVarsRequest{
		Org: []command.SharedVar{
			{Name: "KEY", Value: "org-fallback"},
			{Name: "LOST", IsSecret: true},                      // no copy for this server
			{Name: "BROKEN", IsSecret: true, Ciphertext: "junk"}, // wrong key
		},
		Server: []command.SharedVar{{Name: "KEY", IsSecret: true, Ciphertext: "junk"}},
	}
	res := Resolve(snap, nil, fakeDecrypt)
	if v := res.Values["KEY"]; v.Value != "org-fallback" || v.Scope != ScopeOrg {
		t.Fatalf("a broken server secret must fall back to the org value, got %+v", v)
	}
	if _, ok := res.Values["LOST"]; ok {
		t.Fatal("missing secret resolved")
	}
	if _, ok := res.Values["BROKEN"]; ok {
		t.Fatal("undecryptable secret resolved")
	}
	wantUndec := []command.VarRef{{Scope: ScopeOrg, Name: "BROKEN"}, {Scope: ScopeServer, Name: "KEY"}}
	if !reflect.DeepEqual(res.Undecryptable, wantUndec) {
		t.Fatalf("undecryptable = %+v", res.Undecryptable)
	}
	if !reflect.DeepEqual(res.Missing, []command.VarRef{{Scope: ScopeOrg, Name: "LOST"}}) {
		t.Fatalf("missing = %+v", res.Missing)
	}
}

func TestBuiltins(t *testing.T) {
	srv := ServerBuiltins(command.SyncVarsRequest{ServerID: "s1", ServerName: "box", OrgID: "o1"}, "/opt/wf", "Europe/Berlin")
	want := map[string]string{"WF_SERVER_ID": "s1", "WF_SERVER_NAME": "box", "WF_ORG_ID": "o1", "WF_DATA_DIR": "/opt/wf", "WF_TZ": "Europe/Berlin"}
	if !reflect.DeepEqual(srv, want) {
		t.Fatalf("server builtins = %v", srv)
	}
	app := AppBuiltins("a1", "nextcloud", "/opt/wf/apps-data/a1")
	if !reflect.DeepEqual(app, map[string]string{"WF_APP_ID": "a1", "WF_APP_SLUG": "nextcloud", "WF_APP_DIR": "/opt/wf/apps-data/a1"}) {
		t.Fatalf("app builtins = %v", app)
	}
}
```

`internal/infra/vars/refs_test.go`:

```go
package vars

import (
	"reflect"
	"sort"
	"testing"
)

func refs(s string) []string {
	m := map[string]bool{}
	References([]byte(s), m)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestReferencesEveryInterpolationForm(t *testing.T) {
	src := `services:
  app:
    image: "nginx:${TAG:-latest}"
    environment:
      A: $PLAIN
      B: ${BRACED}
      C: ${COLON_DASH:-x}
      D: ${DASH-x}
      E: ${COLON_Q:?required}
      F: ${Q?required}
      G: ${COLON_PLUS:+set}
      H: ${PLUS+set}
      I: $$ESCAPED
      J: $${ESCAPED_BRACED}
      K: ${OUTER:-${INNER}}
      L: $$$AFTER_ESCAPE
      M: price 5$ and $1 and ${2X} and ${UNTERMINATED
      N: ${lower_case}
`
	want := []string{"AFTER_ESCAPE", "BRACED", "COLON_DASH", "COLON_PLUS", "COLON_Q", "DASH", "INNER", "OUTER",
		"PLAIN", "PLUS", "Q", "TAG", "lower_case"}
	if got := refs(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("refs = %v\nwant  %v", got, want)
	}
}

func TestReferencesInEnvValues(t *testing.T) {
	if got := refs("URL=https://${BASE_DOMAIN}/x\nPORT=8080\n# ${COMMENTED}\n"); !reflect.DeepEqual(got, []string{"BASE_DOMAIN", "COMMENTED"}) {
		t.Fatalf("refs = %v", got)
	}
}
```

(A commented-out `${COMMENTED}` still counts. Over-reporting a name costs at most one extra rendered entry; under-reporting would leave an app without its value.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-4; mkdir -p $GOTMPDIR; go test ./internal/infra/vars/`
Expected: FAIL: `undefined: NewStore`, `undefined: Resolve`, `undefined: References`.

- [ ] **Step 3: Implement**

`internal/infra/vars/store.go`:

```go
// Package vars is the agent's shared-variables state: the last vars.sync
// snapshot ({AGENT_DATA_DIR}/vars/snapshot.json, 0600, ciphertext only),
// the resolver that turns it into effective values, and the scanner that
// finds which names an app references.
package vars

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"winterflow/internal/domain/command"
)

// Store holds the snapshot in memory and on disk.
type Store struct {
	path   string
	mu     sync.Mutex
	cur    command.SyncVarsRequest
	loaded bool
}

// NewStore keeps the snapshot at dir/snapshot.json. Nothing is read until
// first use.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "snapshot.json")}
}

// Current returns the stored snapshot (zero before the first sync). A file
// that cannot be read or parsed counts as no snapshot; the next vars.sync
// replaces it.
func (s *Store) Current() command.SyncVarsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	return s.cur
}

func (s *Store) loadLocked() {
	if s.loaded {
		return
	}
	s.loaded = true
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var snap command.SyncVarsRequest
	if json.Unmarshal(raw, &snap) == nil {
		s.cur = snap
	}
}

// Apply stores snap unless it is older than the stored one, so a reconnect
// sync racing a change push cannot roll values back. It reports whether
// snap was stored.
func (s *Store) Apply(snap command.SyncVarsRequest) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	if snap.Revision < s.cur.Revision {
		return false, nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(s.path, raw, 0o600); err != nil {
		return false, err
	}
	s.cur = snap
	return true, nil
}

// writeFileAtomic writes data to a temp file beside path, fsyncs it and
// renames it over path, so a crash leaves either the old or the new file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // no-op after the rename
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
```

`internal/infra/vars/resolve.go`:

```go
package vars

import (
	"sort"

	"winterflow/internal/domain/command"
)

// Value scopes, lowest precedence first.
const (
	ScopeBuiltin = "builtin"
	ScopeOrg     = "org"
	ScopeServer  = "server"
)

// Value is one resolved variable.
type Value struct {
	Value  string
	Secret bool
	Scope  string
}

// Decrypter turns one ECIES ciphertext into plaintext with the agent key.
type Decrypter func(ciphertext string) (string, error)

// Resolution is a snapshot resolved for this server: the effective value
// of every name, the secrets that did not decrypt, and the secrets this
// server has no copy of.
type Resolution struct {
	Values        map[string]Value
	Undecryptable []command.VarRef
	Missing       []command.VarRef
}

// Resolve applies built-ins < org < server: a later scope replaces an
// earlier entry of the same name, whether either is plain or secret. A
// secret without a ciphertext (Missing) or one that does not decrypt
// (Undecryptable) is left out, as if its scope did not define it.
func Resolve(snap command.SyncVarsRequest, builtins map[string]string, decrypt Decrypter) Resolution {
	res := Resolution{Values: map[string]Value{}}
	for name, v := range builtins {
		res.Values[name] = Value{Value: v, Scope: ScopeBuiltin}
	}
	apply := func(scope string, list []command.SharedVar) {
		for _, v := range list {
			if !v.IsSecret {
				res.Values[v.Name] = Value{Value: v.Value, Scope: scope}
				continue
			}
			if v.Ciphertext == "" {
				res.Missing = append(res.Missing, command.VarRef{Scope: scope, Name: v.Name})
				continue
			}
			plain, err := decrypt(v.Ciphertext)
			if err != nil {
				res.Undecryptable = append(res.Undecryptable, command.VarRef{Scope: scope, Name: v.Name})
				continue
			}
			res.Values[v.Name] = Value{Value: plain, Secret: true, Scope: scope}
		}
	}
	apply(ScopeOrg, snap.Org)
	apply(ScopeServer, snap.Server)
	sortRefs(res.Undecryptable)
	sortRefs(res.Missing)
	return res
}

func sortRefs(refs []command.VarRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Scope < refs[j].Scope
	})
}

// ServerBuiltins are the read-only WF_ values every app on the server
// shares.
func ServerBuiltins(snap command.SyncVarsRequest, dataDir, tz string) map[string]string {
	return map[string]string{
		"WF_SERVER_ID":   snap.ServerID,
		"WF_SERVER_NAME": snap.ServerName,
		"WF_ORG_ID":      snap.OrgID,
		"WF_DATA_DIR":    dataDir,
		"WF_TZ":          tz,
	}
}

// AppBuiltins are the read-only WF_ values specific to one app.
func AppBuiltins(appID, slug, appDir string) map[string]string {
	return map[string]string{
		"WF_APP_ID":   appID,
		"WF_APP_SLUG": slug,
		"WF_APP_DIR":  appDir,
	}
}
```

`internal/infra/vars/refs.go`:

```go
package vars

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isNameChar(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }

// nameEnd returns the end of the identifier starting at i, or i when there
// is none.
func nameEnd(data []byte, i int) int {
	if i >= len(data) || !isNameStart(data[i]) {
		return i
	}
	j := i + 1
	for j < len(data) && isNameChar(data[j]) {
		j++
	}
	return j
}

// braceForm reports whether data[k:] continues a ${NAME form compose
// interpolates: '}' or one of :- - :? ? :+ +.
func braceForm(data []byte, k int) bool {
	if k >= len(data) {
		return false
	}
	switch data[k] {
	case '}', '-', '?', '+':
		return true
	case ':':
		return k+1 < len(data) && (data[k+1] == '-' || data[k+1] == '?' || data[k+1] == '+')
	}
	return false
}

// References adds to into every variable name data references: $NAME,
// ${NAME} and ${NAME:-x}, ${NAME-x}, ${NAME:?x}, ${NAME?x}, ${NAME:+x},
// ${NAME+x}. $$ is compose's escape for a literal $ and starts nothing.
// Names inside a default (${A:-${B}}) are found too.
func References(data []byte, into map[string]bool) {
	for i := 0; i+1 < len(data); i++ {
		if data[i] != '$' {
			continue
		}
		switch next := data[i+1]; {
		case next == '$':
			i++ // literal $
		case next == '{':
			end := nameEnd(data, i+2)
			if end > i+2 && braceForm(data, end) {
				into[string(data[i+2:end])] = true
			}
			i++ // keep scanning inside the braces: a default can reference more names
		default:
			end := nameEnd(data, i+1)
			if end > i+1 {
				into[string(data[i+1:end])] = true
				i = end - 1
			}
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/infra/vars/ -race && go vet ./internal/infra/vars/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/vars
git commit -m "feat(agent): shared-variables snapshot store, resolver and reference scanner

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: Compose env-file wiring, gitignore guard, literal quoting

**Files:**
- Modify: `internal/infra/orchestrator/docker_compose/store.go` (constants, gitignore list)
- Modify: `internal/infra/orchestrator/docker_compose/envfile.go` (`marshalSharedEnv`, `\$` in `parseEnv`)
- Modify: `internal/infra/orchestrator/docker_compose/gitrepo.go` (`ensureIgnored`, `gitHead`, guard in `gitCommitAll`)
- Modify: `internal/infra/orchestrator/docker_compose/operations.go` (`composeArgs`)
- Modify: `internal/infra/orchestrator/docker_compose/runhelper.go` (`runHelperScript`)
- Test: `envfile_test.go`, `gitrepo_test.go`, `runhelper_test.go`, `source_integration_unit_test.go` in the same package

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces (package `dockercompose`, used by Tasks 6–7):
  - constants `envSharedRel = ".env.shared"`, `envSharedSecretsRel = ".env.shared.secrets"`
  - `marshalSharedEnv(vars []resolvedItem) []byte` — every value double-quoted with `\\`, `\"`, `\n`, `\$` escapes (literal under compose interpolation)
  - `parseEnv` also unescapes `\$`
  - `ensureIgnored(dir string) error` — appends any of `.env.secrets`, `.env.shared`, `.env.shared.secrets` missing from `dir/.gitignore`; called first thing in `gitCommitAll`
  - `gitHead(dir string) (string, error)` — HEAD hash without walking history
  - `composeArgs` env-file order: `.env.shared`, `.env.shared.secrets` (each only when present), `.env`, `.env.secrets` (when present)

- [ ] **Step 1: Write the failing tests**

Append to `internal/infra/orchestrator/docker_compose/envfile_test.go`:

```go
// TestMarshalSharedEnvRoundTripsAwkwardValues: compose interpolates
// unquoted and double-quoted env-file values, so a shared value with $ must
// be escaped to arrive literally (checked against docker compose v5.5.1:
// "pa\$word" -> pa$word, "a\\b" -> a\b, "\n" -> newline).
func TestMarshalSharedEnvRoundTripsAwkwardValues(t *testing.T) {
	values := map[string]string{
		"DOLLAR":  "pa$word and ${NOT_A_REF}",
		"QUOTES":  `say "hi" it's`,
		"BACK":    `C:\temp\new`,
		"PEM":     "-----BEGIN KEY-----\nabc\n-----END KEY-----\n",
		"SPACES":  "  padded # not a comment  ",
		"EMPTY":   "",
		"WF_TZ":   "Europe/Berlin",
	}
	var items []resolvedItem
	for k, v := range values {
		items = append(items, resolvedItem{name: k, content: []byte(v)})
	}
	raw := marshalSharedEnv(items)
	if !strings.Contains(string(raw), `DOLLAR="pa\$word and \${NOT_A_REF}"`) {
		t.Fatalf("$ not escaped:\n%s", raw)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != len(values) || !sort.StringsAreSorted(lines) {
		t.Fatalf("want one sorted line per value:\n%s", raw)
	}
	if got := parseEnv(raw); !reflect.DeepEqual(got, values) {
		t.Fatalf("round trip:\n got %q\nwant %q", got, values)
	}
}
```

(add `"reflect"` and `"sort"` to that file's imports if missing.)

Append to `internal/infra/orchestrator/docker_compose/gitrepo_test.go`:

```go
// TestCommitNeverRecordsSharedEnvFiles: a rollback restores a .gitignore
// written before shared variables existed; the next commit must still keep
// the rendered plaintext out of history.
func TestCommitNeverRecordsSharedEnvFiles(t *testing.T) {
	dir := t.TempDir()
	if err := gitEnsure(dir); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, ".gitignore", ".env.secrets\n") // an old agent's .gitignore
	writeFileT(t, dir, "compose.yml", "services: {}\n")
	writeFileT(t, dir, ".env.shared", "SMTP_HOST=\"mail\"\n")
	writeFileT(t, dir, ".env.shared.secrets", "SMTP_PASS=\"plaintext\"\n")
	if _, err := gitCommitAll(dir, "rollback to old"); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := repo.Head()
	commit, _ := repo.CommitObject(head.Hash())
	tree, _ := commit.Tree()
	for _, f := range []string{".env.shared", ".env.shared.secrets"} {
		if _, err := tree.File(f); err == nil {
			t.Fatalf("%s was committed", f)
		}
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, want := range []string{".env.secrets", ".env.shared", ".env.shared.secrets"} {
		if !strings.Contains(string(gi), want+"\n") {
			t.Fatalf(".gitignore lacks %s: %q", want, gi)
		}
	}
	// Idempotent: a second commit does not append the lines again.
	if _, err := gitCommitAll(dir, "noop"); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(dir, ".gitignore")); string(again) != string(gi) {
		t.Fatalf(".gitignore grew: %q", again)
	}
	if h, err := gitHead(dir); err != nil || h != head.Hash().String() {
		t.Fatalf("gitHead = %q, %v; want %s", h, err, head.Hash())
	}
}
```

(add `git "github.com/go-git/go-git/v5"`, `"os"`, `"path/filepath"`, `"strings"` to its imports if missing.)

In `internal/infra/orchestrator/docker_compose/runhelper_test.go`, replace `TestRunHelperScript` with:

```go
func TestRunHelperScript(t *testing.T) {
	got := string(runHelperScript("wf-app-1", ""))
	for _, want := range []string{
		"#!/bin/sh\n",
		`cd "$(dirname "$0")/.." || exit 1`,
		// Prepended in reverse, so the final order is
		// .env.shared .env.shared.secrets .env .env.secrets <args>.
		`if [ -f .env.secrets ]; then set -- --env-file .env.secrets "$@"; fi`,
		`set -- --env-file .env "$@"`,
		`if [ -f .env.shared.secrets ]; then set -- --env-file .env.shared.secrets "$@"; fi`,
		`if [ -f .env.shared ]; then set -- --env-file .env.shared "$@"; fi`,
		`exec docker compose --project-name wf-app-1 "$@"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("script missing %q:\n%s", want, got)
		}
	}
	order := []string{".env.secrets \"$@\"", "--env-file .env \"$@\"", ".env.shared.secrets \"$@\"", "--env-file .env.shared \"$@\""}
	last := -1
	for _, o := range order {
		i := strings.Index(got, o)
		if i < last {
			t.Fatalf("prepend order wrong at %q:\n%s", o, got)
		}
		last = i
	}
	if strings.Contains(got, ` -f "`) {
		t.Fatalf("no compose file expected:\n%s", got)
	}
	withFile := string(runHelperScript("wf-app-1", "source/compose.yml"))
	if !strings.Contains(withFile, `exec docker compose --project-name wf-app-1 -f "source/compose.yml" "$@"`) {
		t.Fatalf("script missing -f flag:\n%s", withFile)
	}
}

// TestRunHelperScriptRunsWithEnvFileOrder executes the script with a fake
// docker on PATH and checks the exact argv it receives.
func TestRunHelperScriptRunsWithEnvFileOrder(t *testing.T) {
	app := t.TempDir()
	bin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(app, ".winterflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, runRel), runHelperScript("wf-a", ""), 0o755); err != nil {
		t.Fatal(err)
	}
	argv := filepath.Join(bin, "argv")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argv + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() string {
		cmd := exec.Command(filepath.Join(app, runRel), "up", "-d")
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run.sh: %v: %s", err, out)
		}
		raw, _ := os.ReadFile(argv)
		return strings.Join(strings.Fields(string(raw)), " ")
	}
	if got := run(); got != "compose --project-name wf-a --env-file .env up -d" {
		t.Fatalf("without optional files: %s", got)
	}
	for _, f := range []string{".env.shared", ".env.shared.secrets", ".env.secrets"} {
		if err := os.WriteFile(filepath.Join(app, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want := "compose --project-name wf-a --env-file .env.shared --env-file .env.shared.secrets --env-file .env --env-file .env.secrets up -d"
	if got := run(); got != want {
		t.Fatalf("with all files:\n got %s\nwant %s", got, want)
	}
}
```

(add `"os/exec"` to that file's imports.)

Append to `internal/infra/orchestrator/docker_compose/source_integration_unit_test.go`:

```go
func TestComposeArgsSharedEnvFileOrder(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")
	dir := r.appDataDir("app-1")
	for _, f := range []string{envSharedRel, envSharedSecretsRel, envSecretsRel} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("X=\"1\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := strings.Join(r.composeArgs("app-1", "up"), " ")
	want := "--env-file .env.shared --env-file .env.shared.secrets --env-file .env --env-file .env.secrets up"
	if !strings.HasSuffix(args, want) {
		t.Fatalf("args = %s\nwant suffix %s", args, want)
	}
	_ = os.Remove(filepath.Join(dir, envSharedSecretsRel))
	if args := strings.Join(r.composeArgs("app-1", "up"), " "); strings.Contains(args, envSharedSecretsRel) || !strings.Contains(args, "--env-file .env.shared --env-file .env ") {
		t.Fatalf("missing file not omitted: %s", args)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-5; mkdir -p $GOTMPDIR; go test ./internal/infra/orchestrator/docker_compose/ -run 'TestMarshalSharedEnv|TestCommitNeverRecords|TestRunHelperScript|TestComposeArgsSharedEnvFileOrder'`
Expected: FAIL: `undefined: marshalSharedEnv`, `undefined: envSharedRel`, `undefined: gitHead`; `TestRunHelperScript` misses the `set --` lines.

- [ ] **Step 3: Implement**

`store.go`: extend the committed-layout const block:

```go
	// Shared-variable deploy artifacts: rendered from the agent's snapshot,
	// gitignored (plaintext secrets live in the second one), never history.
	envSharedRel        = ".env.shared"
	envSharedSecretsRel = ".env.shared.secrets"
```

and in `writeAppStore` change the ignored list to:

```go
	ignored := []string{envSecretsRel, envSharedRel, envSharedSecretsRel, deployedRel, runRel}
```

`envfile.go`: add below `marshalEnv`:

```go
// marshalSharedEnv renders shared variables as an env file whose values
// compose reads literally: every value is double-quoted with \\, \", \n and
// \$ escapes, so a secret like pa$word is not interpolated. Sorted by name.
func marshalSharedEnv(vars []resolvedItem) []byte {
	sorted := append([]resolvedItem(nil), vars...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, `$`, `\$`)
	var b bytes.Buffer
	for _, v := range sorted {
		b.WriteString(v.name)
		b.WriteString(`="`)
		b.WriteString(esc.Replace(string(v.content)))
		b.WriteString("\"\n")
	}
	return b.Bytes()
}
```

and in `parseEnv` change the unescape replacer to also handle `\$`:

```go
			val = strings.NewReplacer("\\n", "\n", "\\\"", "\"", "\\$", "$", "\\\\", "\\").Replace(val[1 : len(val)-1])
```

`gitrepo.go`: add (with `"strings"` imported):

```go
// alwaysIgnored are deploy artifacts holding plaintext. They must never be
// committed, whatever the worktree's .gitignore says: a rollback restores
// a .gitignore written before some of them existed.
var alwaysIgnored = []string{envSecretsRel, envSharedRel, envSharedSecretsRel}

// ensureIgnored appends any of alwaysIgnored missing from dir/.gitignore.
func ensureIgnored(dir string) error {
	path := filepath.Join(dir, gitignoreRel)
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, p := range alwaysIgnored {
		if !have[p] {
			add = append(add, p)
		}
	}
	if len(add) == 0 {
		return nil
	}
	out := string(raw)
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out+strings.Join(add, "\n")+"\n"), 0o644)
}

// gitHead returns the HEAD commit hash without walking history.
func gitHead(dir string) (string, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}
```

and make `gitCommitAll` call the guard before staging:

```go
func gitCommitAll(dir, subject string) (string, error) {
	if err := ensureIgnored(dir); err != nil {
		return "", fmt.Errorf("guard .gitignore: %w", err)
	}
	repo, err := git.PlainOpen(dir)
	// ... unchanged from here
```

`operations.go`: replace the env-file part of `composeArgs`:

```go
// composeArgs builds the common CLI prefix. Env files are always explicit:
// with -f pointing into source/, compose's implicit project directory moves
// there and would stop auto-loading the app's committed .env. Later files
// override earlier ones: shared values first, so the app's own win.
func (r *Repository) composeArgs(appID string, verb ...string) []string {
	args := []string{"compose", "--project-name", projectName(appID)}
	dir := r.appDataDir(appID)
	if f := r.composeFile(dir, r.appSourceSpec(dir)); f != "" {
		args = append(args, "-f", f)
	}
	for _, f := range []string{envSharedRel, envSharedSecretsRel} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			args = append(args, "--env-file", f)
		}
	}
	args = append(args, "--env-file", envRel)
	if _, err := os.Stat(filepath.Join(dir, envSecretsRel)); err == nil {
		args = append(args, "--env-file", envSecretsRel)
	}
	return append(args, verb...)
}
```

`runhelper.go`: replace `runHelperScript`:

```go
// runHelperScript renders the helper for a project. composeFile is "" when
// compose auto-detects the winterflow-authored root compose file. Env files
// are prepended to "$@" in reverse, so compose sees the same order as
// composeArgs: .env.shared .env.shared.secrets .env .env.secrets.
func runHelperScript(project, composeFile string) []byte {
	cmd := "exec docker compose --project-name " + project
	if composeFile != "" {
		cmd += ` -f "` + composeFile + `"`
	}
	return []byte(`#!/bin/sh
# Generated by winterflow on every deploy — do not edit.
# Runs docker compose with the same flags winterflow uses, so a manual run
# controls the same stack. Usage: .winterflow/run.sh up -d | down | logs -f
cd "$(dirname "$0")/.." || exit 1
if [ -f .env.secrets ]; then set -- --env-file .env.secrets "$@"; fi
set -- --env-file .env "$@"
if [ -f .env.shared.secrets ]; then set -- --env-file .env.shared.secrets "$@"; fi
if [ -f .env.shared ]; then set -- --env-file .env.shared "$@"; fi
` + cmd + ` "$@"
`)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/infra/orchestrator/docker_compose/ -race`
Expected: PASS for the whole package: the new tests, plus the existing gitrepo, store and source tests (the guard appends lines to `.gitignore`, and those tests check with `Contains`). If an existing test compares a `.gitignore` byte-for-byte, update its expected text to include `.env.shared` and `.env.shared.secrets`.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/orchestrator/docker_compose
git commit -m "feat(agent): shared env files in compose args, run.sh and .gitignore

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 6: Agent rendering, reference cache, stale detection, `SyncVars`

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/shared.go`, `internal/infra/orchestrator/docker_compose/shared_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/repository.go` (fields, constructor, `forgetApp`)
- Modify: `internal/infra/orchestrator/docker_compose/store.go` (`materializeSecrets` ends with `renderShared`)
- Modify: `internal/infra/orchestrator/docker_compose/operations.go` (`GetAppsStatus` attaches `StaleVars`)

**Interfaces:**
- Consumes: Task 4 `agentvars.*`; Task 5 `envSharedRel`, `envSharedSecretsRel`, `marshalSharedEnv`, `ensureIgnored`, `gitHead`; existing `slugify`, `readSourceLock`, `loadSecretStore`, `parseEnv`, `crypto.DecryptWithPrivateKey`.
- Produces (methods on `*dockercompose.Repository`, used by Tasks 7–8):
  - `SyncVars(ctx context.Context, in command.SyncVarsRequest) (command.SyncVarsResponse, error)`
  - `VarsRevision() int64`
  - `StaleApps() map[string][]string` (copy; computed on first use)
  - `RecomputeStale()`
  - unexported: `renderShared(appID string) error`, `appRefs(appID string) map[string]bool`, `withStale([]command.AppStatus) []command.AppStatus`

- [ ] **Step 1: Write the failing tests**

`internal/infra/orchestrator/docker_compose/shared_test.go`:

```go
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
	if len(got[0].StaleVars) == 0 || got[1].StaleVars != nil {
		t.Fatalf("withStale = %+v", got)
	}
}
```

(`writeSourceLock` exists in `source.go`. `newSecretRepo` and `cryptotest` are already used by `store_test.go`.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-6; mkdir -p $GOTMPDIR; go test ./internal/infra/orchestrator/docker_compose/ -run 'TestRender|TestSync|TestStale|TestRefsCache|TestWithStale'`
Expected: FAIL: `r.SyncVars undefined`, `r.appRefs undefined`, `r.refMu undefined`.

- [ ] **Step 3: Implement**

`repository.go`: add imports `"path/filepath"` and `agentvars "winterflow/internal/infra/vars"`, then extend the struct, constructor and `forgetApp`:

```go
type Repository struct {
	cfg *config.ServerConfig
	log *logger.Logger

	sourceMu     sync.Mutex
	sourceChecks map[string]time.Time

	versionMu    sync.Mutex
	versionCache map[string]versionEntry

	// vars is the last vars.sync snapshot ({data}/vars/snapshot.json); tz is
	// the host timezone for WF_TZ, read once.
	vars *agentvars.Store
	tz   string

	// refCache holds each app's referenced shared names keyed by HEAD +
	// source.lock, so stale checks rescan only apps that changed.
	refMu    sync.Mutex
	refCache map[string]refEntry

	// stale is the last computed stale set (app id -> names); staleReady is
	// false until the first computation after a start.
	staleMu    sync.Mutex
	stale      map[string][]string
	staleReady bool
}

func NewRepository(cfg *config.ServerConfig, log *logger.Logger) *Repository {
	return &Repository{
		cfg:          cfg,
		log:          log,
		sourceChecks: make(map[string]time.Time),
		versionCache: make(map[string]versionEntry),
		vars:         agentvars.NewStore(filepath.Join(cfg.GetAgentDataDir(), "vars")),
		tz:           hostTimezone(),
		refCache:     make(map[string]refEntry),
		stale:        make(map[string][]string),
	}
}

// forgetApp drops an app's in-memory bookkeeping after deletion.
func (r *Repository) forgetApp(appID string) {
	r.sourceMu.Lock()
	delete(r.sourceChecks, appID)
	r.sourceMu.Unlock()
	r.versionMu.Lock()
	delete(r.versionCache, appID)
	r.versionMu.Unlock()
	r.refMu.Lock()
	delete(r.refCache, appID)
	r.refMu.Unlock()
	r.staleMu.Lock()
	delete(r.stale, appID)
	r.staleMu.Unlock()
}
```

Also update the package doc comment's layout block in `repository.go` (keep the rest):

```go
//	vars/snapshot.json           -- last vars.sync (0600, ciphertext only)
//	apps-data/{appID}/           -- canonical app folder, a git repository:
//	  ...
//	  .env.secrets, source/      --   gitignored deploy artifacts
//	  .env.shared(.secrets)      --   gitignored, rendered shared variables
```

`store.go`: the last statement of `materializeSecrets` becomes the shared render (replace its final `return nil`):

```go
	// Shared variables render on exactly the paths that materialize the
	// app's own secrets (save, start, update, rollback, source update).
	return r.renderShared(filepath.Base(dir))
}
```

`shared.go`:

```go
package dockercompose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
	agentvars "winterflow/internal/infra/vars"
	"winterflow/pkg/crypto"
)

// refEntry is one app's cached referenced-name set.
type refEntry struct {
	key  string
	refs map[string]bool
}

// composeCandidates are the files compose loads from the app dir when no
// -f is given (base files plus overrides); scanning every existing one
// over-reports at worst.
var composeCandidates = []string{
	"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml",
	"compose.override.yaml", "compose.override.yml", "docker-compose.override.yaml", "docker-compose.override.yml",
}

// hostTimezone names the host's IANA zone for WF_TZ: /etc/timezone, else
// the /etc/localtime link target, else "UTC". ($TZ is deliberately not
// read: no new env var.)
func hostTimezone() string {
	if raw, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(raw)); tz != "" {
			return tz
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			return target[i+len("zoneinfo/"):]
		}
	}
	return "UTC"
}

// refKey identifies the inputs of a reference scan: HEAD (compose file,
// .env and secrets.json are committed) plus the pinned upstream SHA (a
// git-sourced app's compose file lives in the uncommitted source/).
func refKey(dir string) (string, error) {
	head, err := gitHead(dir)
	if err != nil {
		return "", err
	}
	lock, _ := readSourceLock(dir)
	return head + "|" + lock.SHA, nil
}

// appRefs returns the shared names appID references: names used in the
// compose file it runs or in its committed .env, minus the names it
// defines itself (its own value wins anyway). Cached by refKey; a folder
// that is not a repository (yet) is scanned uncached.
func (r *Repository) appRefs(appID string) map[string]bool {
	dir := r.appDataDir(appID)
	key, err := refKey(dir)
	if err == nil {
		r.refMu.Lock()
		e, ok := r.refCache[appID]
		r.refMu.Unlock()
		if ok && e.key == key {
			return e.refs
		}
	}
	refs := r.scanRefs(dir)
	if err == nil {
		r.refMu.Lock()
		r.refCache[appID] = refEntry{key: key, refs: refs}
		r.refMu.Unlock()
	}
	return refs
}

func (r *Repository) scanRefs(dir string) map[string]bool {
	refs := map[string]bool{}
	files := []string{envRel}
	if f := r.composeFile(dir, r.appSourceSpec(dir)); f != "" {
		files = append(files, f)
	} else {
		files = append(files, composeCandidates...)
	}
	for _, f := range files {
		if raw, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			agentvars.References(raw, refs)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, envRel)); err == nil {
		for name := range parseEnv(raw) {
			delete(refs, name)
		}
	}
	for name := range loadSecretStore(dir).Variables {
		delete(refs, name)
	}
	return refs
}

// sharedResolution resolves the current snapshot with this server's
// built-ins, decrypting secrets with the agent key.
func (r *Repository) sharedResolution() agentvars.Resolution {
	snap := r.vars.Current()
	dataDir, err := filepath.Abs(r.cfg.GetAgentDataDir())
	if err != nil {
		dataDir = r.cfg.GetAgentDataDir()
	}
	return agentvars.Resolve(snap, agentvars.ServerBuiltins(snap, dataDir, r.tz), func(ct string) (string, error) {
		return crypto.DecryptWithPrivateKey(r.cfg.GetAgentKeyPath(), ct)
	})
}

// desiredShared is what appID's two shared files should hold under res:
// every built-in, plus each referenced name res defines, split by secrecy.
func (r *Repository) desiredShared(appID string, res agentvars.Resolution, refs map[string]bool) (plain, secret map[string]string) {
	dir := r.appDataDir(appID)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	plain, secret = map[string]string{}, map[string]string{}
	for name, v := range res.Values {
		if v.Scope == agentvars.ScopeBuiltin {
			plain[name] = v.Value
		}
	}
	for name, v := range agentvars.AppBuiltins(appID, slugify(r.appName(dir, appID)), absDir) {
		plain[name] = v
	}
	for name := range refs {
		v, ok := res.Values[name]
		if !ok || v.Scope == agentvars.ScopeBuiltin {
			continue
		}
		if v.Secret {
			secret[name] = v.Value
		} else {
			plain[name] = v.Value
		}
	}
	return plain, secret
}

// renderShared writes appID's .env.shared (0644) and .env.shared.secrets
// (0600, removed when empty) from the current snapshot and marks the app
// fresh. Referenced secrets this server has no usable copy of are logged
// by name only; compose then sees them unset (or their default).
func (r *Repository) renderShared(appID string) error {
	dir := r.appDataDir(appID)
	refs := r.appRefs(appID)
	res := r.sharedResolution()
	plain, secret := r.desiredShared(appID, res, refs)
	for _, ref := range append(append([]command.VarRef{}, res.Missing...), res.Undecryptable...) {
		if _, resolved := res.Values[ref.Name]; refs[ref.Name] && !resolved {
			r.log.Warn("shared secret not available on this server; compose sees it unset",
				"app_id", appID, "name", ref.Name, "scope", ref.Scope)
		}
	}
	if err := ensureIgnored(dir); err != nil {
		return fmt.Errorf("guard .gitignore: %w", err)
	}
	if err := writeSharedEnv(filepath.Join(dir, envSharedRel), plain, 0o644); err != nil {
		return err
	}
	secretsPath := filepath.Join(dir, envSharedSecretsRel)
	if len(secret) == 0 {
		if err := os.Remove(secretsPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := writeSharedEnv(secretsPath, secret, 0o600); err != nil {
		return err
	}
	r.setStale(appID, nil)
	return nil
}

func writeSharedEnv(path string, values map[string]string, perm os.FileMode) error {
	items := make([]resolvedItem, 0, len(values))
	for name, v := range values {
		items = append(items, resolvedItem{name: name, content: []byte(v)})
	}
	if err := os.WriteFile(path, marshalSharedEnv(items), perm); err != nil {
		return err
	}
	return os.Chmod(path, perm) // WriteFile's perm only applies on create
}

// staleNames compares appID's rendered shared files with what res would
// render, for the names the app references only — so an unreferenced
// change, a re-encrypted secret or a never-rendered app that references
// nothing stays fresh. Secrets are compared in memory, never hashed to disk.
func (r *Repository) staleNames(appID string, res agentvars.Resolution) []string {
	dir := r.appDataDir(appID)
	refs := r.appRefs(appID)
	plain, secret := r.desiredShared(appID, res, refs)
	onDisk := map[string]string{}
	for _, rel := range []string{envSharedRel, envSharedSecretsRel} {
		if raw, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
			for k, v := range parseEnv(raw) {
				onDisk[k] = v
			}
		}
	}
	var names []string
	for name := range refs {
		want, inWant := plain[name]
		if !inWant {
			want, inWant = secret[name]
		}
		have, inHave := onDisk[name]
		if inWant != inHave || want != have {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (r *Repository) setStale(appID string, names []string) {
	r.staleMu.Lock()
	defer r.staleMu.Unlock()
	if len(names) == 0 {
		delete(r.stale, appID)
		return
	}
	r.stale[appID] = names
}

// RecomputeStale rebuilds the stale set for every app from the current
// snapshot. File reads only: no process is forked and reference scans are
// cached by HEAD.
func (r *Repository) RecomputeStale() {
	next := map[string][]string{}
	if entries, err := os.ReadDir(r.cfg.GetAppsDataDir()); err == nil {
		res := r.sharedResolution()
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if names := r.staleNames(e.Name(), res); len(names) > 0 {
				next[e.Name()] = names
			}
		}
	}
	r.staleMu.Lock()
	r.stale = next
	r.staleReady = true
	r.staleMu.Unlock()
}

// StaleApps returns a copy of the stale set (app id -> stale names),
// computing it on first use after a start.
func (r *Repository) StaleApps() map[string][]string {
	r.staleMu.Lock()
	ready := r.staleReady
	r.staleMu.Unlock()
	if !ready {
		r.RecomputeStale()
	}
	r.staleMu.Lock()
	defer r.staleMu.Unlock()
	out := make(map[string][]string, len(r.stale))
	for k, v := range r.stale {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// withStale attaches each app's stale names to a status report.
func (r *Repository) withStale(apps []command.AppStatus) []command.AppStatus {
	stale := r.StaleApps()
	for i := range apps {
		apps[i].StaleVars = stale[apps[i].AppID]
	}
	return apps
}

// SyncVars stores a vars.sync snapshot (an older revision is ignored),
// reports the secrets it cannot decrypt and recomputes which apps are
// stale. It renders nothing: running apps keep their values until a
// deploy or a vars.reload.
func (r *Repository) SyncVars(_ context.Context, in command.SyncVarsRequest) (command.SyncVarsResponse, error) {
	applied, err := r.vars.Apply(in)
	if err != nil {
		return command.SyncVarsResponse{}, fmt.Errorf("store shared variables: %w", err)
	}
	if !applied {
		r.log.Info("ignored an older shared-variables snapshot", "revision", in.Revision, "stored", r.vars.Current().Revision)
	}
	undecryptable := r.sharedResolution().Undecryptable
	if undecryptable == nil {
		undecryptable = []command.VarRef{}
	}
	r.RecomputeStale()
	return command.SyncVarsResponse{Revision: r.vars.Current().Revision, Undecryptable: undecryptable}, nil
}

// VarsRevision is the stored snapshot's revision (0 before the first sync).
func (r *Repository) VarsRevision() int64 { return r.vars.Current().Revision }
```

`operations.go`: in `GetAppsStatus`, return the list through `withStale`:

```go
	return r.withStale(out), nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/infra/orchestrator/docker_compose/ -race && go vet ./internal/infra/orchestrator/...`
Expected: PASS, the whole package included. Existing tests that call `materializeSecrets` now also get `.env.shared`. If a test asserts the exact set of files in an app dir, add `.env.shared` to its expectation.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/orchestrator/docker_compose
git commit -m "feat(agent): render referenced shared variables and track stale apps

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Per-app lock, reload candidates and the reload itself

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/applock.go`, `internal/infra/orchestrator/docker_compose/applock_test.go`
- Create: `internal/infra/orchestrator/docker_compose/reload.go`, `internal/infra/orchestrator/docker_compose/reload_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/repository.go` (lock fields, constructor, `forgetApp`)
- Modify: `internal/infra/orchestrator/docker_compose/lifecycle.go`, `operations.go` (take the lock in public mutators)

**Interfaces:**
- Consumes: Task 6 `StaleApps`, `renderShared`; existing `composeUp`, `dockerPSByProject`, `readDeployedMark`, `wfProjectPrefix`; Task 5 `gitHead`.
- Produces (used by Task 8's worker through an interface):
  - `(*Repository).ReloadCandidates(ctx context.Context, req command.ReloadVarsRequest) ([]string, error)`
  - `(*Repository).ReloadSharedVars(ctx context.Context, appID string) ([]string, error)` — returns the names that were stale before the reload
  - `dockercompose.ErrDraftPending`
  - unexported: `lockApp(appID string) func()`, `runningApps(ctx) (map[string]bool, error)`, `pickCandidates(req, running, stale, exists) []string`

- [ ] **Step 1: Write the failing tests**

`internal/infra/orchestrator/docker_compose/applock_test.go`:

```go
package dockercompose

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLockAppSerializesOneAppOnly(t *testing.T) {
	r := newTestRepo(t)
	var inside, maxInside int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer r.lockApp("a")()
			n := atomic.AddInt32(&inside, 1)
			for {
				m := atomic.LoadInt32(&maxInside)
				if n <= m || atomic.CompareAndSwapInt32(&maxInside, m, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
		}()
	}
	wg.Wait()
	if maxInside != 1 {
		t.Fatalf("%d holders of one app's lock at once", maxInside)
	}
	// Another app's lock is independent.
	unlockA := r.lockApp("a")
	done := make(chan struct{})
	go func() { r.lockApp("b")(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("app b waited for app a")
	}
	unlockA()
	r.forgetApp("a")
	r.lockMu.Lock()
	_, kept := r.appLocks["a"]
	r.lockMu.Unlock()
	if kept {
		t.Fatal("forgetApp kept the lock entry")
	}
}
```

`internal/infra/orchestrator/docker_compose/reload_test.go`:

```go
package dockercompose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"winterflow/internal/domain/command"
)

func TestPickCandidates(t *testing.T) {
	running := map[string]bool{"a": true, "b": true, "c": true}
	stale := map[string][]string{"a": {"SMTP_PASS"}, "b": {"S3_KEY"}, "d": {"SMTP_PASS"}} // d is stopped
	exists := func(id string) bool { return id != "gone" }
	cases := map[string]struct {
		req  command.ReloadVarsRequest
		want []string
	}{
		"every stale running app":       {command.ReloadVarsRequest{}, []string{"a", "b"}},
		"stale apps using a name":       {command.ReloadVarsRequest{Names: []string{"SMTP_PASS"}}, []string{"a"}},
		"explicit apps, running only":   {command.ReloadVarsRequest{AppIDs: []string{"c", "d", "c", "gone"}}, []string{"c"}},
		"a name nobody is stale on":     {command.ReloadVarsRequest{Names: []string{"NOPE"}}, nil},
	}
	for name, tc := range cases {
		if got := pickCandidates(tc.req, running, stale, exists); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestReloadRefusesUndeployedDraft(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	// A draft: a newer commit than the deployed mark.
	if _, err := r.SaveAppDraft(context.Background(), command.AppPayload{
		AppID: "app-1", Config: []byte(`{"name":"Shop App","files":[{"filename":"compose.yml","is_encrypted":false}],"variables":[]}`),
		Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(shopCompose + "# draft\n")}},
	}); err != nil {
		t.Fatal(err)
	}
	// Values change after the draft was saved (and rendered): stale.
	syncVars(t, r, testSnapshot(pub, t, 1, "mail.example", "s3cret"))
	names, err := r.ReloadSharedVars(context.Background(), "app-1")
	if !errors.Is(err, ErrDraftPending) {
		t.Fatalf("err = %v, want ErrDraftPending", err)
	}
	if len(names) == 0 {
		t.Fatal("the stale names must still be reported for the error message")
	}
}

func TestReloadRendersBeforeComposeUp(t *testing.T) {
	r, pub := newSecretRepo(t)
	sharedApp(t, r, "app-1", shopCompose, map[string]string{"PORT": "1"})
	r.markDeployed("app-1", "")
	syncVars(t, r, testSnapshot(pub, t, 1, "relay.example", "s3cret"))
	// No docker in unit tests: put a fake one first on PATH that succeeds.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	names, err := r.ReloadSharedVars(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"SMTP_HOST", "SMTP_PASS"}) {
		t.Fatalf("names = %v", names)
	}
	plain, _ := readEnvFile(t, filepath.Join(r.appDataDir("app-1"), envSharedRel))
	if plain["SMTP_HOST"] != "relay.example" {
		t.Fatalf("not re-rendered: %v", plain)
	}
	if got := r.StaleApps(); len(got) != 0 {
		t.Fatalf("still stale after reload: %v", got)
	}
	if _, err := r.ReloadSharedVars(context.Background(), "missing"); err == nil {
		t.Fatal("reloading a missing app succeeded")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-7; mkdir -p $GOTMPDIR; go test ./internal/infra/orchestrator/docker_compose/ -run 'TestLockApp|TestPickCandidates|TestReload'`
Expected: FAIL: `r.lockApp undefined`, `undefined: pickCandidates`, `r.ReloadSharedVars undefined`.

- [ ] **Step 3: Implement**

`repository.go`: add to the struct

```go
	// appLocks serialize mutations of one app: the distributed agent runs
	// commands concurrently and the reload worker runs beside them.
	lockMu   sync.Mutex
	appLocks map[string]*sync.Mutex
```

initialize `appLocks: make(map[string]*sync.Mutex),` in `NewRepository`, and append to `forgetApp`:

```go
	r.lockMu.Lock()
	delete(r.appLocks, appID)
	r.lockMu.Unlock()
```

`applock.go`:

```go
package dockercompose

import "sync"

// lockApp takes appID's mutation lock and returns its release. Public
// entry points that change an app (save, start, stop, restart, update,
// delete, rollback, rename, source update, shared-variable reload) hold it
// for their whole run; internal helpers never take it (no re-entry).
func (r *Repository) lockApp(appID string) func() {
	r.lockMu.Lock()
	m, ok := r.appLocks[appID]
	if !ok {
		m = &sync.Mutex{}
		r.appLocks[appID] = m
	}
	r.lockMu.Unlock()
	m.Lock()
	return m.Unlock
}
```

Take the lock as the first statement of each public mutator. Add the line `defer r.lockApp(appID)()` (or `app.AppID` where the parameter is a payload) to:

- `operations.go`: `SaveApp` (`defer r.lockApp(app.AppID)()`), `SaveAppDraft` (`defer r.lockApp(app.AppID)()`), `Rollback` (`defer r.lockApp(appID)()`)
- `lifecycle.go`: `StartApp`, `StopApp`, `RestartApp`, `UpdateApp`, `DeleteApp`, `RenameApp` (each `defer r.lockApp(appID)()`)

and in `RefreshDueSources` wrap the per-app work after the `due` check in a closure that holds the lock:

```go
		func() {
			defer r.lockApp(appID)()
			changed, sha, err := r.refreshSourceWithoutDeploy(appID)
			if err != nil {
				r.log.Warn("source poll failed", "app_id", appID, "error", err)
				return
			}
			if !changed {
				return
			}
			r.log.Info("source updated, redeploying", "app_id", appID, "sha", sha)
			if err := r.deploy(ctx, appID); err != nil {
				r.log.Warn("redeploy after source update failed", "app_id", appID, "error", err)
			}
			updated = append(updated, appID)
		}()
```

(replacing the existing body from `changed, sha, err := ...` to `updated = append(updated, appID)`).

`reload.go`:

```go
package dockercompose

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
)

// ErrDraftPending refuses a reload while the app has committed but
// undeployed changes: compose up would deploy the draft with the new values.
var ErrDraftPending = errors.New("the app has undeployed changes (a draft); deploy or roll them back, then reload")

// runningApps reports which apps have a running container, from one
// `docker ps` (reload path only; never polled).
func (r *Repository) runningApps(ctx context.Context) (map[string]bool, error) {
	byProject, err := r.dockerPSByProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	out := map[string]bool{}
	for project, containers := range byProject {
		if !strings.HasPrefix(project, wfProjectPrefix) {
			continue
		}
		for _, c := range containers {
			if c.State == "running" {
				out[strings.TrimPrefix(project, wfProjectPrefix)] = true
				break
			}
		}
	}
	return out, nil
}

// pickCandidates selects, in app-id order, the running apps a reload
// covers: the existing ones among req.AppIDs, else the stale ones (with
// req.Names, only those whose stale names include one of them). Stopped
// apps are left for their next start, which renders anyway.
func pickCandidates(req command.ReloadVarsRequest, running map[string]bool, stale map[string][]string, exists func(string) bool) []string {
	var out []string
	if len(req.AppIDs) > 0 {
		seen := map[string]bool{}
		for _, id := range req.AppIDs {
			if running[id] && !seen[id] && exists(id) {
				seen[id] = true
				out = append(out, id)
			}
		}
		sort.Strings(out)
		return out
	}
	want := map[string]bool{}
	for _, n := range req.Names {
		want[n] = true
	}
	for appID, names := range stale {
		if !running[appID] {
			continue
		}
		if len(want) > 0 && !anyWanted(names, want) {
			continue
		}
		out = append(out, appID)
	}
	sort.Strings(out)
	return out
}

func anyWanted(names []string, want map[string]bool) bool {
	for _, n := range names {
		if want[n] {
			return true
		}
	}
	return false
}

// ReloadCandidates lists the apps a vars.reload covers right now.
func (r *Repository) ReloadCandidates(ctx context.Context, req command.ReloadVarsRequest) ([]string, error) {
	running, err := r.runningApps(ctx)
	if err != nil {
		return nil, err
	}
	return pickCandidates(req, running, r.StaleApps(), r.appExists), nil
}

// ReloadSharedVars re-renders appID's shared-variable files and runs
// `compose up -d`, which recreates only the services whose resolved
// config changed. It returns the names that were stale before the reload.
// A failed compose up leaves the previous containers running, as for any
// failed deploy.
func (r *Repository) ReloadSharedVars(ctx context.Context, appID string) ([]string, error) {
	defer r.lockApp(appID)()
	if !r.appExists(appID) {
		return nil, fmt.Errorf("app %s not found", appID)
	}
	names := r.StaleApps()[appID]
	dir := r.appDataDir(appID)
	if deployed, ok := readDeployedMark(dir); ok {
		if head, err := gitHead(dir); err == nil && head != deployed {
			return names, ErrDraftPending
		}
	}
	if err := r.renderShared(appID); err != nil {
		return names, fmt.Errorf("render shared variables: %w", err)
	}
	if err := r.composeUp(ctx, appID); err != nil {
		return names, fmt.Errorf("docker compose up: %w", err)
	}
	return names, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/infra/orchestrator/docker_compose/ -race && go vet ./internal/infra/orchestrator/...`
Expected: PASS for the whole package (`-race` must show no data race in `TestLockAppSerializesOneAppOnly`).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/orchestrator/docker_compose
git commit -m "feat(agent): per-app lock and shared-variable reload of running apps

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 8: Reload worker, `vars.sync`/`vars.reload` dispatch, revision in status reports, wiring

**Files:**
- Create: `internal/app/agent/reload_worker.go`, `internal/app/agent/reload_worker_test.go`
- Create: `internal/app/agent/dispatcher_vars_test.go`
- Modify: `internal/app/agent/dispatcher.go`
- Modify: `internal/app/agent/reporter.go`, `internal/app/agent/reporter_test.go`
- Modify: `cmd/agent/main.go`, `internal/infra/bootstrap/standalone.go`

**Interfaces:**
- Consumes: Task 6 `(*Repository).SyncVars`, `VarsRevision`; Task 7 `ReloadCandidates`, `ReloadSharedVars`; Task 1 `bus.EventVarsReloaded`, `command.VarsReloadedPayload`, `command.ReloadVarsRequest`.
- Produces:
  - `agent.Reloader` interface `{ReloadCandidates(ctx, command.ReloadVarsRequest) ([]string, error); ReloadSharedVars(ctx, appID string) ([]string, error)}`
  - `agent.NewReloadWorker(r Reloader, sink EventSink, log *logger.Logger) *ReloadWorker`; methods `Enqueue(command.ReloadVarsRequest)`, `Forget(appID string)`, `Run(ctx context.Context)`
  - `(*Dispatcher).WithReloads(w *ReloadWorker) *Dispatcher`
  - `agent.VarsRevisionSource` interface `{VarsRevision() int64}`; the status reporter and the `apps.status` command set `GetAppsStatusResponse.VarsRevision`

- [ ] **Step 1: Write the failing tests**

`internal/app/agent/reload_worker_test.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

// fakeReloader stands in for the orchestrator. Candidates are "every app in
// stale that has not been reloaded since it went stale", like the real
// stale filter; block, when set, holds each reload until released or the
// reload's context ends.
type fakeReloader struct {
	mu       sync.Mutex
	stale    map[string][]string
	reloads  []string
	failWith map[string]error
	block    chan struct{}
	started  chan string
}

func (f *fakeReloader) ReloadCandidates(_ context.Context, req command.ReloadVarsRequest) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(req.AppIDs) > 0 {
		return append([]string(nil), req.AppIDs...), nil
	}
	var out []string
	for _, id := range []string{"a", "b", "c"} {
		if _, ok := f.stale[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

func (f *fakeReloader) ReloadSharedVars(ctx context.Context, appID string) ([]string, error) {
	if f.started != nil {
		f.started <- appID
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reloads = append(f.reloads, appID)
	names := f.stale[appID]
	delete(f.stale, appID)
	return names, f.failWith[appID]
}

func (f *fakeReloader) reloaded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reloads...)
}

func testLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

func awaitOutcomes(t *testing.T, ch <-chan sinkCall, n int) []command.VarsReloadedPayload {
	t.Helper()
	var out []command.VarsReloadedPayload
	for len(out) < n {
		select {
		case c := <-ch:
			if c.kind != bus.EventVarsReloaded {
				t.Fatalf("kind = %q", c.kind)
			}
			var p command.VarsReloadedPayload
			if err := json.Unmarshal(c.payload, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		case <-time.After(3 * time.Second):
			t.Fatalf("got %d of %d outcomes", len(out), n)
		}
	}
	return out
}

func TestReloadWorkerReloadsAndReportsEachApp(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"SMTP_PASS"}, "b": {"S3_KEY"}},
		failWith: map[string]error{"b": errors.New("compose up failed")}}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	w.Enqueue(command.ReloadVarsRequest{})
	outs := awaitOutcomes(t, got, 2)
	if outs[0].AppID != "a" || !outs[0].OK || outs[0].Names[0] != "SMTP_PASS" || outs[0].ServerID != "" {
		t.Fatalf("a = %+v", outs[0])
	}
	if outs[1].AppID != "b" || outs[1].OK || !strings.Contains(outs[1].Error, "S3_KEY") || !strings.Contains(outs[1].Error, "compose up failed") {
		t.Fatalf("b = %+v", outs[1])
	}
}

func TestReloadWorkerCoalescesWorkArrivingMidPass(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}, "b": {"X"}},
		block: make(chan struct{}), started: make(chan string, 8)}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	w.Enqueue(command.ReloadVarsRequest{})
	if first := <-f.started; first != "a" {
		t.Fatalf("first = %s", first)
	}
	// While a reloads: the same change is pushed again, plus a request
	// naming b explicitly.
	w.Enqueue(command.ReloadVarsRequest{})
	w.Enqueue(command.ReloadVarsRequest{Names: []string{"X"}})
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{"b"}})
	close(f.block)
	awaitOutcomes(t, got, 2)
	time.Sleep(50 * time.Millisecond) // nothing else may follow
	if r := f.reloaded(); len(r) != 2 || r[0] != "a" || r[1] != "b" {
		t.Fatalf("reloads = %v, want [a b] once each", r)
	}
}

func TestReloadWorkerDeadline(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}}, block: make(chan struct{})}
	got := make(chan sinkCall, 2)
	w := NewReloadWorker(f, collectSink(got), testLog())
	w.timeout = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Enqueue(command.ReloadVarsRequest{})
	out := awaitOutcomes(t, got, 1)[0]
	if out.OK || !strings.Contains(out.Error, "deadline") {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestReloadWorkerForgetDropsQueuedApp(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}, "b": {"X"}},
		block: make(chan struct{}), started: make(chan string, 8)}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Enqueue(command.ReloadVarsRequest{})
	<-f.started // a is reloading, b is queued
	w.Forget("b")
	close(f.block)
	awaitOutcomes(t, got, 1)
	time.Sleep(50 * time.Millisecond)
	if r := f.reloaded(); len(r) != 1 || r[0] != "a" {
		t.Fatalf("reloads = %v, the deleted app must be dropped", r)
	}
}

func TestReloadWorkerExitsOnCancel(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}, "b": {"X"}},
		block: make(chan struct{}), started: make(chan string, 8)}
	w := NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	w.Enqueue(command.ReloadVarsRequest{})
	<-f.started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not exit on cancel")
	}
	if r := f.reloaded(); len(r) != 0 {
		t.Fatalf("reloads after cancel = %v", r)
	}
}
```

`internal/app/agent/dispatcher_vars_test.go`:

```go
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/grpc/proto"
)

func TestDispatchVarsSyncStoresSnapshot(t *testing.T) {
	d := newTestDispatcher(t)
	resp := dispatchJSON(t, d, command.TypeVarsSync, command.SyncVarsRequest{Revision: 4, ServerID: "s1",
		Org: []command.SharedVar{{Name: "SMTP_HOST", Value: "mail"}}})
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatalf("vars.sync failed: %s", resp.Base.Detail)
	}
	var out command.SyncVarsResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil || out.Revision != 4 || out.Undecryptable == nil {
		t.Fatalf("response = %+v, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("AGENT_DATA_DIR"), "vars", "snapshot.json")); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	bad := dispatchJSON(t, d, command.TypeVarsSync, command.SyncVarsRequest{Revision: 5,
		Org: []command.SharedVar{{Name: "WF_SNEAKY", Value: "x"}}})
	if bad.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(bad.Base.Detail, "reserved") {
		t.Fatalf("a reserved name reached the store: %+v", bad.Base)
	}
}

func TestDispatchVarsReload(t *testing.T) {
	d := newTestDispatcher(t)
	resp := dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatal("vars.reload without a worker must fail")
	}
	w := NewReloadWorker(&fakeReloader{}, func(bus.EventKind, []byte) error { return nil }, testLog())
	d.WithReloads(w)
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{Names: []string{"SMTP_PASS"}})
	var out command.ReloadVarsResponse
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS || json.Unmarshal(resp.Payload, &out) != nil || !out.Queued {
		t.Fatalf("queued reload = %+v %s", resp.Base, resp.Payload)
	}
	w.mu.Lock()
	queued := w.pending.names["SMTP_PASS"]
	w.mu.Unlock()
	if !queued {
		t.Fatal("the request was not queued on the worker")
	}
	if bad := dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"../x"}}); bad.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS {
		t.Fatal("path-like app id accepted")
	}
}

func TestDispatchAppsStatusCarriesVarsRevision(t *testing.T) {
	d := newTestDispatcher(t)
	dispatchJSON(t, d, command.TypeVarsSync, command.SyncVarsRequest{Revision: 7})
	resp := dispatchJSON(t, d, command.TypeAppsStatus, command.GetAppsStatusRequest{})
	var out command.GetAppsStatusResponse
	if err := json.Unmarshal(resp.Payload, &out); err != nil || out.VarsRevision == nil || *out.VarsRevision != 7 {
		t.Fatalf("apps.status = %s, %v", resp.Payload, err)
	}
}
```

Append to `internal/app/agent/reporter_test.go`:

```go
type revisionSource struct{ fakeStatusSource }

func (revisionSource) VarsRevision() int64 { return 9 }

func TestStatusReporterAddsVarsRevision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan sinkCall, 4)
	go RunStatusReporter(ctx, &revisionSource{}, collectSink(got), time.Hour, testLog())
	call := <-got
	var body command.GetAppsStatusResponse
	if err := json.Unmarshal(call.payload, &body); err != nil || body.VarsRevision == nil || *body.VarsRevision != 9 {
		t.Fatalf("report = %s", call.payload)
	}
	// A source without revisions (an old orchestrator) sends none.
	plain := make(chan sinkCall, 4)
	go RunStatusReporter(ctx, &fakeStatusSource{}, collectSink(plain), time.Hour, testLog())
	if c := <-plain; strings.Contains(string(c.payload), "vars_revision") {
		t.Fatalf("report = %s", c.payload)
	}
}
```

(add `"strings"` to reporter_test.go's imports.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-8; mkdir -p $GOTMPDIR; go test ./internal/app/agent/ -run 'TestReloadWorker|TestDispatchVars|TestDispatchAppsStatusCarries|TestStatusReporterAddsVarsRevision'`
Expected: FAIL: `undefined: NewReloadWorker`, `d.WithReloads undefined`.

- [ ] **Step 3: Implement**

`internal/app/agent/reload_worker.go`:

```go
package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

// Reloader is the orchestrator's shared-variable reload surface.
type Reloader interface {
	ReloadCandidates(ctx context.Context, req command.ReloadVarsRequest) ([]string, error)
	ReloadSharedVars(ctx context.Context, appID string) ([]string, error)
}

// reloadTimeout bounds one app's reload (render + compose up, which may
// pull images).
const reloadTimeout = 10 * time.Minute

// ReloadWorker performs vars.reload work on one goroutine, one app at a
// time, so the dispatcher (and standalone's single bridge goroutine) only
// queues it. Requests arriving mid-pass are merged and re-evaluated after
// the current app; an app already queued is not queued twice.
type ReloadWorker struct {
	r       Reloader
	sink    EventSink
	log     *logger.Logger
	timeout time.Duration
	wake    chan struct{}

	mu      sync.Mutex
	pending reloadBatch
	dropped map[string]bool
}

// reloadBatch is merged pending work: all (every stale running app),
// names (stale apps using them) and explicit app ids.
type reloadBatch struct {
	all   bool
	names map[string]bool
	apps  map[string]bool
}

func newBatch() reloadBatch {
	return reloadBatch{names: map[string]bool{}, apps: map[string]bool{}}
}

func (b reloadBatch) empty() bool { return !b.all && len(b.names) == 0 && len(b.apps) == 0 }

// NewReloadWorker builds a worker reporting each app's outcome as a
// vars.reloaded event through sink. Start it with Run.
func NewReloadWorker(r Reloader, sink EventSink, log *logger.Logger) *ReloadWorker {
	return &ReloadWorker{
		r: r, sink: sink, log: log, timeout: reloadTimeout,
		wake: make(chan struct{}, 1), pending: newBatch(), dropped: map[string]bool{},
	}
}

// Enqueue merges req into the pending work and wakes the worker.
func (w *ReloadWorker) Enqueue(req command.ReloadVarsRequest) {
	w.mu.Lock()
	switch {
	case len(req.AppIDs) > 0:
		for _, id := range req.AppIDs {
			w.pending.apps[id] = true
			delete(w.dropped, id)
		}
	case len(req.Names) > 0:
		for _, n := range req.Names {
			w.pending.names[n] = true
		}
	default:
		w.pending.all = true
	}
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Forget drops a deleted app's queued work.
func (w *ReloadWorker) Forget(appID string) {
	w.mu.Lock()
	delete(w.pending.apps, appID)
	w.dropped[appID] = true
	w.mu.Unlock()
}

// Run processes work until ctx is done.
func (w *ReloadWorker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
			w.drain(ctx)
		}
	}
}

// drain runs one pass: it takes the pending work, reloads the candidate
// apps one at a time and, after each app, merges whatever arrived meanwhile.
func (w *ReloadWorker) drain(ctx context.Context) {
	var queue []string
	queued := map[string]bool{}
	for {
		if b, ok := w.take(); ok {
			for _, id := range w.candidates(ctx, b) {
				if !queued[id] {
					queued[id] = true
					queue = append(queue, id)
				}
			}
		}
		if len(queue) == 0 || ctx.Err() != nil {
			break
		}
		appID := queue[0]
		queue = queue[1:]
		delete(queued, appID)
		if w.isDropped(appID) {
			continue
		}
		w.reloadOne(ctx, appID)
	}
	w.mu.Lock()
	w.dropped = map[string]bool{}
	w.mu.Unlock()
}

func (w *ReloadWorker) take() (reloadBatch, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.pending
	if b.empty() {
		return b, false
	}
	w.pending = newBatch()
	return b, true
}

func (w *ReloadWorker) isDropped(appID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped[appID]
}

// candidates resolves a batch to app ids, in request order, without
// duplicates.
func (w *ReloadWorker) candidates(ctx context.Context, b reloadBatch) []string {
	var reqs []command.ReloadVarsRequest
	if b.all {
		reqs = append(reqs, command.ReloadVarsRequest{})
	} else if len(b.names) > 0 {
		reqs = append(reqs, command.ReloadVarsRequest{Names: sortedKeys(b.names)})
	}
	if len(b.apps) > 0 {
		reqs = append(reqs, command.ReloadVarsRequest{AppIDs: sortedKeys(b.apps)})
	}
	seen := map[string]bool{}
	var out []string
	for _, req := range reqs {
		ids, err := w.r.ReloadCandidates(ctx, req)
		if err != nil {
			w.log.Warn("shared variable reload: list candidates", "error", err)
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func (w *ReloadWorker) reloadOne(ctx context.Context, appID string) {
	actx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	names, err := w.r.ReloadSharedVars(actx, appID)
	if ctx.Err() != nil {
		return // shutting down: no outcome to report
	}
	p := command.VarsReloadedPayload{AppID: appID, OK: err == nil, Names: names}
	if p.Names == nil {
		p.Names = []string{}
	}
	if err != nil {
		p.Error = reloadError(names, err)
		w.log.Warn("shared variable reload failed", "app_id", appID, "error", err)
	}
	body, mErr := json.Marshal(p)
	if mErr != nil {
		w.log.Error("shared variable reload: marshal outcome", mErr)
		return
	}
	if sErr := w.sink(bus.EventVarsReloaded, body); sErr != nil {
		w.log.Debug("shared variable reload: report outcome", "app_id", appID, "error", sErr)
	}
}

// reloadError names the variable change that triggered a failed reload.
func reloadError(names []string, err error) string {
	if len(names) == 0 {
		return "reload after a shared variable change failed: " + err.Error()
	}
	return "reload after a change to " + strings.Join(names, ", ") + " failed: " + err.Error()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Note: in `TestReloadWorkerExitsOnCancel` the blocked reload sees its context end and `reloadOne` returns before recording anything. `drain` then stops because `ctx.Err() != nil`, and `Run` returns on `ctx.Done()`.

`internal/app/agent/dispatcher.go`:

- add the field `reloads *ReloadWorker` to `Dispatcher`;
- add

```go
// errNoReloads answers vars.reload on a dispatcher without a reload worker.
var errNoReloads = errors.New("shared variable reload is not available on this agent")

// WithReloads attaches the shared-variable reload worker (production
// wiring); without one vars.reload answers errNoReloads.
func (d *Dispatcher) WithReloads(w *ReloadWorker) *Dispatcher {
	d.reloads = w
	return d
}
```

- in `newHandlers`, change the `apps.status` entry to carry the revision:

```go
		command.TypeAppsStatus: handle(d, func(ctx context.Context, _ command.GetAppsStatusRequest) (command.GetAppsStatusResponse, error) {
			apps, err := d.orch.GetAppsStatus(ctx)
			rev := d.orch.VarsRevision()
			return command.GetAppsStatusResponse{Apps: apps, VarsRevision: &rev}, err
		}),
```

- in the `app.delete` entry, after a successful delete (just before `d.reloadIngress(ctx)`), drop queued reload work:

```go
			if d.reloads != nil {
				d.reloads.Forget(in.AppID)
			}
```

- add, before `command.TypeImageTags`:

```go
		command.TypeVarsSync: handle(d, func(ctx context.Context, in command.SyncVarsRequest) (command.SyncVarsResponse, error) {
			return d.orch.SyncVars(ctx, in)
		}),
		command.TypeVarsReload: handle(d, func(_ context.Context, in command.ReloadVarsRequest) (command.ReloadVarsResponse, error) {
			if d.reloads == nil {
				return command.ReloadVarsResponse{}, errNoReloads
			}
			d.reloads.Enqueue(in)
			return command.ReloadVarsResponse{Queued: true}, nil
		}),
```

`internal/app/agent/reporter.go`: add the optional revision and use it in `report`:

```go
// VarsRevisionSource is implemented by status sources that hold a
// shared-variables snapshot; the report then carries its revision, so the
// API can re-push a missed vars.sync without a polling timer.
type VarsRevisionSource interface {
	VarsRevision() int64
}

// statusReport is the apps.status body for apps from src.
func statusReport(apps []command.AppStatus, src StatusSource) command.GetAppsStatusResponse {
	resp := command.GetAppsStatusResponse{Apps: apps}
	if vr, ok := src.(VarsRevisionSource); ok {
		rev := vr.VarsRevision()
		resp.VarsRevision = &rev
	}
	return resp
}
```

and in `RunStatusReporter`'s `report` replace the marshal line with `payload, err := json.Marshal(statusReport(apps, src))`.

`cmd/agent/main.go`: replace the dispatcher line and the status reporter block with:

```go
	// One sink for every agent-initiated event (status, reload outcomes).
	sink := func(kind bus.EventKind, payload []byte) error {
		return agent.SendEvent(string(kind), payload)
	}

	// Shared-variable reloads run on their own goroutine, never inside a
	// command handler.
	reloads := appagent.NewReloadWorker(orchestrator, sink, log)
	go reloads.Run(ctx)

	agent.SetDispatcher(appagent.NewDispatcher(orchestrator, ingressManager, log).WithReloads(reloads))
```

…and keep `go agent.Run(ctx)` where it is. The reporter call becomes:

```go
	go appagent.RunStatusReporter(ctx, orchestrator, sink, 30*time.Second, log)
```

`internal/infra/bootstrap/standalone.go`: build the sink once and reuse it:

```go
	// One sink for every embedded-agent event (status, reload outcomes):
	// straight onto the in-process events queue. Until the embedded server
	// is claimed there is no server id to report as, so events are skipped.
	sink := func(kind bus.EventKind, payload []byte) error {
		id, ok, err := serverRepo.FirstServerID(ctx)
		if err != nil || !ok {
			return err
		}
		return b.Publish(ctx, cfg.GetBusEventsQueue(), bus.EventMessage{ServerID: id, Kind: kind, Payload: payload})
	}
	reloads := appagent.NewReloadWorker(orchestrator, sink, log)
	go reloads.Run(ctx)
	agentDispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log).WithReloads(reloads)
```

(replacing `agentDispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log)`; move the sink definition above it) and

```go
	go appagent.RunStatusReporter(ctx, orchestrator, sink, 30*time.Second, log)
```

replacing the inline closure.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/app/agent/ ./internal/infra/bootstrap/ && go build ./cmd/... && go vet ./internal/app/... ./cmd/...`
Expected: PASS; all four binaries compile.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agent cmd/agent/main.go internal/infra/bootstrap/standalone.go
git commit -m "feat(agent): vars.sync and vars.reload commands with a background reload worker

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 9: API usecase: list, keys, save rules, delete, resync, reload, sync results, key change

**Files:**
- Create: `internal/domain/usecase/sharedvars/usecase.go` (deps, errors, save/delete/resync/reload)
- Create: `internal/domain/usecase/sharedvars/views.go` (response types, list functions)
- Create: `internal/domain/usecase/sharedvars/sync.go` (snapshot push, results, key change, drift, reload outcomes)
- Create: `internal/domain/usecase/sharedvars/usecase_test.go` (external test package over the real repository on `dbtest`)
- Create: `internal/domain/usecase/sharedvars/const_test.go`

**Interfaces:**
- Consumes: Task 1 model/command types; Task 3 `port.SharedVariableRepository` (tests use `sharedvar.New` on `dbtest.Open`); `port.CommandDispatcher`; `util.GenerateID`.
- Produces (package `sharedvars`, import path `winterflow/internal/domain/usecase/sharedvars`; Task 10 and Task 11 use these names):

```go
type ServerDirectory interface {
	GetCapability(ctx context.Context, serverID, name string) (string, bool, error)
	GetServerUserIDs(ctx context.Context, serverID string) ([]string, error)
} // port.ServerRepository satisfies it
type Publisher interface{ Publish(userID string, n model.Notification) } // port.NotificationManager satisfies it
type RevisionCache interface {
	SetVarsRevision(serverID string, rev int64)
	VarsRevision(serverID string) (int64, bool)
} // *status.Cache satisfies it after Task 10
type Deps struct {
	Repo          port.SharedVariableRepository
	Servers       ServerDirectory
	Dispatcher    port.CommandDispatcher
	Notifications Publisher
	Revisions     RevisionCache
	Log           *logger.Logger
}
func New(d *Deps) *UseCase

var ErrForbidden error
type ValidationError struct{ Msg string } // 400; Error() returns Msg
const SyncSynced = "synced"; const SyncPending = "pending"

type SaveInput struct{ Name string; IsSecret bool; Value *string; Ciphertexts map[string]string; Description string; Reload bool }
type ResyncInput struct{ ServerID, VariableID string }
type ReloadInput struct{ ServerID, VariableID string; Names, AppIDs []string }

// views (JSON response types)
type VariableView struct{ ID, Name, Scope string; IsSecret bool; Value, Description string; UpdatedAt time.Time; UpdatedBy string; MissingOn []string }
type InheritedVariable struct{ VariableView; Overridden bool }
type BuiltinVariable struct{ Name, Value, Description string }
type ServerSync struct{ ServerID, Name string; HasKey bool; Revision int64; AgentRevision *int64; State string }
type OrgVariables struct{ Variables []VariableView; Servers []ServerSync }
type ServerVariables struct{ Variables []VariableView; Inherited []InheritedVariable; BuiltIns []BuiltinVariable; MissingOrgSecrets []string; Sync ServerSync }
type EncryptionKey struct{ ServerID, Name, PublicKey string }
type PendingServer struct{ ServerID, Name string }
type EncryptionKeys struct{ Servers []EncryptionKey; PendingServers []PendingServer }
type DispatchedServers struct{ Servers []string }

func (u *UseCase) ListOrg(ctx context.Context, orgID string) (OrgVariables, error)
func (u *UseCase) ListServer(ctx context.Context, serverID string) (ServerVariables, error)
func (u *UseCase) EncryptionKeys(ctx context.Context, orgID string) (EncryptionKeys, error)
func (u *UseCase) SaveOrg(ctx context.Context, userID, orgID string, in SaveInput) (VariableView, error)
func (u *UseCase) SaveServer(ctx context.Context, userID, serverID string, in SaveInput) (VariableView, error)
func (u *UseCase) DeleteOrg(ctx context.Context, orgID, id string, reload bool) error
func (u *UseCase) DeleteServer(ctx context.Context, serverID, id string, reload bool) error
func (u *UseCase) Resync(ctx context.Context, orgID string, in ResyncInput) ([]string, error)
func (u *UseCase) Reload(ctx context.Context, orgID string, in ReloadInput) ([]string, error)
func (u *UseCase) ServerOrg(ctx context.Context, serverID string) (string, error)
func (u *UseCase) Sync(ctx context.Context, serverID string, reload []string) error
func (u *UseCase) KeyObserved(ctx context.Context, serverID string, caps map[string]string)
func (u *UseCase) ObserveRevision(ctx context.Context, serverID string, agentRev int64) (dbRev int64, behind bool, err error)
func (u *UseCase) Reloaded(ctx context.Context, serverID string, raw []byte)
```

- [ ] **Step 1: Write the failing tests**

`internal/domain/usecase/sharedvars/const_test.go`:

```go
package sharedvars

import (
	"testing"

	"winterflow/internal/infra/transport/bus"
)

// The usecase cannot import the bus (domain over infra), so it mirrors the
// hub's not-connected answer; this pins the two together.
func TestAgentNotConnectedMatchesBus(t *testing.T) {
	if agentNotConnected != bus.ErrAgentNotConnected {
		t.Fatalf("%q != %q", agentNotConnected, bus.ErrAgentNotConnected)
	}
}
```

`internal/domain/usecase/sharedvars/usecase_test.go`:

```go
package sharedvars_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/sharedvar"
	"winterflow/internal/infra/db/types"
)

var (
	org1, org2               = dbtest.ID(1), dbtest.ID(2)
	srvA, srvB, srvP, srvX   = dbtest.ID(11), dbtest.ID(12), dbtest.ID(13), dbtest.ID(14)
	users                    = []string{"u1", "u2"}
)

type fakeDispatcher struct {
	mu   sync.Mutex
	sent []port.DispatchInput
}

func (f *fakeDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, in)
	return "req-" + strconv.Itoa(len(f.sent)), nil
}

func (f *fakeDispatcher) of(typ command.Type) []port.DispatchInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []port.DispatchInput
	for _, in := range f.sent {
		if in.Type == typ {
			out = append(out, in)
		}
	}
	return out
}

// lastTo is the last command of typ sent to serverID.
func (f *fakeDispatcher) lastTo(t *testing.T, typ command.Type, serverID string) port.DispatchInput {
	t.Helper()
	all := f.of(typ)
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].AgentID == serverID {
			return all[i]
		}
	}
	t.Fatalf("no %s sent to %s", typ, serverID)
	return port.DispatchInput{}
}

type sent struct {
	user string
	n    model.Notification
}

type fakePublisher struct {
	mu  sync.Mutex
	got []sent
}

func (f *fakePublisher) Publish(userID string, n model.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, sent{userID, n})
}

func (f *fakePublisher) of(typ model.NotificationType) []model.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []model.Notification
	for _, s := range f.got {
		if s.n.Type == typ {
			out = append(out, s.n)
		}
	}
	return out
}

type fakeRevisions struct {
	mu   sync.Mutex
	revs map[string]int64
}

func (f *fakeRevisions) SetVarsRevision(id string, rev int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revs[id] = rev
}

func (f *fakeRevisions) VarsRevision(id string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.revs[id]
	return r, ok
}

type env struct {
	uc   *sharedvars.UseCase
	d    *db.DB
	repo *sharedvar.Repo
	disp *fakeDispatcher
	pub  *fakePublisher
	revs *fakeRevisions
	caps map[string]string // server -> stored public key
}

// fakeServers answers from env.caps; every server has users u1, u2.
type fakeServers struct{ e *env }

func (f fakeServers) GetCapability(_ context.Context, serverID, name string) (string, bool, error) {
	if name != "public_key" {
		return "", false, nil
	}
	v, ok := f.e.caps[serverID]
	return v, ok, nil
}

func (f fakeServers) GetServerUserIDs(context.Context, string) ([]string, error) { return users, nil }

func (e *env) seedServer(t *testing.T, orgID, serverID, name, key string) {
	t.Helper()
	dbtest.Exec(t, e.d, `INSERT INTO servers (server_id, organization_id, name, created_at) VALUES (?, ?, ?, ?)`,
		serverID, orgID, name, types.NewDateTime())
	if key != "" {
		e.setKey(t, serverID, key)
	}
}

func (e *env) setKey(t *testing.T, serverID, key string) {
	t.Helper()
	dbtest.Exec(t, e.d, `INSERT INTO server_capabilities (server_id, name, value, updated_at) VALUES (?, 'public_key', ?, ?)`,
		serverID, key, types.NewDateTime())
	e.caps[serverID] = key
}

// newEnv: org1 owns alpha (keyed), beta (keyed) and pending (no key yet);
// org2 owns foreign.
func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Open(t)
	repo, err := sharedvar.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{d: d, repo: repo, disp: &fakeDispatcher{}, pub: &fakePublisher{},
		revs: &fakeRevisions{revs: map[string]int64{}}, caps: map[string]string{}}
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
	e.seedServer(t, org1, srvA, "alpha", "KEY-A")
	e.seedServer(t, org1, srvB, "beta", "KEY-B")
	e.seedServer(t, org1, srvP, "pending", "")
	e.seedServer(t, org2, srvX, "foreign", "KEY-X")
	e.uc = sharedvars.New(&sharedvars.Deps{Repo: repo, Servers: fakeServers{e}, Dispatcher: e.disp,
		Notifications: e.pub, Revisions: e.revs, Log: dbtest.Log})
	return e
}

func ptr(s string) *string { return &s }

func invalid(t *testing.T, err error, mention string) {
	t.Helper()
	var ve *sharedvars.ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Msg, mention) {
		t.Fatalf("err = %v, want a validation error mentioning %q", err, mention)
	}
}

func agents(ins []port.DispatchInput) []string {
	out := []string{}
	for _, in := range ins {
		out = append(out, in.AgentID)
	}
	return out
}

func answer(t *testing.T, in port.DispatchInput, success bool, errMsg string, payload any) {
	t.Helper()
	res := port.CommandResult{Success: success, Error: errMsg}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		res.Payload = raw
	}
	in.OnResult(res)
}

func TestSaveOrgPlainPushesEveryOrgServer(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("mail"), Description: "relay"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Value != "mail" || v.Scope != "org" || v.UpdatedBy != "u1" || v.ID == "" || v.UpdatedAt.IsZero() {
		t.Fatalf("view = %+v", v)
	}
	syncs := e.disp.of(command.TypeVarsSync)
	if got := agents(syncs); !reflect.DeepEqual(got, []string{srvA, srvB, srvP}) {
		t.Fatalf("synced %v", got)
	}
	req := syncs[0].Payload.(command.SyncVarsRequest)
	if req.Revision != 1 || req.ServerName != "alpha" || req.OrgID != org1 ||
		!reflect.DeepEqual(req.Org, []command.SharedVar{{Name: "SMTP_HOST", Value: "mail"}}) || len(req.Server) != 0 {
		t.Fatalf("snapshot = %+v", req)
	}
	if syncs[0].UserID != "" {
		t.Fatal("syncs are dispatched for no user; results fan out to the org")
	}
	// Same value, new description: nothing for the agents.
	if _, err := e.uc.SaveOrg(ctx, "u2", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("mail"), Description: "new"}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.disp.of(command.TypeVarsSync)); n != 3 {
		t.Fatalf("a description edit pushed %d syncs", n-3)
	}
	if info, _ := e.repo.ServerVarsInfo(ctx, srvX); info.Revision != 0 {
		t.Fatal("another org's server was bumped")
	}
}

func TestNamesAndKinds(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	_, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "WF_NOPE", Value: ptr("1")})
	invalid(t, err, "reserved")
	_, err = e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "A", Value: ptr("1"), Ciphertexts: map[string]string{srvA: "CT"}})
	invalid(t, err, "plain variable")
	_, err = e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "S", IsSecret: true, Value: ptr("plaintext!")})
	invalid(t, err, "ciphertexts")
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "S", IsSecret: true,
		Ciphertexts: map[string]string{srvA: "CT-A", srvB: "CT-B"}}); err != nil {
		t.Fatal(err)
	}
	_, err = e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "S", Value: ptr("now plain")})
	invalid(t, err, "delete it and create it again")
}

func TestSecretCoverageRules(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	save := func(cts map[string]string) (sharedvars.VariableView, error) {
		return e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: cts})
	}
	_, err := save(map[string]string{srvA: "CT-A"})
	invalid(t, err, srvB) // the pending server has no key, so only beta is uncovered
	if _, err := save(map[string]string{srvA: "CT-A", srvB: "CT-B", srvX: "CT-X"}); !errors.Is(err, sharedvars.ErrForbidden) {
		t.Fatalf("foreign ciphertext err = %v", err)
	}
	_, err = save(map[string]string{srvA: "CT-A", srvB: "CT-B", srvP: "CT-P"})
	invalid(t, err, "no public key")

	v, err := save(map[string]string{srvA: "CT-A", srvB: "CT-B"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Value != command.EncryptedPlaceholder || len(v.MissingOn) != 0 {
		t.Fatalf("view = %+v", v)
	}
	// A partial map that would overwrite alpha's copy is not a gap fill.
	_, err = save(map[string]string{srvA: "ROT-A"})
	invalid(t, err, srvB)

	// The pending server reports a key: the secret is now missing there.
	e.setKey(t, srvP, "KEY-P")
	list, err := e.uc.ListOrg(ctx, org1)
	if err != nil || len(list.Variables) != 1 || !reflect.DeepEqual(list.Variables[0].MissingOn, []string{srvP}) {
		t.Fatalf("list = %+v, %v", list, err)
	}
	before := len(e.disp.of(command.TypeVarsSync))
	if _, err := save(map[string]string{srvP: "CT-P"}); err != nil { // gap fill
		t.Fatal(err)
	}
	if got := agents(e.disp.of(command.TypeVarsSync)[before:]); !reflect.DeepEqual(got, []string{srvP}) {
		t.Fatalf("gap fill pushed to %v, want only the filled server", got)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "CT-A" {
		t.Fatalf("gap fill touched alpha: %v", cts)
	}
	// No ciphertexts at all: a description edit keeps every copy.
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_PASS", IsSecret: true, Description: "monthly"}); err != nil {
		t.Fatal(err)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvP); cts[v.ID] != "CT-P" {
		t.Fatalf("description edit dropped a copy: %v", cts)
	}
	// A full new value replaces every copy and pushes everywhere.
	before = len(e.disp.of(command.TypeVarsSync))
	if _, err := save(map[string]string{srvA: "R-A", srvB: "R-B", srvP: "R-P"}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.disp.of(command.TypeVarsSync)) - before; n != 3 {
		t.Fatalf("rotation pushed %d syncs", n)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "R-A" {
		t.Fatalf("rotation = %v", cts)
	}
}

func TestEncryptionKeys(t *testing.T) {
	e := newEnv(t)
	keys, err := e.uc.EncryptionKeys(dbtest.Ctx(t), org1)
	if err != nil {
		t.Fatal(err)
	}
	want := sharedvars.EncryptionKeys{
		Servers:        []sharedvars.EncryptionKey{{ServerID: srvA, Name: "alpha", PublicKey: "KEY-A"}, {ServerID: srvB, Name: "beta", PublicKey: "KEY-B"}},
		PendingServers: []sharedvars.PendingServer{{ServerID: srvP, Name: "pending"}},
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestServerScopeAndServerListing(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	mustSave := func(v sharedvars.VariableView, err error) sharedvars.VariableView {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	mustSave(e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("org-mail")}))
	mustSave(e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A", srvB: "T-B"}}))
	before := len(e.disp.of(command.TypeVarsSync))
	mustSave(e.uc.SaveServer(ctx, "u1", srvA, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("local")}))
	if got := agents(e.disp.of(command.TypeVarsSync)[before:]); !reflect.DeepEqual(got, []string{srvA}) {
		t.Fatalf("server save pushed to %v", got)
	}
	_, err := e.uc.SaveServer(ctx, "u1", srvA, sharedvars.SaveInput{Name: "KEY", IsSecret: true, Ciphertexts: map[string]string{srvB: "K-B"}})
	invalid(t, err, "its own server")
	mustSave(e.uc.SaveServer(ctx, "u1", srvA, sharedvars.SaveInput{Name: "KEY", IsSecret: true, Ciphertexts: map[string]string{srvA: "K-A"}}))

	ls, err := e.uc.ListServer(ctx, srvA)
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.Variables) != 2 || ls.Variables[0].Name != "KEY" || ls.Variables[1].Value != "local" {
		t.Fatalf("own = %+v", ls.Variables)
	}
	if len(ls.Inherited) != 2 || ls.Inherited[0].Name != "SMTP_HOST" || !ls.Inherited[0].Overridden ||
		ls.Inherited[1].Name != "TOKEN" || ls.Inherited[1].Overridden || ls.Inherited[1].Value != command.EncryptedPlaceholder {
		t.Fatalf("inherited = %+v", ls.Inherited)
	}
	if len(ls.MissingOrgSecrets) != 0 {
		t.Fatalf("missing = %v", ls.MissingOrgSecrets)
	}
	names := map[string]string{}
	for _, b := range ls.BuiltIns {
		names[b.Name] = b.Value
	}
	if names["WF_SERVER_NAME"] != "alpha" || names["WF_ORG_ID"] != org1 || len(ls.BuiltIns) != 8 {
		t.Fatalf("builtins = %+v", ls.BuiltIns)
	}
	if ls.Sync.ServerID != srvA || ls.Sync.State != sharedvars.SyncPending || !ls.Sync.HasKey || ls.Sync.AgentRevision != nil {
		t.Fatalf("sync = %+v", ls.Sync)
	}
	e.revs.SetVarsRevision(srvA, ls.Sync.Revision)
	if ls, _ := e.uc.ListServer(ctx, srvA); ls.Sync.State != sharedvars.SyncSynced {
		t.Fatalf("caught-up agent = %+v", ls.Sync)
	}
	e.setKey(t, srvP, "KEY-P")
	if ls, _ := e.uc.ListServer(ctx, srvP); !reflect.DeepEqual(ls.MissingOrgSecrets, []string{"TOKEN"}) {
		t.Fatalf("pending server missing = %v", ls.MissingOrgSecrets)
	}
	// The org list never shows server-scope variables.
	if org, _ := e.uc.ListOrg(ctx, org1); len(org.Variables) != 2 || len(org.Servers) != 3 {
		t.Fatalf("org list = %+v", org)
	}
}

func TestDeleteAndReloadAfterSync(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "SMTP_HOST", Value: ptr("mail")})
	if err != nil {
		t.Fatal(err)
	}
	invalid(t, e.uc.DeleteOrg(ctx, org2, v.ID, false), "not found")
	invalid(t, e.uc.DeleteServer(ctx, srvA, v.ID, false), "not found")
	before := len(e.disp.of(command.TypeVarsSync))
	if err := e.uc.DeleteOrg(ctx, org1, v.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := len(e.disp.of(command.TypeVarsSync)) - before; n != 3 {
		t.Fatalf("delete pushed %d syncs", n)
	}
	syncA := e.disp.lastTo(t, command.TypeVarsSync, srvA)
	if len(syncA.Payload.(command.SyncVarsRequest).Org) != 0 {
		t.Fatal("the deleted variable is still in the snapshot")
	}
	if len(e.disp.of(command.TypeVarsReload)) != 0 {
		t.Fatal("reload sent before the sync succeeded")
	}
	answer(t, syncA, true, "", command.SyncVarsResponse{Revision: 2, Undecryptable: []command.VarRef{}})
	reload := e.disp.lastTo(t, command.TypeVarsReload, srvA)
	if !reflect.DeepEqual(reload.Payload, command.ReloadVarsRequest{Names: []string{"SMTP_HOST"}}) {
		t.Fatalf("reload = %+v", reload.Payload)
	}
}

func TestSyncResults(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A", srvB: "T-B"}})
	if err != nil {
		t.Fatal(err)
	}
	syncA := e.disp.lastTo(t, command.TypeVarsSync, srvA)
	if got := syncA.Payload.(command.SyncVarsRequest).Org; !reflect.DeepEqual(got, []command.SharedVar{{Name: "TOKEN", IsSecret: true, Ciphertext: "T-A"}}) {
		t.Fatalf("alpha's snapshot = %+v (only alpha's ciphertext may travel to alpha)", got)
	}
	if got := e.disp.lastTo(t, command.TypeVarsSync, srvP).Payload.(command.SyncVarsRequest).Org; got[0].Ciphertext != "" || !got[0].IsSecret {
		t.Fatalf("pending server's snapshot = %+v (name only)", got)
	}
	answer(t, syncA, true, "", command.SyncVarsResponse{Revision: 1, Undecryptable: []command.VarRef{{Scope: "org", Name: "TOKEN"}}})
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); len(cts) != 0 {
		t.Fatalf("undecryptable copy kept: %v", cts)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvB); cts[v.ID] != "T-B" {
		t.Fatalf("beta's copy dropped too: %v", cts)
	}
	synced := e.pub.of(model.NotificationVarsSynced)
	if len(synced) != len(users) {
		t.Fatalf("vars.synced sent %d times", len(synced))
	}
	p := synced[0].Payload.(model.VarsSyncedPayload)
	if p.ServerID != srvA || p.Revision != 1 || !reflect.DeepEqual(p.Undecryptable, []string{"TOKEN"}) {
		t.Fatalf("payload = %+v", p)
	}
	if rev, ok := e.revs.VarsRevision(srvA); !ok || rev != 1 {
		t.Fatalf("cached revision = %d %v", rev, ok)
	}
}

func TestSyncNotConnectedStaysPending(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "A", Value: ptr("1")}); err != nil {
		t.Fatal(err)
	}
	syncB := e.disp.lastTo(t, command.TypeVarsSync, srvB)
	answer(t, syncB, false, "agent not connected", nil)
	if n := len(e.pub.got); n != 0 {
		t.Fatalf("an offline agent produced %d notifications", n)
	}
	answer(t, syncB, false, "unsupported command type: vars.sync", nil)
	failed := e.pub.of(model.NotificationVarsSyncFailed)
	if len(failed) != len(users) || failed[0].Payload.(model.VarsSyncFailedPayload).Error != "unsupported command type: vars.sync" {
		t.Fatalf("failed = %+v", failed)
	}
}

func TestKeyChangePurgesThatServersCopies(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	v, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "TOKEN", IsSecret: true, Ciphertexts: map[string]string{srvA: "T-A", srvB: "T-B"}})
	if err != nil {
		t.Fatal(err)
	}
	e.uc.KeyObserved(ctx, srvA, map[string]string{"public_key": "KEY-A", "os": "linux"}) // unchanged
	e.uc.KeyObserved(ctx, srvA, map[string]string{"os": "linux"})                        // no key reported
	e.uc.KeyObserved(ctx, srvP, map[string]string{"public_key": "FIRST-KEY"})             // first report
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); cts[v.ID] != "T-A" {
		t.Fatal("purged without a key change")
	}
	before, _ := e.repo.ServerVarsInfo(ctx, srvA)
	e.uc.KeyObserved(ctx, srvA, map[string]string{"public_key": "KEY-A2"})
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvA); len(cts) != 0 {
		t.Fatalf("copies for the old key kept: %v", cts)
	}
	if cts, _ := e.repo.CiphertextsForServer(ctx, srvB); cts[v.ID] != "T-B" {
		t.Fatal("another server's copy purged")
	}
	if after, _ := e.repo.ServerVarsInfo(ctx, srvA); after.Revision != before.Revision+1 {
		t.Fatalf("revision %d -> %d", before.Revision, after.Revision)
	}
}

func TestObserveRevision(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.SaveOrg(ctx, "u1", org1, sharedvars.SaveInput{Name: "A", Value: ptr("1")}); err != nil {
		t.Fatal(err)
	}
	if rev, behind, err := e.uc.ObserveRevision(ctx, srvA, 0); err != nil || rev != 1 || !behind {
		t.Fatalf("behind agent = %d %v %v", rev, behind, err)
	}
	if _, behind, _ := e.uc.ObserveRevision(ctx, srvA, 1); behind {
		t.Fatal("a caught-up agent counted as behind")
	}
	if rev, ok := e.revs.VarsRevision(srvA); !ok || rev != 1 {
		t.Fatalf("cache = %d %v", rev, ok)
	}
}

func TestResyncAndReloadTargets(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	local, err := e.uc.SaveServer(ctx, "u1", srvB, sharedvars.SaveInput{Name: "LOCAL", Value: ptr("x")})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.uc.Resync(ctx, org1, sharedvars.ResyncInput{}); err != nil || !reflect.DeepEqual(got, []string{srvA, srvB, srvP}) {
		t.Fatalf("resync all = %v %v", got, err)
	}
	if _, err := e.uc.Resync(ctx, org1, sharedvars.ResyncInput{ServerID: srvX}); !errors.Is(err, sharedvars.ErrForbidden) {
		t.Fatalf("foreign resync err = %v", err)
	}
	if got, _ := e.uc.Resync(ctx, org1, sharedvars.ResyncInput{VariableID: local.ID}); !reflect.DeepEqual(got, []string{srvB}) {
		t.Fatalf("resync by variable = %v", got)
	}
	_, err = e.uc.Reload(ctx, org1, sharedvars.ReloadInput{})
	invalid(t, err, "required")
	_, err = e.uc.Reload(ctx, org1, sharedvars.ReloadInput{ServerID: srvA, AppIDs: []string{"../x"}})
	invalid(t, err, "invalid app id")
	if got, _ := e.uc.Reload(ctx, org1, sharedvars.ReloadInput{ServerID: srvA, AppIDs: []string{"a1"}}); !reflect.DeepEqual(got, []string{srvA}) {
		t.Fatalf("reload server = %v", got)
	}
	if p := e.disp.lastTo(t, command.TypeVarsReload, srvA).Payload; !reflect.DeepEqual(p, command.ReloadVarsRequest{AppIDs: []string{"a1"}}) {
		t.Fatalf("reload payload = %+v", p)
	}
	if got, _ := e.uc.Reload(ctx, org1, sharedvars.ReloadInput{VariableID: local.ID}); !reflect.DeepEqual(got, []string{srvB}) {
		t.Fatalf("reload by variable = %v", got)
	}
	reload := e.disp.lastTo(t, command.TypeVarsReload, srvB)
	if !reflect.DeepEqual(reload.Payload, command.ReloadVarsRequest{Names: []string{"LOCAL"}}) {
		t.Fatalf("reload payload = %+v", reload.Payload)
	}
	// An agent refusing the reload (too old) is reported to the org.
	answer(t, reload, false, "unsupported command type: vars.reload", nil)
	if got := e.pub.of(model.NotificationVarsReloaded); len(got) != len(users) || got[0].Payload.(command.VarsReloadedPayload).OK {
		t.Fatalf("refused reload = %+v", got)
	}
}

func TestReloadedFansOutWithServerID(t *testing.T) {
	e := newEnv(t)
	e.uc.Reloaded(dbtest.Ctx(t), srvA, []byte(`{"app_id":"a1","ok":true,"names":["SMTP_PASS"]}`))
	got := e.pub.of(model.NotificationVarsReloaded)
	if len(got) != len(users) {
		t.Fatalf("sent %d", len(got))
	}
	p := got[0].Payload.(command.VarsReloadedPayload)
	if p.ServerID != srvA || p.AppID != "a1" || !p.OK || p.Names[0] != "SMTP_PASS" {
		t.Fatalf("payload = %+v", p)
	}
	e.uc.Reloaded(dbtest.Ctx(t), srvA, []byte(`not json`)) // logged, not sent
	if n := len(e.pub.of(model.NotificationVarsReloaded)); n != len(users) {
		t.Fatalf("malformed outcome fanned out (%d)", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-9; mkdir -p $GOTMPDIR; go test ./internal/domain/usecase/sharedvars/`
Expected: FAIL: `no non-test Go files` / `undefined: sharedvars.New`.

- [ ] **Step 3: Implement**

`internal/domain/usecase/sharedvars/usecase.go`:

```go
// Package sharedvars is the API side of org- and server-level shared
// variables: it validates saves against the browser fan-out coverage rules,
// stores plain values and per-server ciphertexts (never plaintext secrets),
// bumps the affected servers' vars_revision and pushes each one its full
// snapshot as vars.sync.
package sharedvars

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// Per-server sync states as listed by the API; the browser overlays
// "failed" from vars.sync_failed events.
const (
	SyncSynced  = "synced"
	SyncPending = "pending"
)

// agentNotConnected mirrors bus.ErrAgentNotConnected, the hub's answer for
// an agent without a live stream. Such a sync stays pending rather than
// failing: the agent's connect or its next behind-revision report pushes
// again. TestAgentNotConnectedMatchesBus pins the two together.
const agentNotConnected = "agent not connected"

// ErrForbidden reports a server_id (a ciphertexts key, a resync or reload
// target) outside the caller's org.
var ErrForbidden = errors.New("forbidden")

// ValidationError is a refused input (400); Msg is shown to the user.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// ServerDirectory is the slice of port.ServerRepository the usecase needs.
type ServerDirectory interface {
	GetCapability(ctx context.Context, serverID, name string) (string, bool, error)
	GetServerUserIDs(ctx context.Context, serverID string) ([]string, error)
}

// Publisher delivers one SSE notification to a user (the shared
// NotificationManager).
type Publisher interface {
	Publish(userID string, n model.Notification)
}

// RevisionCache remembers each agent's last reported snapshot revision
// (the in-memory status cache).
type RevisionCache interface {
	SetVarsRevision(serverID string, rev int64)
	VarsRevision(serverID string) (int64, bool)
}

type Deps struct {
	Repo          port.SharedVariableRepository
	Servers       ServerDirectory
	Dispatcher    port.CommandDispatcher
	Notifications Publisher
	Revisions     RevisionCache
	Log           *logger.Logger
}

type UseCase struct{ d Deps }

// New builds the usecase. Nil dependencies are allowed for route-table
// documentation, where no method is ever called.
func New(d *Deps) *UseCase { return &UseCase{d: *d} }

// SaveInput is one save. A plain variable carries Value; a secret carries
// Ciphertexts (server_id -> ECIES for that server), all keyed servers in
// scope for a new value, only servers missing it for a gap fill, or none to
// keep every stored copy (description-only edit).
type SaveInput struct {
	Name        string
	IsSecret    bool
	Value       *string
	Ciphertexts map[string]string
	Description string
	Reload      bool
}

// ResyncInput addresses one server, the servers one variable reaches, or
// (both empty) every org server.
type ResyncInput struct {
	ServerID   string
	VariableID string
}

// ReloadInput is ResyncInput plus the vars.reload filter for a server.
type ReloadInput struct {
	ServerID   string
	VariableID string
	Names      []string
	AppIDs     []string
}

// ServerOrg returns the org that owns serverID.
func (u *UseCase) ServerOrg(ctx context.Context, serverID string) (string, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	return info.OrgID, err
}

// SaveOrg upserts an org-scope variable.
func (u *UseCase) SaveOrg(ctx context.Context, userID, orgID string, in SaveInput) (VariableView, error) {
	return u.save(ctx, userID, orgID, nil, in)
}

// SaveServer upserts a server-scope variable of serverID.
func (u *UseCase) SaveServer(ctx context.Context, userID, serverID string, in SaveInput) (VariableView, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return VariableView{}, err
	}
	id := serverID
	return u.save(ctx, userID, info.OrgID, &id, in)
}

func (u *UseCase) save(ctx context.Context, userID, orgID string, serverID *string, in SaveInput) (VariableView, error) {
	if err := model.ValidateSharedVariableName(in.Name); err != nil {
		return VariableView{}, &ValidationError{Msg: err.Error()}
	}
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return VariableView{}, err
	}
	pub := pubKeys(keys)
	for id := range in.Ciphertexts {
		if _, ok := pub[id]; !ok {
			return VariableView{}, ErrForbidden
		}
	}
	existing, found, err := u.d.Repo.FindByName(ctx, orgID, serverID, in.Name)
	if err != nil {
		return VariableView{}, err
	}
	if found && existing.IsSecret != in.IsSecret {
		return VariableView{}, invalid("%s is a %s variable; delete it and create it again to change that", in.Name, kindOf(existing.IsSecret))
	}
	scope := scopeServers(keys, serverID)
	keyed := keyedServers(scope, pub)
	v := model.SharedVariable{
		OrgID: orgID, ServerID: serverID, Name: in.Name, IsSecret: in.IsSecret, Description: in.Description,
		UpdatedAt: time.Now().UTC().Truncate(time.Second), UpdatedBy: userID, CipherServers: []string{},
	}
	save := model.SharedVariableSave{Create: !found}
	if found {
		v.ID = existing.ID
		v.CipherServers = existing.CipherServers
	} else {
		v.ID = util.GenerateID()
	}

	if !in.IsSecret {
		if len(in.Ciphertexts) > 0 {
			return VariableView{}, invalid("a plain variable takes a value, not ciphertexts")
		}
		if in.Value != nil {
			v.Value = *in.Value
		}
		if !found || existing.Value != v.Value {
			save.Bump = scope
		}
	} else {
		if in.Value != nil && *in.Value != "" {
			return VariableView{}, invalid("a secret's value travels encrypted in ciphertexts, never as value")
		}
		for id := range in.Ciphertexts {
			if serverID != nil && id != *serverID {
				return VariableView{}, invalid("a server variable is encrypted for its own server only")
			}
			if pub[id] == "" {
				return VariableView{}, invalid("server %s has no public key yet, so it cannot receive secrets", id)
			}
		}
		sent := keysOf(in.Ciphertexts)
		switch {
		case len(sent) == 0 && found:
			// Description-only edit: every stored ciphertext stays.
		case len(sent) == 0:
			if len(keyed) > 0 {
				return VariableView{}, invalid("encrypt the value for every server; missing: %s", strings.Join(keyed, ", "))
			}
			save.Bump = scope // no keyed server yet: agents still learn the name (missing everywhere)
		case found && subsetOf(sent, minus(keyed, existing.CipherServers)):
			// Gap fill: only servers that lack a copy; nothing is overwritten.
			save.Ciphertexts = in.Ciphertexts
			save.Bump = sent
			v.CipherServers = union(existing.CipherServers, sent)
		default:
			if uncovered := minus(keyed, sent); len(uncovered) > 0 {
				return VariableView{}, invalid("encrypt the new value for every server; missing: %s", strings.Join(uncovered, ", "))
			}
			save.Ciphertexts = in.Ciphertexts
			save.ReplaceCiphertexts = true
			save.Bump = scope
			v.CipherServers = sent
		}
	}
	save.Variable = v
	if err := u.d.Repo.Save(ctx, save); err != nil {
		return VariableView{}, err
	}
	u.syncAll(ctx, save.Bump, reloadNames(in.Reload, in.Name))
	return view(v, keyed), nil
}

// DeleteOrg removes an org-scope variable of orgID.
func (u *UseCase) DeleteOrg(ctx context.Context, orgID, id string, reload bool) error {
	v, err := u.lookup(ctx, orgID, id)
	if err != nil {
		return err
	}
	if v.ServerID != nil {
		return invalid("shared variable not found")
	}
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return err
	}
	return u.remove(ctx, v, scopeServers(keys, nil), reload)
}

// DeleteServer removes a server-scope variable of serverID.
func (u *UseCase) DeleteServer(ctx context.Context, serverID, id string, reload bool) error {
	orgID, err := u.ServerOrg(ctx, serverID)
	if err != nil {
		return err
	}
	v, err := u.lookup(ctx, orgID, id)
	if err != nil {
		return err
	}
	if v.ServerID == nil || *v.ServerID != serverID {
		return invalid("shared variable not found")
	}
	return u.remove(ctx, v, []string{serverID}, reload)
}

func (u *UseCase) lookup(ctx context.Context, orgID, id string) (model.SharedVariable, error) {
	v, err := u.d.Repo.Get(ctx, id)
	if errors.Is(err, model.ErrSharedVariableNotFound) || (err == nil && v.OrgID != orgID) {
		return model.SharedVariable{}, invalid("shared variable not found")
	}
	return v, err
}

func (u *UseCase) remove(ctx context.Context, v model.SharedVariable, bump []string, reload bool) error {
	if err := u.d.Repo.Delete(ctx, v.ID, bump); err != nil {
		return err
	}
	u.syncAll(ctx, bump, reloadNames(reload, v.Name))
	return nil
}

// Resync pushes the full snapshot to the servers in scope and returns them.
func (u *UseCase) Resync(ctx context.Context, orgID string, in ResyncInput) ([]string, error) {
	targets, _, err := u.targets(ctx, orgID, in.ServerID, in.VariableID)
	if err != nil {
		return nil, err
	}
	for _, id := range targets {
		if err := u.Sync(ctx, id, nil); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// Reload sends vars.reload to one server (with its names/app_ids filter)
// or, for a variable, to every server it reaches (filtered to its name).
func (u *UseCase) Reload(ctx context.Context, orgID string, in ReloadInput) ([]string, error) {
	if in.ServerID == "" && in.VariableID == "" {
		return nil, invalid("server_id or variable_id is required")
	}
	targets, name, err := u.targets(ctx, orgID, in.ServerID, in.VariableID)
	if err != nil {
		return nil, err
	}
	req := command.ReloadVarsRequest{Names: in.Names, AppIDs: in.AppIDs}
	if name != "" {
		req = command.ReloadVarsRequest{Names: []string{name}}
	}
	if err := req.Validate(); err != nil {
		return nil, &ValidationError{Msg: err.Error()}
	}
	for _, id := range targets {
		if err := u.dispatchReload(ctx, id, req); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

// targets resolves a resync/reload scope to server ids, plus the
// variable's name when addressed by variable.
func (u *UseCase) targets(ctx context.Context, orgID, serverID, variableID string) ([]string, string, error) {
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return nil, "", err
	}
	all := scopeServers(keys, nil)
	switch {
	case serverID != "":
		for _, id := range all {
			if id == serverID {
				return []string{serverID}, "", nil
			}
		}
		return nil, "", ErrForbidden
	case variableID != "":
		v, err := u.lookup(ctx, orgID, variableID)
		if err != nil {
			return nil, "", err
		}
		if v.ServerID != nil {
			return []string{*v.ServerID}, v.Name, nil
		}
		return all, v.Name, nil
	}
	return all, "", nil
}

// syncAll pushes the snapshot to each server, logging (not failing) a
// publish error: the save is committed and drift or the next connect heals.
func (u *UseCase) syncAll(ctx context.Context, servers, reload []string) {
	for _, id := range servers {
		if err := u.Sync(ctx, id, reload); err != nil {
			u.d.Log.Warn("push shared variables", "server_id", id, "error", err)
		}
	}
}

func reloadNames(reload bool, name string) []string {
	if reload {
		return []string{name}
	}
	return nil
}

func kindOf(secret bool) string {
	if secret {
		return "secret"
	}
	return "plain"
}

func pubKeys(keys []model.ServerKey) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k.ServerID] = k.PublicKey
	}
	return out
}

// scopeServers is every org server (org scope) or the one server.
func scopeServers(keys []model.ServerKey, serverID *string) []string {
	if serverID != nil {
		return []string{*serverID}
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.ServerID)
	}
	return out
}

// keyedServers keeps the servers that have a public key.
func keyedServers(scope []string, pub map[string]string) []string {
	out := []string{}
	for _, id := range scope {
		if pub[id] != "" {
			out = append(out, id)
		}
	}
	return out
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// minus is a without b, in a's order (never nil).
func minus(a, b []string) []string {
	drop := map[string]bool{}
	for _, s := range b {
		drop[s] = true
	}
	out := []string{}
	for _, s := range a {
		if !drop[s] {
			out = append(out, s)
		}
	}
	return out
}

func subsetOf(a, b []string) bool {
	return len(minus(a, b)) == 0
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
```

`internal/domain/usecase/sharedvars/views.go`:

```go
package sharedvars

import (
	"context"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// VariableView is one shared variable as the API shows it. A secret's
// Value is always "<encrypted>"; MissingOn lists the keyed servers in its
// scope that hold no copy.
type VariableView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Scope       string    `json:"scope"`
	IsSecret    bool      `json:"is_secret"`
	Value       string    `json:"value"`
	Description string    `json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedBy   string    `json:"updated_by"`
	MissingOn   []string  `json:"missing_on"`
}

// InheritedVariable is an org variable seen from one server; Overridden
// means a server variable of the same name wins there.
type InheritedVariable struct {
	VariableView
	Overridden bool `json:"overridden"`
}

// BuiltinVariable is one read-only WF_ variable. Value is "" for the ones
// only the agent knows (per app, data dir, timezone).
type BuiltinVariable struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

// ServerSync is one server's shared-variables sync state.
type ServerSync struct {
	ServerID      string `json:"server_id"`
	Name          string `json:"name"`
	HasKey        bool   `json:"has_key"`
	Revision      int64  `json:"revision"`
	AgentRevision *int64 `json:"agent_revision"`
	State         string `json:"state"`
}

// OrgVariables is GET /api/v1/org/list-shared-variables.
type OrgVariables struct {
	Variables []VariableView `json:"variables"`
	Servers   []ServerSync   `json:"servers"`
}

// ServerVariables is GET /api/v1/server/list-shared-variables.
type ServerVariables struct {
	Variables         []VariableView      `json:"variables"`
	Inherited         []InheritedVariable `json:"inherited"`
	BuiltIns          []BuiltinVariable   `json:"builtins"`
	MissingOrgSecrets []string            `json:"missing_org_secrets"`
	Sync              ServerSync          `json:"sync"`
}

// EncryptionKey is one keyed server for the browser fan-out.
type EncryptionKey struct {
	ServerID  string `json:"server_id"`
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

// PendingServer has never reported a public key, so nothing can be
// encrypted for it yet.
type PendingServer struct {
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
}

// EncryptionKeys is GET /api/v1/org/list-encryption-keys.
type EncryptionKeys struct {
	Servers        []EncryptionKey `json:"servers"`
	PendingServers []PendingServer `json:"pending_servers"`
}

// DispatchedServers lists the servers a resync or reload was sent to.
type DispatchedServers struct {
	Servers []string `json:"servers"`
}

func view(v model.SharedVariable, keyed []string) VariableView {
	out := VariableView{
		ID: v.ID, Name: v.Name, Scope: v.Scope(), IsSecret: v.IsSecret, Value: v.Value,
		Description: v.Description, UpdatedAt: v.UpdatedAt, UpdatedBy: v.UpdatedBy, MissingOn: []string{},
	}
	if v.IsSecret {
		out.Value = command.EncryptedPlaceholder
		out.MissingOn = minus(keyed, v.CipherServers)
	}
	return out
}

// ListOrg returns the org-scope variables and every org server's sync state.
func (u *UseCase) ListOrg(ctx context.Context, orgID string) (OrgVariables, error) {
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return OrgVariables{}, err
	}
	vars, err := u.d.Repo.ListOrg(ctx, orgID)
	if err != nil {
		return OrgVariables{}, err
	}
	keyed := keyedServers(scopeServers(keys, nil), pubKeys(keys))
	out := OrgVariables{Variables: make([]VariableView, 0, len(vars)), Servers: make([]ServerSync, 0, len(keys))}
	for _, v := range vars {
		out.Variables = append(out.Variables, view(v, keyed))
	}
	for _, k := range keys {
		info, err := u.d.Repo.ServerVarsInfo(ctx, k.ServerID)
		if err != nil {
			return OrgVariables{}, err
		}
		out.Servers = append(out.Servers, u.syncState(k, info.Revision))
	}
	return out, nil
}

// ListServer returns serverID's own variables, the org's (marked when
// overridden here), the built-ins, the org secrets missing here and the
// server's sync state.
func (u *UseCase) ListServer(ctx context.Context, serverID string) (ServerVariables, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return ServerVariables{}, err
	}
	keys, err := u.d.Repo.OrgServerKeys(ctx, info.OrgID)
	if err != nil {
		return ServerVariables{}, err
	}
	key := model.ServerKey{ServerID: serverID, Name: info.Name}
	for _, k := range keys {
		if k.ServerID == serverID {
			key = k
		}
	}
	keyed := []string{}
	if key.PublicKey != "" {
		keyed = []string{serverID}
	}
	own, err := u.d.Repo.ListServer(ctx, serverID)
	if err != nil {
		return ServerVariables{}, err
	}
	org, err := u.d.Repo.ListOrg(ctx, info.OrgID)
	if err != nil {
		return ServerVariables{}, err
	}
	out := ServerVariables{
		Variables: make([]VariableView, 0, len(own)), Inherited: make([]InheritedVariable, 0, len(org)),
		BuiltIns: builtins(info), MissingOrgSecrets: []string{}, Sync: u.syncState(key, info.Revision),
	}
	overridden := map[string]bool{}
	for _, v := range own {
		overridden[v.Name] = true
		out.Variables = append(out.Variables, view(v, keyed))
	}
	for _, v := range org {
		iv := InheritedVariable{VariableView: view(v, keyed), Overridden: overridden[v.Name]}
		out.Inherited = append(out.Inherited, iv)
		if v.IsSecret && len(iv.MissingOn) > 0 && !iv.Overridden {
			out.MissingOrgSecrets = append(out.MissingOrgSecrets, v.Name)
		}
	}
	return out, nil
}

// EncryptionKeys lists the org's keyed servers and the pending ones.
func (u *UseCase) EncryptionKeys(ctx context.Context, orgID string) (EncryptionKeys, error) {
	keys, err := u.d.Repo.OrgServerKeys(ctx, orgID)
	if err != nil {
		return EncryptionKeys{}, err
	}
	out := EncryptionKeys{Servers: []EncryptionKey{}, PendingServers: []PendingServer{}}
	for _, k := range keys {
		if k.PublicKey == "" {
			out.PendingServers = append(out.PendingServers, PendingServer{ServerID: k.ServerID, Name: k.Name})
			continue
		}
		out.Servers = append(out.Servers, EncryptionKey{ServerID: k.ServerID, Name: k.Name, PublicKey: k.PublicKey})
	}
	return out, nil
}

// syncState compares the agent's last reported revision with the stored one.
func (u *UseCase) syncState(k model.ServerKey, revision int64) ServerSync {
	s := ServerSync{ServerID: k.ServerID, Name: k.Name, HasKey: k.PublicKey != "", Revision: revision, State: SyncPending}
	if u.d.Revisions != nil {
		if rev, ok := u.d.Revisions.VarsRevision(k.ServerID); ok {
			agentRev := rev
			s.AgentRevision = &agentRev
			if rev >= revision {
				s.State = SyncSynced
			}
		}
	}
	return s
}

// builtins lists the agent's read-only WF_ variables with the values the
// API knows.
func builtins(info model.ServerVarsInfo) []BuiltinVariable {
	return []BuiltinVariable{
		{Name: "WF_SERVER_ID", Value: info.ServerID, Description: "This server's id"},
		{Name: "WF_SERVER_NAME", Value: info.Name, Description: "This server's name"},
		{Name: "WF_ORG_ID", Value: info.OrgID, Description: "The organization's id"},
		{Name: "WF_APP_ID", Description: "The app's id (set per app)"},
		{Name: "WF_APP_SLUG", Description: "The app's name as a slug (set per app)"},
		{Name: "WF_APP_DIR", Description: "The app's folder on the server (set per app)"},
		{Name: "WF_DATA_DIR", Description: "The agent's data directory (AGENT_DATA_DIR)"},
		{Name: "WF_TZ", Description: "The server's timezone"},
	}
}
```

`internal/domain/usecase/sharedvars/sync.go`:

```go
package sharedvars

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
)

// notifyTimeout bounds the DB work done when an agent's result arrives.
const notifyTimeout = 30 * time.Second

// Sync dispatches serverID's full snapshot as vars.sync (for no user: the
// outcome fans out to the org). After a successful sync, reload names are
// sent as vars.reload.
func (u *UseCase) Sync(ctx context.Context, serverID string, reload []string) error {
	req, err := u.snapshot(ctx, serverID)
	if err != nil {
		return err
	}
	_, err = u.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, Type: command.TypeVarsSync, Payload: req,
		OnResult: func(res port.CommandResult) { u.synced(serverID, reload, res) },
	})
	return err
}

// snapshot builds the server's effective set. The revision is read first,
// so the lists are at least as new as the revision they are sent under;
// only this server's ciphertexts travel, and a secret without one travels
// as a bare name.
func (u *UseCase) snapshot(ctx context.Context, serverID string) (command.SyncVarsRequest, error) {
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	org, err := u.d.Repo.ListOrg(ctx, info.OrgID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	own, err := u.d.Repo.ListServer(ctx, serverID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	cts, err := u.d.Repo.CiphertextsForServer(ctx, serverID)
	if err != nil {
		return command.SyncVarsRequest{}, err
	}
	return command.SyncVarsRequest{
		Revision: info.Revision, ServerID: serverID, ServerName: info.Name, OrgID: info.OrgID,
		Org: entries(org, cts), Server: entries(own, cts),
	}, nil
}

func entries(vs []model.SharedVariable, cts map[string]string) []command.SharedVar {
	out := make([]command.SharedVar, 0, len(vs))
	for _, v := range vs {
		if v.IsSecret {
			out = append(out, command.SharedVar{Name: v.Name, IsSecret: true, Ciphertext: cts[v.ID]})
			continue
		}
		out = append(out, command.SharedVar{Name: v.Name, Value: v.Value})
	}
	return out
}

// synced handles a vars.sync result: a bounce from an unconnected agent
// stays pending silently; any other failure is vars.sync_failed; success
// drops the copies the agent cannot decrypt, records the agent's revision,
// announces vars.synced and sends the follow-up reload.
func (u *UseCase) synced(serverID string, reload []string, res port.CommandResult) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	if !res.Success {
		if res.Error == agentNotConnected {
			return
		}
		u.notify(ctx, serverID, model.NotificationVarsSyncFailed, model.VarsSyncFailedPayload{ServerID: serverID, Error: res.Error})
		return
	}
	var out command.SyncVarsResponse
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		u.d.Log.Error("vars.sync result: decode", "error", err, "server_id", serverID)
		return
	}
	names := u.dropUndecryptable(ctx, serverID, out.Undecryptable)
	if u.d.Revisions != nil {
		u.d.Revisions.SetVarsRevision(serverID, out.Revision)
	}
	u.notify(ctx, serverID, model.NotificationVarsSynced, model.VarsSyncedPayload{ServerID: serverID, Revision: out.Revision, Undecryptable: names})
	if len(reload) > 0 {
		if err := u.dispatchReload(ctx, serverID, command.ReloadVarsRequest{Names: reload}); err != nil {
			u.d.Log.Warn("reload after shared variable change", "server_id", serverID, "error", err)
		}
	}
}

// dropUndecryptable deletes serverID's copies of the secrets its agent
// could not decrypt, so they show as missing there, and returns their names.
func (u *UseCase) dropUndecryptable(ctx context.Context, serverID string, refs []command.VarRef) []string {
	names := []string{}
	if len(refs) == 0 {
		return names
	}
	orgID, err := u.ServerOrg(ctx, serverID)
	if err != nil {
		u.d.Log.Warn("drop undecryptable shared secrets", "server_id", serverID, "error", err)
		return names
	}
	var ids []string
	for _, ref := range refs {
		var scope *string
		if ref.Scope == model.SharedScopeServer {
			id := serverID
			scope = &id
		}
		v, ok, err := u.d.Repo.FindByName(ctx, orgID, scope, ref.Name)
		if err != nil || !ok || !v.IsSecret {
			continue
		}
		ids = append(ids, v.ID)
		names = append(names, ref.Name)
	}
	if len(ids) == 0 {
		return names
	}
	if err := u.d.Repo.DropCiphertexts(ctx, serverID, ids); err != nil {
		u.d.Log.Warn("drop undecryptable shared secrets", "server_id", serverID, "error", err)
		return names
	}
	u.d.Log.Warn("dropped shared secrets this server cannot decrypt; enter them again", "server_id", serverID, "names", names)
	return names
}

func (u *UseCase) dispatchReload(ctx context.Context, serverID string, req command.ReloadVarsRequest) error {
	_, err := u.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, Type: command.TypeVarsReload, Payload: req,
		OnResult: func(res port.CommandResult) {
			if res.Success || res.Error == agentNotConnected {
				return
			}
			nctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			names := req.Names
			if names == nil {
				names = []string{}
			}
			u.notify(nctx, serverID, model.NotificationVarsReloaded, command.VarsReloadedPayload{ServerID: serverID, Error: res.Error, Names: names})
		},
	})
	return err
}

// KeyObserved handles a capabilities report before it is stored: a
// public_key different from the stored one invalidates every ciphertext
// encrypted for the old key, so those rows are deleted (the secrets show as
// missing and must be entered again) and the revision is bumped. Pushing
// undecryptable ciphertext is never attempted.
func (u *UseCase) KeyObserved(ctx context.Context, serverID string, caps map[string]string) {
	next := caps["public_key"]
	if next == "" || u.d.Servers == nil {
		return
	}
	prev, ok, err := u.d.Servers.GetCapability(ctx, serverID, "public_key")
	if err != nil || !ok || prev == "" || prev == next {
		return
	}
	if err := u.d.Repo.PurgeServerCiphertexts(ctx, serverID); err != nil {
		u.d.Log.Error("purge shared secrets after key change", "error", err, "server_id", serverID)
		return
	}
	u.d.Log.Warn("server key changed: its shared secrets must be entered again", "server_id", serverID)
}

// ObserveRevision records an agent's reported snapshot revision and says
// whether it is behind servers.vars_revision.
func (u *UseCase) ObserveRevision(ctx context.Context, serverID string, agentRev int64) (int64, bool, error) {
	if u.d.Revisions != nil {
		u.d.Revisions.SetVarsRevision(serverID, agentRev)
	}
	info, err := u.d.Repo.ServerVarsInfo(ctx, serverID)
	if err != nil {
		return 0, false, err
	}
	return info.Revision, agentRev < info.Revision, nil
}

// Reloaded fans one agent-reported reload outcome out to the org.
func (u *UseCase) Reloaded(ctx context.Context, serverID string, raw []byte) {
	var p command.VarsReloadedPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		u.d.Log.Error("vars.reloaded event: decode", "error", err, "server_id", serverID)
		return
	}
	p.ServerID = serverID
	if p.Names == nil {
		p.Names = []string{}
	}
	u.notify(ctx, serverID, model.NotificationVarsReloaded, p)
}

// notify sends one notification to every member of the server's org.
func (u *UseCase) notify(ctx context.Context, serverID string, typ model.NotificationType, payload any) {
	if u.d.Servers == nil || u.d.Notifications == nil {
		return
	}
	users, err := u.d.Servers.GetServerUserIDs(ctx, serverID)
	if err != nil {
		u.d.Log.Debug("shared variables: resolve org members", "server_id", serverID, "error", err)
		return
	}
	n := model.Notification{Type: typ, Payload: payload, Timestamp: time.Now()}
	for _, id := range users {
		u.d.Notifications.Publish(id, n)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/domain/usecase/sharedvars/ && go vet ./internal/domain/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/usecase/sharedvars
git commit -m "feat(api): shared variables usecase with coverage rules and snapshot push

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 10: API drivers: connect sync, heartbeat drift, key change, reload fan-out (both topologies)

**Files:**
- Modify: `internal/domain/service/status/cache.go`, `internal/domain/service/status/cache_test.go`
- Modify: `internal/infra/bootstrap/domainsync.go` (a `name` for log lines; the driver is now shared)
- Create: `internal/infra/bootstrap/varssync.go`, `internal/infra/bootstrap/varssync_test.go`
- Modify: `internal/infra/bootstrap/events.go`, `internal/infra/bootstrap/events_test.go`
- Modify: `internal/infra/bootstrap/core.go`, `internal/infra/bootstrap/deps.go`

**Interfaces:**
- Consumes: Task 9 `sharedvars.New`, `Deps`, `(*UseCase).Sync`, `KeyObserved`, `ObserveRevision`, `Reloaded`; Task 3 `sharedvar.New`; Task 1 `bus.EventVarsReloaded`, `GetAppsStatusResponse.VarsRevision`.
- Produces:
  - `(*status.Cache).SetVarsRevision(serverID string, rev int64)`, `(*status.Cache).VarsRevision(serverID string) (int64, bool)`
  - `bootstrap.Deps.SharedVariableRepository port.SharedVariableRepository` (Task 11's handler reads it)
  - unexported: `varsSync` (embeds `*domainSync`; methods `capabilities`, `revision`, `reloaded`), `varsEvents`/`varsDriver` interfaces, `fanSync`
  - `startEventsSubscriber(ctx, b, cache, serverRepo, nm, cfg, log, indexSync serverSync, vars varsDriver)`

`WireCore` is shared by `BootstrapStandalone` and `BootstrapAPI`, so wiring it here covers both topologies. Standalone's agent events arrive through the in-process events queue (Task 8 sink), distributed agents' through the hub's `publishEvent`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/domain/service/status/cache_test.go`:

```go
func TestVarsRevision(t *testing.T) {
	c := NewCache(time.Minute)
	if _, ok := c.VarsRevision("s1"); ok {
		t.Fatal("unknown server has a revision")
	}
	c.SetVarsRevision("s1", 4)
	if rev, ok := c.VarsRevision("s1"); !ok || rev != 4 {
		t.Fatalf("revision = %d %v", rev, ok)
	}
	// Liveness expiry does not forget the revision (it is not liveness).
	c.ExpireStale(time.Now().Add(time.Hour))
	if _, ok := c.VarsRevision("s1"); !ok {
		t.Fatal("revision expired with liveness")
	}
}
```

`internal/infra/bootstrap/varssync_test.go`:

```go
package bootstrap

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/db/repository/sharedvar"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/logger"
)

type varsDispatcher struct {
	mu   sync.Mutex
	sent []port.DispatchInput
}

func (d *varsDispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, in)
	return "req-" + strconv.Itoa(len(d.sent)), nil
}

func (d *varsDispatcher) syncs() []port.DispatchInput {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []port.DispatchInput
	for _, in := range d.sent {
		if in.Type == command.TypeVarsSync {
			out = append(out, in)
		}
	}
	return out
}

var (
	varsOrg = dbtest.ID(1)
	varsSrv = dbtest.ID(11)
)

func newTestVarsSync(t *testing.T) (*varsSync, *sharedvar.Repo, *varsDispatcher) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, varsOrg, varsSrv)
	repo, err := sharedvar.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	disp := &varsDispatcher{}
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	s := newVarsSync(context.Background(), disp, sharedvars.Deps{Repo: repo, Log: log}, log)
	s.retryDelay = time.Hour // retries are driven by the test
	return s, repo, disp
}

var bumps int

// bump records a change on varsSrv (a new plain org variable).
func bump(t *testing.T, repo *sharedvar.Repo) {
	t.Helper()
	bumps++
	if err := repo.Save(dbtest.Ctx(t), model.SharedVariableSave{Create: true, Bump: []string{varsSrv},
		Variable: model.SharedVariable{ID: dbtest.ID(100 + bumps), OrgID: varsOrg, Name: "V" + strconv.Itoa(bumps), Value: "x"}}); err != nil {
		t.Fatal(err)
	}
}

func TestDriftPushesOncePerRevision(t *testing.T) {
	s, repo, disp := newTestVarsSync(t)
	ctx := dbtest.Ctx(t)
	bump(t, repo) // revision 1
	s.revision(ctx, varsSrv, 0)
	s.revision(ctx, varsSrv, 0) // same drift, next report: no second push
	if n := len(disp.syncs()); n != 1 {
		t.Fatalf("pushes = %d, want 1", n)
	}
	if req := disp.syncs()[0].Payload.(command.SyncVarsRequest); req.Revision != 1 || len(req.Org) != 1 {
		t.Fatalf("pushed %+v", req)
	}
	bump(t, repo) // revision 2: a new drift
	s.revision(ctx, varsSrv, 1)
	s.revision(ctx, varsSrv, 2) // caught up
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("pushes = %d, want 2", n)
	}
	s.revision(ctx, dbtest.ID(99), 0) // an unknown server: nothing, no panic
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("unknown server pushed (%d)", n)
	}
}

func TestVarsSyncOnConnectRetriesWhileNotConnected(t *testing.T) {
	s, _, disp := newTestVarsSync(t)
	s.online(varsSrv)
	first := disp.syncs()
	if len(first) != 1 || first[0].UserID != "" {
		t.Fatalf("connect pushes = %+v", first)
	}
	first[0].OnResult(port.CommandResult{Error: bus.ErrAgentNotConnected})
	s.heartbeat(varsSrv) // the stream is up now
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("pushes after heartbeat = %d, want 2", n)
	}
	raw, _ := json.Marshal(command.SyncVarsResponse{Revision: 0, Undecryptable: []command.VarRef{}})
	disp.syncs()[1].OnResult(port.CommandResult{Success: true, Payload: raw})
	s.heartbeat(varsSrv) // settled: no more pushes
	if n := len(disp.syncs()); n != 2 {
		t.Fatalf("pushes after success = %d", n)
	}
}
```

Append to `internal/infra/bootstrap/events_test.go` (add `"fmt"` to its imports):

```go
// fakeVars records the shared-variable hooks and, as the store, the
// capability saves, so their order is visible.
type fakeVars struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeVars) add(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}
func (f *fakeVars) online(id string)    { f.add("online:" + id) }
func (f *fakeVars) heartbeat(id string) { f.add("beat:" + id) }
func (f *fakeVars) capabilities(_ context.Context, id string, caps map[string]string) {
	f.add("key:" + id + ":" + caps["public_key"])
}
func (f *fakeVars) revision(_ context.Context, id string, rev int64) { f.add(fmt.Sprintf("rev:%s:%d", id, rev)) }
func (f *fakeVars) reloaded(_ context.Context, id string, payload []byte) {
	f.add("reloaded:" + id + ":" + string(payload))
}
func (f *fakeVars) TouchLastSeen(context.Context, string) error { return nil }
func (f *fakeVars) SaveCapabilities(_ context.Context, id string, _ map[string]string, _ map[string]bool) error {
	f.add("save:" + id)
	return nil
}

func TestEventSinkDrivesSharedVariables(t *testing.T) {
	rec := &recorder{}
	sink, _ := newTestSink(rec, []string{"alice"})
	fv := &fakeVars{}
	sink.store = fv
	sink.vars = fv
	sink.sync = fanSync{fv}
	ctx := context.Background()

	caps, _ := json.Marshal(capabilitiesEvent{Capabilities: map[string]string{"public_key": "K2"}})
	sink.handle(ctx, bus.EventMessage{ServerID: "s1", Kind: bus.EventCapabilities, Payload: caps})

	// An old agent: apps.status without vars_revision never drives drift.
	old, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{}})
	sink.handle(ctx, bus.EventMessage{ServerID: "s1", Kind: bus.EventAppsStatus, Payload: old})
	rev := int64(3)
	cur, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{}, VarsRevision: &rev})
	sink.handle(ctx, bus.EventMessage{ServerID: "s1", Kind: bus.EventAppsStatus, Payload: cur})

	before := rec.count()
	sink.handle(ctx, bus.EventMessage{ServerID: "s2", Kind: bus.EventVarsReloaded, Payload: []byte(`{"app_id":"a"}`)})
	if rec.count() != before {
		t.Fatal("a reload outcome was treated as a status change")
	}

	want := []string{"key:s1:K2", "save:s1", "online:s1", "rev:s1:3", `reloaded:s2:{"app_id":"a"}`}
	if fmt.Sprint(fv.calls) != fmt.Sprint(want) {
		t.Fatalf("calls = %v\nwant    %v", fv.calls, want)
	}
}

// TestOldAgentIsNeverDriftPushed: without vars_revision in apps.status the
// drift hook never runs, however often the agent reports.
func TestOldAgentIsNeverDriftPushed(t *testing.T) {
	sink, _ := newTestSink(&recorder{}, nil)
	fv := &fakeVars{}
	sink.vars = fv
	old, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{}})
	for i := 0; i < 3; i++ {
		sink.handle(context.Background(), bus.EventMessage{ServerID: "s1", Kind: bus.EventAppsStatus, Payload: old})
	}
	for _, c := range fv.calls {
		if len(c) >= 4 && c[:4] == "rev:" {
			t.Fatalf("drift hook ran for an old agent: %v", fv.calls)
		}
	}
}
```

(`newTestSink` sets `store: fakeStore{}`; the tests above override it.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-10; mkdir -p $GOTMPDIR; go test ./internal/domain/service/status/ ./internal/infra/bootstrap/`
Expected: FAIL: `c.SetVarsRevision undefined`, `undefined: newVarsSync`, `sink.vars undefined`, `undefined: fanSync`.

- [ ] **Step 3: Implement**

`internal/domain/service/status/cache.go`: add a `vars map[string]int64 // server_id → agent's reported vars revision` field to `Cache`, initialize it in `NewCache` (`vars: make(map[string]int64),`) and add:

```go
// SetVarsRevision records the shared-variables snapshot revision a
// server's agent last reported (apps.status or a vars.sync result). It is
// not liveness: it never expires, and a restart simply waits for the next
// report.
func (c *Cache) SetVarsRevision(serverID string, rev int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vars[serverID] = rev
}

// VarsRevision returns the last reported revision; ok is false before the
// first report.
func (c *Cache) VarsRevision(serverID string) (int64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rev, ok := c.vars[serverID]
	return rev, ok
}
```

`internal/infra/bootstrap/domainsync.go`: the online/heartbeat/retry driver is now used for two fills. Add a `name string` field to `domainSync` (first field after `ctx`), set it in `newDomainSync`:

```go
	s := &domainSync{ctx: ctx, name: "sync registered domains", retryDelay: domainSyncRetryDelay, log: log, pending: map[string]*syncState{}}
```

and use it in the three log lines:

```go
		s.log.Warn(s.name+": dispatch", "error", err, "server_id", serverID)
```
```go
			s.log.Debug(s.name+": agent answered an error", "error", res.Error, "server_id", serverID)
```
```go
		s.log.Warn(s.name+": agent not connected, giving up until it comes online again", "server_id", serverID)
```

`internal/infra/bootstrap/varssync.go`:

```go
package bootstrap

import (
	"context"
	"sync"

	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/pkg/logger"
)

// varsSync keeps every agent's shared-variables snapshot current without a
// polling timer:
//   - an agent coming online (or re-registering) gets a vars.sync, with the
//     same pending/retry machinery as the registered-domains fill;
//   - an apps.status report whose vars_revision is behind
//     servers.vars_revision is pushed again, once per revision, healing a
//     missed push within one report;
//   - a changed public key purges the server's ciphertexts before the new
//     capabilities are stored;
//   - agent-reported reload outcomes fan out to the org.
type varsSync struct {
	*domainSync
	uc  *sharedvars.UseCase
	log *logger.Logger

	mu     sync.Mutex
	pushed map[string]int64 // server -> revision last drift-pushed
}

func newVarsSync(ctx context.Context, d port.CommandDispatcher, deps sharedvars.Deps, log *logger.Logger) *varsSync {
	online := &domainSync{ctx: ctx, name: "sync shared variables", retryDelay: domainSyncRetryDelay, log: log, pending: map[string]*syncState{}}
	deps.Dispatcher = resultTap{d, online.result}
	if deps.Log == nil {
		deps.Log = log
	}
	s := &varsSync{domainSync: online, uc: sharedvars.New(&deps), log: log, pushed: map[string]int64{}}
	online.list = func(ctx context.Context, serverID string) error { return s.uc.Sync(ctx, serverID, nil) }
	return s
}

// capabilities runs before the event's capabilities are stored, so the
// stored key is still the previous one.
func (s *varsSync) capabilities(ctx context.Context, serverID string, caps map[string]string) {
	s.uc.KeyObserved(ctx, serverID, caps)
}

// revision handles the vars_revision of an apps.status report.
func (s *varsSync) revision(ctx context.Context, serverID string, agentRev int64) {
	dbRev, behind, err := s.uc.ObserveRevision(ctx, serverID, agentRev)
	if err != nil || !behind {
		return
	}
	s.mu.Lock()
	if s.pushed[serverID] >= dbRev {
		s.mu.Unlock()
		return
	}
	s.pushed[serverID] = dbRev
	s.mu.Unlock()
	pctx, cancel := context.WithTimeout(s.ctx, domainSyncPublishTimeout)
	defer cancel()
	if err := s.uc.Sync(pctx, serverID, nil); err != nil {
		s.log.Warn("sync shared variables: drift push", "error", err, "server_id", serverID)
	}
}

func (s *varsSync) reloaded(ctx context.Context, serverID string, payload []byte) {
	s.uc.Reloaded(ctx, serverID, payload)
}
```

`internal/infra/bootstrap/events.go`:

- add next to `serverSync`:

```go
// varsEvents are the shared-variable hooks of the event sink.
type varsEvents interface {
	capabilities(ctx context.Context, serverID string, caps map[string]string)
	revision(ctx context.Context, serverID string, agentRev int64)
	reloaded(ctx context.Context, serverID string, payload []byte)
}

// varsDriver is varsSync as the subscriber sees it: an online sync plus
// the event hooks.
type varsDriver interface {
	serverSync
	varsEvents
}

// fanSync drives several per-server syncs from the same online/heartbeat
// signals.
type fanSync []serverSync

func (f fanSync) online(serverID string) {
	for _, s := range f {
		s.online(serverID)
	}
}

func (f fanSync) heartbeat(serverID string) {
	for _, s := range f {
		s.heartbeat(serverID)
	}
}
```

- add the field `vars varsEvents` to `eventSink` (after `sync`);
- in `handle`, case `bus.EventCapabilities`, right after the payload decode and before `s.cache.MarkOnline`:

```go
		if s.vars != nil {
			s.vars.capabilities(ctx, ev.ServerID, c.Capabilities)
		}
```

- in case `bus.EventAppsStatus`, right after the payload decode:

```go
		if s.vars != nil && body.VarsRevision != nil {
			s.vars.revision(ctx, ev.ServerID, *body.VarsRevision)
		}
```

- add a case before `default:`:

```go
	case bus.EventVarsReloaded:
		// A reload outcome is not a liveness or status signal.
		if s.vars != nil {
			s.vars.reloaded(ctx, ev.ServerID, ev.Payload)
		}
		return
```

- change `startEventsSubscriber`'s signature and sink construction:

```go
func startEventsSubscriber(ctx context.Context, b bus.Bus, cache *status.Cache, serverRepo port.ServerRepository, nm port.NotificationManager, cfg *config.ServerConfig, log *logger.Logger, indexSync serverSync, vars varsDriver) {
	sink := &eventSink{
		cache: cache,
		store: serverRepo,
		fan:   newStatusFanout(serverRepo, nm, log),
		log:   log,
		sync:  indexSync,
	}
	if vars != nil {
		sink.vars = vars
		sink.sync = fanSync{indexSync, vars}
	}
	// ... rest unchanged
```

`internal/infra/bootstrap/deps.go`: add to `Deps` after `ServerDomainRepository`:

```go
	// SharedVariableRepository stores org- and server-level shared variables.
	SharedVariableRepository port.SharedVariableRepository
```

`internal/infra/bootstrap/core.go`:

- import `"winterflow/internal/domain/usecase/sharedvars"` and `sharedvarrepo "winterflow/internal/infra/db/repository/sharedvar"`;
- add `sharedVar *sharedvarrepo.Repo` to `repos` and build it last in `newRepos`:

```go
	if r.serverDomain, err = serverdomainrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	r.sharedVar, err = sharedvarrepo.New(conn.Tx, conn.Driver, log)
	return r, err
```

- in `WireCore` replace the `startEventsSubscriber` call with:

```go
	vars := newVarsSync(ctx, dispatcher, sharedvars.Deps{
		Repo: r.sharedVar, Servers: r.server, Notifications: nm, Revisions: statusCache, Log: log,
	}, log)
	startEventsSubscriber(ctx, b, statusCache, r.server, nm, cfg, log, newDomainSync(ctx, dispatcher, r.serverDomain, log), vars)
```

  and add `SharedVariableRepository: r.sharedVar,` to the returned `Deps`. Update the `repos` doc comment to "the DB repositories".

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/domain/service/status/ ./internal/infra/bootstrap/ ./internal/app/web/... && go build ./...`
Expected: PASS. The contract suite (`internal/app/web/contract`) still passes: capabilities events now also dispatch `vars.sync`, and the fake agent answers unknown types with an error that only produces a `vars.sync_failed` fan-out, which no current test inspects. Task 11 adds default scripts for both new commands.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/service/status internal/infra/bootstrap
git commit -m "feat(api): push shared variables on connect and on revision drift

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 11: HTTP routes, SSE payloads, OpenAPI, contract suite, docs

**Files:**
- Create: `internal/app/web/handler/sharedvars/handler.go`, `internal/app/web/handler/sharedvars/routes.go`
- Modify: `internal/app/web/handler/notification/routes.go`, `internal/app/web/handler/notification/routes_test.go`
- Modify: `internal/app/web/routes.go`
- Regenerate: `api/openapi.yaml` (`make openapi`), `internal/app/web/testdata/routes.golden` (`-update`), `internal/app/web/contract/testdata/parity.golden.json` (`-update`)
- Create: `internal/app/web/contract/shared_variables_test.go`
- Modify: `internal/app/web/contract/fakeagent_test.go`, `matrix_test.go`, `parity_test.go`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: Task 9 usecase and views; Task 10 `bootstrap.Deps.SharedVariableRepository`, `StatusCache` methods; `webutil.RequireUser`, `RequireServerAccess`, `DecodeBody`, `Success`, `Error`, `Forbidden`.
- Produces (routes; all `api.User`, tag `variables`):

| Method + path | Request | 200 `data` |
|---|---|---|
| `GET /api/v1/org/list-shared-variables` | — | `sharedvars.OrgVariables` |
| `POST /api/v1/org/save-shared-variable` | `SaveVariableRequest` | `sharedvars.VariableView` |
| `POST /api/v1/org/delete-shared-variable` | `DeleteVariableRequest` | null |
| `GET /api/v1/org/list-encryption-keys` | — | `sharedvars.EncryptionKeys` |
| `GET /api/v1/server/list-shared-variables?server_id=` | `api.ServerQuery` | `sharedvars.ServerVariables` |
| `POST /api/v1/server/save-shared-variable` | `SaveServerVariableRequest` | `sharedvars.VariableView` |
| `POST /api/v1/server/delete-shared-variable` | `DeleteServerVariableRequest` | null |
| `POST /api/v1/shared-variables/resync` | `ResyncRequest` | `sharedvars.DispatchedServers` |
| `POST /api/v1/shared-variables/reload` | `ReloadRequest` | `sharedvars.DispatchedServers` |

  Request JSON (the web client in Task 12 sends exactly these keys): `SaveVariableRequest{name, is_secret, value?, ciphertexts?, description, reload?}`; `SaveServerVariableRequest` = the same plus `server_id`; `DeleteVariableRequest{id, reload?}`; `DeleteServerVariableRequest{server_id, id, reload?}`; `ResyncRequest{server_id?, variable_id?}`; `ReloadRequest{server_id?, variable_id?, names?, app_ids?}`.
  SSE: `vars.synced` (`model.VarsSyncedPayload`), `vars.sync_failed` (`model.VarsSyncFailedPayload`), `vars.reloaded` (`command.VarsReloadedPayload`).

- [ ] **Step 1: Write the failing contract tests**

`internal/app/web/handler/notification/routes_test.go`: extend the `want` map, so the test fails until the payloads are declared:

```go
	want := map[string]any{
		"operation_result": nil,
		"server_status":    model.ServerStatusPayload{},
		"apps_status":      command.AppsStatusPayload{},
		"vars.synced":      model.VarsSyncedPayload{},
		"vars.sync_failed": model.VarsSyncFailedPayload{},
		"vars.reloaded":    command.VarsReloadedPayload{},
	}
```

`internal/app/web/contract/fakeagent_test.go`: add to `defaultScript` (before the final `return`):

```go
	case command.TypeVarsSync:
		return func(raw json.RawMessage) reply {
			in := decode[command.SyncVarsRequest](raw)
			return reply{Payload: command.SyncVarsResponse{Revision: in.Revision, Undecryptable: []command.VarRef{}}}
		}
	case command.TypeVarsReload:
		return func(json.RawMessage) reply { return reply{Payload: command.ReloadVarsResponse{Queued: true}} }
```

and add `command.TypeVarsSync, command.TypeVarsReload,` to the list in `TestFakeAgentAnswersEveryCommandType`.

`internal/app/web/contract/matrix_test.go`: add to `probes`:

```go
	"POST /api/v1/org/save-shared-variable":   {malformed: bad("POST", "/api/v1/org/save-shared-variable")},
	"POST /api/v1/org/delete-shared-variable": {malformed: bad("POST", "/api/v1/org/delete-shared-variable")},
	"GET /api/v1/server/list-shared-variables": {valid: func(s string) call { return get("/api/v1/server/list-shared-variables", sq(s)) },
		malformed: bad("GET", "/api/v1/server/list-shared-variables")},
	"POST /api/v1/server/save-shared-variable": {valid: func(s string) call {
		return post("/api/v1/server/save-shared-variable", map[string]any{"server_id": s, "name": "PROBE", "value": "1"})
	}, malformed: bad("POST", "/api/v1/server/save-shared-variable")},
	"POST /api/v1/server/delete-shared-variable": {valid: func(s string) call {
		return post("/api/v1/server/delete-shared-variable", map[string]any{"server_id": s, "id": anApp})
	}, malformed: bad("POST", "/api/v1/server/delete-shared-variable")},
	"POST /api/v1/shared-variables/resync": {valid: func(s string) call {
		return post("/api/v1/shared-variables/resync", map[string]any{"server_id": s})
	}, malformed: bad("POST", "/api/v1/shared-variables/resync")},
	"POST /api/v1/shared-variables/reload": {valid: func(s string) call {
		return post("/api/v1/shared-variables/reload", map[string]any{"server_id": s})
	}, malformed: bad("POST", "/api/v1/shared-variables/reload")},
```

(the delete probe uses an id that does not exist, so its own-server control answers 400, not 403.)

`internal/app/web/contract/parity_test.go`: after the `list members` step add (import `hsv "winterflow/internal/app/web/handler/sharedvars"` and `"winterflow/internal/domain/usecase/sharedvars"`):

```go
	var saved sharedvars.VariableView
	rec("save org variable", p.post(t, "/api/v1/org/save-shared-variable",
		hsv.SaveVariableRequest{Name: "PARITY", Value: strp("1"), Description: "parity"})).
		expect(t, http.StatusOK).data(t, &saved)
```

`internal/app/web/contract/shared_variables_test.go`:

```go
package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"winterflow/internal/app/web/handler/server"
	hsv "winterflow/internal/app/web/handler/sharedvars"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/transport/bus"
)

func strp(s string) *string { return &s }

// setKey records a server's ECIES public key as its agent's registration would.
func setKey(t *testing.T, e *env, serverID, key string) {
	t.Helper()
	if err := e.deps.ServerRepository.SaveCapabilities(e.dbCtx(t), serverID, map[string]string{"public_key": key}, nil); err != nil {
		t.Fatal(err)
	}
}

// registerServer claims another server into c's org and returns its id.
func registerServer(t *testing.T, e *env, c *client) string {
	t.Helper()
	var reg server.RegisterServerResponse
	c.post(t, "/api/v1/server/register", server.RegisterServerRequest{Code: e.seedPendingServer(t)}).
		expect(t, http.StatusOK).data(t, &reg)
	return reg.Server.ID
}

func syncsTo(e *env, serverID string) []command.SyncVarsRequest {
	var out []command.SyncVarsRequest
	for _, c := range e.agent.seen(command.TypeVarsSync) {
		if c.AgentID == serverID {
			out = append(out, decode[command.SyncVarsRequest](c.Payload))
		}
	}
	return out
}

func orgVars(t *testing.T, c *client) (sharedvars.OrgVariables, string) {
	t.Helper()
	var out sharedvars.OrgVariables
	r := c.get(t, "/api/v1/org/list-shared-variables", nil).expect(t, http.StatusOK)
	r.data(t, &out)
	return out, string(r.Body)
}

func findVar(list []sharedvars.VariableView, name string) sharedvars.VariableView {
	for _, v := range list {
		if v.Name == name {
			return v
		}
	}
	return sharedvars.VariableView{}
}

func noSecrets(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, "CT-") {
		t.Fatalf("a response carries a ciphertext: %s", body)
	}
}

func TestSharedVariableRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sidA := owner.serverID(t)
	setKey(t, e, sidA, "KEY-A")
	sidB := registerServer(t, e, owner)
	setKey(t, e, sidB, "KEY-B")
	sidP := registerServer(t, e, owner) // never reported a key: pending
	foreign := e.foreignServer(t)
	save := func(body hsv.SaveVariableRequest) *response {
		return owner.post(t, "/api/v1/org/save-shared-variable", body)
	}

	t.Run("encryption keys", func(t *testing.T) {
		var keys sharedvars.EncryptionKeys
		owner.get(t, "/api/v1/org/list-encryption-keys", nil).expect(t, http.StatusOK).data(t, &keys)
		if len(keys.Servers) != 2 || len(keys.PendingServers) != 1 || keys.PendingServers[0].ServerID != sidP {
			t.Fatalf("keys = %+v", keys)
		}
	})

	var hostID string
	t.Run("a plain org variable is pushed to every org server", func(t *testing.T) {
		var v sharedvars.VariableView
		save(hsv.SaveVariableRequest{Name: "SMTP_HOST", Value: strp("mail.example"), Description: "relay"}).
			expect(t, http.StatusOK).data(t, &v)
		hostID = v.ID
		eventually(t, func() bool { return len(syncsTo(e, sidA)) > 0 && len(syncsTo(e, sidB)) > 0 && len(syncsTo(e, sidP)) > 0 })
		got := syncsTo(e, sidA)
		if last := got[len(got)-1]; len(last.Org) != 1 || last.Org[0].Value != "mail.example" || last.ServerID != sidA {
			t.Fatalf("snapshot = %+v", last)
		}
		if len(syncsTo(e, foreign)) != 0 {
			t.Fatal("another org's server got the snapshot")
		}
	})

	t.Run("a new secret must cover every keyed server", func(t *testing.T) {
		r := save(hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidA: "CT-A"}}).
			expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), sidB) {
			t.Fatalf("400 does not name the uncovered server: %s", r.Body)
		}
	})

	t.Run("a ciphertext for another org's server is 403", func(t *testing.T) {
		save(hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true,
			Ciphertexts: map[string]string{sidA: "CT-A", sidB: "CT-B", foreign: "CT-X"}}).expect(t, http.StatusForbidden)
	})

	var passID string
	t.Run("a covered secret is stored and never returned", func(t *testing.T) {
		var v sharedvars.VariableView
		r := save(hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidA: "CT-A", sidB: "CT-B"}}).
			expect(t, http.StatusOK)
		r.data(t, &v)
		noSecrets(t, string(r.Body))
		passID = v.ID
		list, body := orgVars(t, owner)
		noSecrets(t, body)
		if p := findVar(list.Variables, "SMTP_PASS"); p.Value != command.EncryptedPlaceholder || len(p.MissingOn) != 0 {
			t.Fatalf("listed = %+v", p)
		}
	})

	t.Run("a gap fill is accepted, an overwriting partial map is not", func(t *testing.T) {
		setKey(t, e, sidP, "KEY-P")
		list, _ := orgVars(t, owner)
		if p := findVar(list.Variables, "SMTP_PASS"); len(p.MissingOn) != 1 || p.MissingOn[0] != sidP {
			t.Fatalf("missing = %+v", p.MissingOn)
		}
		save(hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidA: "CT-ROT"}}).
			expect(t, http.StatusBadRequest)
		save(hsv.SaveVariableRequest{Name: "SMTP_PASS", IsSecret: true, Ciphertexts: map[string]string{sidP: "CT-P"}}).
			expect(t, http.StatusOK)
		list, _ = orgVars(t, owner)
		if p := findVar(list.Variables, "SMTP_PASS"); len(p.MissingOn) != 0 {
			t.Fatalf("still missing on %v", p.MissingOn)
		}
		cts, err := e.deps.SharedVariableRepository.CiphertextsForServer(e.dbCtx(t), sidA)
		if err != nil || cts[passID] != "CT-A" {
			t.Fatalf("the gap fill touched alpha's copy: %v %v", cts, err)
		}
	})

	var localID string
	t.Run("a server variable overrides the org one", func(t *testing.T) {
		var v sharedvars.VariableView
		owner.post(t, "/api/v1/server/save-shared-variable", hsv.SaveServerVariableRequest{ServerID: sidA, Name: "SMTP_HOST", Value: strp("local")}).
			expect(t, http.StatusOK).data(t, &v)
		localID = v.ID
		var ls sharedvars.ServerVariables
		r := owner.get(t, "/api/v1/server/list-shared-variables", url.Values{"server_id": {sidA}}).expect(t, http.StatusOK)
		r.data(t, &ls)
		noSecrets(t, string(r.Body))
		if len(ls.Variables) != 1 || ls.Variables[0].Value != "local" {
			t.Fatalf("own = %+v", ls.Variables)
		}
		overridden := map[string]bool{}
		for _, iv := range ls.Inherited {
			overridden[iv.Name] = iv.Overridden
		}
		if !overridden["SMTP_HOST"] || overridden["SMTP_PASS"] || len(ls.BuiltIns) != 8 {
			t.Fatalf("server listing = %+v", ls)
		}
		owner.post(t, "/api/v1/server/save-shared-variable", hsv.SaveServerVariableRequest{ServerID: sidA, Name: "OWN", IsSecret: true,
			Ciphertexts: map[string]string{sidB: "CT-B"}}).expect(t, http.StatusBadRequest)
	})

	t.Run("reserved names and kind changes are refused", func(t *testing.T) {
		save(hsv.SaveVariableRequest{Name: "WF_SERVER_ID", Value: strp("x")}).expect(t, http.StatusBadRequest)
		save(hsv.SaveVariableRequest{Name: "COMPOSE_PROJECT_NAME", Value: strp("x")}).expect(t, http.StatusBadRequest)
		save(hsv.SaveVariableRequest{Name: "SMTP_PASS", Value: strp("now plain")}).expect(t, http.StatusBadRequest)
		save(hsv.SaveVariableRequest{Name: strings.Repeat("A", 300), Value: strp("x")}).expect(t, http.StatusBadRequest)
	})

	t.Run("vars.synced reaches the org", func(t *testing.T) {
		awaitEvent(t, owner, "vars.synced", func(ev event) bool { return ev.Type == model.NotificationVarsSynced })
	})

	t.Run("undecryptable copies are dropped", func(t *testing.T) {
		e.agent.on(command.TypeVarsSync, func(raw json.RawMessage) reply {
			in := decode[command.SyncVarsRequest](raw)
			und := []command.VarRef{}
			if in.ServerID == sidB {
				und = append(und, command.VarRef{Scope: "org", Name: "SMTP_PASS"})
			}
			return reply{Payload: command.SyncVarsResponse{Revision: in.Revision, Undecryptable: und}}
		})
		defer e.agent.on(command.TypeVarsSync, defaultScript(command.TypeVarsSync))
		var d sharedvars.DispatchedServers
		owner.post(t, "/api/v1/shared-variables/resync", hsv.ResyncRequest{ServerID: sidB}).expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 1 || d.Servers[0] != sidB {
			t.Fatalf("resync = %+v", d)
		}
		eventually(t, func() bool {
			list, _ := orgVars(t, owner)
			p := findVar(list.Variables, "SMTP_PASS")
			return len(p.MissingOn) == 1 && p.MissingOn[0] == sidB
		})
	})

	t.Run("a changed server key purges its copies", func(t *testing.T) {
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventCapabilities, Payload: []byte(`{"capabilities":{"public_key":"KEY-A-NEW"}}`)})
		eventually(t, func() bool {
			list, _ := orgVars(t, owner)
			for _, id := range findVar(list.Variables, "SMTP_PASS").MissingOn {
				if id == sidA {
					return true
				}
			}
			return false
		})
	})

	t.Run("an agent behind is pushed once per revision", func(t *testing.T) {
		eventually(t, func() bool { return len(syncsTo(e, sidA)) > 0 }) // the reconnect sync above
		time.Sleep(100 * time.Millisecond)
		n := len(syncsTo(e, sidA))
		rev := int64(0)
		status, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{}, VarsRevision: &rev})
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventAppsStatus, Payload: status})
		eventually(t, func() bool { return len(syncsTo(e, sidA)) == n+1 })
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventAppsStatus, Payload: status})
		time.Sleep(200 * time.Millisecond)
		if got := len(syncsTo(e, sidA)); got != n+1 {
			t.Fatalf("pushes = %d, want %d", got, n+1)
		}
	})

	t.Run("resync and reload", func(t *testing.T) {
		var d sharedvars.DispatchedServers
		owner.post(t, "/api/v1/shared-variables/resync", hsv.ResyncRequest{}).expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 3 {
			t.Fatalf("resync all = %+v", d)
		}
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: sidA, AppIDs: []string{anApp}}).
			expect(t, http.StatusOK).data(t, &d)
		eventually(t, func() bool {
			for _, c := range e.agent.seen(command.TypeVarsReload) {
				if in := decode[command.ReloadVarsRequest](c.Payload); c.AgentID == sidA && len(in.AppIDs) == 1 && in.AppIDs[0] == anApp {
					return true
				}
			}
			return false
		})
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{VariableID: hostID}).expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 3 {
			t.Fatalf("reload by org variable = %+v", d)
		}
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{}).expect(t, http.StatusBadRequest)
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: foreign}).expect(t, http.StatusForbidden)
		owner.post(t, "/api/v1/shared-variables/resync", hsv.ResyncRequest{ServerID: foreign}).expect(t, http.StatusForbidden)
	})

	t.Run("reload: true follows each sync with vars.reload", func(t *testing.T) {
		save(hsv.SaveVariableRequest{Name: "RELOAD_ME", Value: strp("1"), Reload: true}).expect(t, http.StatusOK)
		eventually(t, func() bool {
			n := 0
			for _, c := range e.agent.seen(command.TypeVarsReload) {
				if in := decode[command.ReloadVarsRequest](c.Payload); len(in.Names) == 1 && in.Names[0] == "RELOAD_ME" {
					n++
				}
			}
			return n == 3
		})
	})

	t.Run("vars.reloaded from the agent fans out", func(t *testing.T) {
		e.publishEvent(t, bus.EventMessage{ServerID: sidA, Kind: bus.EventVarsReloaded,
			Payload: []byte(`{"app_id":"` + anApp + `","ok":true,"names":["SMTP_HOST"]}`)})
		awaitEvent(t, owner, "vars.reloaded", func(ev event) bool {
			return ev.Type == model.NotificationVarsReloaded && strings.Contains(string(ev.Payload), sidA)
		})
	})

	t.Run("delete", func(t *testing.T) {
		owner.post(t, "/api/v1/server/delete-shared-variable", hsv.DeleteServerVariableRequest{ServerID: foreign, ID: localID}).
			expect(t, http.StatusForbidden)
		owner.post(t, "/api/v1/server/delete-shared-variable", hsv.DeleteServerVariableRequest{ServerID: sidA, ID: localID}).
			expect(t, http.StatusOK)
		owner.post(t, "/api/v1/org/delete-shared-variable", hsv.DeleteVariableRequest{ID: hostID}).expect(t, http.StatusOK)
		owner.post(t, "/api/v1/org/delete-shared-variable", hsv.DeleteVariableRequest{ID: hostID}).expect(t, http.StatusBadRequest)
		if list, _ := orgVars(t, owner); findVar(list.Variables, "SMTP_HOST").ID != "" {
			t.Fatal("deleted variable still listed")
		}
	})

	t.Run("another org sees none of it", func(t *testing.T) {
		m := e.newClient()
		m.login(t, "mallory@other.test", "password")
		list, _ := orgVars(t, m)
		if len(list.Variables) != 0 || len(list.Servers) != 1 || list.Servers[0].ServerID != foreign {
			t.Fatalf("mallory sees %+v", list)
		}
	})
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `export GOTMPDIR=$HOME/.cache/wf-gotmp-11; mkdir -p $GOTMPDIR; go test ./internal/app/web/handler/notification/ ./internal/app/web/contract/ -run 'TestRoutes|TestSharedVariableRoutes|TestProbesCoverTable|TestFakeAgentAnswersEveryCommandType'`
Expected: FAIL. The notification test reports `EventPayloads = ..., want 6 entries`, and the contract package fails to compile because `winterflow/internal/app/web/handler/sharedvars` does not exist.

- [ ] **Step 3: Implement the handler, routes and wiring**

`internal/app/web/handler/sharedvars/handler.go`:

```go
// Package sharedvars serves org- and server-level shared variables. Every
// server-addressed route verifies the caller's org owns the server; every
// server id inside a body (ciphertexts keys, resync/reload targets) is
// checked against the caller's org by the usecase. No route ever returns a
// secret value or a ciphertext.
package sharedvars

import (
	"context"
	"errors"
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	svuc "winterflow/internal/domain/usecase/sharedvars"
	"winterflow/pkg/logger"
)

// OrgResolver resolves a user's organization (port.UserService).
type OrgResolver interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
}

type Deps struct {
	Logger              *logger.Logger
	Repo                port.SharedVariableRepository
	Servers             port.ServerRepository
	Users               OrgResolver
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
	StatusCache         *status.Cache
}

type Handler struct {
	uc      *svuc.UseCase
	users   OrgResolver
	servers webutil.ServerAccess
	log     *logger.Logger
}

// NewHandler wires the usecase. With zero Deps (route documentation) it
// builds a handler that is never called.
func NewHandler(d *Deps) *Handler {
	deps := svuc.Deps{Repo: d.Repo, Dispatcher: d.CommandDispatcher, Log: d.Logger}
	var access webutil.ServerAccess
	if d.Servers != nil {
		deps.Servers = d.Servers
		access = d.Servers
	}
	if d.NotificationManager != nil {
		deps.Notifications = d.NotificationManager
	}
	if d.StatusCache != nil { // a nil *status.Cache must not become a non-nil interface
		deps.Revisions = d.StatusCache
	}
	return &Handler{uc: svuc.New(&deps), users: d.Users, servers: access, log: d.Logger}
}

// callerOrg resolves the caller and their org, writing 401/400 itself.
func (h *Handler) callerOrg(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return "", "", false
	}
	if h.users == nil {
		webutil.Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	orgID, err := h.users.PrimaryOrganizationID(r.Context(), userID)
	if err != nil {
		h.log.Error("resolve caller org", "error", err, "user_id", userID)
		webutil.Error(w, "failed to resolve organization", nil)
		return "", "", false
	}
	return userID, orgID, true
}

// serverAccess requires serverID (400) owned by the caller's org (403).
func (h *Handler) serverAccess(w http.ResponseWriter, r *http.Request, userID, serverID string) bool {
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return false
	}
	return webutil.RequireServerAccess(w, r, h.servers, userID, serverID)
}

// orgFor is the org an optional server_id addresses: the server's (after
// the ownership check) or, without one, the caller's.
func (h *Handler) orgFor(w http.ResponseWriter, r *http.Request, userID, serverID string) (string, bool) {
	if serverID == "" {
		_, orgID, ok := h.callerOrg(w, r)
		return orgID, ok
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", false
	}
	orgID, err := h.uc.ServerOrg(r.Context(), serverID)
	if err != nil {
		h.fail(w, err, "resolve the server's organization")
		return "", false
	}
	return orgID, true
}

// fail maps usecase errors: validation 400 with its message, a foreign
// server 403, anything else a logged generic 400.
func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	var ve *svuc.ValidationError
	switch {
	case errors.As(err, &ve):
		webutil.Error(w, ve.Msg, nil)
	case errors.Is(err, svuc.ErrForbidden):
		webutil.Forbidden(w)
	default:
		h.log.Error("shared variables: "+what, "error", err)
		webutil.Error(w, "failed to "+what, nil)
	}
}

func (h *Handler) ListOrg(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	out, err := h.uc.ListOrg(r.Context(), orgID)
	if err != nil {
		h.fail(w, err, "list shared variables")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) EncryptionKeys(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	out, err := h.uc.EncryptionKeys(r.Context(), orgID)
	if err != nil {
		h.fail(w, err, "list encryption keys")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) SaveOrg(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveVariableRequest](w, r)
	if !ok {
		return
	}
	out, err := h.uc.SaveOrg(r.Context(), userID, orgID, req.input())
	if err != nil {
		h.fail(w, err, "save shared variable")
		return
	}
	webutil.Success(w, "shared variable saved", out)
}

func (h *Handler) DeleteOrg(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[DeleteVariableRequest](w, r)
	if !ok {
		return
	}
	if err := h.uc.DeleteOrg(r.Context(), orgID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
	}
	webutil.Success(w, "shared variable deleted", nil)
}

func (h *Handler) ListServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	serverID := r.URL.Query().Get("server_id")
	if !h.serverAccess(w, r, userID, serverID) {
		return
	}
	out, err := h.uc.ListServer(r.Context(), serverID)
	if err != nil {
		h.fail(w, err, "list shared variables")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) SaveServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[SaveServerVariableRequest](w, r)
	if !ok || !h.serverAccess(w, r, userID, req.ServerID) {
		return
	}
	out, err := h.uc.SaveServer(r.Context(), userID, req.ServerID, req.input())
	if err != nil {
		h.fail(w, err, "save shared variable")
		return
	}
	webutil.Success(w, "shared variable saved", out)
}

func (h *Handler) DeleteServer(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[DeleteServerVariableRequest](w, r)
	if !ok || !h.serverAccess(w, r, userID, req.ServerID) {
		return
	}
	if err := h.uc.DeleteServer(r.Context(), req.ServerID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
	}
	webutil.Success(w, "shared variable deleted", nil)
}

func (h *Handler) Resync(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[ResyncRequest](w, r)
	if !ok {
		return
	}
	orgID, ok := h.orgFor(w, r, userID, req.ServerID)
	if !ok {
		return
	}
	servers, err := h.uc.Resync(r.Context(), orgID, svuc.ResyncInput{ServerID: req.ServerID, VariableID: req.VariableID})
	if err != nil {
		h.fail(w, err, "resync shared variables")
		return
	}
	webutil.Success(w, "resync dispatched", svuc.DispatchedServers{Servers: servers})
}

func (h *Handler) Reload(w http.ResponseWriter, r *http.Request) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[ReloadRequest](w, r)
	if !ok {
		return
	}
	orgID, ok := h.orgFor(w, r, userID, req.ServerID)
	if !ok {
		return
	}
	servers, err := h.uc.Reload(r.Context(), orgID, svuc.ReloadInput{
		ServerID: req.ServerID, VariableID: req.VariableID, Names: req.Names, AppIDs: req.AppIDs,
	})
	if err != nil {
		h.fail(w, err, "reload apps")
		return
	}
	webutil.Success(w, "reload dispatched", svuc.DispatchedServers{Servers: servers})
}
```

`internal/app/web/handler/sharedvars/routes.go`:

```go
package sharedvars

import (
	"net/http"

	"winterflow/internal/app/web/api"
	svuc "winterflow/internal/domain/usecase/sharedvars"
)

// SaveVariableRequest is the body of POST /api/v1/org/save-shared-variable.
type SaveVariableRequest struct {
	Name        string            `json:"name" required:"true" description:"Env-var name; COMPOSE_PROJECT_NAME and WF_* are reserved."`
	IsSecret    bool              `json:"is_secret"`
	Value       *string           `json:"value,omitempty" description:"Plain variables only."`
	Ciphertexts map[string]string `json:"ciphertexts,omitempty" description:"Secrets only: server_id -> ECIES ciphertext for that server's key (GET /api/v1/org/list-encryption-keys). A new value covers every keyed server; a gap fill only servers missing it; none keeps every stored copy."`
	Description string            `json:"description"`
	Reload      bool              `json:"reload,omitempty" description:"Reload the affected apps after each server stored the change."`
}

func (r SaveVariableRequest) input() svuc.SaveInput {
	return svuc.SaveInput{Name: r.Name, IsSecret: r.IsSecret, Value: r.Value, Ciphertexts: r.Ciphertexts, Description: r.Description, Reload: r.Reload}
}

// SaveServerVariableRequest is the body of POST /api/v1/server/save-shared-variable.
type SaveServerVariableRequest struct {
	ServerID    string            `json:"server_id" required:"true"`
	Name        string            `json:"name" required:"true" description:"Env-var name; COMPOSE_PROJECT_NAME and WF_* are reserved."`
	IsSecret    bool              `json:"is_secret"`
	Value       *string           `json:"value,omitempty" description:"Plain variables only."`
	Ciphertexts map[string]string `json:"ciphertexts,omitempty" description:"Secrets only: exactly one entry, for server_id."`
	Description string            `json:"description"`
	Reload      bool              `json:"reload,omitempty"`
}

func (r SaveServerVariableRequest) input() svuc.SaveInput {
	return svuc.SaveInput{Name: r.Name, IsSecret: r.IsSecret, Value: r.Value, Ciphertexts: r.Ciphertexts, Description: r.Description, Reload: r.Reload}
}

// DeleteVariableRequest is the body of POST /api/v1/org/delete-shared-variable.
type DeleteVariableRequest struct {
	ID     string `json:"id" required:"true"`
	Reload bool   `json:"reload,omitempty"`
}

// DeleteServerVariableRequest is the body of POST /api/v1/server/delete-shared-variable.
type DeleteServerVariableRequest struct {
	ServerID string `json:"server_id" required:"true"`
	ID       string `json:"id" required:"true"`
	Reload   bool   `json:"reload,omitempty"`
}

// ResyncRequest addresses one server, the servers one variable reaches, or
// (neither) every org server.
type ResyncRequest struct {
	ServerID   string `json:"server_id,omitempty"`
	VariableID string `json:"variable_id,omitempty"`
}

// ReloadRequest addresses one server (optionally filtered by names or app
// ids) or, by variable, every server it reaches.
type ReloadRequest struct {
	ServerID   string   `json:"server_id,omitempty"`
	VariableID string   `json:"variable_id,omitempty"`
	Names      []string `json:"names,omitempty"`
	AppIDs     []string `json:"app_ids,omitempty"`
}

// Routes is the shared variables slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	bad := []int{http.StatusBadRequest}
	both := []int{http.StatusBadRequest, http.StatusForbidden}
	const pushed = "Answers once the change is stored; each affected server's agent then gets the new snapshot " +
		"(vars.synced / vars.sync_failed over SSE)."
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/org/list-shared-variables", Handler: h.ListOrg, Auth: api.User,
			Summary: "The org's shared variables (secrets masked) with coverage and per-server sync state", Tag: "variables",
			Response: svuc.OrgVariables{}, Errors: bad,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/save-shared-variable", Handler: h.SaveOrg, Auth: api.User,
			Summary: "Create or update an org-level shared variable", Tag: "variables", Description: pushed,
			Request: SaveVariableRequest{}, Response: svuc.VariableView{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/org/delete-shared-variable", Handler: h.DeleteOrg, Auth: api.User,
			Summary: "Delete an org-level shared variable", Tag: "variables", Description: pushed,
			Request: DeleteVariableRequest{}, Errors: both,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/org/list-encryption-keys", Handler: h.EncryptionKeys, Auth: api.User,
			Summary: "Public keys of the org's servers for encrypting secrets in the browser", Tag: "variables",
			Response: svuc.EncryptionKeys{}, Errors: bad,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/server/list-shared-variables", Handler: h.ListServer, Auth: api.User,
			Summary: "A server's shared variables, the org's as inherited, built-ins and missing org secrets", Tag: "variables",
			Query: api.ServerQuery{}, Response: svuc.ServerVariables{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/server/save-shared-variable", Handler: h.SaveServer, Auth: api.User,
			Summary: "Create or update a server-level shared variable", Tag: "variables", Description: pushed,
			Request: SaveServerVariableRequest{}, Response: svuc.VariableView{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/server/delete-shared-variable", Handler: h.DeleteServer, Auth: api.User,
			Summary: "Delete a server-level shared variable", Tag: "variables", Description: pushed,
			Request: DeleteServerVariableRequest{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/shared-variables/resync", Handler: h.Resync, Auth: api.User,
			Summary: "Push the full shared-variables snapshot again", Tag: "variables",
			Description: "Answers with the servers dispatched to; outcomes arrive as vars.synced / vars.sync_failed over SSE.",
			Request:     ResyncRequest{}, Response: svuc.DispatchedServers{}, Errors: both,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/shared-variables/reload", Handler: h.Reload, Auth: api.User,
			Summary: "Reload running apps whose shared variables changed", Tag: "variables",
			Description: "Answers with the servers dispatched to; each app's outcome arrives as vars.reloaded over SSE.",
			Request:     ReloadRequest{}, Response: svuc.DispatchedServers{}, Errors: both,
		},
	}
}
```

`internal/app/web/handler/notification/routes.go`: extend `EventPayloads` and mention the new events in `StreamDescription` (replace "server_status and apps_status events go to every member" with "server_status, apps_status and vars.* events go to every member"):

```go
		EventPayloads: []api.EventPayload{
			{Type: string(model.NotificationOperationResult)},
			{Type: string(model.NotificationServerStatus), Payload: model.ServerStatusPayload{}},
			{Type: string(model.NotificationAppsStatus), Payload: command.AppsStatusPayload{}},
			{Type: string(model.NotificationVarsSynced), Payload: model.VarsSyncedPayload{}},
			{Type: string(model.NotificationVarsSyncFailed), Payload: model.VarsSyncFailedPayload{}},
			{Type: string(model.NotificationVarsReloaded), Payload: command.VarsReloadedPayload{}},
		},
```

`internal/app/web/routes.go`: import `hsharedvars "winterflow/internal/app/web/handler/sharedvars"`, build the handler in `routeTable` (after `orgAPI`):

```go
	sharedVarsAPI := hsharedvars.NewHandler(&hsharedvars.Deps{
		Logger:              d.Log,
		Repo:                d.SharedVariableRepository,
		Servers:             d.ServerRepository,
		Users:               d.UserService,
		CommandDispatcher:   d.CommandDispatcher,
		NotificationManager: d.NotificationManager,
		StatusCache:         d.StatusCache,
	})
```

and add `sharedVarsAPI.Routes(g),` to the table list after `orgAPI.Routes(g),`.

`CLAUDE.md`: in **Implemented command surface**, after `ingress — domain.list/create/delete (registered domains, see below);` insert `shared variables — vars.sync (a server's full snapshot), vars.reload (re-render + compose up of stale running apps, queued on a worker; per-app outcomes arrive as vars.reloaded agent events);`. After the **Registered domains** paragraph add:

```markdown
**Shared variables:** org- and server-level variables and secrets (`shared_variables`, `shared_secret_ciphertexts`, `servers.vars_revision`; routes `/api/v1/org|server/*-shared-variable*`, `/api/v1/shared-variables/resync|reload`). Precedence built-ins < org < server < app; `COMPOSE_PROJECT_NAME` and `WF_*` are reserved; `WF_*` built-ins are agent-computed. Secrets use browser fan-out: one ECIES ciphertext per keyed server, the API never sees plaintext; a server key change purges that server's copies. The API pushes the full snapshot as `vars.sync` on save/delete, agent connect and revision drift (`apps.status` carries `vars_revision`); the agent stores `{AGENT_DATA_DIR}/vars/snapshot.json` (0600, ciphertext only) and renders only referenced names into the gitignored `.env.shared` / `.env.shared.secrets` (0600) on the paths that call `materializeSecrets`, never on sync. Compose env files: `.env.shared .env.shared.secrets .env .env.secrets`. Stale apps ride `apps.status` as `stale_vars`; reloads run on one worker goroutine under the per-app lock (`Repository.lockApp`).
```

- [ ] **Step 4: Regenerate and run everything**

Run:
```bash
make openapi
go test ./internal/app/web/ -run TestRoutesGolden -update
go test ./internal/app/web/contract -run TestParity -update
git diff --stat api/openapi.yaml internal/app/web/testdata internal/app/web/contract/testdata
go test -race ./internal/app/web/... ./internal/infra/... ./internal/domain/...
```
Expected: the goldens change only by the nine new routes, the three event payloads, the description line and the parity step "save org variable". All tests PASS, including `TestOpenAPIUpToDate`, `TestProbesCoverTable`, `TestForeignServer`, `TestMalformed`, `TestUnauthenticated` and the `TestMain` guard (every new route answers below 400 in `TestSharedVariableRoutes`). With Docker: `make test-pg`, expected PASS (parity golden identical on Postgres). Then `go test ./...` and `go vet ./...`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web api/openapi.yaml CLAUDE.md
git commit -m "feat(api): shared variable routes, SSE events and contract tests

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 12: Web library: types, API client, `encryptForServers`, helpers

**Files:**
- Create: `web/src/lib/shared-variables.ts`
- Create: `web/src/lib/shared-variables.test.ts` (helpers and client, with `@/lib/ecies` mocked)
- Create: `web/src/lib/shared-variables-crypto.test.ts` (real ECIES round trip, no mocks)

**Interfaces:**
- Consumes: Task 11 routes and JSON shapes; `encryptSecret` from `web/src/lib/ecies.ts`; `readApiError` from `web/src/lib/api.ts`; `apiBaseUrl` from `web/src/config.ts`.
- Produces (used by Tasks 13–16):
  - types `VariableScope`, `SharedVariable`, `InheritedVariable`, `BuiltinVariable`, `ServerSync`, `SyncState = "synced" | "pending" | "failed"`, `OrgVariables`, `ServerVariables`, `EncryptionKey`, `PendingServer`, `EncryptionKeys`, `SaveVariable`, `ResyncTarget`, `ReloadTarget`, `SharedName`, `VarsReloaded`
  - `ENCRYPTED = "<encrypted>"`
  - `variableNameError(name: string): string`
  - `encryptForServers(value: string, keys: EncryptionKey[], only?: string[]): Promise<Record<string, string>>`
  - `serverNameMap(keys: EncryptionKeys): Map<string, string>`
  - `coverageLabel(v: SharedVariable, names: Map<string, string>): string`
  - `pushSummary(count: number, pending: number): string`
  - `applySyncEvent(states: Record<string, SyncState>, n: { type: string; payload?: unknown }): Record<string, SyncState>`
  - `sharedNames(v: ServerVariables): SharedName[]`, `overrideNote(name: string, shared: SharedName[]): string`
  - `SharedVariablesApi` and `createSharedVariablesApi(fetchImpl?: typeof fetch): SharedVariablesApi` with `listOrg()`, `listServer(serverId)`, `keys()`, `saveOrg(body)`, `saveServer(serverId, body)`, `deleteOrg(id, reload?)`, `deleteServer(serverId, id, reload?)`, `resync(target)`, `reload(target)`

- [ ] **Step 1: Write the failing tests**

First install the web deps if this worktree has none: `pnpm --dir web install --frozen-lockfile`.

`web/src/lib/shared-variables-crypto.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { p256 } from "@noble/curves/nist.js";
import { sha256 } from "@noble/hashes/sha2.js";
import { gcm } from "@noble/ciphers/aes.js";

import { encryptForServers, type EncryptionKey } from "@/lib/shared-variables";

// decrypt is the agent's half (pkg/crypto/ecies.go) in JS: the payload is
// base64(ephemeral point 65 | IV 12 | ciphertext+tag), the AES key is
// SHA-256 of the ECDH shared X coordinate.
function decrypt(b64: string, priv: Uint8Array): string {
  const raw = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  const eph = raw.subarray(0, 65);
  const iv = raw.subarray(65, 77);
  const shared = p256.getSharedSecret(priv, eph, false).subarray(1, 33);
  return new TextDecoder().decode(gcm(sha256(shared), iv).decrypt(raw.subarray(77)));
}

function keyPair(id: string): { key: EncryptionKey; priv: Uint8Array } {
  const priv = p256.utils.randomSecretKey();
  const point = p256.getPublicKey(priv, false);
  return { key: { server_id: id, name: id, public_key: btoa(String.fromCharCode(...point)) }, priv };
}

describe("encryptForServers", () => {
  it("encrypts once per server, each readable only with that server's key", async () => {
    const a = keyPair("a");
    const b = keyPair("b");
    const cts = await encryptForServers("pa$word\nline 2", [a.key, b.key]);
    expect(Object.keys(cts).sort()).toEqual(["a", "b"]);
    expect(cts.a).not.toBe(cts.b);
    expect(decrypt(cts.a, a.priv)).toBe("pa$word\nline 2");
    expect(decrypt(cts.b, b.priv)).toBe("pa$word\nline 2");
    expect(() => decrypt(cts.a, b.priv)).toThrow();
  });

  it("encrypts only for the listed servers (gap fill)", async () => {
    const a = keyPair("a");
    const b = keyPair("b");
    expect(Object.keys(await encryptForServers("x", [a.key, b.key], ["b"]))).toEqual(["b"]);
  });
});
```

`web/src/lib/shared-variables.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

import {
  applySyncEvent,
  coverageLabel,
  createSharedVariablesApi,
  overrideNote,
  pushSummary,
  serverNameMap,
  sharedNames,
  variableNameError,
  type ServerVariables,
  type SharedVariable,
} from "@/lib/shared-variables";

const secret: SharedVariable = {
  id: "v1", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>",
  description: "", updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: [],
};

describe("helpers", () => {
  it("validates names like the API", () => {
    expect(variableNameError("SMTP_HOST")).toBe("");
    expect(variableNameError("")).toMatch(/required/);
    expect(variableNameError("1X")).toMatch(/letters, digits/);
    expect(variableNameError("WF_APP_ID")).toMatch(/reserved/);
    expect(variableNameError("COMPOSE_PROJECT_NAME")).toMatch(/reserved/);
    expect(variableNameError("A".repeat(256))).toMatch(/255/);
  });

  it("labels coverage by server name", () => {
    const names = new Map([["s3", "srv-3"]]);
    expect(coverageLabel(secret, names)).toBe("All servers");
    expect(coverageLabel({ ...secret, missing_on: ["s3", "s9"] }, names)).toBe("Missing on srv-3, s9");
    expect(coverageLabel({ ...secret, is_secret: false }, names)).toBe("");
  });

  it("summarizes a push", () => {
    expect(pushSummary(3, 1)).toBe("Will be pushed to 3 servers (1 pending, no key yet)");
    expect(pushSummary(1, 0)).toBe("Will be pushed to 1 server");
  });

  it("overlays sync events per server", () => {
    let s = applySyncEvent({}, { type: "vars.sync_failed", payload: { server_id: "a", error: "x" } });
    expect(s).toEqual({ a: "failed" });
    s = applySyncEvent(s, { type: "vars.synced", payload: { server_id: "a", revision: 2 } });
    expect(s).toEqual({ a: "synced" });
    expect(applySyncEvent(s, { type: "apps_status", payload: { server_id: "a" } })).toBe(s);
  });

  it("maps server names from a key listing", () => {
    const m = serverNameMap({ servers: [{ server_id: "a", name: "alpha", public_key: "K" }], pending_servers: [{ server_id: "p", name: "new" }] });
    expect(m.get("a")).toBe("alpha");
    expect(m.get("p")).toBe("new");
  });

  it("lists the shared names an app sees, never values", () => {
    const v: ServerVariables = {
      variables: [{ ...secret, id: "s1", name: "LOCAL", scope: "server", is_secret: false, value: "x" }],
      inherited: [
        { ...secret, name: "LOCAL", is_secret: false, value: "org", overridden: true },
        { ...secret, overridden: false },
      ],
      builtins: [{ name: "WF_TZ", value: "", description: "tz" }],
      missing_org_secrets: [],
      sync: { server_id: "s", name: "s", has_key: true, revision: 1, agent_revision: 1, state: "synced" },
    };
    const names = sharedNames(v);
    expect(names).toEqual([
      { name: "LOCAL", scope: "server" },
      { name: "SMTP_PASS", scope: "org" },
      { name: "WF_TZ", scope: "builtin" },
    ]);
    expect(overrideNote("LOCAL", names)).toBe("overrides server value");
    expect(overrideNote("SMTP_PASS", names)).toBe("overrides org value");
    expect(overrideNote("WF_TZ", names)).toBe("");
    expect(overrideNote("OTHER", names)).toBe("");
  });
});

describe("createSharedVariablesApi", () => {
  function fakeFetch(data: unknown, status = 200) {
    return vi.fn<typeof fetch>(async () =>
      new Response(JSON.stringify(status === 200 ? { success: true, data } : { success: false, message: "nope" }), { status }),
    );
  }

  it("calls each route with the documented body", async () => {
    const f = fakeFetch({ servers: ["a"] });
    const api = createSharedVariablesApi(f);
    await api.saveServer("srv 1", { name: "X", is_secret: false, value: "1", description: "" });
    await api.deleteServer("srv 1", "v1", true);
    await api.deleteOrg("v2");
    expect(await api.resync({})).toEqual(["a"]);
    expect(await api.reload({ server_id: "a", names: ["X"] })).toEqual(["a"]);
    await api.listServer("srv 1");
    const calls = f.mock.calls.map(([url, init]) => [new URL(String(url)).pathname + new URL(String(url)).search, init?.body ? JSON.parse(String(init.body)) : undefined]);
    expect(calls).toEqual([
      ["/api/v1/server/save-shared-variable", { name: "X", is_secret: false, value: "1", description: "", server_id: "srv 1" }],
      ["/api/v1/server/delete-shared-variable", { server_id: "srv 1", id: "v1", reload: true }],
      ["/api/v1/org/delete-shared-variable", { id: "v2" }],
      ["/api/v1/shared-variables/resync", {}],
      ["/api/v1/shared-variables/reload", { server_id: "a", names: ["X"] }],
      ["/api/v1/server/list-shared-variables?server_id=srv%201", undefined],
    ]);
    expect(f.mock.calls[0][1]?.credentials).toBe("include");
  });

  it("surfaces the API's refusal message", async () => {
    const api = createSharedVariablesApi(fakeFetch(null, 400));
    await expect(api.saveOrg({ name: "X", is_secret: false, description: "" })).rejects.toThrow("nope");
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/shared-variables.test.ts src/lib/shared-variables-crypto.test.ts`
Expected: FAIL: `Failed to resolve import "@/lib/shared-variables"`.

- [ ] **Step 3: Implement**

`web/src/lib/shared-variables.ts`:

```ts
// Org- and server-level shared variables: types mirroring the API, the
// browser fan-out encryption (one ECIES ciphertext per server key, so the
// API never sees a secret), small display helpers and the API client.
import { apiBaseUrl } from "@/config";
import { readApiError } from "@/lib/api";
import { encryptSecret } from "@/lib/ecies";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

export type VariableScope = "org" | "server";

export type SharedVariable = {
  id: string;
  name: string;
  scope: VariableScope;
  is_secret: boolean;
  // "<encrypted>" for secrets: no route ever returns a secret value.
  value: string;
  description: string;
  updated_at: string;
  updated_by: string;
  // Keyed servers in scope without a copy of this secret.
  missing_on: string[];
};

export type InheritedVariable = SharedVariable & { overridden: boolean };

export type BuiltinVariable = { name: string; value: string; description: string };

export type ServerSync = {
  server_id: string;
  name: string;
  has_key: boolean;
  revision: number;
  agent_revision: number | null;
  state: "synced" | "pending";
};

// The browser adds "failed" from vars.sync_failed events.
export type SyncState = "synced" | "pending" | "failed";

export type OrgVariables = { variables: SharedVariable[]; servers: ServerSync[] };

export type ServerVariables = {
  variables: SharedVariable[];
  inherited: InheritedVariable[];
  builtins: BuiltinVariable[];
  missing_org_secrets: string[];
  sync: ServerSync;
};

export type EncryptionKey = { server_id: string; name: string; public_key: string };
export type PendingServer = { server_id: string; name: string };
export type EncryptionKeys = { servers: EncryptionKey[]; pending_servers: PendingServer[] };

export type SaveVariable = {
  name: string;
  is_secret: boolean;
  value?: string;
  ciphertexts?: Record<string, string>;
  description: string;
  reload?: boolean;
};

export type ResyncTarget = { server_id?: string; variable_id?: string };
export type ReloadTarget = { server_id?: string; variable_id?: string; names?: string[]; app_ids?: string[] };

// VarsReloaded is the payload of a vars.reloaded SSE event.
export type VarsReloaded = { server_id: string; app_id: string; ok: boolean; error?: string; names: string[] };

// SharedName is a variable an app can reference, by scope, never its value.
export type SharedName = { name: string; scope: VariableScope | "builtin" };

export const ENCRYPTED = "<encrypted>";

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

// variableNameError mirrors model.ValidateSharedVariableName.
export function variableNameError(name: string): string {
  if (!name) return "Name is required";
  if (name.length > 255) return "Name is longer than 255 characters";
  if (!NAME.test(name)) return "Use letters, digits and _, not starting with a digit";
  if (name === "COMPOSE_PROJECT_NAME" || name.startsWith("WF_")) return `${name} is reserved`;
  return "";
}

// encryptForServers encrypts value once per server key (all of keys, or
// only the server ids in only — a gap fill). Each result is readable only
// by that server's agent.
export async function encryptForServers(
  value: string,
  keys: EncryptionKey[],
  only?: string[],
): Promise<Record<string, string>> {
  const out: Record<string, string> = {};
  for (const k of keys) {
    if (only && !only.includes(k.server_id)) continue;
    out[k.server_id] = await encryptSecret(value, k.public_key);
  }
  return out;
}

export function serverNameMap(keys: EncryptionKeys): Map<string, string> {
  const m = new Map<string, string>();
  for (const s of keys.servers) m.set(s.server_id, s.name);
  for (const s of keys.pending_servers) m.set(s.server_id, s.name);
  return m;
}

export function coverageLabel(v: SharedVariable, names: Map<string, string>): string {
  if (!v.is_secret) return "";
  if (v.missing_on.length === 0) return "All servers";
  return `Missing on ${v.missing_on.map((id) => names.get(id) ?? id).join(", ")}`;
}

export function pushSummary(count: number, pending: number): string {
  const s = `Will be pushed to ${count} ${count === 1 ? "server" : "servers"}`;
  return pending > 0 ? `${s} (${pending} pending, no key yet)` : s;
}

// applySyncEvent overlays a vars.synced / vars.sync_failed event on
// per-server sync states; other events return states unchanged.
export function applySyncEvent(
  states: Record<string, SyncState>,
  n: { type: string; payload?: unknown },
): Record<string, SyncState> {
  const id = (n.payload as { server_id?: string } | undefined)?.server_id;
  if (!id) return states;
  if (n.type === "vars.synced") return { ...states, [id]: "synced" };
  if (n.type === "vars.sync_failed") return { ...states, [id]: "failed" };
  return states;
}

// sharedNames lists what an app on the server can reference: the server's
// variables, the org's it does not override, and the built-ins.
export function sharedNames(v: ServerVariables): SharedName[] {
  const out: SharedName[] = v.variables.map((x) => ({ name: x.name, scope: "server" as const }));
  for (const x of v.inherited) if (!x.overridden) out.push({ name: x.name, scope: "org" });
  for (const b of v.builtins) out.push({ name: b.name, scope: "builtin" });
  return out.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}

// overrideNote tells an app variable it shadows a shared one.
export function overrideNote(name: string, shared: SharedName[]): string {
  const hit = shared.find((s) => s.name === name);
  if (!hit || hit.scope === "builtin") return "";
  return `overrides ${hit.scope} value`;
}

export type SharedVariablesApi = {
  listOrg(): Promise<OrgVariables>;
  listServer(serverId: string): Promise<ServerVariables>;
  keys(): Promise<EncryptionKeys>;
  saveOrg(body: SaveVariable): Promise<SharedVariable>;
  saveServer(serverId: string, body: SaveVariable): Promise<SharedVariable>;
  deleteOrg(id: string, reload?: boolean): Promise<void>;
  deleteServer(serverId: string, id: string, reload?: boolean): Promise<void>;
  resync(target: ResyncTarget): Promise<string[]>;
  reload(target: ReloadTarget): Promise<string[]>;
};

export function createSharedVariablesApi(fetchImpl?: typeof fetch): SharedVariablesApi {
  const doFetch: typeof fetch = fetchImpl ?? ((input, init) => fetch(input, init));
  async function call<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await doFetch(`${base}${path}`, { credentials: "include", ...init });
    if (!res.ok) throw await readApiError(res);
    return ((await res.json()) as { data: T }).data;
  }
  function post<T>(path: string, payload: unknown): Promise<T> {
    return call<T>(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
  }
  const servers = (d: { servers?: string[] | null } | null) => d?.servers ?? [];
  return {
    listOrg: () => call<OrgVariables>("/api/v1/org/list-shared-variables"),
    listServer: (serverId) =>
      call<ServerVariables>(`/api/v1/server/list-shared-variables?server_id=${encodeURIComponent(serverId)}`),
    keys: () => call<EncryptionKeys>("/api/v1/org/list-encryption-keys"),
    saveOrg: (body) => post<SharedVariable>("/api/v1/org/save-shared-variable", body),
    saveServer: (serverId, body) =>
      post<SharedVariable>("/api/v1/server/save-shared-variable", { ...body, server_id: serverId }),
    deleteOrg: async (id, reload) => {
      await post<null>("/api/v1/org/delete-shared-variable", reload ? { id, reload } : { id });
    },
    deleteServer: async (serverId, id, reload) => {
      await post<null>(
        "/api/v1/server/delete-shared-variable",
        reload ? { server_id: serverId, id, reload } : { server_id: serverId, id },
      );
    },
    resync: (target) => post<{ servers?: string[] | null }>("/api/v1/shared-variables/resync", target).then(servers),
    reload: (target) => post<{ servers?: string[] | null }>("/api/v1/shared-variables/reload", target).then(servers),
  };
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/shared-variables.test.ts src/lib/shared-variables-crypto.test.ts && pnpm --dir web run lint`
Expected: PASS; lint clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/shared-variables.ts web/src/lib/shared-variables.test.ts web/src/lib/shared-variables-crypto.test.ts
git commit -m "feat(web): shared variables client and per-server encryption

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 13: Web components: `VariablesTable` and `SharedVariableDialog`

**Files:**
- Create: `web/src/components/variables-table.tsx`, `web/src/components/variables-table.test.tsx`
- Create: `web/src/components/shared-variable-dialog.tsx`, `web/src/components/shared-variable-dialog.test.tsx`

**Interfaces:**
- Consumes: Task 12 types and helpers (`coverageLabel`, `encryptForServers`, `pushSummary`, `serverNameMap`, `variableNameError`).
- Produces:
  - `VariablesTable(props: VariablesTableProps)` with `VariablesTableProps = { title: string; variables: SharedVariable[]; inherited?: InheritedVariable[]; builtins?: BuiltinVariable[]; serverNames: Map<string, string>; showCoverage?: boolean; onEdit(v); onDelete(v); onReload(v); onProvide?(v); onResync?(v) }`. Groups are `<section aria-label>`s titled `title`, "Inherited from org" and "Built-in". Row actions carry the aria-labels `Edit NAME`, `Delete NAME`, `Reload apps using NAME`, `Resync NAME`, and a "Provide value" button.
  - `DialogMode = { kind: "create" } | { kind: "edit"; variable: SharedVariable } | { kind: "provide"; variable: SharedVariable }`
  - `SharedVariableDialog({ open, mode, keys, serverId?, onOpenChange, onSave })` where `onSave(body: SaveVariable): Promise<void>`; labels "Name", "Secret" (switch), "Value", "Description", "Reload affected apps now" (checkbox, default off); buttons "Save" / "Save and push" (new org-wide secret value), "Replace value", "Cancel".

- [ ] **Step 1: Write the failing tests**

`web/src/components/variables-table.test.tsx`:

```tsx
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { VariablesTable } from "@/components/variables-table";
import type { InheritedVariable, SharedVariable } from "@/lib/shared-variables";

const base: SharedVariable = {
  id: "v1", name: "SMTP_HOST", scope: "server", is_secret: false, value: "mail.local",
  description: "relay", updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: [],
};
const secret: SharedVariable = { ...base, id: "v2", name: "SMTP_PASS", is_secret: true, value: "<encrypted>", missing_on: ["s3"] };

function renderTable(extra: Partial<Parameters<typeof VariablesTable>[0]> = {}) {
  const handlers = { onEdit: vi.fn(), onDelete: vi.fn(), onReload: vi.fn(), onProvide: vi.fn(), onResync: vi.fn() };
  render(
    <MemoryRouter>
      <VariablesTable title="This server" variables={[base, secret]} serverNames={new Map([["s3", "srv-3"]])} {...handlers} {...extra} />
    </MemoryRouter>,
  );
  return handlers;
}

describe("VariablesTable", () => {
  it("shows plain values and never a secret value", () => {
    renderTable();
    const own = screen.getByRole("region", { name: "This server" });
    expect(within(own).getByText("mail.local")).toBeInTheDocument();
    expect(within(own).getByText("encrypted")).toBeInTheDocument();
    expect(screen.queryByText("<encrypted>")).not.toBeInTheDocument();
  });

  it("marks coverage and offers Provide value for missing secrets", async () => {
    const user = userEvent.setup();
    const h = renderTable({ showCoverage: true });
    expect(screen.getByText("Missing on srv-3")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Provide value" }));
    expect(h.onProvide).toHaveBeenCalledWith(secret);
    await user.click(screen.getByRole("button", { name: "Reload apps using SMTP_HOST" }));
    expect(h.onReload).toHaveBeenCalledWith(base);
    await user.click(screen.getByRole("button", { name: "Edit SMTP_PASS" }));
    expect(h.onEdit).toHaveBeenCalledWith(secret);
    await user.click(screen.getByRole("button", { name: "Delete SMTP_HOST" }));
    expect(h.onDelete).toHaveBeenCalledWith(base);
    await user.click(screen.getByRole("button", { name: "Resync SMTP_HOST" }));
    expect(h.onResync).toHaveBeenCalledWith(base);
  });

  it("groups inherited org variables read-only with overridden marks, then built-ins", () => {
    const inherited: InheritedVariable[] = [
      { ...base, id: "o1", scope: "org", value: "mail.org", overridden: true },
      { ...secret, id: "o2", scope: "org", overridden: false },
    ];
    renderTable({ inherited, builtins: [{ name: "WF_SERVER_NAME", value: "alpha", description: "This server's name" }, { name: "WF_TZ", value: "", description: "tz" }] });
    const org = screen.getByRole("region", { name: "Inherited from org" });
    expect(within(org).getByText("overridden")).toBeInTheDocument();
    expect(within(org).getByText("missing here")).toBeInTheDocument();
    expect(within(org).queryByRole("button", { name: /^Edit|^Delete/ })).not.toBeInTheDocument();
    expect(within(org).getByRole("link", { name: "Manage org variables" })).toHaveAttribute("href", "/org/variables");
    const builtins = screen.getByRole("region", { name: "Built-in" });
    expect(within(builtins).getByText("alpha")).toBeInTheDocument();
    expect(within(builtins).getByText("set by the agent")).toBeInTheDocument();
  });
});
```

`web/src/components/shared-variable-dialog.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

import { SharedVariableDialog, type DialogMode } from "@/components/shared-variable-dialog";
import type { EncryptionKeys, SharedVariable } from "@/lib/shared-variables";

const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "a", name: "alpha", public_key: "KA" },
    { server_id: "b", name: "beta", public_key: "KB" },
  ],
  pending_servers: [{ server_id: "p", name: "new-box" }],
};
const SECRET: SharedVariable = {
  id: "v2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>",
  description: "old", updated_at: "2026-10-11T00:00:00Z", updated_by: "u1", missing_on: ["b"],
};

function renderDialog(mode: DialogMode, serverId?: string) {
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<SharedVariableDialog open mode={mode} keys={KEYS} serverId={serverId} onOpenChange={() => {}} onSave={onSave} />);
  return onSave;
}

describe("SharedVariableDialog", () => {
  it("adds a plain variable with reload off by default", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "create" });
    expect(screen.getByRole("checkbox", { name: "Reload affected apps now" })).not.toBeChecked();
    await user.type(screen.getByLabelText("Name"), "SMTP_HOST");
    await user.type(screen.getByLabelText("Value"), "mail.example");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_HOST", is_secret: false, value: "mail.example", description: "" });
  });

  it("encrypts a new org secret for every keyed server and says where it goes", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "SMTP_PASS");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.type(screen.getByLabelText("Value"), "s3cret");
    expect(screen.getByText("Will be pushed to 2 servers (1 pending, no key yet)")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Reload affected apps now" }));
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "SMTP_PASS", is_secret: true, description: "", reload: true,
      ciphertexts: { a: "ct(KA):s3cret", b: "ct(KB):s3cret" },
    });
  });

  it("encrypts a server secret for that server only", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "create" }, "b");
    await user.type(screen.getByLabelText("Name"), "LOCAL_KEY");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.type(screen.getByLabelText("Value"), "k");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "LOCAL_KEY", is_secret: true, description: "", ciphertexts: { b: "ct(KB):k" } });
  });

  it("locks an existing secret: description edits keep the value", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "edit", variable: SECRET });
    expect(screen.getByLabelText("Name")).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Secret" })).toBeDisabled();
    expect(screen.queryByLabelText("Value")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_PASS", is_secret: true, description: "old" });
  });

  it("replaces an existing secret's value everywhere", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "edit", variable: SECRET });
    await user.click(screen.getByRole("button", { name: "Replace value" }));
    await user.type(screen.getByLabelText("Value"), "n3w");
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "SMTP_PASS", is_secret: true, description: "old",
      ciphertexts: { a: "ct(KA):n3w", b: "ct(KB):n3w" },
    });
  });

  it("provides a missing value for the missing servers only", async () => {
    const user = userEvent.setup();
    const onSave = renderDialog({ kind: "provide", variable: SECRET });
    expect(screen.getByText("Encrypts the value for: beta")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "SMTP_PASS", is_secret: true, description: "old", ciphertexts: { b: "ct(KB):s3cret" } });
  });

  it("rejects a reserved name before saving", async () => {
    const user = userEvent.setup();
    renderDialog({ kind: "create" });
    await user.type(screen.getByLabelText("Name"), "WF_X");
    expect(screen.getByText("WF_X is reserved")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/variables-table.test.tsx src/components/shared-variable-dialog.test.tsx`
Expected: FAIL: cannot resolve `@/components/variables-table` / `@/components/shared-variable-dialog`.

- [ ] **Step 3: Implement**

`web/src/components/variables-table.tsx`:

```tsx
import { Link } from "react-router-dom";
import { Lock, Pencil, RefreshCw, RotateCw, Trash2 } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  coverageLabel,
  type BuiltinVariable,
  type InheritedVariable,
  type SharedVariable,
} from "@/lib/shared-variables";

export type VariablesTableProps = {
  title: string;
  variables: SharedVariable[];
  inherited?: InheritedVariable[];
  builtins?: BuiltinVariable[];
  serverNames: Map<string, string>;
  // Org page: a Coverage column ("All servers" / "Missing on …").
  showCoverage?: boolean;
  onEdit: (v: SharedVariable) => void;
  onDelete: (v: SharedVariable) => void;
  onReload: (v: SharedVariable) => void;
  onProvide?: (v: SharedVariable) => void;
  onResync?: (v: SharedVariable) => void;
};

// Value never renders a secret: the API only ever sends "<encrypted>".
function Value({ v }: { v: SharedVariable }) {
  if (v.is_secret) {
    return (
      <span className="inline-flex items-center gap-1 text-muted-foreground">
        <Lock className="size-3" /> encrypted
      </span>
    );
  }
  return <code className="break-all text-xs">{v.value}</code>;
}

function Empty({ children }: { children: string }) {
  return <p className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">{children}</p>;
}

// VariablesTable lists shared variables in up to three groups: the
// editable ones (title), the org's as seen from a server (read-only), and
// the agent's built-ins (read-only). Shared by the server Variables tab and
// the org variables page.
export function VariablesTable({
  title,
  variables,
  inherited,
  builtins,
  serverNames,
  showCoverage,
  onEdit,
  onDelete,
  onReload,
  onProvide,
  onResync,
}: VariablesTableProps) {
  return (
    <div className="space-y-8">
      <section aria-label={title} className="space-y-2">
        <h3 className="text-sm font-medium">{title}</h3>
        {variables.length === 0 ? (
          <Empty>No variables yet.</Empty>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Value</TableHead>
                {showCoverage ? <TableHead>Coverage</TableHead> : null}
                <TableHead>Description</TableHead>
                <TableHead className="w-0" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {variables.map((v) => (
                <TableRow key={v.id}>
                  <TableCell className="font-mono">{v.name}</TableCell>
                  <TableCell>
                    <Value v={v} />
                  </TableCell>
                  {showCoverage ? (
                    <TableCell className={v.missing_on.length > 0 ? "text-amber-700 dark:text-amber-400" : undefined}>
                      {v.is_secret ? coverageLabel(v, serverNames) : "—"}
                    </TableCell>
                  ) : null}
                  <TableCell className="text-muted-foreground">{v.description}</TableCell>
                  <TableCell>
                    <div className="flex justify-end gap-1">
                      {onProvide && v.is_secret && v.missing_on.length > 0 ? (
                        <Button size="sm" variant="outline" onClick={() => onProvide(v)}>
                          Provide value
                        </Button>
                      ) : null}
                      <Button size="icon" variant="ghost" title="Reload apps using this" aria-label={`Reload apps using ${v.name}`} onClick={() => onReload(v)}>
                        <RotateCw className="size-4" />
                      </Button>
                      {onResync ? (
                        <Button size="icon" variant="ghost" title="Resync" aria-label={`Resync ${v.name}`} onClick={() => onResync(v)}>
                          <RefreshCw className="size-4" />
                        </Button>
                      ) : null}
                      <Button size="icon" variant="ghost" aria-label={`Edit ${v.name}`} onClick={() => onEdit(v)}>
                        <Pencil className="size-4" />
                      </Button>
                      <Button size="icon" variant="ghost" className="hover:text-destructive" aria-label={`Delete ${v.name}`} onClick={() => onDelete(v)}>
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </section>

      {inherited ? (
        <section aria-label="Inherited from org" className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium">Inherited from org</h3>
            <Link to="/org/variables" className="text-sm underline-offset-4 hover:underline">
              Manage org variables
            </Link>
          </div>
          {inherited.length === 0 ? (
            <Empty>The organization has no variables.</Empty>
          ) : (
            <Table>
              <TableBody>
                {inherited.map((v) => {
                  const missing = v.is_secret && v.missing_on.length > 0 && !v.overridden;
                  return (
                    <TableRow key={v.id} className={v.overridden ? "opacity-60" : undefined}>
                      <TableCell className="font-mono">{v.name}</TableCell>
                      <TableCell>
                        <Value v={v} />
                      </TableCell>
                      <TableCell>
                        {v.overridden ? <Badge variant="outline">overridden</Badge> : null}
                        {missing ? <Badge variant="destructive">missing here</Badge> : null}
                      </TableCell>
                      <TableCell className="text-right">
                        {missing && onProvide ? (
                          <Button size="sm" variant="outline" onClick={() => onProvide(v)}>
                            Provide value
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </section>
      ) : null}

      {builtins ? (
        <section aria-label="Built-in" className="space-y-2">
          <h3 className="text-sm font-medium">Built-in</h3>
          <Table>
            <TableBody>
              {builtins.map((b) => (
                <TableRow key={b.name}>
                  <TableCell className="font-mono">{b.name}</TableCell>
                  <TableCell>
                    {b.value ? <code className="text-xs">{b.value}</code> : <span className="text-muted-foreground">set by the agent</span>}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{b.description}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      ) : null}
    </div>
  );
}
```

(If the "Provide value" query in the test matches two buttons, check the fixture: `secret` is in `variables` and not in `inherited` there, so only one renders.)

`web/src/components/shared-variable-dialog.tsx`:

```tsx
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  encryptForServers,
  pushSummary,
  serverNameMap,
  variableNameError,
  type EncryptionKeys,
  type SaveVariable,
  type SharedVariable,
} from "@/lib/shared-variables";

export type DialogMode =
  | { kind: "create" }
  | { kind: "edit"; variable: SharedVariable }
  | { kind: "provide"; variable: SharedVariable };

// SharedVariableDialog adds or edits a shared variable, or provides a
// missing secret for the servers that lack it (gap fill). serverId set =
// a server-scope variable, whose secret is encrypted for that server only.
// An existing secret's name, kind and value are locked: "Replace value"
// sends a new value to every keyed server; anything else is delete and
// recreate. Secret values are encrypted here and cleared on close. The
// parent remounts it (key) per opening.
export function SharedVariableDialog({
  open,
  mode,
  keys,
  serverId,
  onOpenChange,
  onSave,
}: {
  open: boolean;
  mode: DialogMode;
  keys: EncryptionKeys;
  serverId?: string;
  onOpenChange: (open: boolean) => void;
  onSave: (body: SaveVariable) => Promise<void>;
}) {
  const existing = mode.kind === "create" ? null : mode.variable;
  const provide = mode.kind === "provide";
  const [name, setName] = useState(existing?.name ?? "");
  const [secret, setSecret] = useState(existing?.is_secret ?? false);
  const [value, setValue] = useState(existing && !existing.is_secret ? existing.value : "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [replace, setReplace] = useState(false);
  const [reload, setReload] = useState(false);
  const [busy, setBusy] = useState(false);

  const names = serverNameMap(keys);
  const scopeKeys = serverId ? keys.servers.filter((k) => k.server_id === serverId) : keys.servers;
  const pending = serverId ? keys.pending_servers.filter((p) => p.server_id === serverId) : keys.pending_servers;
  const typingSecret = secret && (mode.kind === "create" || provide || replace);
  const lockedSecret = mode.kind === "edit" && secret && !replace;
  const trimmed = name.trim();
  const nameError = mode.kind === "create" && trimmed ? variableNameError(trimmed) : "";
  const noKey = typingSecret && !provide && serverId !== undefined && scopeKeys.length === 0;
  const canSave = !busy && trimmed !== "" && !nameError && !noKey && (!typingSecret || value !== "");
  const pushesEverywhere = typingSecret && !provide && serverId === undefined;

  // The typed value must not outlive the dialog.
  const changeOpen = (next: boolean) => {
    if (!next) setValue("");
    onOpenChange(next);
  };

  const save = async () => {
    setBusy(true);
    try {
      const body: SaveVariable = { name: trimmed, is_secret: secret, description };
      if (reload) body.reload = true;
      if (!secret) {
        body.value = value;
      } else if (provide && existing) {
        body.ciphertexts = await encryptForServers(value, keys.servers, existing.missing_on);
      } else if (typingSecret) {
        body.ciphertexts = await encryptForServers(value, scopeKeys);
      }
      await onSave(body);
      changeOpen(false);
    } catch (e) {
      toast.error("Couldn't save the variable", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  };

  const title = provide ? `Provide ${existing?.name}` : existing ? `Edit ${existing.name}` : "Add variable";
  const missingNames = existing?.missing_on.map((id) => names.get(id) ?? id).join(", ") ?? "";

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {provide
              ? `Encrypts the value for: ${missingNames}`
              : "Apps use it as ${NAME}. Secret values are encrypted in your browser, once per server."}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="sv-name">Name</Label>
            <Input
              id="sv-name"
              value={name}
              disabled={existing !== null}
              placeholder="SMTP_PASS"
              className="font-mono"
              onChange={(e) => setName(e.target.value)}
            />
            {nameError ? <p className="text-sm text-destructive">{nameError}</p> : null}
          </div>
          {provide ? null : (
            <div className="flex items-center gap-2">
              <Switch id="sv-secret" checked={secret} disabled={existing !== null} onCheckedChange={setSecret} />
              <Label htmlFor="sv-secret">Secret</Label>
            </div>
          )}
          {lockedSecret ? (
            <div className="flex items-center justify-between gap-2 rounded-md border p-3 text-sm">
              <span className="text-muted-foreground">The value is encrypted and can't be shown.</span>
              <Button size="sm" variant="outline" onClick={() => setReplace(true)}>
                Replace value
              </Button>
            </div>
          ) : (
            <div className="space-y-2">
              <Label htmlFor="sv-value">Value</Label>
              <Textarea id="sv-value" rows={3} value={value} className="font-mono" onChange={(e) => setValue(e.target.value)} />
              {typingSecret && !provide ? (
                <p className="text-sm text-muted-foreground">
                  {noKey ? "This server has no public key yet, so it can't receive secrets." : pushSummary(scopeKeys.length, pending.length)}
                </p>
              ) : null}
            </div>
          )}
          {provide ? null : (
            <div className="space-y-2">
              <Label htmlFor="sv-description">Description</Label>
              <Input id="sv-description" value={description} onChange={(e) => setDescription(e.target.value)} />
            </div>
          )}
          <div className="flex items-center gap-2">
            <Checkbox id="sv-reload" checked={reload} onCheckedChange={(c) => setReload(c === true)} />
            <Label htmlFor="sv-reload">Reload affected apps now</Label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => changeOpen(false)}>
            Cancel
          </Button>
          <Button disabled={!canSave} onClick={() => void save()}>
            {pushesEverywhere ? "Save and push" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/components/variables-table.test.tsx src/components/shared-variable-dialog.test.tsx && pnpm --dir web run lint`
Expected: PASS; lint clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/variables-table.tsx web/src/components/variables-table.test.tsx web/src/components/shared-variable-dialog.tsx web/src/components/shared-variable-dialog.test.tsx
git commit -m "feat(web): shared variables table and add/edit/provide dialog

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 14: Server settings "Variables" tab

**Files:**
- Create: `web/src/lib/app-status.ts`, `web/src/lib/app-status.test.ts`
- Modify: `web/src/context/apps-context-base.ts`, `web/src/context/apps-context.tsx` (stale map from `apps.status`)
- Create: `web/src/pages/settings-variables.tsx`, `web/src/pages/settings-variables.test.tsx`
- Modify: `web/src/pages/settings.tsx`, `web/src/pages/settings.test.tsx`

**Interfaces:**
- Consumes: Task 12 `createSharedVariablesApi`, `applySyncEvent`, `serverNameMap`, types; Task 13 `VariablesTable`, `SharedVariableDialog`, `DialogMode`; `useNotifications().subscribe`.
- Produces:
  - `AppStatusWire = { app_id: string; status_code: number; stale_vars?: string[] | null }` and `splitStatus(apps: AppStatusWire[] | null | undefined): { status: Record<string, number>; stale: Record<string, string[]> }` in `web/src/lib/app-status.ts`
  - `AppsContextValue.staleVarsByApp: Record<string, string[]>` (app id → stale shared names; stale apps only), filled from `get-apps-status` and `apps_status` SSE
  - `VariablesCard({ serverId, api }: { serverId: string; api: SharedVariablesApi })`; the settings tab `?tab=variables` labelled "Variables" (between Domains and Agent).

- [ ] **Step 1: Write the failing tests**

`web/src/lib/app-status.test.ts`:

```ts
import { describe, expect, it } from "vitest";

import { splitStatus } from "@/lib/app-status";

describe("splitStatus", () => {
  it("splits a status report into status codes and stale names", () => {
    expect(
      splitStatus([
        { app_id: "a", status_code: 1, stale_vars: ["SMTP_PASS"] },
        { app_id: "b", status_code: 5, stale_vars: [] },
        { app_id: "c", status_code: 2, stale_vars: null },
        { app_id: "d", status_code: 1 },
      ]),
    ).toEqual({ status: { a: 1, b: 5, c: 2, d: 1 }, stale: { a: ["SMTP_PASS"] } });
    expect(splitStatus(null)).toEqual({ status: {}, stale: {} });
  });
});
```

`web/src/pages/settings-variables.test.tsx`:

```tsx
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));

import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { EncryptionKeys, ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";
import { VariablesCard } from "@/pages/settings-variables";

const T = "2026-10-11T00:00:00Z";
const SERVER: ServerVariables = {
  variables: [{ id: "s1", name: "SMTP_HOST", scope: "server", is_secret: false, value: "local", description: "", updated_at: T, updated_by: "u", missing_on: [] }],
  inherited: [
    { id: "o1", name: "SMTP_HOST", scope: "org", is_secret: false, value: "org-mail", description: "", updated_at: T, updated_by: "u", missing_on: [], overridden: true },
    { id: "o2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: ["srv-a"], overridden: false },
  ],
  builtins: [{ name: "WF_SERVER_NAME", value: "alpha", description: "This server's name" }],
  missing_org_secrets: ["SMTP_PASS"],
  sync: { server_id: "srv-a", name: "alpha", has_key: true, revision: 3, agent_revision: 2, state: "pending" },
};
const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "srv-a", name: "alpha", public_key: "KA" },
    { server_id: "srv-b", name: "beta", public_key: "KB" },
  ],
  pending_servers: [],
};

function fakeApi() {
  return {
    listOrg: vi.fn(),
    listServer: vi.fn().mockResolvedValue(SERVER),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn().mockResolvedValue({}),
    deleteOrg: vi.fn(),
    deleteServer: vi.fn().mockResolvedValue(undefined),
    resync: vi.fn().mockResolvedValue(["srv-a"]),
    reload: vi.fn().mockResolvedValue(["srv-a"]),
  };
}

function renderCard(api: ReturnType<typeof fakeApi>) {
  const hub: { h?: NotificationHandler } = {};
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      hub.h = h;
      return () => {
        hub.h = undefined;
      };
    },
    waitFor: vi.fn(),
  };
  const apps = { staleVarsByApp: { a1: ["SMTP_PASS"], a2: ["SMTP_HOST"], a3: [] } } as unknown as AppsContextValue;
  render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={apps}>
          <VariablesCard serverId="srv-a" api={api as unknown as SharedVariablesApi} />
        </AppsContext.Provider>
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
  return { emit: (n: Notification) => act(() => hub.h?.(n)) };
}

describe("VariablesCard", () => {
  it("shows this server's, the inherited and the built-in variables", async () => {
    renderCard(fakeApi());
    expect(await screen.findByText("local")).toBeInTheDocument();
    const org = screen.getByRole("region", { name: "Inherited from org" });
    expect(within(org).getByText("overridden")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Built-in" })).getByText("alpha")).toBeInTheDocument();
    expect(screen.getByText("Sync pending")).toBeInTheDocument();
  });

  it("follows the sync state over SSE", async () => {
    const { emit } = renderCard(fakeApi());
    await screen.findByText("Sync pending");
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "srv-a", error: "boom" } });
    expect(screen.getByText("Sync failed")).toBeInTheDocument();
    emit({ type: "vars.synced", ref: "", payload: { server_id: "srv-b", revision: 1 } });
    expect(screen.getByText("Sync failed")).toBeInTheDocument();
    emit({ type: "vars.synced", ref: "", payload: { server_id: "srv-a", revision: 3 } });
    expect(screen.getByText("Synced")).toBeInTheDocument();
  });

  it("asks for missing org secrets and encrypts them for this server only", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    expect(await screen.findByText("1 org secret isn't available on this server")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Provide values" }));
    expect(screen.getByText("Encrypts the value for: alpha")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "s3cret");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.saveOrg).toHaveBeenCalledWith({
      name: "SMTP_PASS", is_secret: true, description: "", ciphertexts: { "srv-a": "ct(KA):s3cret" },
    });
    expect(api.listServer).toHaveBeenCalledTimes(2); // reloaded after the save
  });

  it("resyncs, reloads stale apps and reloads by variable", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    await screen.findByText("local");
    await user.click(screen.getByRole("button", { name: "Reload stale apps (2)" }));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a" });
    await user.click(screen.getByRole("button", { name: "Resync" }));
    expect(api.resync).toHaveBeenCalledWith({ server_id: "srv-a" });
    await user.click(screen.getByRole("button", { name: "Reload apps using SMTP_HOST" }));
    expect(api.reload).toHaveBeenCalledWith({ server_id: "srv-a", names: ["SMTP_HOST"] });
  });

  it("adds a server variable", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    await screen.findByText("local");
    await user.click(screen.getByRole("button", { name: "Add variable" }));
    await user.type(screen.getByLabelText("Name"), "LOCAL_DIR");
    await user.type(screen.getByLabelText("Value"), "/srv");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.saveServer).toHaveBeenCalledWith("srv-a", { name: "LOCAL_DIR", is_secret: false, value: "/srv", description: "" });
  });

  it("deletes after confirmation", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderCard(api);
    await screen.findByText("local");
    await user.click(screen.getByRole("button", { name: "Delete SMTP_HOST" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(api.deleteServer).toHaveBeenCalledWith("srv-a", "s1");
  });
});
```

`web/src/pages/settings.test.tsx`: add the mock next to the other page mocks:

```tsx
vi.mock("@/pages/settings-variables", () => ({ VariablesCard: () => <p>variables card</p> }));
```

and the test:

```tsx
  it("offers the Variables tab", () => {
    ingress = false;
    renderAt("/settings?tab=variables");
    expect(screen.getByRole("tab", { name: "Variables" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("variables card")).toBeInTheDocument();
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/lib/app-status.test.ts src/pages/settings-variables.test.tsx src/pages/settings.test.tsx`
Expected: FAIL: cannot resolve `@/lib/app-status` / `@/pages/settings-variables`; the settings test finds no "Variables" tab.

- [ ] **Step 3: Implement**

`web/src/lib/app-status.ts`:

```ts
// AppStatusWire is one app of an apps.status report as the API sends it
// (get-apps-status and the apps_status SSE event).
export type AppStatusWire = {
  app_id: string;
  status_code: number;
  // Shared variables whose rendered values differ from the agent's
  // current snapshot; empty/absent = fresh.
  stale_vars?: string[] | null;
};

// splitStatus turns a report into the apps context's two maps: container
// status codes, and the stale shared names of stale apps only.
export function splitStatus(apps: AppStatusWire[] | null | undefined): {
  status: Record<string, number>;
  stale: Record<string, string[]>;
} {
  const status: Record<string, number> = {};
  const stale: Record<string, string[]> = {};
  for (const a of apps ?? []) {
    status[a.app_id] = a.status_code;
    if (a.stale_vars && a.stale_vars.length > 0) stale[a.app_id] = a.stale_vars;
  }
  return { status, stale };
}
```

`web/src/context/apps-context-base.ts`: in `AppsContextValue`, after `statusByApp`, add

```ts
  // staleVarsByApp maps app id -> the shared variables changed since it
  // was last rendered (stale apps only; live from apps_status).
  staleVarsByApp: Record<string, string[]>;
```

`web/src/context/apps-context.tsx`:

- replace the local `AppStatusResponse` type with `import { splitStatus, type AppStatusWire } from "@/lib/app-status";` and type `GetAppsStatusResponse` as `{ data?: { apps?: AppStatusWire[] | null } }`;
- add state `const [staleVarsByApp, setStaleVarsByApp] = useState<Record<string, string[]>>({});` next to `statusByApp`;
- in `loadStatus`, replace the map-building loop with

```ts
      const { status, stale } = splitStatus(result.data?.apps);
      setStatusByApp(status);
      setStaleVarsByApp(stale);
```

- in the `apps_status` SSE effect, type the payload's apps as `AppStatusWire[] | null` and replace its loop the same way:

```ts
      const { status, stale } = splitStatus(p.apps);
      setStatusByApp(status);
      setStaleVarsByApp(stale);
```

- add `staleVarsByApp` to the `value` object and to its `useMemo` dependency list, right after `statusByApp`.

`web/src/pages/settings-variables.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus, RefreshCw, RotateCw } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { SharedVariableDialog, type DialogMode } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
import { useApps } from "@/context/use-apps";
import { useNotifications } from "@/context/use-notifications";
import {
  applySyncEvent,
  serverNameMap,
  type EncryptionKeys,
  type SaveVariable,
  type ServerVariables,
  type SharedVariable,
  type SharedVariablesApi,
  type SyncState,
} from "@/lib/shared-variables";

const NO_KEYS: EncryptionKeys = { servers: [], pending_servers: [] };
const SYNC_LABEL: Record<SyncState, string> = { synced: "Synced", pending: "Sync pending", failed: "Sync failed" };

function errorText(e: unknown): string | undefined {
  return e instanceof Error ? e.message : undefined;
}

// VariablesCard is the server settings "Variables" tab: this server's
// shared variables (editable), the org's as inherited here (read-only,
// "overridden" when shadowed), the agent's built-ins, a banner for org
// secrets this server has no copy of, and the sync state (from the API,
// then from vars.synced / vars.sync_failed events).
export function VariablesCard({ serverId, api }: { serverId: string; api: SharedVariablesApi }) {
  const { staleVarsByApp } = useApps();
  const { subscribe } = useNotifications();
  const [data, setData] = useState<ServerVariables | null>(null);
  const [keys, setKeys] = useState<EncryptionKeys>(NO_KEYS);
  const [sync, setSync] = useState<Record<string, SyncState>>({});
  const [dialog, setDialog] = useState<DialogMode | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<SharedVariable | null>(null);

  const load = useCallback(async () => {
    try {
      const [vars, k] = await Promise.all([api.listServer(serverId), api.keys()]);
      setData(vars);
      setKeys(k);
      setSync((s) => ({ ...s, [serverId]: vars.sync.state }));
    } catch (e) {
      toast.error("Failed to load variables", { description: errorText(e) });
    }
  }, [api, serverId]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => subscribe((n) => setSync((s) => applySyncEvent(s, n))), [subscribe]);

  const staleCount = Object.values(staleVarsByApp).filter((names) => names.length > 0).length;
  const names = useMemo(() => serverNameMap(keys), [keys]);
  const state = sync[serverId] ?? "pending";
  const missing = data?.missing_org_secrets ?? [];
  const firstMissing = data?.inherited.find((v) => missing.includes(v.name));

  const open = (mode: DialogMode) => {
    setDialogKey((k) => k + 1);
    setDialog(mode);
  };

  // A provide dialog on an inherited row fills the org secret's gap; the
  // rest are this server's variables.
  const save = async (body: SaveVariable) => {
    if (dialog && dialog.kind !== "create" && dialog.variable.scope === "org") await api.saveOrg(body);
    else await api.saveServer(serverId, body);
    toast.success("Variable saved");
    await load();
  };

  const act = (done: string, fn: () => Promise<unknown>) => {
    fn().then(
      () => toast.success(done),
      (e: unknown) => toast.error("Request failed", { description: errorText(e) }),
    );
  };

  const remove = async (v: SharedVariable) => {
    try {
      await api.deleteServer(serverId, v.id);
      toast.success("Variable deleted");
      await load();
    } catch (e) {
      toast.error("Couldn't delete the variable", { description: errorText(e) });
    }
  };

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
        <div className="min-w-0 space-y-1.5">
          <CardTitle className="flex items-center gap-2">
            Variables
            {data ? <Badge variant={state === "failed" ? "destructive" : "secondary"}>{SYNC_LABEL[state]}</Badge> : null}
          </CardTitle>
          <CardDescription>
            Available to every app on this server as <code>{"${NAME}"}</code>. Server values override org
            values; an app's own variables override both.
          </CardDescription>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              act("Resync sent", () => {
                setSync((s) => ({ ...s, [serverId]: "pending" }));
                return api.resync({ server_id: serverId });
              })
            }
          >
            <RefreshCw className="size-4" /> Resync
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={staleCount === 0}
            onClick={() => act("Reload queued", () => api.reload({ server_id: serverId }))}
          >
            <RotateCw className="size-4" /> Reload stale apps ({staleCount})
          </Button>
          <Button size="sm" onClick={() => open({ kind: "create" })}>
            <Plus className="size-4" /> Add variable
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {missing.length > 0 ? (
          <Alert>
            <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
              <span>
                {missing.length === 1
                  ? "1 org secret isn't available on this server"
                  : `${missing.length} org secrets aren't available on this server`}
              </span>
              {firstMissing ? (
                <Button size="sm" variant="outline" onClick={() => open({ kind: "provide", variable: firstMissing })}>
                  Provide values
                </Button>
              ) : null}
            </AlertDescription>
          </Alert>
        ) : null}
        {data === null ? (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        ) : (
          <VariablesTable
            title="This server"
            variables={data.variables}
            inherited={data.inherited}
            builtins={data.builtins}
            serverNames={names}
            onEdit={(v) => open({ kind: "edit", variable: v })}
            onDelete={setToDelete}
            onProvide={(v) => open({ kind: "provide", variable: v })}
            onReload={(v) => act("Reload queued", () => api.reload({ server_id: serverId, names: [v.name] }))}
          />
        )}
      </CardContent>

      {dialog ? (
        <SharedVariableDialog
          key={dialogKey}
          open
          mode={dialog}
          keys={keys}
          serverId={dialog.kind === "provide" ? undefined : serverId}
          onOpenChange={(o) => {
            if (!o) setDialog(null);
          }}
          onSave={save}
        />
      ) : null}

      <AlertDialog
        open={toDelete !== null}
        onOpenChange={(o) => {
          if (!o) setToDelete(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {toDelete?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Apps that use it fall back to the org value, if there is one, on their next deploy or reload.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                const v = toDelete;
                setToDelete(null);
                if (v) void remove(v);
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
```

`web/src/pages/settings.tsx`:

- imports: add `import { VariablesCard } from "@/pages/settings-variables";` and `import { createSharedVariablesApi } from "@/lib/shared-variables";`
- `const TABS = ["registries", "networks", "volumes", "domains", "variables", "agent"] as const;`
- inside `SettingsPage`, after `const docker = useDocker();`: `const sharedApi = useMemo(() => createSharedVariablesApi(), []);`
- in `TabsList`, after the Domains trigger: `<TabsTrigger value="variables">Variables</TabsTrigger>`
- after the domains `TabsContent`:

```tsx
      <TabsContent value="variables">
        {activeServer ? <VariablesCard serverId={activeServer.id} api={sharedApi} /> : null}
      </TabsContent>
```

- update the component's doc comment to list the Variables tab.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/lib/app-status.test.ts src/pages/settings-variables.test.tsx src/pages/settings.test.tsx && pnpm --dir web run lint && pnpm --dir web run build && git checkout web/dist/.gitkeep`
Expected: PASS; lint clean; `tsc -b` and the Vite build succeed (only restore `web/dist/.gitkeep` if the build removed it).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/app-status.ts web/src/lib/app-status.test.ts web/src/context/apps-context-base.ts web/src/context/apps-context.tsx web/src/pages/settings-variables.tsx web/src/pages/settings-variables.test.tsx web/src/pages/settings.tsx web/src/pages/settings.test.tsx
git commit -m "feat(web): server Variables settings tab and stale apps in the apps context

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 15: Org variables page and navigation

**Files:**
- Create: `web/src/pages/org-variables.tsx`, `web/src/pages/org-variables.test.tsx`
- Modify: `web/src/main.tsx` (route `/org/variables`)
- Modify: `web/src/components/nav-user.tsx` (menu item next to Members)

**Interfaces:**
- Consumes: Task 12 `createSharedVariablesApi`, `applySyncEvent`, `serverNameMap`, `SyncState`; Task 13 `VariablesTable`, `SharedVariableDialog`; `useAppBreadcrumbs` from `@/layouts/use-app-layout`; `useNotifications().subscribe`.
- Produces: default export `OrgVariablesPage({ api }: { api?: SharedVariablesApi })` (the route renders it without props; tests pass a fake); route `/org/variables`; user-menu item "Org variables".

- [ ] **Step 1: Write the failing test**

`web/src/pages/org-variables.test.tsx`:

```tsx
import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

vi.mock("@/lib/ecies", () => ({
  encryptSecret: vi.fn(async (value: string, key: string) => `ct(${key}):${value}`),
}));
vi.mock("@/layouts/use-app-layout", () => ({ useAppBreadcrumbs: () => {} }));

import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { EncryptionKeys, OrgVariables, SharedVariablesApi } from "@/lib/shared-variables";
import OrgVariablesPage from "@/pages/org-variables";

const T = "2026-10-11T00:00:00Z";
const ORG: OrgVariables = {
  variables: [
    { id: "v1", name: "SMTP_HOST", scope: "org", is_secret: false, value: "mail", description: "relay", updated_at: T, updated_by: "u", missing_on: [] },
    { id: "v2", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: [] },
    { id: "v3", name: "S3_KEY", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: T, updated_by: "u", missing_on: ["s3"] },
  ],
  servers: [
    { server_id: "s1", name: "alpha", has_key: true, revision: 4, agent_revision: 4, state: "synced" },
    { server_id: "s3", name: "gamma", has_key: true, revision: 4, agent_revision: 2, state: "pending" },
    { server_id: "s4", name: "new-box", has_key: false, revision: 4, agent_revision: null, state: "pending" },
  ],
};
const KEYS: EncryptionKeys = {
  servers: [
    { server_id: "s1", name: "alpha", public_key: "K1" },
    { server_id: "s3", name: "gamma", public_key: "K3" },
  ],
  pending_servers: [{ server_id: "s4", name: "new-box" }],
};

function fakeApi() {
  return {
    listOrg: vi.fn().mockResolvedValue(ORG),
    listServer: vi.fn(),
    keys: vi.fn().mockResolvedValue(KEYS),
    saveOrg: vi.fn().mockResolvedValue({}),
    saveServer: vi.fn(),
    deleteOrg: vi.fn().mockResolvedValue(undefined),
    deleteServer: vi.fn(),
    resync: vi.fn().mockResolvedValue(["s1", "s3", "s4"]),
    reload: vi.fn().mockResolvedValue(["s1"]),
  };
}

function renderPage(api: ReturnType<typeof fakeApi>) {
  const hub: { h?: NotificationHandler } = {};
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      hub.h = h;
      return () => {
        hub.h = undefined;
      };
    },
    waitFor: vi.fn(),
  };
  render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <OrgVariablesPage api={api as unknown as SharedVariablesApi} />
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
  return { emit: (n: Notification) => act(() => hub.h?.(n)) };
}

describe("OrgVariablesPage", () => {
  it("shows each secret's coverage and each server's sync state", async () => {
    const { emit } = renderPage(fakeApi());
    expect(await screen.findByText("All servers")).toBeInTheDocument();
    expect(screen.getByText("Missing on gamma")).toBeInTheDocument();
    const servers = screen.getByRole("region", { name: "Servers" });
    expect(within(servers).getByText("alpha").closest("li")).toHaveTextContent("Synced");
    expect(within(servers).getByText("new-box").closest("li")).toHaveTextContent("No key yet");
    emit({ type: "vars.sync_failed", ref: "", payload: { server_id: "s3", error: "boom" } });
    expect(within(servers).getByText("gamma").closest("li")).toHaveTextContent("Sync failed");
  });

  it("provides a missing value for the missing server only", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("Missing on gamma");
    await user.click(screen.getByRole("button", { name: "Provide value" }));
    expect(screen.getByText("Encrypts the value for: gamma")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Value"), "k3y");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(api.saveOrg).toHaveBeenCalledWith({ name: "S3_KEY", is_secret: true, description: "", ciphertexts: { s3: "ct(K3):k3y" } });
  });

  it("confirms where a new secret goes before pushing it", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("All servers");
    await user.click(screen.getByRole("button", { name: "Add variable" }));
    await user.type(screen.getByLabelText("Name"), "API_TOKEN");
    await user.click(screen.getByRole("switch", { name: "Secret" }));
    await user.type(screen.getByLabelText("Value"), "t0k");
    expect(screen.getByText("Will be pushed to 2 servers (1 pending, no key yet)")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save and push" }));
    expect(api.saveOrg).toHaveBeenCalledWith({
      name: "API_TOKEN", is_secret: true, description: "", ciphertexts: { s1: "ct(K1):t0k", s3: "ct(K3):t0k" },
    });
  });

  it("resyncs all servers or one variable, and reloads apps using a variable", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("All servers");
    await user.click(screen.getByRole("button", { name: "Resync all servers" }));
    expect(api.resync).toHaveBeenCalledWith({});
    await user.click(screen.getByRole("button", { name: "Resync SMTP_PASS" }));
    expect(api.resync).toHaveBeenCalledWith({ variable_id: "v2" });
    await user.click(screen.getByRole("button", { name: "Reload apps using SMTP_HOST" }));
    expect(api.reload).toHaveBeenCalledWith({ variable_id: "v1" });
  });

  it("deletes after confirmation", async () => {
    const user = userEvent.setup();
    const api = fakeApi();
    renderPage(api);
    await screen.findByText("All servers");
    await user.click(screen.getByRole("button", { name: "Delete SMTP_HOST" }));
    await user.click(screen.getByRole("button", { name: "Delete" }));
    expect(api.deleteOrg).toHaveBeenCalledWith("v1");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `pnpm --dir web exec vitest run src/pages/org-variables.test.tsx`
Expected: FAIL: cannot resolve `@/pages/org-variables`.

- [ ] **Step 3: Implement**

`web/src/pages/org-variables.tsx`:

```tsx
import { useCallback, useEffect, useMemo, useState } from "react";
import { Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { SharedVariableDialog, type DialogMode } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
import { useNotifications } from "@/context/use-notifications";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import {
  applySyncEvent,
  createSharedVariablesApi,
  serverNameMap,
  type EncryptionKeys,
  type OrgVariables,
  type SaveVariable,
  type SharedVariable,
  type SharedVariablesApi,
  type SyncState,
} from "@/lib/shared-variables";

const NO_KEYS: EncryptionKeys = { servers: [], pending_servers: [] };
const SYNC_LABEL: Record<SyncState, string> = { synced: "Synced", pending: "Sync pending", failed: "Sync failed" };

function errorText(e: unknown): string | undefined {
  return e instanceof Error ? e.message : undefined;
}

// OrgVariablesPage manages org-level shared variables: available to apps
// on every server of the organization. Secrets show their coverage ("All
// servers" / "Missing on …"); a new value is encrypted for every keyed
// server, "Provide value" only for the servers missing it.
export default function OrgVariablesPage({ api: injected }: { api?: SharedVariablesApi }) {
  const breadcrumbs = useMemo(() => [{ label: "Organization" }, { label: "Variables" }], []);
  useAppBreadcrumbs(breadcrumbs);
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const { subscribe } = useNotifications();
  const [data, setData] = useState<OrgVariables | null>(null);
  const [keys, setKeys] = useState<EncryptionKeys>(NO_KEYS);
  const [sync, setSync] = useState<Record<string, SyncState>>({});
  const [dialog, setDialog] = useState<DialogMode | null>(null);
  const [dialogKey, setDialogKey] = useState(0);
  const [toDelete, setToDelete] = useState<SharedVariable | null>(null);

  const load = useCallback(async () => {
    try {
      const [vars, k] = await Promise.all([api.listOrg(), api.keys()]);
      setData(vars);
      setKeys(k);
      setSync(Object.fromEntries(vars.servers.map((s) => [s.server_id, s.state])));
    } catch (e) {
      toast.error("Failed to load org variables", { description: errorText(e) });
    }
  }, [api]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => subscribe((n) => setSync((s) => applySyncEvent(s, n))), [subscribe]);

  const names = useMemo(() => serverNameMap(keys), [keys]);
  const open = (mode: DialogMode) => {
    setDialogKey((k) => k + 1);
    setDialog(mode);
  };
  const save = async (body: SaveVariable) => {
    await api.saveOrg(body);
    toast.success("Variable saved");
    await load();
  };
  const act = (done: string, fn: () => Promise<unknown>) => {
    fn().then(
      () => toast.success(done),
      (e: unknown) => toast.error("Request failed", { description: errorText(e) }),
    );
  };
  const remove = async (v: SharedVariable) => {
    try {
      await api.deleteOrg(v.id);
      toast.success("Variable deleted");
      await load();
    } catch (e) {
      toast.error("Couldn't delete the variable", { description: errorText(e) });
    }
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
          <div className="min-w-0 space-y-1.5">
            <CardTitle>Org variables</CardTitle>
            <CardDescription>
              Available to apps on every server as <code>{"${NAME}"}</code>. A server variable or an app's own
              variable with the same name wins.
            </CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" variant="outline" onClick={() => act("Resync sent", () => api.resync({}))}>
              <RefreshCw className="size-4" /> Resync all servers
            </Button>
            <Button size="sm" onClick={() => open({ kind: "create" })}>
              <Plus className="size-4" /> Add variable
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {data === null ? (
            <div className="flex h-24 items-center justify-center">
              <Spinner />
            </div>
          ) : (
            <VariablesTable
              title="Variables"
              variables={data.variables}
              serverNames={names}
              showCoverage
              onEdit={(v) => open({ kind: "edit", variable: v })}
              onDelete={setToDelete}
              onProvide={(v) => open({ kind: "provide", variable: v })}
              onResync={(v) => act("Resync sent", () => api.resync({ variable_id: v.id }))}
              onReload={(v) => act("Reload queued", () => api.reload({ variable_id: v.id }))}
            />
          )}
        </CardContent>
      </Card>

      {data && data.servers.length > 0 ? (
        <section aria-label="Servers" className="space-y-2">
          <h3 className="text-sm font-medium">Servers</h3>
          <ul className="divide-y rounded-md border">
            {data.servers.map((s) => {
              const state = sync[s.server_id] ?? s.state;
              return (
                <li key={s.server_id} className="flex items-center justify-between px-3 py-2 text-sm">
                  <span>{s.name}</span>
                  <span className="flex items-center gap-2">
                    {s.has_key ? null : <Badge variant="outline">No key yet</Badge>}
                    <Badge variant={state === "failed" ? "destructive" : "secondary"}>{SYNC_LABEL[state]}</Badge>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      ) : null}

      {dialog ? (
        <SharedVariableDialog
          key={dialogKey}
          open
          mode={dialog}
          keys={keys}
          onOpenChange={(o) => {
            if (!o) setDialog(null);
          }}
          onSave={save}
        />
      ) : null}

      <AlertDialog
        open={toDelete !== null}
        onOpenChange={(o) => {
          if (!o) setToDelete(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {toDelete?.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              It is removed from every server. Apps that use it lose the value on their next deploy or reload.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                const v = toDelete;
                setToDelete(null);
                if (v) void remove(v);
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
```

`web/src/main.tsx`: `import OrgVariablesPage from "@/pages/org-variables"` and add after the `/org/members` route:

```tsx
      {
        path: "/org/variables",
        element: <OrgVariablesPage />,
      },
```

`web/src/components/nav-user.tsx`: import `Braces` from `lucide-react` and add after the Members item:

```tsx
              <DropdownMenuItem onClick={() => navigate("/org/variables")}>
                <Braces />
                Org variables
              </DropdownMenuItem>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `pnpm --dir web exec vitest run src/pages/org-variables.test.tsx src/components/nav-user.test.tsx && pnpm --dir web run lint`
Expected: PASS; lint clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/org-variables.tsx web/src/pages/org-variables.test.tsx web/src/main.tsx web/src/components/nav-user.tsx
git commit -m "feat(web): org variables page with coverage, provide value and resync

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 16: App page stale badge with Reload, editor shared list and hints

**Files:**
- Modify: `web/src/context/apps-context-base.ts`, `web/src/context/apps-context.tsx` (`reloadSharedVars`, clear stale on a successful `vars.reloaded`)
- Create: `web/src/components/stale-vars-badge.tsx`, `web/src/components/stale-vars-badge.test.tsx`
- Create: `web/src/components/shared-variables-hint.tsx`, `web/src/components/shared-variables-hint.test.tsx`
- Modify: `web/src/pages/app-details.tsx` (render the badge under the header)
- Modify: `web/src/components/app-editor.tsx` (shared list, override notes, compose hint)

**Interfaces:**
- Consumes: Task 14 `AppsContextValue.staleVarsByApp`; Task 12 `createSharedVariablesApi`, `sharedNames`, `overrideNote`, `SharedName`, `VarsReloaded`; `POST /api/v1/shared-variables/reload` with `{server_id, app_ids: [appId]}`.
- Produces:
  - `AppsContextValue.reloadSharedVars(appId: string): Promise<void>`
  - `StaleVarsBadge({ appId }: { appId: string })` — renders nothing when the app is fresh
  - `useSharedNames(serverId?: string, api?: SharedVariablesApi): SharedName[]` and `SharedVariablesHint({ shared }: { shared: SharedName[] })` in `shared-variables-hint.tsx`

- [ ] **Step 1: Write the failing tests**

`web/src/components/stale-vars-badge.test.tsx`:

```tsx
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { StaleVarsBadge } from "@/components/stale-vars-badge";
import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";
import {
  NotificationsContext,
  type Notification,
  type NotificationHandler,
  type NotificationsContextValue,
} from "@/context/notifications-context-base";

function renderBadge(stale: Record<string, string[]>, reload = vi.fn().mockResolvedValue(undefined)) {
  const hub: { h?: NotificationHandler } = {};
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      hub.h = h;
      return () => {
        hub.h = undefined;
      };
    },
    waitFor: vi.fn(),
  };
  const apps = { staleVarsByApp: stale, reloadSharedVars: reload } as unknown as AppsContextValue;
  render(
    <NotificationsContext.Provider value={notifications}>
      <AppsContext.Provider value={apps}>
        <StaleVarsBadge appId="a1" />
      </AppsContext.Provider>
    </NotificationsContext.Provider>,
  );
  return { reload, emit: (n: Notification) => act(() => hub.h?.(n)) };
}

describe("StaleVarsBadge", () => {
  it("renders nothing for a fresh app", () => {
    renderBadge({ a2: ["X"] });
    expect(screen.queryByText(/Shared variables changed/)).not.toBeInTheDocument();
  });

  it("names the changed variables and reloads the app", async () => {
    const user = userEvent.setup();
    const { reload, emit } = renderBadge({ a1: ["SMTP_HOST", "SMTP_PASS"] });
    expect(screen.getByText("Shared variables changed — reload to apply")).toBeInTheDocument();
    expect(screen.getByText("SMTP_HOST, SMTP_PASS")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Reload" }));
    expect(reload).toHaveBeenCalledWith("a1");
    expect(screen.getByRole("button", { name: "Reload" })).toBeDisabled(); // waits for vars.reloaded
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "s", app_id: "other", ok: true, names: [] } });
    expect(screen.getByRole("button", { name: "Reload" })).toBeDisabled();
    emit({ type: "vars.reloaded", ref: "", payload: { server_id: "s", app_id: "a1", ok: false, error: "compose up failed", names: ["SMTP_HOST"] } });
    expect(screen.getByRole("button", { name: "Reload" })).toBeEnabled();
  });
});
```

`web/src/components/shared-variables-hint.test.tsx`:

```tsx
import { render, renderHook, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SharedVariablesHint, useSharedNames } from "@/components/shared-variables-hint";
import type { ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";

describe("SharedVariablesHint", () => {
  it("lists names and scopes, collapsed by default, never values", () => {
    render(<SharedVariablesHint shared={[{ name: "SMTP_PASS", scope: "org" }, { name: "WF_APP_DIR", scope: "builtin" }]} />);
    const summary = screen.getByText("Shared variables available to this app (2)");
    expect(summary.closest("details")).not.toHaveAttribute("open");
    expect(screen.getByText("SMTP_PASS")).toBeInTheDocument();
    expect(screen.getByText("org")).toBeInTheDocument();
    expect(screen.getByText("built-in")).toBeInTheDocument();
  });

  it("renders nothing without shared names", () => {
    const { container } = render(<SharedVariablesHint shared={[]} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("useSharedNames", () => {
  it("loads the server's shared names once per server", async () => {
    const listing: ServerVariables = {
      variables: [],
      inherited: [{ id: "o", name: "SMTP_PASS", scope: "org", is_secret: true, value: "<encrypted>", description: "", updated_at: "", updated_by: "", missing_on: [], overridden: false }],
      builtins: [],
      missing_org_secrets: [],
      sync: { server_id: "s", name: "s", has_key: true, revision: 0, agent_revision: null, state: "pending" },
    };
    const api = { listServer: vi.fn().mockResolvedValue(listing) } as unknown as SharedVariablesApi;
    const { result } = renderHook(() => useSharedNames("s1", api));
    await waitFor(() => expect(result.current).toEqual([{ name: "SMTP_PASS", scope: "org" }]));
    expect(api.listServer).toHaveBeenCalledWith("s1");
  });

  it("yields nothing when the listing fails", async () => {
    const api = { listServer: vi.fn().mockRejectedValue(new Error("down")) } as unknown as SharedVariablesApi;
    const { result } = renderHook(() => useSharedNames("s1", api));
    await waitFor(() => expect(api.listServer).toHaveBeenCalled());
    expect(result.current).toEqual([]);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `pnpm --dir web exec vitest run src/components/stale-vars-badge.test.tsx src/components/shared-variables-hint.test.tsx`
Expected: FAIL: cannot resolve `@/components/stale-vars-badge` / `@/components/shared-variables-hint`.

- [ ] **Step 3: Implement**

`web/src/context/apps-context-base.ts`: add to `AppsContextValue` after `staleVarsByApp`:

```ts
  // reloadSharedVars queues a vars.reload for one app (re-render + compose
  // up -d); its outcome arrives as a vars.reloaded notification.
  reloadSharedVars: (appId: string) => Promise<void>;
```

`web/src/context/apps-context.tsx`:

- import `readApiError` from `@/lib/api` and `type VarsReloaded` from `@/lib/shared-variables`;
- add, after `rename`:

```tsx
  const reloadSharedVars = useCallback(
    async (appId: string) => {
      if (!activeServerId) throw new Error("No active server");
      const res = await fetch(`${base}/api/v1/shared-variables/reload`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ server_id: activeServerId, app_ids: [appId] }),
      });
      if (!res.ok) throw await readApiError(res);
    },
    [activeServerId],
  );

  // A successful reload applied the new values: the app is fresh until the
  // next apps_status says otherwise.
  useEffect(() => {
    return subscribe((n) => {
      if (n.type !== "vars.reloaded") return;
      const p = n.payload as VarsReloaded | undefined;
      if (!p?.ok || p.server_id !== activeServerId) return;
      setStaleVarsByApp((prev) => {
        if (!(p.app_id in prev)) return prev;
        const next = { ...prev };
        delete next[p.app_id];
        return next;
      });
    });
  }, [subscribe, activeServerId]);
```

- add `reloadSharedVars` to the `value` object and its dependency list.

`web/src/components/stale-vars-badge.tsx`:

```tsx
import { useEffect, useState } from "react";
import { RotateCw } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { useApps } from "@/context/use-apps";
import { useNotifications } from "@/context/use-notifications";
import type { VarsReloaded } from "@/lib/shared-variables";

// StaleVarsBadge tells that an app's shared variables changed since it was
// last rendered and offers the reload that applies them. The button stays
// busy until the agent reports the outcome (vars.reloaded).
export function StaleVarsBadge({ appId }: { appId: string }) {
  const { staleVarsByApp, reloadSharedVars } = useApps();
  const { subscribe } = useNotifications();
  const [busy, setBusy] = useState(false);
  const names = staleVarsByApp[appId] ?? [];

  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== "vars.reloaded") return;
        const p = n.payload as VarsReloaded | undefined;
        if (p?.app_id !== appId) return;
        setBusy(false);
        if (p.ok) toast.success("Shared variables applied");
        else toast.error("Reload failed", { description: p.error });
      }),
    [subscribe, appId],
  );

  if (names.length === 0) return null;

  const reload = async () => {
    setBusy(true);
    try {
      await reloadSharedVars(appId);
      toast.info("Reload queued");
    } catch (e) {
      setBusy(false);
      toast.error("Couldn't reload the app", { description: e instanceof Error ? e.message : undefined });
    }
  };

  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200"
    >
      <span>Shared variables changed — reload to apply</span>
      <span className="font-mono text-xs">{names.join(", ")}</span>
      <Button size="sm" variant="outline" className="ml-auto" disabled={busy} onClick={() => void reload()}>
        {busy ? <Spinner className="size-4" /> : <RotateCw className="size-4" />}
        Reload
      </Button>
    </div>
  );
}
```

`web/src/components/shared-variables-hint.tsx`:

```tsx
import { useEffect, useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import {
  createSharedVariablesApi,
  sharedNames,
  type SharedName,
  type SharedVariablesApi,
} from "@/lib/shared-variables";

const SCOPE_LABEL: Record<SharedName["scope"], string> = { org: "org", server: "server", builtin: "built-in" };

// useSharedNames loads the shared names apps on serverId can reference
// (names and scopes only); empty while loading or when the listing fails.
export function useSharedNames(serverId?: string, api?: SharedVariablesApi): SharedName[] {
  const client = useMemo(() => api ?? createSharedVariablesApi(), [api]);
  const [loaded, setLoaded] = useState<{ serverId: string; names: SharedName[] } | null>(null);
  useEffect(() => {
    if (!serverId) return;
    let live = true;
    client.listServer(serverId).then(
      (v) => {
        if (live) setLoaded({ serverId, names: sharedNames(v) });
      },
      () => {
        if (live) setLoaded({ serverId, names: [] });
      },
    );
    return () => {
      live = false;
    };
  }, [client, serverId]);
  return loaded && loaded.serverId === serverId ? loaded.names : [];
}

// SharedVariablesHint is the editor's collapsed, read-only list of shared
// variables an app can use as ${NAME} — never a value.
export function SharedVariablesHint({ shared }: { shared: SharedName[] }) {
  if (shared.length === 0) return null;
  return (
    <details className="rounded-md border p-3 text-sm">
      <summary className="cursor-pointer text-muted-foreground">
        Shared variables available to this app ({shared.length})
      </summary>
      <ul className="mt-2 grid gap-1 sm:grid-cols-2">
        {shared.map((s) => (
          <li key={`${s.scope}:${s.name}`} className="flex items-center gap-2">
            <code className="text-xs">{s.name}</code>
            <Badge variant="outline">{SCOPE_LABEL[s.scope]}</Badge>
          </li>
        ))}
      </ul>
    </details>
  );
}
```

`web/src/pages/app-details.tsx`: `import { StaleVarsBadge } from "@/components/stale-vars-badge";` and render `<StaleVarsBadge appId={app.id} />` directly after the closing `</header>`.

`web/src/components/app-editor.tsx`:

- imports: `import { SharedVariablesHint, useSharedNames } from "@/components/shared-variables-hint";` and `import { overrideNote } from "@/lib/shared-variables";`
- in `AppEditor`, after `const { activeServer, loading: serversLoading } = useServers();`: `const shared = useSharedNames(activeServer?.id);`
- the Variables section description becomes:

```tsx
          <>
            Substituted into files as <code>{"${NAME}"}</code>, including org and
            server variables. Secret values are encrypted in your browser.
          </>
```

- inside each variable row, after the name `Input`, show the override note when there is one:

```tsx
                  {overrideNote(v.name, shared) ? (
                    <span className="shrink-0 text-xs text-muted-foreground">{overrideNote(v.name, shared)}</span>
                  ) : null}
```

- after the variables list (still inside the Variables `EditorSection`, after the closing of the `state.config.variables.length === 0 ? … : …` expression): `<SharedVariablesHint shared={shared} />`
- the file editor placeholder becomes `placeholder="file contents (use ${VAR}, including org/server variables)"`.

- [ ] **Step 4: Run the whole web suite, lint and build**

Run: `pnpm --dir web test && pnpm --dir web run lint && pnpm --dir web run build && git checkout web/dist/.gitkeep`
Expected: all Vitest suites PASS (including the existing `settings-*.test.tsx`, which cast partial `AppsContextValue`s); lint clean; `tsc -b` and the Vite build succeed.

- [ ] **Step 5: Commit**

```bash
git add web/src/context web/src/components/stale-vars-badge.tsx web/src/components/stale-vars-badge.test.tsx web/src/components/shared-variables-hint.tsx web/src/components/shared-variables-hint.test.tsx web/src/pages/app-details.tsx web/src/components/app-editor.tsx
git commit -m "feat(web): stale shared variables badge with reload and editor hints

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Final verification (after Task 16)

- [ ] Run the full gates, the way CI does:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-final; mkdir -p $GOTMPDIR
gofmt -l . | grep -v '^web/' ; go vet ./... && make sqlc-check && make test && make web-test
make test-pg            # needs Docker; otherwise note "Postgres in CI only" in the PR
make diffcov DIFFCOV_BASE=main
bash scripts/install_test.sh
```
Expected: `gofmt -l` prints nothing; every command succeeds; diff coverage ≥80% per language. If CI prints `::notice::raise coverage-baseline.json …`, bump the file in the PR.

- [ ] Manual check on a throwaway standalone instance (spec "Manual, before PR"): `make build`, run `bin/standalone` with a temp `AGENT_DATA_DIR` and `DATABASE_URL`, claim it, then:
  1. Create an org secret `SMTP_PASS`; create an app whose compose has `environment: [PASS=${SMTP_PASS}]`; deploy; `docker exec <container> env | grep PASS` shows the value; `.env.shared.secrets` is 0600 and absent from `git -C {AGENT_DATA_DIR}/apps-data/<id> log --stat`.
  2. Rotate `SMTP_PASS` (Replace value): within 30 s the app shows "Shared variables changed — reload to apply"; Reload; the container env shows the new value; the badge clears.
  3. Register a second server (or simulate with a pending registration and a `public_key` capability): `/org/variables` shows "Missing on …" for `SMTP_PASS`; "Provide value" names that server and clears the badge after saving.
  4. Roll the app back to its first revision: `git -C … show HEAD --stat` does not list `.env.shared*` (Review Focus 1).
