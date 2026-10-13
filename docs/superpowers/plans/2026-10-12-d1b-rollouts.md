# D1b: Rollout Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The D1b slice of the templates spec: a database-backed, resumable, one-app-at-a-time rollout queue with two kinds — `template_upgrade` ("Upgrade all to rN" of a template's instances) and `vars_reload` (the reloads a shared-variable change asks for, moved off the bulk `vars.reload`) — with its routes, its SSE event, the agent's waitable single-app `vars.reload`, and the UI's "Upgrade all to rN" and rollout strip.

**Architecture:** A `rollouts` table (migration `0015_rollouts.sql`) behind a sqlc repository whose every worker write names the lease owner. A domain usecase `internal/domain/usecase/rollouts` queues, lists and stops rollouts and runs one worker goroutine per API process (started by `bootstrap.WireCore`, so standalone and the distributed API both run it): a pass on an in-process wake and once a minute, a 2-minute lease claimed in one `UPDATE` and renewed every 30 s, and per step: pick the next app fresh → record it → dispatch and await (10.5 min) → wait for a status report received after the step showing the app running (5 min) → count / skip / fail-and-stop. `template_upgrade` steps call the D1a instance update path (`templates.UseCase.UpdateInstance` at the target revision); `vars_reload` steps send `vars.reload {app_ids:[id]}`, which the agent now answers with that app's outcome (a detached command, queued on its existing reload worker). The status cache keeps each report's receive time and `vars_revision`, so a reload waits until the server reported the pushed change. The shared-variable routes keep their paths and status codes but queue or extend the org's `vars_reload` rollout. The web client reads `get-rollouts` once and follows `rollout` SSE events.

**Tech Stack:** Go 1.27, chi, sqlc over SQLite (`modernc.org/sqlite`) / Postgres (`pgx/v5`), swaggest OpenAPI generator, kin-openapi contract suite; React 19, React Router 7, TypeScript 5.9, Vitest 5 + Testing Library, the vendored shadcn/ui components. No new dependency, no new env var.

**Spec:** docs/superpowers/specs/2026-10-11-templates-design.md (§Data model `rollouts`, §API → Rollouts, §SSE, §Rollouts, §Agent change, §UI rollout strip / Upgrade all, §Errors, §Security and resource invariants, §Testing; D1b = "Delivery" item 2)

**Base:** branch `feat/rollouts` at `22ff374` (origin/main: D1a #20 and D1c #23 merged). Every snippet below is against that base plus the earlier tasks. The code in this plan was written and run in a scratch copy before the plan was written, then this document's own snippets were re-applied task by task to a fresh copy of the base: after every task the build, `go vet` and the task's tests passed (for Tasks 2, 7 and 8 the whole `WF_CONTRACT_GUARD=require go test ./...`), the failing-test steps of Tasks 2 and 5–11 failed as stated, and the result was byte-for-byte the scratch code; the final state also passed the contract suite and `internal/infra/db/...` on Postgres 16, `make sqlc-check`, `make openapi` with no diff, the whole web suite (89 files, 690 tests), `eslint .`, `tsc -b`, `vite build`, and `tools/diffcov` at 88.2% of changed Go lines (SQLite and Postgres profiles merged) and 100% of changed web lines.

## Global Constraints

- **Fail closed:** rollout routes scoped to the caller's org (another org's rollout/template → 404), server-addressed paths through `webutil.RequireServerAccess`; tests assert it. (No rollout route takes a `server_id`: `get-rollouts`, `stop-rollout` and `upgrade-instances` resolve the caller's org with `webutil.CallerOrg`; another org's template, rollout or org id as `scope_key` answers 404, pinned in Task 7/8 contract tests. The worker dispatches only to servers of the rollout's own org: `template_upgrade` picks instances through `ListInstancesOnOrgServers` (a join on `servers.organization_id`), `ReloadScope` servers come from the org's own list. Instance routes additionally require the server to be in the org the call acts for — Task 2.)
- **Bounds:** every network op / wait bounded (command 10 min, health 5 min, lease 2 min); never block the InProcessBridge goroutine. (Constants in `internal/domain/usecase/rollouts/usecase.go`: `commandWait` 10.5 min — 10 min plus 30 s so the agent's own 10-minute bound answers first —, `healthWait` 5 min, `leaseFor` 2 min, `renewEvery` 30 s, `settleWait` 5 min. `vars.reload` becomes a detached command, so a single-app reload's wait runs off the bridge goroutine.)
- **Idle cost:** one indexed query a minute; SSE only on change; never exec per app in polled paths. (The idle worker runs `ListClaimableRollouts` — index `idx_rollouts_claim` — once a minute and on a wake; `rollout` events are published only when a rollout is queued, extended-and-woken, starts a step, counts one, or ends; the worker reads the in-memory status cache, it never execs.)
- **SQL:** per engine in sqlc query files, never in Go; migrations identical numbering in sqlite + pgsql, ASCII; inside `Tx.WithTx` use `Tx.Querier(ctx)` with fn's ctx. (This plan opens no transaction: every rollout write is one statement.) `make sqlc` after any query or migration change; never edit `internal/infra/db/sqlc/gen`; add every new sqlc type pair to `sqlc/shapes_test.go` and write every new TIMESTAMP column in `repository/timestamps_test.go`.
- **Gates:** `make openapi`, `make sqlc-check`, `WF_CONTRACT_GUARD=require go test ./...`, `go vet`, `pnpm --dir web run lint`, `tsc -b`, vitest all green; changed-line coverage ≥80% Go and web (`go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info`).
- **Web:** never delete `web/src/components/ui`; keep `web/dist/.gitkeep` (run `git checkout -- web/dist/.gitkeep` after a `vite build`); UI wording "Add …"; incremental UX, no redesigns. Tests mock `sonner`, render inside `NotificationsContext.Provider` and `MemoryRouter`, answer fetches with `fakeAPI` (`@/test/fake-api`) or an injected fake client.
- **Commits:** Conventional Commits; every commit message ends with exactly:
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`
- **Never bare `git stash`.**
- Run `gofmt -w` on every Go file you touch before committing; gofmt is the authority over the alignment shown here. Go commands use a private temp dir: `export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR` before the first `go` command of a task.
- Layering: `internal/domain/**` never imports `internal/infra/**` outside `_test.go` files.
- No new env var (`.env.example` unchanged): every bound is a constant.

## Review Focus

1. **An API restart (or a second replica) in the middle of a step.** Expected: the lease expires after 2 minutes, a pass claims the rollout again, the step is redone from a fresh pick (an instance already on the target revision is not picked; an app no longer stale is not picked); a process that lost its lease abandons its step and its writes change nothing. Pinned in Task 1 (`TestClaimLeaseAndTakeover`) and Task 5 (`TestAnotherProcessTakesOverAnExpiredLease`).
2. **A variable saved with reload while the agents don't have the change yet.** The rollout is queued in the same request that pushes `vars.sync`, so the status cache still shows the old staleness. Expected: the worker waits (5 s polls, up to `settle_by`) until each scoped server's report carries the revision the save produced, then reloads; it never ends "done" before the change landed. Pinned in Task 8 (`TestReloadWaitsForTheServerToReportTheChange`).
3. **A reload request landing while the worker finds nothing left.** Expected: the extension bumps `scope_rev`, the worker's `Complete` at the old revision fails and it looks again, so the new names are reloaded in the same rollout. Pinned in Task 1 (`TestStepsExtendAndFinish`) and Task 8 (`TestAnExtensionLandingAtTheEndIsNotLost`).
4. **An app the step can't change.** An app stopped by the time the agent got to it (skipped), an app still stale after a successful reload, an instance the revision needs new values for. Expected: never an endless loop on the same app — skips are recorded in `skip_ids` and never picked again; still-stale at the same snapshot fails the rollout naming the app. Pinned in Task 6 (`TestUpgradeAllMovesEveryInstanceAndSkipsWhatNeedsValues`) and Task 8 (`TestReloadSkipsAStoppedAppAndStopsAtAFailure`, `TestReloadThatLeavesTheAppStaleFails`).
5. **The single-app `vars.reload` on the agent.** Expected: answered with that app's outcome (an error naming the change when it failed, `skipped` when not running), the wait registered during a reload in flight hears the *next* reload (it may need the newer snapshot), a deleted app or a stopping agent ends the wait, and the standalone bridge goroutine is never held. Pinned in Task 4 (`TestReloadWorkerWaitDuringAReloadTakesTheNextOne`, `TestReloadWorkerEndsWaitsOnForgetFailureAndStop`, `TestDispatchVarsReloadOneAppAnswersItsOutcome`, `TestDetached`).
6. **A caller who is a member of two orgs, and an instance row on another org's server.** Expected: instance routes answer 403 for a server outside the org the call acts for even though the general ownership check passes; an `Upgrade all` never picks an instance whose server is not the rollout's org's. Pinned in Task 2 (`TestInstanceRoutesStayInTheCallersOrg`, `TestInstanceRoutesRequireTheCallersOrgServer`, `TestListInstancesOnOrgServers`).

## Rulings

Each: what — why — cost if wrong.

- **Ruling (controller, security review): defense in depth against an instance on another org's server — (1) the instance routes' `serverCaller` also requires the server to belong to the org the call acts for (`ServerInOrg`, 403, a lookup error denies), (2) `template_upgrade`'s next-app list joins `servers` and keeps only instances whose server's `organization_id` is the rollout's org** — the routes resolve the org with `CallerOrg` (the caller's primary org) but authorize with `RequireOwnedServer` (any org the caller is a member of); not exploitable today (every membership insert creates a new user, so one org per user), but the rollout worker dispatches to every instance of an org's template — a member of two orgs can no longer create instances on the second org's servers through the API (they would act for their primary org anyway).
- **Ruling: the migration is `0015_rollouts.sql`** — `0014_templates.sql` is the newest on `main` — rename both files and `make sqlc` if something else lands first.
- **Ruling: a Docker healthcheck's `unhealthy` is not seen; the health wait passes on Active, and on Idle only at the 5-minute bound** — `mapContainerState` reads `docker ps`'s State, and a running container whose healthcheck fails is State `running` → Active (the Status text "(unhealthy)" is not parsed); an app with a finished one-shot container (a migration job, exit 0) aggregates to Idle and never reads Active, so failing it would make such apps un-upgradable. Restarting, Problematic, Stopped, no containers or no report after the step fail at the bound. Changing the agent's mapping would change app status and alerts everywhere, out of scope — an unhealthy app passes its step, and an app with a one-shot container takes the full 5 minutes per step.
- **Ruling: `vars_reload`'s scope is `model.ReloadScope` `{names, server_ids, all_stale, app_ids, revisions, settle_by}`** — the spec's `{names, server_ids}` cannot express the reload route's other forms (`{server_id}` = every stale app of the server, `{server_id, app_ids}` = the app page's Reload of one stale app); and a reload is queued in the same request that pushes `vars.sync`, so the worker must know which shared-variables revision each server's report has to reach before its `stale_vars` mean anything (`revisions`, read from `servers.vars_revision` after the save) and how long to wait for a server that never reports it (`settle_by`, 5 minutes, then the server is left out and the rollout can end `done`). Names × servers merge as a cross product, as the spec's extension implies — a stale app on a scoped server is reloaded for a name another server's request brought; it is stale anyway — an offline server's stale apps are not reloaded (as today, where a `vars.reload` to an unconnected agent was dropped).
- **Ruling: the extension is computed in Go and written by one conditional `UPDATE … WHERE scope_rev = ?` (retried up to 5 times on a lost race), and `done` is likewise conditional on the `scope_rev` the worker evaluated** — a JSON set-union in SQL would be two different dialects (`json_each` vs `jsonb`) to keep identical; the version column gives the same atomicity and also closes the "extended while the worker decided it was done" race the spec does not mention — under heavy concurrent saves a reload request could give up after 5 lost races (logged for save/delete, a 400 for the reload route).
- **Ruling: an extension joins the active rollout's fate** — the spec extends the active rollout; one that is about to fail, or has `stop_requested`, still takes it — the extended names are not reloaded when that rollout then fails or stops; the strip shows the failure and Reload starts a new rollout.
- **Ruling: extra columns `scope_rev`, `total`, `skip_ids`, `current_server_id`; `current_app_id` and `lease_owner` are `''` rather than NULL** — `total` feeds "3 of 12", `current_server_id` "shop on srv-2" and the failure link, `skip_ids` keeps a skipped instance/app from being picked again (the next app is chosen fresh each step and would otherwise loop on it) — a skipped app stays skipped for the rest of that rollout even if fixed meanwhile.
- **Ruling: `template_upgrade` picks only `created` instances below the target revision, oldest first; it skips an instance whose render fails values validation (`templates.ErrNeedsValues`: a newly required parameter without a default, or a stored value the new pattern refuses) or that went away (not found, retired); any other error (a host another app took, a missing storage, the agent's failure) fails the rollout** — the spec: "skipped (counted) if the revision requires a value it lacks", the first failure stops — a pattern change also skips (the instance needs a new value either way).
- **Ruling: `vars.reload` is `Detached()` as a type** — the bridge and the gRPC agent decide before decoding the payload; the bulk form answers at once anyway — one extra goroutine per bulk reload.
- **Ruling: the agent's single-app bound is 10 minutes from enqueue (queue wait plus the reload), the API awaits 10.5 minutes** — the spec: the reply is bounded by the worker's 10-minute per-app deadline; the slack lets the agent's timeout answer arrive first — a reload queued behind another long one times out and fails the step.
- **Ruling: an app still stale for the scope after a successful reload, at the same server revision, fails the step** — the next app is chosen from live staleness, so it would be picked forever; a newer revision (another change arrived) is not a failure, the app is picked again — a stale app that cannot be made fresh (an undecryptable secret) stops the rollout naming it.
- **Ruling: a rollout's dispatches carry its creator as the user** — `port.DispatchInput.UserID` routes the `operation_result` (harmless: no browser waits for that ref); `rollout` events go to every org member.
- **Ruling: `get-rollouts` takes `kind` (required) and `scope_key` (optional: every scope of the org's, active first, at most 100); a `template_upgrade` scope must be the caller's org's template (404), a `vars_reload` scope the caller's org id (404); `scope_key` must be a UUID (400, so Postgres answers like SQLite)** — the spec lists `kind, scope_key?` — none.
- **Ruling: `stop-rollout` on an ended rollout answers 200 with it unchanged; a queued rollout stops when the worker claims it** — idempotent like delete-instance — a queued rollout shows "Stop" disabled for up to one pass.
- **Ruling: `upgrade-instances` takes `revision` 0 as the latest; `revision` < 0 is 400** — matches `create-instance` — none.
- **Ruling: the `rollout` SSE payload is `model.RolloutView`, the same type `get-rollouts` returns** — the spec's fields plus `revision`, `stop_requested`, `current_server_id`, `current_app_name`, `created_at`, `created_by`, `finished_at`, so the strip renders from either — larger events.
- **Ruling: the shared-variable routes answer as before (`reload` answers `{servers}` — now the servers in the rollout's scope — with the message "reload queued"); `reload: true` on a save that changed no server (a description edit) queues nothing; a queue error after a committed save is logged, not returned** — the spec keeps paths and status codes; the change is stored either way — a failed queue is silent to the user (the variable shows stale apps, Reload is there).
- **Ruling: the strip shows an active rollout (with Stop) or the failure that ended the latest one (with the app's link and Dismiss, session-local); done and stopped rollouts show nothing** — "A failure names the app and links to it"; a finished rollout has nothing to act on — an old failure reappears on each visit until a newer rollout or Dismiss.
- **Ruling: the template page re-reads its instances on every step of its rollout** — the instances' revisions change underneath it — one `get-instances` per step while the page is open.

## File map

| File | Task | Change |
|---|---|---|
| `internal/infra/db/migrations/{sqlite,pgsql}/0015_rollouts.sql` | 1 | the table and its indexes |
| `internal/infra/db/sqlc/query/{sqlite,pgsql}/rollouts.sql`, `sqlc/gen/**` (generated), `sqlc/shapes_test.go` | 1, 2 | queries |
| `internal/infra/db/sqlc/query/{sqlite,pgsql}/{servers,templates}.sql`, `internal/domain/port/{server,template}.go`, `internal/infra/db/repository/{server,template}/*`, `internal/app/web/handler/templates/{respond_test,handler_test}.go`, `internal/app/web/contract/instances_test.go` | 2 | `ServerInOrg`, `ListInstancesOnOrgServers` |
| `internal/domain/model/rollout.go`, `rollout_test.go`, `internal/domain/port/rollout.go` | 1 | types, `ReloadScope`, `RolloutView`, port |
| `internal/infra/db/repository/rollout/{repo,sqlite,pgsql,repo_test}.go`, `repository/timestamps_test.go` | 1 | repository |
| `internal/infra/bootstrap/{deps,core}.go` | 1, 7 | the repository, then the queue and its worker |
| `internal/domain/service/status/{cache,cache_test}.go`, `internal/infra/bootstrap/{events,events_test}.go` | 3 | report time and revision |
| `internal/domain/command/{command,vars,backup_test}.go`, `internal/app/agent/{reload_worker,dispatcher}.go` + tests | 4 | waitable single-app reload |
| `internal/domain/model/notification.go` | 5 | `rollout` event type |
| `internal/domain/usecase/rollouts/{usecase,worker,wait}.go`, `{fixture,worker}_test.go` | 5, 6, 8 | the queue and the worker; later `usecase.go`'s kinds map |
| `internal/domain/usecase/templates/{update,update_test}.go` | 6 | `ErrNeedsValues` |
| `internal/domain/usecase/rollouts/{upgrade,agent_test,upgrade_test}.go` | 6 | `template_upgrade` |
| `internal/infra/bootstrap/rollouts.go` | 7 | builds the queue |
| `internal/app/web/handler/templates/handler.go` | 2, 7 | the org check, then the queue |
| `internal/app/web/handler/templates/{routes,templates}.go` | 7 | `upgrade-instances` |
| `internal/app/web/handler/rollouts/{handler,handler_test}.go` | 7 | `get-rollouts`, `stop-rollout` |
| `internal/app/web/routes.go` | 7, 8 | wiring |
| `internal/app/web/handler/notification/{routes,routes_test}.go` | 7 | `rollout` EventPayload |
| `internal/app/web/contract/{rollouts,matrix,parity}_test.go`, `testdata/parity.golden.json` | 7, 8 | contract tests |
| `api/openapi.yaml`, `internal/app/web/testdata/routes.golden` | 7, 8 | regenerated |
| `internal/domain/usecase/rollouts/{reload,reload_test}.go` | 8 | `vars_reload` |
| `internal/domain/usecase/sharedvars/{usecase,sync,usecase_test}.go`, `internal/infra/bootstrap/varssync.go`, `internal/app/web/handler/sharedvars/{handler,routes}.go`, `internal/app/web/contract/shared_variables_test.go` | 8 | reloads on the queue |
| `web/src/lib/rollouts.ts`, `web/src/test/rollouts.ts`, `web/src/hooks/use-rollout.ts` + tests | 9 | client, fixture, hook |
| `web/src/components/rollout-strip.tsx` + test | 10 | the strip |
| `web/src/components/template-instances.tsx`, `web/src/pages/{template-page,org-variables,settings-variables}.tsx` + tests | 11 | Upgrade all, the strip on three pages |
| `CLAUDE.md` | 12 | docs |

Tasks 1, 3 and 4 are independent of each other; 2 follows 1 (both edit `shapes_test.go`); 5 needs 1 and 3; 6 needs 2 and 5; 7 needs 6; 8 needs 4 and 7; 9–11 need the API of 7–8 (they run against fakes); 12 is last. No file is edited by two tasks except where the table says so (`sqlc/shapes_test.go` 1→2, `templates/handler.go` 2→7, `bootstrap/{deps,core}.go` 1→7, `rollouts/usecase.go` 5→6→8, `web/routes.go` 7→8, `contract/rollouts_test.go` 7→8, `api/openapi.yaml` 7→8).

---
### Task 1: The `rollouts` table, its queries and the repository

**Files:**
- Create: `internal/infra/db/migrations/sqlite/0015_rollouts.sql`, `internal/infra/db/migrations/pgsql/0015_rollouts.sql`
- Create: `internal/infra/db/sqlc/query/sqlite/rollouts.sql`, `internal/infra/db/sqlc/query/pgsql/rollouts.sql`
- Generated (do not edit): `internal/infra/db/sqlc/gen/{sqlite,pgsql}/{models.go,querier.go,rollouts.sql.go}`
- Create: `internal/domain/model/rollout.go`, `internal/domain/model/rollout_test.go`, `internal/domain/port/rollout.go`
- Create: `internal/infra/db/repository/rollout/{repo.go,sqlite.go,pgsql.go,repo_test.go}`
- Modify: `internal/infra/db/sqlc/shapes_test.go`, `internal/infra/db/repository/timestamps_test.go`, `internal/infra/bootstrap/deps.go`, `internal/infra/bootstrap/core.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `model.RolloutTemplateUpgrade = "template_upgrade"`, `model.RolloutVarsReload = "vars_reload"`; `model.RolloutState` (`RolloutQueued`, `RolloutRunning`, `RolloutDone`, `RolloutStopped`, `RolloutFailed`) with `Active() bool`; `model.ErrRolloutNotFound` (NotFound class), `model.ErrRolloutActive` (Conflict class).
  - `model.Rollout{ID, OrgID, Kind string; Scope []byte; ScopeKey string; ScopeRev int64; State RolloutState; StopRequested bool; CurrentAppID, CurrentServerID string; Done, Skipped, Total int64; SkipIDs []string; Error, LeaseOwner string; LeaseUntil *time.Time; CreatedAt time.Time; CreatedBy string; FinishedAt *time.Time}`.
  - `model.UpgradeScope{TemplateID string; Revision int64}`; `model.ReloadScope{Names, ServerIDs, AllStale, AppIDs []string; Revisions map[string]int64; SettleBy time.Time}` with `Merge(o) ReloadScope`, `Servers() []string`, `Wants(serverID, appID string, stale []string) bool`.
  - `model.RolloutView` (JSON: `id, kind, scope_key, revision, state, stop_requested, done, skipped, total, current_app_id, current_server_id, current_app_name, error, created_at, created_by, finished_at`).
  - `port.RolloutRepository` (methods below); `rolloutrepo.New(tx *backend.TxManager, driver string, log *logger.Logger) (*rolloutrepo.Repo, error)` (package `winterflow/internal/infra/db/repository/rollout`); `bootstrap.Deps.RolloutRepository`.

- [ ] **Step 1: Write the migration (both engines; only the id columns differ)**

**Create** `internal/infra/db/migrations/sqlite/0015_rollouts.sql`:

```sql
-- Rollouts: queued, resumable, one-app-at-a-time jobs (upgrade a template's
-- instances, reload the apps a shared-variable change made stale). One API
-- process drives a rollout at a time through a lease; the partial unique
-- index allows one active rollout per (kind, scope_key).

CREATE TABLE rollouts (
    id VARCHAR(36) PRIMARY KEY,
    org_id VARCHAR(36) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    scope TEXT NOT NULL,
    scope_key VARCHAR(36) NOT NULL,
    scope_rev INTEGER NOT NULL DEFAULT 1,
    state VARCHAR(16) NOT NULL,
    stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
    current_app_id VARCHAR(36) NOT NULL DEFAULT '',
    current_server_id VARCHAR(36) NOT NULL DEFAULT '',
    done INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    total INTEGER NOT NULL DEFAULT 0,
    skip_ids TEXT NOT NULL DEFAULT '[]',
    error TEXT NOT NULL DEFAULT '',
    lease_owner VARCHAR(36) NOT NULL DEFAULT '',
    lease_until TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    finished_at TIMESTAMP,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

-- One active rollout per scope: a template's upgrade, an org's reloads.
CREATE UNIQUE INDEX idx_rollouts_active ON rollouts (kind, scope_key) WHERE state IN ('queued', 'running');
-- The worker's once-a-minute look for claimable work.
CREATE INDEX idx_rollouts_claim ON rollouts (state, lease_until);
CREATE INDEX idx_rollouts_scope ON rollouts (org_id, kind, scope_key, created_at);
```

**Create** `internal/infra/db/migrations/pgsql/0015_rollouts.sql`:

```sql
-- Rollouts: queued, resumable, one-app-at-a-time jobs (upgrade a template's
-- instances, reload the apps a shared-variable change made stale). One API
-- process drives a rollout at a time through a lease; the partial unique
-- index allows one active rollout per (kind, scope_key).

CREATE TABLE rollouts (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    kind VARCHAR(32) NOT NULL,
    scope TEXT NOT NULL,
    scope_key VARCHAR(36) NOT NULL,
    scope_rev INTEGER NOT NULL DEFAULT 1,
    state VARCHAR(16) NOT NULL,
    stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
    current_app_id VARCHAR(36) NOT NULL DEFAULT '',
    current_server_id VARCHAR(36) NOT NULL DEFAULT '',
    done INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    total INTEGER NOT NULL DEFAULT 0,
    skip_ids TEXT NOT NULL DEFAULT '[]',
    error TEXT NOT NULL DEFAULT '',
    lease_owner VARCHAR(36) NOT NULL DEFAULT '',
    lease_until TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    finished_at TIMESTAMP,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

-- One active rollout per scope: a template's upgrade, an org's reloads.
CREATE UNIQUE INDEX idx_rollouts_active ON rollouts (kind, scope_key) WHERE state IN ('queued', 'running');
-- The worker's once-a-minute look for claimable work.
CREATE INDEX idx_rollouts_claim ON rollouts (state, lease_until);
CREATE INDEX idx_rollouts_scope ON rollouts (org_id, kind, scope_key, created_at);
```
- [ ] **Step 2: Write the queries (both engines; `?` vs `$n`, `::bigint` on the LIMIT)**

`ClaimRollout` is the spec's single-statement claim; `CompleteRollout` ends a rollout only at the `scope_rev` the worker evaluated; `PruneRollouts` qualifies its columns (sqlc refuses the ambiguous `kind` otherwise).

**Create** `internal/infra/db/sqlc/query/sqlite/rollouts.sql`:

```sql
-- The rollout queue. Every worker write names its lease_owner, so a worker
-- that lost its lease changes nothing.

-- name: InsertRollout :execrows
-- An active rollout of the same (kind, scope_key) inserts nothing (0 rows).
INSERT INTO rollouts (id, org_id, kind, scope, scope_key, state, created_at, created_by)
VALUES (?, ?, ?, ?, ?, 'queued', ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetRollout :one
SELECT * FROM rollouts WHERE id = ?;

-- name: GetActiveRollout :one
SELECT * FROM rollouts WHERE kind = ? AND scope_key = ? AND state IN ('queued', 'running');

-- name: ListRolloutsForScope :many
-- The active rollout first, then the ended ones newest first.
SELECT * FROM rollouts
WHERE org_id = ? AND kind = ? AND scope_key = ?
ORDER BY CASE WHEN state IN ('queued', 'running') THEN 0 ELSE 1 END, created_at DESC, id DESC
LIMIT 21;

-- name: ListRolloutsOfKind :many
SELECT * FROM rollouts
WHERE org_id = ? AND kind = ?
ORDER BY CASE WHEN state IN ('queued', 'running') THEN 0 ELSE 1 END, created_at DESC, id DESC
LIMIT 100;

-- name: ExtendRollout :execrows
-- Optimistic: 0 rows when the scope moved on (another extension) or the
-- rollout ended.
UPDATE rollouts SET scope = ?, scope_rev = scope_rev + 1
WHERE id = ? AND scope_rev = ? AND state IN ('queued', 'running');

-- name: RequestRolloutStop :execrows
UPDATE rollouts SET stop_requested = TRUE
WHERE id = ? AND org_id = ? AND state IN ('queued', 'running');

-- name: ListClaimableRollouts :many
-- The worker's idle query: active rollouts nobody holds a live lease on.
SELECT id FROM rollouts
WHERE state IN ('queued', 'running') AND (lease_until IS NULL OR lease_until < ?)
ORDER BY created_at ASC, id ASC
LIMIT 10;

-- name: ClaimRollout :execrows
UPDATE rollouts SET lease_owner = ?, lease_until = ?, state = 'running'
WHERE id = ? AND state IN ('queued', 'running') AND (lease_until IS NULL OR lease_until < ?);

-- name: RenewRolloutLease :execrows
UPDATE rollouts SET lease_until = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: StartRolloutStep :execrows
UPDATE rollouts SET current_app_id = ?, current_server_id = ?, total = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: CountRolloutStep :execrows
UPDATE rollouts SET done = done + ?, skipped = skipped + ?, skip_ids = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: FinishRollout :execrows
-- Stopped or failed. A failure keeps the current app: it names what failed.
UPDATE rollouts
SET state = ?, error = ?, current_app_id = ?, current_server_id = ?, lease_owner = '', lease_until = NULL, finished_at = ?
WHERE id = ? AND lease_owner = ? AND state = 'running';

-- name: CompleteRollout :execrows
-- Done, unless the scope was extended since the worker read it (0 rows: it
-- looks again).
UPDATE rollouts
SET state = 'done', current_app_id = '', current_server_id = '', lease_owner = '', lease_until = NULL, finished_at = ?
WHERE id = ? AND lease_owner = ? AND state = 'running' AND scope_rev = ?;

-- name: PruneRollouts :exec
-- Keeps the newest ended rollouts of a scope.
DELETE FROM rollouts
WHERE rollouts.kind = sqlc.arg(kind) AND rollouts.scope_key = sqlc.arg(scope_key)
  AND rollouts.state IN ('done', 'stopped', 'failed')
  AND rollouts.id NOT IN (
    SELECT r.id FROM rollouts AS r
    WHERE r.kind = sqlc.arg(kind) AND r.scope_key = sqlc.arg(scope_key) AND r.state IN ('done', 'stopped', 'failed')
    ORDER BY r.finished_at DESC, r.id DESC
    LIMIT sqlc.arg(keep));
```

**Create** `internal/infra/db/sqlc/query/pgsql/rollouts.sql`:

```sql
-- The rollout queue. Every worker write names its lease_owner, so a worker
-- that lost its lease changes nothing.

-- name: InsertRollout :execrows
-- An active rollout of the same (kind, scope_key) inserts nothing (0 rows).
INSERT INTO rollouts (id, org_id, kind, scope, scope_key, state, created_at, created_by)
VALUES ($1, $2, $3, $4, $5, 'queued', $6, $7)
ON CONFLICT DO NOTHING;

-- name: GetRollout :one
SELECT * FROM rollouts WHERE id = $1;

-- name: GetActiveRollout :one
SELECT * FROM rollouts WHERE kind = $1 AND scope_key = $2 AND state IN ('queued', 'running');

-- name: ListRolloutsForScope :many
-- The active rollout first, then the ended ones newest first.
SELECT * FROM rollouts
WHERE org_id = $1 AND kind = $2 AND scope_key = $3
ORDER BY CASE WHEN state IN ('queued', 'running') THEN 0 ELSE 1 END, created_at DESC, id DESC
LIMIT 21;

-- name: ListRolloutsOfKind :many
SELECT * FROM rollouts
WHERE org_id = $1 AND kind = $2
ORDER BY CASE WHEN state IN ('queued', 'running') THEN 0 ELSE 1 END, created_at DESC, id DESC
LIMIT 100;

-- name: ExtendRollout :execrows
-- Optimistic: 0 rows when the scope moved on (another extension) or the
-- rollout ended.
UPDATE rollouts SET scope = $1, scope_rev = scope_rev + 1
WHERE id = $2 AND scope_rev = $3 AND state IN ('queued', 'running');

-- name: RequestRolloutStop :execrows
UPDATE rollouts SET stop_requested = TRUE
WHERE id = $1 AND org_id = $2 AND state IN ('queued', 'running');

-- name: ListClaimableRollouts :many
-- The worker's idle query: active rollouts nobody holds a live lease on.
SELECT id FROM rollouts
WHERE state IN ('queued', 'running') AND (lease_until IS NULL OR lease_until < $1)
ORDER BY created_at ASC, id ASC
LIMIT 10;

-- name: ClaimRollout :execrows
UPDATE rollouts SET lease_owner = $1, lease_until = $2, state = 'running'
WHERE id = $3 AND state IN ('queued', 'running') AND (lease_until IS NULL OR lease_until < $4);

-- name: RenewRolloutLease :execrows
UPDATE rollouts SET lease_until = $1
WHERE id = $2 AND lease_owner = $3 AND state = 'running';

-- name: StartRolloutStep :execrows
UPDATE rollouts SET current_app_id = $1, current_server_id = $2, total = $3
WHERE id = $4 AND lease_owner = $5 AND state = 'running';

-- name: CountRolloutStep :execrows
UPDATE rollouts SET done = done + $1, skipped = skipped + $2, skip_ids = $3
WHERE id = $4 AND lease_owner = $5 AND state = 'running';

-- name: FinishRollout :execrows
-- Stopped or failed. A failure keeps the current app: it names what failed.
UPDATE rollouts
SET state = $1, error = $2, current_app_id = $3, current_server_id = $4, lease_owner = '', lease_until = NULL, finished_at = $5
WHERE id = $6 AND lease_owner = $7 AND state = 'running';

-- name: CompleteRollout :execrows
-- Done, unless the scope was extended since the worker read it (0 rows: it
-- looks again).
UPDATE rollouts
SET state = 'done', current_app_id = '', current_server_id = '', lease_owner = '', lease_until = NULL, finished_at = $1
WHERE id = $2 AND lease_owner = $3 AND state = 'running' AND scope_rev = $4;

-- name: PruneRollouts :exec
-- Keeps the newest ended rollouts of a scope.
DELETE FROM rollouts
WHERE rollouts.kind = sqlc.arg(kind) AND rollouts.scope_key = sqlc.arg(scope_key)
  AND rollouts.state IN ('done', 'stopped', 'failed')
  AND rollouts.id NOT IN (
    SELECT r.id FROM rollouts AS r
    WHERE r.kind = sqlc.arg(kind) AND r.scope_key = sqlc.arg(scope_key) AND r.state IN ('done', 'stopped', 'failed')
    ORDER BY r.finished_at DESC, r.id DESC
    LIMIT sqlc.arg(keep)::bigint);
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
make sqlc && git status --short internal/infra/db/sqlc/gen
```

Expected: `models.go` and `querier.go` modified and `rollouts.sql.go` new in both `gen/sqlite` and `gen/pgsql`; the generated `Rollout` struct has `LeaseUntil, FinishedAt *types.DateTime`, `ScopeRev, Done, Skipped, Total int64`, `StopRequested bool`.

- [ ] **Step 3: Pin the identical shapes**

In `internal/infra/db/sqlc/shapes_test.go`:

**Replace in** `internal/infra/db/sqlc/shapes_test.go`:

```go
	_ = sqlitegen.RetireTemplateInstancesForAppParams(pgsqlgen.RetireTemplateInstancesForAppParams{})
	_ = pgsqlgen.RetireTemplateInstancesForAppParams(sqlitegen.RetireTemplateInstancesForAppParams{})
)
```
**with:**

```go
	_ = sqlitegen.RetireTemplateInstancesForAppParams(pgsqlgen.RetireTemplateInstancesForAppParams{})
	_ = pgsqlgen.RetireTemplateInstancesForAppParams(sqlitegen.RetireTemplateInstancesForAppParams{})
	_ = sqlitegen.Rollout(pgsqlgen.Rollout{})
	_ = pgsqlgen.Rollout(sqlitegen.Rollout{})
	_ = sqlitegen.InsertRolloutParams(pgsqlgen.InsertRolloutParams{})
	_ = pgsqlgen.InsertRolloutParams(sqlitegen.InsertRolloutParams{})
	_ = sqlitegen.GetActiveRolloutParams(pgsqlgen.GetActiveRolloutParams{})
	_ = pgsqlgen.GetActiveRolloutParams(sqlitegen.GetActiveRolloutParams{})
	_ = sqlitegen.ListRolloutsForScopeParams(pgsqlgen.ListRolloutsForScopeParams{})
	_ = pgsqlgen.ListRolloutsForScopeParams(sqlitegen.ListRolloutsForScopeParams{})
	_ = sqlitegen.ListRolloutsOfKindParams(pgsqlgen.ListRolloutsOfKindParams{})
	_ = pgsqlgen.ListRolloutsOfKindParams(sqlitegen.ListRolloutsOfKindParams{})
	_ = sqlitegen.ExtendRolloutParams(pgsqlgen.ExtendRolloutParams{})
	_ = pgsqlgen.ExtendRolloutParams(sqlitegen.ExtendRolloutParams{})
	_ = sqlitegen.RequestRolloutStopParams(pgsqlgen.RequestRolloutStopParams{})
	_ = pgsqlgen.RequestRolloutStopParams(sqlitegen.RequestRolloutStopParams{})
	_ = sqlitegen.ClaimRolloutParams(pgsqlgen.ClaimRolloutParams{})
	_ = pgsqlgen.ClaimRolloutParams(sqlitegen.ClaimRolloutParams{})
	_ = sqlitegen.RenewRolloutLeaseParams(pgsqlgen.RenewRolloutLeaseParams{})
	_ = pgsqlgen.RenewRolloutLeaseParams(sqlitegen.RenewRolloutLeaseParams{})
	_ = sqlitegen.StartRolloutStepParams(pgsqlgen.StartRolloutStepParams{})
	_ = pgsqlgen.StartRolloutStepParams(sqlitegen.StartRolloutStepParams{})
	_ = sqlitegen.CountRolloutStepParams(pgsqlgen.CountRolloutStepParams{})
	_ = pgsqlgen.CountRolloutStepParams(sqlitegen.CountRolloutStepParams{})
	_ = sqlitegen.FinishRolloutParams(pgsqlgen.FinishRolloutParams{})
	_ = pgsqlgen.FinishRolloutParams(sqlitegen.FinishRolloutParams{})
	_ = sqlitegen.CompleteRolloutParams(pgsqlgen.CompleteRolloutParams{})
	_ = pgsqlgen.CompleteRolloutParams(sqlitegen.CompleteRolloutParams{})
	_ = sqlitegen.PruneRolloutsParams(pgsqlgen.PruneRolloutsParams{})
	_ = pgsqlgen.PruneRolloutsParams(sqlitegen.PruneRolloutsParams{})
)
```
- [ ] **Step 4: Domain types and the port**

**Create** `internal/domain/model/rollout.go`:

```go
package model

import (
	"slices"
	"time"
)

// Rollout kinds: upgrade a template's instances, reload the apps a
// shared-variable change made stale.
const (
	RolloutTemplateUpgrade = "template_upgrade"
	RolloutVarsReload      = "vars_reload"
)

// RolloutState is where a rollout is in its life.
type RolloutState string

const (
	RolloutQueued  RolloutState = "queued"
	RolloutRunning RolloutState = "running"
	RolloutDone    RolloutState = "done"
	RolloutStopped RolloutState = "stopped"
	RolloutFailed  RolloutState = "failed"
)

// Active reports whether the rollout may still take steps.
func (s RolloutState) Active() bool { return s == RolloutQueued || s == RolloutRunning }

// Rollout errors.
var (
	ErrRolloutNotFound = &classified{"rollout not found", ErrNotFound}
	ErrRolloutActive   = &classified{"an upgrade of this template is already running: wait for it or stop it", ErrConflict}
)

// Rollout is one queued, resumable, one-app-at-a-time job.
type Rollout struct {
	ID    string
	OrgID string
	Kind  string
	// Scope is the kind's JSON scope (UpgradeScope, ReloadScope); ScopeRev
	// counts its changes (an extension bumps it).
	Scope    []byte
	ScopeKey string
	ScopeRev int64
	State    RolloutState
	// StopRequested ends the rollout (stopped) after the step in flight.
	StopRequested bool
	// CurrentAppID and CurrentServerID name the app in flight; after a
	// failure, the app that failed.
	CurrentAppID    string
	CurrentServerID string
	Done            int64
	Skipped         int64
	// Total is Done + Skipped + the apps still to do, as of the last step.
	Total int64
	// SkipIDs are what this rollout skipped (instance ids for an upgrade,
	// app ids for a reload): never picked again.
	SkipIDs    []string
	Error      string
	LeaseOwner string
	LeaseUntil *time.Time
	CreatedAt  time.Time
	CreatedBy  string
	FinishedAt *time.Time
}

// UpgradeScope is a template_upgrade rollout's scope: the instances of
// TemplateID below Revision move to it.
type UpgradeScope struct {
	TemplateID string `json:"template_id"`
	Revision   int64  `json:"revision"`
}

// ReloadScope is a vars_reload rollout's scope: the running apps to reload
// are the stale ones (stale_vars in the API's status cache) that
//   - are on a server of ServerIDs and stale for one of Names (a variable
//     saved or deleted with reload, a reload by name or by variable), or
//   - are on a server of AllStale (a reload of a server's stale apps), or
//   - are one of AppIDs (an app's own Reload), whatever the name.
//
// Revisions holds, per server, the shared-variables revision its status
// report must have reached before its stale_vars count (the change is
// pushed with vars.sync first); a server that has not reached it by
// SettleBy is left out.
type ReloadScope struct {
	Names     []string         `json:"names"`
	ServerIDs []string         `json:"server_ids"`
	AllStale  []string         `json:"all_stale,omitempty"`
	AppIDs    []string         `json:"app_ids,omitempty"`
	Revisions map[string]int64 `json:"revisions,omitempty"`
	SettleBy  time.Time        `json:"settle_by"`
}

// Merge is the union of two scopes: lists joined (sorted, no duplicates),
// the higher revision per server, the later SettleBy.
func (s ReloadScope) Merge(o ReloadScope) ReloadScope {
	out := ReloadScope{
		Names: joined(s.Names, o.Names), ServerIDs: joined(s.ServerIDs, o.ServerIDs),
		AllStale: joined(s.AllStale, o.AllStale), AppIDs: joined(s.AppIDs, o.AppIDs),
		Revisions: map[string]int64{}, SettleBy: s.SettleBy,
	}
	for _, m := range []map[string]int64{s.Revisions, o.Revisions} {
		for id, rev := range m {
			if cur, ok := out.Revisions[id]; !ok || rev > cur {
				out.Revisions[id] = rev
			}
		}
	}
	if o.SettleBy.After(out.SettleBy) {
		out.SettleBy = o.SettleBy
	}
	return out
}

// Servers are the servers the scope reaches, sorted.
func (s ReloadScope) Servers() []string { return joined(s.ServerIDs, s.AllStale) }

// Wants reports whether a running app on serverID that is stale for the
// names in stale is to be reloaded.
func (s ReloadScope) Wants(serverID, appID string, stale []string) bool {
	switch {
	case len(stale) == 0:
		return false
	case slices.Contains(s.AllStale, serverID), slices.Contains(s.AppIDs, appID):
		return true
	case !slices.Contains(s.ServerIDs, serverID):
		return false
	}
	for _, n := range stale {
		if slices.Contains(s.Names, n) {
			return true
		}
	}
	return false
}

// joined is a and b without duplicates, sorted (never nil).
func joined(a, b []string) []string {
	out := append(append([]string{}, a...), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// RolloutView is a rollout as the API shows it (get-rollouts, the rollout
// SSE event).
type RolloutView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind" description:"template_upgrade or vars_reload."`
	ScopeKey string `json:"scope_key" description:"The template id (template_upgrade) or the org id (vars_reload)."`
	// Revision is a template_upgrade's target revision (0 for vars_reload).
	Revision      int64  `json:"revision" description:"template_upgrade: the target revision; 0 for vars_reload."`
	State         string `json:"state" description:"queued, running, done, stopped or failed."`
	StopRequested bool   `json:"stop_requested" description:"Stop was asked: the rollout stops after the app in flight."`
	Done          int64  `json:"done"`
	Skipped       int64  `json:"skipped" description:"Apps left out: an instance the revision needs new values for, an app that stopped meanwhile."`
	Total         int64  `json:"total" description:"done + skipped + the apps still to do, as of the last step."`
	// CurrentAppID names the app in flight; after a failure, the app that
	// failed.
	CurrentAppID    string     `json:"current_app_id" description:"The app in flight; after a failure, the app that failed."`
	CurrentServerID string     `json:"current_server_id"`
	CurrentAppName  string     `json:"current_app_name"`
	Error           string     `json:"error"`
	CreatedAt       time.Time  `json:"created_at"`
	CreatedBy       string     `json:"created_by"`
	FinishedAt      *time.Time `json:"finished_at"`
}
```

**Create** `internal/domain/port/rollout.go`:

```go
package port

import (
	"context"
	"time"

	"winterflow/internal/domain/model"
)

// RolloutRepository persists the rollout queue. Every worker write names
// the lease owner: a worker whose lease was taken over changes nothing
// (the call reports false).
type RolloutRepository interface {
	// InsertRollout inserts r as queued; false when its (kind, scope_key)
	// already has an active rollout.
	InsertRollout(ctx context.Context, r model.Rollout) (bool, error)
	// GetRollout returns a rollout; model.ErrRolloutNotFound when unknown.
	GetRollout(ctx context.Context, id string) (model.Rollout, error)
	// GetActiveRollout returns the queued or running rollout of a scope;
	// model.ErrRolloutNotFound when there is none.
	GetActiveRollout(ctx context.Context, kind, scopeKey string) (model.Rollout, error)
	// ListRollouts returns an org's rollouts of kind (of one scope unless
	// scopeKey is ""): the active ones first, then the ended ones newest
	// first.
	ListRollouts(ctx context.Context, orgID, kind, scopeKey string) ([]model.Rollout, error)
	// ExtendRollout replaces an active rollout's scope if its scope_rev is
	// still rev, bumping it; false when the scope moved on or it ended.
	ExtendRollout(ctx context.Context, id string, rev int64, scope []byte) (bool, error)
	// RequestStop asks an active rollout of the org to stop; false when it
	// is not active (or not the org's).
	RequestStop(ctx context.Context, orgID, id string) (bool, error)

	// ListClaimable returns active rollouts without a live lease at now.
	ListClaimable(ctx context.Context, now time.Time) ([]string, error)
	// Claim takes the lease (until) and marks the rollout running, if it
	// is active and nobody holds a live lease at now.
	Claim(ctx context.Context, id, owner string, now, until time.Time) (bool, error)
	RenewLease(ctx context.Context, id, owner string, until time.Time) (bool, error)
	// StartStep records the app a step works on and the total as of now.
	StartStep(ctx context.Context, id, owner, appID, serverID string, total int64) (bool, error)
	// CountStep adds a step's outcome (skipIDs replaces the skip list).
	CountStep(ctx context.Context, id, owner string, done, skipped int64, skipIDs []string) (bool, error)
	// Finish ends the rollout stopped or failed, naming the app (a failure
	// names the one that failed) and releasing the lease.
	Finish(ctx context.Context, id, owner string, state model.RolloutState, errMsg, appID, serverID string) (bool, error)
	// Complete ends the rollout done, unless its scope_rev moved past rev.
	Complete(ctx context.Context, id, owner string, rev int64) (bool, error)
	// Prune deletes the ended rollouts of a scope beyond the newest keep.
	Prune(ctx context.Context, kind, scopeKey string, keep int) error
}
```

**Create** `internal/domain/model/rollout_test.go`:

```go
package model

import (
	"slices"
	"testing"
	"time"
)

func TestReloadScopeMerge(t *testing.T) {
	a := ReloadScope{Names: []string{"B", "A"}, ServerIDs: []string{"s1"}, Revisions: map[string]int64{"s1": 5}}
	b := ReloadScope{Names: []string{"A"}, AllStale: []string{"s3"}, AppIDs: []string{"x"},
		Revisions: map[string]int64{"s1": 3, "s2": 1}, SettleBy: time.Unix(100, 0)}
	m := a.Merge(b)
	if !slices.Equal(m.Names, []string{"A", "B"}) || !slices.Equal(m.AppIDs, []string{"x"}) ||
		m.Revisions["s1"] != 5 || m.Revisions["s2"] != 1 || !m.SettleBy.Equal(time.Unix(100, 0)) {
		t.Fatalf("merged = %+v", m)
	}
	if !slices.Equal(m.Servers(), []string{"s1", "s3"}) {
		t.Fatalf("servers = %v", m.Servers())
	}
	// The later SettleBy wins either way round.
	if got := b.Merge(a).SettleBy; !got.Equal(time.Unix(100, 0)) {
		t.Fatalf("settle by = %v", got)
	}
}

func TestReloadScopeWants(t *testing.T) {
	s := ReloadScope{Names: []string{"SMTP_PASS"}, ServerIDs: []string{"s1", "s2"}, AllStale: []string{"s3"}, AppIDs: []string{"app-x"}}
	cases := []struct {
		server, app string
		stale       []string
		want        bool
	}{
		{"s1", "a", []string{"SMTP_PASS"}, true},
		{"s1", "a", []string{"OTHER"}, false},
		{"s1", "a", nil, false},
		{"s9", "a", []string{"SMTP_PASS"}, false},
		{"s3", "a", []string{"OTHER"}, true},
		{"s9", "app-x", []string{"OTHER"}, true},
		{"s3", "a", nil, false},
	}
	for _, tc := range cases {
		if got := s.Wants(tc.server, tc.app, tc.stale); got != tc.want {
			t.Errorf("Wants(%s, %s, %v) = %v", tc.server, tc.app, tc.stale, got)
		}
	}
	if !RolloutQueued.Active() || !RolloutRunning.Active() || RolloutDone.Active() || RolloutFailed.Active() || RolloutStopped.Active() {
		t.Fatal("Active")
	}
}
```
- [ ] **Step 5: Write the failing repository tests**

**Create** `internal/infra/db/repository/rollout/repo_test.go`:

```go
package rollout

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db/dbtest"
)

var org1, org2 = dbtest.ID(1), dbtest.ID(2)

func newRepo(t *testing.T) *Repo {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func rollout(n int, scopeKey string) model.Rollout {
	return model.Rollout{
		ID: dbtest.ID(100 + n), OrgID: org1, Kind: model.RolloutTemplateUpgrade,
		Scope: []byte(`{"template_id":"t","revision":2}`), ScopeKey: scopeKey, CreatedBy: "u1",
	}
}

func mustInsert(t *testing.T, r *Repo, ro model.Rollout) {
	t.Helper()
	if ok, err := r.InsertRollout(dbtest.Ctx(t), ro); err != nil || !ok {
		t.Fatalf("insert %s: %v %v", ro.ID, ok, err)
	}
}

func mustTrue(t *testing.T, what string, ok bool, err error) {
	t.Helper()
	if err != nil || !ok {
		t.Fatalf("%s: %v %v", what, ok, err)
	}
}

func mustFalse(t *testing.T, what string, ok bool, err error) {
	t.Helper()
	if err != nil || ok {
		t.Fatalf("%s: want false, got %v %v", what, ok, err)
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestOneActiveRolloutPerScope(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	mustInsert(t, r, rollout(1, "tpl-a"))
	ok, err := r.InsertRollout(ctx, rollout(2, "tpl-a"))
	mustFalse(t, "a second active rollout of the scope", ok, err)
	mustInsert(t, r, rollout(3, "tpl-b"))

	got, err := r.GetActiveRollout(ctx, model.RolloutTemplateUpgrade, "tpl-a")
	if err != nil || got.ID != dbtest.ID(101) || got.State != model.RolloutQueued || got.ScopeRev != 1 ||
		string(got.Scope) != `{"template_id":"t","revision":2}` || len(got.SkipIDs) != 0 || got.CreatedBy != "u1" {
		t.Fatalf("active = %+v, %v", got, err)
	}
	if _, err := r.GetActiveRollout(ctx, model.RolloutVarsReload, "tpl-a"); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("another kind's scope: %v", err)
	}
	if _, err := r.GetRollout(ctx, dbtest.ID(999)); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("unknown id: %v", err)
	}

	// Once it ends, the scope takes a new rollout.
	ok, err = r.Claim(ctx, dbtest.ID(101), "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)
	ok, err = r.Finish(ctx, dbtest.ID(101), "w1", model.RolloutStopped, "", "", "")
	mustTrue(t, "finish", ok, err)
	mustInsert(t, r, rollout(2, "tpl-a"))
}

func TestClaimLeaseAndTakeover(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := dbtest.ID(101)
	mustInsert(t, r, rollout(1, "tpl-a"))

	ids, err := r.ListClaimable(ctx, now)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("claimable = %v, %v", ids, err)
	}
	ok, err := r.Claim(ctx, id, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)
	ok, err = r.Claim(ctx, id, "w2", now.Add(time.Minute), now.Add(3*time.Minute))
	mustFalse(t, "a live lease", ok, err)
	if ids, _ := r.ListClaimable(ctx, now.Add(time.Minute)); len(ids) != 0 {
		t.Fatalf("a leased rollout is claimable: %v", ids)
	}
	ok, err = r.RenewLease(ctx, id, "w1", now.Add(4*time.Minute))
	mustTrue(t, "renew", ok, err)
	ok, err = r.Claim(ctx, id, "w2", now.Add(3*time.Minute), now.Add(5*time.Minute))
	mustFalse(t, "a renewed lease", ok, err)

	// Past the lease another worker takes over; the first one's writes
	// then change nothing.
	ok, err = r.Claim(ctx, id, "w2", now.Add(5*time.Minute), now.Add(7*time.Minute))
	mustTrue(t, "takeover", ok, err)
	ok, err = r.RenewLease(ctx, id, "w1", now.Add(9*time.Minute))
	mustFalse(t, "the old owner's renewal", ok, err)
	ok, err = r.StartStep(ctx, id, "w1", "app", "srv", 3)
	mustFalse(t, "the old owner's step", ok, err)
	got, _ := r.GetRollout(ctx, id)
	if got.State != model.RolloutRunning || got.LeaseOwner != "w2" || got.LeaseUntil == nil ||
		!got.LeaseUntil.Equal(now.Add(7*time.Minute)) || got.CurrentAppID != "" {
		t.Fatalf("after takeover = %+v", got)
	}
}

func TestStepsExtendAndFinish(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	id := dbtest.ID(101)
	mustInsert(t, r, rollout(1, "tpl-a"))
	ok, err := r.Claim(ctx, id, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim", ok, err)

	ok, err = r.StartStep(ctx, id, "w1", "app-1", "srv-1", 4)
	mustTrue(t, "start", ok, err)
	ok, err = r.CountStep(ctx, id, "w1", 1, 0, nil)
	mustTrue(t, "count done", ok, err)
	ok, err = r.CountStep(ctx, id, "w1", 0, 1, []string{"inst-2"})
	mustTrue(t, "count skipped", ok, err)
	got, _ := r.GetRollout(ctx, id)
	if got.Done != 1 || got.Skipped != 1 || got.Total != 4 || len(got.SkipIDs) != 1 || got.SkipIDs[0] != "inst-2" ||
		got.CurrentAppID != "app-1" || got.CurrentServerID != "srv-1" {
		t.Fatalf("after steps = %+v", got)
	}

	// An extension moves scope_rev: completing at the old one does nothing.
	ok, err = r.ExtendRollout(ctx, id, 1, []byte(`{"names":["A"]}`))
	mustTrue(t, "extend", ok, err)
	ok, err = r.ExtendRollout(ctx, id, 1, []byte(`{"names":["B"]}`))
	mustFalse(t, "extend at a stale rev", ok, err)
	ok, err = r.Complete(ctx, id, "w1", 1)
	mustFalse(t, "complete at a stale rev", ok, err)
	got, _ = r.GetRollout(ctx, id)
	if got.ScopeRev != 2 || string(got.Scope) != `{"names":["A"]}` || got.State != model.RolloutRunning {
		t.Fatalf("after extend = %+v", got)
	}
	ok, err = r.Complete(ctx, id, "w1", 2)
	mustTrue(t, "complete", ok, err)
	got, _ = r.GetRollout(ctx, id)
	if got.State != model.RolloutDone || got.CurrentAppID != "" || got.LeaseOwner != "" || got.LeaseUntil != nil || got.FinishedAt == nil {
		t.Fatalf("done = %+v", got)
	}
	ok, err = r.ExtendRollout(ctx, id, 2, []byte(`{}`))
	mustFalse(t, "extend an ended rollout", ok, err)

	// A failure keeps the app that failed.
	mustInsert(t, r, rollout(2, "tpl-a"))
	id2 := dbtest.ID(102)
	ok, err = r.Claim(ctx, id2, "w1", now, now.Add(2*time.Minute))
	mustTrue(t, "claim 2", ok, err)
	ok, err = r.Finish(ctx, id2, "w1", model.RolloutFailed, "compose up failed", "app-9", "srv-9")
	mustTrue(t, "fail", ok, err)
	got, _ = r.GetRollout(ctx, id2)
	if got.State != model.RolloutFailed || got.Error != "compose up failed" || got.CurrentAppID != "app-9" || got.CurrentServerID != "srv-9" {
		t.Fatalf("failed = %+v", got)
	}
}

func TestRequestStopIsScopedToTheOrg(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustInsert(t, r, rollout(1, "tpl-a"))
	ok, err := r.RequestStop(ctx, org2, dbtest.ID(101))
	mustFalse(t, "another org's stop", ok, err)
	ok, err = r.RequestStop(ctx, org1, dbtest.ID(101))
	mustTrue(t, "stop", ok, err)
	if got, _ := r.GetRollout(ctx, dbtest.ID(101)); !got.StopRequested {
		t.Fatalf("stop not recorded: %+v", got)
	}
}

func TestListAndPrune(t *testing.T) {
	r := newRepo(t)
	ctx := dbtest.Ctx(t)
	now := time.Now().UTC().Truncate(time.Second)
	for n := 1; n <= 23; n++ {
		ro := rollout(n, "tpl-a")
		mustInsert(t, r, ro)
		ok, err := r.Claim(ctx, ro.ID, "w1", now, now.Add(2*time.Minute))
		mustTrue(t, fmt.Sprint("claim ", n), ok, err)
		ok, err = r.Complete(ctx, ro.ID, "w1", 1)
		mustTrue(t, fmt.Sprint("complete ", n), ok, err)
	}
	mustInsert(t, r, rollout(30, "tpl-a")) // active
	mustInsert(t, r, rollout(31, "tpl-b"))
	other := rollout(32, "tpl-c")
	other.OrgID = org2
	mustInsert(t, r, other)

	if err := r.Prune(ctx, model.RolloutTemplateUpgrade, "tpl-a", 20); err != nil {
		t.Fatal(err)
	}
	list, err := r.ListRollouts(ctx, org1, model.RolloutTemplateUpgrade, "tpl-a")
	if err != nil || len(list) != 21 || list[0].ID != dbtest.ID(130) || list[0].State != model.RolloutQueued {
		t.Fatalf("scope list = %d %v (first %+v)", len(list), err, list[0])
	}
	for _, ro := range list {
		if ro.ID == dbtest.ID(101) || ro.ID == dbtest.ID(102) || ro.ID == dbtest.ID(103) {
			t.Fatalf("pruned rollout %s still listed", ro.ID)
		}
	}
	all, err := r.ListRollouts(ctx, org1, model.RolloutTemplateUpgrade, "")
	if err != nil || len(all) != 22 || all[0].State != model.RolloutQueued || all[1].State != model.RolloutQueued {
		t.Fatalf("kind list = %d %v", len(all), err)
	}
	if got, _ := r.ListRollouts(ctx, org2, model.RolloutTemplateUpgrade, ""); len(got) != 1 {
		t.Fatalf("org2 list = %+v", got)
	}
}
```
In `internal/infra/db/repository/timestamps_test.go` (every TIMESTAMP column must be written once):


**Replace in** `internal/infra/db/repository/timestamps_test.go`:

```go
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
```
**with:**

```go
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	rolloutrepo "winterflow/internal/infra/db/repository/rollout"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
```

**Replace in** `internal/infra/db/repository/timestamps_test.go`:

```go
	sharedVars := must(sharedvarrepo.New(d.Tx, d.Driver, dbtest.Log))
	templates := must(templaterepo.New(d.Tx, d.Driver, dbtest.Log))

	admin := must(users.BootstrapLocalAdmin(ctx, "Admin", "admin@example.com", "correct horse battery"))
```
**with:**

```go
	sharedVars := must(sharedvarrepo.New(d.Tx, d.Driver, dbtest.Log))
	templates := must(templaterepo.New(d.Tx, d.Driver, dbtest.Log))
	rollouts := must(rolloutrepo.New(d.Tx, d.Driver, dbtest.Log))

	admin := must(users.BootstrapLocalAdmin(ctx, "Admin", "admin@example.com", "correct horse battery"))
```

**Replace in** `internal/infra/db/repository/timestamps_test.go`:

```go
	}

	columns := timestampColumns(t, d)
	if len(columns) == 0 {
```
**with:**

```go
	}

	// created_at on insert, lease_until on the claim, finished_at when it
	// ends (a second one keeps its lease for the check).
	for i, id := range []string{dbtest.ID(601), dbtest.ID(602)} {
		ro := model.Rollout{ID: id, OrgID: orgID, Kind: model.RolloutTemplateUpgrade, Scope: []byte(`{}`), ScopeKey: fmt.Sprint("tpl-", i)}
		if _, err := rollouts.InsertRollout(ctx, ro); err != nil {
			t.Fatal(err)
		}
		if _, err := rollouts.Claim(ctx, id, "w", at, at.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := rollouts.Complete(ctx, dbtest.ID(601), "w", 1); err != nil {
		t.Fatal(err)
	}

	columns := timestampColumns(t, d)
	if len(columns) == 0 {
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/infra/db/repository/rollout/
```

Expected: FAIL to compile: `undefined: New`.

- [ ] **Step 6: The repository**

`repo.go` (port methods and hydrate against `sqlitegen` types), `sqlite.go` (passthrough), `pgsql.go` (struct conversions):

**Create** `internal/infra/db/repository/rollout/repo.go`:

```go
// Package rollout implements port.RolloutRepository over the rollouts
// table.
package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	rolloutRow    = sqlitegen.Rollout
	insertParams  = sqlitegen.InsertRolloutParams
	activeParams  = sqlitegen.GetActiveRolloutParams
	scopeParams   = sqlitegen.ListRolloutsForScopeParams
	kindParams    = sqlitegen.ListRolloutsOfKindParams
	extendParams  = sqlitegen.ExtendRolloutParams
	stopParams    = sqlitegen.RequestRolloutStopParams
	claimParams   = sqlitegen.ClaimRolloutParams
	renewParams   = sqlitegen.RenewRolloutLeaseParams
	startParams   = sqlitegen.StartRolloutStepParams
	countParams   = sqlitegen.CountRolloutStepParams
	finishParams  = sqlitegen.FinishRolloutParams
	completeParam = sqlitegen.CompleteRolloutParams
	pruneParams   = sqlitegen.PruneRolloutsParams
)

type querier interface {
	InsertRollout(ctx context.Context, db backend.DBTX, p insertParams) (int64, error)
	GetRollout(ctx context.Context, db backend.DBTX, id string) (rolloutRow, error)
	GetActiveRollout(ctx context.Context, db backend.DBTX, p activeParams) (rolloutRow, error)
	ListRolloutsForScope(ctx context.Context, db backend.DBTX, p scopeParams) ([]rolloutRow, error)
	ListRolloutsOfKind(ctx context.Context, db backend.DBTX, p kindParams) ([]rolloutRow, error)
	ExtendRollout(ctx context.Context, db backend.DBTX, p extendParams) (int64, error)
	RequestRolloutStop(ctx context.Context, db backend.DBTX, p stopParams) (int64, error)
	ListClaimableRollouts(ctx context.Context, db backend.DBTX, now *types.DateTime) ([]string, error)
	ClaimRollout(ctx context.Context, db backend.DBTX, p claimParams) (int64, error)
	RenewRolloutLease(ctx context.Context, db backend.DBTX, p renewParams) (int64, error)
	StartRolloutStep(ctx context.Context, db backend.DBTX, p startParams) (int64, error)
	CountRolloutStep(ctx context.Context, db backend.DBTX, p countParams) (int64, error)
	FinishRollout(ctx context.Context, db backend.DBTX, p finishParams) (int64, error)
	CompleteRollout(ctx context.Context, db backend.DBTX, p completeParam) (int64, error)
	PruneRollouts(ctx context.Context, db backend.DBTX, p pruneParams) error
}

// Repo is the rollouts repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.RolloutRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("rollout repository: unknown database driver %q", driver)
}

func at(t time.Time) *types.DateTime {
	dt := types.DateTime(t)
	return &dt
}

func affected(n int64, err error) (bool, error) { return n > 0, err }

// InsertRollout inserts r as queued unless its scope has an active one.
func (r *Repo) InsertRollout(ctx context.Context, ro model.Rollout) (bool, error) {
	return affected(r.q.InsertRollout(ctx, r.tx.Querier(ctx), insertParams{
		ID: ro.ID, OrgID: ro.OrgID, Kind: ro.Kind, Scope: string(ro.Scope), ScopeKey: ro.ScopeKey,
		CreatedAt: types.NewDateTime(), CreatedBy: ro.CreatedBy,
	}))
}

// GetRollout returns a rollout by id.
func (r *Repo) GetRollout(ctx context.Context, id string) (model.Rollout, error) {
	return one(r.q.GetRollout(ctx, r.tx.Querier(ctx), id))
}

// GetActiveRollout returns the queued or running rollout of a scope.
func (r *Repo) GetActiveRollout(ctx context.Context, kind, scopeKey string) (model.Rollout, error) {
	return one(r.q.GetActiveRollout(ctx, r.tx.Querier(ctx), activeParams{Kind: kind, ScopeKey: scopeKey}))
}

// ListRollouts returns an org's rollouts of kind, active first.
func (r *Repo) ListRollouts(ctx context.Context, orgID, kind, scopeKey string) ([]model.Rollout, error) {
	var (
		rows []rolloutRow
		err  error
	)
	if scopeKey == "" {
		rows, err = r.q.ListRolloutsOfKind(ctx, r.tx.Querier(ctx), kindParams{OrgID: orgID, Kind: kind})
	} else {
		rows, err = r.q.ListRolloutsForScope(ctx, r.tx.Querier(ctx), scopeParams{OrgID: orgID, Kind: kind, ScopeKey: scopeKey})
	}
	if err != nil {
		return nil, err
	}
	out := make([]model.Rollout, 0, len(rows))
	for _, row := range rows {
		ro, err := hydrate(row)
		if err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, nil
}

// ExtendRollout replaces the scope while scope_rev is still rev.
func (r *Repo) ExtendRollout(ctx context.Context, id string, rev int64, scope []byte) (bool, error) {
	return affected(r.q.ExtendRollout(ctx, r.tx.Querier(ctx), extendParams{Scope: string(scope), ID: id, ScopeRev: rev}))
}

// RequestStop sets stop_requested on an active rollout of the org.
func (r *Repo) RequestStop(ctx context.Context, orgID, id string) (bool, error) {
	return affected(r.q.RequestRolloutStop(ctx, r.tx.Querier(ctx), stopParams{ID: id, OrgID: orgID}))
}

// ListClaimable returns active rollouts without a live lease, oldest first.
func (r *Repo) ListClaimable(ctx context.Context, now time.Time) ([]string, error) {
	return r.q.ListClaimableRollouts(ctx, r.tx.Querier(ctx), at(now))
}

// Claim takes the lease of an active rollout nobody holds.
func (r *Repo) Claim(ctx context.Context, id, owner string, now, until time.Time) (bool, error) {
	return affected(r.q.ClaimRollout(ctx, r.tx.Querier(ctx), claimParams{
		LeaseOwner: owner, LeaseUntil: at(until), ID: id, LeaseUntil_2: at(now),
	}))
}

// RenewLease moves the owner's lease to until.
func (r *Repo) RenewLease(ctx context.Context, id, owner string, until time.Time) (bool, error) {
	return affected(r.q.RenewRolloutLease(ctx, r.tx.Querier(ctx), renewParams{LeaseUntil: at(until), ID: id, LeaseOwner: owner}))
}

// StartStep records the app in flight and the total.
func (r *Repo) StartStep(ctx context.Context, id, owner, appID, serverID string, total int64) (bool, error) {
	return affected(r.q.StartRolloutStep(ctx, r.tx.Querier(ctx), startParams{
		CurrentAppID: appID, CurrentServerID: serverID, Total: total, ID: id, LeaseOwner: owner,
	}))
}

// CountStep adds a step's outcome.
func (r *Repo) CountStep(ctx context.Context, id, owner string, done, skipped int64, skipIDs []string) (bool, error) {
	if skipIDs == nil {
		skipIDs = []string{}
	}
	raw, err := json.Marshal(skipIDs)
	if err != nil {
		return false, err
	}
	return affected(r.q.CountRolloutStep(ctx, r.tx.Querier(ctx), countParams{
		Done: done, Skipped: skipped, SkipIds: string(raw), ID: id, LeaseOwner: owner,
	}))
}

// Finish ends the rollout stopped or failed.
func (r *Repo) Finish(ctx context.Context, id, owner string, state model.RolloutState, errMsg, appID, serverID string) (bool, error) {
	return affected(r.q.FinishRollout(ctx, r.tx.Querier(ctx), finishParams{
		State: string(state), Error: errMsg, CurrentAppID: appID, CurrentServerID: serverID,
		FinishedAt: at(time.Now()), ID: id, LeaseOwner: owner,
	}))
}

// Complete ends the rollout done while scope_rev is still rev.
func (r *Repo) Complete(ctx context.Context, id, owner string, rev int64) (bool, error) {
	return affected(r.q.CompleteRollout(ctx, r.tx.Querier(ctx), completeParam{
		FinishedAt: at(time.Now()), ID: id, LeaseOwner: owner, ScopeRev: rev,
	}))
}

// Prune keeps the newest keep ended rollouts of a scope.
func (r *Repo) Prune(ctx context.Context, kind, scopeKey string, keep int) error {
	return r.q.PruneRollouts(ctx, r.tx.Querier(ctx), pruneParams{Kind: kind, ScopeKey: scopeKey, Keep: int64(keep)})
}

func one(row rolloutRow, err error) (model.Rollout, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return model.Rollout{}, model.ErrRolloutNotFound
	}
	if err != nil {
		return model.Rollout{}, err
	}
	return hydrate(row)
}

func hydrate(row rolloutRow) (model.Rollout, error) {
	out := model.Rollout{
		ID: row.ID, OrgID: row.OrgID, Kind: row.Kind, Scope: []byte(row.Scope), ScopeKey: row.ScopeKey,
		ScopeRev: row.ScopeRev, State: model.RolloutState(row.State), StopRequested: row.StopRequested,
		CurrentAppID: row.CurrentAppID, CurrentServerID: row.CurrentServerID, Done: row.Done,
		Skipped: row.Skipped, Total: row.Total, Error: row.Error, LeaseOwner: row.LeaseOwner,
		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
		LeaseUntil: timePtr(row.LeaseUntil), FinishedAt: timePtr(row.FinishedAt),
	}
	if err := json.Unmarshal([]byte(row.SkipIds), &out.SkipIDs); err != nil {
		return model.Rollout{}, fmt.Errorf("rollout %s: skip_ids: %w", row.ID, err)
	}
	return out, nil
}

func timePtr(dt *types.DateTime) *time.Time {
	if dt == nil {
		return nil
	}
	t := dt.Time()
	return &t
}
```

**Create** `internal/infra/db/repository/rollout/sqlite.go`:

```go
package rollout

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
	"winterflow/internal/infra/db/types"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertRollout(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return sqlitegen.New(db).InsertRollout(ctx, p)
}

func (sqliteQuerier) GetRollout(ctx context.Context, db backend.DBTX, id string) (rolloutRow, error) {
	return sqlitegen.New(db).GetRollout(ctx, id)
}

func (sqliteQuerier) GetActiveRollout(ctx context.Context, db backend.DBTX, p activeParams) (rolloutRow, error) {
	return sqlitegen.New(db).GetActiveRollout(ctx, p)
}

func (sqliteQuerier) ListRolloutsForScope(ctx context.Context, db backend.DBTX, p scopeParams) ([]rolloutRow, error) {
	return sqlitegen.New(db).ListRolloutsForScope(ctx, p)
}

func (sqliteQuerier) ListRolloutsOfKind(ctx context.Context, db backend.DBTX, p kindParams) ([]rolloutRow, error) {
	return sqlitegen.New(db).ListRolloutsOfKind(ctx, p)
}

func (sqliteQuerier) ExtendRollout(ctx context.Context, db backend.DBTX, p extendParams) (int64, error) {
	return sqlitegen.New(db).ExtendRollout(ctx, p)
}

func (sqliteQuerier) RequestRolloutStop(ctx context.Context, db backend.DBTX, p stopParams) (int64, error) {
	return sqlitegen.New(db).RequestRolloutStop(ctx, p)
}

func (sqliteQuerier) ListClaimableRollouts(ctx context.Context, db backend.DBTX, now *types.DateTime) ([]string, error) {
	return sqlitegen.New(db).ListClaimableRollouts(ctx, now)
}

func (sqliteQuerier) ClaimRollout(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return sqlitegen.New(db).ClaimRollout(ctx, p)
}

func (sqliteQuerier) RenewRolloutLease(ctx context.Context, db backend.DBTX, p renewParams) (int64, error) {
	return sqlitegen.New(db).RenewRolloutLease(ctx, p)
}

func (sqliteQuerier) StartRolloutStep(ctx context.Context, db backend.DBTX, p startParams) (int64, error) {
	return sqlitegen.New(db).StartRolloutStep(ctx, p)
}

func (sqliteQuerier) CountRolloutStep(ctx context.Context, db backend.DBTX, p countParams) (int64, error) {
	return sqlitegen.New(db).CountRolloutStep(ctx, p)
}

func (sqliteQuerier) FinishRollout(ctx context.Context, db backend.DBTX, p finishParams) (int64, error) {
	return sqlitegen.New(db).FinishRollout(ctx, p)
}

func (sqliteQuerier) CompleteRollout(ctx context.Context, db backend.DBTX, p completeParam) (int64, error) {
	return sqlitegen.New(db).CompleteRollout(ctx, p)
}

func (sqliteQuerier) PruneRollouts(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return sqlitegen.New(db).PruneRollouts(ctx, p)
}
```

**Create** `internal/infra/db/repository/rollout/pgsql.go`:

```go
package rollout

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
	"winterflow/internal/infra/db/types"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertRollout(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return pgsqlgen.New(db).InsertRollout(ctx, pgsqlgen.InsertRolloutParams(p))
}

func (pgsqlQuerier) GetRollout(ctx context.Context, db backend.DBTX, id string) (rolloutRow, error) {
	row, err := pgsqlgen.New(db).GetRollout(ctx, id)
	return rolloutRow(row), err
}

func (pgsqlQuerier) GetActiveRollout(ctx context.Context, db backend.DBTX, p activeParams) (rolloutRow, error) {
	row, err := pgsqlgen.New(db).GetActiveRollout(ctx, pgsqlgen.GetActiveRolloutParams(p))
	return rolloutRow(row), err
}

func (pgsqlQuerier) ListRolloutsForScope(ctx context.Context, db backend.DBTX, p scopeParams) ([]rolloutRow, error) {
	rows, err := pgsqlgen.New(db).ListRolloutsForScope(ctx, pgsqlgen.ListRolloutsForScopeParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]rolloutRow, len(rows))
	for i, row := range rows {
		out[i] = rolloutRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ListRolloutsOfKind(ctx context.Context, db backend.DBTX, p kindParams) ([]rolloutRow, error) {
	rows, err := pgsqlgen.New(db).ListRolloutsOfKind(ctx, pgsqlgen.ListRolloutsOfKindParams(p))
	if err != nil {
		return nil, err
	}
	out := make([]rolloutRow, len(rows))
	for i, row := range rows {
		out[i] = rolloutRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) ExtendRollout(ctx context.Context, db backend.DBTX, p extendParams) (int64, error) {
	return pgsqlgen.New(db).ExtendRollout(ctx, pgsqlgen.ExtendRolloutParams(p))
}

func (pgsqlQuerier) RequestRolloutStop(ctx context.Context, db backend.DBTX, p stopParams) (int64, error) {
	return pgsqlgen.New(db).RequestRolloutStop(ctx, pgsqlgen.RequestRolloutStopParams(p))
}

func (pgsqlQuerier) ListClaimableRollouts(ctx context.Context, db backend.DBTX, now *types.DateTime) ([]string, error) {
	return pgsqlgen.New(db).ListClaimableRollouts(ctx, now)
}

func (pgsqlQuerier) ClaimRollout(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return pgsqlgen.New(db).ClaimRollout(ctx, pgsqlgen.ClaimRolloutParams(p))
}

func (pgsqlQuerier) RenewRolloutLease(ctx context.Context, db backend.DBTX, p renewParams) (int64, error) {
	return pgsqlgen.New(db).RenewRolloutLease(ctx, pgsqlgen.RenewRolloutLeaseParams(p))
}

func (pgsqlQuerier) StartRolloutStep(ctx context.Context, db backend.DBTX, p startParams) (int64, error) {
	return pgsqlgen.New(db).StartRolloutStep(ctx, pgsqlgen.StartRolloutStepParams(p))
}

func (pgsqlQuerier) CountRolloutStep(ctx context.Context, db backend.DBTX, p countParams) (int64, error) {
	return pgsqlgen.New(db).CountRolloutStep(ctx, pgsqlgen.CountRolloutStepParams(p))
}

func (pgsqlQuerier) FinishRollout(ctx context.Context, db backend.DBTX, p finishParams) (int64, error) {
	return pgsqlgen.New(db).FinishRollout(ctx, pgsqlgen.FinishRolloutParams(p))
}

func (pgsqlQuerier) CompleteRollout(ctx context.Context, db backend.DBTX, p completeParam) (int64, error) {
	return pgsqlgen.New(db).CompleteRollout(ctx, pgsqlgen.CompleteRolloutParams(p))
}

func (pgsqlQuerier) PruneRollouts(ctx context.Context, db backend.DBTX, p pruneParams) error {
	return pgsqlgen.New(db).PruneRollouts(ctx, pgsqlgen.PruneRolloutsParams(p))
}
```
- [ ] **Step 7: Wire the repository**

**Replace in** `internal/infra/bootstrap/deps.go`:

```go
	SharedVariableRepository port.SharedVariableRepository
	// TemplateRepository stores templates, their revisions and instances.
	TemplateRepository  port.TemplateRepository
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
```
**with:**

```go
	SharedVariableRepository port.SharedVariableRepository
	// TemplateRepository stores templates, their revisions and instances.
	TemplateRepository port.TemplateRepository
	// RolloutRepository stores the rollout queue.
	RolloutRepository   port.RolloutRepository
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
```

**Replace in** `internal/infra/bootstrap/core.go`:

```go
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
```
**with:**

```go
	appdomainrepo "winterflow/internal/infra/db/repository/appdomain"
	metricrepo "winterflow/internal/infra/db/repository/metric"
	rolloutrepo "winterflow/internal/infra/db/repository/rollout"
	serverrepo "winterflow/internal/infra/db/repository/server"
	serverdomainrepo "winterflow/internal/infra/db/repository/serverdomain"
```

**Replace in** `internal/infra/bootstrap/core.go`:

```go
		SharedVariableRepository: r.sharedVar,
		TemplateRepository:       r.template,
		CommandDispatcher:        dispatcher,
		NotificationManager:      nm,
```
**with:**

```go
		SharedVariableRepository: r.sharedVar,
		TemplateRepository:       r.template,
		RolloutRepository:        r.rollout,
		CommandDispatcher:        dispatcher,
		NotificationManager:      nm,
```

**Replace in** `internal/infra/bootstrap/core.go`:

```go
	sharedVar     *sharedvarrepo.Repo
	template      *templaterepo.Repo
	alert         *alertrepo.Repo
}
```
**with:**

```go
	sharedVar     *sharedvarrepo.Repo
	template      *templaterepo.Repo
	rollout       *rolloutrepo.Repo
	alert         *alertrepo.Repo
}
```

**Replace in** `internal/infra/bootstrap/core.go`:

```go
		return r, err
	}
	r.alert, err = alertrepo.New(conn.Tx, conn.Driver, log, util.GenerateID)
	return r, err
```
**with:**

```go
		return r, err
	}
	if r.rollout, err = rolloutrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	r.alert, err = alertrepo.New(conn.Tx, conn.Driver, log, util.GenerateID)
	return r, err
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal; go vet ./internal/... && go test -count=1 ./internal/domain/model/ ./internal/infra/db/... ./internal/infra/bootstrap/
```

Expected: no gofmt output; every package `ok` (`repository/rollout`, `repository` with the new timestamp writes, `sqlc` with the shapes).

Run:

```bash
make test-pg TEST_PG_ARGS='-run "TestOneActiveRolloutPerScope|TestClaimLeaseAndTakeover|TestStepsExtendAndFinish|TestRequestStopIsScopedToTheOrg|TestListAndPrune|TestSQLiteTimestamps"'
```

Expected: `ok` for `internal/infra/db/repository/rollout` on postgres:16 (the contract package runs nothing for this filter).

- [ ] **Commit**

```bash
git add internal/infra/db/migrations internal/infra/db/sqlc internal/domain/model/rollout.go internal/domain/model/rollout_test.go internal/domain/port/rollout.go internal/infra/db/repository internal/infra/bootstrap/deps.go internal/infra/bootstrap/core.go
git commit -m "feat(db): rollouts table and repository

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Instances stay on their own org's servers (hardening)

**Files:**
- Modify: `internal/infra/db/sqlc/query/{sqlite,pgsql}/servers.sql`, `internal/infra/db/sqlc/query/{sqlite,pgsql}/templates.sql`, `internal/infra/db/sqlc/shapes_test.go` (also edited by Task 1)
- Generated (do not edit): `internal/infra/db/sqlc/gen/{sqlite,pgsql}/{servers.sql.go,templates.sql.go,querier.go}`
- Modify: `internal/domain/port/server.go`, `internal/domain/port/template.go`
- Modify: `internal/infra/db/repository/server/{repo.go,sqlite.go,pgsql.go,repo_test.go}`
- Modify: `internal/infra/db/repository/template/{repo.go,sqlite.go,pgsql.go,repo_test.go}`
- Modify: `internal/app/web/handler/templates/handler.go` (also edited by Task 7), `respond_test.go`, `handler_test.go`
- Test: `internal/app/web/contract/instances_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `port.ServerRepository.ServerInOrg(ctx, serverID, orgID string) (bool, error)` (`(*serverrepo.Repo).ServerInOrg`, query `ServerInOrganization`); `port.TemplateRepository.ListInstancesOnOrgServers(ctx, orgID string) ([]model.Instance, error)` (query `ListLiveTemplateInstancesOnOrgServers`, joining `servers` on `organization_id = org_id`); `htemplates.InstanceServers` (the type of `htemplates.Deps.Servers`: `webutil.ServerAccess` plus `ServerInOrg`; `port.ServerRepository` satisfies it, so `routes.go` is unchanged).

A security review found that the instance routes resolve the org the call acts for with `webutil.CallerOrg` (the caller's *primary* org) but authorize the server with `RequireOwnedServer` (*any* org the caller belongs to). Today every membership insert creates a new user, so a user has exactly one org and this is not exploitable; but the rollout worker dispatches an upgrade to every instance of an org's template, so an instance must never sit on another org's server. Two layers: `serverCaller` also requires the server to be in the resolved org (403, a lookup error denies too), and the `template_upgrade` next-app query (Task 6) only sees instances on the org's own servers.

- [ ] **Step 1: The two queries**

**Replace in** `internal/infra/db/sqlc/query/sqlite/servers.sql`:

```sql
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
);

-- name: HasAnyServer :one
```
**with:**

```sql
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = ?)
);

-- name: ServerInOrganization :one
-- Whether the server belongs to the organization (instance routes act for
-- the caller's primary organization only).
SELECT EXISTS (SELECT 1 FROM servers WHERE server_id = ? AND organization_id = ?);

-- name: HasAnyServer :one
```

**Replace in** `internal/infra/db/sqlc/query/pgsql/servers.sql`:

```sql
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $2)
);

-- name: HasAnyServer :one
```
**with:**

```sql
      AND organization_id IN (SELECT organization_id FROM organization_users WHERE user_id = $2)
);

-- name: ServerInOrganization :one
-- Whether the server belongs to the organization (instance routes act for
-- the caller's primary organization only).
SELECT EXISTS (SELECT 1 FROM servers WHERE server_id = $1 AND organization_id = $2);

-- name: HasAnyServer :one
```

**Replace in** `internal/infra/db/sqlc/query/sqlite/templates.sql`:

```sql
ORDER BY created_at ASC, id ASC;

-- name: MarkTemplateInstanceSaved :exec
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
```
**with:**

```sql
ORDER BY created_at ASC, id ASC;

-- name: ListLiveTemplateInstancesOnOrgServers :many
-- The org's pending and created instances on the org's own servers: what a
-- rollout of the org may touch.
SELECT i.* FROM template_instances AS i
JOIN servers AS s ON s.server_id = i.server_id AND s.organization_id = i.org_id
WHERE i.org_id = ? AND i.state IN ('pending', 'created')
ORDER BY i.created_at ASC, i.id ASC;

-- name: MarkTemplateInstanceSaved :exec
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
```

**Replace in** `internal/infra/db/sqlc/query/pgsql/templates.sql`:

```sql
ORDER BY created_at ASC, id ASC;

-- name: MarkTemplateInstanceSaved :exec
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
```
**with:**

```sql
ORDER BY created_at ASC, id ASC;

-- name: ListLiveTemplateInstancesOnOrgServers :many
-- The org's pending and created instances on the org's own servers: what a
-- rollout of the org may touch.
SELECT i.* FROM template_instances AS i
JOIN servers AS s ON s.server_id = i.server_id AND s.organization_id = i.org_id
WHERE i.org_id = $1 AND i.state IN ('pending', 'created')
ORDER BY i.created_at ASC, i.id ASC;

-- name: MarkTemplateInstanceSaved :exec
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
make sqlc && git status --short internal/infra/db/sqlc/gen
```

Expected: `servers.sql.go`, `templates.sql.go` and `querier.go` modified in both engines; `ServerInOrganization` returns `bool` and `ListLiveTemplateInstancesOnOrgServers` `[]TemplateInstance` on both.

- [ ] **Step 2: Write the failing tests**

**Replace in** `internal/infra/db/sqlc/shapes_test.go`:

```go
	_ = sqlitegen.RetireTemplateInstancesForAppParams(pgsqlgen.RetireTemplateInstancesForAppParams{})
	_ = pgsqlgen.RetireTemplateInstancesForAppParams(sqlitegen.RetireTemplateInstancesForAppParams{})
	_ = sqlitegen.Rollout(pgsqlgen.Rollout{})
	_ = pgsqlgen.Rollout(sqlitegen.Rollout{})
```
**with:**

```go
	_ = sqlitegen.RetireTemplateInstancesForAppParams(pgsqlgen.RetireTemplateInstancesForAppParams{})
	_ = pgsqlgen.RetireTemplateInstancesForAppParams(sqlitegen.RetireTemplateInstancesForAppParams{})
	_ = sqlitegen.ServerInOrganizationParams(pgsqlgen.ServerInOrganizationParams{})
	_ = pgsqlgen.ServerInOrganizationParams(sqlitegen.ServerInOrganizationParams{})
	_ = sqlitegen.Rollout(pgsqlgen.Rollout{})
	_ = pgsqlgen.Rollout(sqlitegen.Rollout{})
```

**Replace in** `internal/infra/db/repository/server/repo_test.go`:

```go
		}
	}
}

```
**with:**

```go
		}
	}
	for _, c := range []struct {
		server, org string
		want        bool
	}{{srv1, org1, true}, {srv1, org2, false}, {srv2, org1, false}} {
		if got, err := repo.ServerInOrg(ctx, c.server, c.org); got != c.want || err != nil {
			t.Errorf("ServerInOrg(%s, %s) = %v, %v", c.server, c.org, got, err)
		}
	}
}

```

**Replace in** `internal/infra/db/repository/template/repo_test.go`:

```go
}

// The app routes see an app that is a live instance; delete-app retires
// its key. An unlinked instance is no longer the app's, yet still read by
```
**with:**

```go
}

// A rollout of an org touches only instances on the org's own servers.
func TestListInstancesOnOrgServers(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	_, _, _ = r.ClaimInstance(ctx, instance(1, "own"))
	stray := instance(2, "stray")
	stray.ServerID = srv2 // org2's server
	_, _, _ = r.ClaimInstance(ctx, stray)
	if all, _ := r.ListInstances(ctx, org1); len(all) != 2 {
		t.Fatalf("all = %+v", all)
	}
	got, err := r.ListInstancesOnOrgServers(ctx, org1)
	if err != nil || len(got) != 1 || got[0].Key != "own" {
		t.Fatalf("on org1's servers = %+v, %v", got, err)
	}
}

// The app routes see an app that is a live instance; delete-app retires
// its key. An unlinked instance is no longer the app's, yet still read by
```

**Replace in** `internal/app/web/handler/templates/respond_test.go`:

```go

func (allServers) UserOwnsServer(context.Context, string, string) (bool, error) { return true, nil }
```
**with:**

```go

func (allServers) UserOwnsServer(context.Context, string, string) (bool, error) { return true, nil }

func (allServers) ServerInOrg(context.Context, string, string) (bool, error) { return true, nil }
```

**Replace in** `internal/app/web/handler/templates/handler_test.go`:

```go
		t.Fatalf("oversized save = %d %s", w.Code, w.Body)
	}
}
```
**with:**

```go
		t.Fatalf("oversized save = %d %s", w.Code, w.Body)
	}
}

// memberElsewhere: the caller belongs to the server's org (ownership
// passes), but it is not the org they act for (their primary one).
type memberElsewhere struct{ err error }

func (memberElsewhere) UserOwnsServer(context.Context, string, string) (bool, error) {
	return true, nil
}
func (m memberElsewhere) ServerInOrg(context.Context, string, string) (bool, error) {
	return false, m.err
}

// An instance never sits on a server outside the org the call acts for,
// even one of another org the caller is a member of.
func TestInstanceRoutesRequireTheCallersOrgServer(t *testing.T) {
	for _, servers := range []memberElsewhere{{}, {err: errors.New("db down")}} {
		h := NewHandler(&Deps{Logger: testLog, Users: orgs{}, Servers: servers})
		if w := serve(h.GetInstance, http.MethodGet, "/?server_id=s-b&key=k", ""); w.Code != http.StatusForbidden {
			t.Errorf("get-instance on another org's server (%v) = %d %s", servers.err, w.Code, w.Body)
		}
		if w := serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s-b","key":"k","template_id":"00000000-0000-7000-8000-000000000021"}`); w.Code != http.StatusForbidden {
			t.Errorf("create-instance on another org's server (%v) = %d %s", servers.err, w.Code, w.Body)
		}
	}
}
```

**Replace in** `internal/app/web/contract/instances_test.go`:

```go
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/crypto/cryptotest"
```
**with:**

```go
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	"winterflow/internal/infra/transport/bus"
	"winterflow/pkg/crypto/cryptotest"
```

**Replace in** `internal/app/web/contract/instances_test.go`:

```go
	owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: ie.tplID}).expect(t, http.StatusOK)
}
```
**with:**

```go
	owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: ie.tplID}).expect(t, http.StatusOK)
}

// A member of two orgs acts for their primary one (the oldest membership):
// the other org's server is theirs to see, but no instance may sit on it.
func TestInstanceRoutesStayInTheCallersOrg(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	other := e.foreignServer(t)
	orgB := dbtest.QueryString(t, e.conn, `SELECT organization_id FROM servers WHERE server_id = ?`, other)
	dbtest.Exec(t, e.conn, `INSERT INTO organization_users (organization_id, user_id, role, created_at) VALUES (?, ?, 'admin', '2099-01-01 00:00:00')`,
		orgB, owner.userID)
	owner.get(t, "/api/v1/app/get-apps", sq(other)).expect(t, http.StatusOK)
	owner.get(t, "/api/v1/instance/get-instances", sq(other)).expect(t, http.StatusForbidden)
	owner.post(t, "/api/v1/instance/create-instance", htpl.CreateInstanceRequest{ServerID: other, Key: "k", TemplateID: anApp}).
		expect(t, http.StatusForbidden)
	if n := e.agent.count(); n != 0 {
		t.Fatalf("%d command(s) reached an agent", n)
	}
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/infra/db/repository/server/ ./internal/infra/db/repository/template/ ./internal/app/web/handler/templates/
```

Expected: server and template: FAIL to compile (`repo.ServerInOrg undefined`, `r.ListInstancesOnOrgServers undefined`); templates handler: FAIL — `TestInstanceRoutesRequireTheCallersOrgServer` panics with a nil pointer (ownership alone let the call through to the instance usecase, which the test leaves unwired).

- [ ] **Step 3: Implement**

**Replace in** `internal/domain/port/server.go`:

```go
	GetCapability(ctx context.Context, serverID, name string) (value string, ok bool, err error)

	ServerOwnership
}
```
**with:**

```go
	GetCapability(ctx context.Context, serverID, name string) (value string, ok bool, err error)

	// ServerInOrg reports whether the server belongs to orgID (unknown:
	// false, no error).
	ServerInOrg(ctx context.Context, serverID, orgID string) (bool, error)

	ServerOwnership
}
```

**Replace in** `internal/domain/port/template.go`:

```go
	// oldest first.
	ListInstances(ctx context.Context, orgID string) ([]model.Instance, error)
	// MarkSaved records a save the agent confirmed: revision, values and
	// state created, last error cleared. A deleted or unlinked instance is
```
**with:**

```go
	// oldest first.
	ListInstances(ctx context.Context, orgID string) ([]model.Instance, error)
	// ListInstancesOnOrgServers is ListInstances without an instance whose
	// server is not the org's: what a rollout of the org may touch.
	ListInstancesOnOrgServers(ctx context.Context, orgID string) ([]model.Instance, error)
	// MarkSaved records a save the agent confirmed: revision, values and
	// state created, last error cleared. A deleted or unlinked instance is
```

**Replace in** `internal/infra/db/repository/server/repo.go`:

```go
	registrationRow    = sqlitegen.ServerRegistration
	ownsParams         = sqlitegen.UserOwnsServerParams
	insertServerParams = sqlitegen.InsertServerParams
	touchParams        = sqlitegen.TouchServerLastSeenParams
```
**with:**

```go
	registrationRow    = sqlitegen.ServerRegistration
	ownsParams         = sqlitegen.UserOwnsServerParams
	inOrgParams        = sqlitegen.ServerInOrganizationParams
	insertServerParams = sqlitegen.InsertServerParams
	touchParams        = sqlitegen.TouchServerLastSeenParams
```

**Replace in** `internal/infra/db/repository/server/repo.go`:

```go
	ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
	UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error)
	HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error)
	GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error)
```
**with:**

```go
	ListServerUserIDs(ctx context.Context, db backend.DBTX, serverID string) ([]string, error)
	UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error)
	ServerInOrganization(ctx context.Context, db backend.DBTX, p inOrgParams) (bool, error)
	HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error)
	GetFirstServerID(ctx context.Context, db backend.DBTX) (string, error)
```

**Replace in** `internal/infra/db/repository/server/repo.go`:

```go
func (r *Repo) UserOwnsServer(ctx context.Context, userID, serverID string) (bool, error) {
	return r.q.UserOwnsServer(ctx, r.tx.Querier(ctx), ownsParams{ServerID: serverID, UserID: userID})
}

```
**with:**

```go
func (r *Repo) UserOwnsServer(ctx context.Context, userID, serverID string) (bool, error) {
	return r.q.UserOwnsServer(ctx, r.tx.Querier(ctx), ownsParams{ServerID: serverID, UserID: userID})
}

// ServerInOrg reports whether the server belongs to orgID. Unknown server:
// false, no error.
func (r *Repo) ServerInOrg(ctx context.Context, serverID, orgID string) (bool, error) {
	return r.q.ServerInOrganization(ctx, r.tx.Querier(ctx), inOrgParams{ServerID: serverID, OrganizationID: orgID})
}

```

**Replace in** `internal/infra/db/repository/server/sqlite.go`:

```go
func (sqliteQuerier) UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error) {
	return sqlitegen.New(db).UserOwnsServer(ctx, p)
}

```
**with:**

```go
func (sqliteQuerier) UserOwnsServer(ctx context.Context, db backend.DBTX, p ownsParams) (bool, error) {
	return sqlitegen.New(db).UserOwnsServer(ctx, p)
}

func (sqliteQuerier) ServerInOrganization(ctx context.Context, db backend.DBTX, p inOrgParams) (bool, error) {
	return sqlitegen.New(db).ServerInOrganization(ctx, p)
}

```

**Replace in** `internal/infra/db/repository/server/pgsql.go`:

```go
}

func (pgsqlQuerier) HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error) {
	return pgsqlgen.New(db).HasAnyServer(ctx)
```
**with:**

```go
}

func (pgsqlQuerier) ServerInOrganization(ctx context.Context, db backend.DBTX, p inOrgParams) (bool, error) {
	return pgsqlgen.New(db).ServerInOrganization(ctx, pgsqlgen.ServerInOrganizationParams(p))
}

func (pgsqlQuerier) HasAnyServer(ctx context.Context, db backend.DBTX) (bool, error) {
	return pgsqlgen.New(db).HasAnyServer(ctx)
```

**Replace in** `internal/infra/db/repository/template/repo.go`:

```go
	GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error)
	ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error)
	MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) error
	SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) error
```
**with:**

```go
	GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error)
	ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error)
	ListLiveTemplateInstancesOnOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error)
	MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) error
	SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) error
```

**Replace in** `internal/infra/db/repository/template/repo.go`:

```go
// ListInstances returns the org's pending and created instances.
func (r *Repo) ListInstances(ctx context.Context, orgID string) ([]model.Instance, error) {
	rows, err := r.q.ListLiveTemplateInstances(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
```
**with:**

```go
// ListInstances returns the org's pending and created instances.
func (r *Repo) ListInstances(ctx context.Context, orgID string) ([]model.Instance, error) {
	return hydrateInstances(r.q.ListLiveTemplateInstances(ctx, r.tx.Querier(ctx), orgID))
}

// ListInstancesOnOrgServers returns the org's pending and created
// instances on the org's own servers.
func (r *Repo) ListInstancesOnOrgServers(ctx context.Context, orgID string) ([]model.Instance, error) {
	return hydrateInstances(r.q.ListLiveTemplateInstancesOnOrgServers(ctx, r.tx.Querier(ctx), orgID))
}

func hydrateInstances(rows []instanceRow, err error) ([]model.Instance, error) {
	if err != nil {
		return nil, err
```

**Replace in** `internal/infra/db/repository/template/sqlite.go`:

```go
}

func (sqliteQuerier) MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) error {
	return sqlitegen.New(db).MarkTemplateInstanceSaved(ctx, p)
```
**with:**

```go
}

func (sqliteQuerier) ListLiveTemplateInstancesOnOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	return sqlitegen.New(db).ListLiveTemplateInstancesOnOrgServers(ctx, orgID)
}

func (sqliteQuerier) MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) error {
	return sqlitegen.New(db).MarkTemplateInstanceSaved(ctx, p)
```

**Replace in** `internal/infra/db/repository/template/pgsql.go`:

```go

func (pgsqlQuerier) ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	rows, err := pgsqlgen.New(db).ListLiveTemplateInstances(ctx, orgID)
	if err != nil {
		return nil, err
```
**with:**

```go

func (pgsqlQuerier) ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	return instanceRows(pgsqlgen.New(db).ListLiveTemplateInstances(ctx, orgID))
}

func (pgsqlQuerier) ListLiveTemplateInstancesOnOrgServers(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	return instanceRows(pgsqlgen.New(db).ListLiveTemplateInstancesOnOrgServers(ctx, orgID))
}

func instanceRows(rows []pgsqlgen.TemplateInstance, err error) ([]instanceRow, error) {
	if err != nil {
		return nil, err
```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go

import (
	"errors"
	"net/http"
```
**with:**

```go

import (
	"context"
	"errors"
	"net/http"
```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go
const WaitBound = 10 * time.Minute

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
```
**with:**

```go
const WaitBound = 10 * time.Minute

// InstanceServers authorizes the instance routes (port.ServerRepository):
// the server must be one of the caller's organizations' and, beyond that,
// the organization the call acts for (the caller's primary one), so an
// instance never sits on another organization's server.
type InstanceServers interface {
	webutil.ServerAccess
	ServerInOrg(ctx context.Context, serverID, orgID string) (bool, error)
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go
	SharedVariables         tuc.SharedNames
	Users                   webutil.OrgResolver
	Servers                 webutil.ServerAccess
	CommandDispatcher       port.CommandDispatcher
	AppRepository           port.AppRepository
```
**with:**

```go
	SharedVariables         tuc.SharedNames
	Users                   webutil.OrgResolver
	Servers                 InstanceServers
	CommandDispatcher       port.CommandDispatcher
	AppRepository           port.AppRepository
```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go
	uc        *tuc.UseCase
	users     webutil.OrgResolver
	servers   webutil.ServerAccess
	log       *logger.Logger
	waitBound time.Duration
```
**with:**

```go
	uc        *tuc.UseCase
	users     webutil.OrgResolver
	servers   InstanceServers
	log       *logger.Logger
	waitBound time.Duration
```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go

// serverCaller resolves the caller and their org and requires serverID
// (400) owned by the caller's org (403).
func (h *Handler) serverCaller(w http.ResponseWriter, r *http.Request, serverID string) (string, string, bool) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok || !webutil.RequireOwnedServer(w, r, h.servers, userID, serverID) {
		return "", "", false
	}
```
**with:**

```go

// serverCaller resolves the caller and their org and requires serverID
// (400) owned by the caller's organizations and in the org the call acts
// for (403 either way; a lookup error denies too).
func (h *Handler) serverCaller(w http.ResponseWriter, r *http.Request, serverID string) (string, string, bool) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok || !webutil.RequireOwnedServer(w, r, h.servers, userID, serverID) {
		return "", "", false
	}
	if in, err := h.servers.ServerInOrg(r.Context(), serverID, orgID); err != nil || !in {
		webutil.Forbidden(w)
		return "", "", false
	}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal; go vet ./... && WF_CONTRACT_GUARD=require go test -count=1 ./...
```

Expected: no gofmt output; every package `ok` (the contract suite's `TestInstanceRoutesStayInTheCallersOrg`: the second org's server is 200 on get-apps, 403 on get-instances and create-instance; `TestForeignServer` unchanged).

Run:

```bash
make test-pg TEST_PG_ARGS='-run "TestOwnershipAndPresence|TestListInstancesOnOrgServers|TestInstanceRoutesStayInTheCallersOrg"'
```

Expected: `ok` on postgres:16.

- [ ] **Commit**

```bash
git add internal/infra/db internal/domain/port/server.go internal/domain/port/template.go internal/app/web/handler/templates internal/app/web/contract/instances_test.go
git commit -m "fix(templates): instance routes act on servers of the caller's org only

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: The status cache keeps each report's time and shared-variables revision

**Files:**
- Modify: `internal/domain/service/status/cache.go`, `internal/domain/service/status/cache_test.go`
- Modify: `internal/infra/bootstrap/events.go`, `internal/infra/bootstrap/events_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `status.Report{Apps []command.AppStatus; VarsRevision int64; HasRevision bool; Received time.Time}`; `(*status.Cache).SetAppReport(serverID string, apps []command.AppStatus, varsRev *int64, now time.Time) (changed, transition bool)` (`SetAppStatus` is it with `nil`); `(*status.Cache).LatestReport(serverID string, now time.Time) (status.Report, bool)` (false when absent or expired). The events sink records `apps.status` through `SetAppReport`.

The worker needs "a report received after the step" (health) and "a report whose `stale_vars` were computed against revision N" (reload settle); today the cache keeps neither.

- [ ] **Step 1: Write the failing tests**

**Replace in** `internal/domain/service/status/cache_test.go`:

```go
		t.Fatal("revision expired with liveness")
	}
}
```
**with:**

```go
		t.Fatal("revision expired with liveness")
	}
}

func TestLatestReportKeepsTimeAndRevision(t *testing.T) {
	c := NewCache(60 * time.Second)
	now := time.Unix(1000, 0)
	if _, ok := c.LatestReport("srv-1", now); ok {
		t.Fatal("a report before any arrived")
	}
	rev := int64(7)
	apps := []command.AppStatus{{AppID: "a1", StatusCode: command.ContainerStatusActive, StaleVars: []string{"SMTP_PASS"}}}
	c.SetAppReport("srv-1", apps, &rev, now)
	got, ok := c.LatestReport("srv-1", now.Add(10*time.Second))
	if !ok || !got.Received.Equal(now) || !got.HasRevision || got.VarsRevision != 7 || len(got.Apps) != 1 || got.Apps[0].StaleVars[0] != "SMTP_PASS" {
		t.Fatalf("report = %+v, %v", got, ok)
	}
	// An agent predating shared variables reports no revision.
	c.SetAppStatus("srv-1", apps, now.Add(20*time.Second))
	got, _ = c.LatestReport("srv-1", now.Add(20*time.Second))
	if got.HasRevision || !got.Received.Equal(now.Add(20*time.Second)) {
		t.Fatalf("report without a revision = %+v", got)
	}
	if _, ok := c.LatestReport("srv-1", now.Add(81*time.Second)); ok {
		t.Fatal("an expired report is still served")
	}
}
```

**Replace in** `internal/infra/bootstrap/events_test.go`:

```go
	ctx := context.Background()

	body, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{
		{AppID: "app-1", StatusCode: command.ContainerStatusActive},
	}})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventAppsStatus, Payload: body})

	// First report is both an apps_status and a liveness transition.
```
**with:**

```go
	ctx := context.Background()

	rev := int64(3)
	body, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{
		{AppID: "app-1", StatusCode: command.ContainerStatusActive},
	}, VarsRevision: &rev})
	sink.handle(ctx, bus.EventMessage{ServerID: "srv-1", Kind: bus.EventAppsStatus, Payload: body})
	// The cache keeps the report with its revision (the rollout worker
	// reads it).
	if rep, ok := sink.cache.LatestReport("srv-1", time.Now()); !ok || !rep.HasRevision || rep.VarsRevision != 3 || len(rep.Apps) != 1 {
		t.Fatalf("cached report = %+v, %v", rep, ok)
	}

	// First report is both an apps_status and a liveness transition.
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/domain/service/status/ ./internal/infra/bootstrap/
```

Expected: FAIL to compile: `c.SetAppReport undefined`, `sink.cache.LatestReport undefined`.

- [ ] **Step 2: Implement**

**Replace in** `internal/domain/service/status/cache.go`:

```go
	apps    []command.AppStatus
	expires time.Time
}

```
**with:**

```go
	apps    []command.AppStatus
	expires time.Time
	// received is when the report arrived; varsRev (hasRev) the
	// shared-variables revision its stale_vars were computed against.
	received time.Time
	varsRev  int64
	hasRev   bool
}

// Report is a server's latest apps.status as the API received it: the
// rollout worker waits for one received after a step, and for one whose
// stale_vars already reflect a shared-variable change.
type Report struct {
	Apps []command.AppStatus
	// VarsRevision is the agent's shared-variables revision when it built
	// the report; HasRevision is false for an agent predating shared
	// variables.
	VarsRevision int64
	HasRevision  bool
	Received     time.Time
}

```

**Replace in** `internal/domain/service/status/cache.go`:

```go
// as a liveness transition.
func (c *Cache) SetAppStatus(serverID string, apps []command.AppStatus, now time.Time) (changed, transition bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, had := c.apps[serverID]
	changed = !had || !reflect.DeepEqual(prev.apps, apps)
	c.apps[serverID] = appEntry{apps: apps, expires: now.Add(c.ttl)}
	// A status report is also a liveness signal.
	return changed, c.markOnlineLocked(serverID, now)
}

```
**with:**

```go
// as a liveness transition.
func (c *Cache) SetAppStatus(serverID string, apps []command.AppStatus, now time.Time) (changed, transition bool) {
	return c.SetAppReport(serverID, apps, nil, now)
}

// SetAppReport is SetAppStatus for a report that also carries the agent's
// shared-variables revision (nil: an agent predating shared variables).
func (c *Cache) SetAppReport(serverID string, apps []command.AppStatus, varsRev *int64, now time.Time) (changed, transition bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, had := c.apps[serverID]
	changed = !had || !reflect.DeepEqual(prev.apps, apps)
	e := appEntry{apps: apps, expires: now.Add(c.ttl), received: now}
	if varsRev != nil {
		e.varsRev, e.hasRev = *varsRev, true
	}
	c.apps[serverID] = e
	// A status report is also a liveness signal.
	return changed, c.markOnlineLocked(serverID, now)
}

// LatestReport returns the server's latest report; false when there is
// none or it expired.
func (c *Cache) LatestReport(serverID string, now time.Time) (Report, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.apps[serverID]
	if !ok || now.After(e.expires) {
		return Report{}, false
	}
	return Report{Apps: e.apps, VarsRevision: e.varsRev, HasRevision: e.hasRev, Received: e.received}, true
}

```

**Replace in** `internal/infra/bootstrap/events.go`:

```go
		}
		var changed bool
		changed, transition = s.cache.SetAppStatus(ev.ServerID, body.Apps, now)
		// The reporter pushes on a fixed timer; only actual changes are worth
		// a DB lookup + SSE fan-out. New clients seed from the cache on load.
```
**with:**

```go
		}
		var changed bool
		changed, transition = s.cache.SetAppReport(ev.ServerID, body.Apps, body.VarsRevision, now)
		// The reporter pushes on a fixed timer; only actual changes are worth
		// a DB lookup + SSE fan-out. New clients seed from the cache on load.
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test -count=1 ./internal/domain/service/status/ ./internal/infra/bootstrap/
```

Expected: both `ok`.

- [ ] **Commit**

```bash
git add internal/domain/service/status internal/infra/bootstrap/events.go internal/infra/bootstrap/events_test.go
git commit -m "feat(status): keep each report's receive time and vars revision

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Agent — the waitable single-app `vars.reload`

**Files:**
- Modify: `internal/domain/command/command.go`, `internal/domain/command/vars.go`, `internal/domain/command/backup_test.go`
- Modify: `internal/app/agent/reload_worker.go`, `internal/app/agent/reload_worker_test.go`
- Modify: `internal/app/agent/dispatcher.go`, `internal/app/agent/dispatcher_vars_test.go`

**Interfaces:**
- Consumes: the existing `ReloadWorker` (`Enqueue`, `Forget`, `Run`).
- Produces: `command.ReloadAppWait = 10 * time.Minute`; `command.ReloadVarsResponse{Queued bool; AppID string; Names []string; Skipped bool}` (the three new fields `omitempty`); `command.TypeVarsReload.Detached() == true`; `agent.ReloadOutcome{Names []string; Skipped bool; Err error}`; `(*ReloadWorker).EnqueueApp(appID string) <-chan ReloadOutcome`; the dispatcher answers `vars.reload` with exactly one app id with that app's outcome (an error response carrying the reload error naming the change, or success with `skipped`), at most `reloadWait` (default `command.ReloadAppWait`) later.

A wait registered while that app's reload is already running hears the next reload (the change it brings may need a newer snapshot); `Forget` (app deleted), a failed candidate lookup, an app not running at the lookup, and the worker stopping each end the wait.

- [ ] **Step 1: Write the failing tests**

**Replace in** `internal/domain/command/backup_test.go`:

```go

func TestDetached(t *testing.T) {
	if !TypeBackupRun.Detached() || !TypeBackupRestore.Detached() || TypeBackupPoints.Detached() || TypeAppSave.Detached() ||
		TypeStorageOrphans.Detached() {
		t.Fatal("only backup.run and backup.restore wait for long work")
	}
}
```
**with:**

```go

func TestDetached(t *testing.T) {
	if !TypeBackupRun.Detached() || !TypeBackupRestore.Detached() || !TypeVarsReload.Detached() ||
		TypeBackupPoints.Detached() || TypeAppSave.Detached() || TypeStorageOrphans.Detached() || TypeVarsSync.Detached() {
		t.Fatal("only backup.run, backup.restore and vars.reload wait for long work")
	}
}
```

**Replace in** `internal/app/agent/reload_worker_test.go`:

```go
// stale that has not been reloaded since it went stale", like the real
// stale filter; block, when set, holds each reload until released or the
// reload's context ends. Apps in skip report skipped (no longer running).
type fakeReloader struct {
	mu       sync.Mutex
```
**with:**

```go
// stale that has not been reloaded since it went stale", like the real
// stale filter; block, when set, holds each reload until released or the
// reload's context ends. Apps in skip report skipped (no longer running);
// apps in stopped are left out of an app-id lookup (not running).
type fakeReloader struct {
	mu       sync.Mutex
```

**Replace in** `internal/app/agent/reload_worker_test.go`:

```go
	failWith map[string]error
	skip     map[string]bool
	block    chan struct{}
	started  chan string
```
**with:**

```go
	failWith map[string]error
	skip     map[string]bool
	stopped  map[string]bool
	block    chan struct{}
	started  chan string
```

**Replace in** `internal/app/agent/reload_worker_test.go`:

```go
	defer f.mu.Unlock()
	if len(req.AppIDs) > 0 {
		return append([]string(nil), req.AppIDs...), nil
	}
	var out []string
```
**with:**

```go
	defer f.mu.Unlock()
	if len(req.AppIDs) > 0 {
		var out []string
		for _, id := range req.AppIDs {
			if !f.stopped[id] {
				out = append(out, id)
			}
		}
		return out, nil
	}
	var out []string
```

**Replace in** `internal/app/agent/reload_worker_test.go`:

```go
		t.Fatalf("reloads after cancel = %v", r)
	}
}
```
**with:**

```go
		t.Fatalf("reloads after cancel = %v", r)
	}
}

// awaitReload is the outcome a waited app's channel delivers.
func awaitReload(t *testing.T, ch <-chan ReloadOutcome) ReloadOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(3 * time.Second):
		t.Fatal("no outcome for the waited app")
		return ReloadOutcome{}
	}
}

func TestReloadWorkerEnqueueAppDeliversTheOutcome(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"SMTP_PASS"}, "b": {"S3_KEY"}, "c": {"X"}},
		failWith: map[string]error{"b": errors.New("compose up failed")},
		skip:     map[string]bool{"c": true}, stopped: map[string]bool{"d": true}}
	got := make(chan sinkCall, 8)
	w := NewReloadWorker(f, collectSink(got), testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	if o := awaitReload(t, w.EnqueueApp("a")); o.Err != nil || o.Skipped || len(o.Names) != 1 || o.Names[0] != "SMTP_PASS" {
		t.Fatalf("a = %+v", o)
	}
	if o := awaitReload(t, w.EnqueueApp("b")); o.Err == nil || !strings.Contains(o.Err.Error(), "S3_KEY") || !strings.Contains(o.Err.Error(), "compose up failed") {
		t.Fatalf("b = %+v", o)
	}
	// Stopped when its turn came (c), or not running at the lookup (d).
	if o := awaitReload(t, w.EnqueueApp("c")); o.Err != nil || !o.Skipped {
		t.Fatalf("c = %+v", o)
	}
	if o := awaitReload(t, w.EnqueueApp("d")); o.Err != nil || !o.Skipped {
		t.Fatalf("d = %+v", o)
	}
	// The reloads still report as vars.reloaded events (a and b; c and d
	// were skipped).
	if outs := awaitOutcomes(t, got, 2); outs[0].AppID != "a" || outs[1].AppID != "b" {
		t.Fatalf("events = %+v", outs)
	}
	w.mu.Lock()
	left := len(w.waiters)
	w.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d waiters left behind", left)
	}
}

func TestReloadWorkerWaitDuringAReloadTakesTheNextOne(t *testing.T) {
	f := &fakeReloader{stale: map[string][]string{"a": {"X"}}, block: make(chan struct{}), started: make(chan string, 4)}
	w := NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)

	first := w.EnqueueApp("a")
	<-f.started
	// A change arrives while a reloads: its wait is for a reload that
	// renders the newer snapshot, not the one in flight.
	second := w.EnqueueApp("a")
	f.block <- struct{}{}
	awaitReload(t, first)
	select {
	case o := <-second:
		t.Fatalf("the second wait took the first reload's outcome: %+v", o)
	case <-f.started:
	}
	f.block <- struct{}{}
	awaitReload(t, second)
	if r := f.reloaded(); len(r) != 2 {
		t.Fatalf("reloads = %v, want two", r)
	}
}

func TestReloadWorkerEndsWaitsOnForgetFailureAndStop(t *testing.T) {
	// Deleted before its turn.
	w := NewReloadWorker(&fakeReloader{}, func(bus.EventKind, []byte) error { return nil }, testLog())
	ch := w.EnqueueApp("a")
	w.Forget("a")
	if o := awaitReload(t, ch); !errors.Is(o.Err, errAppDeleted) {
		t.Fatalf("forgotten = %+v", o)
	}

	// The lookup fails (a hung docker ps).
	f := &fakeReloader{candBlock: make(chan struct{}), candGaveUp: make(chan error, 4)}
	w = NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	w.candidatesTimeout = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	if o := awaitReload(t, w.EnqueueApp("a")); !errors.Is(o.Err, context.DeadlineExceeded) {
		t.Fatalf("failed lookup = %+v", o)
	}

	// The worker stops while one waits behind a reload in flight.
	close(f.candBlock)
	f.stale = map[string][]string{"b": {"X"}}
	f.block, f.started = make(chan struct{}), make(chan string, 4)
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{"b"}})
	<-f.started
	waiting := w.EnqueueApp("c")
	cancel()
	if o := awaitReload(t, waiting); !errors.Is(o.Err, context.Canceled) {
		t.Fatalf("stopped = %+v", o)
	}
}
```

**Replace in** `internal/app/agent/dispatcher_vars_test.go`:

```go

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
```
**with:**

```go

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
```

**Replace in** `internal/app/agent/dispatcher_vars_test.go`:

```go
		t.Fatalf("apps.status = %s, %v", resp.Payload, err)
	}
}
```
**with:**

```go
		t.Fatalf("apps.status = %s, %v", resp.Payload, err)
	}
}

func TestDispatchVarsReloadOneAppAnswersItsOutcome(t *testing.T) {
	d := newTestDispatcher(t)
	f := &fakeReloader{stale: map[string][]string{"a": {"SMTP_PASS"}, "b": {"X"}},
		failWith: map[string]error{"b": errors.New("compose up failed")}, stopped: map[string]bool{"c": true}}
	w := NewReloadWorker(f, func(bus.EventKind, []byte) error { return nil }, testLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	d.WithReloads(w)

	resp := dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"a"}})
	var out command.ReloadVarsResponse
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS || json.Unmarshal(resp.Payload, &out) != nil ||
		out.AppID != "a" || out.Skipped || len(out.Names) != 1 || out.Names[0] != "SMTP_PASS" {
		t.Fatalf("reloaded = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"b"}})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "compose up failed") {
		t.Fatalf("failed reload = %+v", resp.Base)
	}
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"c"}})
	out = command.ReloadVarsResponse{}
	if resp.Base.ResponseCode != proto.ResponseCode_RESPONSE_CODE_SUCCESS || json.Unmarshal(resp.Payload, &out) != nil || !out.Skipped {
		t.Fatalf("stopped app = %+v %s", resp.Base, resp.Payload)
	}

	// A reload that outlives the bound answers the bound.
	f.block = make(chan struct{})
	f.stale["a"] = []string{"SMTP_PASS"}
	d.reloadWait = 50 * time.Millisecond
	resp = dispatchJSON(t, d, command.TypeVarsReload, command.ReloadVarsRequest{AppIDs: []string{"a"}})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "did not end within 50ms") {
		t.Fatalf("slow reload = %+v", resp.Base)
	}
	close(f.block)
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/domain/command/ ./internal/app/agent/
```

Expected: command: FAIL `only backup.run, backup.restore and vars.reload wait for long work`; agent: FAIL to compile (`unknown field stopped`, `w.EnqueueApp undefined`, `d.reloadWait undefined`).

- [ ] **Step 2: Implement**

**Replace in** `internal/domain/command/command.go`:

```go
// Detached reports whether a command's answer waits for long work that runs
// elsewhere (backup.run and backup.restore wait for their run on the backup
// scheduler). Such a command is dispatched off the standalone bridge's single
// goroutine, so it never holds up other commands.
func (t Type) Detached() bool { return t == TypeBackupRun || t == TypeBackupRestore }

// ContentTypeJSON is the envelope content type used for every command payload.
```
**with:**

```go
// Detached reports whether a command's answer waits for long work that runs
// elsewhere (backup.run and backup.restore wait for their run on the backup
// scheduler, a single-app vars.reload for its turn on the reload worker).
// Such a command is dispatched off the standalone bridge's single goroutine,
// so it never holds up other commands. The type decides, not the payload
// (the bridge has not decoded it): a bulk vars.reload answers at once either
// way.
func (t Type) Detached() bool {
	return t == TypeBackupRun || t == TypeBackupRestore || t == TypeVarsReload
}

// ContentTypeJSON is the envelope content type used for every command payload.
```

**Replace in** `internal/domain/command/vars.go`:

```go
import (
	"fmt"

	"winterflow/internal/domain/model"
```
**with:**

```go
import (
	"fmt"
	"time"

	"winterflow/internal/domain/model"
```

**Replace in** `internal/domain/command/vars.go`:

```go
}

// ReloadVarsRequest is vars.reload: re-render and `compose up -d` running
// apps. AppIDs selects those apps; else Names selects the stale apps whose
// stale names include one of them; neither = every stale running app.
type ReloadVarsRequest struct {
	Names  []string `json:"names,omitempty"`
```
**with:**

```go
}

// ReloadAppWait bounds a single-app vars.reload on the agent: the app's
// turn on the reload worker plus its reload (whose own deadline is also 10
// minutes, so a reload that starts late answers this bound's timeout).
const ReloadAppWait = 10 * time.Minute

// ReloadVarsRequest is vars.reload: re-render and `compose up -d` running
// apps. AppIDs selects those apps; else Names selects the stale apps whose
// stale names include one of them; neither = every stale running app.
// Exactly one app id is the waitable form: the answer comes when that
// app's reload ends (an error when it failed); any other form answers once
// queued and reports each app as a vars.reloaded event.
type ReloadVarsRequest struct {
	Names  []string `json:"names,omitempty"`
```

**Replace in** `internal/domain/command/vars.go`:

```go

// ReloadVarsResponse acknowledges a queued reload; each app's outcome
// arrives later as a vars.reloaded event.
type ReloadVarsResponse struct {
	Queued bool `json:"queued"`
}

```
**with:**

```go

// ReloadVarsResponse acknowledges a queued reload; each app's outcome
// arrives later as a vars.reloaded event. The single-app form answers when
// the app's reload ended: AppID, the names that were stale, and Skipped
// when the app was not running (nothing reloaded, nothing failed).
type ReloadVarsResponse struct {
	Queued  bool     `json:"queued"`
	AppID   string   `json:"app_id,omitempty"`
	Names   []string `json:"names,omitempty"`
	Skipped bool     `json:"skipped,omitempty"`
}

```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
	"context"
	"encoding/json"
	"sort"
	"strings"
```
**with:**

```go
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
const candidatesTimeout = 2 * time.Minute

// ReloadWorker performs vars.reload work on one goroutine, one app at a
// time, so the dispatcher (and standalone's single bridge goroutine) only
```
**with:**

```go
const candidatesTimeout = 2 * time.Minute

// errAppDeleted ends the wait of an app deleted before its reload ran.
var errAppDeleted = errors.New("the app was deleted")

// ReloadOutcome is one waited app's reload result (EnqueueApp). Skipped:
// the app was not running when its turn came, so nothing was reloaded and
// nothing failed; Err names the change that failed to apply.
type ReloadOutcome struct {
	Names   []string
	Skipped bool
	Err     error
}

// ReloadWorker performs vars.reload work on one goroutine, one app at a
// time, so the dispatcher (and standalone's single bridge goroutine) only
```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
	busy    bool
	dropped map[string]bool
}

```
**with:**

```go
	busy    bool
	dropped map[string]bool
	// waiters are the single-app requests waiting for an app's next
	// reload; a reload takes those registered before it starts.
	waiters map[string][]chan ReloadOutcome
}

```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
		r: r, sink: sink, log: log, timeout: reloadTimeout, candidatesTimeout: candidatesTimeout,
		wake: make(chan struct{}, 1), pending: newBatch(), dropped: map[string]bool{},
	}
}
```
**with:**

```go
		r: r, sink: sink, log: log, timeout: reloadTimeout, candidatesTimeout: candidatesTimeout,
		wake: make(chan struct{}, 1), pending: newBatch(), dropped: map[string]bool{},
		waiters: map[string][]chan ReloadOutcome{},
	}
}
```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
}

// Forget drops a deleted app's queued work.
func (w *ReloadWorker) Forget(appID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.pending.apps, appID)
	if w.busy {
		w.dropped[appID] = true
	}
}

// Run processes work until ctx is done.
func (w *ReloadWorker) Run(ctx context.Context) {
	for {
		select {
```
**with:**

```go
}

// EnqueueApp queues appID like Enqueue with that one app id and returns the
// channel its outcome arrives on, once: the outcome of the app's next
// reload, a skip when it is not running then, or an error when it is
// deleted first or the worker stops. It never blocks.
func (w *ReloadWorker) EnqueueApp(appID string) <-chan ReloadOutcome {
	ch := make(chan ReloadOutcome, 1)
	w.mu.Lock()
	w.waiters[appID] = append(w.waiters[appID], ch)
	w.mu.Unlock()
	w.Enqueue(command.ReloadVarsRequest{AppIDs: []string{appID}})
	return ch
}

// takeWaiters removes and returns appID's waiters.
func (w *ReloadWorker) takeWaiters(appID string) []chan ReloadOutcome {
	w.mu.Lock()
	defer w.mu.Unlock()
	ws := w.waiters[appID]
	delete(w.waiters, appID)
	return ws
}

// deliver sends o to each waiter (buffered, each used once: never blocks).
func deliver(ws []chan ReloadOutcome, o ReloadOutcome) {
	for _, ch := range ws {
		ch <- o
	}
}

// Forget drops a deleted app's queued work and ends its waits.
func (w *ReloadWorker) Forget(appID string) {
	w.mu.Lock()
	delete(w.pending.apps, appID)
	if w.busy {
		w.dropped[appID] = true
	}
	w.mu.Unlock()
	deliver(w.takeWaiters(appID), ReloadOutcome{Err: errAppDeleted})
}

// Run processes work until ctx is done; then every wait still open ends
// with ctx's error.
func (w *ReloadWorker) Run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		all := w.waiters
		w.waiters = map[string][]chan ReloadOutcome{}
		w.mu.Unlock()
		for _, ws := range all {
			deliver(ws, ReloadOutcome{Err: ctx.Err()})
		}
	}()
	for {
		select {
```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
		if err != nil {
			// No app to attribute an outcome to: the request is dropped
			// and the apps stay stale until the next vars.reload.
			w.log.Warn("shared variable reload: list candidates failed, request dropped",
				"names", req.Names, "app_ids", req.AppIDs, "error", err)
			continue
		}
```
**with:**

```go
		if err != nil {
			// No app to attribute an outcome to: the request is dropped
			// and the apps stay stale until the next vars.reload. A waited
			// app hears why.
			w.log.Warn("shared variable reload: list candidates failed, request dropped",
				"names", req.Names, "app_ids", req.AppIDs, "error", err)
			for _, id := range req.AppIDs {
				seen[id] = true
				deliver(w.takeWaiters(id), ReloadOutcome{Err: err})
			}
			continue
		}
```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
		}
	}
	return out
}
```
**with:**

```go
		}
	}
	// An explicit app the lookup left out is not running (or gone): a
	// waited one is skipped.
	for id := range b.apps {
		if !seen[id] {
			deliver(w.takeWaiters(id), ReloadOutcome{Skipped: true})
		}
	}
	return out
}
```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
// it neither reloaded nor failed, and its next start renders anyway.
func (w *ReloadWorker) reloadOne(ctx context.Context, appID string) {
	actx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	names, skipped, err := w.r.ReloadSharedVars(actx, appID)
	if ctx.Err() != nil {
		return // shutting down: no outcome to report
	}
	if skipped && err == nil {
		w.log.Debug("shared variable reload: app no longer running, skipped", "app_id", appID)
		return
	}
```
**with:**

```go
// it neither reloaded nor failed, and its next start renders anyway.
func (w *ReloadWorker) reloadOne(ctx context.Context, appID string) {
	// The waits registered before this reload starts hear its outcome; a
	// later one waits for the next reload (it may need a newer snapshot).
	waiting := w.takeWaiters(appID)
	actx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	names, skipped, err := w.r.ReloadSharedVars(actx, appID)
	if ctx.Err() != nil {
		deliver(waiting, ReloadOutcome{Names: names, Err: ctx.Err()})
		return // shutting down: no outcome to report
	}
	if skipped && err == nil {
		w.log.Debug("shared variable reload: app no longer running, skipped", "app_id", appID)
		deliver(waiting, ReloadOutcome{Names: names, Skipped: true})
		return
	}
```

**Replace in** `internal/app/agent/reload_worker.go`:

```go
		p.Names = []string{}
	}
	if err != nil {
		p.Error = reloadError(names, err)
		w.log.Warn("shared variable reload failed", "app_id", appID, "error", err)
	}
	body, mErr := json.Marshal(p)
	if mErr != nil {
```
**with:**

```go
		p.Names = []string{}
	}
	out := ReloadOutcome{Names: names}
	if err != nil {
		p.Error = reloadError(names, err)
		out.Err = errors.New(p.Error)
		w.log.Warn("shared variable reload failed", "app_id", appID, "error", err)
	}
	deliver(waiting, out)
	body, mErr := json.Marshal(p)
	if mErr != nil {
```

**Replace in** `internal/app/agent/dispatcher.go`:

```go
// one registration line in newHandlers plus the orchestrator method.
type Dispatcher struct {
	orch     *dockercompose.Repository
	ingress  port.IngressManager
	reloads  *ReloadWorker
	backups  Backups
	runWait  time.Duration // bound of a backup.run / backup.restore
	log      *logger.Logger
	handlers map[command.Type]handlerFunc
}

```
**with:**

```go
// one registration line in newHandlers plus the orchestrator method.
type Dispatcher struct {
	orch    *dockercompose.Repository
	ingress port.IngressManager
	reloads *ReloadWorker
	backups Backups
	runWait time.Duration // bound of a backup.run / backup.restore
	// reloadWait bounds a single-app vars.reload.
	reloadWait time.Duration
	log        *logger.Logger
	handlers   map[command.Type]handlerFunc
}

```

**Replace in** `internal/app/agent/dispatcher.go`:

```go
		ingress = noIngress{}
	}
	d := &Dispatcher{orch: orch, ingress: ingress, backups: noBackups{}, runWait: command.BackupRunWait, log: log}
	d.handlers = d.newHandlers()
	return d
```
**with:**

```go
		ingress = noIngress{}
	}
	d := &Dispatcher{orch: orch, ingress: ingress, backups: noBackups{}, runWait: command.BackupRunWait,
		reloadWait: command.ReloadAppWait, log: log}
	d.handlers = d.newHandlers()
	return d
```

**Replace in** `internal/app/agent/dispatcher.go`:

```go
			return d.orch.SyncVars(ctx, in)
		}),
		command.TypeVarsReload: handle(d, func(_ context.Context, in command.ReloadVarsRequest) (command.ReloadVarsResponse, error) {
			if d.reloads == nil {
				return command.ReloadVarsResponse{}, errNoReloads
			}
			d.reloads.Enqueue(in)
			return command.ReloadVarsResponse{Queued: true}, nil
		}),
		command.TypeStorageList: handle(d, func(ctx context.Context, in command.ListStoragesRequest) (command.ListStoragesResponse, error) {
			list, err := d.orch.ListStorages(ctx, !in.RegistryOnly)
```
**with:**

```go
			return d.orch.SyncVars(ctx, in)
		}),
		command.TypeVarsReload: handle(d, d.reloadVars),
		command.TypeStorageList: handle(d, func(ctx context.Context, in command.ListStoragesRequest) (command.ListStoragesResponse, error) {
			list, err := d.orch.ListStorages(ctx, !in.RegistryOnly)
```

**Replace in** `internal/app/agent/dispatcher.go`:

```go
		proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, detail, nil)
	return resp
}
```
**with:**

```go
		proto.ResponseCode_RESPONSE_CODE_SERVER_ERROR, detail, nil)
	return resp
}

// reloadVars queues a vars.reload. The bulk form answers once queued; the
// single-app form (exactly one app id) answers with that app's outcome, at
// most reloadWait later. vars.reload is a detached command, so the wait
// never holds up the command pipeline.
func (d *Dispatcher) reloadVars(ctx context.Context, in command.ReloadVarsRequest) (command.ReloadVarsResponse, error) {
	if d.reloads == nil {
		return command.ReloadVarsResponse{}, errNoReloads
	}
	if len(in.AppIDs) != 1 {
		d.reloads.Enqueue(in)
		return command.ReloadVarsResponse{Queued: true}, nil
	}
	appID := in.AppIDs[0]
	resp := command.ReloadVarsResponse{Queued: true, AppID: appID}
	ctx, cancel := context.WithTimeout(ctx, d.reloadWait)
	defer cancel()
	select {
	case o := <-d.reloads.EnqueueApp(appID):
		resp.Names, resp.Skipped = o.Names, o.Skipped
		return resp, o.Err
	case <-ctx.Done():
		return resp, fmt.Errorf("the reload of app %s did not end within %s", appID, d.reloadWait)
	}
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal; go vet ./internal/app/agent/ ./internal/domain/command/ && go test -race -count=3 ./internal/app/agent/ ./internal/domain/command/ ./internal/infra/transport/grpc/agent/
```

Expected: no gofmt output; all `ok` three times under `-race` (the gRPC agent package already runs detached commands on the agent's lifetime and replies on the current stream; it needs no change).

- [ ] **Commit**

```bash
git add internal/domain/command internal/app/agent/reload_worker.go internal/app/agent/reload_worker_test.go internal/app/agent/dispatcher.go internal/app/agent/dispatcher_vars_test.go
git commit -m "feat(agent): vars.reload of one app answers its outcome (detached)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: The rollout queue and its worker

**Files:**
- Modify: `internal/domain/model/notification.go`
- Create: `internal/domain/usecase/rollouts/usecase.go`, `worker.go`, `wait.go`
- Test: `internal/domain/usecase/rollouts/fixture_test.go`, `worker_test.go`

**Interfaces:**
- Consumes: `port.RolloutRepository`, `model.Rollout*` (Task 1); `status.Report`, `LatestReport` (Task 3); `port.TemplateRepository.GetTemplate`; `templates.UpdateInput`, `templates.Outcome` (D1a).
- Produces:
  - `model.NotificationRollout = "rollout"`.
  - `rollouts.Deps{Repo port.RolloutRepository; Templates port.TemplateRepository; Instances InstanceUpgrader; Dispatcher port.CommandDispatcher; Status Reports; Apps AppNames; Members Members; Notifications Publisher; Log *logger.Logger}`; `rollouts.New(Deps) *UseCase`; `(*UseCase).Wake()`, `Run(ctx)`, `List(ctx, orgID, kind, scopeKey string) ([]model.RolloutView, error)`, `Stop(ctx, orgID, id string) (model.RolloutView, error)`.
  - package-internal, used by Tasks 6 and 8: `runner` (`next(ctx, r) (pick, error)`, `run(ctx, r, pick) stepResult`), `pick{appID, serverID, skipID string; remaining int64; wait bool; inst model.Instance; revSeen int64}`, `done()`, `skipped()`, `failed(msg)`, `errBadScope`, `(*UseCase).queue(ctx, r) (model.RolloutView, bool, error)`, `awaitResult(ctx, <-chan port.CommandResult) (port.CommandResult, error)`, `awaitRunning(ctx, serverID, appID string, since time.Time) (command.AppStatus, error)`, the map `uc.kinds` (empty here).

The worker in one paragraph: `Run` makes a pass now, on every `Wake` and once a minute; a pass lists claimable rollouts (one indexed query) and claims each with the spec's single `UPDATE`; `drive` takes steps while a `keepLease` goroutine renews the lease every 30 s and cancels the drive when a renewal finds the lease gone; a `step` stops on `stop_requested`, asks the kind for the next app (`wait`: sleep 5 s and look again; none: `Complete` at the evaluated `scope_rev`, or look again when it was extended), records it (`StartStep`, an event), runs it, and counts it, records the skip or fails the rollout. `ended` announces the end with a context detached from the drive (the renewal cancels the drive as soon as it sees the rollout ended) and prunes the scope to 20 ended rollouts.

- [ ] **Step 1: The event type**

In `internal/domain/model/notification.go`:

**Replace in** `internal/domain/model/notification.go`:

```go
	// (Payload is command.VarsReloadedPayload).
	NotificationVarsReloaded NotificationType = "vars.reloaded"
)

```
**with:**

```go
	// (Payload is command.VarsReloadedPayload).
	NotificationVarsReloaded NotificationType = "vars.reloaded"
	// NotificationRollout is a rollout's progress or end, to the org,
	// whenever its counts or state change (Payload is RolloutView).
	NotificationRollout NotificationType = "rollout"
)

```
- [ ] **Step 2: Write the failing tests** (a fake kind drives the worker over the real SQLite repository)

**Create** `internal/domain/usecase/rollouts/fixture_test.go`:

```go
package rollouts

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
	rolloutrepo "winterflow/internal/infra/db/repository/rollout"
	templaterepo "winterflow/internal/infra/db/repository/template"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	members    = []string{"u1", "u2"}
)

// fakeMembers: every org has members u1 and u2.
type fakeMembers struct{}

func (fakeMembers) ListMembers(context.Context, string) ([]model.Member, error) {
	out := []model.Member{}
	for _, id := range members {
		out = append(out, model.Member{User: model.User{ID: id}})
	}
	return out, nil
}

// fakePublisher records rollout events (u1's copy of each).
type fakePublisher struct {
	mu   sync.Mutex
	seen []model.RolloutView
	n    int
}

func (f *fakePublisher) Publish(userID string, n model.Notification) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if v, ok := n.Payload.(model.RolloutView); ok && n.Type == model.NotificationRollout && userID == "u1" {
		f.seen = append(f.seen, v)
	}
}

func (f *fakePublisher) events() []model.RolloutView {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// fakeReports is a settable status cache.
type fakeReports struct {
	mu      sync.Mutex
	reports map[string]status.Report
}

func (f *fakeReports) set(serverID string, rep status.Report) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports[serverID] = rep
}

func (f *fakeReports) LatestReport(serverID string, _ time.Time) (status.Report, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rep, ok := f.reports[serverID]
	return rep, ok
}

// env is the usecase over a real database with fakes around it and short
// bounds.
type env struct {
	uc      *UseCase
	deps    Deps
	d       *db.DB
	repo    *rolloutrepo.Repo
	tpl     *templaterepo.Repo
	pub     *fakePublisher
	reports *fakeReports
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	repo, err := rolloutrepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := templaterepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{d: d, repo: repo, tpl: tpl, pub: &fakePublisher{}, reports: &fakeReports{reports: map[string]status.Report{}}}
	e.deps = Deps{Repo: repo, Templates: tpl, Status: e.reports, Members: fakeMembers{}, Notifications: e.pub, Log: dbtest.Log}
	e.uc = e.newUseCase()
	return e
}

// newUseCase is another API process on the same database.
func (e *env) newUseCase() *UseCase {
	uc := New(e.deps)
	uc.renew, uc.idle, uc.settlePoll, uc.healthPoll = 10*time.Millisecond, time.Hour, 10*time.Millisecond, 5*time.Millisecond
	uc.health, uc.settle = 200*time.Millisecond, 200*time.Millisecond
	return uc
}

// get reads a rollout.
func (e *env) get(t *testing.T, id string) model.Rollout {
	t.Helper()
	r, err := e.repo.GetRollout(dbtest.Ctx(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// eventually polls cond for up to 5 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 5s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

**Create** `internal/domain/usecase/rollouts/worker_test.go`:

```go
package rollouts

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/infra/db/dbtest"
)

// fakeKind steps through todo in order: run answers results[app] (done
// by default, which takes the app off todo) and, for an app in block,
// first waits for the channel or the step's context.
type fakeKind struct {
	mu      sync.Mutex
	todo    []string
	results map[string]stepResult
	block   map[string]chan struct{}
	ran     []string
	gaveUp  chan error
}

func (f *fakeKind) next(_ context.Context, r model.Rollout) (pick, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var left []string
	for _, id := range f.todo {
		if !slices.Contains(r.SkipIDs, id) {
			left = append(left, id)
		}
	}
	if len(left) == 0 {
		return pick{}, nil
	}
	return pick{appID: left[0], serverID: srv1, skipID: left[0], remaining: int64(len(left))}, nil
}

func (f *fakeKind) run(ctx context.Context, _ model.Rollout, p pick) stepResult {
	f.mu.Lock()
	ch := f.block[p.appID]
	f.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			f.gaveUp <- ctx.Err()
			return failed("cancelled")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, p.appID)
	res, ok := f.results[p.appID]
	if !ok {
		res = done()
	}
	if res.outcome == stepDone {
		f.todo = slices.DeleteFunc(f.todo, func(id string) bool { return id == p.appID })
	}
	return res
}

func (f *fakeKind) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ran)
}

// queueFake queues a rollout of the fake kind for scope.
func (e *env) queueFake(t *testing.T, n int, scope string) string {
	t.Helper()
	id := dbtest.ID(100 + n)
	if _, ok, err := e.uc.queue(dbtest.Ctx(t), model.Rollout{ID: id, OrgID: org1, Kind: "fake", Scope: []byte(`{}`), ScopeKey: scope, CreatedBy: "u1"}); err != nil || !ok {
		t.Fatalf("queue: %v %v", ok, err)
	}
	return id
}

func TestPassStepsThroughToDone(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{todo: []string{"a1", "a2", "a3"}, results: map[string]stepResult{"a2": skipped()}}
	e.uc.kinds["fake"] = k
	id := e.queueFake(t, 1, "s")
	e.uc.pass(dbtest.Ctx(t))

	r := e.get(t, id)
	if r.State != model.RolloutDone || r.Done != 2 || r.Skipped != 1 || r.Total != 3 || !slices.Equal(r.SkipIDs, []string{"a2"}) ||
		r.CurrentAppID != "" || r.LeaseOwner != "" || r.FinishedAt == nil {
		t.Fatalf("rollout = %+v", r)
	}
	if got := k.runs(); !slices.Equal(got, []string{"a1", "a2", "a3"}) {
		t.Fatalf("runs = %v", got)
	}
	evs := e.pub.events()
	if len(evs) < 3 || evs[0].State != "queued" || evs[len(evs)-1].State != "done" || evs[len(evs)-1].Done != 2 {
		t.Fatalf("events = %+v", evs)
	}
	// Each event reached every member; a step in flight names its app.
	if e.pub.n != len(evs)*len(members) || !slices.ContainsFunc(evs, func(v model.RolloutView) bool {
		return v.State == "running" && v.CurrentAppID == "a3" && v.Total == 3 && v.Done == 1 && v.Skipped == 1
	}) {
		t.Fatalf("events = %+v (%d sent)", evs, e.pub.n)
	}
}

func TestFirstFailureEndsTheRollout(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{todo: []string{"a1", "a2", "a3"}, results: map[string]stepResult{"a2": failed("compose up failed")}}
	e.uc.kinds["fake"] = k
	id := e.queueFake(t, 1, "s")
	e.uc.pass(dbtest.Ctx(t))

	r := e.get(t, id)
	if r.State != model.RolloutFailed || r.Error != "compose up failed" || r.CurrentAppID != "a2" || r.CurrentServerID != srv1 || r.Done != 1 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := k.runs(); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Fatalf("runs = %v: nothing may follow a failure", got)
	}
}

func TestStopEndsAfterTheStepInFlight(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	k := &fakeKind{todo: []string{"a1", "a2"}, block: map[string]chan struct{}{"a1": release}}
	e.uc.kinds["fake"] = k
	id := e.queueFake(t, 1, "s")
	passed := make(chan struct{})
	go func() { e.uc.pass(context.Background()); close(passed) }()
	eventually(t, "a1 in flight", func() bool { return e.get(t, id).CurrentAppID == "a1" })

	ctx := dbtest.Ctx(t)
	if _, err := e.uc.Stop(ctx, org2, id); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("another org's stop: %v", err)
	}
	v, err := e.uc.Stop(ctx, org1, id)
	if err != nil || !v.StopRequested || v.State != "running" {
		t.Fatalf("stop = %+v, %v", v, err)
	}
	close(release)
	<-passed
	r := e.get(t, id)
	if r.State != model.RolloutStopped || r.Done != 1 || r.CurrentAppID != "" {
		t.Fatalf("rollout = %+v", r)
	}
	if got := k.runs(); !slices.Equal(got, []string{"a1"}) {
		t.Fatalf("runs = %v", got)
	}
	// Stopping an ended rollout answers it as it is.
	if v, err := e.uc.Stop(ctx, org1, id); err != nil || v.State != "stopped" {
		t.Fatalf("stop again = %+v, %v", v, err)
	}
}

func TestAnotherProcessTakesOverAnExpiredLease(t *testing.T) {
	e := newEnv(t)
	t0 := time.Now().UTC().Truncate(time.Second)
	// Process 1 (its clock stuck at t0) holds a1 forever.
	k1 := &fakeKind{todo: []string{"a1", "a2"}, block: map[string]chan struct{}{"a1": make(chan struct{})}, gaveUp: make(chan error, 1)}
	e.uc.kinds["fake"] = k1
	e.uc.now = func() time.Time { return t0 }
	id := e.queueFake(t, 1, "s")
	go e.uc.pass(context.Background())
	eventually(t, "a1 in flight", func() bool { return e.get(t, id).CurrentAppID == "a1" })

	// Process 2 runs past process 1's lease: it claims the rollout and
	// redoes a1, and process 1's next renewal finds the lease gone and
	// abandons its step without counting it.
	uc2 := e.newUseCase()
	k2 := &fakeKind{todo: []string{"a1", "a2"}}
	uc2.kinds["fake"] = k2
	uc2.now = func() time.Time { return t0.Add(5 * time.Minute) }
	uc2.pass(dbtest.Ctx(t))
	select {
	case err := <-k1.gaveUp:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("process 1's step ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process 1 kept its step after losing the lease")
	}
	r := e.get(t, id)
	if r.State != model.RolloutDone || r.Done != 2 || len(k1.runs()) != 0 || !slices.Equal(k2.runs(), []string{"a1", "a2"}) {
		t.Fatalf("rollout = %+v; runs 1 = %v, 2 = %v", r, k1.runs(), k2.runs())
	}
}

func TestUnknownKindFails(t *testing.T) {
	e := newEnv(t)
	id := e.queueFake(t, 1, "s") // no runner for "fake"
	e.uc.pass(dbtest.Ctx(t))
	if r := e.get(t, id); r.State != model.RolloutFailed || r.Error != "unknown rollout kind fake" {
		t.Fatalf("rollout = %+v", r)
	}
}

func TestEndedRolloutsArePrunedPerScope(t *testing.T) {
	e := newEnv(t)
	e.uc.kinds["fake"] = &fakeKind{}
	for n := 1; n <= keepEnded+2; n++ {
		e.queueFake(t, n, "s")
		e.uc.pass(dbtest.Ctx(t))
	}
	e.queueFake(t, 50, "other")
	e.uc.pass(dbtest.Ctx(t))
	list, err := e.repo.ListRollouts(dbtest.Ctx(t), org1, "fake", "s")
	if err != nil || len(list) != keepEnded {
		t.Fatalf("scope s keeps %d, %v", len(list), err)
	}
	if other, _ := e.repo.ListRollouts(dbtest.Ctx(t), org1, "fake", "other"); len(other) != 1 {
		t.Fatalf("scope other = %+v", other)
	}
}

// countingRepo counts the worker's idle query.
type countingRepo struct {
	port.RolloutRepository
	n atomic.Int64
}

func (c *countingRepo) ListClaimable(ctx context.Context, now time.Time) ([]string, error) {
	c.n.Add(1)
	return c.RolloutRepository.ListClaimable(ctx, now)
}

func TestIdleWorkerQueriesOnlyOnTickOrWake(t *testing.T) {
	e := newEnv(t)
	counter := &countingRepo{RolloutRepository: e.repo}
	e.uc.d.Repo = counter
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.uc.Run(ctx)
	eventually(t, "the first pass", func() bool { return counter.n.Load() == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := counter.n.Load(); n != 1 {
		t.Fatalf("an idle worker queried %d times before its tick", n)
	}
	e.uc.Wake()
	eventually(t, "the woken pass", func() bool { return counter.n.Load() == 2 })
}

func TestListValidatesKindAndScope(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.List(ctx, org1, "bogus", ""); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := e.uc.List(ctx, org1, model.RolloutVarsReload, org2); !errors.Is(err, model.ErrRolloutNotFound) {
		t.Fatalf("another org as scope: %v", err)
	}
	if _, err := e.uc.List(ctx, org1, model.RolloutTemplateUpgrade, dbtest.ID(77)); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("unknown template: %v", err)
	}
	if _, ok, err := e.uc.queue(ctx, model.Rollout{ID: dbtest.ID(201), OrgID: org1, Kind: model.RolloutVarsReload, Scope: []byte(`{}`), ScopeKey: org1}); err != nil || !ok {
		t.Fatalf("queue: %v %v", ok, err)
	}
	if list, err := e.uc.List(ctx, org1, model.RolloutVarsReload, ""); err != nil || len(list) != 1 || list[0].ScopeKey != org1 || list[0].State != "queued" {
		t.Fatalf("org1 list = %+v, %v", list, err)
	}
	if list, err := e.uc.List(ctx, org2, model.RolloutVarsReload, ""); err != nil || len(list) != 0 {
		t.Fatalf("org2 list = %+v, %v", list, err)
	}
}

func TestAwaitRunning(t *testing.T) {
	e := newEnv(t)
	since := time.Now()
	ctx := dbtest.Ctx(t)
	app := func(code command.ContainerStatusCode) []command.AppStatus {
		return []command.AppStatus{{AppID: "a1", StatusCode: code}}
	}
	// A report from before the step does not count; a later Active does.
	e.reports.set(srv1, status.Report{Apps: app(command.ContainerStatusActive), Received: since.Add(-time.Second)})
	go func() {
		time.Sleep(30 * time.Millisecond)
		e.reports.set(srv1, status.Report{Apps: app(command.ContainerStatusActive), Received: since.Add(time.Second)})
	}()
	if _, err := e.uc.awaitRunning(ctx, srv1, "a1", since); err != nil {
		t.Fatalf("active: %v", err)
	}
	cases := []struct {
		apps []command.AppStatus
		want string
	}{
		{app(command.ContainerStatusIdle), ""},
		{app(command.ContainerStatusRestarting), "not running 200ms later (restarting)"},
		{app(command.ContainerStatusProblematic), "a container exited with an error"},
		{nil, "the app has no containers"},
	}
	for _, tc := range cases {
		e.reports.set(srv1, status.Report{Apps: tc.apps, Received: since.Add(time.Second)})
		_, err := e.uc.awaitRunning(ctx, srv1, "a1", since)
		if (tc.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%+v: %v, want %q", tc.apps, err, tc.want)
		}
	}
	if _, err := e.uc.awaitRunning(ctx, srv2, "a1", since); err == nil || !strings.Contains(err.Error(), "no status report") {
		t.Fatalf("silent server: %v", err)
	}
}

func TestAwaitResultBounds(t *testing.T) {
	e := newEnv(t)
	e.uc.cmdWait = 20 * time.Millisecond
	ch := make(chan port.CommandResult, 1)
	if _, err := e.uc.awaitResult(dbtest.Ctx(t), ch); err == nil || err.Error() != "no answer from the server within 20ms" {
		t.Fatalf("silent agent: %v", err)
	}
	ch <- port.CommandResult{}
	if _, err := e.uc.awaitResult(dbtest.Ctx(t), ch); err == nil || err.Error() != "the agent reported a failure" {
		t.Fatalf("failure without a message: %v", err)
	}
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/domain/usecase/rollouts/
```

Expected: FAIL to compile: `undefined: UseCase`, `undefined: Deps`.

- [ ] **Step 3: Implement**

**Create** `internal/domain/usecase/rollouts/usecase.go`:

```go
// Package rollouts is the rollout queue: queued, resumable,
// one-app-at-a-time jobs (upgrade a template's instances, reload the apps a
// shared-variable change made stale). One worker goroutine per API process
// claims a rollout with a lease, takes its steps and renews the lease
// meanwhile; another process takes over a rollout whose lease expired. No
// transaction is held while a step waits on an agent.
package rollouts

import (
	"context"
	"encoding/json"
	"time"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
	"winterflow/pkg/util"
)

// Bounds. Every wait of a step is bounded; the idle worker costs one
// indexed query a minute.
const (
	// leaseFor is how long a claim (or renewal) holds a rollout.
	leaseFor = 2 * time.Minute
	// renewEvery renews the lease well inside it.
	renewEvery = 30 * time.Second
	// idleTick is how often the idle worker looks for claimable work.
	idleTick = time.Minute
	// commandWait bounds a step's command (an image pull, a reload); the
	// agent's own bound is the same 10 minutes, so its timeout answer
	// arrives first.
	commandWait = 10*time.Minute + 30*time.Second
	// healthWait bounds the wait for the app to report running.
	healthWait = 5 * time.Minute
	// healthPoll re-reads the status cache (memory only) while waiting.
	healthPoll = time.Second
	// settleWait is how long a vars_reload waits for a server to report
	// the change it was pushed; settlePoll how often it looks.
	settleWait = 5 * time.Minute
	settlePoll = 5 * time.Second
	// keepEnded is how many ended rollouts a scope keeps.
	keepEnded = 20
	// notifyTimeout bounds the reads a notification needs.
	notifyTimeout = 30 * time.Second
)

// InstanceUpgrader re-renders an instance at another revision and
// dispatches its save (*templates.UseCase).
type InstanceUpgrader interface {
	UpdateInstance(ctx context.Context, userID, orgID string, in tuc.UpdateInput) (tuc.Outcome, error)
}

// Reports is the status cache as the worker reads it (*status.Cache).
type Reports interface {
	LatestReport(serverID string, now time.Time) (status.Report, bool)
}

// AppNames names apps in views (port.AppRepository satisfies it).
type AppNames interface {
	GetApp(ctx context.Context, appID string) (model.App, error)
}

// Members lists an org's users, the audience of rollout events
// (port.UserService satisfies it).
type Members interface {
	ListMembers(ctx context.Context, orgID string) ([]model.Member, error)
}

// Publisher delivers one SSE notification to a user.
type Publisher interface {
	Publish(userID string, n model.Notification)
}

// Deps wires the usecase. With zero Deps (route documentation) it is never
// called.
type Deps struct {
	Repo          port.RolloutRepository
	Templates     port.TemplateRepository
	Instances     InstanceUpgrader
	Dispatcher    port.CommandDispatcher
	Status        Reports
	Apps          AppNames
	Members       Members
	Notifications Publisher
	Log           *logger.Logger
}

// UseCase queues, lists and stops rollouts, and drives them (Run). Build
// one per API process and share it: Wake reaches the worker only through
// the same value.
type UseCase struct {
	d     Deps
	owner string
	wake  chan struct{}
	kinds map[string]runner
	now   func() time.Time

	// Bounds, shortened by tests.
	lease, renew, idle, cmdWait, health, healthPoll, settle, settlePoll time.Duration
}

// New builds the usecase; Run starts its worker.
func New(d Deps) *UseCase {
	uc := &UseCase{
		d: d, owner: util.GenerateID(), wake: make(chan struct{}, 1), now: time.Now,
		lease: leaseFor, renew: renewEvery, idle: idleTick, cmdWait: commandWait,
		health: healthWait, healthPoll: healthPoll, settle: settleWait, settlePoll: settlePoll,
	}
	uc.kinds = map[string]runner{}
	return uc
}

// Wake makes the worker look for work now. It never blocks.
func (uc *UseCase) Wake() {
	select {
	case uc.wake <- struct{}{}:
	default:
	}
}

// queue inserts r (queued) and tells the worker and the org; false when
// its scope already has an active rollout. The answer is read before the
// worker is woken, so it shows the rollout queued.
func (uc *UseCase) queue(ctx context.Context, r model.Rollout) (model.RolloutView, bool, error) {
	ok, err := uc.d.Repo.InsertRollout(ctx, r)
	if err != nil || !ok {
		return model.RolloutView{}, ok, err
	}
	stored, err := uc.d.Repo.GetRollout(ctx, r.ID)
	uc.Wake()
	if err != nil {
		return model.RolloutView{}, true, err
	}
	v := uc.view(ctx, stored)
	uc.publish(ctx, stored.OrgID, v)
	return v, true, nil
}

// List returns an org's rollouts of kind: the active ones first, then the
// ended ones newest first (scopeKey narrows to one scope). Another org's
// template, or another org as a vars_reload scope, is not found.
func (uc *UseCase) List(ctx context.Context, orgID, kind, scopeKey string) ([]model.RolloutView, error) {
	switch {
	case kind != model.RolloutTemplateUpgrade && kind != model.RolloutVarsReload:
		return nil, model.Rejected("kind must be template_upgrade or vars_reload")
	case kind == model.RolloutVarsReload && scopeKey != "" && scopeKey != orgID:
		return nil, model.ErrRolloutNotFound
	case kind == model.RolloutTemplateUpgrade && scopeKey != "":
		if _, err := uc.d.Templates.GetTemplate(ctx, orgID, scopeKey); err != nil {
			return nil, err
		}
	}
	list, err := uc.d.Repo.ListRollouts(ctx, orgID, kind, scopeKey)
	if err != nil {
		return nil, err
	}
	out := make([]model.RolloutView, 0, len(list))
	for _, r := range list {
		out = append(out, uc.view(ctx, r))
	}
	return out, nil
}

// Stop asks the org's rollout to stop after the step in flight. An ended
// rollout answers as it is.
func (uc *UseCase) Stop(ctx context.Context, orgID, id string) (model.RolloutView, error) {
	ok, err := uc.d.Repo.RequestStop(ctx, orgID, id)
	if err != nil {
		return model.RolloutView{}, err
	}
	r, err := uc.d.Repo.GetRollout(ctx, id)
	if err != nil {
		return model.RolloutView{}, err
	}
	if r.OrgID != orgID {
		return model.RolloutView{}, model.ErrRolloutNotFound
	}
	v := uc.view(ctx, r)
	if ok {
		uc.Wake()
		uc.publish(ctx, orgID, v)
	}
	return v, nil
}

// view is a rollout as the API shows it.
func (uc *UseCase) view(ctx context.Context, r model.Rollout) model.RolloutView {
	v := model.RolloutView{
		ID: r.ID, Kind: r.Kind, ScopeKey: r.ScopeKey, State: string(r.State), StopRequested: r.StopRequested,
		Done: r.Done, Skipped: r.Skipped, Total: r.Total, CurrentAppID: r.CurrentAppID,
		CurrentServerID: r.CurrentServerID, Error: r.Error, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy,
		FinishedAt: r.FinishedAt,
	}
	if r.Kind == model.RolloutTemplateUpgrade {
		var s model.UpgradeScope
		if json.Unmarshal(r.Scope, &s) == nil {
			v.Revision = s.Revision
		}
	}
	if r.CurrentAppID != "" && uc.d.Apps != nil {
		if app, err := uc.d.Apps.GetApp(ctx, r.CurrentAppID); err == nil {
			v.CurrentAppName = app.Name
		}
	}
	return v
}

// publish sends a rollout event to every member of the org.
func (uc *UseCase) publish(ctx context.Context, orgID string, v model.RolloutView) {
	if uc.d.Members == nil || uc.d.Notifications == nil {
		return
	}
	members, err := uc.d.Members.ListMembers(ctx, orgID)
	if err != nil {
		uc.d.Log.Debug("rollout event: list members", "org_id", orgID, "error", err)
		return
	}
	n := model.Notification{Type: model.NotificationRollout, Payload: v, Timestamp: uc.now()}
	for _, m := range members {
		uc.d.Notifications.Publish(m.ID, n)
	}
}
```

**Create** `internal/domain/usecase/rollouts/worker.go`:

```go
package rollouts

import (
	"context"
	"errors"
	"slices"
	"time"

	"winterflow/internal/domain/model"
)

// runner is one kind's step logic.
type runner interface {
	// next picks the rollout's next app, evaluated fresh each step (an app
	// handled by other means meanwhile is not picked). A pick without an
	// app is the end, or with wait set: nothing yet, look again shortly.
	next(ctx context.Context, r model.Rollout) (pick, error)
	// run takes the step on the picked app: dispatch, await the result,
	// wait for the app to report running.
	run(ctx context.Context, r model.Rollout, p pick) stepResult
}

// pick is the app a step works on.
type pick struct {
	appID, serverID string
	// skipID is what a skip records in the rollout's skip list.
	skipID string
	// remaining counts the apps still to do, this one included.
	remaining int64
	// wait: no app yet, but the scope is not settled.
	wait bool
	// inst is a template_upgrade's instance; revSeen the server's
	// shared-variables revision a vars_reload picked the app at.
	inst    model.Instance
	revSeen int64
}

// outcome is how a step ended.
type outcome int

const (
	stepDone outcome = iota
	stepSkipped
	stepFailed
)

type stepResult struct {
	outcome outcome
	err     string
}

func done() stepResult             { return stepResult{outcome: stepDone} }
func skipped() stepResult          { return stepResult{outcome: stepSkipped} }
func failed(msg string) stepResult { return stepResult{outcome: stepFailed, err: msg} }

// errBadScope fails a rollout whose scope cannot be read.
var errBadScope = errors.New("the rollout's scope is unreadable")

// Run drives the queue until ctx is done: a pass now, then one on every
// Wake and once a minute (the idle cost: one indexed query).
func (uc *UseCase) Run(ctx context.Context) {
	tick := time.NewTicker(uc.idle)
	defer tick.Stop()
	for {
		uc.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-uc.wake:
		case <-tick.C:
		}
	}
}

// pass claims and drives the claimable rollouts, one at a time, until none
// is left.
func (uc *UseCase) pass(ctx context.Context) {
	for ctx.Err() == nil {
		ids, err := uc.d.Repo.ListClaimable(ctx, uc.now())
		if err != nil {
			uc.d.Log.Warn("rollouts: list claimable", "error", err)
			return
		}
		claimed := false
		for _, id := range ids {
			now := uc.now()
			ok, err := uc.d.Repo.Claim(ctx, id, uc.owner, now, now.Add(uc.lease))
			if err != nil {
				uc.d.Log.Warn("rollouts: claim", "rollout_id", id, "error", err)
				return
			}
			if ok {
				claimed = true
				uc.drive(ctx, id)
			}
		}
		if !claimed {
			return
		}
	}
}

// drive takes the claimed rollout's steps while this process holds its
// lease. Losing the lease (another process took over after it expired)
// cancels the step in flight; the new owner re-evaluates it.
func (uc *UseCase) drive(parent context.Context, id string) {
	ctx, cancel := context.WithCancel(parent)
	renewing := make(chan struct{})
	go func() {
		defer close(renewing)
		uc.keepLease(ctx, cancel, id)
	}()
	defer func() {
		cancel()
		<-renewing
	}()
	for ctx.Err() == nil {
		r, err := uc.d.Repo.GetRollout(ctx, id)
		if err != nil || r.LeaseOwner != uc.owner || !r.State.Active() {
			return
		}
		if !uc.step(ctx, r) {
			return
		}
	}
}

// keepLease renews the lease until ctx ends; a renewal that finds the
// lease gone (taken over, or the rollout ended) cancels the drive. A failed
// write is retried at the next tick: the lease outlives a few.
func (uc *UseCase) keepLease(ctx context.Context, lost context.CancelFunc, id string) {
	t := time.NewTicker(uc.renew)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		ok, err := uc.d.Repo.RenewLease(ctx, id, uc.owner, uc.now().Add(uc.lease))
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			uc.d.Log.Warn("rollouts: renew lease", "rollout_id", id, "error", err)
		case !ok:
			lost()
			return
		}
	}
}

// step takes one step of r and reports whether to take another.
func (uc *UseCase) step(ctx context.Context, r model.Rollout) bool {
	if r.StopRequested {
		uc.finish(ctx, r, model.RolloutStopped, "", "", "")
		return false
	}
	k, ok := uc.kinds[r.Kind]
	if !ok {
		uc.finish(ctx, r, model.RolloutFailed, "unknown rollout kind "+r.Kind, "", "")
		return false
	}
	p, err := k.next(ctx, r)
	switch {
	case errors.Is(err, errBadScope):
		uc.finish(ctx, r, model.RolloutFailed, err.Error(), "", "")
		return false
	case err != nil:
		// A database error: the lease expires and a pass retries.
		uc.d.Log.Warn("rollouts: pick the next app", "rollout_id", r.ID, "error", err)
		return false
	}
	if p.appID == "" {
		if p.wait {
			return sleep(ctx, uc.settlePoll)
		}
		ok, err := uc.d.Repo.Complete(ctx, r.ID, uc.owner, r.ScopeRev)
		if err != nil {
			uc.d.Log.Warn("rollouts: complete", "rollout_id", r.ID, "error", err)
			return false
		}
		if ok {
			uc.ended(ctx, r.ID)
			return false
		}
		return true // the scope was extended meanwhile: look again
	}
	total := r.Done + r.Skipped + p.remaining
	if ok, err := uc.d.Repo.StartStep(ctx, r.ID, uc.owner, p.appID, p.serverID, total); err != nil || !ok {
		return false
	}
	uc.changed(ctx, r.ID)
	res := k.run(ctx, r, p)
	if ctx.Err() != nil {
		return false // the lease is gone or the process stops: the step is redone
	}
	switch res.outcome {
	case stepFailed:
		uc.finish(ctx, r, model.RolloutFailed, res.err, p.appID, p.serverID)
		return false
	case stepSkipped:
		ok, err = uc.d.Repo.CountStep(ctx, r.ID, uc.owner, 0, 1, append(slices.Clone(r.SkipIDs), p.skipID))
	default:
		ok, err = uc.d.Repo.CountStep(ctx, r.ID, uc.owner, 1, 0, r.SkipIDs)
	}
	if err != nil || !ok {
		return false
	}
	uc.changed(ctx, r.ID)
	return true
}

// finish ends r stopped or failed.
func (uc *UseCase) finish(ctx context.Context, r model.Rollout, state model.RolloutState, msg, appID, serverID string) {
	ok, err := uc.d.Repo.Finish(ctx, r.ID, uc.owner, state, msg, appID, serverID)
	if err != nil {
		uc.d.Log.Warn("rollouts: finish", "rollout_id", r.ID, "error", err)
		return
	}
	if ok {
		uc.ended(ctx, r.ID)
	}
}

// ended announces a rollout that just ended and prunes its scope. Its
// context outlives the drive's: the lease renewal cancels the drive as soon
// as it sees the rollout ended.
func (uc *UseCase) ended(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	r := uc.changed(ctx, id)
	if r.ID == "" {
		return
	}
	if err := uc.d.Repo.Prune(ctx, r.Kind, r.ScopeKey, keepEnded); err != nil {
		uc.d.Log.Warn("rollouts: prune", "rollout_id", id, "error", err)
	}
}

// changed announces the rollout's new counts or state to its org.
func (uc *UseCase) changed(ctx context.Context, id string) model.Rollout {
	r, err := uc.d.Repo.GetRollout(ctx, id)
	if err != nil {
		uc.d.Log.Warn("rollouts: read for an event", "rollout_id", id, "error", err)
		return model.Rollout{}
	}
	uc.publish(ctx, r.OrgID, uc.view(ctx, r))
	return r
}

// sleep waits d unless ctx ends first; it reports whether ctx is still
// live.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
```

**Create** `internal/domain/usecase/rollouts/wait.go`:

```go
package rollouts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
)

// awaitResult waits for a dispatched command's result, at most uc.cmdWait;
// a failed command is its error.
func (uc *UseCase) awaitResult(ctx context.Context, ch <-chan port.CommandResult) (port.CommandResult, error) {
	t := time.NewTimer(uc.cmdWait)
	defer t.Stop()
	select {
	case res := <-ch:
		if !res.Success {
			if res.Error == "" {
				res.Error = "the agent reported a failure"
			}
			return res, errors.New(res.Error)
		}
		return res, nil
	case <-t.C:
		return port.CommandResult{}, fmt.Errorf("no answer from the server within %s", uc.cmdWait)
	case <-ctx.Done():
		return port.CommandResult{}, ctx.Err()
	}
}

// awaitRunning waits, at most uc.health, until a status report received
// after since shows the app running (Active). At the bound an app whose
// other containers exited cleanly (Idle: a finished one-shot container,
// which never reads Active) passes; no report, no containers, or
// Restarting, Problematic or Stopped fails with the reason. A Docker
// healthcheck's "unhealthy" is not seen: such a container reads running.
// It returns the last status seen for the app.
func (uc *UseCase) awaitRunning(ctx context.Context, serverID, appID string, since time.Time) (command.AppStatus, error) {
	deadline := uc.now().Add(uc.health)
	var (
		last             command.AppStatus
		reported, listed bool
	)
	for {
		if rep, ok := uc.d.Status.LatestReport(serverID, uc.now()); ok && rep.Received.After(since) {
			reported = true
			last, listed = findApp(rep.Apps, appID)
			if listed && last.StatusCode == command.ContainerStatusActive {
				return last, nil
			}
		}
		if !uc.now().Before(deadline) {
			break
		}
		if !sleep(ctx, uc.healthPoll) {
			return last, ctx.Err()
		}
	}
	switch {
	case !reported:
		return last, fmt.Errorf("the server sent no status report within %s", uc.health)
	case !listed:
		return last, fmt.Errorf("the app has no containers %s later", uc.health)
	case last.StatusCode == command.ContainerStatusIdle:
		return last, nil
	}
	return last, fmt.Errorf("the app is not running %s later (%s)", uc.health, statusWord(last.StatusCode))
}

// findApp is appID's status in a report.
func findApp(apps []command.AppStatus, appID string) (command.AppStatus, bool) {
	for _, a := range apps {
		if a.AppID == appID {
			return a, true
		}
	}
	return command.AppStatus{}, false
}

// statusWord names a status code for an error.
func statusWord(c command.ContainerStatusCode) string {
	switch c {
	case command.ContainerStatusRestarting:
		return "restarting"
	case command.ContainerStatusProblematic:
		return "a container exited with an error"
	case command.ContainerStatusStopped:
		return "stopped"
	}
	return "status unknown"
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal; go vet ./internal/domain/... && go test -race -count=3 ./internal/domain/usecase/rollouts/
```

Expected: no gofmt output; `ok` three times (about 5 s each).

- [ ] **Commit**

```bash
git add internal/domain/model/notification.go internal/domain/usecase/rollouts
git commit -m "feat(rollouts): the rollout queue and its worker

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: `template_upgrade` — "Upgrade all" steps through the instance upgrade

**Files:**
- Modify: `internal/domain/usecase/templates/update.go`, `internal/domain/usecase/templates/update_test.go`
- Create: `internal/domain/usecase/rollouts/upgrade.go`
- Modify: `internal/domain/usecase/rollouts/usecase.go` (the kinds map)
- Test: `internal/domain/usecase/rollouts/agent_test.go` (a fake agent used again by Task 8), `upgrade_test.go`

**Interfaces:**
- Consumes: Task 5's internals; `templates.UseCase.UpdateInstance`, `port.TemplateRepository.{GetTemplate,GetRevision}` and `ListInstancesOnOrgServers` (Task 2: an instance on another org's server is never picked).
- Produces: `templates.ErrNeedsValues` (an update or upgrade whose values the revision refuses also matches it; the message and the `ErrRejected` class are unchanged); `(*rollouts.UseCase).QueueUpgrade(ctx, userID, orgID, templateID string, revision int64) (model.RolloutView, error)` (0 = latest; `ErrTemplateNotFound`, `ErrTemplateRevisionGone`, `ErrRolloutActive`); the `template_upgrade` runner.

- [ ] **Step 1: Write the failing tests**

**Replace in** `internal/domain/usecase/templates/update_test.go`:

```go
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a newly required value missing: %v", err)
	}
```
**with:**

```go
		t.Fatal(err)
	}
	_, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2})
	if !errors.Is(err, model.ErrRejected) || !errors.Is(err, templates.ErrNeedsValues) || !strings.Contains(err.Error(), "parameters.REGION: required") {
		t.Fatalf("a newly required value missing: %v", err)
	}
```

**Create** `internal/domain/usecase/rollouts/agent_test.go`:

```go
package rollouts

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	apprepo "winterflow/internal/infra/db/repository/app"
	"winterflow/pkg/util"
)

// fakeAgent answers app.save and vars.reload on its own goroutine, then
// reports the server's apps as the status reporter would: the app it
// touched running (or after[app]) and no longer stale.
type fakeAgent struct {
	mu      sync.Mutex
	reports *fakeReports
	apps    map[string]map[string]command.AppStatus // server -> app -> status
	varsRev map[string]int64
	fail    map[string]string                      // app -> error its commands answer
	after   map[string]command.ContainerStatusCode // app -> status after its command
	skip    map[string]bool                        // app -> vars.reload answers skipped
	stuck   map[string]bool                        // app -> stays stale after a reload
	seen    []string                               // "type app" per command
}

func newFakeAgent(reports *fakeReports) *fakeAgent {
	return &fakeAgent{reports: reports, apps: map[string]map[string]command.AppStatus{}, varsRev: map[string]int64{},
		fail: map[string]string{}, after: map[string]command.ContainerStatusCode{}, skip: map[string]bool{}, stuck: map[string]bool{}}
}

func (a *fakeAgent) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	go a.answer(in)
	return util.GenerateID(), nil
}

func (a *fakeAgent) answer(in port.DispatchInput) {
	var (
		appID   string
		payload any
	)
	switch req := in.Payload.(type) {
	case command.SaveAppRequest:
		appID, payload = req.App.AppID, command.SaveAppResponse{AppID: req.App.AppID}
	case command.ReloadVarsRequest:
		appID = req.AppIDs[0]
		payload = command.ReloadVarsResponse{Queued: true, AppID: appID, Skipped: a.skipped(appID)}
	}
	a.mu.Lock()
	a.seen = append(a.seen, string(in.Type)+" "+appID)
	msg := a.fail[appID]
	if msg == "" && !a.skip[appID] {
		code, ok := a.after[appID]
		if !ok {
			code = command.ContainerStatusActive
		}
		st := command.AppStatus{AppID: appID, StatusCode: code, Containers: []command.ContainerStatus{{StatusCode: code}}}
		if a.stuck[appID] {
			st.StaleVars = a.apps[in.AgentID][appID].StaleVars
		}
		a.setLocked(in.AgentID, st)
	}
	a.mu.Unlock()
	res := port.CommandResult{Success: msg == "", Error: msg}
	if msg == "" {
		res.Payload, _ = json.Marshal(payload)
	}
	in.OnResult(res)
	time.Sleep(20 * time.Millisecond) // the next report comes after the answer
	a.report(in.AgentID)
}

func (a *fakeAgent) skipped(appID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.skip[appID]
}

// set records an app's status (stale names included) on a server.
func (a *fakeAgent) set(serverID string, st command.AppStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.setLocked(serverID, st)
}

func (a *fakeAgent) setLocked(serverID string, st command.AppStatus) {
	if a.apps[serverID] == nil {
		a.apps[serverID] = map[string]command.AppStatus{}
	}
	a.apps[serverID][st.AppID] = st
}

// report publishes the server's apps to the status cache now.
func (a *fakeAgent) report(serverID string) {
	a.mu.Lock()
	apps := []command.AppStatus{}
	for _, st := range a.apps[serverID] {
		apps = append(apps, st)
	}
	rev := a.varsRev[serverID]
	a.mu.Unlock()
	slices.SortFunc(apps, func(x, y command.AppStatus) int { return compare(x.AppID, y.AppID) })
	a.reports.set(serverID, status.Report{Apps: apps, VarsRevision: rev, HasRevision: true, Received: time.Now()})
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (a *fakeAgent) commands() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.seen)
}

// withAgent wires instances and the app catalog over a fake agent.
func (e *env) withAgent(t *testing.T) (*fakeAgent, *templates.UseCase) {
	t.Helper()
	apps, err := apprepo.New(e.d.Tx, e.d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	a := newFakeAgent(e.reports)
	tu := templates.New(templates.Deps{Repo: e.tpl, Apps: appuc.NewUseCase(&appuc.Deps{CommandDispatcher: a, AppRepository: apps, Log: dbtest.Log}), Log: dbtest.Log})
	e.deps.Instances, e.deps.Apps, e.deps.Dispatcher = tu, apps, a
	e.uc = e.newUseCase()
	return a, tu
}
```

**Create** `internal/domain/usecase/rollouts/upgrade_test.go`:

```go
package rollouts

import (
	"errors"
	"slices"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

const shopCompose = "services:\n  app:\n    image: example/shop:${VERSION}\n"

func shopBody(pattern string) template.Body {
	def := "1"
	return template.Body{
		Files:      map[string]string{"compose.yaml": shopCompose},
		Parameters: []template.Parameter{{Name: "NAME", Required: true, Pattern: pattern}, {Name: "VERSION", Default: &def}},
		AppName:    "shop-${NAME}",
	}
}

// shop saves template "shop" (r1) with created instances named names on
// srv1 and returns it with the instances' app ids by name.
func (e *env) shop(t *testing.T, tu *templates.UseCase, names ...string) (templates.SavedTemplate, map[string]string) {
	t.Helper()
	ctx := dbtest.Ctx(t)
	tpl, err := tu.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "shop", Body: shopBody("^[a-z]+$")})
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]string{}
	for _, n := range names {
		out, err := tu.CreateInstance(ctx, "u1", org1, templates.CreateInput{ServerID: srv1, Key: n, TemplateID: tpl.ID, Params: map[string]string{"NAME": n}})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case res := <-out.Done:
			if !res.Success {
				t.Fatalf("create %s: %s", n, res.Error)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("create %s: no result", n)
		}
		v, _ := out.Final(ctx)
		apps[n] = v.AppID
	}
	return tpl, apps
}

// revise adds revision 2 with NAME's pattern.
func revise(t *testing.T, tu *templates.UseCase, tpl templates.SavedTemplate, pattern string) {
	t.Helper()
	if _, err := tu.SaveTemplate(dbtest.Ctx(t), "u1", org1, templates.SaveInput{ID: tpl.ID, Name: "shop", Body: shopBody(pattern), BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
}

// revisions are the instances' revisions by key.
func revisions(t *testing.T, e *env) map[string]int64 {
	t.Helper()
	all, err := e.tpl.ListInstances(dbtest.Ctx(t), org1)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, inst := range all {
		out[inst.Key] = inst.Revision
	}
	return out
}

func TestQueueUpgradeChecksTheTemplateAndTheScope(t *testing.T) {
	e := newEnv(t)
	_, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, _ := e.shop(t, tu)
	if _, err := e.uc.QueueUpgrade(ctx, "u1", org2, tpl.ID, 0); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template: %v", err)
	}
	if _, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 7); !errors.Is(err, model.ErrTemplateRevisionGone) {
		t.Fatalf("an unknown revision: %v", err)
	}
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil || v.State != "queued" || v.Kind != model.RolloutTemplateUpgrade || v.ScopeKey != tpl.ID || v.Revision != 1 || v.CreatedBy != "u1" {
		t.Fatalf("queued = %+v, %v", v, err)
	}
	if _, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0); !errors.Is(err, model.ErrRolloutActive) || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("a second upgrade: %v", err)
	}
	if list, err := e.uc.List(ctx, org1, model.RolloutTemplateUpgrade, tpl.ID); err != nil || len(list) != 1 || list[0].ID != v.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestUpgradeAllMovesEveryInstanceAndSkipsWhatNeedsValues(t *testing.T) {
	e := newEnv(t)
	a, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, apps := e.shop(t, tu, "acme", "bolt", "coda")
	// r2 refuses "bolt": that instance needs a new value, so it is skipped.
	revise(t, tu, tpl, "^[ac][a-z]+$")
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil || v.Revision != 2 {
		t.Fatalf("queue = %+v, %v", v, err)
	}
	e.uc.pass(ctx)

	r := e.get(t, v.ID)
	if r.State != model.RolloutDone || r.Done != 2 || r.Skipped != 1 || r.Total != 3 || len(r.SkipIDs) != 1 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := revisions(t, e); got["acme"] != 2 || got["bolt"] != 1 || got["coda"] != 2 {
		t.Fatalf("revisions = %v", got)
	}
	// The three creates, then the upgrades oldest first; the skipped
	// instance was not saved again.
	save := func(n string) string { return "app.save " + apps[n] }
	if got, want := a.commands(), []string{save("acme"), save("bolt"), save("coda"), save("acme"), save("coda")}; !slices.Equal(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	evs := e.pub.events()
	if last := evs[len(evs)-1]; last.State != "done" || last.Revision != 2 || last.Done != 2 || last.Skipped != 1 {
		t.Fatalf("last event = %+v", last)
	}
	if !slices.ContainsFunc(evs, func(v model.RolloutView) bool {
		return v.CurrentAppID == apps["coda"] && v.CurrentAppName == "shop-coda"
	}) {
		t.Fatalf("no event names the app in flight: %+v", evs)
	}
}

func TestUpgradeAllStopsAtTheFirstFailure(t *testing.T) {
	e := newEnv(t)
	a, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, apps := e.shop(t, tu, "acme", "bolt", "coda")
	revise(t, tu, tpl, "^[a-z]+$")
	a.mu.Lock()
	a.fail[apps["bolt"]] = "pull access denied for example/shop"
	a.mu.Unlock()
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)

	r := e.get(t, v.ID)
	if r.State != model.RolloutFailed || r.Error != "pull access denied for example/shop" || r.CurrentAppID != apps["bolt"] || r.Done != 1 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := revisions(t, e); got["acme"] != 2 || got["bolt"] != 1 || got["coda"] != 1 {
		t.Fatalf("revisions = %v: nothing may follow the failure", got)
	}
}

func TestUpgradeFailsWhenTheAppDoesNotRunAfterwards(t *testing.T) {
	e := newEnv(t)
	a, tu := e.withAgent(t)
	ctx := dbtest.Ctx(t)
	tpl, apps := e.shop(t, tu, "acme")
	revise(t, tu, tpl, "^[a-z]+$")
	a.mu.Lock()
	a.after[apps["acme"]] = command.ContainerStatusRestarting
	a.mu.Unlock()
	v, err := e.uc.QueueUpgrade(ctx, "u1", org1, tpl.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	if r := e.get(t, v.ID); r.State != model.RolloutFailed || r.Error != "the app is not running 200ms later (restarting)" || r.CurrentAppID != apps["acme"] {
		t.Fatalf("rollout = %+v", r)
	}
	// The save itself went through: the instance is on r2.
	if got := revisions(t, e); got["acme"] != 2 {
		t.Fatalf("revisions = %v", got)
	}
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/domain/usecase/templates/ ./internal/domain/usecase/rollouts/
```

Expected: FAIL to compile: `undefined: templates.ErrNeedsValues`, `e.uc.QueueUpgrade undefined`.

- [ ] **Step 2: Implement**

**Replace in** `internal/domain/usecase/templates/update.go`:

```go
import (
	"context"

	"winterflow/internal/domain/model"
```
**with:**

```go
import (
	"context"
	"errors"

	"winterflow/internal/domain/model"
```

**Replace in** `internal/domain/usecase/templates/update.go`:

```go
// agent already confirmed: its backups belong to the app's Backups tab.
var ErrBackupsNotPending = model.Rejected("backups can only be set while the instance is pending; change them in the app's Backups tab")

// UpdateInput is POST update-instance and (with Revision) upgrade-instance.
```
**with:**

```go
// agent already confirmed: its backups belong to the app's Backups tab.
var ErrBackupsNotPending = model.Rejected("backups can only be set while the instance is pending; change them in the app's Backups tab")

// ErrNeedsValues marks an update or upgrade the values at hand can't
// render (a newly required parameter without a default, a stored value the
// revision's pattern refuses): an "Upgrade all" rollout skips such an
// instance. The error's message is the validation's.
var ErrNeedsValues = errors.New("the instance needs new values for this revision")

// needsValues is a values error that also matches ErrNeedsValues.
type needsValues struct{ err error }

func (e needsValues) Error() string   { return e.err.Error() }
func (e needsValues) Unwrap() []error { return []error{e.err, ErrNeedsValues} }

// UpdateInput is POST update-instance and (with Revision) upgrade-instance.
```

**Replace in** `internal/domain/usecase/templates/update.go`:

```go
	values, err := template.Resolve(body, stored, template.Given{Params: in.Params, Secrets: in.Secrets})
	if err != nil {
		return Outcome{}, err
	}
	input.Values = values
```
**with:**

```go
	values, err := template.Resolve(body, stored, template.Given{Params: in.Params, Secrets: in.Secrets})
	if err != nil {
		return Outcome{}, needsValues{err}
	}
	input.Values = values
```

**Create** `internal/domain/usecase/rollouts/upgrade.go`:

```go
package rollouts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"winterflow/internal/domain/model"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/util"
)

// QueueUpgrade queues "Upgrade all": the template's created instances on a
// revision below revision (0: the latest) move to it, oldest first, one at
// a time. Another org's template or an unknown revision is not found; a
// template with an upgrade still active is model.ErrRolloutActive.
func (uc *UseCase) QueueUpgrade(ctx context.Context, userID, orgID, templateID string, revision int64) (model.RolloutView, error) {
	tpl, err := uc.d.Templates.GetTemplate(ctx, orgID, templateID)
	if err != nil {
		return model.RolloutView{}, err
	}
	if revision == 0 {
		revision = tpl.LatestRevision
	}
	if _, err := uc.d.Templates.GetRevision(ctx, tpl.ID, revision); err != nil {
		return model.RolloutView{}, err
	}
	scope, err := json.Marshal(model.UpgradeScope{TemplateID: tpl.ID, Revision: revision})
	if err != nil {
		return model.RolloutView{}, err
	}
	v, ok, err := uc.queue(ctx, model.Rollout{
		ID: util.GenerateID(), OrgID: orgID, Kind: model.RolloutTemplateUpgrade, Scope: scope, ScopeKey: tpl.ID, CreatedBy: userID,
	})
	if err == nil && !ok {
		err = model.ErrRolloutActive
	}
	return v, err
}

// upgrader is the template_upgrade kind.
type upgrader struct{ uc *UseCase }

func upgradeScope(r model.Rollout) (model.UpgradeScope, error) {
	var s model.UpgradeScope
	if err := json.Unmarshal(r.Scope, &s); err != nil || s.TemplateID == "" || s.Revision < 1 {
		return model.UpgradeScope{}, errBadScope
	}
	return s, nil
}

// next is the oldest created instance of the template below the target
// revision that this rollout has not skipped, on one of the org's own
// servers.
func (k upgrader) next(ctx context.Context, r model.Rollout) (pick, error) {
	s, err := upgradeScope(r)
	if err != nil {
		return pick{}, err
	}
	all, err := k.uc.d.Templates.ListInstancesOnOrgServers(ctx, r.OrgID)
	if err != nil {
		return pick{}, err
	}
	var todo []model.Instance
	for _, inst := range all {
		if inst.TemplateID == s.TemplateID && inst.State == model.InstanceCreated && inst.Revision < s.Revision &&
			!slices.Contains(r.SkipIDs, inst.ID) {
			todo = append(todo, inst)
		}
	}
	if len(todo) == 0 {
		return pick{}, nil
	}
	inst := todo[0]
	return pick{appID: inst.AppID, serverID: inst.ServerID, skipID: inst.ID, remaining: int64(len(todo)), inst: inst}, nil
}

// run re-renders the instance at the target revision with its stored
// values (the upgrade-instance path) and waits for the save and for the
// app to run. An instance the revision needs new values for, or one gone
// meanwhile, is skipped.
func (k upgrader) run(ctx context.Context, r model.Rollout, p pick) stepResult {
	s, err := upgradeScope(r)
	if err != nil {
		return failed(err.Error())
	}
	out, err := k.uc.d.Instances.UpdateInstance(ctx, r.CreatedBy, r.OrgID, tuc.UpdateInput{
		ServerID: p.inst.ServerID, Key: p.inst.Key, Revision: s.Revision,
	})
	switch {
	case errors.Is(err, tuc.ErrNeedsValues), errors.Is(err, model.ErrNotFound), errors.Is(err, model.ErrKeyRetired):
		return skipped()
	case err != nil:
		return failed(err.Error())
	case out.Done == nil:
		return done() // nothing to dispatch
	}
	if _, err := k.uc.awaitResult(ctx, out.Done); err != nil {
		return failed(err.Error())
	}
	if _, err := k.uc.awaitRunning(ctx, p.serverID, p.appID, k.uc.now()); err != nil {
		return failed(err.Error())
	}
	return done()
}
```

**Replace in** `internal/domain/usecase/rollouts/usecase.go`:

```go
		health: healthWait, healthPoll: healthPoll, settle: settleWait, settlePoll: settlePoll,
	}
	uc.kinds = map[string]runner{}
	return uc
}
```
**with:**

```go
		health: healthWait, healthPoll: healthPoll, settle: settleWait, settlePoll: settlePoll,
	}
	uc.kinds = map[string]runner{model.RolloutTemplateUpgrade: upgrader{uc}}
	return uc
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal; go vet ./internal/domain/... && go test -count=1 ./internal/domain/usecase/templates/ ./internal/app/web/contract/ && go test -race -count=2 ./internal/domain/usecase/rollouts/
```

Expected: no gofmt output; all `ok` (the contract suite pins that the 400 messages of update/upgrade-instance did not change).

- [ ] **Commit**

```bash
git add internal/domain/usecase/templates/update.go internal/domain/usecase/templates/update_test.go internal/domain/usecase/rollouts
git commit -m "feat(rollouts): template_upgrade steps through the instance upgrade

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Routes — `upgrade-instances`, `get-rollouts`, `stop-rollout`, the `rollout` event; the worker runs

**Files:**
- Create: `internal/infra/bootstrap/rollouts.go`
- Modify: `internal/infra/bootstrap/deps.go`, `internal/infra/bootstrap/core.go`
- Modify: `internal/app/web/handler/templates/{handler.go,routes.go,templates.go}`
- Create: `internal/app/web/handler/rollouts/handler.go`, `handler_test.go`
- Modify: `internal/app/web/routes.go`, `internal/app/web/handler/notification/routes.go`, `routes_test.go`
- Generated: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`, `internal/app/web/contract/testdata/parity.golden.json`
- Test: `internal/app/web/contract/rollouts_test.go` (new), `matrix_test.go`, `parity_test.go`

**Interfaces:**
- Consumes: `rollouts.New`, `QueueUpgrade`, `List`, `Stop`, `Run` (Tasks 5–6).
- Produces: `bootstrap.Deps.Rollouts *rollouts.UseCase` (the one value whose `Wake` reaches the worker `WireCore` started); `POST /api/v1/template/upgrade-instances` (`htemplates.UpgradeInstancesRequest{ID string; Revision int64}` → 200 `model.RolloutView`, 400/404/409); `GET /api/v1/rollout/get-rollouts` (`hrollouts.RolloutsQuery{Kind, ScopeKey}` → `hrollouts.RolloutList{Rollouts []model.RolloutView}`, 400/404); `POST /api/v1/rollout/stop-rollout` (`hrollouts.RolloutIDRequest{ID}` → `model.RolloutView`, 400/404); SSE `rollout` with payload `model.RolloutView`.

- [ ] **Step 1: Build and start the queue in `WireCore`**

**Create** `internal/infra/bootstrap/rollouts.go`:

```go
package bootstrap

import (
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/rollouts"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
)

// newRollouts builds the rollout queue. Its template_upgrade steps render
// and dispatch through a templates usecase of their own, built exactly as
// the instance routes build theirs; its vars_reload steps dispatch
// vars.reload. The caller starts its worker (Run).
func newRollouts(r repos, d port.CommandDispatcher, cache *status.Cache, nm port.NotificationManager, log *logger.Logger) *rollouts.UseCase {
	apps := appuc.NewUseCase(&appuc.Deps{
		CommandDispatcher:       d,
		AppRepository:           r.app,
		AppDomainRepository:     r.appDomain,
		ServerDomainRepository:  r.serverDomain,
		ServerStorageRepository: r.serverStorage,
		Servers:                 r.server,
		Log:                     log,
	})
	instances := templates.New(templates.Deps{Repo: r.template, Apps: apps, Shared: r.sharedVar, Status: cache, Log: log})
	return rollouts.New(rollouts.Deps{
		Repo: r.rollout, Templates: r.template, Instances: instances, Dispatcher: d, Status: cache,
		Apps: r.app, Members: r.user, Notifications: nm, Log: log,
	})
}
```

**Replace in** `internal/infra/bootstrap/deps.go`:

```go
	"winterflow/internal/domain/service/alerting"
	"winterflow/internal/domain/service/status"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
```
**with:**

```go
	"winterflow/internal/domain/service/alerting"
	"winterflow/internal/domain/service/status"
	"winterflow/internal/domain/usecase/rollouts"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
```

**Replace in** `internal/infra/bootstrap/deps.go`:

```go
	TemplateRepository port.TemplateRepository
	// RolloutRepository stores the rollout queue.
	RolloutRepository   port.RolloutRepository
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
```
**with:**

```go
	TemplateRepository port.TemplateRepository
	// RolloutRepository stores the rollout queue.
	RolloutRepository port.RolloutRepository
	// Rollouts is the rollout queue whose worker WireCore started: every
	// handler that queues a rollout must use this one value (its Wake
	// reaches that worker).
	Rollouts            *rollouts.UseCase
	CommandDispatcher   port.CommandDispatcher
	NotificationManager port.NotificationManager
```

**Replace in** `internal/infra/bootstrap/core.go`:

```go
	}, vars, ms, latest, al.evaluator)

	return &Deps{
		Log:                      log,
```
**with:**

```go
	}, vars, ms, latest, al.evaluator)

	// The rollout queue: one worker per API process (standalone included);
	// an idle one costs one indexed query a minute.
	queue := newRollouts(r, dispatcher, statusCache, nm, log)
	go queue.Run(ctx)

	return &Deps{
		Log:                      log,
```

**Replace in** `internal/infra/bootstrap/core.go`:

```go
		TemplateRepository:       r.template,
		RolloutRepository:        r.rollout,
		CommandDispatcher:        dispatcher,
		NotificationManager:      nm,
```
**with:**

```go
		TemplateRepository:       r.template,
		RolloutRepository:        r.rollout,
		Rollouts:                 queue,
		CommandDispatcher:        dispatcher,
		NotificationManager:      nm,
```
- [ ] **Step 2: Write the failing handler tests**

**Create** `internal/app/web/handler/rollouts/handler_test.go`:

```go
package rollouts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "rollouts-test"})

const anID = "00000000-0000-7000-8000-000000000101"

type orgs struct{}

func (orgs) PrimaryOrganizationID(context.Context, string) (string, error) { return "org-1", nil }

// queue records what the routes asked and answers err.
type queue struct {
	err    error
	called string
}

func (q *queue) List(_ context.Context, orgID, kind, scopeKey string) ([]model.RolloutView, error) {
	q.called = fmt.Sprintf("list %s %s %s", orgID, kind, scopeKey)
	return []model.RolloutView{{ID: anID, Kind: kind}}, q.err
}

func (q *queue) Stop(_ context.Context, orgID, id string) (model.RolloutView, error) {
	q.called = fmt.Sprintf("stop %s %s", orgID, id)
	return model.RolloutView{ID: id, StopRequested: true}, q.err
}

func authed(r *http.Request) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "provider_u1", Attributes: map[string]any{"user_id": "u1"}})
}

func TestRoutes(t *testing.T) {
	for _, rt := range NewHandler(&Deps{}).Routes(api.Guards{}) {
		if rt.Handler == nil || rt.Auth != api.User || rt.Tag != "rollouts" || rt.Accepted || !strings.HasPrefix(rt.Path, "/api/v1/rollout/") {
			t.Errorf("%s %s: want a synchronous User route tagged rollouts", rt.Method, rt.Path)
		}
	}
}

func TestGetRollouts(t *testing.T) {
	q := &queue{}
	h := NewHandler(&Deps{Logger: testLog, Users: orgs{}, Rollouts: q})
	get := func(query url.Values) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.GetRollouts(w, authed(httptest.NewRequest(http.MethodGet, "/api/v1/rollout/get-rollouts?"+query.Encode(), nil)))
		return w
	}
	if w := get(url.Values{"kind": {"vars_reload"}}); w.Code != http.StatusOK || q.called != "list org-1 vars_reload " {
		t.Fatalf("list = %d %s (%s)", w.Code, w.Body, q.called)
	}
	if w := get(url.Values{}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "kind is required") {
		t.Fatalf("no kind = %d %s", w.Code, w.Body)
	}
	if w := get(url.Values{"kind": {"template_upgrade"}, "scope_key": {"x"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("a scope key that is no id = %d", w.Code)
	}
	q.err = model.ErrTemplateNotFound
	if w := get(url.Values{"kind": {"template_upgrade"}, "scope_key": {anID}}); w.Code != http.StatusNotFound {
		t.Fatalf("another org's template = %d", w.Code)
	}
	q.err = errors.New("db down")
	if w := get(url.Values{"kind": {"vars_reload"}}); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "failed to list rollouts") {
		t.Fatalf("db error = %d %s", w.Code, w.Body)
	}
}

func TestStopRollout(t *testing.T) {
	q := &queue{}
	h := NewHandler(&Deps{Logger: testLog, Users: orgs{}, Rollouts: q})
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.StopRollout(w, authed(httptest.NewRequest(http.MethodPost, "/api/v1/rollout/stop-rollout", strings.NewReader(body))))
		return w
	}
	if w := post(`{"id":"` + anID + `"}`); w.Code != http.StatusOK || q.called != "stop org-1 "+anID {
		t.Fatalf("stop = %d %s (%s)", w.Code, w.Body, q.called)
	}
	if w := post(`{"id":"nope"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a bad id = %d", w.Code)
	}
	q.err = model.ErrRolloutNotFound
	if w := post(`{"id":"` + anID + `"}`); w.Code != http.StatusNotFound {
		t.Fatalf("another org's rollout = %d", w.Code)
	}
}
```

**Replace in** `internal/app/web/handler/notification/routes_test.go`:

```go
		"vars.sync_failed": model.VarsSyncFailedPayload{},
		"vars.reloaded":    command.VarsReloadedPayload{},
	}
	if len(rt.EventPayloads) != len(want) {
```
**with:**

```go
		"vars.sync_failed": model.VarsSyncFailedPayload{},
		"vars.reloaded":    command.VarsReloadedPayload{},
		"rollout":          model.RolloutView{},
	}
	if len(rt.EventPayloads) != len(want) {
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/app/web/handler/rollouts/ ./internal/app/web/handler/notification/
```

Expected: rollouts: FAIL to compile (`undefined: NewHandler`); notification: FAIL `want 8 entries`.

- [ ] **Step 3: The routes**

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
```
**with:**

```go
}

// RolloutQueue queues "Upgrade all" (*rollouts.UseCase).
type RolloutQueue interface {
	QueueUpgrade(ctx context.Context, userID, orgID, templateID string, revision int64) (model.RolloutView, error)
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go
	ServerStorageRepository port.ServerStorageRepository
	StatusCache             *status.Cache
}

```
**with:**

```go
	ServerStorageRepository port.ServerStorageRepository
	StatusCache             *status.Cache
	Rollouts                RolloutQueue
}

```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go
type Handler struct {
	uc        *tuc.UseCase
	users     webutil.OrgResolver
	servers   InstanceServers
```
**with:**

```go
type Handler struct {
	uc        *tuc.UseCase
	rollouts  RolloutQueue
	users     webutil.OrgResolver
	servers   InstanceServers
```

**Replace in** `internal/app/web/handler/templates/handler.go`:

```go
		deps.Status = d.StatusCache
	}
	return &Handler{uc: tuc.New(deps), users: d.Users, servers: d.Servers, log: d.Logger, waitBound: WaitBound}
}

```
**with:**

```go
		deps.Status = d.StatusCache
	}
	return &Handler{uc: tuc.New(deps), rollouts: d.Rollouts, users: d.Users, servers: d.Servers, log: d.Logger, waitBound: WaitBound}
}

```

**Replace in** `internal/app/web/handler/templates/routes.go`:

```go

	"winterflow/internal/app/web/api"
	tuc "winterflow/internal/domain/usecase/templates"
)
```
**with:**

```go

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	tuc "winterflow/internal/domain/usecase/templates"
)
```

**Replace in** `internal/app/web/handler/templates/routes.go`:

```go
			Request: TemplateIDRequest{}, Errors: orgConflict,
		},
	}
}
```
**with:**

```go
			Request: TemplateIDRequest{}, Errors: orgConflict,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/upgrade-instances", Handler: h.UpgradeInstances, Auth: api.User,
			Summary: "Upgrade all of a template's instances, one app at a time", Tag: "templates",
			Description: "Queues a template_upgrade rollout: each created instance below the revision is re-rendered with " +
				"its values and redeployed, oldest first; one that needs new values is skipped; the first failure stops it. " +
				"409 while an upgrade of the template is active. Progress arrives as rollout events over SSE.",
			Request: UpgradeInstancesRequest{}, Response: model.RolloutView{}, Errors: orgConflict,
		},
	}
}
```

**Replace in** `internal/app/web/handler/templates/templates.go`:

```go
type TemplateIDRequest struct {
	ID string `json:"id" required:"true"`
}

```
**with:**

```go
type TemplateIDRequest struct {
	ID string `json:"id" required:"true"`
}

// UpgradeInstancesRequest is the body of POST upgrade-instances.
type UpgradeInstancesRequest struct {
	ID       string `json:"id" required:"true"`
	Revision int64  `json:"revision" description:"The revision to move the instances to; absent or 0 is the latest."`
}

```

**Replace in** `internal/app/web/handler/templates/templates.go`:

```go
	webutil.Success(w, "template deleted", nil)
}
```
**with:**

```go
	webutil.Success(w, "template deleted", nil)
}

func (h *Handler) UpgradeInstances(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[UpgradeInstancesRequest](w, r)
	if !ok || !requireID(w, "id", req.ID) {
		return
	}
	if req.Revision < 0 {
		webutil.Error(w, "revision must be a revision number", nil)
		return
	}
	out, err := h.rollouts.QueueUpgrade(r.Context(), userID, orgID, req.ID, req.Revision)
	if err != nil {
		h.fail(w, err, "queue the upgrade")
		return
	}
	webutil.Success(w, "upgrade queued", out)
}
```

**Create** `internal/app/web/handler/rollouts/handler.go`:

```go
// Package rollouts serves the rollout queue: the org's rollouts and Stop.
// Every route is scoped to the caller's org: another org's rollout,
// template or org answers 404.
package rollouts

import (
	"context"
	"errors"
	"net/http"

	"winterflow/internal/app/web/api"
	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
)

// Queue is the rollout queue as the routes use it (*rollouts.UseCase).
type Queue interface {
	List(ctx context.Context, orgID, kind, scopeKey string) ([]model.RolloutView, error)
	Stop(ctx context.Context, orgID, id string) (model.RolloutView, error)
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
type Deps struct {
	Logger   *logger.Logger
	Users    webutil.OrgResolver
	Rollouts Queue
}

// Handler serves the rollout routes.
type Handler struct {
	queue Queue
	users webutil.OrgResolver
	log   *logger.Logger
}

// NewHandler builds the handler.
func NewHandler(d *Deps) *Handler {
	return &Handler{queue: d.Rollouts, users: d.Users, log: d.Logger}
}

// RolloutsQuery is the query of GET get-rollouts.
type RolloutsQuery struct {
	Kind     string `query:"kind" required:"true" description:"template_upgrade or vars_reload."`
	ScopeKey string `query:"scope_key" description:"A template id (template_upgrade) or the org id (vars_reload); absent: every scope of the org."`
}

// RolloutIDRequest is the body of POST stop-rollout.
type RolloutIDRequest struct {
	ID string `json:"id" required:"true"`
}

// RolloutList is the data of GET get-rollouts.
type RolloutList struct {
	Rollouts []model.RolloutView `json:"rollouts" description:"The active rollout first, then the last 20 ended ones of each scope, newest first."`
}

// Routes is the rollouts slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	errs := []int{http.StatusBadRequest, http.StatusNotFound}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/rollout/get-rollouts", Handler: h.GetRollouts, Auth: api.User,
			Summary: "The org's rollouts of a kind: the active one and the last 20 ended", Tag: "rollouts",
			Description: "Progress arrives as rollout events over SSE.",
			Query:       RolloutsQuery{}, Response: RolloutList{}, Errors: errs,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/rollout/stop-rollout", Handler: h.StopRollout, Auth: api.User,
			Summary: "Stop a rollout after the app in flight", Tag: "rollouts",
			Description: "An ended rollout answers as it is.",
			Request:     RolloutIDRequest{}, Response: model.RolloutView{}, Errors: errs,
		},
	}
}

func (h *Handler) GetRollouts(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := webutil.CallerOrg(w, r, h.users, h.log)
	if !ok {
		return
	}
	q := r.URL.Query()
	kind, scopeKey := q.Get("kind"), q.Get("scope_key")
	if kind == "" {
		webutil.Error(w, "kind is required", nil)
		return
	}
	if scopeKey != "" && !tuc.ValidID(scopeKey) {
		webutil.Error(w, "scope_key must be a template or org id", nil)
		return
	}
	list, err := h.queue.List(r.Context(), orgID, kind, scopeKey)
	if err != nil {
		h.fail(w, err, "list rollouts")
		return
	}
	webutil.Success(w, "", RolloutList{Rollouts: list})
}

func (h *Handler) StopRollout(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := webutil.CallerOrg(w, r, h.users, h.log)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[RolloutIDRequest](w, r)
	if !ok {
		return
	}
	if !tuc.ValidID(req.ID) {
		webutil.Error(w, "id must be a rollout id", nil)
		return
	}
	v, err := h.queue.Stop(r.Context(), orgID, req.ID)
	if err != nil {
		h.fail(w, err, "stop the rollout")
		return
	}
	webutil.Success(w, "stop requested", v)
}

// fail maps usecase errors: unknown (or another org's) 404, a fixable
// request 400 with its message, anything else a logged generic 400.
func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrRejected) {
		webutil.ErrorStatus(w, webutil.StatusFor(err), err.Error())
		return
	}
	h.log.Error("rollouts: "+what, "error", err)
	webutil.Error(w, "failed to "+what, nil)
}
```

**Replace in** `internal/app/web/handler/notification/routes.go`:

```go
	"An operation_result is delivered only to the open streams of the user who dispatched it; " +
	"match it on ref == the 202's data.request_id. server_status, apps_status, alert and vars.* events go to every " +
	"member of the server's organization and interleave with results; ignore types you do not handle."

// Routes is the SSE stream's slice of the route table. operation_result
```
**with:**

```go
	"An operation_result is delivered only to the open streams of the user who dispatched it; " +
	"match it on ref == the 202's data.request_id. server_status, apps_status, alert and vars.* events go to every " +
	"member of the server's organization, rollout events to every member of the rollout's organization; they " +
	"interleave with results; ignore types you do not handle."

// Routes is the SSE stream's slice of the route table. operation_result
```

**Replace in** `internal/app/web/handler/notification/routes.go`:

```go
			{Type: string(model.NotificationVarsSyncFailed), Payload: model.VarsSyncFailedPayload{}},
			{Type: string(model.NotificationVarsReloaded), Payload: command.VarsReloadedPayload{}},
		},
	}}
```
**with:**

```go
			{Type: string(model.NotificationVarsSyncFailed), Payload: model.VarsSyncFailedPayload{}},
			{Type: string(model.NotificationVarsReloaded), Payload: command.VarsReloadedPayload{}},
			{Type: string(model.NotificationRollout), Payload: model.RolloutView{}},
		},
	}}
```

**Replace in** `internal/app/web/routes.go`:

```go
	"winterflow/internal/app/web/handler/notification"
	horg "winterflow/internal/app/web/handler/org"
	"winterflow/internal/app/web/handler/server"
	hsharedvars "winterflow/internal/app/web/handler/sharedvars"
```
**with:**

```go
	"winterflow/internal/app/web/handler/notification"
	horg "winterflow/internal/app/web/handler/org"
	hrollouts "winterflow/internal/app/web/handler/rollouts"
	"winterflow/internal/app/web/handler/server"
	hsharedvars "winterflow/internal/app/web/handler/sharedvars"
```

**Replace in** `internal/app/web/routes.go`:

```go
		ServerStorageRepository: d.ServerStorageRepository,
		StatusCache:             d.StatusCache,
	})

	routes := []api.Route{{
```
**with:**

```go
		ServerStorageRepository: d.ServerStorageRepository,
		StatusCache:             d.StatusCache,
		Rollouts:                d.Rollouts,
	})
	rolloutsAPI := hrollouts.NewHandler(&hrollouts.Deps{Logger: d.Log, Users: d.UserService, Rollouts: d.Rollouts})

	routes := []api.Route{{
```

**Replace in** `internal/app/web/routes.go`:

```go
		sharedVarsAPI.Routes(g),
		templatesAPI.Routes(g),
		authprvd.Routes(),
	} {
```
**with:**

```go
		sharedVarsAPI.Routes(g),
		templatesAPI.Routes(g),
		rolloutsAPI.Routes(g),
		authprvd.Routes(),
	} {
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -w internal/app/web/routes.go && go build ./... && make openapi && go test -count=1 ./internal/app/web -run TestRoutesGolden -update && go test -count=1 ./internal/app/web/... 2>&1 | grep -v "no test files" | grep -v "^ok" ; git diff --stat api/openapi.yaml internal/app/web/testdata/routes.golden
```

Expected: no failing package listed; `api/openapi.yaml` gains the three routes, `RolloutView`, `RolloutList`, `UpgradeInstancesRequest`, `RolloutIDRequest` and the `rollout` event payload; `routes.golden` three lines.

- [ ] **Step 4: Contract tests and the parity transcript**

**Create** `internal/app/web/contract/rollouts_test.go`:

```go
package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	hrollouts "winterflow/internal/app/web/handler/rollouts"
	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
)

// reportRunning plays the agent's status reporter until the test ends:
// every 50ms an apps.status of the server with apps() running (and stale()
// as their stale names, when set), at the shared-variables revision rev().
// A rollout step waits for a report received after it.
func reportRunning(t *testing.T, e *env, serverID string, apps func() []string, stale func(appID string) []string, rev func() int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(e.ctx)
	t.Cleanup(cancel)
	go func() {
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			report := command.GetAppsStatusResponse{Apps: []command.AppStatus{}}
			for _, id := range apps() {
				st := command.AppStatus{AppID: id, StatusCode: command.ContainerStatusActive,
					Containers: []command.ContainerStatus{{ContainerID: "c-" + id, Name: "app", StatusCode: command.ContainerStatusActive}}}
				if stale != nil {
					st.StaleVars = stale(id)
				}
				report.Apps = append(report.Apps, st)
			}
			if rev != nil {
				r := rev()
				report.VarsRevision = &r
			}
			body, _ := json.Marshal(report)
			_ = e.bus.Publish(ctx, e.cfg.GetBusEventsQueue(), bus.EventMessage{ServerID: serverID, Kind: bus.EventAppsStatus, Payload: body})
		}
	}()
}

// fixed is a constant app list for reportRunning.
func fixed(ids ...string) func() []string { return func() []string { return ids } }

// awaitRollout waits for the rollout event of id in state.
func awaitRollout(t *testing.T, c *client, id, state string) model.RolloutView {
	t.Helper()
	var v model.RolloutView
	awaitEvent(t, c, "rollout "+id+" "+state, func(ev event) bool {
		var got model.RolloutView
		if ev.Type != model.NotificationRollout || json.Unmarshal(ev.Payload, &got) != nil || got.ID != id || got.State != state {
			return false
		}
		v = got
		return true
	})
	return v
}

// rolloutsOf reads get-rollouts.
func rolloutsOf(t *testing.T, c *client, q url.Values) []model.RolloutView {
	t.Helper()
	var list hrollouts.RolloutList
	c.get(t, "/api/v1/rollout/get-rollouts", q).expect(t, http.StatusOK).data(t, &list)
	return list.Rollouts
}

func TestUpgradeAllRollout(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	a := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
	b := ie.created(t, "tenant-2", "bolt", "9002").Instance.AppID
	reportRunning(t, e, sid, fixed(a, b), nil, nil)
	saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1})

	var queued model.RolloutView
	owner.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID}).expect(t, http.StatusOK).data(t, &queued)
	if queued.State != "queued" || queued.Kind != model.RolloutTemplateUpgrade || queued.ScopeKey != ie.tplID || queued.Revision != 2 {
		t.Fatalf("queued = %+v", queued)
	}
	t.Run("one upgrade of a template at a time", func(t *testing.T) {
		owner.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID, Revision: 2}).expect(t, http.StatusConflict)
	})
	done := awaitRollout(t, owner, queued.ID, "done")
	if done.Done != 2 || done.Skipped != 0 || done.Total != 2 || done.FinishedAt == nil {
		t.Fatalf("done = %+v", done)
	}
	t.Run("every instance is on the new revision", func(t *testing.T) {
		for _, key := range []string{"tenant-1", "tenant-2"} {
			if v := instanceOf(t, owner, sid, key); v.Revision != 2 || v.State != "created" {
				t.Fatalf("%s = %+v", key, v)
			}
		}
	})
	t.Run("get-rollouts lists it", func(t *testing.T) {
		list := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutTemplateUpgrade}, "scope_key": {ie.tplID}})
		if len(list) != 1 || list[0].ID != queued.ID || list[0].State != "done" {
			t.Fatalf("list = %+v", list)
		}
		owner.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {"bogus"}}).expect(t, http.StatusBadRequest)
	})
	t.Run("another org's template and rollout are unknown", func(t *testing.T) {
		mallory, _ := e.foreignClient(t)
		mallory.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID}).expect(t, http.StatusNotFound)
		mallory.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {model.RolloutTemplateUpgrade}, "scope_key": {ie.tplID}}).
			expect(t, http.StatusNotFound)
		mallory.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: queued.ID}).expect(t, http.StatusNotFound)
		if list := rolloutsOf(t, mallory, url.Values{"kind": {model.RolloutTemplateUpgrade}}); len(list) != 0 {
			t.Fatalf("mallory sees %+v", list)
		}
	})
}

func TestStopRollout(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	a := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID
	b := ie.created(t, "tenant-2", "bolt", "9002").Instance.AppID
	reportRunning(t, e, sid, fixed(a, b), nil, nil)
	saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1})

	// The first upgrade's save is held until Stop was asked.
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	e.agent.on(command.TypeAppSave, func(raw json.RawMessage) reply {
		<-release
		return reply{Payload: command.SaveAppResponse{AppID: decode[command.SaveAppRequest](raw).App.AppID, Revision: "rev-2"}}
	})
	var queued model.RolloutView
	owner.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: ie.tplID}).expect(t, http.StatusOK).data(t, &queued)
	awaitEvent(t, owner, "the first step", func(ev event) bool {
		var v model.RolloutView
		return ev.Type == model.NotificationRollout && json.Unmarshal(ev.Payload, &v) == nil && v.ID == queued.ID && v.CurrentAppID == a
	})
	var stopping model.RolloutView
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: queued.ID}).expect(t, http.StatusOK).data(t, &stopping)
	if !stopping.StopRequested || stopping.State != "running" {
		t.Fatalf("stopping = %+v", stopping)
	}
	free()
	stopped := awaitRollout(t, owner, queued.ID, "stopped")
	if stopped.Done != 1 || stopped.CurrentAppID != "" {
		t.Fatalf("stopped = %+v", stopped)
	}
	if v := instanceOf(t, owner, sid, "tenant-2"); v.Revision != 1 {
		t.Fatalf("tenant-2 = %+v: nothing follows a stop", v)
	}
	// Stopping an ended rollout answers it as it is; a malformed id is 400.
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: queued.ID}).expect(t, http.StatusOK)
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: "nope"}).expect(t, http.StatusBadRequest)
}
```

**Replace in** `internal/app/web/contract/matrix_test.go`:

```go
	}, malformed: bad("POST", "/api/v1/shared-variables/reload")},

	"GET /api/v1/template/get-template":     {malformed: bad("GET", "/api/v1/template/get-template")},
	"POST /api/v1/template/save-template":   {malformed: bad("POST", "/api/v1/template/save-template")},
	"POST /api/v1/template/delete-template": {malformed: bad("POST", "/api/v1/template/delete-template")},

	// The template and key do not exist, so the own-server control answers
```
**with:**

```go
	}, malformed: bad("POST", "/api/v1/shared-variables/reload")},

	"GET /api/v1/template/get-template":       {malformed: bad("GET", "/api/v1/template/get-template")},
	"POST /api/v1/template/save-template":     {malformed: bad("POST", "/api/v1/template/save-template")},
	"POST /api/v1/template/delete-template":   {malformed: bad("POST", "/api/v1/template/delete-template")},
	"POST /api/v1/template/upgrade-instances": {malformed: bad("POST", "/api/v1/template/upgrade-instances")},
	"GET /api/v1/rollout/get-rollouts":        {malformed: bad("GET", "/api/v1/rollout/get-rollouts")},
	"POST /api/v1/rollout/stop-rollout":       {malformed: bad("POST", "/api/v1/rollout/stop-rollout")},

	// The template and key do not exist, so the own-server control answers
```

**Replace in** `internal/app/web/contract/parity_test.go`:

```go

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/app/web/handler/server"
	hsv "winterflow/internal/app/web/handler/sharedvars"
```
**with:**

```go

	happ "winterflow/internal/app/web/handler/app"
	hrollouts "winterflow/internal/app/web/handler/rollouts"
	"winterflow/internal/app/web/handler/server"
	hsv "winterflow/internal/app/web/handler/sharedvars"
```

**Replace in** `internal/app/web/contract/parity_test.go`:

```go
	})).expect(t, http.StatusOK)
	rec("list templates", p.get(t, "/api/v1/template/get-templates", nil)).expect(t, http.StatusOK)

	var buf bytes.Buffer
```
**with:**

```go
	})).expect(t, http.StatusOK)
	rec("list templates", p.get(t, "/api/v1/template/get-templates", nil)).expect(t, http.StatusOK)
	// The only instance is on the latest revision: the upgrade ends at once.
	rec("upgrade all instances", p.post(t, "/api/v1/template/upgrade-instances", htpl.UpgradeInstancesRequest{ID: tpl.ID})).
		expect(t, http.StatusOK)
	rq := url.Values{"kind": {model.RolloutTemplateUpgrade}, "scope_key": {tpl.ID}}
	eventually(t, func() bool {
		var list hrollouts.RolloutList
		p.get(t, "/api/v1/rollout/get-rollouts", rq).expect(t, http.StatusOK).data(t, &list)
		return len(list.Rollouts) == 1 && list.Rollouts[0].State == string(model.RolloutDone)
	})
	rec("list rollouts", p.get(t, "/api/v1/rollout/get-rollouts", rq)).expect(t, http.StatusOK)

	var buf bytes.Buffer
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test -count=1 ./internal/app/web/contract -run TestParity
```

Expected: FAIL (transcript differs: `upgrade all instances` and `list rollouts` appended).

Run:

```bash
go test -count=1 ./internal/app/web/contract -run TestParity -update && git diff internal/app/web/contract/testdata/parity.golden.json | head -40
```

Expected: two steps appended: `upgrade all instances` (200, `state: "queued"`, `revision: 1`, `total: 0`, `finished_at: null`, message `upgrade queued`) and `list rollouts` (200, one rollout with `state: "done"`, `finished_at: "<time>Z"`).

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal; go vet ./... && WF_CONTRACT_GUARD=require go test -count=1 ./...
```

Expected: no gofmt output; every package `ok`; the contract guard lists no route without a happy path.

Run:

```bash
make test-pg TEST_PG_ARGS='-run "TestParity|TestUpgradeAllRollout|TestStopRollout"'
```

Expected: `ok` on postgres:16 (the golden is the same on both databases).

- [ ] **Commit**

```bash
git add internal/infra/bootstrap internal/app/web api/openapi.yaml
git commit -m "feat(api): upgrade all instances, list and stop rollouts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: `vars_reload` — shared-variable reloads move onto the queue

**Files:**
- Create: `internal/domain/usecase/rollouts/reload.go`, `reload_test.go`
- Modify: `internal/domain/usecase/rollouts/usecase.go` (the kinds map)
- Modify: `internal/domain/usecase/sharedvars/{usecase.go,sync.go,usecase_test.go}`, `internal/infra/bootstrap/varssync.go`
- Modify: `internal/app/web/handler/sharedvars/{handler.go,routes.go}`, `internal/app/web/routes.go`
- Modify: `internal/app/web/contract/shared_variables_test.go`, `internal/app/web/contract/rollouts_test.go`
- Generated: `api/openapi.yaml`

**Interfaces:**
- Consumes: `model.ReloadScope` (Task 1), `LatestReport` (Task 3), the single-app `vars.reload` (Task 4), the worker (Task 5), the routes (Task 7).
- Produces: `(*rollouts.UseCase).QueueReload(ctx, userID, orgID string, s model.ReloadScope) error` (insert or extend; sets `SettleBy`); the `vars_reload` runner; `sharedvars.ReloadQueue` (`QueueReload`), `sharedvars.Deps.Reloads`; signature changes `DeleteOrg(ctx, userID, orgID, id, reload)`, `DeleteServer(ctx, userID, serverID, id, reload)`, `Reload(ctx, userID, orgID, in)`, `Sync(ctx, serverID)` (no reload names); `dispatchReload` is removed (the API never sends a bulk `vars.reload` any more); `hsharedvars.Deps.Reloads`.

- [ ] **Step 1: Write the failing usecase tests**

**Create** `internal/domain/usecase/rollouts/reload_test.go`:

```go
package rollouts

import (
	"context"
	"slices"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/infra/db/dbtest"
)

// stale is a running app stale for names.
func stale(appID string, names ...string) command.AppStatus {
	return command.AppStatus{AppID: appID, StatusCode: command.ContainerStatusActive, StaleVars: names,
		Containers: []command.ContainerStatus{{StatusCode: command.ContainerStatusActive}}}
}

// reloadEnv is an env whose agent has apps on srv1 at shared-variables
// revision 3, reported once.
func reloadEnv(t *testing.T, apps ...command.AppStatus) (*env, *fakeAgent) {
	t.Helper()
	e := newEnv(t)
	a, _ := e.withAgent(t)
	a.mu.Lock()
	a.varsRev[srv1] = 3
	a.mu.Unlock()
	for _, st := range apps {
		a.set(srv1, st)
	}
	a.report(srv1)
	return e, a
}

// smtpOnSrv1 is the scope a save of SMTP_PASS with reload makes on srv1.
func smtpOnSrv1() model.ReloadScope {
	return model.ReloadScope{Names: []string{"SMTP_PASS"}, ServerIDs: []string{srv1}, Revisions: map[string]int64{srv1: 3}}
}

// activeReload is the org's active vars_reload rollout.
func (e *env) activeReload(t *testing.T, orgID string) model.Rollout {
	t.Helper()
	r, err := e.repo.GetActiveRollout(dbtest.Ctx(t), model.RolloutVarsReload, orgID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestQueueReloadQueuesOnceThenExtends(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	first := e.activeReload(t, org1)
	more := model.ReloadScope{Names: []string{"S3_KEY"}, ServerIDs: []string{srv2}, AllStale: []string{srv2}, Revisions: map[string]int64{srv1: 4, srv2: 1}}
	if err := e.uc.QueueReload(ctx, "u2", org1, more); err != nil {
		t.Fatal(err)
	}
	got := e.activeReload(t, org1)
	s, _ := reloadScope(got)
	if got.ID != first.ID || got.ScopeRev != 2 || got.CreatedBy != "u1" || got.ScopeKey != org1 ||
		!slices.Equal(s.Names, []string{"S3_KEY", "SMTP_PASS"}) || !slices.Equal(s.Servers(), []string{srv1, srv2}) ||
		s.Revisions[srv1] != 4 || s.Revisions[srv2] != 1 || s.SettleBy.IsZero() {
		t.Fatalf("extended = %+v, scope %+v", got, s)
	}
	// Another org has its own.
	if err := e.uc.QueueReload(ctx, "u9", org2, model.ReloadScope{AllStale: []string{srv2}}); err != nil {
		t.Fatal(err)
	}
	if other := e.activeReload(t, org2); other.ID == first.ID {
		t.Fatal("org2 joined org1's rollout")
	}
}

func TestReloadRolloutTakesTheStaleRunningApps(t *testing.T) {
	stopped := stale("a3", "SMTP_PASS")
	stopped.StatusCode, stopped.Containers = command.ContainerStatusStopped, []command.ContainerStatus{{StatusCode: command.ContainerStatusStopped}}
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"), stale("a2", "OTHER"), stopped, stale("a4", "SMTP_PASS", "OTHER"))
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	r := e.get(t, e.pub.events()[0].ID)
	if r.State != model.RolloutDone || r.Done != 2 || r.Total != 2 {
		t.Fatalf("rollout = %+v", r)
	}
	// a2 is stale for another name, a3 is stopped.
	if got := a.commands(); !slices.Equal(got, []string{"vars.reload a1", "vars.reload a4"}) {
		t.Fatalf("commands = %v", got)
	}
}

func TestReloadWaitsForTheServerToReportTheChange(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"))
	ctx := dbtest.Ctx(t)
	scope := smtpOnSrv1()
	scope.Revisions[srv1] = 4 // the save bumped it; the agent still reports 3
	if err := e.uc.QueueReload(ctx, "u1", org1, scope); err != nil {
		t.Fatal(err)
	}
	id := e.activeReload(t, org1).ID
	passed := make(chan struct{})
	go func() { e.uc.pass(context.Background()); close(passed) }()
	time.Sleep(50 * time.Millisecond)
	if got := a.commands(); len(got) != 0 {
		t.Fatalf("reloaded before the server reported the change: %v", got)
	}
	a.mu.Lock()
	a.varsRev[srv1] = 4
	a.mu.Unlock()
	a.report(srv1)
	<-passed
	if r := e.get(t, id); r.State != model.RolloutDone || r.Done != 1 {
		t.Fatalf("rollout = %+v", r)
	}

	// A server that never reports the change is left out after SettleBy.
	if err := e.uc.QueueReload(ctx, "u1", org1, model.ReloadScope{Names: []string{"X"}, ServerIDs: []string{srv2}, Revisions: map[string]int64{srv2: 9}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	e.uc.pass(ctx)
	if r := e.activeOrLast(t, org1); r.State != model.RolloutDone || r.Done != 0 || time.Since(start) < e.uc.settle/2 {
		t.Fatalf("unsettled rollout = %+v after %s", r, time.Since(start))
	}
}

// activeOrLast is the org's newest vars_reload rollout.
func (e *env) activeOrLast(t *testing.T, orgID string) model.Rollout {
	t.Helper()
	list, err := e.repo.ListRollouts(dbtest.Ctx(t), orgID, model.RolloutVarsReload, orgID)
	if err != nil || len(list) == 0 {
		t.Fatalf("list = %v, %v", list, err)
	}
	return list[0]
}

func TestReloadSkipsAStoppedAppAndStopsAtAFailure(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"), stale("a2", "SMTP_PASS"), stale("a3", "SMTP_PASS"))
	ctx := dbtest.Ctx(t)
	a.mu.Lock()
	a.skip["a1"] = true // stopped by the time the agent got to it
	a.fail["a2"] = "reload after a change to SMTP_PASS failed: docker compose up: exit status 1"
	a.mu.Unlock()
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	r := e.activeOrLast(t, org1)
	if r.State != model.RolloutFailed || r.Skipped != 1 || !slices.Equal(r.SkipIDs, []string{"a1"}) || r.CurrentAppID != "a2" ||
		r.Error != "reload after a change to SMTP_PASS failed: docker compose up: exit status 1" {
		t.Fatalf("rollout = %+v", r)
	}
	if got := a.commands(); !slices.Equal(got, []string{"vars.reload a1", "vars.reload a2"}) {
		t.Fatalf("commands = %v", got)
	}
}

func TestReloadThatLeavesTheAppStaleFails(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"))
	a.mu.Lock()
	a.stuck["a1"] = true
	a.mu.Unlock()
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.pass(ctx)
	if r := e.activeOrLast(t, org1); r.State != model.RolloutFailed || r.Error != "the app is still stale after the reload: SMTP_PASS" {
		t.Fatalf("rollout = %+v", r)
	}
	if got := a.commands(); len(got) != 1 {
		t.Fatalf("commands = %v: the app must not be reloaded in a loop", got)
	}
}

func TestAnExtensionLandingAtTheEndIsNotLost(t *testing.T) {
	e, a := reloadEnv(t, stale("a1", "SMTP_PASS"), stale("b1", "S3_KEY"))
	ctx := dbtest.Ctx(t)
	if err := e.uc.QueueReload(ctx, "u1", org1, smtpOnSrv1()); err != nil {
		t.Fatal(err)
	}
	e.uc.d.Repo = &completeHook{RolloutRepository: e.repo, before: func() {
		// S3_KEY was saved with reload while the worker found nothing left.
		if err := e.uc.QueueReload(ctx, "u1", org1, model.ReloadScope{Names: []string{"S3_KEY"}, ServerIDs: []string{srv1}}); err != nil {
			t.Error(err)
		}
	}}
	e.uc.pass(ctx)
	if r := e.activeOrLast(t, org1); r.State != model.RolloutDone || r.Done != 2 || r.ScopeRev != 2 {
		t.Fatalf("rollout = %+v", r)
	}
	if got := a.commands(); !slices.Equal(got, []string{"vars.reload a1", "vars.reload b1"}) {
		t.Fatalf("commands = %v", got)
	}
}

func TestAnUnreadableScopeFailsTheRollout(t *testing.T) {
	e := newEnv(t)
	id := dbtest.ID(301)
	if _, ok, err := e.uc.queue(dbtest.Ctx(t), model.Rollout{ID: id, OrgID: org1, Kind: model.RolloutVarsReload, Scope: []byte("{"), ScopeKey: org1}); err != nil || !ok {
		t.Fatalf("queue: %v %v", ok, err)
	}
	e.uc.pass(dbtest.Ctx(t))
	if r := e.get(t, id); r.State != model.RolloutFailed || r.Error != errBadScope.Error() {
		t.Fatalf("rollout = %+v", r)
	}
}

// completeHook runs before() once, right before the first Complete.
type completeHook struct {
	port.RolloutRepository
	before func()
	ran    bool
}

func (c *completeHook) Complete(ctx context.Context, id, owner string, rev int64) (bool, error) {
	if !c.ran {
		c.ran = true
		c.before()
	}
	return c.RolloutRepository.Complete(ctx, id, owner, rev)
}
```

**Replace in** `internal/domain/usecase/sharedvars/usecase_test.go`:

```go
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

```
**with:**

```go
}

// queued is one reload put on the rollout queue.
type queued struct {
	user, org string
	scope     model.ReloadScope
}

type fakeQueue struct {
	mu  sync.Mutex
	got []queued
}

func (f *fakeQueue) QueueReload(_ context.Context, userID, orgID string, s model.ReloadScope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, queued{userID, orgID, s})
	return nil
}

// last is the last reload queued.
func (f *fakeQueue) last(t *testing.T) queued {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("no reload queued")
	}
	return f.got[len(f.got)-1]
}

type env struct {
	uc    *sharedvars.UseCase
	d     *db.DB
	repo  *sharedvar.Repo
	disp  *fakeDispatcher
	pub   *fakePublisher
	revs  *fakeRevisions
	queue *fakeQueue
	caps  map[string]string // server -> stored public key
}

```

**Replace in** `internal/domain/usecase/sharedvars/usecase_test.go`:

```go
	}
	e := &env{d: d, repo: repo, disp: &fakeDispatcher{}, pub: &fakePublisher{},
		revs: &fakeRevisions{revs: map[string]int64{}}, caps: map[string]string{}}
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
```
**with:**

```go
	}
	e := &env{d: d, repo: repo, disp: &fakeDispatcher{}, pub: &fakePublisher{},
		revs: &fakeRevisions{revs: map[string]int64{}}, queue: &fakeQueue{}, caps: map[string]string{}}
	dbtest.SeedOrg(t, d, org1)
	dbtest.SeedOrg(t, d, org2)
```

**Replace in** `internal/domain/usecase/sharedvars/usecase_test.go`:

```go
	e.seedServer(t, org2, srvX, "foreign", "KEY-X")
	e.uc = sharedvars.New(&sharedvars.Deps{Repo: repo, Servers: fakeServers{e}, Dispatcher: e.disp,
		Notifications: e.pub, Revisions: e.revs, Log: dbtest.Log})
	return e
}
```
**with:**

```go
	e.seedServer(t, org2, srvX, "foreign", "KEY-X")
	e.uc = sharedvars.New(&sharedvars.Deps{Repo: repo, Servers: fakeServers{e}, Dispatcher: e.disp,
		Notifications: e.pub, Revisions: e.revs, Reloads: e.queue, Log: dbtest.Log})
	return e
}
```

**Replace in** `internal/domain/usecase/sharedvars/usecase_test.go`:

```go
}

func TestDeleteAndReloadAfterSync(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
```
**with:**

```go
}

func TestDeleteQueuesTheReload(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
```

**Replace in** `internal/domain/usecase/sharedvars/usecase_test.go`:

```go
		t.Fatal(err)
	}
	invalid(t, e.uc.DeleteOrg(ctx, org2, v.ID, false), "not found")
	invalid(t, e.uc.DeleteServer(ctx, srvA, v.ID, false), "not found")
	before := len(e.disp.of(command.TypeVarsSync))
	if err := e.uc.DeleteOrg(ctx, org1, v.ID, true); err != nil {
		t.Fatal(err)
	}
```
**with:**

```go
		t.Fatal(err)
	}
	invalid(t, e.uc.DeleteOrg(ctx, "u2", org2, v.ID, false), "not found")
	invalid(t, e.uc.DeleteServer(ctx, "u1", srvA, v.ID, false), "not found")
	before := len(e.disp.of(command.TypeVarsSync))
	if err := e.uc.DeleteOrg(ctx, "u1", org1, v.ID, true); err != nil {
		t.Fatal(err)
	}
```

**Replace in** `internal/domain/usecase/sharedvars/usecase_test.go`:

```go
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
```
**with:**

```go
		t.Fatal("the deleted variable is still in the snapshot")
	}
	// The reload goes on the org's rollout, at the revision the delete
	// bumped every server to; nothing is sent to the agents directly.
	q := e.queue.last(t)
	want := model.ReloadScope{Names: []string{"SMTP_HOST"}, ServerIDs: []string{srvA, srvB, srvP},
		Revisions: map[string]int64{srvA: 2, srvB: 2, srvP: 2}}
	if q.user != "u1" || q.org != org1 || !reflect.DeepEqual(q.scope, want) {
		t.Fatalf("queued = %+v, want %+v", q, want)
	}
	answer(t, syncA, true, "", command.SyncVarsResponse{Revision: 2, Undecryptable: []command.VarRef{}})
	if n := len(e.disp.of(command.TypeVarsReload)); n != 0 {
		t.Fatalf("%d vars.reload sent directly", n)
	}
}

func TestSaveWithReloadQueuesOnlyWhenSomethingChanged(t *testing.T) {
	e := newEnv(t)
	ctx := dbtest.Ctx(t)
	if _, err := e.uc.SaveServer(ctx, "u1", srvB, sharedvars.SaveInput{Name: "LOCAL", Value: ptr("x"), Reload: true}); err != nil {
		t.Fatal(err)
	}
	q := e.queue.last(t)
	if !reflect.DeepEqual(q.scope, model.ReloadScope{Names: []string{"LOCAL"}, ServerIDs: []string{srvB}, Revisions: map[string]int64{srvB: 1}}) {
		t.Fatalf("queued = %+v", q)
	}
	// A description-only edit changes no server: nothing to reload.
	if _, err := e.uc.SaveServer(ctx, "u1", srvB, sharedvars.SaveInput{Name: "LOCAL", Value: ptr("x"), Description: "d", Reload: true}); err != nil {
		t.Fatal(err)
	}
	if n := len(e.queue.got); n != 1 {
		t.Fatalf("queued %d reloads", n)
	}
}
```

**Replace in** `internal/domain/usecase/sharedvars/usecase_test.go`:

```go
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
```
**with:**

```go
		t.Fatalf("resync by variable = %v", got)
	}
	_, err = e.uc.Reload(ctx, "u1", org1, sharedvars.ReloadInput{})
	invalid(t, err, "required")
	_, err = e.uc.Reload(ctx, "u1", org1, sharedvars.ReloadInput{ServerID: srvA, AppIDs: []string{"../x"}})
	invalid(t, err, "invalid app id")
	if _, err := e.uc.Reload(ctx, "u1", org1, sharedvars.ReloadInput{ServerID: srvX}); !errors.Is(err, sharedvars.ErrForbidden) {
		t.Fatalf("foreign reload err = %v", err)
	}
	cases := []struct {
		in      sharedvars.ReloadInput
		servers []string
		scope   model.ReloadScope
	}{
		{sharedvars.ReloadInput{ServerID: srvA, AppIDs: []string{"a1"}}, []string{srvA},
			model.ReloadScope{AppIDs: []string{"a1"}, ServerIDs: []string{srvA}, Revisions: map[string]int64{srvA: 0}}},
		{sharedvars.ReloadInput{ServerID: srvA, Names: []string{"SMTP_PASS"}}, []string{srvA},
			model.ReloadScope{Names: []string{"SMTP_PASS"}, ServerIDs: []string{srvA}, Revisions: map[string]int64{srvA: 0}}},
		{sharedvars.ReloadInput{ServerID: srvA}, []string{srvA},
			model.ReloadScope{AllStale: []string{srvA}, Revisions: map[string]int64{srvA: 0}}},
		{sharedvars.ReloadInput{VariableID: local.ID}, []string{srvB},
			model.ReloadScope{Names: []string{"LOCAL"}, ServerIDs: []string{srvB}, Revisions: map[string]int64{srvB: 1}}},
	}
	for _, tc := range cases {
		got, err := e.uc.Reload(ctx, "u1", org1, tc.in)
		if q := e.queue.last(t); err != nil || !reflect.DeepEqual(got, tc.servers) || !reflect.DeepEqual(q.scope, tc.scope) || q.org != org1 {
			t.Errorf("%+v: servers %v, queued %+v, %v", tc.in, got, q, err)
		}
	}
	if n := len(e.disp.of(command.TypeVarsReload)); n != 0 {
		t.Fatalf("%d vars.reload sent directly", n)
	}
	// Without a queue a reload is refused, not silently dropped.
	bare := sharedvars.New(&sharedvars.Deps{Repo: e.repo, Servers: fakeServers{e}, Dispatcher: e.disp, Log: dbtest.Log})
	if _, err := bare.Reload(ctx, "u1", org1, sharedvars.ReloadInput{ServerID: srvA}); err == nil {
		t.Fatal("a reload without a queue succeeded")
	}
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
go test ./internal/domain/usecase/rollouts/ ./internal/domain/usecase/sharedvars/
```

Expected: FAIL to compile: `e.uc.QueueReload undefined`, `unknown field Reloads in struct literal`, `too many arguments in call to e.uc.DeleteOrg`.

- [ ] **Step 2: The `vars_reload` kind**

**Create** `internal/domain/usecase/rollouts/reload.go`:

```go
package rollouts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/util"
)

// extendTries bounds how often QueueReload retries a lost race (another
// request extended the scope, or the rollout ended, in between).
const extendTries = 5

// QueueReload queues the org's vars_reload rollout for s, or extends the
// active one: its scope becomes the union, written in one conditional
// update (the next app is always chosen from live staleness, so a running
// rollout can take more). SettleBy is set here: servers get settleWait to
// report the change.
func (uc *UseCase) QueueReload(ctx context.Context, userID, orgID string, s model.ReloadScope) error {
	s.SettleBy = uc.now().Add(uc.settle).UTC()
	for range extendTries {
		active, err := uc.d.Repo.GetActiveRollout(ctx, model.RolloutVarsReload, orgID)
		if errors.Is(err, model.ErrRolloutNotFound) {
			raw, err := json.Marshal(s)
			if err != nil {
				return err
			}
			_, ok, err := uc.queue(ctx, model.Rollout{
				ID: util.GenerateID(), OrgID: orgID, Kind: model.RolloutVarsReload, Scope: raw, ScopeKey: orgID, CreatedBy: userID,
			})
			if err != nil || ok {
				return err
			}
			continue // queued by another request meanwhile: extend that one
		}
		if err != nil {
			return err
		}
		cur, err := reloadScope(active)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(cur.Merge(s))
		if err != nil {
			return err
		}
		ok, err := uc.d.Repo.ExtendRollout(ctx, active.ID, active.ScopeRev, raw)
		if err != nil {
			return err
		}
		if ok {
			uc.Wake()
			return nil
		}
	}
	return errors.New("queue the reload: the reload rollout kept changing; try again")
}

// reloader is the vars_reload kind.
type reloader struct{ uc *UseCase }

func reloadScope(r model.Rollout) (model.ReloadScope, error) {
	var s model.ReloadScope
	if err := json.Unmarshal(r.Scope, &s); err != nil {
		return model.ReloadScope{}, errBadScope
	}
	return s, nil
}

// candidate is a stale running app a vars_reload may take next.
type candidate struct {
	serverID, appID string
	rev             int64
}

// next is the first stale running app the scope wants, by server and app
// id, from the status cache. A server whose latest report predates the
// change (its revision is behind the scope's) does not count yet: until
// SettleBy the rollout waits for it, then leaves it out.
func (k reloader) next(_ context.Context, r model.Rollout) (pick, error) {
	s, err := reloadScope(r)
	if err != nil {
		return pick{}, err
	}
	now := k.uc.now()
	waiting := false
	var todo []candidate
	for _, serverID := range s.Servers() {
		rep, ok := k.uc.d.Status.LatestReport(serverID, now)
		if !ok || (rep.HasRevision && rep.VarsRevision < s.Revisions[serverID]) {
			waiting = waiting || now.Before(s.SettleBy)
			continue
		}
		for _, a := range rep.Apps {
			if !slices.Contains(r.SkipIDs, a.AppID) && running(a) && s.Wants(serverID, a.AppID, a.StaleVars) {
				todo = append(todo, candidate{serverID: serverID, appID: a.AppID, rev: rep.VarsRevision})
			}
		}
	}
	if len(todo) == 0 {
		return pick{wait: waiting}, nil
	}
	slices.SortFunc(todo, func(a, b candidate) int {
		return strings.Compare(a.serverID+"/"+a.appID, b.serverID+"/"+b.appID)
	})
	c := todo[0]
	return pick{appID: c.appID, serverID: c.serverID, skipID: c.appID, remaining: int64(len(todo)), revSeen: c.rev}, nil
}

// run sends the single-app vars.reload and waits for its outcome (the
// agent answers when the reload ended) and for the app to run. An app the
// agent found stopped is skipped. One still stale for the scope at the
// same snapshot afterwards fails the step instead of being picked again.
func (k reloader) run(ctx context.Context, r model.Rollout, p pick) stepResult {
	results := make(chan port.CommandResult, 1)
	if _, err := k.uc.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: p.serverID, UserID: r.CreatedBy, Type: command.TypeVarsReload,
		Payload: command.ReloadVarsRequest{AppIDs: []string{p.appID}}, Wait: k.uc.cmdWait,
		OnResult: func(res port.CommandResult) {
			select {
			case results <- res:
			default:
			}
		},
	}); err != nil {
		return failed(err.Error())
	}
	res, err := k.uc.awaitResult(ctx, results)
	if err != nil {
		return failed(err.Error())
	}
	var out command.ReloadVarsResponse
	if json.Unmarshal(res.Payload, &out) == nil && out.Skipped {
		return skipped()
	}
	st, err := k.uc.awaitRunning(ctx, p.serverID, p.appID, k.uc.now())
	if err != nil {
		return failed(err.Error())
	}
	s, err := reloadScope(r)
	if err != nil {
		return failed(err.Error())
	}
	if s.Wants(p.serverID, p.appID, st.StaleVars) {
		if rep, ok := k.uc.d.Status.LatestReport(p.serverID, k.uc.now()); ok && rep.VarsRevision == p.revSeen {
			return failed("the app is still stale after the reload: " + strings.Join(st.StaleVars, ", "))
		}
	}
	return done()
}

// running reports whether one of the app's containers runs.
func running(a command.AppStatus) bool {
	for _, c := range a.Containers {
		if c.StatusCode == command.ContainerStatusActive {
			return true
		}
	}
	return false
}
```

**Replace in** `internal/domain/usecase/rollouts/usecase.go`:

```go
		health: healthWait, healthPoll: healthPoll, settle: settleWait, settlePoll: settlePoll,
	}
	uc.kinds = map[string]runner{model.RolloutTemplateUpgrade: upgrader{uc}}
	return uc
}
```
**with:**

```go
		health: healthWait, healthPoll: healthPoll, settle: settleWait, settlePoll: settlePoll,
	}
	uc.kinds = map[string]runner{model.RolloutTemplateUpgrade: upgrader{uc}, model.RolloutVarsReload: reloader{uc}}
	return uc
}
```
- [ ] **Step 3: Shared variables queue their reloads**

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
// stores plain values and per-server ciphertexts (never plaintext secrets),
// bumps the affected servers' vars_revision and pushes each one its full
// snapshot as vars.sync.
package sharedvars

```
**with:**

```go
// stores plain values and per-server ciphertexts (never plaintext secrets),
// bumps the affected servers' vars_revision and pushes each one its full
// snapshot as vars.sync. Reloads (reload: true, the reload route) queue or
// extend the org's vars_reload rollout, which reloads the stale running
// apps one at a time once each server reported the change.
package sharedvars

```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
}

type Deps struct {
	Repo          port.SharedVariableRepository
```
**with:**

```go
}

// ReloadQueue queues or extends an org's vars_reload rollout
// (*rollouts.UseCase).
type ReloadQueue interface {
	QueueReload(ctx context.Context, userID, orgID string, s model.ReloadScope) error
}

// errNoReloads answers a reload on a usecase built without a queue.
var errNoReloads = errors.New("reloads are not available")

type Deps struct {
	Repo          port.SharedVariableRepository
```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
	Notifications Publisher
	Revisions     RevisionCache
	Log           *logger.Logger
}

```
**with:**

```go
	Notifications Publisher
	Revisions     RevisionCache
	// Reloads is the rollout queue reloads go to (nil: the usecase only
	// syncs, as the event sink's does).
	Reloads ReloadQueue
	Log     *logger.Logger
}

```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
		return VariableView{}, err
	}
	u.syncAll(ctx, save.Bump, reloadNames(in.Reload, in.Name))
	return view(v, keyed), nil
}

// DeleteOrg removes an org-scope variable of orgID.
func (u *UseCase) DeleteOrg(ctx context.Context, orgID, id string, reload bool) error {
	v, err := u.lookup(ctx, orgID, id)
	if err != nil {
```
**with:**

```go
		return VariableView{}, err
	}
	u.syncAll(ctx, save.Bump)
	if in.Reload {
		u.queueReload(ctx, userID, orgID, in.Name, save.Bump)
	}
	return view(v, keyed), nil
}

// DeleteOrg removes an org-scope variable of orgID.
func (u *UseCase) DeleteOrg(ctx context.Context, userID, orgID, id string, reload bool) error {
	v, err := u.lookup(ctx, orgID, id)
	if err != nil {
```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
		return err
	}
	return u.remove(ctx, v, scopeServers(keys, nil), reload)
}

// DeleteServer removes a server-scope variable of serverID.
func (u *UseCase) DeleteServer(ctx context.Context, serverID, id string, reload bool) error {
	orgID, err := u.ServerOrg(ctx, serverID)
	if err != nil {
```
**with:**

```go
		return err
	}
	return u.remove(ctx, userID, v, scopeServers(keys, nil), reload)
}

// DeleteServer removes a server-scope variable of serverID.
func (u *UseCase) DeleteServer(ctx context.Context, userID, serverID, id string, reload bool) error {
	orgID, err := u.ServerOrg(ctx, serverID)
	if err != nil {
```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
		return invalid("shared variable not found")
	}
	return u.remove(ctx, v, []string{serverID}, reload)
}

```
**with:**

```go
		return invalid("shared variable not found")
	}
	return u.remove(ctx, userID, v, []string{serverID}, reload)
}

```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
}

func (u *UseCase) remove(ctx context.Context, v model.SharedVariable, bump []string, reload bool) error {
	if err := u.d.Repo.Delete(ctx, v.ID, bump); err != nil {
		return err
	}
	u.syncAll(ctx, bump, reloadNames(reload, v.Name))
	return nil
}

```
**with:**

```go
}

func (u *UseCase) remove(ctx context.Context, userID string, v model.SharedVariable, bump []string, reload bool) error {
	if err := u.d.Repo.Delete(ctx, v.ID, bump); err != nil {
		return err
	}
	u.syncAll(ctx, bump)
	if reload {
		u.queueReload(ctx, userID, v.OrgID, v.Name, bump)
	}
	return nil
}

// queueReload puts a saved or deleted variable's reload on the org's
// rollout, logging (not failing) an error: the change is committed and the
// servers' stale apps can still be reloaded by hand.
func (u *UseCase) queueReload(ctx context.Context, userID, orgID, name string, servers []string) {
	if len(servers) == 0 {
		return
	}
	scope, err := u.reloadScope(ctx, model.ReloadScope{Names: []string{name}, ServerIDs: servers})
	if err == nil {
		err = u.queue(ctx, userID, orgID, scope)
	}
	if err != nil {
		u.d.Log.Warn("queue the reload after a shared variable change", "name", name, "error", err)
	}
}

// reloadScope adds each server's current revision to s: the server's
// report must reach it before its stale apps count (the change travels
// as vars.sync first).
func (u *UseCase) reloadScope(ctx context.Context, s model.ReloadScope) (model.ReloadScope, error) {
	s.Revisions = map[string]int64{}
	for _, id := range s.Servers() {
		info, err := u.d.Repo.ServerVarsInfo(ctx, id)
		if err != nil {
			return model.ReloadScope{}, err
		}
		s.Revisions[id] = info.Revision
	}
	return s, nil
}

func (u *UseCase) queue(ctx context.Context, userID, orgID string, s model.ReloadScope) error {
	if u.d.Reloads == nil {
		return errNoReloads
	}
	return u.d.Reloads.QueueReload(ctx, userID, orgID, s)
}

```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
	}
	for _, id := range targets {
		if err := u.Sync(ctx, id, nil); err != nil {
			return nil, err
		}
```
**with:**

```go
	}
	for _, id := range targets {
		if err := u.Sync(ctx, id); err != nil {
			return nil, err
		}
```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
}

// Reload sends vars.reload to one server (with its names/app_ids filter)
// or, for a variable, to every server it reaches (filtered to its name).
func (u *UseCase) Reload(ctx context.Context, orgID string, in ReloadInput) ([]string, error) {
	if in.ServerID == "" && in.VariableID == "" {
		return nil, invalid("server_id or variable_id is required")
```
**with:**

```go
}

// Reload queues (or extends) the org's vars_reload rollout: for one server
// its stale running apps (or those stale for names, or the app ids), for a
// variable the apps stale for its name on every server it reaches. It
// returns the servers in scope.
func (u *UseCase) Reload(ctx context.Context, userID, orgID string, in ReloadInput) ([]string, error) {
	if in.ServerID == "" && in.VariableID == "" {
		return nil, invalid("server_id or variable_id is required")
```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
		return nil, &ValidationError{Msg: err.Error()}
	}
	for _, id := range targets {
		if err := u.dispatchReload(ctx, id, req); err != nil {
			return nil, err
		}
	}
	return targets, nil
```
**with:**

```go
		return nil, &ValidationError{Msg: err.Error()}
	}
	var s model.ReloadScope
	switch {
	case len(req.AppIDs) > 0:
		s = model.ReloadScope{AppIDs: req.AppIDs, ServerIDs: targets}
	case len(req.Names) > 0:
		s = model.ReloadScope{Names: req.Names, ServerIDs: targets}
	default:
		s = model.ReloadScope{AllStale: targets}
	}
	if s, err = u.reloadScope(ctx, s); err != nil {
		return nil, err
	}
	if err := u.queue(ctx, userID, orgID, s); err != nil {
		return nil, err
	}
	return targets, nil
```

**Replace in** `internal/domain/usecase/sharedvars/usecase.go`:

```go
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

```
**with:**

```go
// syncAll pushes the snapshot to each server, logging (not failing) a
// publish error: the save is committed and drift or the next connect heals.
func (u *UseCase) syncAll(ctx context.Context, servers []string) {
	for _, id := range servers {
		if err := u.Sync(ctx, id); err != nil {
			u.d.Log.Warn("push shared variables", "server_id", id, "error", err)
		}
	}
}

```

**Replace in** `internal/domain/usecase/sharedvars/sync.go`:

```go

// Sync dispatches serverID's full snapshot as vars.sync (for no user: the
// outcome fans out to the org). After a successful sync, reload names are
// sent as vars.reload.
func (u *UseCase) Sync(ctx context.Context, serverID string, reload []string) error {
	req, err := u.snapshot(ctx, serverID)
	if err != nil {
```
**with:**

```go

// Sync dispatches serverID's full snapshot as vars.sync (for no user: the
// outcome fans out to the org).
func (u *UseCase) Sync(ctx context.Context, serverID string) error {
	req, err := u.snapshot(ctx, serverID)
	if err != nil {
```

**Replace in** `internal/domain/usecase/sharedvars/sync.go`:

```go
	_, err = u.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, Type: command.TypeVarsSync, Payload: req,
		OnResult: func(res port.CommandResult) { u.synced(serverID, reload, res) },
	})
	return err
```
**with:**

```go
	_, err = u.d.Dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, Type: command.TypeVarsSync, Payload: req,
		OnResult: func(res port.CommandResult) { u.synced(serverID, res) },
	})
	return err
```

**Replace in** `internal/domain/usecase/sharedvars/sync.go`:

```go
// the current revision: a late result for an older snapshot may name
// ciphertexts replaced since), records the agent's revision (never moving
// the cached one back), announces vars.synced and sends the follow-up
// reload.
func (u *UseCase) synced(serverID string, reload []string, res port.CommandResult) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
```
**with:**

```go
// the current revision: a late result for an older snapshot may name
// ciphertexts replaced since), records the agent's revision (never moving
// the cached one back) and announces vars.synced.
func (u *UseCase) synced(serverID string, res port.CommandResult) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
```

**Replace in** `internal/domain/usecase/sharedvars/sync.go`:

```go
	}
	u.notify(ctx, serverID, model.NotificationVarsSynced, model.VarsSyncedPayload{ServerID: serverID, Revision: out.Revision, Undecryptable: names})
	if len(reload) > 0 {
		if err := u.dispatchReload(ctx, serverID, command.ReloadVarsRequest{Names: reload}); err != nil {
			u.d.Log.Warn("reload after shared variable change", "server_id", serverID, "error", err)
		}
	}
}

```
**with:**

```go
	}
	u.notify(ctx, serverID, model.NotificationVarsSynced, model.VarsSyncedPayload{ServerID: serverID, Revision: out.Revision, Undecryptable: names})
}

```

**Replace in** `internal/domain/usecase/sharedvars/sync.go`:

```go
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

```
**with:**

```go
	u.d.Log.Warn("dropped shared secrets this server cannot decrypt; enter them again", "server_id", serverID, "names", names)
	return names
}

```

**Replace in** `internal/infra/bootstrap/varssync.go`:

```go
	}
	s := &varsSync{indexSync: online, uc: sharedvars.New(&deps), log: log, pushed: map[string]int64{}}
	online.list = func(ctx context.Context, serverID string) error { return s.uc.Sync(ctx, serverID, nil) }
	return s
}
```
**with:**

```go
	}
	s := &varsSync{indexSync: online, uc: sharedvars.New(&deps), log: log, pushed: map[string]int64{}}
	online.list = func(ctx context.Context, serverID string) error { return s.uc.Sync(ctx, serverID) }
	return s
}
```

**Replace in** `internal/infra/bootstrap/varssync.go`:

```go
	pctx, cancel := context.WithTimeout(s.ctx, indexSyncPublishTimeout)
	defer cancel()
	if err := s.uc.Sync(pctx, serverID, nil); err != nil {
		s.log.Warn("sync shared variables: drift push", "error", err, "server_id", serverID)
		// Nothing was sent: the next report tries again.
```
**with:**

```go
	pctx, cancel := context.WithTimeout(s.ctx, indexSyncPublishTimeout)
	defer cancel()
	if err := s.uc.Sync(pctx, serverID); err != nil {
		s.log.Warn("sync shared variables: drift push", "error", err, "server_id", serverID)
		// Nothing was sent: the next report tries again.
```

**Replace in** `internal/app/web/handler/sharedvars/handler.go`:

```go
	NotificationManager port.NotificationManager
	StatusCache         *status.Cache
}

```
**with:**

```go
	NotificationManager port.NotificationManager
	StatusCache         *status.Cache
	// Reloads is the rollout queue (nil: reload requests fail).
	Reloads svuc.ReloadQueue
}

```

**Replace in** `internal/app/web/handler/sharedvars/handler.go`:

```go
// builds a handler that is never called.
func NewHandler(d *Deps) *Handler {
	deps := svuc.Deps{Repo: d.Repo, Dispatcher: d.CommandDispatcher, Log: d.Logger}
	var access webutil.ServerAccess
	if d.Servers != nil {
```
**with:**

```go
// builds a handler that is never called.
func NewHandler(d *Deps) *Handler {
	deps := svuc.Deps{Repo: d.Repo, Dispatcher: d.CommandDispatcher, Reloads: d.Reloads, Log: d.Logger}
	var access webutil.ServerAccess
	if d.Servers != nil {
```

**Replace in** `internal/app/web/handler/sharedvars/handler.go`:

```go

func (h *Handler) DeleteOrg(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
```
**with:**

```go

func (h *Handler) DeleteOrg(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
```

**Replace in** `internal/app/web/handler/sharedvars/handler.go`:

```go
		return
	}
	if err := h.uc.DeleteOrg(r.Context(), orgID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
```
**with:**

```go
		return
	}
	if err := h.uc.DeleteOrg(r.Context(), userID, orgID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
```

**Replace in** `internal/app/web/handler/sharedvars/handler.go`:

```go
		return
	}
	if err := h.uc.DeleteServer(r.Context(), req.ServerID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
```
**with:**

```go
		return
	}
	if err := h.uc.DeleteServer(r.Context(), userID, req.ServerID, req.ID, req.Reload); err != nil {
		h.fail(w, err, "delete shared variable")
		return
```

**Replace in** `internal/app/web/handler/sharedvars/handler.go`:

```go
		return
	}
	servers, err := h.uc.Reload(r.Context(), orgID, svuc.ReloadInput{
		ServerID: req.ServerID, VariableID: req.VariableID, Names: req.Names, AppIDs: req.AppIDs,
	})
```
**with:**

```go
		return
	}
	servers, err := h.uc.Reload(r.Context(), userID, orgID, svuc.ReloadInput{
		ServerID: req.ServerID, VariableID: req.VariableID, Names: req.Names, AppIDs: req.AppIDs,
	})
```

**Replace in** `internal/app/web/handler/sharedvars/handler.go`:

```go
		return
	}
	webutil.Success(w, "reload dispatched", svuc.DispatchedServers{Servers: servers})
}
```
**with:**

```go
		return
	}
	webutil.Success(w, "reload queued", svuc.DispatchedServers{Servers: servers})
}
```

**Replace in** `internal/app/web/handler/sharedvars/routes.go`:

```go
	Ciphertexts map[string]string `json:"ciphertexts,omitempty" description:"Secrets only: server_id -> ECIES ciphertext for that server's key (GET /api/v1/org/list-encryption-keys). A new value covers every keyed server; a gap fill only servers missing it; none keeps every stored copy."`
	Description string            `json:"description"`
	Reload      bool              `json:"reload,omitempty" description:"Reload the affected apps after each server stored the change."`
}

```
**with:**

```go
	Ciphertexts map[string]string `json:"ciphertexts,omitempty" description:"Secrets only: server_id -> ECIES ciphertext for that server's key (GET /api/v1/org/list-encryption-keys). A new value covers every keyed server; a gap fill only servers missing it; none keeps every stored copy."`
	Description string            `json:"description"`
	Reload      bool              `json:"reload,omitempty" description:"Reload the affected running apps, one at a time, once each server stored the change (the org's vars_reload rollout)."`
}

```

**Replace in** `internal/app/web/handler/sharedvars/routes.go`:

```go
			Method: http.MethodPost, Path: "/api/v1/shared-variables/reload", Handler: h.Reload, Auth: api.User,
			Summary: "Reload running apps whose shared variables changed", Tag: "variables",
			Description: "Answers with the servers dispatched to; each app's outcome arrives as vars.reloaded over SSE.",
			Request:     ReloadRequest{}, Response: svuc.DispatchedServers{}, Errors: both,
		},
	}
```
**with:**

```go
			Method: http.MethodPost, Path: "/api/v1/shared-variables/reload", Handler: h.Reload, Auth: api.User,
			Summary: "Reload running apps whose shared variables changed", Tag: "variables",
			Description: "Queues, or extends, the org's vars_reload rollout: the stale running apps in scope reload one " +
				"at a time and the first failure stops it. Answers with the servers in scope; progress arrives as rollout " +
				"events, each app's outcome as vars.reloaded over SSE.",
			Request: ReloadRequest{}, Response: svuc.DispatchedServers{}, Errors: both,
		},
	}
```

**Replace in** `internal/app/web/routes.go`:

```go
	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/app/web/util"
	"winterflow/internal/infra/bootstrap"
)
```
**with:**

```go
	"winterflow/internal/app/web/middleware/ratelimit"
	"winterflow/internal/app/web/util"
	svuc "winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/bootstrap"
)
```

**Replace in** `internal/app/web/routes.go`:

```go
	usersAPI := huser.NewHandler(&huser.Deps{Logger: d.Log, Tokens: d.UserService, Users: d.UserService})
	orgAPI := horg.NewHandler(&horg.Deps{Logger: d.Log, Users: d.UserService})
	sharedVarsAPI := hsharedvars.NewHandler(&hsharedvars.Deps{
		Logger:              d.Log,
```
**with:**

```go
	usersAPI := huser.NewHandler(&huser.Deps{Logger: d.Log, Tokens: d.UserService, Users: d.UserService})
	orgAPI := horg.NewHandler(&horg.Deps{Logger: d.Log, Users: d.UserService})
	// A nil queue (the spec-only route table) must not become a non-nil
	// interface.
	var reloads svuc.ReloadQueue
	if d.Rollouts != nil {
		reloads = d.Rollouts
	}
	sharedVarsAPI := hsharedvars.NewHandler(&hsharedvars.Deps{
		Logger:              d.Log,
```

**Replace in** `internal/app/web/routes.go`:

```go
		NotificationManager: d.NotificationManager,
		StatusCache:         d.StatusCache,
	})

```
**with:**

```go
		NotificationManager: d.NotificationManager,
		StatusCache:         d.StatusCache,
		Reloads:             reloads,
	})

```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal; go vet ./... && go test -race -count=1 ./internal/domain/usecase/rollouts/ ./internal/domain/usecase/sharedvars/ ./internal/infra/bootstrap/
```

Expected: no gofmt output; all `ok`.

- [ ] **Step 4: Contract tests**

**Replace in** `internal/app/web/contract/shared_variables_test.go`:

```go
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
```
**with:**

```go
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: sidA, AppIDs: []string{anApp}}).
			expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 1 || d.Servers[0] != sidA {
			t.Fatalf("reload of an app = %+v", d)
		}
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{VariableID: hostID}).expect(t, http.StatusOK).data(t, &d)
		if len(d.Servers) != 3 {
			t.Fatalf("reload by org variable = %+v", d)
		}
		// Both went on the org's one reload rollout (no server has reported
		// the apps' staleness here, so it is still waiting).
		if list := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutVarsReload}}); len(list) != 1 || !model.RolloutState(list[0].State).Active() {
			t.Fatalf("reload rollouts = %+v", list)
		}
		owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{}).expect(t, http.StatusBadRequest)
```

**Replace in** `internal/app/web/contract/shared_variables_test.go`:

```go
	})

	t.Run("reload: true follows each sync with vars.reload", func(t *testing.T) {
		save(t, hsv.SaveVariableRequest{Name: "RELOAD_ME", Value: strp("1"), Reload: true}).expect(t, http.StatusOK)
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

```
**with:**

```go
	})

	t.Run("reload: true extends the org's reload rollout", func(t *testing.T) {
		before := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutVarsReload}})
		save(t, hsv.SaveVariableRequest{Name: "RELOAD_ME", Value: strp("1"), Reload: true}).expect(t, http.StatusOK)
		after := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutVarsReload}})
		if len(after) != 1 || after[0].ID != before[0].ID {
			t.Fatalf("rollouts before %+v, after %+v", before, after)
		}
		// Nothing is sent to an agent before its server reports a stale app.
		if n := len(e.agent.seen(command.TypeVarsReload)); n != 0 {
			t.Fatalf("%d vars.reload sent", n)
		}
	})

```
In `internal/app/web/contract/rollouts_test.go`, add the imports and the reload tests:

**Replace in** `internal/app/web/contract/rollouts_test.go`:

```go

	hrollouts "winterflow/internal/app/web/handler/rollouts"
	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
)
```
**with:**

```go

	hrollouts "winterflow/internal/app/web/handler/rollouts"
	hsv "winterflow/internal/app/web/handler/sharedvars"
	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/sharedvars"
	"winterflow/internal/infra/transport/bus"
)
```

**Replace in** `internal/app/web/contract/rollouts_test.go`:

```go
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: "nope"}).expect(t, http.StatusBadRequest)
}
```
**with:**

```go
	owner.post(t, "/api/v1/rollout/stop-rollout", hrollouts.RolloutIDRequest{ID: "nope"}).expect(t, http.StatusBadRequest)
}

func TestVarsReloadRollout(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	// The agent reports anApp running and stale for SMTP_HOST until it got
	// the reload, at the server's current revision (0: nothing saved).
	reloaded := func() bool {
		for _, c := range e.agent.seen(command.TypeVarsReload) {
			if in := decode[command.ReloadVarsRequest](c.Payload); len(in.AppIDs) == 1 && in.AppIDs[0] == anApp {
				return true
			}
		}
		return false
	}
	stale := func(string) []string {
		if reloaded() {
			return nil
		}
		return []string{"SMTP_HOST"}
	}
	reportRunning(t, e, sid, fixed(anApp), stale, func() int64 { return 0 })

	var d sharedvars.DispatchedServers
	owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: sid}).expect(t, http.StatusOK).data(t, &d)
	if len(d.Servers) != 1 || d.Servers[0] != sid {
		t.Fatalf("reload = %+v", d)
	}
	list := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutVarsReload}})
	if len(list) != 1 {
		t.Fatalf("rollouts = %+v", list)
	}
	done := awaitRollout(t, owner, list[0].ID, "done")
	if done.Done != 1 || done.Total != 1 || done.Kind != model.RolloutVarsReload {
		t.Fatalf("done = %+v", done)
	}
	if got := e.agent.seen(command.TypeVarsReload); len(got) != 1 || got[0].AgentID != sid {
		t.Fatalf("vars.reload sent = %+v", got)
	}
	// The org's id is its scope; another org's is unknown.
	owner.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {model.RolloutVarsReload}, "scope_key": {done.ScopeKey}}).expect(t, http.StatusOK)
	mallory, _ := e.foreignClient(t)
	mallory.get(t, "/api/v1/rollout/get-rollouts", url.Values{"kind": {model.RolloutVarsReload}, "scope_key": {done.ScopeKey}}).
		expect(t, http.StatusNotFound)
}

func TestVarsReloadRolloutStopsAtAFailure(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	e.agent.fail(command.TypeVarsReload, "reload after a change to SMTP_HOST failed: docker compose up: exit status 1")
	reportRunning(t, e, sid, fixed(anApp), func(string) []string { return []string{"SMTP_HOST"} }, func() int64 { return 0 })
	owner.post(t, "/api/v1/shared-variables/reload", hsv.ReloadRequest{ServerID: sid, Names: []string{"SMTP_HOST"}}).expect(t, http.StatusOK)
	list := rolloutsOf(t, owner, url.Values{"kind": {model.RolloutVarsReload}})
	failed := awaitRollout(t, owner, list[0].ID, "failed")
	if failed.CurrentAppID != anApp || failed.CurrentServerID != sid || failed.Error != "reload after a change to SMTP_HOST failed: docker compose up: exit status 1" {
		t.Fatalf("failed = %+v", failed)
	}
}
```
Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
make openapi && git diff --stat api/openapi.yaml && WF_CONTRACT_GUARD=require go test -count=1 ./...
```

Expected: `api/openapi.yaml` changes only the reload route's description and the `reload` field's; every package `ok`.

Run:

```bash
make test-pg TEST_PG_ARGS='-run "TestSharedVariables|TestVarsReloadRollout|TestParity"'
```

Expected: `ok` on postgres:16.

- [ ] **Commit**

```bash
git add internal/domain/usecase/rollouts internal/domain/usecase/sharedvars internal/infra/bootstrap/varssync.go internal/app/web api/openapi.yaml
git commit -m "feat(vars): reloads go through the vars_reload rollout

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Web — the rollouts client and `useRollout`

**Files:**
- Create: `web/src/lib/rollouts.ts`, `web/src/hooks/use-rollout.ts`
- Create: `web/src/test/rollouts.ts` (a fixture; `web/src/test/` is exempt from coverage)
- Test: `web/src/lib/rollouts.test.ts`, `web/src/hooks/use-rollout.test.tsx`

**Interfaces:**
- Consumes: `apiBase` (`@/config`), `readApiError` (`@/lib/api`), `ApiError` (`@/lib/templates`), `useNotifications`.
- Produces (`@/lib/rollouts`): types `RolloutKind`, `RolloutState`, `Rollout` (= `model.RolloutView`), `RolloutsApi`; `ROLLOUT_EVENT = "rollout"`; `isActive(r)`, `appLabel(r)`, `rolloutLine(r, serverName)` ("Upgrading 3 of 12: bakomi…", "Reloading 2 of 5: shop on srv-2…", "Upgrade to r3 queued…", "… (stopping after this one)"), `failureLine(r, serverName)` ("Upgrade to r3 stopped at bakomi", "Reload stopped at shop on srv-2"); `createRolloutsApi(fetchImpl?)` with `list(kind, scopeKey?)`, `stop(id)`, `upgradeAll(templateId, revision)` (failures are `ApiError` with the status).
- Produces (`@/hooks/use-rollout`): `useRollout(api, kind, scopeKey?: string | null)` → `{ rollout: Rollout | null, set, stop(id) }` (`null` scope: follow nothing yet).
- Produces (`@/test/rollouts`): `rollout(over?)` — a running upgrade of `t1` to r3, 3 of 12 on bakomi.

- [ ] **Step 1: Write the failing tests**

**Create** `web/src/test/rollouts.ts`:

```ts
import type { Rollout } from "@/lib/rollouts";

// rollout is a running template upgrade of t1 (3 of 12: bakomi on s1),
// with over applied.
export function rollout(over: Partial<Rollout> = {}): Rollout {
  return {
    id: "r1",
    kind: "template_upgrade",
    scope_key: "t1",
    revision: 3,
    state: "running",
    stop_requested: false,
    done: 1,
    skipped: 1,
    total: 12,
    current_app_id: "a1b2c3d4e5f6",
    current_server_id: "s1",
    current_app_name: "bakomi",
    error: "",
    created_at: "2026-10-12T10:00:00Z",
    created_by: "u1",
    finished_at: null,
    ...over,
  };
}
```

**Create** `web/src/lib/rollouts.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";

import { createRolloutsApi, failureLine, isActive, rolloutLine } from "@/lib/rollouts";
import { ApiError } from "@/lib/templates";
import { fakeAPI } from "@/test/fake-api";
import { rollout } from "@/test/rollouts";

const names = (id: string) => ({ s1: "alpha", s2: "srv-2" })[id] ?? id;

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("rolloutLine", () => {
  it("counts the app in flight among the total", () => {
    expect(rolloutLine(rollout(), names)).toBe("Upgrading 3 of 12: bakomi…");
    expect(
      rolloutLine(rollout({ kind: "vars_reload", done: 1, skipped: 0, total: 5, current_app_name: "shop", current_server_id: "s2" }), names),
    ).toBe("Reloading 2 of 5: shop on srv-2…");
  });

  it("says queued, stopping, and names an app without a name by its id", () => {
    expect(rolloutLine(rollout({ state: "queued", current_app_id: "" }), names)).toBe("Upgrade to r3 queued…");
    expect(rolloutLine(rollout({ kind: "vars_reload", state: "queued", current_app_id: "" }), names)).toBe("Reload queued…");
    expect(rolloutLine(rollout({ stop_requested: true, current_app_name: "" }), names)).toBe(
      "Upgrading 3 of 12: a1b2c3d4 (stopping after this one)",
    );
  });

  it("names where a failed rollout stopped", () => {
    expect(failureLine(rollout({ state: "failed" }), names)).toBe("Upgrade to r3 stopped at bakomi");
    expect(failureLine(rollout({ kind: "vars_reload", state: "failed", current_app_name: "shop", current_server_id: "s2" }), names)).toBe(
      "Reload stopped at shop on srv-2",
    );
    expect(isActive(rollout())).toBe(true);
    expect(isActive(rollout({ state: "failed" }))).toBe(false);
  });
});

describe("createRolloutsApi", () => {
  it("lists, stops and queues Upgrade all", async () => {
    const api = fakeAPI({
      "/api/v1/rollout/get-rollouts": { rollouts: null },
      "/api/v1/rollout/stop-rollout": rollout({ stop_requested: true }),
      "/api/v1/template/upgrade-instances": rollout({ state: "queued" }),
    });
    const r = createRolloutsApi();
    expect(await r.list("template_upgrade", "t1")).toEqual([]);
    await r.list("vars_reload");
    expect((await r.stop("r1")).stop_requested).toBe(true);
    expect((await r.upgradeAll("t1", 3)).state).toBe("queued");
    expect(api.calls.map((c) => c.query)).toEqual(["?kind=template_upgrade&scope_key=t1", "?kind=vars_reload", "", ""]);
    expect(api.posted("/api/v1/rollout/stop-rollout")).toEqual([{ id: "r1" }]);
    expect(api.posted("/api/v1/template/upgrade-instances")).toEqual([{ id: "t1", revision: 3 }]);
  });

  it("refuses with the API's status", async () => {
    fakeAPI({ "/api/v1/template/upgrade-instances": Object.assign(new Error("an upgrade is already running"), { status: 409 }) });
    const err = await createRolloutsApi().upgradeAll("t1", 3).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(409);
    expect((err as ApiError).message).toBe("an upgrade is already running");
  });
});
```

**Create** `web/src/hooks/use-rollout.test.tsx`:

```tsx
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { NotificationsContext, type Notification, type NotificationHandler } from "@/context/notifications-context-base";
import { useRollout } from "@/hooks/use-rollout";
import type { Rollout, RolloutsApi } from "@/lib/rollouts";
import { rollout } from "@/test/rollouts";

function setup(list: Promise<Rollout[]>, scopeKey?: string | null) {
  const hub: { h?: NotificationHandler } = {};
  const api = {
    list: vi.fn(() => list),
    stop: vi.fn(async (id: string) => rollout({ id, stop_requested: true })),
    upgradeAll: vi.fn(),
  } satisfies RolloutsApi;
  const wrapper = ({ children }: { children: ReactNode }) => (
    <NotificationsContext.Provider
      value={{
        connected: true,
        subscribe: (h) => {
          hub.h = h;
          return () => {
            hub.h = undefined;
          };
        },
        waitFor: vi.fn(),
      }}
    >
      {children}
    </NotificationsContext.Provider>
  );
  const view = renderHook(() => useRollout(api, "template_upgrade", scopeKey), { wrapper });
  const emit = (n: Notification) => act(() => hub.h?.(n));
  return { ...view, api, emit };
}

describe("useRollout", () => {
  it("reads the newest rollout of its scope, then follows its events", async () => {
    const { result, api, emit } = setup(Promise.resolve([rollout({ done: 1 }), rollout({ id: "r0", state: "done" })]), "t1");
    await waitFor(() => expect(result.current.rollout?.done).toBe(1));
    expect(api.list).toHaveBeenCalledWith("template_upgrade", "t1");

    emit({ type: "rollout", ref: "", payload: rollout({ done: 2 }) });
    expect(result.current.rollout?.done).toBe(2);
    // Another template's, another kind's, or an older rollout's event is not this page's.
    emit({ type: "rollout", ref: "", payload: rollout({ scope_key: "t2", done: 9 }) });
    emit({ type: "rollout", ref: "", payload: rollout({ kind: "vars_reload", done: 9 }) });
    emit({ type: "rollout", ref: "", payload: rollout({ id: "r0", created_at: "2026-10-01T00:00:00Z", done: 9 }) });
    emit({ type: "apps_status", ref: "", payload: rollout({ done: 9 }) });
    expect(result.current.rollout?.done).toBe(2);
    // A newer one replaces it.
    emit({ type: "rollout", ref: "", payload: rollout({ id: "r2", created_at: "2026-10-13T00:00:00Z", state: "queued" }) });
    expect(result.current.rollout?.id).toBe("r2");
  });

  it("keeps an event that beat the first read, and stops", async () => {
    let answer: (l: Rollout[]) => void = () => {};
    const { result, api, emit } = setup(new Promise((resolve) => (answer = resolve)));
    emit({ type: "rollout", ref: "", payload: rollout({ done: 4 }) });
    await act(async () => answer([rollout({ done: 1 })]));
    expect(result.current.rollout?.done).toBe(4);
    await act(() => result.current.stop("r1"));
    expect(api.stop).toHaveBeenCalledWith("r1");
    expect(result.current.rollout?.stop_requested).toBe(true);
  });

  it("follows nothing without a scope yet", () => {
    const { result, api, emit } = setup(Promise.resolve([rollout()]), null);
    emit({ type: "rollout", ref: "", payload: rollout() });
    expect(api.list).not.toHaveBeenCalled();
    expect(result.current.rollout).toBeNull();
  });

  it("shows nothing when the read fails", async () => {
    const { result, api } = setup(Promise.reject(new Error("down")));
    await waitFor(() => expect(api.list).toHaveBeenCalled());
    expect(result.current.rollout).toBeNull();
  });
});
```
Run:

```bash
pnpm --dir web exec vitest run src/lib/rollouts.test.ts src/hooks/use-rollout.test.tsx
```

Expected: FAIL: `Failed to resolve import "@/lib/rollouts"`.

- [ ] **Step 2: Implement**

**Create** `web/src/lib/rollouts.ts`:

```ts
// Rollouts: the API's queue of one-app-at-a-time jobs ("Upgrade all" of a
// template's instances, the reloads a shared-variable change asks for).
// Types mirror model.RolloutView; progress arrives as "rollout" events on
// the SSE stream, to every member of the org.
import { apiBase } from "@/config";
import { ApiError } from "@/lib/templates";
import { readApiError } from "@/lib/api";

export type RolloutKind = "template_upgrade" | "vars_reload";
export type RolloutState = "queued" | "running" | "done" | "stopped" | "failed";

export type Rollout = {
  id: string;
  kind: RolloutKind;
  scope_key: string; // the template id, or the org id for vars_reload
  revision: number; // template_upgrade: the target revision
  state: RolloutState;
  stop_requested: boolean;
  done: number;
  skipped: number;
  total: number; // done + skipped + the apps still to do
  current_app_id: string; // in flight; after a failure, the app that failed
  current_server_id: string;
  current_app_name: string;
  error: string;
  created_at: string;
  created_by: string;
  finished_at: string | null;
};

// ROLLOUT_EVENT is the SSE notification type of a rollout change.
export const ROLLOUT_EVENT = "rollout";

export function isActive(r: Rollout): boolean {
  return r.state === "queued" || r.state === "running";
}

// appLabel names the rollout's current app (its id's prefix until the API
// knows a name).
export function appLabel(r: Rollout): string {
  return r.current_app_name || r.current_app_id.slice(0, 8);
}

// rolloutLine is the strip's text for an active rollout: "Upgrading 3 of
// 12: bakomi…", "Reloading 2 of 5: shop on srv-2…".
export function rolloutLine(r: Rollout, serverName: (id: string) => string): string {
  const verb = r.kind === "template_upgrade" ? "Upgrading" : "Reloading";
  if (r.state === "queued" || !r.current_app_id) {
    return r.kind === "template_upgrade" ? `Upgrade to r${r.revision} queued…` : "Reload queued…";
  }
  const where = r.kind === "vars_reload" ? ` on ${serverName(r.current_server_id)}` : "";
  const step = Math.min(r.done + r.skipped + 1, Math.max(r.total, 1));
  const stopping = r.stop_requested ? " (stopping after this one)" : "…";
  return `${verb} ${step} of ${r.total}: ${appLabel(r)}${where}${stopping}`;
}

// failureLine names what stopped a failed rollout.
export function failureLine(r: Rollout, serverName: (id: string) => string): string {
  const what = r.kind === "template_upgrade" ? `Upgrade to r${r.revision}` : "Reload";
  const where = r.kind === "vars_reload" && r.current_server_id ? ` on ${serverName(r.current_server_id)}` : "";
  return `${what} stopped at ${appLabel(r)}${where}`;
}

export type RolloutsApi = {
  // list reads the newest rollouts of a kind (the active one first); for
  // vars_reload the scope is the caller's org.
  list(kind: RolloutKind, scopeKey?: string): Promise<Rollout[]>;
  stop(id: string): Promise<Rollout>;
  // upgradeAll queues "Upgrade all" of a template's instances to revision
  // (an ApiError with status 409 while one is active).
  upgradeAll(templateId: string, revision: number): Promise<Rollout>;
};

export function createRolloutsApi(fetchImpl?: typeof fetch): RolloutsApi {
  const doFetch: typeof fetch = fetchImpl ?? ((input, init) => fetch(input, init));
  async function call<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await doFetch(`${apiBase}${path}`, { credentials: "include", ...init });
    if (!res.ok) throw new ApiError((await readApiError(res)).message, res.status);
    return ((await res.json()) as { data: T }).data;
  }
  const post = (payload: unknown): RequestInit => ({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  return {
    list: (kind, scopeKey) => {
      const q = new URLSearchParams({ kind });
      if (scopeKey) q.set("scope_key", scopeKey);
      return call<{ rollouts: Rollout[] | null }>(`/api/v1/rollout/get-rollouts?${q}`).then((d) => d.rollouts ?? []);
    },
    stop: (id) => call<Rollout>("/api/v1/rollout/stop-rollout", post({ id })),
    upgradeAll: (templateId, revision) =>
      call<Rollout>("/api/v1/template/upgrade-instances", post({ id: templateId, revision })),
  };
}
```

**Create** `web/src/hooks/use-rollout.ts`:

```ts
import { useCallback, useEffect, useState } from "react";

import { useNotifications } from "@/context/use-notifications";
import { ROLLOUT_EVENT, type Rollout, type RolloutKind, type RolloutsApi } from "@/lib/rollouts";

// useRollout follows the newest rollout of a kind (of one scope, when
// given; null: none yet, follow nothing) for a page: read once, then kept
// current by the stream's rollout events. set shows one this page just
// queued; stop asks the API to stop it and shows the answer. A failed read
// leaves no rollout (the page works without it).
export function useRollout(api: RolloutsApi, kind: RolloutKind, scopeKey?: string | null) {
  const { subscribe } = useNotifications();
  const [rollout, setRollout] = useState<Rollout | null>(null);

  useEffect(() => {
    if (scopeKey === null) return;
    let live = true;
    api.list(kind, scopeKey).then(
      // An event that arrived meanwhile is newer than the read.
      (list) => live && setRollout((cur) => cur ?? list[0] ?? null),
      () => {},
    );
    return () => {
      live = false;
    };
  }, [api, kind, scopeKey]);

  useEffect(
    () =>
      subscribe((n) => {
        if (n.type !== ROLLOUT_EVENT || scopeKey === null) return;
        const r = n.payload as Rollout | undefined;
        if (!r || r.kind !== kind || (scopeKey && r.scope_key !== scopeKey)) return;
        // A newer rollout replaces the shown one; an older one's late event
        // does not.
        setRollout((cur) => (!cur || cur.id === r.id || r.created_at >= cur.created_at ? r : cur));
      }),
    [subscribe, kind, scopeKey],
  );

  const stop = useCallback(
    async (id: string) => {
      setRollout(await api.stop(id));
    },
    [api],
  );

  return { rollout, set: setRollout, stop };
}
```
Run:

```bash
pnpm --dir web exec tsc -b && pnpm --dir web run lint && pnpm --dir web exec vitest run src/lib/rollouts.test.ts src/hooks/use-rollout.test.tsx
```

Expected: no type or lint errors; 2 files, 9 tests passed.

- [ ] **Commit**

```bash
git add web/src/lib/rollouts.ts web/src/lib/rollouts.test.ts web/src/hooks/use-rollout.ts web/src/hooks/use-rollout.test.tsx web/src/test/rollouts.ts
git commit -m "feat(web): rollouts client and useRollout

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Web — the rollout strip

**Files:**
- Create: `web/src/components/rollout-strip.tsx`
- Test: `web/src/components/rollout-strip.test.tsx`

**Interfaces:**
- Consumes: Task 9; `ErrorText`, `serverPath`, `Button`.
- Produces: `RolloutStrip({ rollout, serverName, onStop })` — an active rollout: a `role="status"` line with a spinner and **Stop** (disabled once `stop_requested`; a refused stop toasts "Couldn't stop it"); a failed one: a `role="alert"` naming the app, **Open {app}** linking to `/server/{server}/app/{app}`, the error (`ErrorText`) and **Dismiss**; nothing otherwise.

- [ ] **Step 1: Write the failing test**

**Create** `web/src/components/rollout-strip.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const toast = vi.hoisted(() => ({ error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

import { RolloutStrip } from "@/components/rollout-strip";
import type { Rollout } from "@/lib/rollouts";
import { rollout } from "@/test/rollouts";

const names = (id: string) => (id === "s1" ? "alpha" : id);

function renderStrip(r: Rollout | null, onStop = vi.fn(async () => {})) {
  render(
    <MemoryRouter>
      <RolloutStrip rollout={r} serverName={names} onStop={onStop} />
    </MemoryRouter>,
  );
  return onStop;
}

describe("RolloutStrip", () => {
  it("shows the app in flight and stops after it", async () => {
    const onStop = renderStrip(rollout());
    expect(screen.getByRole("status")).toHaveTextContent("Upgrading 3 of 12: bakomi…");
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalledWith("r1");
  });

  it("disables Stop once asked", () => {
    renderStrip(rollout({ stop_requested: true }));
    expect(screen.getByRole("button", { name: "Stop" })).toBeDisabled();
  });

  it("toasts a refused stop", async () => {
    renderStrip(rollout(), vi.fn(async () => Promise.reject(new Error("rollout not found"))));
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(toast.error).toHaveBeenCalledWith("Couldn't stop it", { description: "rollout not found" });
  });

  it("names the app a failure stopped at, links to it, and can be dismissed", async () => {
    renderStrip(rollout({ kind: "vars_reload", state: "failed", error: "docker compose up: exit status 1", current_app_name: "shop" }));
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Reload stopped at shop on alpha");
    expect(alert).toHaveTextContent("docker compose up: exit status 1");
    expect(screen.getByRole("link", { name: "Open shop" })).toHaveAttribute("href", "/server/s1/app/a1b2c3d4e5f6");
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows nothing for no rollout, a finished one or a stopped one", () => {
    for (const r of [null, rollout({ state: "done" }), rollout({ state: "stopped" })]) {
      const { container, unmount } = render(
        <MemoryRouter>
          <RolloutStrip rollout={r} serverName={names} onStop={vi.fn()} />
        </MemoryRouter>,
      );
      expect(container).toBeEmptyDOMElement();
      unmount();
    }
  });
});
```
Run:

```bash
pnpm --dir web exec vitest run src/components/rollout-strip.test.tsx
```

Expected: FAIL: `Failed to resolve import "@/components/rollout-strip"`.

- [ ] **Step 2: Implement**

**Create** `web/src/components/rollout-strip.tsx`:

```tsx
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { ErrorText } from "@/components/error-text";
import { serverPath } from "@/lib/server-paths";
import { appLabel, failureLine, isActive, rolloutLine, type Rollout } from "@/lib/rollouts";

// RolloutStrip shows a rollout in progress ("Upgrading 3 of 12: bakomi…"
// with Stop) or the failure that ended one, naming the app with a link to
// it and the error (Dismiss hides it for this visit). Nothing for a
// finished or stopped rollout, or none.
export function RolloutStrip({
  rollout,
  serverName,
  onStop,
}: {
  rollout: Rollout | null;
  serverName: (id: string) => string;
  onStop: (id: string) => Promise<void>;
}) {
  const [stopping, setStopping] = useState(false);
  const [dismissed, setDismissed] = useState<string | null>(null); // a failed rollout's id

  if (!rollout) return null;
  if (isActive(rollout)) {
    const stop = async () => {
      setStopping(true);
      try {
        await onStop(rollout.id);
      } catch (e) {
        toast.error("Couldn't stop it", { description: e instanceof Error ? e.message : undefined });
      } finally {
        setStopping(false);
      }
    };
    return (
      <div role="status" className="flex flex-wrap items-center gap-3 rounded-md border bg-muted/30 px-4 py-2 text-sm">
        <Loader2 aria-hidden className="size-4 animate-spin text-muted-foreground" />
        <span className="mr-auto">{rolloutLine(rollout, serverName)}</span>
        <Button size="sm" variant="outline" disabled={stopping || rollout.stop_requested} onClick={() => void stop()}>
          Stop
        </Button>
      </div>
    );
  }
  if (rollout.state !== "failed" || dismissed === rollout.id) return null;
  const link = rollout.current_app_id ? serverPath(rollout.current_server_id, `/app/${rollout.current_app_id}`) : null;
  return (
    <div role="alert" className="flex flex-wrap items-start gap-3 rounded-md border border-destructive/40 px-4 py-2 text-sm">
      <div className="mr-auto min-w-0 space-y-1">
        <p>
          {failureLine(rollout, serverName)}
          {link ? (
            <>
              {" · "}
              <Link to={link} className="font-medium underline-offset-2 hover:underline">
                Open {appLabel(rollout)}
              </Link>
            </>
          ) : null}
        </p>
        {rollout.error ? <ErrorText text={rollout.error} className="text-xs" /> : null}
      </div>
      <Button size="sm" variant="ghost" onClick={() => setDismissed(rollout.id)}>
        Dismiss
      </Button>
    </div>
  );
}
```
Run:

```bash
pnpm --dir web exec tsc -b && pnpm --dir web run lint && pnpm --dir web exec vitest run src/components/rollout-strip.test.tsx
```

Expected: 5 tests passed.

- [ ] **Commit**

```bash
git add web/src/components/rollout-strip.tsx web/src/components/rollout-strip.test.tsx
git commit -m "feat(web): rollout strip

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Web — "Upgrade all to rN" and the strip on the template page and both Variables pages

**Files:**
- Modify: `web/src/components/template-instances.tsx`, `template-instances.test.tsx`
- Modify: `web/src/pages/template-page.tsx`, `template-page.test.tsx`
- Modify: `web/src/pages/org-variables.tsx`, `org-variables.test.tsx`
- Modify: `web/src/pages/settings-variables.tsx`, `settings-variables.test.tsx`

**Interfaces:**
- Consumes: Tasks 9–10.
- Produces: `TemplateInstances` gains optional `onUpgradeAll?: () => void` and `rolloutActive?: boolean` ("{n} apps are behind rN", **Upgrade all to rN** confirmed by "Upgrade {n} apps to rN?" / **Upgrade all**, disabled while a rollout runs, hidden with nothing behind; pending instances don't count); the template page follows its template's rollout (strip under the header, the instance list re-read on every step); `OrgVariablesPage` gains `rollouts?: RolloutsApi`, `VariablesCard` gains `rollouts?: RolloutsApi` (both default to `createRolloutsApi()`) and show the org's reload rollout.

The Variables page tests' notification fakes kept one subscriber; the strip subscribes too, so they now fan each event out to every subscriber.

- [ ] **Step 1: Write the failing tests**

**Replace in** `web/src/components/template-instances.test.tsx`:

```tsx
    expect(screen.getByText(/No app uses this template yet/)).toBeInTheDocument();
  });
});
```
**with:**

```tsx
    expect(screen.getByText(/No app uses this template yet/)).toBeInTheDocument();
  });

  it("upgrades every created instance behind after a confirm, not while a rollout runs", async () => {
    const user = userEvent.setup();
    const onUpgradeAll = vi.fn();
    const list = [inst({}), inst({ key: "k2", app_id: "a2", revision: 3 }), inst({ key: "k3", app_id: "a3", state: "pending", revision: 1 })];
    const tree = (active: boolean, instances = list) => (
      <MemoryRouter>
        <NotificationsContext.Provider value={notifications}>
          <TemplateInstances view={VIEW} instances={instances} onChanged={onChanged} onUpgradeAll={onUpgradeAll} rolloutActive={active} />
        </NotificationsContext.Provider>
      </MemoryRouter>
    );
    const { rerender } = render(tree(false));
    // Only k1 counts: k2 is on r3, k3 was never created.
    expect(screen.getByText("1 app is behind r3")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Upgrade all to r3" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("Upgrade 1 app to r3?");
    await user.click(screen.getByRole("button", { name: "Upgrade all" }));
    expect(onUpgradeAll).toHaveBeenCalledTimes(1);

    rerender(tree(true));
    expect(screen.getByRole("button", { name: "Upgrade all to r3" })).toBeDisabled();
    rerender(tree(false, [inst({ revision: 3 })]));
    expect(screen.queryByRole("button", { name: /Upgrade all/ })).not.toBeInTheDocument();
  });
});
```

**Replace in** `web/src/pages/template-page.test.tsx`:

```tsx
import TemplatePage from "@/pages/template-page";
import { fakeAPI } from "@/test/fake-api";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
```
**with:**

```tsx
import TemplatePage from "@/pages/template-page";
import { fakeAPI } from "@/test/fake-api";
import { rollout } from "@/test/rollouts";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
```

**Replace in** `web/src/pages/template-page.test.tsx`:

```tsx
    expect(screen.getByLabelText("Template name")).toHaveValue("twillingate");
  });
});
```
**with:**

```tsx
    expect(screen.getByLabelText("Template name")).toHaveValue("twillingate");
  });

  it("upgrades all instances behind after a confirm, and shows the rollout", async () => {
    const user = userEvent.setup();
    const behind = { key: "k1", server_id: "s1", app_id: "a1", template_id: "t1", template_name: "twillingate", revision: 2,
      parameters: { NAME: "bakomi" }, secrets: null, state: "created", status_code: 1, last_error: "",
      created_at: "2026-10-12T10:00:00Z", updated_at: "2026-10-12T10:00:00Z" };
    const api = fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [behind] },
      "/api/v1/rollout/get-rollouts": { rollouts: [] },
      "/api/v1/template/upgrade-instances": rollout({ state: "queued", current_app_id: "", done: 0, skipped: 0, total: 0 }),
    });
    renderPage("/org/templates/t1?tab=instances");
    await user.click(await screen.findByRole("button", { name: "Upgrade all to r3" }));
    await user.click(screen.getByRole("button", { name: "Upgrade all" }));
    expect(await screen.findByText("Upgrade to r3 queued…")).toBeInTheDocument();
    expect(api.posted("/api/v1/template/upgrade-instances")).toEqual([{ id: "t1", revision: 3 }]);
    expect(api.calls.find((c) => c.path === "/api/v1/rollout/get-rollouts")?.query).toBe("?kind=template_upgrade&scope_key=t1");
    // One upgrade at a time: the button waits for this one.
    expect(screen.getByRole("button", { name: "Upgrade all to r3" })).toBeDisabled();
  });

  it("says why an upgrade could not start", async () => {
    const user = userEvent.setup();
    fakeAPI({
      "/api/v1/template/get-template": view(3),
      "/api/v1/instance/get-instances": { instances: [{ key: "k1", server_id: "s1", app_id: "a1", revision: 1, state: "created", parameters: {} }] },
      "/api/v1/template/upgrade-instances": Object.assign(new Error("an upgrade of this template is already running"), { status: 409 }),
    });
    renderPage("/org/templates/t1?tab=instances");
    await user.click(await screen.findByRole("button", { name: "Upgrade all to r3" }));
    await user.click(screen.getByRole("button", { name: "Upgrade all" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't start the upgrade", { description: "an upgrade of this template is already running" }),
    );
  });
});
```

**Replace in** `web/src/pages/org-variables.test.tsx`:

```tsx
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { EncryptionKeys, OrgVariables, SharedVariablesApi } from "@/lib/shared-variables";
import OrgVariablesPage from "@/pages/org-variables";

const T = "2026-10-11T00:00:00Z";
```
**with:**

```tsx
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { Rollout } from "@/lib/rollouts";
import type { EncryptionKeys, OrgVariables, SharedVariablesApi } from "@/lib/shared-variables";
import OrgVariablesPage from "@/pages/org-variables";
import { rollout } from "@/test/rollouts";

const T = "2026-10-11T00:00:00Z";
```

**Replace in** `web/src/pages/org-variables.test.tsx`:

```tsx
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

```
**with:**

```tsx
}

// noRollouts is a rollouts client with nothing running.
function noRollouts(list: Rollout[] = []) {
  return { list: vi.fn().mockResolvedValue(list), stop: vi.fn(async (id: string) => rollout({ id, stop_requested: true })), upgradeAll: vi.fn() };
}

function renderPage(api: ReturnType<typeof fakeApi>, rollouts = noRollouts()) {
  // Every subscriber hears each event (the page and its rollout strip).
  const handlers = new Set<NotificationHandler>();
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      handlers.add(h);
      return () => {
        handlers.delete(h);
      };
    },
    waitFor: vi.fn(),
  };
  const emit = (n: Notification) => act(() => handlers.forEach((h) => h(n)));
  render(
    <MemoryRouter>
      <NotificationsContext.Provider value={notifications}>
        <OrgVariablesPage api={api as unknown as SharedVariablesApi} rollouts={rollouts} />
      </NotificationsContext.Provider>
    </MemoryRouter>,
  );
  return { emit };
}

```

**Replace in** `web/src/pages/org-variables.test.tsx`:

```tsx
    expect(api.deleteOrg).toHaveBeenCalledWith("v1");
  });
});
```
**with:**

```tsx
    expect(api.deleteOrg).toHaveBeenCalledWith("v1");
  });

  it("shows the org's reload rollout and stops it", async () => {
    const user = userEvent.setup();
    const rollouts = noRollouts([
      rollout({ kind: "vars_reload", scope_key: "org-1", done: 1, skipped: 0, total: 5, current_app_name: "shop", current_server_id: "s3" }),
    ]);
    const { emit } = renderPage(fakeApi(), rollouts);
    expect(await screen.findByText("Reloading 2 of 5: shop on gamma…")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Stop" }));
    expect(rollouts.stop).toHaveBeenCalledWith("r1");
    expect(screen.getByRole("button", { name: "Stop" })).toBeDisabled();
    emit({ type: "rollout", ref: "", payload: rollout({ kind: "vars_reload", state: "stopped" }) });
    expect(screen.queryByText(/Reloading/)).not.toBeInTheDocument();
  });
});
```

**Replace in** `web/src/pages/settings-variables.test.tsx`:

```tsx
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { EncryptionKeys, ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";
import { VariablesCard } from "@/pages/settings-variables";

const T = "2026-10-11T00:00:00Z";
```
**with:**

```tsx
  type NotificationsContextValue,
} from "@/context/notifications-context-base";
import type { Rollout } from "@/lib/rollouts";
import type { EncryptionKeys, ServerVariables, SharedVariablesApi } from "@/lib/shared-variables";
import { VariablesCard } from "@/pages/settings-variables";
import { rollout } from "@/test/rollouts";

const T = "2026-10-11T00:00:00Z";
```

**Replace in** `web/src/pages/settings-variables.test.tsx`:

```tsx
}

function renderCard(api: ReturnType<typeof fakeApi>, serverId = "srv-a") {
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
  // a4 is stale but stopped: the agent skips it, so it is not counted.
  const apps = {
```
**with:**

```tsx
}

function renderCard(api: ReturnType<typeof fakeApi>, serverId = "srv-a", running: Rollout[] = []) {
  const rollouts = { list: vi.fn().mockResolvedValue(running), stop: vi.fn(), upgradeAll: vi.fn() };
  // Every subscriber hears each event (the page and its rollout strip).
  const handlers = new Set<NotificationHandler>();
  const notifications: NotificationsContextValue = {
    connected: true,
    subscribe: (h) => {
      handlers.add(h);
      return () => {
        handlers.delete(h);
      };
    },
    waitFor: vi.fn(),
  };
  const emit = (n: Notification) => act(() => handlers.forEach((h) => h(n)));
  // a4 is stale but stopped: the agent skips it, so it is not counted.
  const apps = {
```

**Replace in** `web/src/pages/settings-variables.test.tsx`:

```tsx
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={apps}>
          <VariablesCard serverId={id} api={api as unknown as SharedVariablesApi} />
        </AppsContext.Provider>
      </NotificationsContext.Provider>
```
**with:**

```tsx
      <NotificationsContext.Provider value={notifications}>
        <AppsContext.Provider value={apps}>
          <VariablesCard serverId={id} api={api as unknown as SharedVariablesApi} rollouts={rollouts} />
        </AppsContext.Provider>
      </NotificationsContext.Provider>
```

**Replace in** `web/src/pages/settings-variables.test.tsx`:

```tsx
  );
  const view = render(tree(serverId));
  return { switchTo: (id: string) => view.rerender(tree(id)), emit: (n: Notification) => act(() => hub.h?.(n)) };
}

```
**with:**

```tsx
  );
  const view = render(tree(serverId));
  return { switchTo: (id: string) => view.rerender(tree(id)), emit };
}

```

**Replace in** `web/src/pages/settings-variables.test.tsx`:

```tsx
    expect(await screen.findByText("Synced")).toBeInTheDocument();
  });
});
```
**with:**

```tsx
    expect(await screen.findByText("Synced")).toBeInTheDocument();
  });

  it("shows the org's reload rollout, and a failure naming the app", async () => {
    const { emit } = renderCard(fakeApi(), "srv-a", [
      rollout({ kind: "vars_reload", done: 0, skipped: 0, total: 2, current_app_name: "shop", current_server_id: "srv-b" }),
    ]);
    expect(await screen.findByText("Reloading 1 of 2: shop on beta…")).toBeInTheDocument();
    emit({
      type: "rollout",
      ref: "",
      payload: rollout({ kind: "vars_reload", state: "failed", current_app_name: "shop", current_server_id: "srv-b", error: "compose up failed" }),
    });
    expect(screen.getByText(/Reload stopped at shop on beta/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open shop" })).toHaveAttribute("href", "/server/srv-b/app/a1b2c3d4e5f6");
  });
});
```
Run:

```bash
pnpm --dir web exec vitest run src/components/template-instances.test.tsx src/pages/template-page.test.tsx src/pages/org-variables.test.tsx src/pages/settings-variables.test.tsx
```

Expected: FAIL: the four new tests (no "Upgrade all to r3" button, no reload line); `tsc` would also flag the unknown `rollouts` and `onUpgradeAll` props.

- [ ] **Step 2: Implement**

**Replace in** `web/src/components/template-instances.tsx`:

```tsx
// instance (its deploy never succeeded) shows why and offers Delete: no
// other page reaches it, and it keeps the template from being deleted.
export function TemplateInstances({
  view,
  instances,
  onChanged,
}: {
  view: TemplateView;
  instances: InstanceView[];
  onChanged: () => void;
}) {
  const { servers } = useServers();
```
**with:**

```tsx
// instance (its deploy never succeeded) shows why and offers Delete: no
// other page reaches it, and it keeps the template from being deleted.
// With onUpgradeAll, "Upgrade all to rN" (confirmed first) queues the
// upgrade of every created instance behind; it waits while a rollout of
// the template is active.
export function TemplateInstances({
  view,
  instances,
  onChanged,
  onUpgradeAll,
  rolloutActive = false,
}: {
  view: TemplateView;
  instances: InstanceView[];
  onChanged: () => void;
  onUpgradeAll?: () => void;
  rolloutActive?: boolean;
}) {
  const { servers } = useServers();
```

**Replace in** `web/src/components/template-instances.tsx`:

```tsx
  }

  return (
    <>
      <ul className="divide-y rounded-md border">
        {instances.map((inst) => {
```
**with:**

```tsx
  }

  const behind = instances.filter((i) => i.state === "created" && i.revision < view.latest_revision).length;

  return (
    <>
      {onUpgradeAll && behind > 0 ? (
        <div className="mb-3 flex items-center justify-end gap-3">
          <span className="text-sm text-muted-foreground">
            {behind} {behind === 1 ? "app is" : "apps are"} behind r{view.latest_revision}
          </span>
          <ConfirmButton
            trigger={
              <Button size="sm" disabled={rolloutActive} title={rolloutActive ? "An upgrade is running" : undefined}>
                Upgrade all to r{view.latest_revision}
              </Button>
            }
            title={`Upgrade ${behind} ${behind === 1 ? "app" : "apps"} to r${view.latest_revision}?`}
            description="They are upgraded one at a time, oldest first, each with its own values. An app that needs a new value is skipped (upgrade it on its own); the first failure stops the rest."
            confirmLabel="Upgrade all"
            onConfirm={onUpgradeAll}
          />
        </div>
      ) : null}
      <ul className="divide-y rounded-md border">
        {instances.map((inst) => {
```

**Replace in** `web/src/pages/template-page.tsx`:

```tsx
import { NotFound } from "@/components/not-found";
import { LoadError, PageSpinner } from "@/components/page-states";
import { TemplateEditor } from "@/components/template-editor";
import { TemplateInstances } from "@/components/template-instances";
import { TemplateRevisions } from "@/components/template-revisions";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, type InstanceView, type TemplateView } from "@/lib/templates";
```
**with:**

```tsx
import { NotFound } from "@/components/not-found";
import { LoadError, PageSpinner } from "@/components/page-states";
import { RolloutStrip } from "@/components/rollout-strip";
import { TemplateEditor } from "@/components/template-editor";
import { TemplateInstances } from "@/components/template-instances";
import { TemplateRevisions } from "@/components/template-revisions";
import { useServers } from "@/context/use-servers";
import { useRollout } from "@/hooks/use-rollout";
import { useTemplatesApi } from "@/hooks/use-templates-api";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { createRolloutsApi, isActive } from "@/lib/rollouts";
import { bodyFromDraft, draftFromBody, draftProblem, emptyDraft, type TemplateDraft } from "@/lib/template-draft";
import { ApiError, type InstanceView, type TemplateView } from "@/lib/templates";
```

**Replace in** `web/src/pages/template-page.tsx`:

```tsx
  const navigate = useNavigate();
  const api = useTemplatesApi();
  const prefill = (location.state as DraftState | null) ?? null;
  const [params, setParams] = useSearchParams();
```
**with:**

```tsx
  const navigate = useNavigate();
  const api = useTemplatesApi();
  const rolloutsApi = useMemo(() => createRolloutsApi(), []);
  const { servers } = useServers();
  const prefill = (location.state as DraftState | null) ?? null;
  const [params, setParams] = useSearchParams();
```

**Replace in** `web/src/pages/template-page.tsx`:

```tsx
    if (templateId) api.listInstances({ templateId }).then(setInstances, () => {});
  }, [api, templateId]);

  const dirty = baseline === null || !sameDraft(draft, baseline);
```
**with:**

```tsx
    if (templateId) api.listInstances({ templateId }).then(setInstances, () => {});
  }, [api, templateId]);

  // The template's "Upgrade all" rollout: each step it takes (and its end)
  // moves an instance, so the list follows.
  const { rollout, set: setRollout, stop } = useRollout(rolloutsApi, "template_upgrade", templateId ?? null);
  const progress = rollout ? `${rollout.id}:${rollout.done}:${rollout.skipped}:${rollout.state}` : "";
  useEffect(() => {
    if (progress) reloadInstances();
  }, [progress, reloadInstances]);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  const upgradeAll = async () => {
    if (!view) return;
    try {
      setRollout(await rolloutsApi.upgradeAll(view.id, view.latest_revision));
    } catch (e) {
      toast.error("Couldn't start the upgrade", { description: e instanceof Error ? e.message : undefined });
    }
  };

  const dirty = baseline === null || !sameDraft(draft, baseline);
```

**Replace in** `web/src/pages/template-page.tsx`:

```tsx
      </header>

      {prefill ? (
        <div className="space-y-2 rounded-md border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
```
**with:**

```tsx
      </header>

      <RolloutStrip rollout={rollout} serverName={serverName} onStop={stop} />

      {prefill ? (
        <div className="space-y-2 rounded-md border bg-muted/30 px-4 py-3 text-sm text-muted-foreground">
```

**Replace in** `web/src/pages/template-page.tsx`:

```tsx
          </TabsContent>
          <TabsContent value="instances" className="mt-4">
            <TemplateInstances view={view} instances={instances} onChanged={reloadInstances} />
          </TabsContent>
        </Tabs>
```
**with:**

```tsx
          </TabsContent>
          <TabsContent value="instances" className="mt-4">
            <TemplateInstances
              view={view}
              instances={instances}
              onChanged={reloadInstances}
              onUpgradeAll={() => void upgradeAll()}
              rolloutActive={!!rollout && isActive(rollout)}
            />
          </TabsContent>
        </Tabs>
```

**Replace in** `web/src/pages/org-variables.tsx`:

```tsx

import { DeleteVariableDialog } from "@/components/delete-variable-dialog";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
```
**with:**

```tsx

import { DeleteVariableDialog } from "@/components/delete-variable-dialog";
import { RolloutStrip } from "@/components/rollout-strip";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
```

**Replace in** `web/src/pages/org-variables.tsx`:

```tsx
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { useSharedVariablesPage, type Scope } from "@/hooks/use-shared-variables-page";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { createSharedVariablesApi, type SharedVariablesApi, type SyncState } from "@/lib/shared-variables";

```
**with:**

```tsx
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/ui/spinner";
import { useRollout } from "@/hooks/use-rollout";
import { useSharedVariablesPage, type Scope } from "@/hooks/use-shared-variables-page";
import { useAppBreadcrumbs } from "@/layouts/use-app-layout";
import { createRolloutsApi, type RolloutsApi } from "@/lib/rollouts";
import { createSharedVariablesApi, type SharedVariablesApi, type SyncState } from "@/lib/shared-variables";

```

**Replace in** `web/src/pages/org-variables.tsx`:

```tsx
// every server of the organization. Secrets show their coverage ("All
// servers" / "Missing on ..."); a new value is encrypted for every keyed
// server, "Provide value" only for the servers missing it. The logic lives
// in useSharedVariablesPage; this is the layout.
export default function OrgVariablesPage({ api: injected }: { api?: SharedVariablesApi }) {
  useAppBreadcrumbs(BREADCRUMBS);
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const page = useSharedVariablesPage(api, ORG_SCOPE);
  const { org: data } = page;

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
```
**with:**

```tsx
// every server of the organization. Secrets show their coverage ("All
// servers" / "Missing on ..."); a new value is encrypted for every keyed
// server, "Provide value" only for the servers missing it. The org's reload
// rollout (reload: true, Reload) shows above with Stop. The logic lives in
// useSharedVariablesPage; this is the layout.
export default function OrgVariablesPage({
  api: injected,
  rollouts: injectedRollouts,
}: {
  api?: SharedVariablesApi;
  rollouts?: RolloutsApi;
}) {
  useAppBreadcrumbs(BREADCRUMBS);
  const api = useMemo(() => injected ?? createSharedVariablesApi(), [injected]);
  const rollouts = useMemo(() => injectedRollouts ?? createRolloutsApi(), [injectedRollouts]);
  const page = useSharedVariablesPage(api, ORG_SCOPE);
  const { org: data } = page;
  const reload = useRollout(rollouts, "vars_reload");

  return (
    <div className="space-y-6">
      <RolloutStrip rollout={reload.rollout} serverName={(id) => page.serverNames.get(id) ?? id} onStop={reload.stop} />
      <Card>
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-x-4 gap-y-3 space-y-0">
```

**Replace in** `web/src/pages/settings-variables.tsx`:

```tsx

import { DeleteVariableDialog } from "@/components/delete-variable-dialog";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
```
**with:**

```tsx

import { DeleteVariableDialog } from "@/components/delete-variable-dialog";
import { RolloutStrip } from "@/components/rollout-strip";
import { SharedVariableDialog } from "@/components/shared-variable-dialog";
import { VariablesTable } from "@/components/variables-table";
```

**Replace in** `web/src/pages/settings-variables.tsx`:

```tsx
import { Spinner } from "@/components/ui/spinner";
import { useApps } from "@/context/use-apps";
import { useSharedVariablesPage, type Scope } from "@/hooks/use-shared-variables-page";
import { reloadableStaleCount } from "@/lib/app-status";
import type { SharedVariablesApi, SyncState } from "@/lib/shared-variables";

```
**with:**

```tsx
import { Spinner } from "@/components/ui/spinner";
import { useApps } from "@/context/use-apps";
import { useRollout } from "@/hooks/use-rollout";
import { useSharedVariablesPage, type Scope } from "@/hooks/use-shared-variables-page";
import { reloadableStaleCount } from "@/lib/app-status";
import { createRolloutsApi, type RolloutsApi } from "@/lib/rollouts";
import type { SharedVariablesApi, SyncState } from "@/lib/shared-variables";

```

**Replace in** `web/src/pages/settings-variables.tsx`:

```tsx
// "overridden" when shadowed), the agent's built-ins, a banner for org
// secrets this server has no copy of, and the sync state (from the API,
// then from vars.synced / vars.sync_failed events). The logic lives in
// useSharedVariablesPage; this is the layout.
export function VariablesCard({ serverId, api }: { serverId: string; api: SharedVariablesApi }) {
  const { staleVarsByApp, statusByApp } = useApps();
  const scope = useMemo<Scope>(() => ({ kind: "server", serverId }), [serverId]);
  const page = useSharedVariablesPage(api, scope);
  const { server: data } = page;

```
**with:**

```tsx
// "overridden" when shadowed), the agent's built-ins, a banner for org
// secrets this server has no copy of, and the sync state (from the API,
// then from vars.synced / vars.sync_failed events), and the org's reload
// rollout with Stop. The logic lives in useSharedVariablesPage; this is the
// layout.
export function VariablesCard({
  serverId,
  api,
  rollouts: injectedRollouts,
}: {
  serverId: string;
  api: SharedVariablesApi;
  rollouts?: RolloutsApi;
}) {
  const { staleVarsByApp, statusByApp } = useApps();
  const scope = useMemo<Scope>(() => ({ kind: "server", serverId }), [serverId]);
  const page = useSharedVariablesPage(api, scope);
  const rollouts = useMemo(() => injectedRollouts ?? createRolloutsApi(), [injectedRollouts]);
  const reload = useRollout(rollouts, "vars_reload");
  const { server: data } = page;

```

**Replace in** `web/src/pages/settings-variables.tsx`:

```tsx
      </CardHeader>
      <CardContent className="space-y-4">
        {missing.length > 0 ? (
          <Alert>
```
**with:**

```tsx
      </CardHeader>
      <CardContent className="space-y-4">
        <RolloutStrip rollout={reload.rollout} serverName={(id) => page.serverNames.get(id) ?? id} onStop={reload.stop} />
        {missing.length > 0 ? (
          <Alert>
```
Run:

```bash
pnpm --dir web exec tsc -b && pnpm --dir web run lint && pnpm --dir web exec vitest run
```

Expected: no type or lint errors; 89 files, 690 tests passed.

- [ ] **Commit**

```bash
git add web/src/components/template-instances.tsx web/src/components/template-instances.test.tsx web/src/pages
git commit -m "feat(web): Upgrade all to rN and the rollout strip

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Docs and the gates

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:** none.

- [ ] **Step 1: Document rollouts**

`.env.example` is unchanged (no env var was added).

**Replace in** `CLAUDE.md`:

```markdown
**Single gRPC envelope + typed commands** (the deliberate simplification over v1's ~14 oneof RPC pairs): all hub↔agent traffic rides one envelope (`proto.RequestEnvelope`/`ResponseEnvelope` = a `type` string + JSON `payload`). The catalog of command types and payload structs lives in `internal/domain/command`; `internal/infra/transport/codec` is the ONLY place that (de)serializes payloads. **To add a feature:** define a command type + payload struct in `internal/domain/command`, add one registration line in the agent Dispatcher's `newHandlers` map (`internal/app/agent/dispatcher.go`) plus the orchestrator method, and add a usecase/handler + route on the API side. Routes live in typed tables: each handler package's `Routes(g api.Guards) []api.Route` (`internal/app/web/api`) is both what `routes.go` registers with chi (`api.Register`, fail-closed on nil guards) and what `internal/app/web/api/openapi` renders into `api/openapi.yaml`; `api.Guards` carries only the auth rate limiter (`g.AuthLimit`) — there is no role guard. Run `make openapi` after any route or response-type change. Response-side schemas require every non-`omitempty` field and allow `null` for nil-able kinds; a type also used in a request body gets a separate lenient `<Name>Input` component. A new SSE notification type needs an `api.EventPayload` entry in `handler/notification/routes.go`. Add a contract test in `internal/app/web/contract` (happy path, plus a `probes` entry when the route takes input).

**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes, `delete_backups` its backups), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; ingress — `domain.list/create/delete` (registered domains, see below); storages — `storage.list/test/save/delete/orphans/delete-orphan` (see below); backups — `backup.update/points/run/restore/delete-point` (see below); shared variables — `vars.sync` (a server's full snapshot), `vars.reload` (re-render + compose up of stale running apps, queued on a worker; per-app outcomes arrive as `vars.reloaded` agent events); agent — `agent.update` (self-update). All are agent-bound; their routes answer 202 + `request_id` (result over SSE), except the shared-variable routes, which answer 200 once the DB write commits (sync/reload outcomes arrive as `vars.*` SSE events).

**Registered domains:** every ingress hostname must be covered by a domain registered on the same server (`{AGENT_DATA_DIR}/domains/`, mirrored into the API's `server_domains`): an exact name or `*.suffix` (one label; exact wins), each with a TLS mode — `auto` (ACME per host), `certificate` (uploaded PEM pair, key ECIES-encrypted like registry passwords, loaded via `load_files`) or `off` (plain HTTP behind a tunnel/proxy). The ingress `ssl` flag is ignored. `save-app` 400s on uncovered or per-server-duplicate hosts; the agent excludes uncovered hosts again in `BuildConfig`. The first start without `domains/` registers existing hostnames once (`ssl:true` → auto, else off).

**Shared variables:** org- and server-level variables and secrets (`shared_variables`, `shared_secret_ciphertexts`, `servers.vars_revision`; routes `/api/v1/org|server/*-shared-variable*`, `/api/v1/shared-variables/resync|reload`). Precedence built-ins < org < server < app; `COMPOSE_PROJECT_NAME` and `WF_*` are reserved; `WF_*` built-ins are agent-computed. Secrets use browser fan-out: one ECIES ciphertext per keyed server, the API never sees plaintext; a server key change purges that server's copies. The API pushes the full snapshot as `vars.sync` on save/delete, agent connect and revision drift (`apps.status` carries `vars_revision`); the agent stores `{AGENT_DATA_DIR}/vars/snapshot.json` (0600, ciphertext only) and renders only referenced names into the gitignored `.env.shared` / `.env.shared.secrets` (0600) on the paths that call `materializeSecrets`, never on sync. Compose env files (composeArgsFor and run.sh): `.env.shared .env.shared.secrets .env .winterflow/storage.env .env.secrets`. Stale apps ride `apps.status` as `stale_vars`; reloads run on one worker goroutine under the per-app operation lock and register with `beginOp` (refused while a restore runs).


```
**with:**

```markdown
**Single gRPC envelope + typed commands** (the deliberate simplification over v1's ~14 oneof RPC pairs): all hub↔agent traffic rides one envelope (`proto.RequestEnvelope`/`ResponseEnvelope` = a `type` string + JSON `payload`). The catalog of command types and payload structs lives in `internal/domain/command`; `internal/infra/transport/codec` is the ONLY place that (de)serializes payloads. **To add a feature:** define a command type + payload struct in `internal/domain/command`, add one registration line in the agent Dispatcher's `newHandlers` map (`internal/app/agent/dispatcher.go`) plus the orchestrator method, and add a usecase/handler + route on the API side. Routes live in typed tables: each handler package's `Routes(g api.Guards) []api.Route` (`internal/app/web/api`) is both what `routes.go` registers with chi (`api.Register`, fail-closed on nil guards) and what `internal/app/web/api/openapi` renders into `api/openapi.yaml`; `api.Guards` carries only the auth rate limiter (`g.AuthLimit`) — there is no role guard. Run `make openapi` after any route or response-type change. Response-side schemas require every non-`omitempty` field and allow `null` for nil-able kinds; a type also used in a request body gets a separate lenient `<Name>Input` component. A new SSE notification type needs an `api.EventPayload` entry in `handler/notification/routes.go`. Add a contract test in `internal/app/web/contract` (happy path, plus a `probes` entry when the route takes input).

**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes, `delete_backups` its backups), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; ingress — `domain.list/create/delete` (registered domains, see below); storages — `storage.list/test/save/delete/orphans/delete-orphan` (see below); backups — `backup.update/points/run/restore/delete-point` (see below); shared variables — `vars.sync` (a server's full snapshot), `vars.reload` (re-render + compose up of stale running apps, queued on a worker; per-app outcomes arrive as `vars.reloaded` agent events; with exactly one `app_ids` entry it waits for that app's outcome, at most `command.ReloadAppWait`, which is why `vars.reload` is a detached command); agent — `agent.update` (self-update). All are agent-bound; their routes answer 202 + `request_id` (result over SSE), except the shared-variable routes, which answer 200 once the DB write commits (sync outcomes arrive as `vars.*` SSE events, reloads as `rollout` events).

**Registered domains:** every ingress hostname must be covered by a domain registered on the same server (`{AGENT_DATA_DIR}/domains/`, mirrored into the API's `server_domains`): an exact name or `*.suffix` (one label; exact wins), each with a TLS mode — `auto` (ACME per host), `certificate` (uploaded PEM pair, key ECIES-encrypted like registry passwords, loaded via `load_files`) or `off` (plain HTTP behind a tunnel/proxy). The ingress `ssl` flag is ignored. `save-app` 400s on uncovered or per-server-duplicate hosts; the agent excludes uncovered hosts again in `BuildConfig`. The first start without `domains/` registers existing hostnames once (`ssl:true` → auto, else off).

**Shared variables:** org- and server-level variables and secrets (`shared_variables`, `shared_secret_ciphertexts`, `servers.vars_revision`; routes `/api/v1/org|server/*-shared-variable*`, `/api/v1/shared-variables/resync|reload`). Precedence built-ins < org < server < app; `COMPOSE_PROJECT_NAME` and `WF_*` are reserved; `WF_*` built-ins are agent-computed. Secrets use browser fan-out: one ECIES ciphertext per keyed server, the API never sees plaintext; a server key change purges that server's copies. The API pushes the full snapshot as `vars.sync` on save/delete, agent connect and revision drift (`apps.status` carries `vars_revision`); the agent stores `{AGENT_DATA_DIR}/vars/snapshot.json` (0600, ciphertext only) and renders only referenced names into the gitignored `.env.shared` / `.env.shared.secrets` (0600) on the paths that call `materializeSecrets`, never on sync. Compose env files (composeArgsFor and run.sh): `.env.shared .env.shared.secrets .env .winterflow/storage.env .env.secrets`. Stale apps ride `apps.status` as `stale_vars`; reloads run on one worker goroutine under the per-app operation lock and register with `beginOp` (refused while a restore runs). The API never sends a bulk `vars.reload` any more: `reload: true` on save/delete and `POST /api/v1/shared-variables/reload` queue or extend the org's `vars_reload` rollout (see **Rollouts**), which reloads the stale running apps one at a time with the single-app form.


```

**Replace in** `CLAUDE.md`:

```markdown
 Two per-app locks exist: the **operation lock** (`applock.go`: `lockForMutation`, held for a public mutator's whole run — save, start/stop/restart/update, delete, rollback, rename, `backup.update`, storage redeploys, the source poll — giving up after 30s with `ErrAppBusy` so the standalone bridge never wedges; the reload worker waits ctx-bounded) and the file lock above. Order: operation lock → `beginOp`/`beginRestore` → `lockApp` → `lockSource` → `sharedMu` (a save or source poll releases `lockSource` before taking `lockApp`; only a rollback holds both). A restore never takes the operation lock; `beginRestore`/`beginOp` exclude it from the mutators and reloads.

**Templates and instances (API only; the agent never sees a template):** a template is an org's stack definition with immutable revisions (`templates`, `template_revisions`; body = files, parameters, app name/icon/color, ingress rows, backup defaults, validated by `internal/domain/template`: ≤1 MiB, RE2 patterns ≤512 B, no default on a secret parameter, undeclared `${VAR}`s in files only warn). An instance (`template_instances`) is an ordinary app rendered from a revision and the caller's values by `template.Render` into the `app.save` payload the editor would send (files unchanged, one variable per parameter, secrets as the caller's ECIES ciphertext or the `<encrypted>` keep placeholder, config blob with the rendered ingress, `template_id` and the `instance` marker) and dispatched through the app usecase's `CheckSave` + `DispatchSave`, so every save-app check applies before anything is claimed; a secret must have the ECIES wire format's shape (`crypto.CheckCiphertext`) and a plain value (or default) may not contain `$` (compose interpolates `.env`). An ingress row's `port` is a number or `${PARAM}` of a plain parameter and its `service` is informational (the ingress proxies a hostname to `127.0.0.1:<port>` on the host, so each instance publishes its own host port, e.g. `127.0.0.1:${PORT}:8080`). The caller's `key` is unique per org among instances not unlinked (partial unique index); a create claims it only after the checks pass, a replay with the same request hash (server, template, requested revision, plain values, secret *names*, backup overrides) answers the created instance or re-dispatches a pending one, another request is 409, a deleted instance's key stays retired (409), unlink frees it. Updating or upgrading a pending instance renders like a create (every secret resent, the full backups list sent, a `backups` override accepted only while pending). Instance routes take `wait: true`: the handler holds only its own request on a buffered channel fed by the dispatch result observer (which records the outcome either way; an observed dispatch keeps its result for `appuc.ObservedResultTTL`, 15 minutes, beyond the 10-minute wait), at most 10 minutes, then answers 202; a failed command is 502 and stays as `last_error`. An upgrade sends `instance.backup_defaults` for backups new in the revision, which the agent's save (`applyBackupDefaults`) adds where the app has no entry and then drops. An instance's app is changed through its instance: save-app and rollback-app on an app a pending or created instance manages answer 409 (`port.InstanceLinks`, served by the templates repository and given to the app usecase; rename is allowed), and delete-app's success retires its instance's key like delete-instance. Upgrade-all and rollouts are D1b.

**App secrets are ECIES-encrypted end-to-end** (`pkg/crypto`, P-256 ECDH→SHA-256→AES-256-GCM): the browser encrypts a secret with the agent's public key (served by `GET /api/v1/server/get-public-key`, sourced from the `public_key` capability or the local agent cert in standalone); the agent decrypts with its mTLS private key before render. Editing sends the `<encrypted>` placeholder for unchanged secrets (the agent preserves the stored value, never returns plaintext). Registry passwords use the same scheme.
```
**with:**

```markdown
 Two per-app locks exist: the **operation lock** (`applock.go`: `lockForMutation`, held for a public mutator's whole run — save, start/stop/restart/update, delete, rollback, rename, `backup.update`, storage redeploys, the source poll — giving up after 30s with `ErrAppBusy` so the standalone bridge never wedges; the reload worker waits ctx-bounded) and the file lock above. Order: operation lock → `beginOp`/`beginRestore` → `lockApp` → `lockSource` → `sharedMu` (a save or source poll releases `lockSource` before taking `lockApp`; only a rollback holds both). A restore never takes the operation lock; `beginRestore`/`beginOp` exclude it from the mutators and reloads.

**Templates and instances (API only; the agent never sees a template):** a template is an org's stack definition with immutable revisions (`templates`, `template_revisions`; body = files, parameters, app name/icon/color, ingress rows, backup defaults, validated by `internal/domain/template`: ≤1 MiB, RE2 patterns ≤512 B, no default on a secret parameter, undeclared `${VAR}`s in files only warn). An instance (`template_instances`) is an ordinary app rendered from a revision and the caller's values by `template.Render` into the `app.save` payload the editor would send (files unchanged, one variable per parameter, secrets as the caller's ECIES ciphertext or the `<encrypted>` keep placeholder, config blob with the rendered ingress, `template_id` and the `instance` marker) and dispatched through the app usecase's `CheckSave` + `DispatchSave`, so every save-app check applies before anything is claimed; a secret must have the ECIES wire format's shape (`crypto.CheckCiphertext`) and a plain value (or default) may not contain `$` (compose interpolates `.env`). An ingress row's `port` is a number or `${PARAM}` of a plain parameter and its `service` is informational (the ingress proxies a hostname to `127.0.0.1:<port>` on the host, so each instance publishes its own host port, e.g. `127.0.0.1:${PORT}:8080`). The caller's `key` is unique per org among instances not unlinked (partial unique index); a create claims it only after the checks pass, a replay with the same request hash (server, template, requested revision, plain values, secret *names*, backup overrides) answers the created instance or re-dispatches a pending one, another request is 409, a deleted instance's key stays retired (409), unlink frees it. Updating or upgrading a pending instance renders like a create (every secret resent, the full backups list sent, a `backups` override accepted only while pending). Instance routes take `wait: true`: the handler holds only its own request on a buffered channel fed by the dispatch result observer (which records the outcome either way; an observed dispatch keeps its result for `appuc.ObservedResultTTL`, 15 minutes, beyond the 10-minute wait), at most 10 minutes, then answers 202; a failed command is 502 and stays as `last_error`. An upgrade sends `instance.backup_defaults` for backups new in the revision, which the agent's save (`applyBackupDefaults`) adds where the app has no entry and then drops. An instance's app is changed through its instance: save-app and rollback-app on an app a pending or created instance manages answer 409 (`port.InstanceLinks`, served by the templates repository and given to the app usecase; rename is allowed), and delete-app's success retires its instance's key like delete-instance. `POST /api/v1/template/upgrade-instances` ("Upgrade all") queues the template's `template_upgrade` rollout.

**Rollouts:** the queue of one-app-at-a-time jobs (`rollouts`, `internal/domain/usecase/rollouts`; routes `/api/v1/template/upgrade-instances`, `/api/v1/rollout/get-rollouts|stop-rollout`; an SSE `rollout` event, a `model.RolloutView`, to the org on every change and only then). Kinds: `template_upgrade` (scope key the template id; the oldest `created` instance below the target revision is re-rendered with its stored values through the instance update path; one whose values the revision refuses, `templates.ErrNeedsValues`, or that went away is skipped) and `vars_reload` (scope key the org id; scope `model.ReloadScope`: names × server_ids, all_stale servers, app_ids, plus each server's `revisions` and `settle_by`; the next app is a running app whose `stale_vars` in the API's status cache the scope wants, on a server whose latest report reached the scope's revision; until `settle_by`, 5 minutes, it waits for a server that has not, then leaves it out; a step sends the single-app `vars.reload`). One active rollout per (kind, scope_key) (partial unique index): a second upgrade of a template is 409, a reload extends the active one (scope merged in Go, written by one conditional UPDATE on `scope_rev`; the worker completes a rollout only at the `scope_rev` it evaluated). One worker goroutine per API process, started by `WireCore` (`Deps.Rollouts` is the one value whose `Wake` reaches it): a pass on every wake and once a minute (one indexed query when idle); a 2-minute lease claimed in one statement and renewed every 30 s, every worker write naming its owner (a lost lease cancels the step in flight; another process resumes after expiry); a step picks the next app fresh, records `current_app_id`, dispatches and awaits the result (10.5 minutes), then waits for a status report received after it showing the app Active (5 minutes; Idle passes at the bound; a Docker healthcheck's `unhealthy` is not seen: such a container reads running) and counts it, skips it (`skip_ids`, never picked again) or fails the rollout naming the app; `stop_requested` stops it after the step in flight. No transaction is held while a step waits. Ended rollouts beyond the newest 20 per scope are pruned when one ends. The status cache keeps each report's receive time and `vars_revision` (`status.Cache.LatestReport`).

**App secrets are ECIES-encrypted end-to-end** (`pkg/crypto`, P-256 ECDH→SHA-256→AES-256-GCM): the browser encrypts a secret with the agent's public key (served by `GET /api/v1/server/get-public-key`, sourced from the `public_key` capability or the local agent cert in standalone); the agent decrypts with its mTLS private key before render. Editing sends the `<encrypted>` placeholder for unchanged secrets (the agent preserves the stored value, never returns plaintext). Registry passwords use the same scheme.
```
- [ ] **Step 2: Run the gates**

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1b; mkdir -p $GOTMPDIR
gofmt -l ./internal ./cmd ./pkg ./tools          # expect: no output
go vet ./...                                      # expect: no output
make sqlc-check                                   # expect: no diff
make openapi && git diff --exit-code api/openapi.yaml   # expect: exit 0
WF_CONTRACT_GUARD=require make test               # expect: every package ok; coverage.out written
make test-pg                                      # expect: ok (contract suite + internal/infra/db on postgres:16)
pnpm --dir web run lint && pnpm --dir web exec tsc -b && pnpm --dir web run build && git checkout -- web/dist/.gitkeep
```

Then the coverage gate as CI computes it (the Postgres profile is the only coverage of `repository/rollout/pgsql.go`):

```bash
make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out'
tail -n +2 contract-postgres.out >> coverage.out
make web-test
go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info
```

Expected: `go … changed lines covered (≈88%) PASS`, `web 78/78 changed lines covered (100.0%) PASS`. If CI prints `::notice::raise coverage-baseline.json …`, bump `coverage-baseline.json` in the same PR.
- [ ] **Commit**

```bash
git add CLAUDE.md
git commit -m "docs: the rollout queue

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review

**Spec coverage (D1b):**

| Spec item | Task |
|---|---|
| `rollouts` table, partial unique index on active (kind, scope_key) | 1 |
| `POST /api/v1/template/upgrade-instances` `{id, revision}`; 409 while active | 6, 7 |
| `GET /api/v1/rollout/get-rollouts` (`kind`, `scope_key?`: active + last 20 ended); `POST /api/v1/rollout/stop-rollout` `{id}` | 1, 5, 7 |
| Shared-variable routes keep paths/codes; `reload: true` and `/shared-variables/reload` queue or extend a `vars_reload` rollout | 8 |
| SSE `rollout` to the org on change; `api.EventPayload`; `make openapi` | 5, 7 |
| Worker: one goroutine per API process, wake + 1-minute tick, single-statement claim, 2-minute lease renewed, no transaction while waiting | 5, 7 |
| Step: fresh pick → `current_app_id` → dispatch + await 10 min → health on a later report ≤ 5 min → count / fail-and-stop / `stop_requested` → done | 5, 6, 8 |
| `template_upgrade`: oldest created instance below the revision; skipped (counted) when the revision needs a value | 6 |
| `vars_reload`: next app with a running container whose `stale_vars` include a name; `vars.reload {app_ids:[id]}` awaited | 8 |
| Resume after lease expiry; one active per scope; `vars_reload` extension in one statement; pruning to 20 per scope | 1, 5, 8 |
| Agent: single-app `vars.reload` detached, queued on the reload worker, reply carries the outcome, bounded; bulk form kept | 4 |
| UI: Instances "Upgrade all to rN" (confirm first), rollout strip with Stop on the template page, the server's Variables tab and the org Variables page; a failure names the app and links to it | 9, 10, 11 |
| Errors: 404 another org's template/rollout, 409 active upgrade, 400 bad input | 7, 8 |
| Testing: worker (claim, lease renewal, takeover, resume, stop, first failure, skip, one active per scope, extension, pruning) with `-race`; repository incl. the claim and the partial index on both engines; contract happy paths + probes + 404s + parity; agent waitable reload; web strip and Stop | 1–11 |
| Open items: healthcheck `unhealthy` (Rulings), migration number (Rulings) | — |
| Controller ruling (security review): instance routes require the server in the caller's org; `template_upgrade` picks only instances on the org's servers | 2, 6 |

**Placeholder scan:** every code step carries a whole file or exact replace blocks taken from the scratch run; each "Run" names its command and expected result.

**Type consistency:** `model.RolloutView` is the one view type (routes, SSE, web `Rollout`); `pick`/`stepResult`/`runner` are defined once (Task 5) and used unchanged by `upgrader` (Task 6) and `reloader` (Task 8); `QueueReload(ctx, userID, orgID, model.ReloadScope)` is the same in `rollouts` and `sharedvars.ReloadQueue`; `useRollout` returns `{ rollout, set, stop }` and the three pages read exactly those.
