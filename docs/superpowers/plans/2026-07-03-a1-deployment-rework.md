# A1 Deployment Rework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `apps_templates/{rev}` + rendered `apps/` double-store with one git-versioned folder per app (`apps-data/{appID}`), name symlinks in `apps/`, compose-native env handling, and `app.revisions`/`app.rollback` — plus the approved ride-along refactors.

**Architecture:** The app folder IS the deployment: `docker compose up` runs in `apps-data/{appID}` directly. Every save is a git commit (go-git, no host git needed); rollback restores an old tree as a NEW commit. Secrets stay ECIES-encrypted at rest in a committed `secrets.json` and are materialized to gitignored paths (`.env.secrets`, encrypted files) only at deploy — an improvement over today, where resolved plaintext was stored in revisions. No custom `${VAR}` rendering: compose interpolates from `--env-file .env --env-file .env.secrets`.

**Tech Stack:** Go 1.25, go-git/v5, docker compose CLI, React 19.

## Global Constraints

- Two topologies stay; agent filesystem remains the source of truth; DB is a reconciled cache.
- **No data migration** — the old `apps_templates`/rendered layout is dev-only and simply stops being read.
- TDD for all behavior; keep overall backend coverage ≥60% incl. generated code.
- Verification per commit: `make build && go build ./cmd/hub ./cmd/agent && go vet ./... && go test ./...`; web: build + lint (pre-existing `sidebar.tsx` finding only).
- Compose project naming stays `wf-{appID}` (running deployments survive the layout switch logically; dev boxes redeploy).

## Target layout (rooted at `AGENT_DATA_DIR`)

```
apps/                       # human-readable view: {slug} -> ../apps-data/{appID}
apps-data/{appID}/
  .git/                     # full history; every save/rollback is a commit
  .gitignore                # ".env.secrets" + one line per encrypted file path
  .winterflow/config.json   # the API-authored app config blob (committed)
  .winterflow/secrets.json  # {"variables":{name:eciesB64},"files":{path:eciesB64}} (committed)
  compose.yml, <files...>   # plain files verbatim (committed)
  .env                      # plain variables KEY=VALUE (committed)
  .env.secrets              # decrypted at deploy (gitignored)
  <encrypted files>         # decrypted at deploy (gitignored)
```

---

