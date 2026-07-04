# A2 Git-Sourced Apps + Image Tags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy apps straight from a git repo URL (SHA-pinned, auto-updating, private repos via encrypted tokens) and browse registry image tags from the editor — the final migration phase.

**Architecture:** A git-sourced app keeps the A1 layout and adds an upstream clone under `source/` (gitignored from winterflow's own history). Every deploy pins the source SHA in a committed `.winterflow/source.lock`, so rollback restores config AND source position. Compose resolution: `source/{compose_path}` → `source/compose.yml|docker-compose.yml` → winterflow-authored root `compose.yml`. An agent-side poller fetches upstream on a per-app interval and redeploys on new commits. `image.tags` implements the registry HTTP v2 tags API with docker-config credentials and the Docker Hub bearer-token flow.

**Tech Stack:** go-git (clone/fetch/checkout + BasicAuth `x-access-token`), registry HTTP v2, httptest fakes, React 19.

## Global Constraints

- Same as A1: two topologies, agent filesystem is truth, TDD, coverage ≥60% incl. generated, per-commit verification, no data migration.
- Secrets rule extends to the repo token: ciphertext at rest (`secrets.json.source_token`), decrypted only for git transport auth — never written to `.env.secrets`, never in git objects.
- Plain `http://` transport only for localhost registries/upstreams (tests); everything else https.

## Wire/store shapes (used by every task)

```go
// command.AppPayload gains:
Source *SourcePayload `json:"source,omitempty"`
type SourcePayload struct {
    RepoURL     string `json:"repo_url"`
    Branch      string `json:"branch"`                 // default "main" if empty? no — required by UI validation
    ComposePath string `json:"compose_path,omitempty"` // relative path inside the repo
    AutoUpdate  bool   `json:"auto_update"`
    PollSeconds int    `json:"poll_seconds,omitempty"` // default 120
    // Token is ECIES ciphertext, the "<encrypted>" placeholder, or empty (no auth).
    Token []byte `json:"token,omitempty"`
}
// command: TypeImageTags = "image.tags"; ImageTagsRequest{Image string}; ImageTagsResponse{Image string; Tags []string}
// secretStore gains: SourceToken string `json:"source_token,omitempty"`
// committed lock: .winterflow/source.lock = {"sha": "..."} (sourceLock struct)
// config blob (API-authored) carries the same source object minus token (for UI redisplay).
```

---

### Task 1: command payloads (`SourcePayload`, `image.tags`)

**Files:** `internal/domain/command/app.go`, `command.go`, `docker.go` (ImageTags near registry types); extend `codec_test.go` round-trip with a Source-carrying payload.
- [ ] Add types above + `TypeImageTags`. Round-trip test: AppPayload with Source survives encode/decode; ImageTagsResponse round-trip. Suite green → commit `feat(command): git source payload + image.tags`.

### Task 2: upstream source management (`source.go`)

**Files:** Create `internal/infra/orchestrator/docker_compose/source.go`, `source_test.go` (upstream = local bare-ish repo created with the existing gitrepo helpers in a temp dir; go-git clones from a filesystem path).

**Interfaces — Produces:**
```go
type sourceSpec struct { RepoURL, Branch, ComposePath string; AutoUpdate bool; PollSeconds int }
func sourceFromConfig(cfg map[string]any) *sourceSpec        // nil when no source configured
const sourceDirRel = "source"
const sourceLockRel = ".winterflow/source.lock"
type sourceLock struct { SHA string `json:"sha"` }
func readSourceLock(dir string) (sourceLock, bool)
// ensureSource clones/fetches the upstream into {dir}/source, resolves the
// branch head (or lock.SHA when pin != "" — used by rollback), checks the
// worktree out at that commit, and writes source.lock. token decrypted by the
// caller; empty = anonymous. Returns the checked-out SHA.
func (r *Repository) ensureSource(ctx context.Context, dir string, spec sourceSpec, token, pin string) (string, error)
func (r *Repository) sourceTokenPlaintext(dir string) string  // decrypt secretStore.SourceToken, "" on absence/failure
```

- [ ] Failing tests: clone from a local upstream (two commits on `main`) → `source/` present at head SHA + lock written; second call with no upstream change → same SHA, no error; upstream gains a commit → ensureSource follows head; `pin` set → checks out exactly that SHA even though head moved; missing branch errors; `source/` and `source.lock` interplay with the app repo: `source/` never committed (gitignore), lock IS committed by the save path (asserted in Task 3).
- [ ] Implement with go-git: `git.PlainClone` (URL can be a local path), `repo.Fetch` + `ResolveRevision("refs/remotes/origin/"+branch)`, worktree `Checkout(&git.CheckoutOptions{Hash, Force: true})`; BasicAuth{"x-access-token", token} only for http(s) URLs when token != "".
- [ ] PASS → commit `feat(orchestrator): upstream source clone, pinning, and lock`.

### Task 3: store + save/deploy integration for source apps

**Files:** `store.go` (secretStore.SourceToken; writeAppStore handles `app.Source`: token placeholder→previous ciphertext, gitignore gains `source/`), `operations.go` (saveWithoutDeploy: after writeAppStore and BEFORE commit → ensureSource(head) so the lock lands in the save commit; composeArgs → compose file resolution via `-f`), `lifecycle.go` (GetApp masks token + returns source config untouched inside the blob; UpdateApp for source apps refreshes source first; rollbackWithoutDeploy: after gitRestore, if lock present → ensureSource(pin=lock.SHA)), `repository_test.go` + `store_test.go` extensions.

**Compose resolution (composeFile):**
```go
// composeFile returns the -f argument for the app, or "" for auto-detect.
// source apps: source/{ComposePath} if set; else source/compose.yml or
// source/docker-compose.yml if present; else root compose.yml (winterflow-
// authored referencing ./source).
func (r *Repository) composeFile(dir string, spec *sourceSpec) string
```
`composeRun`/`composePS` gain `-f <file>` when non-empty and now ALWAYS pass `--env-file .env` (+ `.env.secrets` when present) — with `-f` into `source/`, compose's implicit project-directory moves there, so explicit env files (cwd-relative) keep interpolation on the app's committed values. cwd stays the app dir.

- [ ] Failing tests: save with Source (local upstream) → `source/` populated, lock committed (`git show HEAD:.winterflow/source.lock` via gitLog+readSourceLock after fresh restore), `source/` absent from git objects; token ciphertext in secrets.json + masked in GetApp + placeholder preserved on re-save; composeFile resolution matrix (compose_path / repo-root compose.yml / fallback root); rollback: save@upstream-c1 → upstream advances + save again (lock=c2) → rollback to first commit → `source/` back at c1 (file content check) and lock says c1; composeArgs now always include `--env-file .env`.
- [ ] Implement → PASS; integration test extension (`deploy_integration_test.go`): a source app deploying from a local upstream repo with a real `docker compose up`.
- [ ] Suite + `go test -tags integration` green → commit `feat(orchestrator): git-sourced apps — SHA-pinned deploys, token auth, rollback-aware source`.

### Task 4: registry tags client + command

**Files:** Create `internal/infra/orchestrator/docker_compose/registry_tags.go`, `registry_tags_test.go`; register `TypeImageTags` in the dispatcher map; extend `dispatcher_test.go`.

**Interfaces — Produces:**
```go
func (r *Repository) ImageTags(ctx context.Context, image string) ([]string, error)
// parseImageRef("nginx") -> host="registry-1.docker.io", repo="library/nginx"
// parseImageRef("ghcr.io/x/y:tag") -> host "ghcr.io", repo "x/y" (tag ignored)
// parseImageRef("127.0.0.1:5001/foo") -> host "127.0.0.1:5001", repo "foo", http allowed
func parseImageRef(image string) (host, repo string, err error)
```
Flow: GET `{scheme}://{host}/v2/{repo}/tags/list?n=100` with Basic auth from docker config when present; on 401 + `WWW-Authenticate: Bearer realm=...,service=...,scope=...` → GET token (same Basic auth) → retry with Bearer; follow RFC5988 `Link: <...>; rel="next"` pagination, cap 500 tags; sort descending by version-ish comparison (pkg/version.ParseNumericVersion) with non-numeric tags after, "latest" first.

- [ ] Failing tests (httptest servers): parseImageRef table; anonymous flow against fake registry with two pages; bearer-token flow (401 → token endpoint → retry OK); basic-auth header forwarded from a DOCKER_CONFIG fixture; unknown repo → error surfaced; dispatcher: `image.tags` with a fake-local image errors cleanly, malformed payload correlated error.
- [ ] Implement → PASS → commit `feat(agent): image.tags — registry v2 tag listing with docker credentials`.

### Task 5: API endpoint for image tags

**Files:** `internal/domain/usecase/app/usecase.go` (`GetImageTags(ctx, userID, serverID, image) (string, error)` dispatching `TypeImageTags`), `handler/app/lifecycle.go` (`GetImageTags` GET query `server_id`,`image` → 202), `routes.go` (`GET /api/v1/image/get-tags`), handler test row in the 202 table.
- [ ] TDD via handler table test → commit `feat(api): expose registry image tags`.

### Task 6: source poller (agent, both topologies)

**Files:** Create `internal/app/agent/source_poller.go`, `source_poller_test.go`; `operations.go` gains the orchestrator method it drives; wire in `cmd/agent/main.go` + `bootstrap/standalone.go`.

**Interfaces — Produces:**
```go
// orchestrator: RefreshDueSources checks every source app whose auto_update
// is on and whose poll interval has elapsed since its last check (tracked in
// memory); on a new upstream commit it re-pins, commits, re-materializes and
// redeploys. Returns the app ids that were updated.
func (r *Repository) RefreshDueSources(ctx context.Context) []string
// agent: RunSourcePoller ticks every 30s and calls RefreshDueSources.
func RunSourcePoller(ctx context.Context, orch SourceRefresher, log *logger.Logger)
type SourceRefresher interface { RefreshDueSources(ctx context.Context) []string }
```
- [ ] Failing tests: orchestrator-level — app with auto_update + 0s interval and an advanced upstream → RefreshDueSources returns the id, lock/commit advanced (deploy-free internal variant used, mirroring saveWithoutDeploy pattern: split `refreshSourceWithoutDeploy`); interval not yet elapsed → skipped; auto_update=false → never. Poller-level — fake SourceRefresher counts calls, ctx cancel stops (10ms tick for test via injectable interval).
- [ ] Implement → PASS; wire both binaries → commit `feat(agent): polling auto-redeploy for git-sourced apps`.

### Task 7: web — Source card + payload plumbing

**Files:** `web/src/types/app-config.ts` (AppEditorState.config gains `source?: {repo_url, branch, compose_path, auto_update, poll_seconds, token_set}` + `sourceToken?: string` transient), `web/src/components/app-editor.tsx` (new "Deploy from Git" card: toggle + URL/branch/compose-path/token/auto-update fields; compose file becomes optional when source on), `web/src/lib/app-editor-io.ts` (validate: source→repo_url+branch required, compose.yml required only when no source; buildSavePayload emits `source` with ECIES-encrypted token / `"<encrypted>"` placeholder; stateFromDetail reads config.source + placeholder for token when `token_set`).
- [ ] Implement; build + lint. Commit `feat(web): deploy-from-git source configuration in the editor`.

### Task 8: web — image tag browser

**Files:** `web/src/context/apps-context(.tsx/-base.ts)` (`getImageTags(image): Promise<string[]>` via 202+waitFor), `web/src/components/image-tag-picker.tsx` (dialog listing tags for one image, click → onSelect), `app-editor.tsx` (detect `image:` refs in compose content via regex `/^\s*image:\s*["']?([\w./:@-]+)/gm`, render a chip row under the compose editor with a Tags button each; selection rewrites that image line's tag in the editor state).
- [ ] Implement; build + lint. Commit `feat(web): browse registry tags from the editor`.

### Task 9: E2E + docs + coverage

- [ ] Coverage ≥60% incl. generated (top up if needed).
- [ ] Browser E2E (scratch stack): create a local upstream repo (file path URL) with a compose file; create a git-sourced app via the API from the page; assert `source/` cloned + lock committed + container up; advance the upstream, hit UpdateApp (control update) → new SHA deployed; rollback via History → old SHA restored; image tag browser exercised against a local fake registry is covered by unit tests (skip in browser).
- [ ] Update `MIGRATION.md` (A2 done; command/route surface; **migration complete** statement) + `CLAUDE.md` (source apps paragraph).
- [ ] Commit `docs: record A2 — the v1→v2 migration is complete`.