### Task 1: go-git wrapper (`gitrepo.go`)

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/gitrepo.go`, `gitrepo_test.go`
- Modify: `go.mod` (`go get github.com/go-git/go-git/v5`)

**Interfaces — Produces:**
```go
type commitInfo struct {
    Hash      string `json:"hash"`      // full hash; short = Hash[:8]
    Subject   string `json:"subject"`
    Timestamp int64  `json:"timestamp"` // unix seconds
}
func gitEnsure(dir string) error                        // init if no .git
func gitCommitAll(dir, subject string) (hash string, err error)  // add -A (honors .gitignore) + commit; returns current HEAD hash unchanged if worktree clean
func gitLog(dir string) ([]commitInfo, error)           // newest first
func gitCount(dir string) (int, error)                  // commits on HEAD
func gitRestore(dir, hash string) error                 // worktree := tree(hash): delete tracked files absent in target, write target files; does NOT commit
```

- [ ] Failing tests: ensure→commit→count=1; second commit with change→count=2; clean-worktree commit returns same HEAD without new commit; `.gitignore`d file not committed; log order + subjects + timestamps; restore(first) brings back deleted/modified files and removes files added later (gitignored files untouched); restore unknown hash errors.
- [ ] Run: `go test ./internal/infra/orchestrator/docker_compose/ -run TestGit -v` → FAIL (undefined).
- [ ] Implement with go-git: `git.PlainInit`, worktree `AddWithOptions(All:true)` + `Status()` guard, `Commit` with fixed author `WinterFlow Agent <agent@winterflow.local>`, `Log`, restore via `object.Commit.Tree()` walk (collect target files → remove tracked-not-in-target via status/HEAD tree diff → write blobs with stored mode).
- [ ] Tests pass → commit `feat(orchestrator): go-git repo primitives for app history`.

### Task 2: slugified name symlinks (`symlink.go`)

**Files:** Create `internal/infra/orchestrator/docker_compose/symlink.go`, `symlink_test.go`.

**Interfaces — Produces:**
```go
func slugify(name string) string // lowercase, [a-z0-9-], collapse '-', trim; "" -> "app"
// ensureAppSymlink points {appsDir}/{slug} at ../apps-data/{appID} (relative),
// removing any other symlink that targets this appID first (rename). On slug
// collision with a DIFFERENT app: slug + "-" + appID[:8].
func ensureAppSymlink(appsDir, dataDirName, appID, name string) (linkName string, err error)
func removeAppSymlink(appsDir, appID string) error       // remove link(s) targeting appID
func healAppSymlinks(appsDir, dataDirName string, apps map[string]string) error // appID->name; drop dangling, add missing
```
`dataDirName` is the sibling dir name (`"apps-data"`), so targets are relative: `../apps-data/{appID}`.

- [ ] Failing tests: slugify table (`"My App!" -> "my-app"`, unicode stripped, empty → `app`); ensure creates relative link readable via `os.Readlink`; rename swaps link; collision (two apps named `web`) → second gets `web-<id8>`; heal removes dangling + recreates missing; removeAppSymlink only removes links for that app.
- [ ] FAIL → implement (`os.Symlink`, `os.Readlink`, `filepath.Join("..", dataDirName, appID)`) → PASS → commit `feat(orchestrator): human-readable app symlinks in apps/`.

### Task 3: env files (`envfile.go`)

**Files:** Create `internal/infra/orchestrator/docker_compose/envfile.go`, `envfile_test.go`.

**Interfaces — Produces:**
```go
func marshalEnv(vars []resolvedItem) []byte      // NAME=VALUE lines, sorted by name; values with \n or quotes → double-quoted with escapes
func parseEnv(raw []byte) map[string]string      // inverse; ignores comments/blank lines
```

- [ ] Failing tests: round-trip simple/empty/multiline/quoted values; comment + blank lines ignored; deterministic sorted output.
- [ ] FAIL → implement → PASS → commit `feat(orchestrator): compose-native .env serialization`.

### Task 4: app store — write/read the new layout (`store.go`)

**Files:** Create `internal/infra/orchestrator/docker_compose/store.go`, `store_test.go`. Modify `pkg/config/config.go` (+ test): add `GetAppsDataDir() = {data}/apps-data`; keep `GetAppsDir()` (now the symlink dir); DELETE `GetAppsTemplatesDir`.

**Interfaces — Produces:**
```go
const configRel  = ".winterflow/config.json"
const secretsRel = ".winterflow/secrets.json"
const envRel     = ".env"
const envSecretsRel = ".env.secrets"
type secretStore struct {
    Variables map[string]string `json:"variables"` // name -> ECIES b64
    Files     map[string]string `json:"files"`     // rel path -> ECIES b64
}
func (r *Repository) appDataDir(appID string) string
// writeAppStore splits the payload and writes the committed state:
// config.json, secrets.json (placeholders resolved by copying the PREVIOUS
// ciphertext), plain files verbatim (removing previously-managed plain files
// no longer listed), .env from plain vars, .gitignore (.env.secrets + secret
// file paths). Returns the built secretStore.
func (r *Repository) writeAppStore(dir string, app command.AppPayload) (secretStore, error)
// materializeSecrets decrypts the store with the agent key: .env.secrets from
// Variables (empty file removed), each Files entry written to its path.
// Undecryptable entries are logged and skipped.
func (r *Repository) materializeSecrets(dir string, s secretStore) error
func (r *Repository) readAppConfig(dir string) (map[string]any, []byte, error) // parsed + raw config.json
```
Placeholder rule: `Encrypted && Content=="<encrypted>"` → previous ciphertext from existing secrets.json (skip+warn if none). Secrets are no longer stored as plaintext anywhere except the gitignored materialized outputs.

- [ ] Failing tests (temp dir, real ECIES key material via `pkg/cert` helpers like `secrets_test.go` does today): write→files on disk verbatim; .env content matches marshalEnv; secrets.json holds ciphertext (NOT plaintext); placeholder preserves prior ciphertext; removed plain file deleted; .gitignore lists `.env.secrets` + encrypted file; materializeSecrets writes decrypted `.env.secrets` and secret files; undecryptable entry skipped; readAppConfig round-trip.
- [ ] FAIL → implement → PASS → commit `feat(orchestrator): git-backed app store layout (config, secrets-at-rest, env)`.

### Task 5: rewrite orchestrator operations on the new layout

**Files:**
- Rewrite: `operations.go` (SaveApp/ListApps/GetAppsStatus + compose helpers now run in `appDataDir`; render/writeRevision/prune deleted), `lifecycle.go` (GetApp/Delete/Rename/Start/Stop/Restart/Update; `redeployLatest` becomes `deploy(dir)` = materializeSecrets + composeUp), `secrets.go` (resolveItems stays for decrypt; `previousValues` deleted), `logs.go` (run dir → appDataDir).
- Delete: `revisions.go` + revision tests; `pkg/template/` (+ its test); prune `repository_test.go`/`secrets_test.go` cases tied to render/revisions and rewrite them for the new flow.
- New methods: `Revisions(ctx, appID) ([]commitInfo, string, error)` (log + current HEAD) and `Rollback(ctx, appID, hash) (string, error)` (restore→commit "rollback to {hash[:8]}"→materialize→symlink heal→composeUp; returns new HEAD).

**SaveApp flow (the reference path):**
```go
func (r *Repository) SaveApp(ctx context.Context, app command.AppPayload) (string, error) {
    // validate id; dir := r.appDataDir(app.AppID); MkdirAll; gitEnsure(dir)
    // s, err := r.writeAppStore(dir, app)
    // hash, err := gitCommitAll(dir, "save "+name)
    // r.materializeSecrets(dir, s)
    // ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", app.AppID, nameFromConfig)
    // composeUp(ctx, app.AppID)  → cwd=dir; env-file flags: if .env.secrets exists
    //   args += ["--env-file",".env","--env-file",".env.secrets"] (both, since an
    //   explicit --env-file disables the automatic .env load)
    // return hash
}
```
`ListApps`: scan `apps-data/*/` dirs, read config.json → model.App{ID=dir, Name/Icon/Color from config, Version=strconv(gitCount)}; heal symlinks from the scan. `GetApp`: rebuild AppPayload from config.json + plain files read verbatim + `.env` parse; encrypted entries → `"<encrypted>"` placeholder content. `DeleteApp`: composeDown → removeAppSymlink → RemoveAll(dir). `RenameApp`: patch `name` in config.json, commit "rename to X", swap symlink. Compose helpers keep `projectName(appID)` but `cmd.Dir = appDataDir`; composeUp gains the env-file args.

- [ ] Write the new package tests FIRST (failing): save creates commit + symlink + no plaintext secrets on committed paths; second save → count 2; ListApps reads new layout + heals symlink; GetApp masks secrets and round-trips plain content; rename swaps symlink + commits; delete removes dir + link; Revisions returns newest-first; Rollback restores an earlier compose.yml as a NEW commit (count grows). Compose-exec paths guarded as today (missing docker → error paths / integration tag).
- [ ] FAIL → implement → `go test ./internal/infra/orchestrator/...` PASS; fix `internal/app/agent` dispatcher compile (SaveApp now returns string — temporary shim in handleSaveApp until Task 7).
- [ ] Update `deploy_integration_test.go` (integration tag) for the new layout.
- [ ] Full suite green → commit `feat(orchestrator): git-per-app deployment store replaces templates+render`.

### Task 6: command surface — new/changed payloads

**Files:** Modify `internal/domain/command/app.go`, `internal/domain/command/command.go`.

```go
// command.go: TypeAppRevisions Type = "app.revisions"; TypeAppRollback Type = "app.rollback"
// app.go:
type SaveAppResponse struct { AppID string `json:"app_id"`; Revision string `json:"revision"` } // short hash
type GetAppRequest  struct { AppID string `json:"app_id"` }                  // revision param dropped
type GetAppResponse struct { App AppPayload `json:"app"` }
type RevisionInfo = commitInfo-shaped: { Hash, Subject string; Timestamp int64 } (json: hash/subject/timestamp)
type GetRevisionsRequest  struct { AppID string `json:"app_id"` }
type GetRevisionsResponse struct { AppID string `json:"app_id"`; Current string `json:"current"`; Revisions []RevisionInfo `json:"revisions"` }
type RollbackAppRequest   struct { AppID string `json:"app_id"`; Hash string `json:"hash"` }
type RollbackAppResponse  struct { AppID string `json:"app_id"`; Revision string `json:"revision"` }
```
- [ ] Compile-driven: update codec_test round-trip for changed shapes; remove `revision` query handling in `handler/app/lifecycle.go` GetApp. Suite green → commit `feat(command): app.revisions + app.rollback; revision fields go git-native`.

### Task 7: agent dispatcher → generic registration map (ride-along)

**Files:** Rewrite `internal/app/agent/dispatcher.go` + `dispatcher_docker.go`; modify `internal/infra/transport/codec/codec.go` (DELETE `NewRequestPayload`; keep DecodePayload/EncodeResponse/EncodeRequest? — EncodeRequest is now unused too: delete, prune codec_test accordingly).

```go
type handlerFunc func(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope
func handle[Req, Resp any](d *Dispatcher, typ command.Type, fn func(context.Context, Req) (Resp, error)) handlerFunc {
    return func(ctx context.Context, agentID string, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
        var in Req
        if err := codec.DecodePayload(req.Payload, &in); err != nil { return d.errResponse(agentID, req, "invalid payload: "+err.Error()) }
        out, err := fn(ctx, in)
        if err != nil { return d.errResponse(agentID, req, err.Error()) }
        return d.ok(agentID, req, out)
    }
}
// NewDispatcher builds map[command.Type]handlerFunc with one registration line
// per command, incl. TypeAppRevisions -> orch.Revisions, TypeAppRollback -> orch.Rollback.
```
- [ ] Existing dispatcher tests keep passing (same observable envelope behavior); add cases for `app.revisions`/`app.rollback` (empty repo → error; after a save → one revision). Suite green → commit `refactor(agent): dispatcher switch becomes a typed registration map`.

### Task 8: API usecase + routes for revisions/rollback

**Files:** Modify `internal/domain/usecase/app/usecase.go` (+ test), `internal/app/web/handler/app/lifecycle.go` (+ handler_test), `internal/app/web/routes.go`.

```go
func (uc *UseCase) GetRevisions(ctx, userID, serverID, appID string) (string, error) // Dispatch TypeAppRevisions
func (uc *UseCase) RollbackApp(ctx, userID, serverID, appID, hash string) (string, error) // Dispatch TypeAppRollback
// routes: GET /api/v1/app/get-revisions (query server_id, app_id) → 202
//         POST /api/v1/app/rollback-app {server_id, app_id, hash} → 202
```
- [ ] TDD via handler tests (fake dispatcher, 202 + request_id + validation). Commit `feat(api): expose app revisions and rollback`.

### Task 9: web — History tab

**Files:** Modify `web/src/context/apps-context.tsx` (+ base types: `getRevisions(appId): Promise<{current: string; revisions: {hash, subject, timestamp}[]}>`, `rollback(appId, hash): Promise<void>` via dispatchAndWait), `web/src/pages/app-details.tsx` (TABS += "history"; `<HistoryTab appId>`).

HistoryTab: fetch on mount; table rows: short hash (mono), subject, `new Date(ts*1000).toLocaleString()`, "Current" badge on head, Rollback button (AlertDialog confirm) → `rollback` → refetch list + toast. Empty state "No history yet."

- [ ] Implement; `pnpm --dir web run build` + lint clean. Commit `feat(web): history tab with git revisions and rollback`.

### Task 10: web-handler `DispatchJSON` + 401 (ride-along)

**Files:** Modify `internal/app/web/util/response.go` (+`Unauthorized(w)` writing 401 JSON), new `internal/app/web/util/dispatch.go` with:
```go
func RequireUser(w http.ResponseWriter, r *http.Request) (string, bool) // 401 on failure
func DecodeBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) // 400 on failure
```
Convert `handler/app/*.go` + `handler/docker/handler.go` to use them (each endpoint keeps its explicit field validation). Auth failures now 401 (was 400).
- [ ] Update handler tests to expect 401 for unauthenticated. Suite green → commit `refactor(web): shared handler helpers; auth failures return 401`.

### Task 11: collapse pass-through layers (ride-along)

**Files:** Move `FindOrCreateUser` from `internal/infra/db/service/user.go` into `DbUserRepository` (repo implements `port.UserService`); DELETE `internal/infra/db/service/` and `internal/domain/usecase/server/` + `internal/domain/usecase/docker/`; `handler/server` calls `port.ServerRepository` directly (GetServers/ClaimServer/PendingRegistrationCode are all on it); `handler/docker` dispatches via `port.CommandDispatcher` directly (one `dispatch(type, payload)` helper in the handler); delete `port.ServerService`; update `bootstrap.Deps`/`wireCore`, `routes.go`, and the affected tests (move db/service tests to repository; docker usecase test folds into handler test).
- [ ] Suite green → commit `refactor: collapse db/service shim and pass-through usecases`.

### Task 12: final verification + docs

- [ ] `go test ./... -coverpkg=./... -coverprofile` → confirm ≥60% incl. generated (top up orchestrator/store tests if the rewrite dropped it).
- [ ] Standalone E2E (scratch env + Playwright, docker required): deploy app → `ls data/apps` shows `{slug}` symlink resolving to `apps-data/{id}`; `git -C data/apps-data/{id} log --oneline` shows commits; edit via Editor tab → new commit; History tab lists both; rollback → old compose restored, containers re-upped, History grows; secrets: add a secret var, confirm `.env.secrets` gitignored + absent from `git show`, plaintext nowhere in `.git`.
- [ ] Update `MIGRATION.md` (A1 done section: layout, commands, deleted layers) and `CLAUDE.md` (orchestration paragraph + "add a feature" recipe now references the dispatcher map; sqlc note unchanged).
- [ ] Commit `docs: record A1 deployment rework`.
