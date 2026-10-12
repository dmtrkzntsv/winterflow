# D1a: Templates and Instances API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Org-level templates with immutable revisions, and instances (ordinary apps rendered from a revision and the caller's parameter values) driven over an idempotent, key-addressed API that can wait for the agent's outcome: the D1a slice of the templates spec, usable from `curl` and the twillingate-cloud portal.

**Architecture:** Three new tables (migration `0013_templates.sql`: `templates`, `template_revisions`, `template_instances`) behind a sqlc repository. A pure domain package `internal/domain/template` validates a revision body and renders (body, values) into exactly the `app.save` payload the app editor would send; the API never sends the agent anything template-shaped except an optional `instance.backup_defaults` list that the agent's save merges. A new usecase `internal/domain/usecase/templates` claims keys, replays creates, and dispatches through the app usecase (split into `CheckSave` + `DispatchSave` with a result observer, so every save-app check runs before the key is claimed). `wait: true` is a per-request wait on a buffered channel fed by that observer, bounded at 10 minutes, after which the call answers 202.

**Tech Stack:** Go 1.25, chi, sqlc over SQLite (`modernc.org/sqlite`) / Postgres (`pgx/v5`), swaggest OpenAPI generator, kin-openapi contract suite. No web changes (D1c), no rollout queue (D1b).

**Spec:** docs/superpowers/specs/2026-10-11-templates-design.md (D1a = "Delivery" item 1)

**Base:** branch `feat/templates` (HEAD `fd96d5b`, the spec commit, on top of `main` `e4cc703`). Every diff below is against that base plus the earlier tasks. The code in this plan was implemented and run in a scratch worktree before the plan was written: `WF_CONTRACT_GUARD=require make test`, the contract suite and `internal/infra/db/...` on Postgres 16, `make sqlc-check`, `make openapi` and `go vet ./...` all passed on the final state, and the Task 2, 6 and 7 intermediate states were built and tested on their own.

## Global Constraints

- Run `gofmt -w` on every Go file you touch before committing; gofmt is the authority over the alignment shown here.
- Go commands use a private temp dir: `export GOTMPDIR=$HOME/.cache/wf-gotmp-d1a; mkdir -p $GOTMPDIR` before the first `go` command of a task.
- Layering: `internal/domain/**` never imports `internal/infra/**` outside `_test.go` files.
- Persistence: per-engine SQL only (`internal/infra/db/sqlc/query/{sqlite,pgsql}/templates.sql`, `internal/infra/db/migrations/{sqlite,pgsql}/0013_templates.sql`, identical apart from `UUID` vs `VARCHAR(36)` and `$n` vs `?`), `make sqlc` after any query or migration change, never edit `internal/infra/db/sqlc/gen` by hand, keep `.sql` files ASCII, `Tx.WithTx` with `Tx.Querier(ctx)` inside `fn`. Add every new sqlc row/param type pair to `internal/infra/db/sqlc/shapes_test.go`; every new TIMESTAMP column needs a write in `internal/infra/db/repository/timestamps_test.go`.
- Migration number: `0013` is the next free number (`0012_shared_variables.sql` is the newest on `main` and on this branch). Renumber at merge if something else lands first (rename both files, `make sqlc`).
- Template body rules (spec, verbatim): template name `^[a-z][a-z0-9-]{0,62}$`, unique in the org; parameter names `^[A-Z][A-Z0-9_]*$`, unique, not `COMPOSE_PROJECT_NAME`, not starting with `WF_`; `pattern` compiles with Go's `regexp` (RE2), at most 512 bytes; a `default` must match its parameter's `pattern`; a secret parameter has no `default`; `app_name` and ingress hosts reference only plain parameters; the compose recipe passes `internal/domain/backup/recipe`; every `backups` default names a backup the recipe has; the body is at most 1 MiB.
- Undeclared `${VAR}` references in files are a **warning**, never a refusal: "`SMTP_PASS is not a parameter; it must come from a shared variable on the server`" for each reference that is not a parameter, not `WF_*`/`COMPOSE_PROJECT_NAME`, not an org shared variable and has no fallback of its own (`${X:-y}`, `${X-y}`, `${X:+y}`, `${X+y}`). The scanner is the shared-variables one, moved to `internal/domain/varref`.
- Rendering substitutes only plain parameters, only into `app_name`, ingress hosts and ingress ports; there is no expression language. Files are copied unchanged.
- Secrets: values reach the API only as the caller's ECIES ciphertext and pass unchanged into encrypted app variables; on update/upgrade/unlink a secret not given again is sent as `command.EncryptedPlaceholder` (`<encrypted>`). No route returns a secret value or ciphertext: `get-instance` lists secret names with `<encrypted>`.
- Security: every instance route checks `webutil.RequireServerAccess` (403) before anything else; template routes are scoped to the caller's org (`PrimaryOrganizationID`); a template or key of another org answers 404; template ids must be canonical UUIDs (400 otherwise, so SQLite and Postgres answer alike).
- Bounded waits: `wait` holds only its own request, at most `WaitBound = 10 * time.Minute`, then answers 202 with the `request_id`; a client that goes away ends the wait; the result hook never blocks (buffered channel, non-blocking send) and records the outcome whether or not anyone waits; result DB writes use a 30 s context.
- Routes live in `Routes(g api.Guards) []api.Route` tables; after any route or response-type change run `make openapi` and `go test ./internal/app/web -run TestRoutesGolden -update`, and commit both. Every new route needs a happy-path contract test (the `TestMain` guard) and a `probes` entry (`TestProbesCoverTable`; server-addressed routes need a `valid` call for `TestForeignServer`).
- No new env var (`.env.example` unchanged), no new SSE notification type (instance results are ordinary `operation_result`s), no web change, no `upgrade-instances` route and no `rollouts` table (D1b).
- Commits: Conventional Commits, each message ending with the line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- CI gates: changed Go lines ≥ 80% covered (`go run ./tools/diffcov --base origin/main --min 80 ...`), coverage ratchet, contract suite on SQLite and Postgres, `make sqlc-check`, OpenAPI up to date.

## Review Focus

1. **A retried create carries a freshly encrypted secret.** ECIES ciphertexts differ on every encryption, so a portal retry after a lost response sends different bytes for the same secret. Expected: the same request (replayed, no 409, no second app). Pinned in Task 7 (`TestCreateReplays` resends `CT-other-bytes`).
2. **A backup the user turned off in the Backups tab, then an update or upgrade.** `recipe.Clean` drops an override that only says "off", so "names without an entry" alone would re-enable it from the template default. Expected: it stays off; only backups the new revision's compose file adds get defaults. Pinned in Task 3 (`TestNewBackupDefaultsOnlyForBackupsNewInTheRevision`: a changed default for an existing backup is not re-sent) and Task 4 (`TestSaveAppliesInstanceBackupDefaults`: a stored entry wins).
3. **The caller stops waiting** (client timeout, disconnect, or the 10-minute bound). Expected: the agent's later result still moves the instance to `created` (or records `last_error`), and delivering it never blocks the bus goroutine. Pinned in Task 7 (`TestResultRecordedWithoutAWaiter`, `TestRespond`'s gone-client and past-the-bound cases).
4. **Unlink, then create again with the same key.** Expected: a new instance with a new app id; the unlinked app is untouched and no longer carries `template_id`. Pinned in Task 9 (`TestInstanceLifecycle/a_freed_key_makes_a_new_app;_delete_retires_it`, `TestDeleteRetiresAndUnlinkFrees`).
5. **A save-app check fails on create** (host already used by another app, storage missing on the server). Expected: 400 and the key is not claimed, so the caller can fix the request and reuse the key instead of hitting a 409. Pinned in Task 7 (`TestCreateRefusedBeforeTheClaim`, contract `a host another app holds is refused before the key is claimed`).

## Decisions on the spec's open items

1. **How `wait` is implemented API-side.** There is no blocking reply manager any more: `dispatch.Manager.Dispatch` is fire-and-forward and `HandleResult` runs the command's `OnResult` on the bus response subscriber, then publishes the SSE notification (`port.DispatchInput.Wait` only lengthens the pending TTL, as `backup.run` uses it). The app usecase gains an `Observer` (`func(port.CommandResult)`) that runs after its own `OnResult` work (catalog upsert, domain index). The templates usecase wraps each instance command's observer as: record the outcome in `template_instances` (30 s context), then a non-blocking send on a `chan port.CommandResult` of capacity 1, returned as `Outcome.Done`. The handler (`respond`) answers 202 at once without `wait`; with `wait` it selects on `Done`, a `time.NewTimer(WaitBound)` and `r.Context().Done()`: a result gives 200 `{instance, request_id, warnings}` (the instance re-read through `Outcome.Final` after the DB write) or 502 with the agent's error; the bound gives 202 `{request_id}`; a gone client gets nothing. Only the request's own goroutine blocks; the HTTP server has no write timeout (`internal/app/web/bootstrap.go`), and `dispatch.pendingTTL` is already 10 minutes, so the result is never dropped as expired before the bound. Pub/sub fans responses out to every API replica, so the replica holding the request sees its result.
2. **Backup defaults on update and upgrade without the API reading the app's overrides.** The API never sends a `backups` key on update, upgrade or unlink, so `keepBackups` (agent, `writeAppStore`) keeps the stored list. On an upgrade the marker carries `instance.backup_defaults`: the template defaults of backups that the **new** revision's compose recipe declares and the **current** revision's does not (`template.NewBackupDefaults(prev, next)`, computed from the two stored revision bodies, never from the app). The agent's `applyBackupDefaults` (new, `backupconfig.go`, applied right after `keepBackups`) appends each default whose name has no entry in the backups list and deletes `backup_defaults` from the stored marker. A backup both revisions declare keeps the app's settings, Backups-tab changes included, and a plain "off" that `recipe.Clean` minimized away is not re-enabled. The API checks that an enabled default's storage exists on the server in `checkBackups` (same 400 as a backups list). An agent older than this change stores the key unused (no new backups get defaults) — acceptable, API and agent ship together.
3. **Migration number:** `0013` (see Global Constraints).
4. (Healthcheck `unhealthy` vs Active belongs to the rollout health wait: D1b.)

## Rulings on spec ambiguities (and why)

- **Ingress `port` may be `"${PARAM}"`; `service` is informational.** Today's ingress proxies a hostname to `127.0.0.1:<port>` on the host (`proxyRoute`); it has no notion of a compose service. The spec's example (`service: app, port: 8080`) would make every tenant on one host claim port 8080. So a row's `port` is a JSON number or a string that is a number or exactly `${PARAM}` of a plain parameter (rendered and checked as 1–65535, 400 naming the parameter), and `service` is stored for people and D2 only. The twillingate template publishes `127.0.0.1:${PORT}:8080` and uses `port: "${PORT}"`.
- **Extra columns:** `templates.latest_revision` (the optimistic `base_revision` check is one `UPDATE ... WHERE latest_revision = ?`) and `templates.updated_at`; `template_instances.last_error` (spec's `get-instance` returns "last error", the table listed none). The key column is `instance_key` (keyword-safe on both engines).
- **Stored values are the values callers set**; defaults are applied at each render, so an upgrade follows a changed default. `get-instance.parameters` shows the set values.
- **Request hash** = SHA-256 of `{server_id, template_id, revision as requested (0 = latest), plain values as sent, sorted secret names, backup overrides}`. The backup overrides are part of the request too (a different storage is a different create).
- **`wait` answers:** 200 `{instance, request_id, warnings}` (an unlinked instance is answered with `state: "unlinked"`, since its key no longer reads it), 502 with the agent's error, 202 past the bound. A created-instance replay and a delete of an already deleted instance answer 200 with `request_id: ""` and dispatch nothing.
- **State rules:** update and upgrade work on `pending` and `created` instances (they fix a failed create without a 409); unlink needs `created` (409 `the instance was never created`); every call on a `deleted` key is 409 `this key belonged to a deleted instance`, except delete (idempotent) and get-instance (shows it). `get-instances` lists `pending` and `created` only.
- **Control** accepts `start`, `stop`, `restart` only (the spec's list); `update` is 400.
- **Validation details the spec leaves open:** key `^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`; a required plain parameter may not be empty; a `backups` override must name a backup the template has a default for; file paths are relative, cleaned, not under `.git/` or `.winterflow/`, and not `.env`, `.env.secrets`, `.env.shared`, `.env.shared.secrets`, `.gitignore` (the agent writes those); a name collision is 400 (the spec lists it under validation), a stale `base_revision` 409.
- **Org:** the instance's org is the caller's primary org, as for shared variables (AGENTS.md: one user per org); the server is authorized separately with `RequireServerAccess`.

---

## File map

| File | Task | Change |
|---|---|---|
| `internal/infra/db/migrations/{sqlite,pgsql}/0013_templates.sql` | 1 | new tables |
| `internal/infra/db/sqlc/query/{sqlite,pgsql}/templates.sql`, `sqlc/gen/**` (generated), `sqlc/shapes_test.go` | 1 | queries |
| `internal/domain/model/template.go`, `internal/domain/port/template.go` | 1 | types, error classes, port |
| `internal/infra/db/repository/template/{repo,sqlite,pgsql,repo_test}.go`, `repository/timestamps_test.go` | 1 | repository |
| `internal/infra/bootstrap/{deps,core}.go` | 1 | wire the repository |
| `internal/domain/varref/{refs,refs_test}.go` (moved from `internal/infra/vars/refs.go`), `internal/infra/vars/refs.go` | 2 | shared scanner + `Undefaulted` |
| `internal/domain/template/{body,validate,ingress,validate_test}.go` | 2 | body model, validation, warnings |
| `internal/domain/template/{values,render,hash,render_test}.go` | 3 | values, rendering, backup defaults, request hash |
| `internal/infra/orchestrator/docker_compose/{backupconfig,store,backupconfig_test}.go` | 4 | agent merges `instance.backup_defaults` |
| `internal/domain/usecase/app/{usecase,backups}.go`, `observe_test.go` | 5 | `CheckSave`/`DispatchSave`/observers; defaults' storages |
| `internal/app/web/util/response.go`, `internal/app/web/openapi_test.go` | 6, 7 | `ErrorStatus` (404/409/502) |
| `internal/domain/usecase/templates/{usecase,views,templates,fixture_test,templates_test}.go` | 6 | template usecase |
| `internal/app/web/handler/templates/{handler,templates,routes,handler_test}.go`, `internal/app/web/routes.go` | 6 | template routes |
| `internal/app/web/contract/{templates_test,matrix_test}.go` | 6–9 | contract tests and probes |
| `internal/app/web/api/{route.go,openapi/openapi.go,openapi/openapi_test.go}` | 7 | Accepted routes may also answer 200 |
| `internal/domain/usecase/templates/{outcome,instance_views,instances,instances_test}.go` | 7 | create/replay/get/list |
| `internal/app/web/handler/templates/{respond,instances,instance_routes,respond_test}.go` | 7 | wait, create/get/list routes |
| `internal/app/web/contract/instances_test.go` | 7–9 | instance contract tests |
| `internal/domain/usecase/templates/{update,update_test}.go`, `handler/templates/update.go` | 8 | update/upgrade |
| `internal/domain/usecase/templates/{lifecycle,lifecycle_test}.go`, `handler/templates/lifecycle.go` | 9 | control/delete/unlink |
| `api/openapi.yaml`, `internal/app/web/testdata/routes.golden` | 6–9 | regenerated |
| `internal/app/web/contract/{parity_test.go,testdata/parity.golden.json}`, `CLAUDE.md` | 10 | parity, docs, gates |

---
### Task 1: Tables, queries and the templates repository

**Files:**
- Create: `internal/infra/db/migrations/sqlite/0013_templates.sql`, `internal/infra/db/migrations/pgsql/0013_templates.sql`
- Create: `internal/infra/db/sqlc/query/sqlite/templates.sql`, `internal/infra/db/sqlc/query/pgsql/templates.sql`
- Generated (do not edit): `internal/infra/db/sqlc/gen/{sqlite,pgsql}/{models.go,querier.go,templates.sql.go}`
- Create: `internal/domain/model/template.go`, `internal/domain/port/template.go`
- Create: `internal/infra/db/repository/template/{repo.go,sqlite.go,pgsql.go,repo_test.go}`
- Modify: `internal/infra/db/sqlc/shapes_test.go`, `internal/infra/db/repository/timestamps_test.go`
- Modify: `internal/infra/bootstrap/deps.go`, `internal/infra/bootstrap/core.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `model.ErrNotFound`, `model.ErrConflict` (classes: `errors.Is(err, model.ErrNotFound)` → 404, `model.ErrConflict` → 409); sentinels `model.ErrTemplateNotFound`, `ErrTemplateRevisionGone`, `ErrInstanceNotFound` (NotFound class), `ErrRevisionConflict`, `ErrTemplateInUse`, `ErrKeyInUse`, `ErrKeyRetired`, `ErrInstanceNotCreated` (Conflict class), `ErrTemplateNameTaken` (matches `model.ErrRejected`, 400).
  - `model.Template{ID, OrgID, Name string; LatestRevision int64; CreatedAt time.Time; CreatedBy string; UpdatedAt time.Time}`, `model.TemplateSummary{Template; Instances, Behind int64}`, `model.TemplateRevision{TemplateID string; Revision int64; Body []byte; Note string; CreatedAt time.Time; CreatedBy string}`.
  - `model.InstanceState` with `InstancePending = "pending"`, `InstanceCreated = "created"`, `InstanceDeleted = "deleted"`, `InstanceUnlinked = "unlinked"`; `model.Instance{ID, OrgID, Key, AppID, ServerID, TemplateID string; Revision int64; Params map[string]string; SecretNames []string; RequestHash string; State InstanceState; LastError string; CreatedAt, UpdatedAt time.Time}`.
  - `port.TemplateRepository` (methods `CreateTemplate`, `AddRevision`, `GetTemplate`, `ListTemplates`, `GetRevision`, `ListRevisions`, `DeleteTemplate`, `ClaimInstance`, `GetInstance`, `ListInstances`, `MarkSaved`, `SetState`, `SetError`, signatures below).
  - `templaterepo.New(tx *backend.TxManager, driver string, log *logger.Logger) (*templaterepo.Repo, error)` (package `winterflow/internal/infra/db/repository/template`).
  - `bootstrap.Deps.TemplateRepository port.TemplateRepository`.

- [ ] **Step 1: Write the migration (both engines)**

`internal/infra/db/migrations/sqlite/0013_templates.sql`:

```sql
-- Templates: an org's stack definitions, each a sequence of immutable
-- revisions (the JSON body). Instances: apps created from a revision,
-- addressed by the caller's key, unique in the org among instances that
-- are not unlinked. The rollout queue arrives in its own migration (D1b).

CREATE TABLE templates (
    id VARCHAR(36) PRIMARY KEY,
    org_id VARCHAR(36) NOT NULL,
    name VARCHAR(63) NOT NULL,
    latest_revision INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (org_id, name),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE TABLE template_revisions (
    template_id VARCHAR(36) NOT NULL,
    revision INTEGER NOT NULL,
    body TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    PRIMARY KEY (template_id, revision),
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE CASCADE
);

-- template_id is NULL once the template is gone (only deleted and unlinked
-- rows can outlive it: a template with live instances can't be deleted).
CREATE TABLE template_instances (
    id VARCHAR(36) PRIMARY KEY,
    org_id VARCHAR(36) NOT NULL,
    instance_key VARCHAR(128) NOT NULL,
    app_id VARCHAR(36) NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    template_id VARCHAR(36),
    revision INTEGER NOT NULL,
    params TEXT NOT NULL,
    secret_names TEXT NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    state VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE,
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE SET NULL
);

-- One live key per org: an unlinked instance frees its key.
CREATE UNIQUE INDEX idx_template_instances_key ON template_instances (org_id, instance_key) WHERE state <> 'unlinked';
CREATE INDEX idx_template_instances_template ON template_instances (template_id);
CREATE INDEX idx_template_instances_server ON template_instances (server_id);
```

`internal/infra/db/migrations/pgsql/0013_templates.sql` (the same file with `UUID` id columns):

```sql
-- Templates: an org's stack definitions, each a sequence of immutable
-- revisions (the JSON body). Instances: apps created from a revision,
-- addressed by the caller's key, unique in the org among instances that
-- are not unlinked. The rollout queue arrives in its own migration (D1b).

CREATE TABLE templates (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    name VARCHAR(63) NOT NULL,
    latest_revision INTEGER NOT NULL,
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    UNIQUE (org_id, name),
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE
);

CREATE TABLE template_revisions (
    template_id UUID NOT NULL,
    revision INTEGER NOT NULL,
    body TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    PRIMARY KEY (template_id, revision),
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE CASCADE
);

-- template_id is NULL once the template is gone (only deleted and unlinked
-- rows can outlive it: a template with live instances can't be deleted).
CREATE TABLE template_instances (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    instance_key VARCHAR(128) NOT NULL,
    app_id UUID NOT NULL,
    server_id UUID NOT NULL,
    template_id UUID,
    revision INTEGER NOT NULL,
    params TEXT NOT NULL,
    secret_names TEXT NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    state VARCHAR(16) NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    FOREIGN KEY (org_id) REFERENCES organizations (organization_id) ON DELETE CASCADE,
    FOREIGN KEY (server_id) REFERENCES servers (server_id) ON DELETE CASCADE,
    FOREIGN KEY (template_id) REFERENCES templates (id) ON DELETE SET NULL
);

-- One live key per org: an unlinked instance frees its key.
CREATE UNIQUE INDEX idx_template_instances_key ON template_instances (org_id, instance_key) WHERE state <> 'unlinked';
CREATE INDEX idx_template_instances_template ON template_instances (template_id);
CREATE INDEX idx_template_instances_server ON template_instances (server_id);
```

- [ ] **Step 2: Write the queries (both engines)**

`internal/infra/db/sqlc/query/sqlite/templates.sql`:

```sql
-- Templates, their immutable revisions and their instances.

-- name: InsertTemplate :execrows
-- A taken name inserts nothing (0 rows): the caller answers 400.
INSERT INTO templates (id, org_id, name, latest_revision, created_at, created_by, updated_at)
VALUES (?, ?, ?, 1, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: BumpTemplateRevision :execrows
-- Optimistic: 0 rows when latest_revision moved past the caller's base.
UPDATE templates
SET latest_revision = latest_revision + 1, name = ?, updated_at = ?
WHERE id = ? AND org_id = ? AND latest_revision = ?;

-- name: GetTemplate :one
SELECT * FROM templates WHERE id = ? AND org_id = ?;

-- name: GetTemplateIDByName :one
SELECT id FROM templates WHERE org_id = ? AND name = ?;

-- name: ListTemplates :many
SELECT t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created')) AS instances,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created') AND i.revision < t.latest_revision) AS behind
FROM templates AS t
WHERE t.org_id = ?
ORDER BY t.name ASC;

-- name: DeleteTemplate :execrows
-- Refused (0 rows) while an instance is pending or created.
DELETE FROM templates
WHERE templates.id = ? AND templates.org_id = ?
  AND NOT EXISTS (
    SELECT 1 FROM template_instances AS i
    WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));

-- name: InsertTemplateRevision :exec
INSERT INTO template_revisions (template_id, revision, body, note, created_at, created_by)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetTemplateRevision :one
SELECT * FROM template_revisions WHERE template_id = ? AND revision = ?;

-- name: ListTemplateRevisions :many
SELECT revision, note, created_at, created_by
FROM template_revisions
WHERE template_id = ?
ORDER BY revision DESC;

-- name: ClaimTemplateInstance :execrows
-- A live key (the partial unique index) inserts nothing (0 rows).
INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision,
    params, secret_names, request_hash, state, last_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', '', ?, ?)
ON CONFLICT DO NOTHING;

-- name: GetLiveTemplateInstance :one
SELECT * FROM template_instances WHERE org_id = ? AND instance_key = ? AND state <> 'unlinked';

-- name: ListLiveTemplateInstances :many
SELECT * FROM template_instances
WHERE org_id = ? AND state IN ('pending', 'created')
ORDER BY created_at ASC, id ASC;

-- name: MarkTemplateInstanceSaved :execrows
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
UPDATE template_instances
SET revision = ?, params = ?, secret_names = ?, state = 'created', last_error = '', updated_at = ?
WHERE id = ? AND state IN ('pending', 'created');

-- name: SetTemplateInstanceState :execrows
UPDATE template_instances
SET state = ?, last_error = '', updated_at = ?
WHERE id = ? AND state IN ('pending', 'created');

-- name: SetTemplateInstanceError :exec
UPDATE template_instances SET last_error = ?, updated_at = ? WHERE id = ?;
```

`internal/infra/db/sqlc/query/pgsql/templates.sql` (same names, `$n` placeholders):

```sql
-- Templates, their immutable revisions and their instances.

-- name: InsertTemplate :execrows
-- A taken name inserts nothing (0 rows): the caller answers 400.
INSERT INTO templates (id, org_id, name, latest_revision, created_at, created_by, updated_at)
VALUES ($1, $2, $3, 1, $4, $5, $6)
ON CONFLICT DO NOTHING;

-- name: BumpTemplateRevision :execrows
-- Optimistic: 0 rows when latest_revision moved past the caller's base.
UPDATE templates
SET latest_revision = latest_revision + 1, name = $1, updated_at = $2
WHERE id = $3 AND org_id = $4 AND latest_revision = $5;

-- name: GetTemplate :one
SELECT * FROM templates WHERE id = $1 AND org_id = $2;

-- name: GetTemplateIDByName :one
SELECT id FROM templates WHERE org_id = $1 AND name = $2;

-- name: ListTemplates :many
SELECT t.id, t.org_id, t.name, t.latest_revision, t.created_at, t.created_by, t.updated_at,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created')) AS instances,
    (SELECT COUNT(*) FROM template_instances AS i
     WHERE i.template_id = t.id AND i.state IN ('pending', 'created') AND i.revision < t.latest_revision) AS behind
FROM templates AS t
WHERE t.org_id = $1
ORDER BY t.name ASC;

-- name: DeleteTemplate :execrows
-- Refused (0 rows) while an instance is pending or created.
DELETE FROM templates
WHERE templates.id = $1 AND templates.org_id = $2
  AND NOT EXISTS (
    SELECT 1 FROM template_instances AS i
    WHERE i.template_id = templates.id AND i.state IN ('pending', 'created'));

-- name: InsertTemplateRevision :exec
INSERT INTO template_revisions (template_id, revision, body, note, created_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetTemplateRevision :one
SELECT * FROM template_revisions WHERE template_id = $1 AND revision = $2;

-- name: ListTemplateRevisions :many
SELECT revision, note, created_at, created_by
FROM template_revisions
WHERE template_id = $1
ORDER BY revision DESC;

-- name: ClaimTemplateInstance :execrows
-- A live key (the partial unique index) inserts nothing (0 rows).
INSERT INTO template_instances (id, org_id, instance_key, app_id, server_id, template_id, revision,
    params, secret_names, request_hash, state, last_error, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', '', $11, $12)
ON CONFLICT DO NOTHING;

-- name: GetLiveTemplateInstance :one
SELECT * FROM template_instances WHERE org_id = $1 AND instance_key = $2 AND state <> 'unlinked';

-- name: ListLiveTemplateInstances :many
SELECT * FROM template_instances
WHERE org_id = $1 AND state IN ('pending', 'created')
ORDER BY created_at ASC, id ASC;

-- name: MarkTemplateInstanceSaved :execrows
-- A save the agent confirmed; a deleted or unlinked row stays as it is.
UPDATE template_instances
SET revision = $1, params = $2, secret_names = $3, state = 'created', last_error = '', updated_at = $4
WHERE id = $5 AND state IN ('pending', 'created');

-- name: SetTemplateInstanceState :execrows
UPDATE template_instances
SET state = $1, last_error = '', updated_at = $2
WHERE id = $3 AND state IN ('pending', 'created');

-- name: SetTemplateInstanceError :exec
UPDATE template_instances SET last_error = $1, updated_at = $2 WHERE id = $3;
```

Notes: `ON CONFLICT DO NOTHING` without a target covers the partial unique index `idx_template_instances_key` on both engines (on Postgres a concurrent insert of the same key waits for the first transaction, then inserts nothing); `DeleteTemplate` qualifies `templates.id` because the sub-select also has an `id` column (sqlc reports `column reference "id" is ambiguous` otherwise).

- [ ] **Step 3: Generate**

Run: `make sqlc`
Expected: no output errors; new files `internal/infra/db/sqlc/gen/sqlite/templates.sql.go` and `internal/infra/db/sqlc/gen/pgsql/templates.sql.go`, and `models.go`/`querier.go` changed on both. `TemplateInstance.TemplateID` is `*string`, every `INTEGER` is `int64`, every `TIMESTAMP` is `types.DateTime` on both engines.

- [ ] **Step 4: Add the shape checks**

In `internal/infra/db/sqlc/shapes_test.go`, add these lines just before the closing `)` of the `var (` block:

```go
	_ = sqlitegen.Template(pgsqlgen.Template{})
	_ = pgsqlgen.Template(sqlitegen.Template{})
	_ = sqlitegen.TemplateRevision(pgsqlgen.TemplateRevision{})
	_ = pgsqlgen.TemplateRevision(sqlitegen.TemplateRevision{})
	_ = sqlitegen.TemplateInstance(pgsqlgen.TemplateInstance{})
	_ = pgsqlgen.TemplateInstance(sqlitegen.TemplateInstance{})
	_ = sqlitegen.InsertTemplateParams(pgsqlgen.InsertTemplateParams{})
	_ = pgsqlgen.InsertTemplateParams(sqlitegen.InsertTemplateParams{})
	_ = sqlitegen.BumpTemplateRevisionParams(pgsqlgen.BumpTemplateRevisionParams{})
	_ = pgsqlgen.BumpTemplateRevisionParams(sqlitegen.BumpTemplateRevisionParams{})
	_ = sqlitegen.GetTemplateParams(pgsqlgen.GetTemplateParams{})
	_ = pgsqlgen.GetTemplateParams(sqlitegen.GetTemplateParams{})
	_ = sqlitegen.GetTemplateIDByNameParams(pgsqlgen.GetTemplateIDByNameParams{})
	_ = pgsqlgen.GetTemplateIDByNameParams(sqlitegen.GetTemplateIDByNameParams{})
	_ = sqlitegen.ListTemplatesRow(pgsqlgen.ListTemplatesRow{})
	_ = pgsqlgen.ListTemplatesRow(sqlitegen.ListTemplatesRow{})
	_ = sqlitegen.DeleteTemplateParams(pgsqlgen.DeleteTemplateParams{})
	_ = pgsqlgen.DeleteTemplateParams(sqlitegen.DeleteTemplateParams{})
	_ = sqlitegen.InsertTemplateRevisionParams(pgsqlgen.InsertTemplateRevisionParams{})
	_ = pgsqlgen.InsertTemplateRevisionParams(sqlitegen.InsertTemplateRevisionParams{})
	_ = sqlitegen.GetTemplateRevisionParams(pgsqlgen.GetTemplateRevisionParams{})
	_ = pgsqlgen.GetTemplateRevisionParams(sqlitegen.GetTemplateRevisionParams{})
	_ = sqlitegen.ListTemplateRevisionsRow(pgsqlgen.ListTemplateRevisionsRow{})
	_ = pgsqlgen.ListTemplateRevisionsRow(sqlitegen.ListTemplateRevisionsRow{})
	_ = sqlitegen.ClaimTemplateInstanceParams(pgsqlgen.ClaimTemplateInstanceParams{})
	_ = pgsqlgen.ClaimTemplateInstanceParams(sqlitegen.ClaimTemplateInstanceParams{})
	_ = sqlitegen.GetLiveTemplateInstanceParams(pgsqlgen.GetLiveTemplateInstanceParams{})
	_ = pgsqlgen.GetLiveTemplateInstanceParams(sqlitegen.GetLiveTemplateInstanceParams{})
	_ = sqlitegen.MarkTemplateInstanceSavedParams(pgsqlgen.MarkTemplateInstanceSavedParams{})
	_ = pgsqlgen.MarkTemplateInstanceSavedParams(sqlitegen.MarkTemplateInstanceSavedParams{})
	_ = sqlitegen.SetTemplateInstanceStateParams(pgsqlgen.SetTemplateInstanceStateParams{})
	_ = pgsqlgen.SetTemplateInstanceStateParams(sqlitegen.SetTemplateInstanceStateParams{})
	_ = sqlitegen.SetTemplateInstanceErrorParams(pgsqlgen.SetTemplateInstanceErrorParams{})
	_ = pgsqlgen.SetTemplateInstanceErrorParams(sqlitegen.SetTemplateInstanceErrorParams{})
```

Run: `go test ./internal/infra/db/sqlc/`
Expected: `ok` (the two engines' types are identical).

- [ ] **Step 5: Write the model and the port**

`internal/domain/model/template.go`:

```go
package model

import (
	"errors"
	"time"
)

// ErrNotFound marks an unknown template, revision or instance key, or one
// of another org: the API answers 404. ErrConflict marks a request the
// current state refuses: the API answers 409 with the message.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// classified is a sentinel error that also matches its class.
type classified struct {
	msg   string
	class error
}

func (e *classified) Error() string        { return e.msg }
func (e *classified) Is(target error) bool { return target == e.class }

// Template errors. ErrTemplateNameTaken matches ErrRejected (400 naming
// the field).
var (
	ErrTemplateNotFound     = &classified{"template not found", ErrNotFound}
	ErrTemplateRevisionGone = &classified{"template revision not found", ErrNotFound}
	ErrInstanceNotFound     = &classified{"instance not found", ErrNotFound}
	ErrTemplateNameTaken    = rejected("name: a template with this name already exists")
	ErrRevisionConflict     = &classified{"someone saved the template meanwhile: reload it and save again", ErrConflict}
	ErrTemplateInUse        = &classified{"the template has live instances: delete or unlink them first", ErrConflict}
	ErrKeyInUse             = &classified{"key in use with different values: update the instance instead", ErrConflict}
	ErrKeyRetired           = &classified{"this key belonged to a deleted instance", ErrConflict}
	ErrInstanceNotCreated   = &classified{"the instance was never created: retry the create or delete it", ErrConflict}
)

// Template is an org's stack definition; its body lives in revisions.
type Template struct {
	ID             string
	OrgID          string
	Name           string
	LatestRevision int64
	CreatedAt      time.Time
	CreatedBy      string
	UpdatedAt      time.Time
}

// TemplateSummary is a template with its live instance counts.
type TemplateSummary struct {
	Template
	// Instances counts the pending and created instances; Behind those of
	// them on a revision older than the latest.
	Instances int64
	Behind    int64
}

// TemplateRevision is one immutable revision: Body is the JSON body.
type TemplateRevision struct {
	TemplateID string
	Revision   int64
	Body       []byte
	Note       string
	CreatedAt  time.Time
	CreatedBy  string
}

// InstanceState is where an instance is in its life.
type InstanceState string

const (
	// InstancePending: the key is claimed, the save not yet confirmed.
	InstancePending InstanceState = "pending"
	// InstanceCreated: the agent confirmed a save.
	InstanceCreated InstanceState = "created"
	// InstanceDeleted: the app was deleted; the key stays retired.
	InstanceDeleted InstanceState = "deleted"
	// InstanceUnlinked: the app became an ordinary app; the key is free.
	InstanceUnlinked InstanceState = "unlinked"
)

// Instance is an app created from a template revision.
type Instance struct {
	ID       string
	OrgID    string
	Key      string
	AppID    string
	ServerID string
	// TemplateID is "" once the template is gone.
	TemplateID string
	Revision   int64
	// Params are the plain parameter values; SecretNames the secret
	// parameters a value was given for (never a value).
	Params      map[string]string
	SecretNames []string
	RequestHash string
	State       InstanceState
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
```

`internal/domain/port/template.go`:

```go
package port

import (
	"context"

	"winterflow/internal/domain/model"
)

// TemplateRepository persists templates, their revisions and instances.
// Every template read is scoped to an org: another org's id is
// model.ErrTemplateNotFound.
type TemplateRepository interface {
	// CreateTemplate inserts the template and its revision 1 in one
	// transaction; model.ErrTemplateNameTaken when the org has the name.
	CreateTemplate(ctx context.Context, t model.Template, rev model.TemplateRevision) error
	// AddRevision adds revision base+1 (renaming the template to name) in
	// one transaction and returns it: model.ErrRevisionConflict when the
	// latest revision is not base, model.ErrTemplateNameTaken when another
	// template of the org has name, model.ErrTemplateNotFound when unknown.
	AddRevision(ctx context.Context, orgID, templateID, name string, base int64, rev model.TemplateRevision) (int64, error)
	GetTemplate(ctx context.Context, orgID, templateID string) (model.Template, error)
	ListTemplates(ctx context.Context, orgID string) ([]model.TemplateSummary, error)
	// GetRevision returns one revision; model.ErrTemplateRevisionGone when
	// the template has no such revision.
	GetRevision(ctx context.Context, templateID string, revision int64) (model.TemplateRevision, error)
	// ListRevisions returns the revisions newest first, without bodies.
	ListRevisions(ctx context.Context, templateID string) ([]model.TemplateRevision, error)
	// DeleteTemplate removes the template and its revisions:
	// model.ErrTemplateInUse while an instance is pending or created,
	// model.ErrTemplateNotFound when unknown.
	DeleteTemplate(ctx context.Context, orgID, templateID string) error

	// ClaimInstance inserts inst as pending unless the org already has a
	// live (not unlinked) instance with its key; it returns the stored
	// instance and whether this call inserted it.
	ClaimInstance(ctx context.Context, inst model.Instance) (model.Instance, bool, error)
	// GetInstance returns the org's live instance with key (pending,
	// created or deleted); model.ErrInstanceNotFound when none.
	GetInstance(ctx context.Context, orgID, key string) (model.Instance, error)
	// ListInstances returns the org's pending and created instances,
	// oldest first.
	ListInstances(ctx context.Context, orgID string) ([]model.Instance, error)
	// MarkSaved records a save the agent confirmed: revision, values and
	// state created, last error cleared. A deleted or unlinked instance is
	// left alone.
	MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string) error
	// SetState moves a pending or created instance to state (deleted,
	// unlinked), clearing its last error.
	SetState(ctx context.Context, id string, state model.InstanceState) error
	// SetError records the last failed command's error.
	SetError(ctx context.Context, id, msg string) error
}
```

- [ ] **Step 6: Write the failing repository tests**

`internal/infra/db/repository/template/repo_test.go`:

```go
package template

import (
	"errors"
	"sync"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/infra/db"
	"winterflow/internal/infra/db/dbtest"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
	tpl1, tpl2 = dbtest.ID(21), dbtest.ID(22)
)

func newRepo(t *testing.T) (*Repo, *db.DB) {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	r, err := New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}

func mustCreate(t *testing.T, r *Repo, orgID, id, name string) {
	t.Helper()
	err := r.CreateTemplate(dbtest.Ctx(t), model.Template{ID: id, OrgID: orgID, Name: name, CreatedBy: "u"},
		model.TemplateRevision{Body: []byte(`{"files":{}}`), Note: "first", CreatedBy: "u"})
	if err != nil {
		t.Fatal(err)
	}
}

func instance(n int, key string) model.Instance {
	return model.Instance{
		ID: dbtest.ID(100 + n), OrgID: org1, Key: key, AppID: dbtest.ID(200 + n), ServerID: srv1,
		TemplateID: tpl1, Revision: 1, Params: map[string]string{"NAME": "acme"},
		SecretNames: []string{"B", "A"}, RequestHash: "h1",
	}
}

func TestNewRejectsUnknownDriver(t *testing.T) {
	if _, err := New(nil, "mysql", dbtest.Log); err == nil {
		t.Fatal("unknown driver accepted")
	}
}

func TestTemplateNamesAreUniquePerOrg(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	err := r.CreateTemplate(ctx, model.Template{ID: tpl2, OrgID: org1, Name: "shop"}, model.TemplateRevision{Body: []byte(`{}`)})
	if !errors.Is(err, model.ErrTemplateNameTaken) || !errors.Is(err, model.ErrRejected) {
		t.Fatalf("same name in the org: %v", err)
	}
	// Another org may use the name.
	mustCreate(t, r, org2, tpl2, "shop")
	list, err := r.ListTemplates(ctx, org1)
	if err != nil || len(list) != 1 || list[0].Name != "shop" || list[0].LatestRevision != 1 {
		t.Fatalf("org1 list = %+v, %v", list, err)
	}
	if _, err := r.GetTemplate(ctx, org2, tpl1); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template: %v", err)
	}
}

func TestAddRevisionIsOptimistic(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	mustCreate(t, r, org1, tpl2, "blog")
	rev, err := r.AddRevision(ctx, org1, tpl1, "shop-2", 1, model.TemplateRevision{Body: []byte(`{"v":2}`), Note: "two"})
	if err != nil || rev != 2 {
		t.Fatalf("add r2 = %d, %v", rev, err)
	}
	if _, err := r.AddRevision(ctx, org1, tpl1, "shop-2", 1, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrRevisionConflict) {
		t.Fatalf("stale base: %v", err)
	}
	if _, err := r.AddRevision(ctx, org1, tpl1, "blog", 2, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrTemplateNameTaken) {
		t.Fatalf("rename onto another template: %v", err)
	}
	if _, err := r.AddRevision(ctx, org2, tpl1, "x", 2, model.TemplateRevision{Body: []byte(`{}`)}); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org: %v", err)
	}
	got, err := r.GetRevision(ctx, tpl1, 2)
	if err != nil || string(got.Body) != `{"v":2}` || got.Note != "two" {
		t.Fatalf("r2 = %+v, %v", got, err)
	}
	if _, err := r.GetRevision(ctx, tpl1, 3); !errors.Is(err, model.ErrTemplateRevisionGone) {
		t.Fatalf("missing revision: %v", err)
	}
	revs, err := r.ListRevisions(ctx, tpl1)
	if err != nil || len(revs) != 2 || revs[0].Revision != 2 || revs[1].Note != "first" || revs[0].Body != nil {
		t.Fatalf("revisions = %+v, %v", revs, err)
	}
	tpl, err := r.GetTemplate(ctx, org1, tpl1)
	if err != nil || tpl.Name != "shop-2" || tpl.LatestRevision != 2 {
		t.Fatalf("template = %+v, %v", tpl, err)
	}
}

func TestClaimInstanceOncePerLiveKey(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	got, inserted, err := r.ClaimInstance(ctx, instance(1, "k1"))
	if err != nil || !inserted || got.State != model.InstancePending || got.Params["NAME"] != "acme" ||
		len(got.SecretNames) != 2 || got.SecretNames[0] != "A" {
		t.Fatalf("first claim = %+v, %v, %v", got, inserted, err)
	}
	again, inserted, err := r.ClaimInstance(ctx, instance(2, "k1"))
	if err != nil || inserted || again.ID != got.ID || again.AppID != got.AppID {
		t.Fatalf("second claim = %+v, %v, %v", again, inserted, err)
	}
	// Another org's key with the same value is its own.
	other := instance(3, "k1")
	other.OrgID, other.ServerID, other.TemplateID = org2, srv2, ""
	if _, inserted, err := r.ClaimInstance(ctx, other); err != nil || !inserted {
		t.Fatalf("other org = %v, %v", inserted, err)
	}
}

// Two creates racing on one key make one instance (and so one app id).
func TestClaimInstanceConcurrent(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	var wg sync.WaitGroup
	apps := make([]string, 8)
	errs := make([]error, 8)
	for i := range apps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, _, err := r.ClaimInstance(ctx, instance(10+i, "race"))
			apps[i], errs[i] = got.AppID, err
		}(i)
	}
	wg.Wait()
	for i := range apps {
		if errs[i] != nil {
			t.Fatalf("claim %d: %v", i, errs[i])
		}
		if apps[i] != apps[0] {
			t.Fatalf("claims returned different apps: %v", apps)
		}
	}
}

func TestUnlinkFreesTheKeyAndDeleteRetiresIt(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	first, _, _ := r.ClaimInstance(ctx, instance(1, "k"))
	if err := r.MarkSaved(ctx, first.ID, 2, map[string]string{"NAME": "b"}, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := r.GetInstance(ctx, org1, "k")
	if err != nil || saved.State != model.InstanceCreated || saved.Revision != 2 || saved.Params["NAME"] != "b" || len(saved.SecretNames) != 0 {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	if err := r.SetState(ctx, first.ID, model.InstanceUnlinked); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetInstance(ctx, org1, "k"); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("unlinked key: %v", err)
	}
	second, inserted, err := r.ClaimInstance(ctx, instance(2, "k"))
	if err != nil || !inserted || second.ID == first.ID {
		t.Fatalf("reclaim after unlink = %+v, %v, %v", second, inserted, err)
	}
	if err := r.SetState(ctx, second.ID, model.InstanceDeleted); err != nil {
		t.Fatal(err)
	}
	if _, inserted, _ := r.ClaimInstance(ctx, instance(3, "k")); inserted {
		t.Fatal("a deleted instance's key was claimed again")
	}
	// A late save confirmation does not revive a deleted instance.
	if err := r.MarkSaved(ctx, second.ID, 3, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetInstance(ctx, org1, "k"); got.State != model.InstanceDeleted {
		t.Fatalf("after a late save: %+v", got)
	}
	if err := r.SetError(ctx, second.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.GetInstance(ctx, org1, "k"); got.LastError != "boom" {
		t.Fatalf("last error = %q", got.LastError)
	}
}

func TestDeleteTemplateRefusedWhileLive(t *testing.T) {
	r, d := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	live, _, _ := r.ClaimInstance(ctx, instance(1, "live"))
	gone, _, _ := r.ClaimInstance(ctx, instance(2, "gone"))
	if err := r.SetState(ctx, gone.ID, model.InstanceDeleted); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListTemplates(ctx, org1)
	if len(list) != 1 || list[0].Instances != 1 || list[0].Behind != 0 {
		t.Fatalf("counts = %+v", list)
	}
	if err := r.DeleteTemplate(ctx, org1, tpl1); !errors.Is(err, model.ErrTemplateInUse) {
		t.Fatalf("delete with a live instance: %v", err)
	}
	if err := r.DeleteTemplate(ctx, org2, tpl1); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("delete from another org: %v", err)
	}
	if err := r.SetState(ctx, live.ID, model.InstanceUnlinked); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteTemplate(ctx, org1, tpl1); err != nil {
		t.Fatal(err)
	}
	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM template_revisions WHERE template_id = ?`, tpl1); n != "0" {
		t.Fatalf("revisions left: %s", n)
	}
	// The retired rows outlive the template with template_id NULL.
	if got, err := r.GetInstance(ctx, org1, "gone"); err != nil || got.TemplateID != "" {
		t.Fatalf("retired row = %+v, %v", got, err)
	}
	if n := dbtest.QueryString(t, d, `SELECT COUNT(*) FROM template_instances WHERE template_id IS NULL`); n != "2" {
		t.Fatalf("rows with template_id NULL: %s", n)
	}
}

func TestListInstancesAndBehind(t *testing.T) {
	r, _ := newRepo(t)
	ctx := dbtest.Ctx(t)
	mustCreate(t, r, org1, tpl1, "shop")
	a, _, _ := r.ClaimInstance(ctx, instance(1, "a"))
	_, _, _ = r.ClaimInstance(ctx, instance(2, "b"))
	if _, err := r.AddRevision(ctx, org1, tpl1, "shop", 1, model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkSaved(ctx, a.ID, 2, nil, nil); err != nil {
		t.Fatal(err)
	}
	list, _ := r.ListTemplates(ctx, org1)
	if list[0].Instances != 2 || list[0].Behind != 1 {
		t.Fatalf("counts = %+v", list[0])
	}
	got, err := r.ListInstances(ctx, org1)
	if err != nil || len(got) != 2 || got[0].Key != "a" || got[1].Key != "b" {
		t.Fatalf("list = %+v, %v", got, err)
	}
	if other, _ := r.ListInstances(ctx, org2); len(other) != 0 {
		t.Fatalf("org2 sees %+v", other)
	}
}
```

Run: `go test ./internal/infra/db/repository/template/`
Expected: FAIL to compile (`undefined: New`, `undefined: Repo`).

- [ ] **Step 7: Write the repository**

`internal/infra/db/repository/template/repo.go`:

```go
// Package template implements port.TemplateRepository over the templates,
// template_revisions and template_instances tables.
package template

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

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
	templateRow    = sqlitegen.Template
	summaryRow     = sqlitegen.ListTemplatesRow
	revisionRow    = sqlitegen.TemplateRevision
	revisionInfo   = sqlitegen.ListTemplateRevisionsRow
	instanceRow    = sqlitegen.TemplateInstance
	insertParams   = sqlitegen.InsertTemplateParams
	bumpParams     = sqlitegen.BumpTemplateRevisionParams
	getParams      = sqlitegen.GetTemplateParams
	byNameParams   = sqlitegen.GetTemplateIDByNameParams
	deleteParams   = sqlitegen.DeleteTemplateParams
	insertRevision = sqlitegen.InsertTemplateRevisionParams
	getRevision    = sqlitegen.GetTemplateRevisionParams
	claimParams    = sqlitegen.ClaimTemplateInstanceParams
	liveParams     = sqlitegen.GetLiveTemplateInstanceParams
	savedParams    = sqlitegen.MarkTemplateInstanceSavedParams
	stateParams    = sqlitegen.SetTemplateInstanceStateParams
	errorParams    = sqlitegen.SetTemplateInstanceErrorParams
)

type querier interface {
	InsertTemplate(ctx context.Context, db backend.DBTX, p insertParams) (int64, error)
	BumpTemplateRevision(ctx context.Context, db backend.DBTX, p bumpParams) (int64, error)
	GetTemplate(ctx context.Context, db backend.DBTX, p getParams) (templateRow, error)
	GetTemplateIDByName(ctx context.Context, db backend.DBTX, p byNameParams) (string, error)
	ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error)
	DeleteTemplate(ctx context.Context, db backend.DBTX, p deleteParams) (int64, error)
	InsertTemplateRevision(ctx context.Context, db backend.DBTX, p insertRevision) error
	GetTemplateRevision(ctx context.Context, db backend.DBTX, p getRevision) (revisionRow, error)
	ListTemplateRevisions(ctx context.Context, db backend.DBTX, templateID string) ([]revisionInfo, error)
	ClaimTemplateInstance(ctx context.Context, db backend.DBTX, p claimParams) (int64, error)
	GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error)
	ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error)
	MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) (int64, error)
	SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) (int64, error)
	SetTemplateInstanceError(ctx context.Context, db backend.DBTX, p errorParams) error
}

// Repo is the templates repository.
type Repo struct {
	tx  *backend.TxManager
	q   querier
	log *logger.Logger
}

var _ port.TemplateRepository = (*Repo)(nil)

// New builds the repository for driver (backend.SQLite or backend.Postgres).
func New(tx *backend.TxManager, driver string, log *logger.Logger) (*Repo, error) {
	switch driver {
	case backend.SQLite:
		return &Repo{tx: tx, q: sqliteQuerier{}, log: log}, nil
	case backend.Postgres:
		return &Repo{tx: tx, q: pgsqlQuerier{}, log: log}, nil
	}
	return nil, fmt.Errorf("template repository: unknown database driver %q", driver)
}

// CreateTemplate inserts the template (latest_revision 1) and revision 1.
func (r *Repo) CreateTemplate(ctx context.Context, t model.Template, rev model.TemplateRevision) error {
	now := types.NewDateTime()
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.InsertTemplate(ctx, r.tx.Querier(ctx), insertParams{
			ID: t.ID, OrgID: t.OrgID, Name: t.Name, CreatedAt: now, CreatedBy: t.CreatedBy, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return model.ErrTemplateNameTaken
		}
		rev.TemplateID, rev.Revision = t.ID, 1
		return r.insertRevision(ctx, rev, now)
	})
}

// AddRevision bumps latest_revision from base (renaming the template) and
// inserts the new revision, in one transaction.
func (r *Repo) AddRevision(ctx context.Context, orgID, templateID, name string, base int64, rev model.TemplateRevision) (int64, error) {
	now := types.NewDateTime()
	next := base + 1
	err := r.tx.WithTx(ctx, func(ctx context.Context) error {
		cur, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrTemplateNotFound
		}
		if err != nil {
			return err
		}
		if cur.Name != name {
			switch other, err := r.q.GetTemplateIDByName(ctx, r.tx.Querier(ctx), byNameParams{OrgID: orgID, Name: name}); {
			case err == nil && other != templateID:
				return model.ErrTemplateNameTaken
			case err != nil && !errors.Is(err, sql.ErrNoRows):
				return err
			}
		}
		n, err := r.q.BumpTemplateRevision(ctx, r.tx.Querier(ctx), bumpParams{
			Name: name, UpdatedAt: now, ID: templateID, OrgID: orgID, LatestRevision: base,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return model.ErrRevisionConflict
		}
		rev.TemplateID, rev.Revision = templateID, next
		return r.insertRevision(ctx, rev, now)
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

func (r *Repo) insertRevision(ctx context.Context, rev model.TemplateRevision, now types.DateTime) error {
	return r.q.InsertTemplateRevision(ctx, r.tx.Querier(ctx), insertRevision{
		TemplateID: rev.TemplateID, Revision: rev.Revision, Body: string(rev.Body), Note: rev.Note,
		CreatedAt: now, CreatedBy: rev.CreatedBy,
	})
}

// GetTemplate returns the org's template.
func (r *Repo) GetTemplate(ctx context.Context, orgID, templateID string) (model.Template, error) {
	row, err := r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
	if errors.Is(err, sql.ErrNoRows) {
		return model.Template{}, model.ErrTemplateNotFound
	}
	if err != nil {
		return model.Template{}, err
	}
	return hydrateTemplate(row), nil
}

// ListTemplates returns the org's templates by name with instance counts.
func (r *Repo) ListTemplates(ctx context.Context, orgID string) ([]model.TemplateSummary, error) {
	rows, err := r.q.ListTemplates(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]model.TemplateSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.TemplateSummary{
			Template: hydrateTemplate(templateRow{
				ID: row.ID, OrgID: row.OrgID, Name: row.Name, LatestRevision: row.LatestRevision,
				CreatedAt: row.CreatedAt, CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt,
			}),
			Instances: row.Instances,
			Behind:    row.Behind,
		})
	}
	return out, nil
}

// GetRevision returns one revision with its body.
func (r *Repo) GetRevision(ctx context.Context, templateID string, revision int64) (model.TemplateRevision, error) {
	row, err := r.q.GetTemplateRevision(ctx, r.tx.Querier(ctx), getRevision{TemplateID: templateID, Revision: revision})
	if errors.Is(err, sql.ErrNoRows) {
		return model.TemplateRevision{}, model.ErrTemplateRevisionGone
	}
	if err != nil {
		return model.TemplateRevision{}, err
	}
	return model.TemplateRevision{
		TemplateID: row.TemplateID, Revision: row.Revision, Body: []byte(row.Body), Note: row.Note,
		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
	}, nil
}

// ListRevisions returns the revisions newest first, without bodies.
func (r *Repo) ListRevisions(ctx context.Context, templateID string) ([]model.TemplateRevision, error) {
	rows, err := r.q.ListTemplateRevisions(ctx, r.tx.Querier(ctx), templateID)
	if err != nil {
		return nil, err
	}
	out := make([]model.TemplateRevision, 0, len(rows))
	for _, row := range rows {
		out = append(out, model.TemplateRevision{
			TemplateID: templateID, Revision: row.Revision, Note: row.Note,
			CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy,
		})
	}
	return out, nil
}

// DeleteTemplate deletes the template unless an instance is live: one
// guarded statement, then a read to tell "in use" from "unknown".
func (r *Repo) DeleteTemplate(ctx context.Context, orgID, templateID string) error {
	return r.tx.WithTx(ctx, func(ctx context.Context) error {
		n, err := r.q.DeleteTemplate(ctx, r.tx.Querier(ctx), deleteParams{ID: templateID, OrgID: orgID})
		if err != nil || n > 0 {
			return err
		}
		_, err = r.q.GetTemplate(ctx, r.tx.Querier(ctx), getParams{ID: templateID, OrgID: orgID})
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrTemplateNotFound
		}
		if err != nil {
			return err
		}
		return model.ErrTemplateInUse
	})
}

// ClaimInstance inserts inst as pending, or returns the live instance that
// holds its key (the partial unique index makes the insert a no-op).
func (r *Repo) ClaimInstance(ctx context.Context, inst model.Instance) (model.Instance, bool, error) {
	params, names, err := encodeValues(inst.Params, inst.SecretNames)
	if err != nil {
		return model.Instance{}, false, err
	}
	now := types.NewDateTime()
	var (
		out      model.Instance
		inserted bool
	)
	err = r.tx.WithTx(ctx, func(ctx context.Context) error {
		p := claimParams{
			ID: inst.ID, OrgID: inst.OrgID, InstanceKey: inst.Key, AppID: inst.AppID, ServerID: inst.ServerID,
			Revision: inst.Revision, Params: params, SecretNames: names, RequestHash: inst.RequestHash,
			CreatedAt: now, UpdatedAt: now,
		}
		if inst.TemplateID != "" {
			id := inst.TemplateID
			p.TemplateID = &id
		}
		n, err := r.q.ClaimTemplateInstance(ctx, r.tx.Querier(ctx), p)
		if err != nil {
			return err
		}
		inserted = n > 0
		row, err := r.q.GetLiveTemplateInstance(ctx, r.tx.Querier(ctx), liveParams{OrgID: inst.OrgID, InstanceKey: inst.Key})
		if err != nil {
			return err
		}
		out, err = hydrateInstance(row)
		return err
	})
	return out, inserted, err
}

// GetInstance returns the org's live instance with key.
func (r *Repo) GetInstance(ctx context.Context, orgID, key string) (model.Instance, error) {
	row, err := r.q.GetLiveTemplateInstance(ctx, r.tx.Querier(ctx), liveParams{OrgID: orgID, InstanceKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return model.Instance{}, model.ErrInstanceNotFound
	}
	if err != nil {
		return model.Instance{}, err
	}
	return hydrateInstance(row)
}

// ListInstances returns the org's pending and created instances.
func (r *Repo) ListInstances(ctx context.Context, orgID string) ([]model.Instance, error) {
	rows, err := r.q.ListLiveTemplateInstances(ctx, r.tx.Querier(ctx), orgID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Instance, 0, len(rows))
	for _, row := range rows {
		inst, err := hydrateInstance(row)
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

// MarkSaved records a confirmed save.
func (r *Repo) MarkSaved(ctx context.Context, id string, revision int64, params map[string]string, secretNames []string) error {
	p, names, err := encodeValues(params, secretNames)
	if err != nil {
		return err
	}
	_, err = r.q.MarkTemplateInstanceSaved(ctx, r.tx.Querier(ctx), savedParams{
		Revision: revision, Params: p, SecretNames: names, UpdatedAt: types.NewDateTime(), ID: id,
	})
	return err
}

// SetState moves a live instance to state.
func (r *Repo) SetState(ctx context.Context, id string, state model.InstanceState) error {
	_, err := r.q.SetTemplateInstanceState(ctx, r.tx.Querier(ctx), stateParams{
		State: string(state), UpdatedAt: types.NewDateTime(), ID: id,
	})
	return err
}

// SetError records the last failed command's error.
func (r *Repo) SetError(ctx context.Context, id, msg string) error {
	return r.q.SetTemplateInstanceError(ctx, r.tx.Querier(ctx), errorParams{
		LastError: msg, UpdatedAt: types.NewDateTime(), ID: id,
	})
}

// encodeValues serializes the plain values (an object; nil is {}) and the
// secret names (sorted; nil is []).
func encodeValues(params map[string]string, secretNames []string) (string, string, error) {
	if params == nil {
		params = map[string]string{}
	}
	p, err := json.Marshal(params)
	if err != nil {
		return "", "", err
	}
	names := append([]string{}, secretNames...)
	sort.Strings(names)
	n, err := json.Marshal(names)
	if err != nil {
		return "", "", err
	}
	return string(p), string(n), nil
}

func hydrateTemplate(row templateRow) model.Template {
	return model.Template{
		ID: row.ID, OrgID: row.OrgID, Name: row.Name, LatestRevision: row.LatestRevision,
		CreatedAt: row.CreatedAt.Time(), CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt.Time(),
	}
}

func hydrateInstance(row instanceRow) (model.Instance, error) {
	inst := model.Instance{
		ID: row.ID, OrgID: row.OrgID, Key: row.InstanceKey, AppID: row.AppID, ServerID: row.ServerID,
		Revision: row.Revision, RequestHash: row.RequestHash, State: model.InstanceState(row.State),
		LastError: row.LastError, CreatedAt: row.CreatedAt.Time(), UpdatedAt: row.UpdatedAt.Time(),
	}
	if row.TemplateID != nil {
		inst.TemplateID = *row.TemplateID
	}
	if err := json.Unmarshal([]byte(row.Params), &inst.Params); err != nil {
		return model.Instance{}, fmt.Errorf("instance %s: params: %w", row.ID, err)
	}
	if err := json.Unmarshal([]byte(row.SecretNames), &inst.SecretNames); err != nil {
		return model.Instance{}, fmt.Errorf("instance %s: secret_names: %w", row.ID, err)
	}
	return inst, nil
}
```

`internal/infra/db/repository/template/sqlite.go`:

```go
package template

import (
	"context"

	"winterflow/internal/infra/db/backend"
	sqlitegen "winterflow/internal/infra/db/sqlc/gen/sqlite"
)

type sqliteQuerier struct{}

var _ querier = sqliteQuerier{}

func (sqliteQuerier) InsertTemplate(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return sqlitegen.New(db).InsertTemplate(ctx, p)
}

func (sqliteQuerier) BumpTemplateRevision(ctx context.Context, db backend.DBTX, p bumpParams) (int64, error) {
	return sqlitegen.New(db).BumpTemplateRevision(ctx, p)
}

func (sqliteQuerier) GetTemplate(ctx context.Context, db backend.DBTX, p getParams) (templateRow, error) {
	return sqlitegen.New(db).GetTemplate(ctx, p)
}

func (sqliteQuerier) GetTemplateIDByName(ctx context.Context, db backend.DBTX, p byNameParams) (string, error) {
	return sqlitegen.New(db).GetTemplateIDByName(ctx, p)
}

func (sqliteQuerier) ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error) {
	return sqlitegen.New(db).ListTemplates(ctx, orgID)
}

func (sqliteQuerier) DeleteTemplate(ctx context.Context, db backend.DBTX, p deleteParams) (int64, error) {
	return sqlitegen.New(db).DeleteTemplate(ctx, p)
}

func (sqliteQuerier) InsertTemplateRevision(ctx context.Context, db backend.DBTX, p insertRevision) error {
	return sqlitegen.New(db).InsertTemplateRevision(ctx, p)
}

func (sqliteQuerier) GetTemplateRevision(ctx context.Context, db backend.DBTX, p getRevision) (revisionRow, error) {
	return sqlitegen.New(db).GetTemplateRevision(ctx, p)
}

func (sqliteQuerier) ListTemplateRevisions(ctx context.Context, db backend.DBTX, templateID string) ([]revisionInfo, error) {
	return sqlitegen.New(db).ListTemplateRevisions(ctx, templateID)
}

func (sqliteQuerier) ClaimTemplateInstance(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return sqlitegen.New(db).ClaimTemplateInstance(ctx, p)
}

func (sqliteQuerier) GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error) {
	return sqlitegen.New(db).GetLiveTemplateInstance(ctx, p)
}

func (sqliteQuerier) ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	return sqlitegen.New(db).ListLiveTemplateInstances(ctx, orgID)
}

func (sqliteQuerier) MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) (int64, error) {
	return sqlitegen.New(db).MarkTemplateInstanceSaved(ctx, p)
}

func (sqliteQuerier) SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) (int64, error) {
	return sqlitegen.New(db).SetTemplateInstanceState(ctx, p)
}

func (sqliteQuerier) SetTemplateInstanceError(ctx context.Context, db backend.DBTX, p errorParams) error {
	return sqlitegen.New(db).SetTemplateInstanceError(ctx, p)
}
```

`internal/infra/db/repository/template/pgsql.go`:

```go
package template

import (
	"context"

	"winterflow/internal/infra/db/backend"
	pgsqlgen "winterflow/internal/infra/db/sqlc/gen/pgsql"
)

type pgsqlQuerier struct{}

var _ querier = pgsqlQuerier{}

func (pgsqlQuerier) InsertTemplate(ctx context.Context, db backend.DBTX, p insertParams) (int64, error) {
	return pgsqlgen.New(db).InsertTemplate(ctx, pgsqlgen.InsertTemplateParams(p))
}

func (pgsqlQuerier) BumpTemplateRevision(ctx context.Context, db backend.DBTX, p bumpParams) (int64, error) {
	return pgsqlgen.New(db).BumpTemplateRevision(ctx, pgsqlgen.BumpTemplateRevisionParams(p))
}

func (pgsqlQuerier) GetTemplate(ctx context.Context, db backend.DBTX, p getParams) (templateRow, error) {
	row, err := pgsqlgen.New(db).GetTemplate(ctx, pgsqlgen.GetTemplateParams(p))
	return templateRow(row), err
}

func (pgsqlQuerier) GetTemplateIDByName(ctx context.Context, db backend.DBTX, p byNameParams) (string, error) {
	return pgsqlgen.New(db).GetTemplateIDByName(ctx, pgsqlgen.GetTemplateIDByNameParams(p))
}

func (pgsqlQuerier) ListTemplates(ctx context.Context, db backend.DBTX, orgID string) ([]summaryRow, error) {
	rows, err := pgsqlgen.New(db).ListTemplates(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]summaryRow, len(rows))
	for i, row := range rows {
		out[i] = summaryRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) DeleteTemplate(ctx context.Context, db backend.DBTX, p deleteParams) (int64, error) {
	return pgsqlgen.New(db).DeleteTemplate(ctx, pgsqlgen.DeleteTemplateParams(p))
}

func (pgsqlQuerier) InsertTemplateRevision(ctx context.Context, db backend.DBTX, p insertRevision) error {
	return pgsqlgen.New(db).InsertTemplateRevision(ctx, pgsqlgen.InsertTemplateRevisionParams(p))
}

func (pgsqlQuerier) GetTemplateRevision(ctx context.Context, db backend.DBTX, p getRevision) (revisionRow, error) {
	row, err := pgsqlgen.New(db).GetTemplateRevision(ctx, pgsqlgen.GetTemplateRevisionParams(p))
	return revisionRow(row), err
}

func (pgsqlQuerier) ListTemplateRevisions(ctx context.Context, db backend.DBTX, templateID string) ([]revisionInfo, error) {
	rows, err := pgsqlgen.New(db).ListTemplateRevisions(ctx, templateID)
	if err != nil {
		return nil, err
	}
	out := make([]revisionInfo, len(rows))
	for i, row := range rows {
		out[i] = revisionInfo(row)
	}
	return out, nil
}

func (pgsqlQuerier) ClaimTemplateInstance(ctx context.Context, db backend.DBTX, p claimParams) (int64, error) {
	return pgsqlgen.New(db).ClaimTemplateInstance(ctx, pgsqlgen.ClaimTemplateInstanceParams(p))
}

func (pgsqlQuerier) GetLiveTemplateInstance(ctx context.Context, db backend.DBTX, p liveParams) (instanceRow, error) {
	row, err := pgsqlgen.New(db).GetLiveTemplateInstance(ctx, pgsqlgen.GetLiveTemplateInstanceParams(p))
	return instanceRow(row), err
}

func (pgsqlQuerier) ListLiveTemplateInstances(ctx context.Context, db backend.DBTX, orgID string) ([]instanceRow, error) {
	rows, err := pgsqlgen.New(db).ListLiveTemplateInstances(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]instanceRow, len(rows))
	for i, row := range rows {
		out[i] = instanceRow(row)
	}
	return out, nil
}

func (pgsqlQuerier) MarkTemplateInstanceSaved(ctx context.Context, db backend.DBTX, p savedParams) (int64, error) {
	return pgsqlgen.New(db).MarkTemplateInstanceSaved(ctx, pgsqlgen.MarkTemplateInstanceSavedParams(p))
}

func (pgsqlQuerier) SetTemplateInstanceState(ctx context.Context, db backend.DBTX, p stateParams) (int64, error) {
	return pgsqlgen.New(db).SetTemplateInstanceState(ctx, pgsqlgen.SetTemplateInstanceStateParams(p))
}

func (pgsqlQuerier) SetTemplateInstanceError(ctx context.Context, db backend.DBTX, p errorParams) error {
	return pgsqlgen.New(db).SetTemplateInstanceError(ctx, pgsqlgen.SetTemplateInstanceErrorParams(p))
}
```

- [ ] **Step 8: Run the repository tests on SQLite**

Run: `go test -race -count=1 ./internal/infra/db/repository/template/`
Expected: `ok` (`TestClaimInstanceConcurrent` races eight claims of one key and gets one app id).

- [ ] **Step 9: Cover the new timestamp columns**

`TestSQLiteTimestampsAreDateTimeText` fails until every TIMESTAMP column is written (`go test ./internal/infra/db/repository/` reports `templates.created_at: never written by this test` and four more). In `internal/infra/db/repository/timestamps_test.go`:

Add the import (after the `sharedvarrepo` line):

```go
	templaterepo "winterflow/internal/infra/db/repository/template"
```

After `sharedVars := must(sharedvarrepo.New(d.Tx, d.Driver, dbtest.Log))` add:

```go
	templates := must(templaterepo.New(d.Tx, d.Driver, dbtest.Log))
```

Replace `	columns := timestampColumns(t, d)` with:

```go
	tplID := dbtest.ID(501)
	if err := templates.CreateTemplate(ctx, model.Template{ID: tplID, OrgID: orgID, Name: "shop"},
		model.TemplateRevision{Body: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := templates.ClaimInstance(ctx, model.Instance{
		ID: dbtest.ID(502), OrgID: orgID, Key: "k", AppID: appID, ServerID: srv.ID, TemplateID: tplID, Revision: 1, RequestHash: "h",
	}); err != nil {
		t.Fatal(err)
	}

	columns := timestampColumns(t, d)
```

Run: `go test -count=1 ./internal/infra/db/repository/`
Expected: `ok`.

- [ ] **Step 10: Wire the repository into Deps**

In `internal/infra/bootstrap/deps.go`, after the `SharedVariableRepository` field add:

```go
	// TemplateRepository stores templates, their revisions and instances.
	TemplateRepository port.TemplateRepository
```

In `internal/infra/bootstrap/core.go`:
- import `templaterepo "winterflow/internal/infra/db/repository/template"` (after the `sharedvarrepo` import);
- in the returned `&Deps{...}` add `TemplateRepository:       r.template,` after `SharedVariableRepository: r.sharedVar,`;
- in `type repos struct` add `template      *templaterepo.Repo` after `sharedVar     *sharedvarrepo.Repo`;
- replace the end of `newRepos`:

```go
	r.sharedVar, err = sharedvarrepo.New(conn.Tx, conn.Driver, log)
	return r, err
```

with:

```go
	if r.sharedVar, err = sharedvarrepo.New(conn.Tx, conn.Driver, log); err != nil {
		return r, err
	}
	r.template, err = templaterepo.New(conn.Tx, conn.Driver, log)
	return r, err
```

Run: `go build ./... && go test -count=1 ./internal/infra/bootstrap/`
Expected: builds; `ok`.

- [ ] **Step 11: Run the database packages on Postgres**

Run: `make test-pg`
Expected: `ok` for `internal/app/web/contract` (unchanged so far) and every `internal/infra/db/...` package, including `repository/template` (its concurrent-claim test exercises Postgres's `ON CONFLICT DO NOTHING` wait on the partial unique index). It needs Docker; without it, rely on CI's `contract` job.

- [ ] **Step 12: Check the generated code is current, then commit**

Run: `make sqlc-check && go vet ./internal/...`
Expected: no diff reported; vet clean.

```bash
git add internal/infra/db/migrations/sqlite/0013_templates.sql internal/infra/db/migrations/pgsql/0013_templates.sql \
  internal/infra/db/sqlc/query/sqlite/templates.sql internal/infra/db/sqlc/query/pgsql/templates.sql \
  internal/infra/db/sqlc/gen internal/infra/db/sqlc/shapes_test.go \
  internal/domain/model/template.go internal/domain/port/template.go \
  internal/infra/db/repository/template internal/infra/db/repository/timestamps_test.go \
  internal/infra/bootstrap/deps.go internal/infra/bootstrap/core.go
git commit -m "feat(db): templates, revisions and instances tables and repository

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Reference scanner in domain, template body and validation

**Files:**
- Move: `internal/infra/vars/refs.go` → `internal/domain/varref/refs.go` (rewritten below)
- Create: `internal/infra/vars/refs.go` (thin wrapper), `internal/domain/varref/refs_test.go`
- Create: `internal/domain/template/body.go`, `internal/domain/template/validate.go`, `internal/domain/template/ingress.go`, `internal/domain/template/validate_test.go`

**Interfaces:**
- Consumes: `recipe.ParseCompose`, `recipe.Validate`, `recipe.Effective`, `recipe.BackupConfig`, `recipe.Keep`, `recipe.ErrInvalid`; `model.Ingress`, `model.DetectComposeFiles`, `model.ComposeFileNames`, `model.Rejected`.
- Produces:
  - `varref.References(data []byte, into map[string]bool)`, `varref.Undefaulted(data []byte, into map[string]bool)`; `vars.References` keeps its signature (delegates).
  - `template.MaxBodyBytes = 1 << 20`, `template.ErrInvalid` (matches `model.ErrRejected`).
  - `template.Body{Files map[string]string; Parameters []Parameter; AppName, Icon, Color string; Ingress []IngressRow; Backups map[string]BackupDefault}`, `template.Parameter{Name string; Secret, Required bool; Default *string; Pattern, Description string}`, `template.IngressRow{Host, Service string; Port Port}`, `template.BackupDefault{Enabled bool; Storage, Every string; Keep *recipe.Keep}`, `template.Port` (string; JSON number or string).
  - `template.Parse(raw []byte) (Body, error)`, `template.Encode(b Body) ([]byte, error)`, `template.Validate(b Body) error`, `template.ValidateName(name string) error`, `template.Warnings(b Body, shared func(name string) bool) []string`, `(Body) RecipeNames() (map[string]bool, error)`.
  - Unexported, used by Task 3: `invalid`, `(Body).param`, `refRE`, `expand`, `renderIngress`, `(BackupDefault).config`, `sortedKeys`.

- [ ] **Step 1: Move the scanner and add `Undefaulted` (test first)**

```bash
mkdir -p internal/domain/varref
git mv internal/infra/vars/refs.go internal/domain/varref/refs.go
```

`internal/domain/varref/refs_test.go`:

```go
package varref

import (
	"reflect"
	"sort"
	"testing"
)

func names(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestUndefaultedSkipsNamesWithAFallback(t *testing.T) {
	src := `A: $PLAIN
B: ${BRACED}
C: ${COLON_DASH:-x}
D: ${DASH-x}
E: ${COLON_Q:?required}
F: ${Q?required}
G: ${COLON_PLUS:+set}
H: ${PLUS+set}
I: $$ESCAPED
K: ${OUTER:-${INNER}}
L: ${BOTH:-x} and ${BOTH}
`
	got := map[string]bool{}
	Undefaulted([]byte(src), got)
	want := []string{"BOTH", "BRACED", "COLON_Q", "INNER", "PLAIN", "Q"}
	if !reflect.DeepEqual(names(got), want) {
		t.Fatalf("undefaulted = %v\nwant          %v", names(got), want)
	}
	all := map[string]bool{}
	References([]byte(src), all)
	if len(all) != 11 { // every name but the escaped one
		t.Fatalf("references = %v", names(all))
	}
}
```

Run: `go test ./internal/domain/varref/`
Expected: FAIL to compile (the moved file still declares `package vars`; `Undefaulted` does not exist yet).

- [ ] **Step 2: Rewrite the moved file and the wrapper**

`internal/domain/varref/refs.go` (the existing scanner, generalized to report whether each reference has a fallback):

```go
// Package varref scans text for the variables compose would interpolate:
// the shared variables renderer (agent) and template validation (API)
// read references the same way.
package varref

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

// braceDefault reports whether the ${NAME form at data[k:] (braceForm is
// true) needs no value for NAME: :- - :+ + substitute without it.
func braceDefault(data []byte, k int) bool {
	switch data[k] {
	case '-', '+':
		return true
	case ':':
		return data[k+1] == '-' || data[k+1] == '+'
	}
	return false
}

// scan calls found for every reference in data with whether that form
// gives the name a fallback. $$ is compose's escape for a literal $ and
// starts nothing. Names inside a default (${A:-${B}}) are found too.
func scan(data []byte, found func(name string, defaulted bool)) {
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
				found(string(data[i+2:end]), braceDefault(data, end))
			}
			i++ // keep scanning inside the braces: a default can reference more names
		default:
			end := nameEnd(data, i+1)
			if end > i+1 {
				found(string(data[i+1:end]), false)
				i = end - 1
			}
		}
	}
}

// References adds to into every variable name data references: $NAME,
// ${NAME} and ${NAME:-x}, ${NAME-x}, ${NAME:?x}, ${NAME?x}, ${NAME:+x},
// ${NAME+x}.
func References(data []byte, into map[string]bool) {
	scan(data, func(name string, _ bool) { into[name] = true })
}

// Undefaulted adds to into every name data references at least once in a
// form that needs a value: $NAME, ${NAME}, ${NAME:?x}, ${NAME?x}. A name
// only ever referenced as ${NAME:-x}, ${NAME-x}, ${NAME:+x} or ${NAME+x}
// is left out.
func Undefaulted(data []byte, into map[string]bool) {
	scan(data, func(name string, defaulted bool) {
		if !defaulted {
			into[name] = true
		}
	})
}
```

`internal/infra/vars/refs.go` (new; keeps the agent's callers and `refs_test.go` unchanged):

```go
package vars

import "winterflow/internal/domain/varref"

// References adds to into every variable name data references (see
// varref.References).
func References(data []byte, into map[string]bool) { varref.References(data, into) }
```

Run: `go test -count=1 ./internal/domain/varref/ ./internal/infra/vars/ && go test -count=1 ./internal/infra/orchestrator/docker_compose/ -run Shared`
Expected: `ok` ×3.

- [ ] **Step 3: Write the failing validation tests**

`internal/domain/template/validate_test.go`:

```go
package template

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
)

const tenantCompose = `services:
  app:
    image: ghcr.io/dmtrkzntsv/twillingate:${VERSION}
    mem_limit: ${MEMORY_MAX}
    ports: ["127.0.0.1:${PORT}:8080"]
    environment:
      PUBLIC_URL: https://${NAME}.twillingate.app
      SMTP_PASS: ${SMTP_PASS}
      OPTIONAL: ${OPTIONAL:-x}
      SESSION_KEY: ${SESSION_KEY}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }
`

func strp(s string) *string { return &s }

// tenant is the spec's twillingate template, with a host port parameter.
func tenant() Body {
	return Body{
		Files: map[string]string{"compose.yaml": tenantCompose},
		Parameters: []Parameter{
			{Name: "NAME", Required: true, Pattern: "^[a-z]{3,20}$", Description: "Instance name"},
			{Name: "VERSION", Required: true},
			{Name: "PORT", Required: true, Pattern: "^[0-9]+$"},
			{Name: "MEMORY_MAX", Default: strp("256m")},
			{Name: "INGEST_DISABLED", Default: strp("")},
			{Name: "SESSION_KEY", Secret: true, Required: true},
			{Name: "API_TOKEN", Secret: true},
		},
		AppName: "${NAME}",
		Icon:    "chart",
		Color:   "#2563eb",
		Ingress: []IngressRow{{Host: "${NAME}.twillingate.app", Service: "app", Port: "${PORT}"}},
		Backups: map[string]BackupDefault{"db": {Enabled: true, Storage: "tenants"}},
	}
}

func wantInvalid(t *testing.T, err error, field string) {
	t.Helper()
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, model.ErrRejected) {
		t.Fatalf("want an invalid-template error naming %s, got %v", field, err)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("error %q does not name %s", err, field)
	}
}

func TestValidateAcceptsTheTenantTemplate(t *testing.T) {
	if err := Validate(tenant()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(b *Body)
		field string
	}{
		{"no compose file", func(b *Body) { b.Files = map[string]string{"app.env": "x"} }, "files"},
		{"absolute file", func(b *Body) { b.Files["/etc/passwd"] = "x" }, "files"},
		{"escaping file", func(b *Body) { b.Files["../x"] = "x" }, "files"},
		{"winterflow file", func(b *Body) { b.Files[".winterflow/config.json"] = "{}" }, "files"},
		{"agent env file", func(b *Body) { b.Files[".env"] = "A=1" }, "files"},
		{"lowercase parameter", func(b *Body) { b.Parameters[0].Name = "name" }, "parameters[0].name"},
		{"reserved WF_", func(b *Body) { b.Parameters[0].Name = "WF_APP_ID" }, "parameters[0].name"},
		{"reserved project", func(b *Body) { b.Parameters[0].Name = "COMPOSE_PROJECT_NAME" }, "parameters[0].name"},
		{"duplicate", func(b *Body) { b.Parameters[1].Name = "NAME" }, "parameters[1].name"},
		{"bad pattern", func(b *Body) { b.Parameters[0].Pattern = "([a-z" }, "parameters[0].pattern"},
		{"long pattern", func(b *Body) { b.Parameters[0].Pattern = strings.Repeat("a", 513) }, "parameters[0].pattern"},
		{"default off pattern", func(b *Body) { b.Parameters[0].Default = strp("UPPER") }, "parameters[0].default"},
		{"secret default", func(b *Body) { b.Parameters[5].Default = strp("x") }, "parameters[5].default"},
		{"empty app name", func(b *Body) { b.AppName = " " }, "app_name"},
		{"app name uses a secret", func(b *Body) { b.AppName = "${SESSION_KEY}" }, "app_name"},
		{"app name uses an unknown", func(b *Body) { b.AppName = "${NOPE}" }, "app_name"},
		{"host uses a secret", func(b *Body) { b.Ingress[0].Host = "${SESSION_KEY}.x.app" }, "ingress[0].host"},
		{"host syntax", func(b *Body) { b.Ingress[0].Host = "Bad_Host" }, "ingress"},
		{"port range", func(b *Body) { b.Ingress[0].Port = "70000" }, "ingress[0].port"},
		{"port mixed", func(b *Body) { b.Ingress[0].Port = "80${PORT}" }, "ingress[0].port"},
		{"port secret", func(b *Body) { b.Ingress[0].Port = "${SESSION_KEY}" }, "ingress[0].port"},
		{"backup not in recipe", func(b *Body) { b.Backups["media"] = BackupDefault{} }, "backups.media"},
		{"enabled backup without storage", func(b *Body) { b.Backups["db"] = BackupDefault{Enabled: true} }, "backups.db.storage"},
		{"backup every", func(b *Body) { b.Backups["db"] = BackupDefault{Every: "1s"} }, "backups.db.every"},
		{"broken recipe", func(b *Body) {
			b.Files["compose.yaml"] = "services: {app: {image: x}}\nx-winterflow:\n  backups:\n    - {name: db, type: sqlite, volume: nope, path: a.db}\n"
		}, "files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tenant()
			tc.edit(&b)
			wantInvalid(t, Validate(b), tc.field)
		})
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"twillingate", "a", "shop-2"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Shop", "2shop", "-x", strings.Repeat("a", 64), "a_b"} {
		wantInvalid(t, ValidateName(bad), "name")
	}
}

func TestParseCapsTheBody(t *testing.T) {
	raw := []byte(`{"files":{"compose.yaml":"` + strings.Repeat("x", MaxBodyBytes) + `"}}`)
	wantInvalid(t, func() error { _, err := Parse(raw); return err }(), "body")
	_, err := Parse([]byte(`{"files": 3}`))
	wantInvalid(t, err, "body")
}

func TestPortJSON(t *testing.T) {
	var rows []IngressRow
	if err := json.Unmarshal([]byte(`[{"host":"a.test","port":8080},{"host":"b.test","port":"${PORT}"}]`), &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Port != "8080" || rows[1].Port != "${PORT}" {
		t.Fatalf("ports = %q %q", rows[0].Port, rows[1].Port)
	}
	out, _ := json.Marshal(rows)
	if want := `[{"host":"a.test","port":8080},{"host":"b.test","port":"${PORT}"}]`; string(out) != want {
		t.Fatalf("marshal = %s", out)
	}
	if err := json.Unmarshal([]byte(`[{"host":"a","port":true}]`), &rows); err == nil {
		t.Fatal("a boolean port was accepted")
	}
	if raw, err := Port("").JSONSchemaBytes(); err != nil || !json.Valid(raw) {
		t.Fatalf("schema = %s, %v", raw, err)
	}
}

func TestWarningsNameUnprovidedReferences(t *testing.T) {
	shared := func(name string) bool { return name == "SMTP_PASS" }
	if got := Warnings(tenant(), shared); len(got) != 0 {
		t.Fatalf("warnings = %v", got)
	}
	got := Warnings(tenant(), func(string) bool { return false })
	want := []string{"SMTP_PASS is not a parameter; it must come from a shared variable on the server"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("warnings = %v", got)
	}
}
```

Run: `go test ./internal/domain/template/`
Expected: FAIL to compile (`undefined: Body`, `undefined: Validate`, ...).

- [ ] **Step 4: Write the body model**

`internal/domain/template/body.go`:

```go
// Package template is a template revision's body: its shape, its
// validation at save, the check of an instance's parameter values, and its
// rendering into an ordinary app.save payload. The agent never sees a
// template; it gets the rendered app.
package template

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
)

// MaxBodyBytes caps a revision's body (its JSON encoding).
const MaxBodyBytes = 1 << 20

// ErrInvalid marks a body, value or request the caller can fix; the
// message names the field (parameters[1].pattern, parameters.NAME). It
// matches model.ErrRejected: the API answers it with a 400.
var ErrInvalid = model.Rejected("invalid template")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Body is a revision's body.
type Body struct {
	// Files are the app's files by relative path: what an app save
	// carries, never a secret. A compose file is required.
	Files      map[string]string `json:"files" required:"true" description:"The app's files by relative path (a compose file is required); never a secret."`
	Parameters []Parameter       `json:"parameters" description:"Each becomes an app variable of the same name."`
	// AppName, Icon and Color are the app's catalog record; AppName may
	// use ${PARAM} of plain parameters.
	AppName string       `json:"app_name" required:"true" description:"May use ${PARAM} of plain parameters."`
	Icon    string       `json:"icon"`
	Color   string       `json:"color"`
	Ingress []IngressRow `json:"ingress" description:"Hostname -> host port rows; hosts and ports may use ${PARAM} of plain parameters."`
	// Backups are, per recipe backup name, the defaults an instance gets.
	Backups map[string]BackupDefault `json:"backups" description:"Per recipe backup name, the settings an instance starts with."`
}

// Parameter is one value an instance supplies.
type Parameter struct {
	Name     string `json:"name" required:"true" description:"^[A-Z][A-Z0-9_]*$; not COMPOSE_PROJECT_NAME or WF_*."`
	Secret   bool   `json:"secret,omitempty" description:"Values arrive ECIES-encrypted for the server and are never returned."`
	Required bool   `json:"required,omitempty"`
	// Default is nil when the parameter has none ("" is a default).
	Default     *string `json:"default,omitempty" description:"Plain parameters only; must match pattern."`
	Pattern     string  `json:"pattern,omitempty" description:"Go RE2 regular expression a value must match, at most 512 bytes."`
	Description string  `json:"description,omitempty"`
}

// IngressRow routes Host to the host port Port that the compose file
// publishes (as the app editor's upstream port). Service names the compose
// service behind it, for people; rendering doesn't use it.
type IngressRow struct {
	Host    string `json:"host" required:"true"`
	Service string `json:"service,omitempty"`
	Port    Port   `json:"port" required:"true"`
}

// BackupDefault is an instance's starting settings for one recipe backup.
type BackupDefault struct {
	Enabled bool         `json:"enabled"`
	Storage string       `json:"storage,omitempty" description:"A storage name, looked up on the instance's server."`
	Every   string       `json:"every,omitempty"`
	Keep    *recipe.Keep `json:"keep,omitempty"`
}

// Port is an ingress row's port: a JSON number, or a string that is a
// number or exactly "${PARAM}" of a plain parameter.
type Port string

// UnmarshalJSON accepts a number or a string.
func (p *Port) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*p = Port(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("port must be a number or a string")
	}
	*p = Port(n.String())
	return nil
}

// MarshalJSON writes a number when the port is one.
func (p Port) MarshalJSON() ([]byte, error) {
	if _, err := strconv.Atoi(string(p)); err == nil {
		return []byte(p), nil
	}
	return json.Marshal(string(p))
}

// JSONSchemaBytes documents the two accepted forms (the OpenAPI
// generator reads it; returning bytes keeps this package free of it).
func (Port) JSONSchemaBytes() ([]byte, error) {
	return []byte(`{"type":["integer","string"],"description":"A host port 1-65535, or \"${PARAM}\" of a plain parameter."}`), nil
}

// Parse decodes and validates a body. The error names the field.
func Parse(raw []byte) (Body, error) {
	if len(raw) > MaxBodyBytes {
		return Body{}, invalid("body", "larger than %d bytes", MaxBodyBytes)
	}
	var b Body
	if err := json.Unmarshal(raw, &b); err != nil {
		return Body{}, invalid("body", "%v", err)
	}
	return b, Validate(b)
}

// Encode is the stored form of a validated body.
func Encode(b Body) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBodyBytes {
		return nil, invalid("body", "larger than %d bytes", MaxBodyBytes)
	}
	return raw, nil
}

// param returns the body's parameter named name.
func (b Body) param(name string) (Parameter, bool) {
	for _, p := range b.Parameters {
		if p.Name == name {
			return p, true
		}
	}
	return Parameter{}, false
}

// composeContents are the body's compose files in compose's load order.
func (b Body) composeContents() [][]byte {
	var out [][]byte
	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := b.Files[n]; return ok }) {
		out = append(out, []byte(b.Files[name]))
	}
	return out
}

// RecipeNames are the backup names the body's compose files declare.
func (b Body) RecipeNames() (map[string]bool, error) {
	comp, err := recipe.ParseCompose(b.composeContents()...)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range comp.Recipe {
		out[r.Name] = true
	}
	return out, nil
}

// trimmed reports whether s has no leading or trailing space.
func trimmed(s string) bool { return strings.TrimSpace(s) == s }
```

- [ ] **Step 5: Write the ingress helpers (shared with rendering)**

`internal/domain/template/ingress.go`:

```go
package template

import (
	"strconv"

	"winterflow/internal/domain/model"
)

// expand substitutes ${NAME} with the plain values (validation allows no
// other reference in the fields it is used on).
func expand(s string, plain map[string]string) string {
	return refRE.ReplaceAllStringFunc(s, func(m string) string {
		return plain[m[2:len(m)-1]]
	})
}

// renderIngress renders the rows. A port from a parameter must be a port
// number: the error names the parameter (at save, example values stand in
// for the real ones, so only host syntax is meant there: lenient skips the
// port check).
func renderIngress(rows []IngressRow, plain map[string]string, lenient bool) ([]model.IngressDomain, error) {
	out := []model.IngressDomain{}
	for _, row := range rows {
		port, err := strconv.Atoi(expand(string(row.Port), plain))
		if err != nil || port < 1 || port > 65535 {
			if !lenient {
				m := refRE.FindStringSubmatch(string(row.Port))
				name := string(row.Port)
				if m != nil {
					name = m[1]
				}
				return nil, invalid("parameters."+name, "%q is not a port 1-65535", expand(string(row.Port), plain))
			}
			port = 1
		}
		out = append(out, model.IngressDomain{Domain: expand(row.Host, plain), UpstreamPort: port})
	}
	return out, nil
}
```

- [ ] **Step 6: Write the validation**

`internal/domain/template/validate.go`:

```go
package template

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/varref"
)

// Limits of a body's parts.
const (
	maxPatternBytes = 512
	maxParameters   = 100
	maxIngressRows  = 50
	maxFileName     = 255
	maxAppName      = 100
	maxDescription  = 1000
)

var (
	// NameRE is a template name.
	NameRE      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	paramNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	// refRE is a ${NAME} reference in app_name, hosts and ports.
	refRE = regexp.MustCompile(`\$\{([^}]*)\}`)
	// placeholder stands in for a parameter without a default when the
	// ingress rows are checked at save: host syntax only.
	placeholder = "wf-placeholder"
)

// reservedFiles are written by the agent; a template can't carry them.
var reservedFiles = map[string]bool{
	".env": true, ".env.secrets": true, ".env.shared": true, ".env.shared.secrets": true, ".gitignore": true,
}

// ValidateName checks a template name.
func ValidateName(name string) error {
	if !NameRE.MatchString(name) {
		return invalid("name", "%q is not a valid template name: lowercase letters, digits and hyphens, starting with a letter, at most 63", name)
	}
	return nil
}

// Validate checks every rule a body must meet at save. The error names the
// field.
func Validate(b Body) error {
	if err := validateFiles(b.Files); err != nil {
		return err
	}
	plain, err := validateParameters(b.Parameters)
	if err != nil {
		return err
	}
	if err := validateAppName(b, plain); err != nil {
		return err
	}
	if len(b.Icon) > 64 || !trimmed(b.Icon) {
		return invalid("icon", "at most 64 characters, no surrounding spaces")
	}
	if len(b.Color) > 32 || !trimmed(b.Color) {
		return invalid("color", "at most 32 characters, no surrounding spaces")
	}
	if err := validateIngress(b, plain); err != nil {
		return err
	}
	return validateBackups(b)
}

func validateFiles(files map[string]string) error {
	if len(model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok })) == 0 {
		return invalid("files", "a compose file is required (%s)", strings.Join(model.ComposeFileNames, ", "))
	}
	for _, name := range sortedKeys(files) {
		if !validFileName(name) {
			return invalid("files", "%q is not a usable file path", name)
		}
	}
	return nil
}

// validFileName mirrors the agent's file path rule (a relative path inside
// the app's folder, not under .git or .winterflow) and refuses the files
// the agent writes itself.
func validFileName(name string) bool {
	if name == "" || len(name) > maxFileName || path.Clean(name) != name || path.IsAbs(name) || strings.Contains(name, `\`) {
		return false
	}
	first, _, _ := strings.Cut(name, "/")
	switch first {
	case ".", "..", ".git", ".winterflow":
		return false
	}
	return !reservedFiles[name]
}

// validateParameters returns the plain parameters by name.
func validateParameters(params []Parameter) (map[string]Parameter, error) {
	if len(params) > maxParameters {
		return nil, invalid("parameters", "at most %d", maxParameters)
	}
	plain := map[string]Parameter{}
	seen := map[string]bool{}
	for i, p := range params {
		field := fmt.Sprintf("parameters[%d]", i)
		switch {
		case !paramNameRE.MatchString(p.Name):
			return nil, invalid(field+".name", "%q is not a valid parameter name: uppercase letters, digits and _, starting with a letter", p.Name)
		case p.Name == "COMPOSE_PROJECT_NAME" || strings.HasPrefix(p.Name, "WF_"):
			return nil, invalid(field+".name", "%s is reserved", p.Name)
		case seen[p.Name]:
			return nil, invalid(field+".name", "%s is declared twice", p.Name)
		case len(p.Description) > maxDescription:
			return nil, invalid(field+".description", "at most %d characters", maxDescription)
		}
		seen[p.Name] = true
		var re *regexp.Regexp
		if p.Pattern != "" {
			if len(p.Pattern) > maxPatternBytes {
				return nil, invalid(field+".pattern", "at most %d bytes", maxPatternBytes)
			}
			var err error
			if re, err = regexp.Compile(p.Pattern); err != nil {
				return nil, invalid(field+".pattern", "%v", err)
			}
		}
		if p.Secret {
			if p.Default != nil {
				return nil, invalid(field+".default", "a secret parameter has no default: a template never holds a secret value")
			}
			continue
		}
		if p.Default != nil && re != nil && !re.MatchString(*p.Default) {
			return nil, invalid(field+".default", "%q does not match the pattern", *p.Default)
		}
		plain[p.Name] = p
	}
	return plain, nil
}

// checkRefs refuses a ${...} in s that is not a declared plain parameter.
func checkRefs(field, s string, plain map[string]Parameter) error {
	for _, m := range refRE.FindAllStringSubmatch(s, -1) {
		if _, ok := plain[m[1]]; !ok {
			return invalid(field, "${%s} is not a plain parameter of the template", m[1])
		}
	}
	return nil
}

func validateAppName(b Body, plain map[string]Parameter) error {
	if strings.TrimSpace(b.AppName) == "" || len(b.AppName) > maxAppName {
		return invalid("app_name", "required, at most %d characters", maxAppName)
	}
	return checkRefs("app_name", b.AppName, plain)
}

// exampleValues are the values the ingress rows are checked with at save:
// each plain parameter's default, or the placeholder.
func exampleValues(plain map[string]Parameter) map[string]string {
	out := map[string]string{}
	for name, p := range plain {
		out[name] = placeholder
		if p.Default != nil && *p.Default != "" {
			out[name] = *p.Default
		}
	}
	return out
}

func validateIngress(b Body, plain map[string]Parameter) error {
	if len(b.Ingress) > maxIngressRows {
		return invalid("ingress", "at most %d rows", maxIngressRows)
	}
	example := exampleValues(plain)
	for i, row := range b.Ingress {
		field := fmt.Sprintf("ingress[%d]", i)
		if err := checkRefs(field+".host", row.Host, plain); err != nil {
			return err
		}
		if err := checkRefs(field+".port", string(row.Port), plain); err != nil {
			return err
		}
		if m := refRE.FindStringSubmatch(string(row.Port)); m == nil {
			if n, err := strconv.Atoi(string(row.Port)); err != nil || n < 1 || n > 65535 {
				return invalid(field+".port", "%q is neither a port 1-65535 nor ${PARAM}", row.Port)
			}
		} else if m[0] != string(row.Port) {
			return invalid(field+".port", "%q: a port is a number or exactly ${PARAM}", row.Port)
		}
		if len(row.Service) > 64 {
			return invalid(field+".service", "at most 64 characters")
		}
	}
	rows, err := renderIngress(b.Ingress, example, true)
	if err != nil {
		return err
	}
	ing := model.Ingress{Domains: rows, Redirects: []model.IngressRedirect{}}
	if err := ing.Validate(); err != nil {
		return invalid("ingress", "%v", err)
	}
	return nil
}

// validateBackups checks the compose recipe and that every default names a
// backup it declares and is valid applied to it.
func validateBackups(b Body) error {
	comp, err := recipe.ParseCompose(b.composeContents()...)
	if err != nil {
		return invalid("files", "%v", err)
	}
	if err := recipe.Validate(comp); err != nil {
		return invalid("files", "%v", err)
	}
	declared := map[string]bool{}
	for _, r := range comp.Recipe {
		declared[r.Name] = true
	}
	for _, name := range sortedKeys(b.Backups) {
		if !declared[name] {
			return invalid("backups."+name, "the compose file declares no backup %q", name)
		}
		if _, err := recipe.Effective(comp, []recipe.BackupConfig{b.Backups[name].config(name)}); err != nil {
			msg := strings.TrimPrefix(err.Error(), recipe.ErrInvalid.Error()+": backups[0]")
			return fmt.Errorf("%w: backups.%s%s", ErrInvalid, name, msg)
		}
	}
	return nil
}

// config is the backups-list entry of a default.
func (d BackupDefault) config(name string) recipe.BackupConfig {
	return recipe.BackupConfig{Name: name, Enabled: d.Enabled, Storage: d.Storage, Every: d.Every, Keep: d.Keep}
}

// Warnings lists the files' ${VAR} references that nothing provides: not a
// parameter, not a WF_ or COMPOSE_PROJECT_NAME name, not an org shared
// variable (shared reports those), and without a fallback of their own.
func Warnings(b Body, shared func(name string) bool) []string {
	refs := map[string]bool{}
	for _, name := range sortedKeys(b.Files) {
		varref.Undefaulted([]byte(b.Files[name]), refs)
	}
	var out []string
	for _, name := range sortedKeys(refs) {
		if _, ok := b.param(name); ok || strings.HasPrefix(name, "WF_") || name == "COMPOSE_PROJECT_NAME" || shared(name) {
			continue
		}
		out = append(out, fmt.Sprintf("%s is not a parameter; it must come from a shared variable on the server", name))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 7: Run the tests**

Run: `go test -count=1 -cover ./internal/domain/template/`
Expected: `ok` (coverage above 85%).

- [ ] **Step 8: Commit**

```bash
git add internal/domain/varref internal/infra/vars/refs.go internal/domain/template
git commit -m "feat(templates): template body model and validation

Moves the shared-variable reference scanner to internal/domain/varref so
template validation can warn about references nothing provides.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Instance values, rendering, backup defaults and the request hash

**Files:**
- Create: `internal/domain/template/values.go`, `internal/domain/template/render.go`, `internal/domain/template/hash.go`, `internal/domain/template/render_test.go`

**Interfaces:**
- Consumes (Task 2): `Body`, `Parameter`, `invalid`, `(Body).param`, `refRE`, `expand`, `renderIngress`, `(BackupDefault).config`, `sortedKeys`, `(Body).RecipeNames`; `command.AppPayload`, `command.ContentItem`, `command.EncryptedPlaceholder`, `model.App`, `model.Ingress`, `model.ValidateStorageName`, `recipe.BackupConfig`.
- Produces:
  - `template.Given{Params, Secrets map[string]string}`, `template.Stored{Params map[string]string; SecretNames []string}`, `template.Values{Plain, Secrets map[string]string; Stored Stored}`, `template.Resolve(b Body, stored Stored, given Given) (Values, error)`.
  - `template.Marker{TemplateID, TemplateName string; Revision int64; Key string; BackupDefaults []recipe.BackupConfig}` (JSON `template_id`, `template_name`, `revision`, `key`, `backup_defaults,omitempty`).
  - `template.Input{AppID string; Values Values; Marker *Marker; Backups []recipe.BackupConfig}`, `template.Render(b Body, in Input) (model.App, command.AppPayload, error)`.
  - `template.CreateBackups(b Body, storages map[string]string) ([]recipe.BackupConfig, error)`, `template.NewBackupDefaults(prev, next Body) ([]recipe.BackupConfig, error)`.
  - `template.HashInput{ServerID, TemplateID string; Revision int64; Params map[string]string; SecretNames []string; Backups map[string]string}`, `template.RequestHash(in HashInput) string` (64 hex chars).

- [ ] **Step 1: Write the failing tests**

`internal/domain/template/render_test.go` (uses `tenant()`, `tenantCompose`, `wantInvalid` from `validate_test.go`):

```go
package template

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

func given(params map[string]string, secrets map[string]string) Given {
	return Given{Params: params, Secrets: secrets}
}

func TestResolveCreate(t *testing.T) {
	v, err := Resolve(tenant(), Stored{}, given(
		map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"},
		map[string]string{"SESSION_KEY": "CT-1"}))
	if err != nil {
		t.Fatal(err)
	}
	wantPlain := map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001", "MEMORY_MAX": "256m", "INGEST_DISABLED": ""}
	if !reflect.DeepEqual(v.Plain, wantPlain) || !reflect.DeepEqual(v.Secrets, map[string]string{"SESSION_KEY": "CT-1"}) {
		t.Fatalf("values = %+v", v)
	}
	// Defaults are not recorded: an upgrade follows the template's default.
	if !reflect.DeepEqual(v.Stored, Stored{Params: map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}, SecretNames: []string{"SESSION_KEY"}}) {
		t.Fatalf("stored = %+v", v.Stored)
	}
}

func TestResolveRefusals(t *testing.T) {
	base := map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	key := map[string]string{"SESSION_KEY": "CT-1"}
	cases := []struct {
		name  string
		g     Given
		field string
	}{
		{"unknown", given(with("NOPE", "1"), key), "parameters.NOPE"},
		{"secret as plain", given(with("SESSION_KEY", "x"), key), "parameters.SESSION_KEY"},
		{"plain as secret", given(base, map[string]string{"SESSION_KEY": "CT", "NAME": "CT"}), "secrets.NAME"},
		{"unknown secret", given(base, map[string]string{"SESSION_KEY": "CT", "NOPE": "CT"}), "secrets.NOPE"},
		{"placeholder secret", given(base, map[string]string{"SESSION_KEY": command.EncryptedPlaceholder}), "secrets.SESSION_KEY"},
		{"missing required", given(map[string]string{"NAME": "acme", "PORT": "1"}, key), "parameters.VERSION"},
		{"empty required", given(with("VERSION", ""), key), "parameters.VERSION"},
		{"missing secret", given(base, nil), "secrets.SESSION_KEY"},
		{"off pattern", given(with("NAME", "AC"), key), "parameters.NAME"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tenant(), Stored{}, tc.g)
			wantInvalid(t, err, tc.field)
		})
	}
}

func TestResolveUpdateKeepsStoredValuesAndSecrets(t *testing.T) {
	stored := Stored{Params: map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001", "GONE": "x"}, SecretNames: []string{"SESSION_KEY", "OLD"}}
	v, err := Resolve(tenant(), stored, given(map[string]string{"VERSION": "v2"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if v.Plain["VERSION"] != "v2" || v.Plain["NAME"] != "acme" || v.Secrets["SESSION_KEY"] != command.EncryptedPlaceholder {
		t.Fatalf("values = %+v", v)
	}
	if _, ok := v.Stored.Params["GONE"]; ok || !reflect.DeepEqual(v.Stored.SecretNames, []string{"SESSION_KEY"}) {
		t.Fatalf("stored = %+v", v.Stored)
	}
	// A newly required parameter without a default must be given.
	b := tenant()
	b.Parameters = append(b.Parameters, Parameter{Name: "REGION", Required: true})
	_, err = Resolve(b, stored, Given{})
	wantInvalid(t, err, "parameters.REGION")
}

func TestRender(t *testing.T) {
	b := tenant()
	v, err := Resolve(b, Stored{}, given(map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}, map[string]string{"SESSION_KEY": "CT-1"}))
	if err != nil {
		t.Fatal(err)
	}
	backups, err := CreateBackups(b, map[string]string{"db": "fast"})
	if err != nil {
		t.Fatal(err)
	}
	marker := &Marker{TemplateID: "tpl", TemplateName: "twillingate", Revision: 3, Key: "k-1"}
	app, payload, err := Render(b, Input{AppID: "app-1", Values: v, Marker: marker, Backups: backups})
	if err != nil {
		t.Fatal(err)
	}
	if app.ID != "app-1" || app.Name != "acme" || app.Icon != "chart" || app.TemplateID != "tpl" {
		t.Fatalf("app = %+v", app)
	}
	if payload.AppID != "app-1" || len(payload.Files) != 1 || string(payload.Files[0].Content) != tenantCompose {
		t.Fatalf("files = %+v", payload.Files)
	}
	vars := map[string]command.ContentItem{}
	for _, it := range payload.Variables {
		vars[it.Name] = it
	}
	if len(vars) != 6 || string(vars["NAME"].Content) != "acme" || vars["NAME"].Encrypted ||
		string(vars["SESSION_KEY"].Content) != "CT-1" || !vars["SESSION_KEY"].Encrypted || string(vars["MEMORY_MAX"].Content) != "256m" {
		t.Fatalf("variables = %+v", payload.Variables)
	}
	if _, ok := vars["API_TOKEN"]; ok {
		t.Fatal("an optional secret that was never given became a variable")
	}
	if err := (command.SaveAppRequest{App: payload}).Validate(); err != nil {
		t.Fatalf("the payload fails save-app's own check: %v", err)
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(payload.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	ing, err := model.ParseIngress(payload.Config)
	if err != nil || ing == nil || len(ing.Domains) != 1 || ing.Domains[0].Domain != "acme.twillingate.app" || ing.Domains[0].UpstreamPort != 9001 {
		t.Fatalf("ingress = %+v, %v", ing, err)
	}
	if string(cfg["template_id"]) != `"tpl"` || string(cfg["instance"]) != `{"template_id":"tpl","template_name":"twillingate","revision":3,"key":"k-1"}` {
		t.Fatalf("marker = %s %s", cfg["template_id"], cfg["instance"])
	}
	if string(cfg["backups"]) != `[{"name":"db","enabled":true,"storage":"fast"}]` {
		t.Fatalf("backups = %s", cfg["backups"])
	}
	// The reconcile reads apps.template_id from the blob.
	var listed model.App
	if err := json.Unmarshal(payload.Config, &listed); err != nil || listed.TemplateID != "tpl" || listed.Name != "acme" {
		t.Fatalf("as listed = %+v, %v", listed, err)
	}
}

func TestRenderUpdateAndUnlink(t *testing.T) {
	b := tenant()
	v, err := Resolve(b, Stored{Params: map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "9001"}, SecretNames: []string{"SESSION_KEY"}}, Given{})
	if err != nil {
		t.Fatal(err)
	}
	app, payload, err := Render(b, Input{AppID: "app-1", Values: v})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]json.RawMessage
	_ = json.Unmarshal(payload.Config, &cfg)
	for _, k := range []string{"backups", "instance", "template_id"} {
		if _, ok := cfg[k]; ok {
			t.Errorf("unlink blob carries %s", k)
		}
	}
	if app.TemplateID != "" {
		t.Fatalf("unlinked app keeps template id %q", app.TemplateID)
	}
	for _, it := range payload.Variables {
		if it.Name == "SESSION_KEY" && (string(it.Content) != command.EncryptedPlaceholder || !it.Encrypted) {
			t.Fatalf("kept secret = %+v", it)
		}
	}
}

func TestRenderRefusesANonPortValue(t *testing.T) {
	b := tenant()
	b.Parameters[2].Pattern = ""
	v, err := Resolve(b, Stored{}, given(map[string]string{"NAME": "acme", "VERSION": "v1", "PORT": "http"}, map[string]string{"SESSION_KEY": "CT"}))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Render(b, Input{AppID: "a", Values: v})
	wantInvalid(t, err, "parameters.PORT")
}

func TestCreateBackups(t *testing.T) {
	b := tenant()
	got, err := CreateBackups(b, nil)
	if err != nil || !reflect.DeepEqual(got, []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "tenants"}}) {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	_, err = CreateBackups(b, map[string]string{"media": "x"})
	wantInvalid(t, err, "backups.media")
	_, err = CreateBackups(b, map[string]string{"db": "Bad Name"})
	wantInvalid(t, err, "backups.db.storage")
}

func TestNewBackupDefaultsOnlyForBackupsNewInTheRevision(t *testing.T) {
	prev := tenant()
	next := tenant()
	next.Files["compose.yaml"] = strings.Replace(tenantCompose,
		"    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }\n",
		"    - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }\n    - { name: media, type: files, volume: data }\n", 1)
	next.Backups["db"] = BackupDefault{Enabled: true, Storage: "elsewhere"}
	next.Backups["media"] = BackupDefault{Enabled: true, Storage: "tenants", Every: "6h"}
	if err := Validate(next); err != nil {
		t.Fatal(err)
	}
	got, err := NewBackupDefaults(prev, next)
	want := []recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "tenants", Every: "6h"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("new defaults = %+v, %v", got, err)
	}
	if got, _ := NewBackupDefaults(next, next); len(got) != 0 {
		t.Fatalf("same revision = %+v", got)
	}
}

func TestRequestHashComparesSecretNamesNotBytes(t *testing.T) {
	in := HashInput{ServerID: "s", TemplateID: "t", Params: map[string]string{"A": "1", "B": "2"}, SecretNames: []string{"K2", "K1"}}
	h := RequestHash(in)
	same := in
	same.Params = map[string]string{"B": "2", "A": "1"}
	same.SecretNames = []string{"K1", "K2"}
	if RequestHash(same) != h {
		t.Fatal("order changed the hash")
	}
	for name, edit := range map[string]func(*HashInput){
		"server":   func(i *HashInput) { i.ServerID = "s2" },
		"revision": func(i *HashInput) { i.Revision = 2 },
		"value":    func(i *HashInput) { i.Params = map[string]string{"A": "1", "B": "3"} },
		"secrets":  func(i *HashInput) { i.SecretNames = []string{"K1"} },
		"backups":  func(i *HashInput) { i.Backups = map[string]string{"db": "x"} },
	} {
		c := in
		edit(&c)
		if RequestHash(c) == h {
			t.Errorf("%s did not change the hash", name)
		}
	}
	if len(h) != 64 {
		t.Fatalf("hash = %q", h)
	}
}
```

Run: `go test ./internal/domain/template/`
Expected: FAIL to compile (`undefined: Resolve`, `undefined: Render`, ...).

- [ ] **Step 2: Write the values check**

`internal/domain/template/values.go`:

```go
package template

import (
	"regexp"
	"sort"

	"winterflow/internal/domain/command"
)

// Given are the values one call sends: plain values by name, and secret
// ciphertexts (ECIES for the instance's server) by name.
type Given struct {
	Params  map[string]string
	Secrets map[string]string
}

// Stored are an instance's recorded values: the plain values its callers
// set (not the defaults) and the names of the secrets it was given.
type Stored struct {
	Params      map[string]string
	SecretNames []string
}

// Values are what a render uses: every plain parameter's value (set, or
// its default; "" when it has neither) and, per secret parameter that has
// one, the ciphertext or command.EncryptedPlaceholder (the agent keeps its
// stored value).
type Values struct {
	Plain   map[string]string
	Secrets map[string]string
	// Stored is what the instance records once the save is confirmed.
	Stored Stored
}

// Resolve checks a call's values against the body's parameters and merges
// them over the stored ones (a stored value of a parameter the body no
// longer has, or that changed between plain and secret, is dropped). The
// error names the parameter: unknown, a secret sent as a plain value or the
// other way round, a required one missing, a value that doesn't match its
// pattern.
func Resolve(b Body, stored Stored, given Given) (Values, error) {
	for _, name := range sortedKeys(given.Params) {
		p, ok := b.param(name)
		switch {
		case !ok:
			return Values{}, invalid("parameters."+name, "the template has no parameter %s", name)
		case p.Secret:
			return Values{}, invalid("parameters."+name, "%s is a secret: send it in secrets, encrypted for the server", name)
		}
	}
	for _, name := range sortedKeys(given.Secrets) {
		p, ok := b.param(name)
		switch {
		case !ok:
			return Values{}, invalid("secrets."+name, "the template has no parameter %s", name)
		case !p.Secret:
			return Values{}, invalid("secrets."+name, "%s is not a secret parameter: send it in parameters", name)
		case given.Secrets[name] == "" || given.Secrets[name] == command.EncryptedPlaceholder:
			return Values{}, invalid("secrets."+name, "a ciphertext is required (leave the secret out to keep its value)")
		}
	}
	had := map[string]bool{}
	for _, n := range stored.SecretNames {
		had[n] = true
	}
	out := Values{Plain: map[string]string{}, Secrets: map[string]string{}, Stored: Stored{Params: map[string]string{}}}
	for _, p := range b.Parameters {
		if p.Secret {
			switch ct, ok := given.Secrets[p.Name]; {
			case ok:
				out.Secrets[p.Name] = ct
			case had[p.Name]:
				out.Secrets[p.Name] = command.EncryptedPlaceholder
			case p.Required:
				return Values{}, invalid("secrets."+p.Name, "required")
			default:
				continue
			}
			out.Stored.SecretNames = append(out.Stored.SecretNames, p.Name)
			continue
		}
		v, set := given.Params[p.Name]
		if !set {
			v, set = stored.Params[p.Name]
		}
		if set {
			out.Stored.Params[p.Name] = v
		} else if p.Default != nil {
			v = *p.Default
		}
		if p.Required && v == "" {
			return Values{}, invalid("parameters."+p.Name, "required")
		}
		if set && p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil || !re.MatchString(v) {
				return Values{}, invalid("parameters."+p.Name, "%q does not match the pattern %s", v, p.Pattern)
			}
		}
		out.Plain[p.Name] = v
	}
	sort.Strings(out.Stored.SecretNames)
	return out, nil
}
```

- [ ] **Step 3: Write the renderer and the backup helpers**

`internal/domain/template/render.go`:

```go
package template

import (
	"encoding/json"
	"sort"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// Marker is the "instance" key of an instance's config blob: the editor
// and the app page know from it that the app is an instance. The agent
// stores it, except BackupDefaults, which its save applies and drops.
type Marker struct {
	TemplateID   string `json:"template_id"`
	TemplateName string `json:"template_name"`
	Revision     int64  `json:"revision"`
	Key          string `json:"key"`
	// BackupDefaults are added by the agent's save for backup names the
	// app's backups list has no entry for (backups new in an upgrade).
	BackupDefaults []recipe.BackupConfig `json:"backup_defaults,omitempty"`
}

// Input is one render.
type Input struct {
	AppID  string
	Values Values
	// Marker is nil when the app stops being an instance (unlink).
	Marker *Marker
	// Backups, when non-nil, is the whole backups list to store (create);
	// nil leaves the app's stored list as it is.
	Backups []recipe.BackupConfig
}

// configFile and configVar are the config blob's item metadata (the app
// editor's shape).
type configFile struct {
	Filename    string `json:"filename"`
	IsEncrypted bool   `json:"is_encrypted"`
}

type configVar struct {
	Name        string `json:"name"`
	IsEncrypted bool   `json:"is_encrypted"`
}

// config is the rendered config blob.
type config struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Icon        string                 `json:"icon"`
	Color       string                 `json:"color"`
	Files       []configFile           `json:"files"`
	Variables   []configVar            `json:"variables"`
	Ingress     model.Ingress          `json:"ingress"`
	TemplateID  string                 `json:"template_id,omitempty"`
	Instance    *Marker                `json:"instance,omitempty"`
	Backups     *[]recipe.BackupConfig `json:"backups,omitempty"`
}

// Render turns a body and an instance's values into the catalog record and
// the app.save payload, exactly what the app editor would send: the files
// unchanged, one variable per parameter (secrets as the caller's
// ciphertext or the keep placeholder), and a config blob with the rendered
// ingress, the template id (the apps.list reconcile keeps apps.template_id
// from it) and the marker.
func Render(b Body, in Input) (model.App, command.AppPayload, error) {
	plain := in.Values.Plain
	app := model.App{ID: in.AppID, Name: expand(b.AppName, plain), Icon: b.Icon, Color: b.Color}
	cfg := config{Name: app.Name, Icon: b.Icon, Color: b.Color, Files: []configFile{}, Variables: []configVar{}}
	payload := command.AppPayload{AppID: in.AppID, Files: []command.ContentItem{}, Variables: []command.ContentItem{}}

	for _, name := range sortedKeys(b.Files) {
		payload.Files = append(payload.Files, command.ContentItem{Name: name, Content: []byte(b.Files[name])})
		cfg.Files = append(cfg.Files, configFile{Filename: name})
	}
	for _, p := range b.Parameters {
		if p.Secret {
			ct, ok := in.Values.Secrets[p.Name]
			if !ok {
				continue
			}
			payload.Variables = append(payload.Variables, command.ContentItem{Name: p.Name, Content: []byte(ct), Encrypted: true})
			cfg.Variables = append(cfg.Variables, configVar{Name: p.Name, IsEncrypted: true})
			continue
		}
		payload.Variables = append(payload.Variables, command.ContentItem{Name: p.Name, Content: []byte(plain[p.Name])})
		cfg.Variables = append(cfg.Variables, configVar{Name: p.Name})
	}
	rows, err := renderIngress(b.Ingress, plain, false)
	if err != nil {
		return model.App{}, command.AppPayload{}, err
	}
	cfg.Ingress = model.Ingress{Domains: rows, Redirects: []model.IngressRedirect{}}
	if in.Marker != nil {
		app.TemplateID = in.Marker.TemplateID
		cfg.TemplateID = in.Marker.TemplateID
		cfg.Instance = in.Marker
	}
	if in.Backups != nil {
		list := in.Backups
		cfg.Backups = &list
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return model.App{}, command.AppPayload{}, err
	}
	payload.Config = raw
	return app, payload, nil
}

// CreateBackups is the backups list a new instance starts with: each
// template default by name, its storage replaced by the call's override.
// An override must name a backup the template has a default for.
func CreateBackups(b Body, storages map[string]string) ([]recipe.BackupConfig, error) {
	for _, name := range sortedKeys(storages) {
		if _, ok := b.Backups[name]; !ok {
			return nil, invalid("backups."+name, "the template has no backup default %q", name)
		}
		if err := model.ValidateStorageName(storages[name]); err != nil {
			return nil, invalid("backups."+name+".storage", "%v", err)
		}
	}
	out := []recipe.BackupConfig{}
	for _, name := range sortedKeys(b.Backups) {
		c := b.Backups[name].config(name)
		if s, ok := storages[name]; ok {
			c.Storage = s
		}
		out = append(out, c)
	}
	return out, nil
}

// NewBackupDefaults are the defaults of next for backups prev's compose
// files don't declare: an upgrade adds them (the agent merges them only
// where the app has no entry). Backups both revisions declare keep the
// app's settings, Backups-tab changes included.
func NewBackupDefaults(prev, next Body) ([]recipe.BackupConfig, error) {
	old, err := prev.RecipeNames()
	if err != nil {
		return nil, err
	}
	var out []recipe.BackupConfig
	for _, name := range sortedKeys(next.Backups) {
		if !old[name] {
			out = append(out, next.Backups[name].config(name))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
```

- [ ] **Step 4: Write the request hash**

`internal/domain/template/hash.go`:

```go
package template

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// HashInput is what a create's replay check compares: the secrets by name
// only (ECIES ciphertexts differ on every encryption).
type HashInput struct {
	ServerID   string
	TemplateID string
	// Revision as requested: 0 is "the latest".
	Revision    int64
	Params      map[string]string
	SecretNames []string
	// Backups are the call's storage overrides.
	Backups map[string]string
}

// RequestHash is the hex SHA-256 of the input's canonical JSON (maps
// marshal with sorted keys; names are sorted).
func RequestHash(in HashInput) string {
	names := append([]string{}, in.SecretNames...)
	sort.Strings(names)
	params, backups := in.Params, in.Backups
	if params == nil {
		params = map[string]string{}
	}
	if backups == nil {
		backups = map[string]string{}
	}
	raw, _ := json.Marshal(struct {
		ServerID    string            `json:"server_id"`
		TemplateID  string            `json:"template_id"`
		Revision    int64             `json:"revision"`
		Params      map[string]string `json:"params"`
		SecretNames []string          `json:"secret_names"`
		Backups     map[string]string `json:"backups"`
	}{in.ServerID, in.TemplateID, in.Revision, params, names, backups})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -count=1 -cover ./internal/domain/template/`
Expected: `ok`, coverage about 93%. `TestRender` also checks that the payload passes `command.SaveAppRequest.Validate` and that `json.Unmarshal` of the config blob into `model.App` (what the agent's `apps.list` does) yields `TemplateID`, so a reconcile keeps `apps.template_id`.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/template
git commit -m "feat(templates): render an instance into an app.save payload

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Agent applies an instance's backup defaults

**Files:**
- Modify: `internal/infra/orchestrator/docker_compose/backupconfig.go` (add `applyBackupDefaults` before `cleanBackups`)
- Modify: `internal/infra/orchestrator/docker_compose/store.go:207` (`writeAppStore`)
- Test: `internal/infra/orchestrator/docker_compose/backupconfig_test.go` (append)

**Interfaces:**
- Consumes: `keepBackups`, `backupsKey`, `recipe.BackupConfig`, test helpers `newTestRepo`, `saveConfig`, `backupCompose`, `withBackups`, `r.readOverrides`, `r.readAppConfig`.
- Produces: `applyBackupDefaults(cfg []byte) []byte`; constants `instanceKey = "instance"`, `backupDefaultsKey = "backup_defaults"`. The wire contract with the API: config blob key `instance.backup_defaults` (a backups list), consumed by the save and never stored.

- [ ] **Step 1: Write the failing tests**

Append to `internal/infra/orchestrator/docker_compose/backupconfig_test.go`:

```go
func TestApplyBackupDefaults(t *testing.T) {
	const marker = `"instance":{"template_id":"t","template_name":"shop","revision":2,"key":"k"`
	// The rewritten blob is re-encoded from maps: keys come out sorted.
	const stored = `"instance":{"key":"k","revision":2,"template_id":"t","template_name":"shop"}`
	cases := []struct{ name, in, want string }{
		{"adds a default with no entry",
			`{"name":"a",` + marker + `,"backup_defaults":[{"name":"media","enabled":true,"storage":"s3"}]},"backups":[{"name":"database","storage":"disk"}]}`,
			`{"backups":[{"name":"database","storage":"disk"},{"name":"media","enabled":true,"storage":"s3"}],` + stored + `,"name":"a"}`},
		{"a stored entry wins",
			`{` + marker + `,"backup_defaults":[{"name":"database","enabled":true,"storage":"s3"}]},"backups":[{"name":"database"}]}`,
			`{"backups":[{"name":"database"}],` + stored + `}`},
		{"no backups list yet",
			`{` + marker + `,"backup_defaults":[{"name":"media","enabled":true,"storage":"s3"}]}}`,
			`{"backups":[{"name":"media","enabled":true,"storage":"s3"}],` + stored + `}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyBackupDefaults([]byte(tc.in)); string(got) != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
	for _, same := range []string{`{"name":"plain"}`, `not json`, `{"instance":{"key":"k"}}`, `{"instance":{"backup_defaults":"x"}}`} {
		if got := applyBackupDefaults([]byte(same)); string(got) != same {
			t.Errorf("%s -> %s", same, got)
		}
	}
}

// An upgrade's defaults reach the stored backups through a save that, like
// every instance update, carries no backups list: the stored settings are
// kept and only the new backup is added; the marker keeps no defaults.
func TestSaveAppliesInstanceBackupDefaults(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"hetzner"}]`)); err != nil {
		t.Fatal(err)
	}
	upgrade := `{"name":"demo","files":[{"filename":"compose.yml"}],"instance":{"template_id":"t","template_name":"shop","revision":2,"key":"k",` +
		`"backup_defaults":[{"name":"database","enabled":true,"storage":"other"},{"name":"media","enabled":true,"storage":"disk","every":"6h"}]}}`
	if _, err := saveConfig(t, r, "app-1", backupCompose, upgrade); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	got, err := r.readOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []recipe.BackupConfig{
		{Name: "database", Enabled: true, Storage: "hetzner"},
		{Name: "media", Enabled: true, Storage: "disk", Every: "6h"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %+v", got)
	}
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst, _ := cfg["instance"].(map[string]any)
	if inst == nil || inst["key"] != "k" || inst["backup_defaults"] != nil {
		t.Fatalf("stored marker = %v", cfg["instance"])
	}
}
```

Run: `go test ./internal/infra/orchestrator/docker_compose/ -run 'BackupDefaults'`
Expected: FAIL to compile (`undefined: applyBackupDefaults`).

- [ ] **Step 2: Implement the merge**

In `internal/infra/orchestrator/docker_compose/backupconfig.go`, insert before the comment `// cleanBackups validates the app's backups after a save wrote its files and`:

```go
// instanceKey is the key of a template instance's marker in config.json;
// backupDefaultsKey the marker's list of backups to add (the API sends it
// on an upgrade that brings backups new to the app).
const (
	instanceKey       = "instance"
	backupDefaultsKey = "backup_defaults"
)

// applyBackupDefaults adds each instance.backup_defaults entry to the
// backups list unless the list already has that name (a stored setting,
// Backups-tab changes included, always wins), and drops backup_defaults
// from the marker: it is an instruction for this save, not state. A blob
// without defaults, or one that doesn't parse, comes back unchanged.
func applyBackupDefaults(cfg []byte) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(cfg, &top) != nil || top == nil {
		return cfg
	}
	var marker map[string]json.RawMessage
	if json.Unmarshal(top[instanceKey], &marker) != nil || marker[backupDefaultsKey] == nil {
		return cfg
	}
	var defaults, list []recipe.BackupConfig
	if json.Unmarshal(marker[backupDefaultsKey], &defaults) != nil {
		return cfg
	}
	if raw, ok := top[backupsKey]; ok && json.Unmarshal(raw, &list) != nil {
		return cfg
	}
	have := map[string]bool{}
	for _, b := range list {
		have[b.Name] = true
	}
	for _, d := range defaults {
		if !have[d.Name] {
			list = append(list, d)
			have[d.Name] = true
		}
	}
	delete(marker, backupDefaultsKey)
	rawMarker, err := json.Marshal(marker)
	if err != nil {
		return cfg
	}
	top[instanceKey] = rawMarker
	if len(list) > 0 {
		rawList, err := json.Marshal(list)
		if err != nil {
			return cfg
		}
		top[backupsKey] = rawList
	}
	out, err := json.Marshal(top)
	if err != nil {
		return cfg
	}
	return out
}
```

In `internal/infra/orchestrator/docker_compose/store.go`, in `writeAppStore`, replace

```go
	if err := fsutil.WriteFile(filepath.Join(dir, configRel), keepBackups(app.Config, prevConfig), 0o644); err != nil {
```

with

```go
	if err := fsutil.WriteFile(filepath.Join(dir, configRel), applyBackupDefaults(keepBackups(app.Config, prevConfig)), 0o644); err != nil {
```

- [ ] **Step 3: Run the tests**

Run: `go test -count=1 ./internal/infra/orchestrator/docker_compose/ -run 'BackupDefaults|KeepBackups|SaveKeepsAndCleansBackups'`
Expected: `ok`. Then the whole package (no Docker needed for these; the package's Docker tests skip without it):

Run: `go test -count=1 ./internal/infra/orchestrator/docker_compose/`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add internal/infra/orchestrator/docker_compose/backupconfig.go internal/infra/orchestrator/docker_compose/store.go internal/infra/orchestrator/docker_compose/backupconfig_test.go
git commit -m "feat(agent): add a template upgrade's new backup defaults on save

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: App usecase — check before dispatch, result observers

**Files:**
- Modify: `internal/domain/usecase/app/usecase.go` (`SaveApp` split, `ControlApp`, `DeleteApp`)
- Modify: `internal/domain/usecase/app/backups.go` (`checkBackups`)
- Test: `internal/domain/usecase/app/observe_test.go` (new)

**Interfaces:**
- Consumes: the existing `UseCase` internals (`requireCovered`, `requireStorageRefs`, `requireBackupStorages`).
- Produces:
  - `type Observer func(port.CommandResult)` — runs after the usecase's own result handling; must not block.
  - `type CheckedSave struct{...}` with `func (s CheckedSave) AppID() string`.
  - `func (uc *UseCase) CheckSave(ctx context.Context, serverID string, app model.App, payload command.AppPayload, draft bool) (CheckedSave, error)` — every save-app check, nothing dispatched; an empty `app.ID` gets a new id.
  - `func (uc *UseCase) DispatchSave(ctx context.Context, userID string, s CheckedSave, observe Observer) (string, error)`.
  - `func (uc *UseCase) ControlAppObserved(ctx context.Context, userID, serverID, appID string, action command.ControlAction, observe Observer) (string, error)`.
  - `func (uc *UseCase) DeleteAppObserved(ctx context.Context, userID, serverID, appID string, deleteVolumes, deleteBackups bool, observe Observer) (string, error)`.
  - `SaveApp`, `ControlApp`, `DeleteApp` keep their signatures and behaviour (they call the above with a nil observer).
  - `checkBackups` also refuses an enabled `instance.backup_defaults` entry whose storage is not in the server's index (400, the backups-list message).

- [ ] **Step 1: Write the failing tests**

`internal/domain/usecase/app/observe_test.go`:

```go
package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/pkg/logger"
)

// recordingAppRepo records the catalog writes, to order them against an
// observer.
type recordingAppRepo struct {
	noopAppRepo
	events *[]string
}

func (r recordingAppRepo) SaveApp(_ context.Context, a model.App) error {
	*r.events = append(*r.events, "persist "+a.ID+" template="+a.TemplateID)
	return nil
}

func (r recordingAppRepo) DeleteApp(_ context.Context, id string) error {
	*r.events = append(*r.events, "delete "+id)
	return nil
}

func TestCheckSaveDispatchesNothing(t *testing.T) {
	disp := &recordingDispatcher{}
	uc := newTestUseCase(disp)
	checked, err := uc.CheckSave(context.Background(), "srv", model.App{Name: "x"}, command.AppPayload{Config: []byte(`{}`)}, false)
	if err != nil || checked.AppID() == "" || len(disp.all) != 0 {
		t.Fatalf("check = %+v, %v, %d dispatches", checked, err, len(disp.all))
	}
	_, err = uc.CheckSave(context.Background(), "srv", model.App{Name: "x"}, command.AppPayload{
		Config: []byte(`{}`), Variables: []command.ContentItem{{Name: "WF_STORAGE_X_PATH"}},
	}, false)
	if !errors.Is(err, model.ErrRejected) {
		t.Fatalf("reserved variable: %v", err)
	}
}

// The observer runs after the usecase's own result handling, on success
// and on failure.
func TestObserversRunAfterTheDB(t *testing.T) {
	var events []string
	disp := &recordingDispatcher{}
	uc := NewUseCase(&Deps{CommandDispatcher: disp, AppRepository: recordingAppRepo{events: &events}, Log: logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})})
	observe := func(res port.CommandResult) {
		events = append(events, "observe "+map[bool]string{true: "ok", false: "fail"}[res.Success])
	}
	checked, _ := uc.CheckSave(context.Background(), "srv", model.App{ID: "a1", Name: "x", TemplateID: "t1"}, command.AppPayload{Config: []byte(`{}`)}, false)
	if _, err := uc.DispatchSave(context.Background(), "u", checked, observe); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true, Payload: []byte(`{"app_id":"a1"}`)})
	disp.last().OnResult(port.CommandResult{Error: "boom"})
	if _, err := uc.ControlAppObserved(context.Background(), "u", "srv", "a1", command.ControlStop, observe); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true})
	if _, err := uc.DeleteAppObserved(context.Background(), "u", "srv", "a1", false, false, observe); err != nil {
		t.Fatal(err)
	}
	disp.last().OnResult(port.CommandResult{Success: true})
	want := "persist a1 template=t1,observe ok,observe fail,observe ok,delete a1,observe ok"
	if got := strings.Join(events, ","); got != want {
		t.Fatalf("events = %s\nwant     %s", got, want)
	}
	// Without an observer control sets no hook at all.
	if _, err := uc.ControlApp(context.Background(), "u", "srv", "a1", command.ControlStart); err != nil || disp.last().OnResult != nil {
		t.Fatalf("plain control: %v", err)
	}
}

// An upgrade's backup defaults need their storage on the server like a
// backups list does.
func TestSaveChecksInstanceBackupDefaults(t *testing.T) {
	ss := newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt"})
	uc := newStoragesUseCase(&recordingDispatcher{}, ss)
	compose := "services: {app: {image: x, volumes: [data:/data]}}\nvolumes: {data: {}}\nx-winterflow:\n  backups:\n" +
		"    - {name: db, type: sqlite, volume: data, path: a.db}\n    - {name: media, type: files, volume: data}\n"
	save := func(defaults []recipe.BackupConfig) error {
		cfg := `{"instance":{"template_id":"t","template_name":"s","revision":2,"key":"k","backup_defaults":` + mustJSON(t, defaults) + `}}`
		_, err := uc.CheckSave(context.Background(), "srv-1", model.App{ID: "a1", Name: "x"}, command.AppPayload{
			Config: []byte(cfg), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
		}, false)
		return err
	}
	if err := save([]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "disk"}, {Name: "db", Storage: "gone"}}); err != nil {
		t.Fatalf("a known storage (and a disabled default's): %v", err)
	}
	if err := save([]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "gone"}}); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `no storage "gone"`) {
		t.Fatalf("an unknown storage: %v", err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
```

Run: `go test ./internal/domain/usecase/app/`
Expected: FAIL to compile (`uc.CheckSave undefined`, `undefined: Observer`...).

- [ ] **Step 2: Split `SaveApp`**

In `internal/domain/usecase/app/usecase.go`, replace the whole `SaveApp` function (from its doc comment `// SaveApp dispatches an app.save command to the server's agent and returns` to the end of the file) with:

```go
// Observer is a caller's extra hook on a dispatched command's result: it
// runs after the usecase's own result handling (the DB is updated by
// then). It must not block.
type Observer func(port.CommandResult)

// CheckedSave is an app.save that passed every save-app check, ready to
// dispatch.
type CheckedSave struct {
	serverID string
	app      model.App
	req      command.SaveAppRequest
	ing      *model.Ingress
}

// AppID is the id the save addresses (assigned when the app was new).
func (s CheckedSave) AppID() string { return s.app.ID }

// SaveApp dispatches an app.save command to the server's agent and returns
// the request id immediately. This call does not block. It is an upsert:
// an empty app.ID means create (the API assigns identity), a present one
// means edit. payload carries the app's config blob plus its files and
// variables (secrets pre-encrypted by the browser); app is the catalog record
// persisted to the DB on success. When the agent confirms the save, the
// OnResult hook persists the app and the result is delivered to the user
// over SSE.
func (uc *UseCase) SaveApp(ctx context.Context, userID, serverID string, app model.App, payload command.AppPayload, draft bool) (string, error) {
	checked, err := uc.CheckSave(ctx, serverID, app, payload, draft)
	if err != nil {
		return "", err
	}
	return uc.DispatchSave(ctx, userID, checked, nil)
}

// CheckSave runs save-app's checks (reserved variables, ingress syntax,
// domain coverage and claims, storage and backup references) without
// dispatching anything: a template instance claims its key only once its
// rendered app passes them. An empty app.ID gets a new id.
func (uc *UseCase) CheckSave(ctx context.Context, serverID string, app model.App, payload command.AppPayload, draft bool) (CheckedSave, error) {
	// New apps have no id yet; the API owns identity, so assign one here and use
	// it both on the wire (the agent keys its on-disk storage by app id) and on
	// the persisted catalog record.
	isNew := app.ID == ""
	if isNew {
		app.ID = util.GenerateID()
	}
	payload.AppID = app.ID
	if len(payload.Config) == 0 {
		// Fall back to the catalog record as the config blob when the caller
		// didn't supply a richer config (keeps older callers working).
		cfgBytes, _ := json.Marshal(app)
		payload.Config = cfgBytes
	}

	// The agent runs the same Validate; here a reserved variable name is a
	// synchronous 400 instead of an async failure.
	req := command.SaveAppRequest{App: payload, Draft: draft}
	if err := req.Validate(); err != nil {
		return CheckedSave{}, err
	}

	// Ingress rides the config blob. Parse + validate + conflict-check here so
	// a bad config is a synchronous 400, not an async agent failure. ing==nil
	// (no "ingress" key) means the feature is untouched: skip everything.
	ing, err := model.ParseIngress(payload.Config)
	if err != nil {
		return CheckedSave{}, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
	}
	if ing != nil {
		if err := ing.Validate(); err != nil {
			return CheckedSave{}, fmt.Errorf("%w: %v", model.ErrIngressInvalid, err)
		}
		if err := uc.requireCovered(ctx, serverID, app.ID, ing); err != nil {
			return CheckedSave{}, err
		}
		if uc.domains != nil {
			claims, err := uc.domains.FindClaims(ctx, serverID, ing.DomainNames(), app.ID)
			if err != nil {
				uc.log.Error("SaveApp: find domain claims", "error", err, "app_id", app.ID)
				return CheckedSave{}, err
			}
			if len(claims) > 0 {
				var parts []string
				for _, c := range claims {
					parts = append(parts, fmt.Sprintf("%s is already used by app %q on this server", c.Domain, c.AppName))
				}
				return CheckedSave{}, fmt.Errorf("%w: %s", model.ErrDomainTaken, strings.Join(parts, "; "))
			}
		}
	}

	if err := uc.requireStorageRefs(ctx, serverID, payload, isNew); err != nil {
		return CheckedSave{}, err
	}
	return CheckedSave{serverID: serverID, app: app, req: req, ing: ing}, nil
}

// DispatchSave dispatches a checked save. On the agent's confirmation the
// catalog record and the domain index are updated, then observe (if any)
// runs.
func (uc *UseCase) DispatchSave(ctx context.Context, userID string, s CheckedSave, observe Observer) (string, error) {
	serverID, app, ing := s.serverID, s.app, s.ing
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppSave,
		Payload: s.req,
		OnResult: func(res port.CommandResult) {
			defer notify(observe, res)
			if !res.Success {
				return
			}
			var saved command.SaveAppResponse
			if len(res.Payload) > 0 {
				if err := json.Unmarshal(res.Payload, &saved); err != nil {
					uc.log.Error("SaveApp: decode result", "error", err)
					return
				}
			}
			persisted := app
			persisted.ServerID = serverID
			if saved.AppID != "" {
				persisted.ID = saved.AppID
			}
			if err := uc.repo.SaveApp(context.Background(), persisted); err != nil {
				uc.log.Error("SaveApp: persist", "error", err, "app_id", persisted.ID)
			}
			if uc.domains != nil && ing != nil {
				if err := uc.domains.ReplaceForApp(context.Background(), persisted.ID, serverID, ing); err != nil {
					// Index only: reconcile heals it on the next apps.list.
					uc.log.Error("SaveApp: update domain index", "error", err, "app_id", persisted.ID)
				}
			}
		},
	})
}

// notify runs observe, when set, with res.
func notify(observe Observer, res port.CommandResult) {
	if observe != nil {
		observe(res)
	}
}
```

- [ ] **Step 3: Observed control and delete**

In the same file replace `ControlApp`:

```go
func (uc *UseCase) ControlApp(ctx context.Context, userID, serverID, appID string, action command.ControlAction) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppControl,
		Payload: command.ControlAppRequest{AppID: appID, Action: action},
	})
}
```

with:

```go
func (uc *UseCase) ControlApp(ctx context.Context, userID, serverID, appID string, action command.ControlAction) (string, error) {
	return uc.ControlAppObserved(ctx, userID, serverID, appID, action, nil)
}

// ControlAppObserved is ControlApp with an observer on the result.
func (uc *UseCase) ControlAppObserved(ctx context.Context, userID, serverID, appID string, action command.ControlAction, observe Observer) (string, error) {
	in := port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppControl,
		Payload: command.ControlAppRequest{AppID: appID, Action: action},
	}
	if observe != nil {
		in.OnResult = func(res port.CommandResult) { observe(res) }
	}
	return uc.dispatcher.Dispatch(ctx, in)
}
```

and replace the head of `DeleteApp`:

```go
func (uc *UseCase) DeleteApp(ctx context.Context, userID, serverID, appID string, deleteVolumes, deleteBackups bool) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppDelete,
		Payload: command.DeleteAppRequest{AppID: appID, DeleteVolumes: deleteVolumes, DeleteBackups: deleteBackups},
		OnResult: func(res port.CommandResult) {
			if !res.Success {
```

with:

```go
func (uc *UseCase) DeleteApp(ctx context.Context, userID, serverID, appID string, deleteVolumes, deleteBackups bool) (string, error) {
	return uc.DeleteAppObserved(ctx, userID, serverID, appID, deleteVolumes, deleteBackups, nil)
}

// DeleteAppObserved is DeleteApp with an observer on the result (it runs
// after the app's rows are removed).
func (uc *UseCase) DeleteAppObserved(ctx context.Context, userID, serverID, appID string, deleteVolumes, deleteBackups bool, observe Observer) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppDelete,
		Payload: command.DeleteAppRequest{AppID: appID, DeleteVolumes: deleteVolumes, DeleteBackups: deleteBackups},
		OnResult: func(res port.CommandResult) {
			defer notify(observe, res)
			if !res.Success {
```

(the rest of the function body is unchanged).

- [ ] **Step 4: Check the defaults' storages**

In `internal/domain/usecase/app/backups.go`, in `checkBackups`, replace

```go
	var cfg struct {
		Backups *[]recipe.BackupConfig `json:"backups"`
	}
```

with

```go
	var cfg struct {
		Backups  *[]recipe.BackupConfig `json:"backups"`
		Instance *struct {
			BackupDefaults []recipe.BackupConfig `json:"backup_defaults"`
		} `json:"instance"`
	}
```

and replace

```go
	if err := uc.requireBackupStorages(ctx, serverID, backups); err != nil {
		return nil, err
	}
	declared := recipe.Declared(backups)
```

with

```go
	if err := uc.requireBackupStorages(ctx, serverID, backups); err != nil {
		return nil, err
	}
	// A template upgrade's backup defaults are added by the agent where
	// the app has no entry yet; an enabled one needs its storage too.
	if cfg.Instance != nil {
		var enabled []recipe.EffectiveBackup
		for _, d := range cfg.Instance.BackupDefaults {
			if d.Enabled {
				enabled = append(enabled, recipe.EffectiveBackup{Name: d.Name, Enabled: true, Storage: d.Storage})
			}
		}
		if err := uc.requireBackupStorages(ctx, serverID, enabled); err != nil {
			return nil, err
		}
	}
	declared := recipe.Declared(backups)
```

- [ ] **Step 5: Run the tests**

Run: `go test -race -count=1 ./internal/domain/usecase/app/ ./internal/app/web/handler/app/`
Expected: `ok` ×2 (the existing `SaveApp` tests pass unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/domain/usecase/app
git commit -m "refactor(apps): split save-app checks from dispatch; result observers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 6: Template routes

**Files:**
- Modify: `internal/app/web/util/response.go` (add `ErrorStatus`), `internal/app/web/openapi_test.go` (two wire cases)
- Create: `internal/domain/usecase/templates/{usecase.go,views.go,templates.go,fixture_test.go,templates_test.go}`
- Create: `internal/app/web/handler/templates/{handler.go,templates.go,routes.go,handler_test.go}`
- Modify: `internal/app/web/routes.go`
- Create: `internal/app/web/contract/templates_test.go`; Modify: `internal/app/web/contract/matrix_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: Task 1 `port.TemplateRepository`, model errors; Task 2 `template.Body`, `Validate`, `ValidateName`, `Encode`, `Warnings`, `ErrInvalid`; Task 5 `appuc.UseCase`.
- Produces:
  - `webutil.ErrorStatus(w http.ResponseWriter, status int, message string)`.
  - Package `winterflow/internal/domain/usecase/templates` (imported as `tuc`): `Deps{Repo port.TemplateRepository; Apps *appuc.UseCase; Shared SharedNames; Status AppStatuses; Log *logger.Logger}`, `New(d Deps) *UseCase`, `SharedNames` (`ListOrg(ctx, orgID) ([]model.SharedVariable, error)`), `AppStatuses` (`AppStatuses(serverID string, now time.Time) []command.AppStatus`), `ValidID(id string) bool`.
  - `SaveInput{ID, Name string; Body template.Body; Note string; BaseRevision int64}`; `(*UseCase).SaveTemplate(ctx, userID, orgID string, in SaveInput) (SavedTemplate, error)`, `ListTemplates(ctx, orgID) (TemplateList, error)`, `GetTemplate(ctx, orgID, id string, revision int64) (TemplateView, error)`, `DeleteTemplate(ctx, orgID, id string) error`, unexported `revision(ctx, templateID, rev) (model.TemplateRevision, template.Body, error)`.
  - Views: `TemplateSummary`, `TemplateList{Templates}`, `TemplateRevisionInfo`, `TemplateView`, `SavedTemplate{ID, Name, Revision, Warnings}`.
  - Package `winterflow/internal/app/web/handler/templates` (imported as `htpl`): `Deps`, `NewHandler(d *Deps) *Handler`, `WaitBound`, `(*Handler).Routes(api.Guards) []api.Route`, unexported `templateRoutes`, `callerOrg`, `serverCaller`, `requireID`, `fail`; request types `TemplateQuery`, `SaveTemplateRequest{ID, Name, Body, Note, BaseRevision}`, `TemplateIDRequest{ID}`.
  - Routes: `GET /api/v1/template/get-templates`, `GET /api/v1/template/get-template`, `POST /api/v1/template/save-template`, `POST /api/v1/template/delete-template`.
  - Contract helpers: `shopCompose`, `shopBody(compose string) template.Body`, `strPtr`, `saveTemplate(t, c, req) tuc.SavedTemplate`, `(e *env) foreignClient(t) (*client, string)`.

- [ ] **Step 1: Add `ErrorStatus` with its wire test**

Append to `internal/app/web/util/response.go`:

```go
// ErrorStatus writes a JSON error with the given status (404 an unknown
// resource, 409 a conflict, 502 an agent's failure).
func ErrorStatus(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(APIResponse{
		Success: false,
		Message: message,
		Data:    nil,
	})
}
```

In `internal/app/web/openapi_test.go`, in `TestOpenAPIMatchesWire`'s `cases`, after the `"rate limited"` case add (they are validated once the routes exist, Step 9):

```go
		{"not found", http.MethodGet, "/api/v1/template/get-template", 404, "application/json", func(w http.ResponseWriter) {
			util.ErrorStatus(w, http.StatusNotFound, "template not found")
		}},
		{"conflict", http.MethodPost, "/api/v1/template/delete-template", 409, "application/json", func(w http.ResponseWriter) {
			util.ErrorStatus(w, http.StatusConflict, "the template has live instances")
		}},
```

- [ ] **Step 2: Write the failing usecase tests**

`internal/domain/usecase/templates/fixture_test.go` (shared by Tasks 6–9):

```go
package templates_test

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
	apprepo "winterflow/internal/infra/db/repository/app"
	templaterepo "winterflow/internal/infra/db/repository/template"
)

var (
	org1, org2 = dbtest.ID(1), dbtest.ID(2)
	srv1, srv2 = dbtest.ID(11), dbtest.ID(12)
)

// dispatcher records every command; tests answer the last one with reply.
type dispatcher struct {
	mu   sync.Mutex
	sent []port.DispatchInput
}

func (d *dispatcher) Dispatch(_ context.Context, in port.DispatchInput) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, in)
	return "req-" + strconv.Itoa(len(d.sent)), nil
}

func (d *dispatcher) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.sent)
}

func (d *dispatcher) last(t *testing.T) port.DispatchInput {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sent) == 0 {
		t.Fatal("nothing dispatched")
	}
	return d.sent[len(d.sent)-1]
}

// reply runs the last command's result hook as the agent's answer would.
func (d *dispatcher) reply(t *testing.T, res port.CommandResult) {
	t.Helper()
	in := d.last(t)
	if res.Success && len(res.Payload) == 0 && in.Type == command.TypeAppSave {
		res.Payload, _ = json.Marshal(command.SaveAppResponse{AppID: in.Payload.(command.SaveAppRequest).App.AppID})
	}
	in.OnResult(res)
}

// noStorages is a storage index without any storage.
type noStorages struct{}

func (noStorages) ListForServer(context.Context, string) ([]model.StorageSummary, error) {
	return nil, nil
}
func (noStorages) Upsert(context.Context, string, model.StorageSummary) error { return nil }
func (noStorages) Delete(context.Context, string, string) error               { return nil }
func (noStorages) ReplaceForServer(context.Context, string, []model.StorageSummary) error {
	return nil
}

type fixture struct {
	uc   *templates.UseCase
	disp *dispatcher
	tpl  templates.SavedTemplate
}

const compose = `services:
  app:
    image: example/shop:${VERSION}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: shop.db }
`

func strp(s string) *string { return &s }

func body() template.Body {
	return template.Body{
		Files: map[string]string{"compose.yaml": compose},
		Parameters: []template.Parameter{
			{Name: "NAME", Required: true, Pattern: "^[a-z]+$"},
			{Name: "VERSION", Default: strp("1")},
			{Name: "KEY", Secret: true, Required: true},
		},
		AppName: "shop-${NAME}",
		Backups: map[string]template.BackupDefault{"db": {Enabled: false}},
	}
}

// newFixture is the usecase over a real database (org1 owns srv1, org2
// srv2) and a recording dispatcher, with template "shop" saved in org1.
// storages, when set, is the server storage index save-app checks.
func newFixture(t *testing.T, storages port.ServerStorageRepository) fixture {
	t.Helper()
	d := dbtest.Open(t)
	dbtest.SeedServer(t, d, org1, srv1)
	dbtest.SeedServer(t, d, org2, srv2)
	repo, err := templaterepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	apps, err := apprepo.New(d.Tx, d.Driver, dbtest.Log)
	if err != nil {
		t.Fatal(err)
	}
	disp := &dispatcher{}
	appDeps := &appuc.Deps{CommandDispatcher: disp, AppRepository: apps, Log: dbtest.Log}
	if storages != nil {
		appDeps.ServerStorageRepository = storages
	}
	uc := templates.New(templates.Deps{Repo: repo, Apps: appuc.NewUseCase(appDeps), Log: dbtest.Log})
	tpl, err := uc.SaveTemplate(dbtest.Ctx(t), "u1", org1, templates.SaveInput{Name: "shop", Body: body()})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{uc: uc, disp: disp, tpl: tpl}
}
```

`internal/domain/usecase/templates/templates_test.go`:

```go
package templates_test

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

func TestSaveTemplateConflictsAndWarnings(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: body(), BaseRevision: 0}); !errors.Is(err, model.ErrRevisionConflict) {
		t.Fatalf("stale base: %v", err)
	}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "x", Body: body(), BaseRevision: 3}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a new template with a base: %v", err)
	}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "Bad Name", Body: body()}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a bad name: %v", err)
	}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{Name: "x", Body: body(), Note: strings.Repeat("n", 1001)}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a long note: %v", err)
	}
	b := body()
	b.Files["compose.yaml"] += "# ${UNSET}\n"
	saved, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1, Note: "two"})
	if err != nil || saved.Revision != 2 || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "UNSET ") {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	view, err := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 1)
	if err != nil || view.Revision != 1 || view.LatestRevision != 2 || len(view.Revisions) != 2 || view.Revisions[0].Note != "two" {
		t.Fatalf("view = %+v, %v", view, err)
	}
	if latest, _ := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 0); latest.Revision != 2 || latest.Note != "two" {
		t.Fatalf("latest = %+v", latest)
	}
	if _, err := f.uc.GetTemplate(ctx, org1, f.tpl.ID, 9); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
	list, err := f.uc.ListTemplates(ctx, org1)
	if err != nil || len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := f.uc.DeleteTemplate(ctx, org2, f.tpl.ID); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("delete from another org: %v", err)
	}
	if err := f.uc.DeleteTemplate(ctx, org1, f.tpl.ID); err != nil {
		t.Fatal(err)
	}
}

func TestValidID(t *testing.T) {
	if !templates.ValidID(dbtest.ID(1)) {
		t.Fatal("a canonical uuid was refused")
	}
	for _, bad := range []string{"", "x", "00000000-0000-7000-8000-00000000000G", "00000000000070008000000000000001", "{00000000-0000-7000-8000-000000000001}"} {
		if templates.ValidID(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
```

Run: `go test ./internal/domain/usecase/templates/`
Expected: FAIL to compile (package `templates` has no non-test files).

- [ ] **Step 3: Write the usecase**

`internal/domain/usecase/templates/usecase.go`:

```go
// Package templates is the API side of templates and their instances:
// template revisions, and instances rendered into ordinary app.save
// payloads and dispatched through the app usecase (so every save-app
// check applies). The agent never sees a template.
package templates

import (
	"context"
	"time"

	"github.com/google/uuid"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/logger"
)

// SharedNames lists an org's shared variables (the save warnings skip
// their names); port.SharedVariableRepository satisfies it.
type SharedNames interface {
	ListOrg(ctx context.Context, orgID string) ([]model.SharedVariable, error)
}

// AppStatuses is the live status cache (*status.Cache satisfies it).
type AppStatuses interface {
	AppStatuses(serverID string, now time.Time) []command.AppStatus
}

// Deps wires the usecase. Shared and Status may be nil.
type Deps struct {
	Repo   port.TemplateRepository
	Apps   *appuc.UseCase
	Shared SharedNames
	Status AppStatuses
	Log    *logger.Logger
}

// UseCase serves the template and instance routes.
type UseCase struct {
	repo   port.TemplateRepository
	apps   *appuc.UseCase
	shared SharedNames
	status AppStatuses
	log    *logger.Logger
}

// New builds the usecase.
func New(d Deps) *UseCase {
	return &UseCase{repo: d.Repo, apps: d.Apps, shared: d.Shared, status: d.Status, log: d.Log}
}

// ValidID reports whether id is a canonical UUID (template ids are; on
// Postgres anything else could not even be looked up).
func ValidID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id
}
```

`internal/domain/usecase/templates/views.go`:

```go
package templates

import (
	"time"

	"winterflow/internal/domain/template"
)

// TemplateSummary is one row of GET get-templates.
type TemplateSummary struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	LatestRevision int64     `json:"latest_revision"`
	Instances      int64     `json:"instances" description:"Pending and created instances."`
	Behind         int64     `json:"behind" description:"Instances on a revision older than the latest."`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TemplateList is the data of GET get-templates.
type TemplateList struct {
	Templates []TemplateSummary `json:"templates"`
}

// TemplateRevisionInfo is one entry of a template's revision list.
type TemplateRevisionInfo struct {
	Revision  int64     `json:"revision"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// TemplateView is the data of GET get-template: one revision's body and
// the revision list, newest first.
type TemplateView struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	LatestRevision int64                  `json:"latest_revision"`
	Revision       int64                  `json:"revision"`
	Body           template.Body          `json:"body"`
	Note           string                 `json:"note"`
	CreatedAt      time.Time              `json:"created_at"`
	CreatedBy      string                 `json:"created_by"`
	Revisions      []TemplateRevisionInfo `json:"revisions"`
}

// SavedTemplate is the data of POST save-template.
type SavedTemplate struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	// Warnings name file references nothing provides (a typo, or a
	// shared variable the servers must have).
	Warnings []string `json:"warnings"`
}
```

`internal/domain/usecase/templates/templates.go`:

```go
package templates

import (
	"context"
	"encoding/json"
	"fmt"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
	"winterflow/pkg/util"
)

// SaveInput is POST save-template: no ID creates the template (revision
// 1, BaseRevision 0); an ID adds revision BaseRevision+1.
type SaveInput struct {
	ID           string
	Name         string
	Body         template.Body
	Note         string
	BaseRevision int64
}

// maxNote caps a revision note.
const maxNote = 1000

// SaveTemplate validates the body and stores it as a new revision. The
// warnings name file references nothing provides.
func (uc *UseCase) SaveTemplate(ctx context.Context, userID, orgID string, in SaveInput) (SavedTemplate, error) {
	if err := template.ValidateName(in.Name); err != nil {
		return SavedTemplate{}, err
	}
	if len(in.Note) > maxNote {
		return SavedTemplate{}, fmt.Errorf("%w: note: at most %d characters", template.ErrInvalid, maxNote)
	}
	if err := template.Validate(in.Body); err != nil {
		return SavedTemplate{}, err
	}
	raw, err := template.Encode(in.Body)
	if err != nil {
		return SavedTemplate{}, err
	}
	rev := model.TemplateRevision{Body: raw, Note: in.Note, CreatedBy: userID}
	out := SavedTemplate{ID: in.ID, Name: in.Name}
	if in.ID == "" {
		if in.BaseRevision != 0 {
			return SavedTemplate{}, fmt.Errorf("%w: base_revision: a new template has none (0)", template.ErrInvalid)
		}
		out.ID, out.Revision = util.GenerateID(), 1
		err = uc.repo.CreateTemplate(ctx, model.Template{ID: out.ID, OrgID: orgID, Name: in.Name, CreatedBy: userID}, rev)
	} else {
		out.Revision, err = uc.repo.AddRevision(ctx, orgID, in.ID, in.Name, in.BaseRevision, rev)
	}
	if err != nil {
		return SavedTemplate{}, err
	}
	shared, err := uc.sharedNames(ctx, orgID)
	if err != nil {
		return SavedTemplate{}, err
	}
	out.Warnings = template.Warnings(in.Body, func(name string) bool { return shared[name] })
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return out, nil
}

// sharedNames are the org's shared variable names.
func (uc *UseCase) sharedNames(ctx context.Context, orgID string) (map[string]bool, error) {
	out := map[string]bool{}
	if uc.shared == nil {
		return out, nil
	}
	vars, err := uc.shared.ListOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, v := range vars {
		out[v.Name] = true
	}
	return out, nil
}

// ListTemplates lists the org's templates with instance counts.
func (uc *UseCase) ListTemplates(ctx context.Context, orgID string) (TemplateList, error) {
	list, err := uc.repo.ListTemplates(ctx, orgID)
	if err != nil {
		return TemplateList{}, err
	}
	out := TemplateList{Templates: make([]TemplateSummary, 0, len(list))}
	for _, t := range list {
		out.Templates = append(out.Templates, TemplateSummary{
			ID: t.ID, Name: t.Name, LatestRevision: t.LatestRevision, Instances: t.Instances, Behind: t.Behind, UpdatedAt: t.UpdatedAt,
		})
	}
	return out, nil
}

// GetTemplate returns a revision (0: the latest) with the revision list.
func (uc *UseCase) GetTemplate(ctx context.Context, orgID, id string, revision int64) (TemplateView, error) {
	t, err := uc.repo.GetTemplate(ctx, orgID, id)
	if err != nil {
		return TemplateView{}, err
	}
	if revision == 0 {
		revision = t.LatestRevision
	}
	rev, body, err := uc.revision(ctx, id, revision)
	if err != nil {
		return TemplateView{}, err
	}
	revs, err := uc.repo.ListRevisions(ctx, id)
	if err != nil {
		return TemplateView{}, err
	}
	out := TemplateView{
		ID: t.ID, Name: t.Name, LatestRevision: t.LatestRevision, Revision: rev.Revision, Body: body,
		Note: rev.Note, CreatedAt: rev.CreatedAt, CreatedBy: rev.CreatedBy, Revisions: make([]TemplateRevisionInfo, 0, len(revs)),
	}
	for _, r := range revs {
		out.Revisions = append(out.Revisions, TemplateRevisionInfo{Revision: r.Revision, Note: r.Note, CreatedAt: r.CreatedAt, CreatedBy: r.CreatedBy})
	}
	return out, nil
}

// revision loads and decodes one stored revision.
func (uc *UseCase) revision(ctx context.Context, templateID string, revision int64) (model.TemplateRevision, template.Body, error) {
	rev, err := uc.repo.GetRevision(ctx, templateID, revision)
	if err != nil {
		return model.TemplateRevision{}, template.Body{}, err
	}
	var body template.Body
	if err := json.Unmarshal(rev.Body, &body); err != nil {
		return model.TemplateRevision{}, template.Body{}, fmt.Errorf("template %s r%d: %w", templateID, revision, err)
	}
	return rev, body, nil
}

// DeleteTemplate deletes a template without live instances.
func (uc *UseCase) DeleteTemplate(ctx context.Context, orgID, id string) error {
	return uc.repo.DeleteTemplate(ctx, orgID, id)
}
```

Run: `go test -race -count=1 ./internal/domain/usecase/templates/`
Expected: `ok`.

- [ ] **Step 4: Write the failing handler tests**

`internal/app/web/handler/templates/handler_test.go`:

```go
package templates

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-pkgz/auth/v2/token"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

var testLog = logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "templates-test"})

// Every route needs a user; template routes are org-scoped, instance
// routes are commands that can also wait (a 202 result and a 200 answer).
func TestRoutes(t *testing.T) {
	seen := map[string]bool{}
	for _, rt := range NewHandler(&Deps{}).Routes(api.Guards{}) {
		key := rt.Method + " " + rt.Path
		if seen[key] {
			t.Errorf("%s twice", key)
		}
		seen[key] = true
		if rt.Handler == nil || rt.Auth != api.User {
			t.Errorf("%s: want a User route with a handler", key)
		}
		switch {
		case strings.HasPrefix(rt.Path, "/api/v1/template/"):
			if rt.Tag != "templates" || rt.Accepted {
				t.Errorf("%s: want a synchronous route tagged templates", key)
			}
		case strings.HasPrefix(rt.Path, "/api/v1/instance/"):
			if rt.Tag != "instances" || (rt.Accepted && (rt.Response == nil || rt.Result == nil)) {
				t.Errorf("%s: want a route tagged instances; a command documents its 202 result and its waited 200", key)
			}
		default:
			t.Errorf("%s: outside /api/v1/template/ and /api/v1/instance/", key)
		}
	}
	for _, want := range []string{"GET /api/v1/template/get-templates", "GET /api/v1/template/get-template",
		"POST /api/v1/template/save-template", "POST /api/v1/template/delete-template"} {
		if !seen[want] {
			t.Errorf("missing %s", want)
		}
	}
}

func TestFail(t *testing.T) {
	h := &Handler{log: testLog}
	cases := []struct {
		err  error
		code int
		msg  string
	}{
		{model.ErrTemplateNotFound, http.StatusNotFound, "template not found"},
		{fmt.Errorf("wrapped: %w", model.ErrKeyInUse), http.StatusConflict, "key in use"},
		{model.ErrTemplateNameTaken, http.StatusBadRequest, "already exists"},
		{errors.New("db down"), http.StatusBadRequest, "failed to save"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		h.fail(w, tc.err, "save")
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.msg) {
			t.Errorf("%v -> %d %s", tc.err, w.Code, w.Body)
		}
	}
}

type orgs struct{ err error }

func (o orgs) PrimaryOrganizationID(context.Context, string) (string, error) { return "org-1", o.err }

func authed(r *http.Request) *http.Request {
	return token.SetUserInfo(r, token.User{ID: "provider_u1", Attributes: map[string]any{"user_id": "u1"}})
}

func serve(fn http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	fn(w, authed(httptest.NewRequest(method, target, strings.NewReader(body))))
	return w
}

// Unwired or failing dependencies fail closed; malformed ids are 400s on
// both databases (Postgres could not even look them up).
func TestTemplateRoutesFailClosed(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog})
	if w := serve(h.ListTemplates, http.MethodGet, "/", ""); w.Code != http.StatusBadRequest {
		t.Errorf("no org resolver = %d", w.Code)
	}
	w := httptest.NewRecorder()
	h.ListTemplates(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d", w.Code)
	}
	h.users = orgs{err: errors.New("down")}
	if w := serve(h.ListTemplates, http.MethodGet, "/", ""); w.Code != http.StatusBadRequest {
		t.Errorf("org lookup error = %d", w.Code)
	}
	h.users = orgs{}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"get, malformed id":      serve(h.GetTemplate, http.MethodGet, "/?id=x", ""),
		"get, negative revision": serve(h.GetTemplate, http.MethodGet, "/?id=00000000-0000-7000-8000-000000000001&revision=-1", ""),
		"save, malformed id":     serve(h.SaveTemplate, http.MethodPost, "/", `{"id":"x","name":"a","body":{}}`),
		"delete, malformed id":   serve(h.DeleteTemplate, http.MethodPost, "/", `{"id":"x"}`),
		"delete, malformed body": serve(h.DeleteTemplate, http.MethodPost, "/", `{`),
	} {
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, w.Code, w.Body)
		}
	}
}

// A save larger than the body cap is refused before it is decoded whole.
func TestSaveTemplateCapsTheRequest(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog})
	h.users = orgs{}
	body := `{"name":"big","body":{"files":{"compose.yaml":"` + strings.Repeat("x", maxSaveBytes) + `"}}}`
	if w := serve(h.SaveTemplate, http.MethodPost, "/", body); w.Code != http.StatusBadRequest {
		t.Fatalf("oversized save = %d %s", w.Code, w.Body)
	}
}
```

Run: `go test ./internal/app/web/handler/templates/`
Expected: FAIL to compile.

- [ ] **Step 5: Write the handler**

`internal/app/web/handler/templates/handler.go`:

```go
// Package templates serves templates (org-scoped) and their instances
// (server-addressed: every instance route checks the caller's org owns the
// server). Instance calls answer 202 with a request_id, or with wait:true
// hold the request for the agent's outcome, at most WaitBound.
package templates

import (
	"context"
	"errors"
	"net/http"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/service/status"
	appuc "winterflow/internal/domain/usecase/app"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/pkg/logger"
)

// WaitBound is how long a wait:true call holds its request (an image pull
// can take minutes); after it the call answers 202 and the outcome is read
// with get-instance.
const WaitBound = 10 * time.Minute

// OrgResolver resolves a user's organization (port.UserService).
type OrgResolver interface {
	PrimaryOrganizationID(ctx context.Context, userID string) (string, error)
}

// Deps wires the handler; with zero Deps (route documentation) it is never
// called.
type Deps struct {
	Logger                  *logger.Logger
	Templates               port.TemplateRepository
	SharedVariables         tuc.SharedNames
	Users                   OrgResolver
	Servers                 webutil.ServerAccess
	CommandDispatcher       port.CommandDispatcher
	AppRepository           port.AppRepository
	AppDomainRepository     port.AppDomainRepository
	ServerDomainRepository  port.ServerDomainRepository
	ServerStorageRepository port.ServerStorageRepository
	StatusCache             *status.Cache
}

// Handler serves the template and instance routes.
type Handler struct {
	uc        *tuc.UseCase
	users     OrgResolver
	servers   webutil.ServerAccess
	log       *logger.Logger
	waitBound time.Duration
}

// NewHandler wires the usecases.
func NewHandler(d *Deps) *Handler {
	apps := appuc.NewUseCase(&appuc.Deps{
		CommandDispatcher:       d.CommandDispatcher,
		AppRepository:           d.AppRepository,
		AppDomainRepository:     d.AppDomainRepository,
		ServerDomainRepository:  d.ServerDomainRepository,
		ServerStorageRepository: d.ServerStorageRepository,
		Servers:                 d.Servers,
		Log:                     d.Logger,
	})
	deps := tuc.Deps{Repo: d.Templates, Apps: apps, Shared: d.SharedVariables, Log: d.Logger}
	if d.StatusCache != nil { // a nil *status.Cache must not become a non-nil interface
		deps.Status = d.StatusCache
	}
	return &Handler{uc: tuc.New(deps), users: d.Users, servers: d.Servers, log: d.Logger, waitBound: WaitBound}
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

// requireID answers 400 unless id is a template id.
func requireID(w http.ResponseWriter, field, id string) bool {
	if !tuc.ValidID(id) {
		webutil.Error(w, field+" must be a template id", nil)
		return false
	}
	return true
}

// serverCaller resolves the caller and their org and requires serverID
// (400) owned by the caller's org (403).
func (h *Handler) serverCaller(w http.ResponseWriter, r *http.Request, serverID string) (string, string, bool) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return "", "", false
	}
	if serverID == "" {
		webutil.Error(w, "server_id is required", nil)
		return "", "", false
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, serverID) {
		return "", "", false
	}
	return userID, orgID, true
}

// fail maps usecase errors: unknown (or another org's) 404, a conflict
// 409, a fixable request 400 with its message, anything else a logged
// generic 400.
func (h *Handler) fail(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, model.ErrNotFound):
		webutil.ErrorStatus(w, http.StatusNotFound, err.Error())
	case errors.Is(err, model.ErrConflict):
		webutil.ErrorStatus(w, http.StatusConflict, err.Error())
	case errors.Is(err, model.ErrRejected):
		webutil.Error(w, err.Error(), nil)
	default:
		h.log.Error("templates: "+what, "error", err)
		webutil.Error(w, "failed to "+what, nil)
	}
}
```

`internal/app/web/handler/templates/templates.go`:

```go
package templates

import (
	"net/http"
	"strconv"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
)

// maxSaveBytes caps a save-template request: the body's own cap plus room
// for the envelope fields.
const maxSaveBytes = template.MaxBodyBytes + 64<<10

// TemplateQuery is the query of GET get-template.
type TemplateQuery struct {
	ID       string `query:"id" required:"true"`
	Revision int64  `query:"revision" description:"Revision to read; absent or 0 is the latest."`
}

// SaveTemplateRequest is the body of POST save-template.
type SaveTemplateRequest struct {
	ID           string        `json:"id,omitempty" description:"Absent to create the template."`
	Name         string        `json:"name" required:"true" description:"^[a-z][a-z0-9-]{0,62}$, unique in the org."`
	Body         template.Body `json:"body" required:"true"`
	Note         string        `json:"note,omitempty"`
	BaseRevision int64         `json:"base_revision" description:"The latest revision the editor read (0 for a new template); another one is a 409."`
}

// TemplateIDRequest is the body of POST delete-template.
type TemplateIDRequest struct {
	ID string `json:"id" required:"true"`
}

func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	out, err := h.uc.ListTemplates(r.Context(), orgID)
	if err != nil {
		h.fail(w, err, "list templates")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) GetTemplate(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if !requireID(w, "id", id) {
		return
	}
	var revision int64
	if s := q.Get("revision"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			webutil.Error(w, "revision must be a revision number", nil)
			return
		}
		revision = n
	}
	out, err := h.uc.GetTemplate(r.Context(), orgID, id, revision)
	if err != nil {
		h.fail(w, err, "read the template")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) SaveTemplate(w http.ResponseWriter, r *http.Request) {
	userID, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSaveBytes)
	req, ok := webutil.DecodeBody[SaveTemplateRequest](w, r)
	if !ok || (req.ID != "" && !requireID(w, "id", req.ID)) {
		return
	}
	out, err := h.uc.SaveTemplate(r.Context(), userID, orgID, tuc.SaveInput{
		ID: req.ID, Name: req.Name, Body: req.Body, Note: req.Note, BaseRevision: req.BaseRevision,
	})
	if err != nil {
		h.fail(w, err, "save the template")
		return
	}
	webutil.Success(w, "template saved", out)
}

func (h *Handler) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	_, orgID, ok := h.callerOrg(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[TemplateIDRequest](w, r)
	if !ok {
		return
	}
	if !requireID(w, "id", req.ID) {
		return
	}
	if err := h.uc.DeleteTemplate(r.Context(), orgID, req.ID); err != nil {
		h.fail(w, err, "delete the template")
		return
	}
	webutil.Success(w, "template deleted", nil)
}
```

`internal/app/web/handler/templates/routes.go` (Task 7 changes `Routes` to append the instance routes):

```go
package templates

import (
	"net/http"

	"winterflow/internal/app/web/api"
	tuc "winterflow/internal/domain/usecase/templates"
)

// Routes is the templates and instances slice of the route table.
func (h *Handler) Routes(api.Guards) []api.Route {
	return h.templateRoutes()
}

// templateRoutes are the org-scoped template routes.
func (h *Handler) templateRoutes() []api.Route {
	org := []int{http.StatusBadRequest, http.StatusNotFound}
	orgConflict := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict}
	return []api.Route{
		{
			Method: http.MethodGet, Path: "/api/v1/template/get-templates", Handler: h.ListTemplates, Auth: api.User,
			Summary: "The org's templates with their instance counts", Tag: "templates",
			Response: tuc.TemplateList{}, Errors: []int{http.StatusBadRequest},
		},
		{
			Method: http.MethodGet, Path: "/api/v1/template/get-template", Handler: h.GetTemplate, Auth: api.User,
			Summary: "A template revision's body (the latest by default) and the revision list", Tag: "templates",
			Query: TemplateQuery{}, Response: tuc.TemplateView{}, Errors: org,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/save-template", Handler: h.SaveTemplate, Auth: api.User,
			Summary: "Create a template or add a revision", Tag: "templates",
			Description: "base_revision must be the latest revision (409 otherwise: someone saved meanwhile). " +
				"warnings name file references nothing provides.",
			Request: SaveTemplateRequest{}, Response: tuc.SavedTemplate{}, Errors: orgConflict,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/template/delete-template", Handler: h.DeleteTemplate, Auth: api.User,
			Summary: "Delete a template without live instances", Tag: "templates",
			Request: TemplateIDRequest{}, Errors: orgConflict,
		},
	}
}
```

Run: `go test -count=1 ./internal/app/web/handler/templates/`
Expected: `ok`.

- [ ] **Step 6: Register the routes**

In `internal/app/web/routes.go` import `htemplates "winterflow/internal/app/web/handler/templates"` (after the `hsharedvars` import); before `routes := []api.Route{{` add:

```go
	templatesAPI := htemplates.NewHandler(&htemplates.Deps{
		Logger:                  d.Log,
		Templates:               d.TemplateRepository,
		SharedVariables:         d.SharedVariableRepository,
		Users:                   d.UserService,
		Servers:                 d.ServerRepository,
		CommandDispatcher:       d.CommandDispatcher,
		AppRepository:           d.AppRepository,
		AppDomainRepository:     d.AppDomainRepository,
		ServerDomainRepository:  d.ServerDomainRepository,
		ServerStorageRepository: d.ServerStorageRepository,
		StatusCache:             d.StatusCache,
	})
```

and add `templatesAPI.Routes(g),` after `sharedVarsAPI.Routes(g),` in the tables loop. (Every field is an interface or `*status.Cache`; `NewHandler` keeps a nil `*status.Cache` from becoming a non-nil interface.)

- [ ] **Step 7: Write the contract tests and probes**

`internal/app/web/contract/templates_test.go`:

```go
package contract_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/template"
	tuc "winterflow/internal/domain/usecase/templates"
)

// shopCompose publishes the port the ingress row routes to, references a
// secret parameter and a shared variable, and declares a sqlite backup.
const shopCompose = `services:
  app:
    image: example/shop:${VERSION}
    ports: ["127.0.0.1:${PORT}:8080"]
    environment:
      SECRET_KEY: ${SECRET_KEY}
      SMTP_PASS: ${SMTP_PASS}
    volumes: [data:/data]
volumes: { data: {} }
x-winterflow:
  backups:
    - { name: db, type: sqlite, volume: data, path: shop.db }
`

func strPtr(s string) *string { return &s }

// shopBody is a template with a plain, a defaulted and a secret parameter,
// an ingress row from parameters and a backup default on storage "disk".
func shopBody(compose string) template.Body {
	return template.Body{
		Files: map[string]string{"compose.yaml": compose},
		Parameters: []template.Parameter{
			{Name: "NAME", Required: true, Pattern: "^[a-z]{3,20}$"},
			{Name: "PORT", Required: true, Pattern: "^[0-9]{2,5}$"},
			{Name: "VERSION", Default: strPtr("1.0")},
			{Name: "SECRET_KEY", Secret: true, Required: true},
		},
		AppName: "shop-${NAME}",
		Icon:    "cart",
		Color:   "#112233",
		Ingress: []template.IngressRow{{Host: "${NAME}.example.test", Service: "app", Port: "${PORT}"}},
		Backups: map[string]template.BackupDefault{"db": {Enabled: true, Storage: "disk"}},
	}
}

// saveTemplate saves a template revision through the API.
func saveTemplate(t *testing.T, c *client, req htpl.SaveTemplateRequest) tuc.SavedTemplate {
	t.Helper()
	var out tuc.SavedTemplate
	c.post(t, "/api/v1/template/save-template", req).expect(t, http.StatusOK).data(t, &out)
	return out
}

// foreignClient is another org's member (logged in, subscribed) and that
// org's server.
func (e *env) foreignClient(t *testing.T) (*client, string) {
	t.Helper()
	srv := e.foreignServer(t)
	m := e.newClient()
	m.userID, m.email = "", "mallory@other.test"
	m.login(t, m.email, "password")
	m.subscribe(t)
	return m, srv
}

func TestTemplateRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)

	saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose), Note: "first"})
	if saved.Revision != 1 || saved.ID == "" || len(saved.Warnings) != 1 || !strings.HasPrefix(saved.Warnings[0], "SMTP_PASS is not a parameter") {
		t.Fatalf("saved = %+v", saved)
	}
	t.Run("the name is unique in the org", func(t *testing.T) {
		owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose)}).
			expect(t, http.StatusBadRequest)
	})
	t.Run("an invalid body names the field", func(t *testing.T) {
		bad := shopBody(shopCompose)
		bad.Parameters[3].Default = strPtr("plaintext")
		r := owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{Name: "other", Body: bad}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "parameters[3].default") {
			t.Fatalf("body = %s", r.Body)
		}
	})
	t.Run("a new revision needs the latest base", func(t *testing.T) {
		next := saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: saved.ID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1, Note: "second"})
		if next.Revision != 2 {
			t.Fatalf("next = %+v", next)
		}
		owner.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{ID: saved.ID, Name: "shop", Body: shopBody(shopCompose), BaseRevision: 1}).
			expect(t, http.StatusConflict)
	})
	t.Run("list and read", func(t *testing.T) {
		var list tuc.TemplateList
		owner.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 1 || list.Templates[0].LatestRevision != 2 || list.Templates[0].Instances != 0 {
			t.Fatalf("list = %+v", list)
		}
		var view tuc.TemplateView
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"1"}}).expect(t, http.StatusOK).data(t, &view)
		if view.Revision != 1 || view.Note != "first" || view.Body.AppName != "shop-${NAME}" || view.Body.Ingress[0].Port != "${PORT}" || len(view.Revisions) != 2 {
			t.Fatalf("view = %+v", view)
		}
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}, "revision": {"9"}}).expect(t, http.StatusNotFound)
	})
	t.Run("another org's template is unknown", func(t *testing.T) {
		mallory, _ := e.foreignClient(t)
		mallory.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}}).expect(t, http.StatusNotFound)
		mallory.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: saved.ID}).expect(t, http.StatusNotFound)
		var list tuc.TemplateList
		mallory.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &list)
		if len(list.Templates) != 0 {
			t.Fatalf("mallory sees %+v", list)
		}
	})
	t.Run("delete", func(t *testing.T) {
		owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: saved.ID}).expect(t, http.StatusOK)
		owner.get(t, "/api/v1/template/get-template", url.Values{"id": {saved.ID}}).expect(t, http.StatusNotFound)
	})
}
```

In `internal/app/web/contract/matrix_test.go`, add to the `probes` map after the `"POST /api/v1/shared-variables/reload"` entry:

```go

	"GET /api/v1/template/get-template":     {malformed: bad("GET", "/api/v1/template/get-template")},
	"POST /api/v1/template/save-template":   {malformed: bad("POST", "/api/v1/template/save-template")},
	"POST /api/v1/template/delete-template": {malformed: bad("POST", "/api/v1/template/delete-template")},
```

- [ ] **Step 8: Regenerate the spec and the route golden**

Run: `make openapi && go test -count=1 ./internal/app/web -run TestRoutesGolden -update`
Expected: `api/openapi.yaml` gains the four `/api/v1/template/*` operations and the `Body`, `BodyInput`, `Parameter`, `IngressRow`, `Port` (`type: [integer, string]`), `BackupDefault`, `TemplateList`, `TemplateView`, `SavedTemplate` components; `routes.golden` gains four lines.

- [ ] **Step 9: Run the web and contract suites**

Run: `go vet ./... && go test -count=1 ./internal/app/web/... && WF_CONTRACT_GUARD=require go test -count=1 ./internal/app/web/contract/`
Expected: `ok` everywhere (the guard sees every template route answer below 400; `TestForeignServer` has nothing new to check; `TestMalformed` gets 400 from the three probes).

- [ ] **Step 10: Commit**

```bash
git add internal/app/web/util/response.go internal/app/web/openapi_test.go internal/domain/usecase/templates \
  internal/app/web/handler/templates internal/app/web/routes.go internal/app/web/contract/templates_test.go \
  internal/app/web/contract/matrix_test.go api/openapi.yaml internal/app/web/testdata/routes.golden
git commit -m "feat(templates): template routes (save, list, read, delete)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Create instances, replay, wait, read

**Files:**
- Modify: `internal/app/web/api/route.go` (doc), `internal/app/web/api/openapi/openapi.go`, `internal/app/web/api/openapi/openapi_test.go`
- Create: `internal/domain/usecase/templates/{outcome.go,instance_views.go,instances.go,instances_test.go}`
- Create: `internal/app/web/handler/templates/{respond.go,instances.go,instance_routes.go,respond_test.go}`; Modify: `internal/app/web/handler/templates/routes.go`
- Modify: `internal/app/web/openapi_test.go` (502 case)
- Create: `internal/app/web/contract/instances_test.go`; Modify: `internal/app/web/contract/matrix_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: Task 3 `template.Resolve`, `Render`, `CreateBackups`, `RequestHash`, `Marker`, `Input`, `Given`, `Stored`, `Values`; Task 5 `appuc.CheckSave`, `DispatchSave`, `Observer`, `CheckedSave.AppID`; Task 6 usecase/handler internals (`revision`, `callerOrg`, `serverCaller`, `requireID`, `fail`, `WaitBound`, `waitBound`).
- Produces:
  - `api.Route`: an `Accepted` route with a non-nil `Response` is also documented with a 200 `envelope(Response)`.
  - `tuc.Outcome{RequestID string; Done <-chan port.CommandResult; Instance *InstanceView; Final func(ctx context.Context) (InstanceView, error)}`; unexported `(*UseCase).observe(record func(ctx, res) error) (appuc.Observer, <-chan port.CommandResult)`, `recordTimeout = 30 * time.Second`.
  - `tuc.InstanceView{Key, ServerID, AppID, TemplateID, TemplateName string; Revision int64; Parameters, Secrets map[string]string; State string; StatusCode command.ContainerStatusCode; LastError string; CreatedAt, UpdatedAt time.Time}`, `tuc.InstanceList{Instances}`, unexported `(*UseCase).view(inst, templateName) InstanceView`.
  - `tuc.ValidateKey(key string) error`, `tuc.CreateInput{ServerID, Key, TemplateID string; Revision int64; Params, Secrets, Backups map[string]string}`, `(*UseCase).CreateInstance(ctx, userID, orgID string, in CreateInput) (Outcome, error)`, `GetInstance(ctx, orgID, serverID, key string) (InstanceView, error)`, `ListInstances(ctx, orgID, serverID, templateID string) (InstanceList, error)`; unexported `dispatchSave(ctx, userID, inst, checked, revision, values) (Outcome, error)`, `reread(inst)`, `templateName(ctx, orgID, id) string`, `sortedNames`.
  - Handler: `htpl.InstanceResult{Instance tuc.InstanceView; RequestID string; Warnings []string}`, unexported `await(ctx, done, bound) (port.CommandResult, bool)`, `(*Handler).respond(w, r, out tuc.Outcome, wait bool, msg string)`, `requireKey(w, key) bool`, `instanceRoutes()`, const `waits`; request types `CreateInstanceRequest{ServerID, Key, TemplateID string; Revision int64; Parameters, Secrets, Backups map[string]string; Wait bool}`, `InstanceQuery`, `InstancesQuery`.
  - Routes: `POST /api/v1/instance/create-instance`, `GET /api/v1/instance/get-instance`, `GET /api/v1/instance/get-instances`.
  - Contract helpers: `instanceOf`, `instanceEnv{e, owner, sid, tplID}`, `newInstanceEnv(t)`, `(instanceEnv).create(key, name, port) htpl.CreateInstanceRequest`, `(instanceEnv).created(t, key, name, port) htpl.InstanceResult`, `lastSave(t, e) command.SaveAppRequest`.

- [ ] **Step 1: Let an Accepted route document its waited 200 (test first)**

Append to `internal/app/web/api/openapi/openapi_test.go`:

```go
// An Accepted route with a Response documents both answers: 202 with the
// request id, and 200 with the Response when the call waited.
func TestAcceptedRouteThatCanWait(t *testing.T) {
	const path = "/api/v1/thing/make-thing"
	route := api.Route{Method: http.MethodPost, Path: path, Handler: noop, Auth: api.User,
		Summary: "make", Tag: "things", Request: saveThing{}, Accepted: true, Result: thingResult{}, Response: thing{}}
	doc := load(t, []api.Route{route})
	if err := visit(schemaFor(t, doc, http.MethodPost, path, http.StatusAccepted, "application/json"),
		`{"success":true,"data":{"request_id":"r"}}`); err != nil {
		t.Fatalf("202: %v", err)
	}
	if err := visit(schemaFor(t, doc, http.MethodPost, path, http.StatusOK, "application/json"),
		`{"success":true,"data":{"name":"x"}}`); err != nil {
		t.Fatalf("200: %v", err)
	}
	route.Response = nil
	if load(t, []api.Route{route}).Paths.Find(path).Post.Responses.Status(http.StatusOK) != nil {
		t.Fatal("an Accepted route without a Response documents a 200")
	}
}
```

Run: `go test ./internal/app/web/api/openapi/ -run TestAcceptedRouteThatCanWait`
Expected: FAIL (`no 200 response`).

In `internal/app/web/api/openapi/openapi.go` replace

```go
	case rt.Accepted:
		oc.AddRespStructure(envelope(AcceptedData{}), oapi.WithHTTPStatus(http.StatusAccepted))
```

with

```go
	case rt.Accepted:
		oc.AddRespStructure(envelope(AcceptedData{}), oapi.WithHTTPStatus(http.StatusAccepted))
		if rt.Response != nil {
			// A route that can also wait for the outcome answers it with 200.
			oc.AddRespStructure(envelope(rt.Response), oapi.WithHTTPStatus(http.StatusOK))
		}
```

In `internal/app/web/api/route.go` replace the `Accepted` field's comment

```go
	// Accepted routes answer 202 with data {request_id}; the agent's reply
	// arrives over SSE as an operation_result whose payload is Result.
	Accepted bool
```

with

```go
	// Accepted routes answer 202 with data {request_id}; the agent's reply
	// arrives over SSE as an operation_result whose payload is Result. An
	// Accepted route with a Response can also answer 200 with it (a call
	// that waited for the outcome).
	Accepted bool
```

Run: `go test -count=1 ./internal/app/web/api/... && make openapi && git diff --stat api/openapi.yaml`
Expected: `ok`; `api/openapi.yaml` unchanged (no existing Accepted route has a Response).

- [ ] **Step 2: Write the failing usecase tests**

`internal/domain/usecase/templates/instances_test.go`:

```go
package templates_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

// create is a create-instance of key k1 on srv1 with the given values.
func (f fixture) create(params map[string]string) templates.CreateInput {
	return templates.CreateInput{ServerID: srv1, Key: "k1", TemplateID: f.tpl.ID, Params: params, Secrets: map[string]string{"KEY": "CT"}}
}

// received waits for an outcome's result.
func received(t *testing.T, done <-chan port.CommandResult) port.CommandResult {
	t.Helper()
	select {
	case res := <-done:
		return res
	case <-time.After(5 * time.Second):
		t.Fatal("no result delivered")
	}
	return port.CommandResult{}
}

func TestCreateClaimsDispatchesAndRecords(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	out, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"}))
	if err != nil || out.RequestID == "" || out.Instance != nil {
		t.Fatalf("create = %+v, %v", out, err)
	}
	pending, err := f.uc.GetInstance(ctx, org1, srv1, "k1")
	if err != nil || pending.State != "pending" || pending.AppID == "" {
		t.Fatalf("before the result = %+v, %v", pending, err)
	}
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	if req.App.AppID != pending.AppID || f.disp.last(t).AgentID != srv1 {
		t.Fatalf("dispatched %+v for %s", req.App.AppID, f.disp.last(t).AgentID)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	if res := received(t, out.Done); !res.Success {
		t.Fatalf("result = %+v", res)
	}
	got, err := out.Final(ctx)
	if err != nil || got.State != "created" || got.Parameters["NAME"] != "acme" || got.Secrets["KEY"] != command.EncryptedPlaceholder {
		t.Fatalf("after the result = %+v, %v", got, err)
	}
	list, err := f.uc.ListInstances(ctx, org1, srv1, f.tpl.ID)
	if err != nil || len(list.Instances) != 1 || list.Instances[0].TemplateName != "shop" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if other, _ := f.uc.ListInstances(ctx, org1, srv2, ""); len(other.Instances) != 0 {
		t.Fatalf("another server's list = %+v", other)
	}
	if _, err := f.uc.ListInstances(ctx, org2, "", f.tpl.ID); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template filter: %v", err)
	}
}

// A retried create carries a new ECIES ciphertext for the same secret: it
// is the same request (secrets compare by name).
func TestCreateReplays(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	in := f.create(map[string]string{"NAME": "acme"})
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
		t.Fatal(err)
	}
	appID := f.disp.last(t).Payload.(command.SaveAppRequest).App.AppID

	// Pending (never confirmed): the same request dispatches the save again,
	// with the same app id.
	in.Secrets = map[string]string{"KEY": "CT-other-bytes"}
	again, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if err != nil || again.RequestID == "" || f.disp.count() != 2 ||
		f.disp.last(t).Payload.(command.SaveAppRequest).App.AppID != appID {
		t.Fatalf("pending replay = %+v, %v", again, err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, again.Done)

	// Created: answered from the DB, nothing dispatched.
	done, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if err != nil || done.Instance == nil || done.Instance.AppID != appID || f.disp.count() != 2 {
		t.Fatalf("created replay = %+v, %v", done, err)
	}
	// Different values: a conflict.
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "other"})); !errors.Is(err, model.ErrKeyInUse) || !errors.Is(err, model.ErrConflict) {
		t.Fatalf("different values: %v", err)
	}
}

// A create whose rendered app fails a save-app check claims nothing: the
// caller can fix the request and use the same key.
func TestCreateRefusedBeforeTheClaim(t *testing.T) {
	f := newFixture(t, noStorages{})
	ctx := dbtest.Ctx(t)
	b := body()
	b.Backups["db"] = template.BackupDefault{Enabled: true, Storage: "nowhere"}
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	in := f.create(map[string]string{"NAME": "acme"})
	_, err := f.uc.CreateInstance(ctx, "u1", org1, in)
	if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `no storage "nowhere"`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.uc.GetInstance(ctx, org1, srv1, "k1"); !errors.Is(err, model.ErrInstanceNotFound) || f.disp.count() != 0 {
		t.Fatalf("a refused create claimed the key: %v", err)
	}
	in.Revision = 1 // its backup is off
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, in); err != nil {
		t.Fatalf("the fixed request: %v", err)
	}
}

func TestCreateErrors(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.CreateInstance(ctx, "u1", org2, f.create(map[string]string{"NAME": "acme"})); !errors.Is(err, model.ErrTemplateNotFound) {
		t.Fatalf("another org's template: %v", err)
	}
	bad := f.create(map[string]string{"NAME": "acme"})
	bad.Key = "has space"
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, bad); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("bad key: %v", err)
	}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "UPPER"})); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("off-pattern value: %v", err)
	}
	missing := f.create(map[string]string{"NAME": "acme"})
	missing.Revision = 7
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, missing); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}
	backups := f.create(map[string]string{"NAME": "acme"})
	backups.Backups = map[string]string{"media": "disk"}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, backups); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("an override of an unknown backup: %v", err)
	}
	out, _ := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"}))
	f.disp.reply(t, port.CommandResult{Error: "pull failed"})
	if res := received(t, out.Done); res.Success {
		t.Fatal("a failure was delivered as a success")
	}
	if got, _ := f.uc.GetInstance(ctx, org1, srv1, "k1"); got.State != "pending" || got.LastError != "pull failed" {
		t.Fatalf("after a failed create = %+v", got)
	}
	if _, err := f.uc.GetInstance(ctx, org1, srv2, "k1"); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("on another server: %v", err)
	}
}

// A caller that stopped waiting (its client went away, or the wait bound
// passed) must not lose the outcome: the result is recorded anyway and
// delivering it never blocks the bus.
func TestResultRecordedWithoutAWaiter(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"})); err != nil {
		t.Fatal(err)
	}
	in := f.disp.last(t)
	finished := make(chan struct{})
	go func() {
		in.OnResult(port.CommandResult{Success: true})
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the result hook blocked on a caller that is not listening")
	}
	if got, _ := f.uc.GetInstance(ctx, org1, srv1, "k1"); got.State != "created" {
		t.Fatalf("after an unwatched result = %+v", got)
	}
}
```

Run: `go test ./internal/domain/usecase/templates/`
Expected: FAIL to compile (`undefined: templates.CreateInput`, ...).

- [ ] **Step 3: Write the outcome, the views and create/get/list**

`internal/domain/usecase/templates/outcome.go`:

```go
package templates

import (
	"context"
	"time"

	"winterflow/internal/domain/port"
	appuc "winterflow/internal/domain/usecase/app"
)

// recordTimeout bounds the DB write a command's result makes.
const recordTimeout = 30 * time.Second

// Outcome is a dispatched instance command: RequestID names its SSE
// result and Done delivers the result once, after the DB is updated
// (buffered: the result hook never waits on a caller that stopped
// listening). When nothing was dispatched (a create replayed onto a
// created instance, a delete of a deleted one) Instance is set instead.
type Outcome struct {
	RequestID string
	Done      <-chan port.CommandResult
	Instance  *InstanceView
	// Final reads the instance once Done delivered a success (an unlinked
	// one can no longer be read by its key).
	Final func(ctx context.Context) (InstanceView, error)
}

// observe wraps record (the DB side of a result) into the observer the
// app usecase calls, and the channel it then delivers the result on.
func (uc *UseCase) observe(record func(ctx context.Context, res port.CommandResult) error) (appuc.Observer, <-chan port.CommandResult) {
	done := make(chan port.CommandResult, 1)
	return func(res port.CommandResult) {
		ctx, cancel := context.WithTimeout(context.Background(), recordTimeout)
		defer cancel()
		if err := record(ctx, res); err != nil {
			uc.log.Error("instance: record a result", "error", err)
		}
		select {
		case done <- res:
		default:
		}
	}, done
}
```

`internal/domain/usecase/templates/instance_views.go`:

```go
package templates

import (
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// InstanceView is an instance as the API shows it: never a secret value.
type InstanceView struct {
	Key          string `json:"key"`
	ServerID     string `json:"server_id"`
	AppID        string `json:"app_id"`
	TemplateID   string `json:"template_id" description:"Empty once the template is deleted."`
	TemplateName string `json:"template_name"`
	Revision     int64  `json:"revision"`
	// Parameters are the values callers set; parameters left out use the
	// revision's default.
	Parameters map[string]string `json:"parameters" description:"Values the callers set; the others use the revision's default."`
	Secrets    map[string]string `json:"secrets" description:"Each secret given, as \"<encrypted>\": values are never returned."`
	State      string            `json:"state" description:"pending, created or deleted."`
	// StatusCode is the app's latest container status (0 unknown).
	StatusCode command.ContainerStatusCode `json:"status_code"`
	LastError  string                      `json:"last_error" description:"The last failed command's error; empty after a success."`
	CreatedAt  time.Time                   `json:"created_at"`
	UpdatedAt  time.Time                   `json:"updated_at"`
}

// InstanceList is the data of GET get-instances.
type InstanceList struct {
	Instances []InstanceView `json:"instances"`
}

// view builds an instance's view; templateName is "" when it is gone.
func (uc *UseCase) view(inst model.Instance, templateName string) InstanceView {
	secrets := map[string]string{}
	for _, n := range inst.SecretNames {
		secrets[n] = command.EncryptedPlaceholder
	}
	params := inst.Params
	if params == nil {
		params = map[string]string{}
	}
	v := InstanceView{
		Key: inst.Key, ServerID: inst.ServerID, AppID: inst.AppID, TemplateID: inst.TemplateID,
		TemplateName: templateName, Revision: inst.Revision, Parameters: params, Secrets: secrets,
		State: string(inst.State), LastError: inst.LastError, CreatedAt: inst.CreatedAt, UpdatedAt: inst.UpdatedAt,
	}
	if uc.status != nil {
		for _, st := range uc.status.AppStatuses(inst.ServerID, time.Now()) {
			if st.AppID == inst.AppID {
				v.StatusCode = st.StatusCode
			}
		}
	}
	return v
}
```

`internal/domain/usecase/templates/instances.go`:

```go
package templates

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	appuc "winterflow/internal/domain/usecase/app"
	"winterflow/pkg/util"
)

// keyRE is an instance key: the caller's own id for the instance.
var keyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

// ValidateKey checks an instance key.
func ValidateKey(key string) error {
	if !keyRE.MatchString(key) {
		return fmt.Errorf("%w: key: 1-128 letters, digits and . _ : @ -, starting with a letter or digit", template.ErrInvalid)
	}
	return nil
}

// CreateInput is POST create-instance.
type CreateInput struct {
	ServerID   string
	Key        string
	TemplateID string
	// Revision 0 is the latest.
	Revision int64
	Params   map[string]string
	Secrets  map[string]string
	// Backups overrides the storage of template backup defaults by name.
	Backups map[string]string
}

// CreateInstance creates the instance, or replays a create the key already
// made: the same request answers the created instance (nothing is
// dispatched) or dispatches a pending one's save again; a different
// request is model.ErrKeyInUse, a deleted instance's key
// model.ErrKeyRetired. The key is claimed only once the rendered app passes
// every save-app check.
func (uc *UseCase) CreateInstance(ctx context.Context, userID, orgID string, in CreateInput) (Outcome, error) {
	if err := ValidateKey(in.Key); err != nil {
		return Outcome{}, err
	}
	tpl, err := uc.repo.GetTemplate(ctx, orgID, in.TemplateID)
	if err != nil {
		return Outcome{}, err
	}
	revision := in.Revision
	if revision == 0 {
		revision = tpl.LatestRevision
	}
	_, body, err := uc.revision(ctx, tpl.ID, revision)
	if err != nil {
		return Outcome{}, err
	}
	values, err := template.Resolve(body, template.Stored{}, template.Given{Params: in.Params, Secrets: in.Secrets})
	if err != nil {
		return Outcome{}, err
	}
	backups, err := template.CreateBackups(body, in.Backups)
	if err != nil {
		return Outcome{}, err
	}
	hash := template.RequestHash(template.HashInput{
		ServerID: in.ServerID, TemplateID: tpl.ID, Revision: in.Revision,
		Params: in.Params, SecretNames: sortedNames(in.Secrets), Backups: in.Backups,
	})
	render := func(appID string) (appuc.CheckedSave, error) {
		app, payload, err := template.Render(body, template.Input{
			AppID: appID, Values: values, Backups: backups,
			Marker: &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Revision: revision, Key: in.Key},
		})
		if err != nil {
			return appuc.CheckedSave{}, err
		}
		return uc.apps.CheckSave(ctx, in.ServerID, app, payload, false)
	}

	existing, err := uc.repo.GetInstance(ctx, orgID, in.Key)
	switch {
	case err == nil:
		return uc.replay(ctx, userID, existing, tpl.Name, hash, revision, values, render)
	case !errors.Is(err, model.ErrNotFound):
		return Outcome{}, err
	}
	checked, err := render(util.GenerateID())
	if err != nil {
		return Outcome{}, err
	}
	inst, inserted, err := uc.repo.ClaimInstance(ctx, model.Instance{
		ID: util.GenerateID(), OrgID: orgID, Key: in.Key, AppID: checked.AppID(), ServerID: in.ServerID,
		TemplateID: tpl.ID, Revision: revision, Params: values.Stored.Params, SecretNames: values.Stored.SecretNames,
		RequestHash: hash,
	})
	if err != nil {
		return Outcome{}, err
	}
	if !inserted {
		// Another create claimed the key in between.
		return uc.replay(ctx, userID, inst, tpl.Name, hash, revision, values, render)
	}
	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
}

// replay answers a create whose key is taken.
func (uc *UseCase) replay(ctx context.Context, userID string, inst model.Instance, templateName, hash string, revision int64, values template.Values, render func(string) (appuc.CheckedSave, error)) (Outcome, error) {
	switch {
	case inst.State == model.InstanceDeleted:
		return Outcome{}, model.ErrKeyRetired
	case inst.RequestHash != hash:
		return Outcome{}, model.ErrKeyInUse
	case inst.State == model.InstanceCreated:
		v := uc.view(inst, templateName)
		return Outcome{Instance: &v}, nil
	}
	checked, err := render(inst.AppID)
	if err != nil {
		return Outcome{}, err
	}
	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
}

// dispatchSave dispatches a rendered save; its confirmation records the
// revision and values (state created), a failure the error.
func (uc *UseCase) dispatchSave(ctx context.Context, userID string, inst model.Instance, checked appuc.CheckedSave, revision int64, values template.Values) (Outcome, error) {
	observer, done := uc.observe(func(ctx context.Context, res port.CommandResult) error {
		if !res.Success {
			return uc.repo.SetError(ctx, inst.ID, res.Error)
		}
		return uc.repo.MarkSaved(ctx, inst.ID, revision, values.Stored.Params, values.Stored.SecretNames)
	})
	id, err := uc.apps.DispatchSave(ctx, userID, checked, observer)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RequestID: id, Done: done, Final: uc.reread(inst)}, nil
}

// reread is an Outcome's Final for an instance that keeps its key.
func (uc *UseCase) reread(inst model.Instance) func(ctx context.Context) (InstanceView, error) {
	return func(ctx context.Context) (InstanceView, error) {
		return uc.GetInstance(ctx, inst.OrgID, inst.ServerID, inst.Key)
	}
}

// GetInstance returns the instance with key on serverID (a deleted one
// too: its key stays retired).
func (uc *UseCase) GetInstance(ctx context.Context, orgID, serverID, key string) (InstanceView, error) {
	inst, err := uc.repo.GetInstance(ctx, orgID, key)
	if err != nil {
		return InstanceView{}, err
	}
	if inst.ServerID != serverID {
		return InstanceView{}, model.ErrInstanceNotFound
	}
	return uc.view(inst, uc.templateName(ctx, orgID, inst.TemplateID)), nil
}

// ListInstances lists the org's pending and created instances, narrowed
// to a server and/or a template when given (another org's template is
// model.ErrTemplateNotFound).
func (uc *UseCase) ListInstances(ctx context.Context, orgID, serverID, templateID string) (InstanceList, error) {
	if templateID != "" {
		if _, err := uc.repo.GetTemplate(ctx, orgID, templateID); err != nil {
			return InstanceList{}, err
		}
	}
	names := map[string]string{}
	tpls, err := uc.repo.ListTemplates(ctx, orgID)
	if err != nil {
		return InstanceList{}, err
	}
	for _, t := range tpls {
		names[t.ID] = t.Name
	}
	all, err := uc.repo.ListInstances(ctx, orgID)
	if err != nil {
		return InstanceList{}, err
	}
	out := InstanceList{Instances: []InstanceView{}}
	for _, inst := range all {
		if (serverID != "" && inst.ServerID != serverID) || (templateID != "" && inst.TemplateID != templateID) {
			continue
		}
		out.Instances = append(out.Instances, uc.view(inst, names[inst.TemplateID]))
	}
	return out, nil
}

// templateName is the template's name, "" when it is gone or unreadable.
func (uc *UseCase) templateName(ctx context.Context, orgID, id string) string {
	if id == "" {
		return ""
	}
	t, err := uc.repo.GetTemplate(ctx, orgID, id)
	if err != nil {
		return ""
	}
	return t.Name
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

Run: `go test -race -count=1 ./internal/domain/usecase/templates/`
Expected: `ok`.

- [ ] **Step 4: Write the failing handler tests**

`internal/app/web/handler/templates/respond_test.go`:

```go
package templates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/port"
	tuc "winterflow/internal/domain/usecase/templates"
)

func TestAwait(t *testing.T) {
	done := make(chan port.CommandResult, 1)
	done <- port.CommandResult{Success: true}
	if res, ok := await(context.Background(), done, time.Minute); !ok || !res.Success {
		t.Fatalf("delivered = %+v, %v", res, ok)
	}
	start := time.Now()
	if _, ok := await(context.Background(), make(chan port.CommandResult), 20*time.Millisecond); ok || time.Since(start) > 5*time.Second {
		t.Fatal("the bound did not end the wait")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := await(ctx, make(chan port.CommandResult), time.Minute); ok {
		t.Fatal("a gone client did not end the wait")
	}
}

func outcome() (tuc.Outcome, chan port.CommandResult) {
	done := make(chan port.CommandResult, 1)
	final := func(context.Context) (tuc.InstanceView, error) {
		return tuc.InstanceView{Key: "k", State: "created"}, nil
	}
	return tuc.Outcome{RequestID: "req-1", Done: done, Final: final}, done
}

func TestRespond(t *testing.T) {
	h := &Handler{log: testLog, waitBound: 20 * time.Millisecond}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	answer := func(r *http.Request, out tuc.Outcome, wait bool) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.respond(w, r, out, wait, "dispatched")
		return w
	}

	out, _ := outcome()
	if w := answer(r, out, false); w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"request_id":"req-1"`) {
		t.Fatalf("no wait = %d %s", w.Code, w.Body)
	}
	out, done := outcome()
	done <- port.CommandResult{Success: true, Payload: []byte(`{"warnings":["w1"]}`)}
	if w := answer(r, out, true); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"created"`) ||
		!strings.Contains(w.Body.String(), `"warnings":["w1"]`) || !strings.Contains(w.Body.String(), `"request_id":"req-1"`) {
		t.Fatalf("waited success = %d %s", w.Code, w.Body)
	}
	out, done = outcome()
	done <- port.CommandResult{Error: "pull failed"}
	if w := answer(r, out, true); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "pull failed") {
		t.Fatalf("waited failure = %d %s", w.Code, w.Body)
	}
	out, _ = outcome()
	if w := answer(r, out, true); w.Code != http.StatusAccepted {
		t.Fatalf("past the bound = %d %s", w.Code, w.Body)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, _ = outcome()
	if w := answer(r.WithContext(ctx), out, true); w.Body.Len() != 0 {
		t.Fatalf("answered a gone client: %d %s", w.Code, w.Body)
	}
	if w := answer(r, tuc.Outcome{Instance: &tuc.InstanceView{Key: "k"}}, true); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"request_id":""`) {
		t.Fatalf("nothing dispatched = %d %s", w.Code, w.Body)
	}
}

// Instance routes need server_id (400) owned by the caller's org (403,
// and without a checker every server is refused), a key, and a canonical
// template id.
func TestInstanceRoutesFailClosed(t *testing.T) {
	h := NewHandler(&Deps{Logger: testLog})
	h.users = orgs{}
	cases := map[string]struct {
		w    *httptest.ResponseRecorder
		code int
	}{
		"get, no checker":        {serve(h.GetInstance, http.MethodGet, "/?server_id=s&key=k", ""), http.StatusForbidden},
		"get, no server":         {serve(h.GetInstance, http.MethodGet, "/?key=k", ""), http.StatusBadRequest},
		"list, no checker":       {serve(h.GetInstances, http.MethodGet, "/?server_id=s", ""), http.StatusForbidden},
		"create, no checker":     {serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s","key":"k","template_id":"x"}`), http.StatusForbidden},
		"create, malformed body": {serve(h.CreateInstance, http.MethodPost, "/", `{`), http.StatusBadRequest},
	}
	for name, tc := range cases {
		if tc.w.Code != tc.code {
			t.Errorf("%s = %d, want %d: %s", name, tc.w.Code, tc.code, tc.w.Body)
		}
	}
	h.servers = allServers{}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"create, no key":           serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s","template_id":"x"}`),
		"create, malformed id":     serve(h.CreateInstance, http.MethodPost, "/", `{"server_id":"s","key":"k","template_id":"x"}`),
		"get, no key":              serve(h.GetInstance, http.MethodGet, "/?server_id=s", ""),
		"list, malformed template": serve(h.GetInstances, http.MethodGet, "/?template_id=x", ""),
	} {
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s", name, w.Code, w.Body)
		}
	}
}

// allServers lets the caller act on any server.
type allServers struct{}

func (allServers) UserOwnsServer(context.Context, string, string) (bool, error) { return true, nil }
```

Run: `go test ./internal/app/web/handler/templates/`
Expected: FAIL to compile (`undefined: await`, `h.respond undefined`, `h.GetInstance undefined`).

- [ ] **Step 5: Write the wait, the handlers and the routes**

`internal/app/web/handler/templates/respond.go`:

```go
package templates

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/port"
	tuc "winterflow/internal/domain/usecase/templates"
)

// InstanceResult is the data of an instance call that waited for its
// outcome (or had nothing to wait for).
type InstanceResult struct {
	Instance tuc.InstanceView `json:"instance"`
	// RequestID names the command that ran ("" when none was needed).
	RequestID string `json:"request_id"`
	// Warnings are the agent's non-fatal warnings (backup settings
	// dropped, an ingress problem).
	Warnings []string `json:"warnings"`
}

// acceptedData is the 202 body.
type acceptedData struct {
	RequestID string `json:"request_id"`
}

// await blocks until done delivers, bound passes, or ctx ends (the client
// went away). ok reports a delivered result.
func await(ctx context.Context, done <-chan port.CommandResult, bound time.Duration) (port.CommandResult, bool) {
	t := time.NewTimer(bound)
	defer t.Stop()
	select {
	case res := <-done:
		return res, true
	case <-t.C:
	case <-ctx.Done():
	}
	return port.CommandResult{}, false
}

// respond answers an instance call: 200 with the instance when nothing was
// dispatched; without wait 202; with wait the outcome (200 with the
// instance re-read after the result was recorded, 502 with the agent's
// error), or 202 once the bound passes.
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, out tuc.Outcome, wait bool, msg string) {
	if out.Instance != nil {
		webutil.Success(w, msg, InstanceResult{Instance: *out.Instance, Warnings: []string{}})
		return
	}
	if !wait {
		webutil.Accepted(w, msg, acceptedData{RequestID: out.RequestID})
		return
	}
	res, ok := await(r.Context(), out.Done, h.waitBound)
	switch {
	case !ok && r.Context().Err() != nil:
		return // the client is gone: there is no one to answer
	case !ok:
		webutil.Accepted(w, msg, acceptedData{RequestID: out.RequestID})
		return
	case !res.Success:
		webutil.ErrorStatus(w, http.StatusBadGateway, res.Error)
		return
	}
	view, err := out.Final(r.Context())
	if err != nil {
		h.fail(w, err, "read the instance")
		return
	}
	var agent struct {
		Warnings []string `json:"warnings"`
	}
	_ = json.Unmarshal(res.Payload, &agent)
	if agent.Warnings == nil {
		agent.Warnings = []string{}
	}
	webutil.Success(w, msg, InstanceResult{Instance: view, RequestID: out.RequestID, Warnings: agent.Warnings})
}
```

`internal/app/web/handler/templates/instances.go`:

```go
package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	tuc "winterflow/internal/domain/usecase/templates"
)

// CreateInstanceRequest is the body of POST create-instance.
type CreateInstanceRequest struct {
	ServerID   string            `json:"server_id" required:"true"`
	Key        string            `json:"key" required:"true" description:"The caller's id for the instance, unique in the org among instances not unlinked; a retried create with the same key and values makes no second app."`
	TemplateID string            `json:"template_id" required:"true"`
	Revision   int64             `json:"revision,omitempty" description:"Absent or 0: the latest."`
	Parameters map[string]string `json:"parameters,omitempty" description:"Plain parameter values by name."`
	Secrets    map[string]string `json:"secrets,omitempty" description:"Secret parameter values by name, ECIES-encrypted with the server's public key."`
	Backups    map[string]string `json:"backups,omitempty" description:"Backup name -> storage name, replacing the template default's storage."`
	Wait       bool              `json:"wait,omitempty" description:"Hold the request for the outcome (at most 10 minutes)."`
}

// InstanceQuery is the query of GET get-instance.
type InstanceQuery struct {
	ServerID string `query:"server_id" required:"true"`
	Key      string `query:"key" required:"true"`
}

// InstancesQuery is the query of GET get-instances.
type InstancesQuery struct {
	ServerID   string `query:"server_id" description:"Only this server's instances."`
	TemplateID string `query:"template_id" description:"Only this template's instances."`
}

// requireKey answers 400 without a key.
func requireKey(w http.ResponseWriter, key string) bool {
	if key == "" {
		webutil.Error(w, "key is required", nil)
		return false
	}
	return true
}

func (h *Handler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[CreateInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	if !requireID(w, "template_id", req.TemplateID) {
		return
	}
	out, err := h.uc.CreateInstance(r.Context(), userID, orgID, tuc.CreateInput{
		ServerID: req.ServerID, Key: req.Key, TemplateID: req.TemplateID, Revision: req.Revision,
		Params: req.Parameters, Secrets: req.Secrets, Backups: req.Backups,
	})
	if err != nil {
		h.fail(w, err, "create the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "instance save dispatched")
}

func (h *Handler) GetInstance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	_, orgID, ok := h.serverCaller(w, r, q.Get("server_id"))
	if !ok || !requireKey(w, q.Get("key")) {
		return
	}
	out, err := h.uc.GetInstance(r.Context(), orgID, q.Get("server_id"), q.Get("key"))
	if err != nil {
		h.fail(w, err, "read the instance")
		return
	}
	webutil.Success(w, "", out)
}

func (h *Handler) GetInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	serverID := q.Get("server_id")
	var orgID string
	if serverID != "" {
		_, org, ok := h.serverCaller(w, r, serverID)
		if !ok {
			return
		}
		orgID = org
	} else {
		_, org, ok := h.callerOrg(w, r)
		if !ok {
			return
		}
		orgID = org
	}
	templateID := q.Get("template_id")
	if templateID != "" && !requireID(w, "template_id", templateID) {
		return
	}
	out, err := h.uc.ListInstances(r.Context(), orgID, serverID, templateID)
	if err != nil {
		h.fail(w, err, "list instances")
		return
	}
	webutil.Success(w, "", out)
}
```

`internal/app/web/handler/templates/instance_routes.go` (Tasks 8 and 9 add entries after create-instance):

```go
package templates

import (
	"net/http"

	"winterflow/internal/app/web/api"
	"winterflow/internal/domain/command"
	tuc "winterflow/internal/domain/usecase/templates"
)

// waits documents how every instance command answers.
const waits = "Answers 202 with a request_id (the outcome arrives over SSE), or with wait:true the outcome: " +
	"200 with the instance, 502 with the agent's error, or 202 once 10 minutes pass (read get-instance)."

// instanceRoutes are the server-addressed instance routes.
func (h *Handler) instanceRoutes() []api.Route {
	instance := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusBadGateway}
	read := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}
	return []api.Route{
		{
			Method: http.MethodPost, Path: "/api/v1/instance/create-instance", Handler: h.CreateInstance, Auth: api.User,
			Summary: "Create an app from a template revision, or replay a create with the same key", Tag: "instances",
			Description: "The same key with the same request makes no second app: a created instance answers 200, " +
				"a pending one is saved again. A different request is 409. " + waits,
			Request: CreateInstanceRequest{}, Accepted: true, Result: command.SaveAppResponse{}, Response: InstanceResult{},
			Errors: instance,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/instance/get-instance", Handler: h.GetInstance, Auth: api.User,
			Summary: "An instance: template, revision, values (secrets masked), state, status, last error", Tag: "instances",
			Query: InstanceQuery{}, Response: tuc.InstanceView{}, Errors: read,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/instance/get-instances", Handler: h.GetInstances, Auth: api.User,
			Summary: "The org's pending and created instances", Tag: "instances",
			Query: InstancesQuery{}, Response: tuc.InstanceList{}, Errors: read,
		},
	}
}
```

In `internal/app/web/handler/templates/routes.go` replace

```go
func (h *Handler) Routes(api.Guards) []api.Route {
	return h.templateRoutes()
}
```

with

```go
func (h *Handler) Routes(api.Guards) []api.Route {
	return append(h.templateRoutes(), h.instanceRoutes()...)
}
```

In `internal/app/web/openapi_test.go`, `TestOpenAPIMatchesWire`, after the `"conflict"` case add:

```go
		{"agent failed", http.MethodPost, "/api/v1/instance/create-instance", 502, "application/json", func(w http.ResponseWriter) {
			util.ErrorStatus(w, http.StatusBadGateway, "pull access denied")
		}},
```

Run: `go test -count=1 ./internal/app/web/handler/templates/`
Expected: `ok`.

- [ ] **Step 6: Write the contract tests and probes**

`internal/app/web/contract/instances_test.go` (Tasks 8 and 9 append to it):

```go
package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	htpl "winterflow/internal/app/web/handler/templates"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	tuc "winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/transport/bus"
)

// instanceOf reads get-instance; no answer may carry a secret.
func instanceOf(t *testing.T, c *client, serverID, key string) tuc.InstanceView {
	t.Helper()
	var v tuc.InstanceView
	r := c.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {serverID}, "key": {key}}).expect(t, http.StatusOK)
	noSecrets(t, string(r.Body))
	r.data(t, &v)
	return v
}

// instanceEnv is an owner whose server covers *.example.test and has the
// storage "disk", with template "shop" (shopBody) saved.
type instanceEnv struct {
	e     *env
	owner *client
	sid   string
	tplID string
}

func newInstanceEnv(t *testing.T) instanceEnv {
	t.Helper()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	registerDomain(t, owner, sid, "*.example.test", model.DomainTLSOff)
	addStorage(t, owner, sid, model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb"}})
	saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{Name: "shop", Body: shopBody(shopCompose)})
	return instanceEnv{e: e, owner: owner, sid: sid, tplID: saved.ID}
}

// create is a waited create of key with NAME and PORT.
func (ie instanceEnv) create(key, name, port string) htpl.CreateInstanceRequest {
	return htpl.CreateInstanceRequest{
		ServerID: ie.sid, Key: key, TemplateID: ie.tplID, Parameters: map[string]string{"NAME": name, "PORT": port},
		Secrets: map[string]string{"SECRET_KEY": "CT-secret"}, Wait: true,
	}
}

// created creates key and returns the waited result.
func (ie instanceEnv) created(t *testing.T, key, name, port string) htpl.InstanceResult {
	t.Helper()
	var res htpl.InstanceResult
	r := ie.owner.post(t, "/api/v1/instance/create-instance", ie.create(key, name, port)).expect(t, http.StatusOK)
	noSecrets(t, string(r.Body))
	r.data(t, &res)
	return res
}

// lastSave is the last app.save the fake agent received.
func lastSave(t *testing.T, e *env) command.SaveAppRequest {
	t.Helper()
	saves := e.agent.seen(command.TypeAppSave)
	if len(saves) == 0 {
		t.Fatal("no app.save reached the agent")
	}
	return decode[command.SaveAppRequest](saves[len(saves)-1].Payload)
}

func TestInstanceCreate(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid

	res := ie.created(t, "tenant-1", "acme", "9001")
	appID := res.Instance.AppID
	t.Run("create with wait answers the instance", func(t *testing.T) {
		if res.Instance.State != "created" || res.Instance.Revision != 1 || res.Instance.Secrets["SECRET_KEY"] != command.EncryptedPlaceholder ||
			res.Instance.Parameters["NAME"] != "acme" || res.Instance.TemplateName != "shop" || res.RequestID == "" {
			t.Fatalf("result = %+v", res)
		}
		req := lastSave(t, e)
		var cfg map[string]json.RawMessage
		_ = json.Unmarshal(req.App.Config, &cfg)
		if req.App.AppID != appID || string(cfg["template_id"]) != `"`+ie.tplID+`"` ||
			string(cfg["backups"]) != `[{"name":"db","enabled":true,"storage":"disk"}]` {
			t.Fatalf("dispatched config = %s", req.App.Config)
		}
		apps := listApps(t, owner, sid)
		if len(apps) != 1 || apps[0].ID != appID || apps[0].Name != "shop-acme" || apps[0].TemplateID != ie.tplID || len(apps[0].Domains) != 1 {
			t.Fatalf("apps = %+v", apps)
		}
	})
	t.Run("get-instance shows the app's live status", func(t *testing.T) {
		body, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{{AppID: appID, StatusCode: command.ContainerStatusActive, Containers: []command.ContainerStatus{}}}})
		e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventAppsStatus, Payload: body})
		eventually(t, func() bool { return instanceOf(t, owner, sid, "tenant-1").StatusCode == command.ContainerStatusActive })
	})
	t.Run("a replay makes no second app; other values are a conflict", func(t *testing.T) {
		n := len(e.agent.seen(command.TypeAppSave))
		var again htpl.InstanceResult
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "acme", "9001")).expect(t, http.StatusOK).data(t, &again)
		if again.Instance.AppID != appID || again.RequestID != "" || len(e.agent.seen(command.TypeAppSave)) != n {
			t.Fatalf("replay = %+v", again)
		}
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "acme", "9002")).expect(t, http.StatusConflict)
	})
	t.Run("a host another app holds is refused before the key is claimed", func(t *testing.T) {
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-x", "acme", "9009")).expect(t, http.StatusBadRequest)
		owner.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {sid}, "key": {"tenant-x"}}).expect(t, http.StatusNotFound)
	})
	t.Run("without wait the result arrives over SSE", func(t *testing.T) {
		in := ie.create("tenant-2", "bravo", "9002")
		in.Wait = false
		var saved command.SaveAppResponse
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/create-instance", Body: in}, &saved)
		if got := instanceOf(t, owner, sid, "tenant-2"); got.AppID != saved.AppID || got.State != "created" {
			t.Fatalf("tenant-2 = %+v (saved %+v)", got, saved)
		}
	})
	t.Run("a failed deploy answers 502; the same request retries it", func(t *testing.T) {
		e.agent.fail(command.TypeAppSave, "pull access denied")
		r := owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie", "9003")).expect(t, http.StatusBadGateway)
		if !strings.Contains(string(r.Body), "pull access denied") {
			t.Fatalf("502 body = %s", r.Body)
		}
		if got := instanceOf(t, owner, sid, "tenant-3"); got.State != "pending" || got.LastError != "pull access denied" {
			t.Fatalf("after a failure = %+v", got)
		}
		e.agent.on(command.TypeAppSave, defaultScript(command.TypeAppSave))
		var retried htpl.InstanceResult
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-3", "charlie", "9003")).expect(t, http.StatusOK).data(t, &retried)
		if retried.Instance.State != "created" || retried.Instance.LastError != "" {
			t.Fatalf("retried = %+v", retried.Instance)
		}
	})
	t.Run("list by template and by server", func(t *testing.T) {
		var list tuc.InstanceList
		owner.get(t, "/api/v1/instance/get-instances", url.Values{"template_id": {ie.tplID}}).expect(t, http.StatusOK).data(t, &list)
		if len(list.Instances) != 3 || list.Instances[0].Key != "tenant-1" {
			t.Fatalf("by template = %+v", list)
		}
		owner.get(t, "/api/v1/instance/get-instances", url.Values{"server_id": {sid}}).expect(t, http.StatusOK).data(t, &list)
		if len(list.Instances) != 3 {
			t.Fatalf("by server = %+v", list)
		}
		var tpls tuc.TemplateList
		owner.get(t, "/api/v1/template/get-templates", nil).expect(t, http.StatusOK).data(t, &tpls)
		if tpls.Templates[0].Instances != 3 {
			t.Fatalf("templates = %+v", tpls)
		}
	})
	t.Run("parameters are checked", func(t *testing.T) {
		bad := ie.create("tenant-4", "UPPER", "9004")
		r := owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "parameters.NAME") {
			t.Fatalf("body = %s", r.Body)
		}
		bad = ie.create("tenant-4", "delta", "9004")
		bad.Secrets = nil
		owner.post(t, "/api/v1/instance/create-instance", bad).expect(t, http.StatusBadRequest)
	})
}

// Another org's template and keys read as unknown.
func TestInstancesOfAnotherOrgAreNotFound(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	ie.created(t, "tenant-1", "acme", "9001")
	mallory, foreign := ie.e.foreignClient(t)
	in := ie.create("k", "acme", "9001")
	in.ServerID = foreign
	mallory.post(t, "/api/v1/instance/create-instance", in).expect(t, http.StatusNotFound)
	mallory.get(t, "/api/v1/instance/get-instances", url.Values{"template_id": {ie.tplID}}).expect(t, http.StatusNotFound)
	mallory.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {foreign}, "key": {"tenant-1"}}).expect(t, http.StatusNotFound)
	var list tuc.InstanceList
	mallory.get(t, "/api/v1/instance/get-instances", nil).expect(t, http.StatusOK).data(t, &list)
	if len(list.Instances) != 0 {
		t.Fatalf("mallory sees %+v", list)
	}
	ie.owner.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {ie.sid}, "key": {"nope"}}).expect(t, http.StatusNotFound)
}
```

In `internal/app/web/contract/matrix_test.go`, add to `probes` after the `"POST /api/v1/template/delete-template"` entry:

```go
	// The template and key do not exist, so the own-server control answers
	// 404, not 403.
	"POST /api/v1/instance/create-instance": {valid: func(s string) call {
		return post("/api/v1/instance/create-instance", map[string]any{"server_id": s, "key": "k", "template_id": anApp})
	}, malformed: bad("POST", "/api/v1/instance/create-instance")},
	"GET /api/v1/instance/get-instance": {valid: func(s string) call {
		return get("/api/v1/instance/get-instance", url.Values{"server_id": {s}, "key": {"k"}})
	}, malformed: bad("GET", "/api/v1/instance/get-instance")},
	"GET /api/v1/instance/get-instances": {valid: func(s string) call { return get("/api/v1/instance/get-instances", sq(s)) },
		malformed: call{Method: http.MethodGet, Path: "/api/v1/instance/get-instances", Query: url.Values{"template_id": {"not-an-id"}}}},
```

- [ ] **Step 7: Regenerate, then run everything that reads the table**

Run: `make openapi && go test -count=1 ./internal/app/web -run TestRoutesGolden -update`
Expected: three `/api/v1/instance/*` operations; `create-instance` documents `200` (`InstanceResult`), `202`, `400`, `401`, `403`, `404`, `409`, `502` and `x-operation-result: SaveAppResponse`.

Run: `go vet ./... && go test -count=1 ./internal/app/web/... && WF_CONTRACT_GUARD=require go test -count=1 ./internal/app/web/contract/`
Expected: `ok` everywhere.

- [ ] **Step 8: Commit**

```bash
git add internal/app/web/api internal/domain/usecase/templates internal/app/web/handler/templates \
  internal/app/web/openapi_test.go internal/app/web/contract/instances_test.go internal/app/web/contract/matrix_test.go \
  api/openapi.yaml internal/app/web/testdata/routes.golden
git commit -m "feat(instances): create with key replay and wait; read instances

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Update and upgrade instances

**Files:**
- Create: `internal/domain/usecase/templates/update.go`, `internal/domain/usecase/templates/update_test.go`
- Create: `internal/app/web/handler/templates/update.go`; Modify: `internal/app/web/handler/templates/instance_routes.go`
- Modify: `internal/app/web/contract/instances_test.go` (append), `internal/app/web/contract/matrix_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: Task 3 `template.NewBackupDefaults`; Task 7 `dispatchSave`, `Outcome`, `respond`, `requireKey`, `serverCaller`, contract `instanceEnv`/`lastSave`/`instanceOf`.
- Produces:
  - `tuc.UpdateInput{ServerID, Key string; Revision int64; Params, Secrets map[string]string}`; `(*UseCase).UpdateInstance(ctx, userID, orgID string, in UpdateInput) (Outcome, error)` (`Revision` 0 or equal to the current: an update; otherwise an upgrade with `Marker.BackupDefaults`); unexported `(*UseCase).live(ctx, orgID, serverID, key) (model.Instance, error)` (Task 9 uses it).
  - Handler: `UpdateInstanceRequest{ServerID, Key string; Parameters, Secrets map[string]string; Wait bool}`, `UpgradeInstanceRequest{ServerID, Key string; Revision int64; Parameters, Secrets map[string]string; Wait bool}`, `(*Handler).UpdateInstance`, `(*Handler).UpgradeInstance`.
  - Routes: `POST /api/v1/instance/update-instance`, `POST /api/v1/instance/upgrade-instance`.

- [ ] **Step 1: Write the failing usecase tests**

`internal/domain/usecase/templates/update_test.go`:

```go
package templates_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

// created is fixture f with instance k1 created (NAME=acme).
func created(t *testing.T, f fixture) {
	t.Helper()
	out, err := f.uc.CreateInstance(dbtest.Ctx(t), "u1", org1, f.create(map[string]string{"NAME": "acme"}))
	if err != nil {
		t.Fatal(err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
}

func TestUpdateKeepsValuesAndSecrets(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "nope"}); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv2, Key: "k1"}); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("on another server: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"NOPE": "x"}}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("unknown parameter: %v", err)
	}
	out, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Params: map[string]string{"VERSION": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	for _, v := range req.App.Variables {
		if v.Name == "KEY" && (string(v.Content) != command.EncryptedPlaceholder || !v.Encrypted) {
			t.Fatalf("an update re-sent the secret: %+v", v)
		}
	}
	if strings.Contains(string(req.App.Config), `"backups"`) {
		t.Fatalf("an update replaced the backups list: %s", req.App.Config)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.Parameters["VERSION"] != "2" || v.Parameters["NAME"] != "acme" || v.Revision != 1 {
		t.Fatalf("after update = %+v", v)
	}
}

func TestUpgradeSendsNewBackupDefaults(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)

	b := body()
	b.Files["compose.yaml"] = strings.Replace(compose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1)
	b.Backups["media"] = template.BackupDefault{Every: "6h"}
	b.Parameters = append(b.Parameters, template.Parameter{Name: "REGION", Required: true})
	if _, err := f.uc.SaveTemplate(ctx, "u1", org1, templates.SaveInput{ID: f.tpl.ID, Name: "shop", Body: b, BaseRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2}); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("a newly required value missing: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 5}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("an unknown revision: %v", err)
	}
	up, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1", Revision: 2, Params: map[string]string{"REGION": "eu"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Backups  json.RawMessage `json:"backups"`
		Instance template.Marker `json:"instance"`
	}
	_ = json.Unmarshal(f.disp.last(t).Payload.(command.SaveAppRequest).App.Config, &cfg)
	if cfg.Backups != nil || cfg.Instance.Revision != 2 || len(cfg.Instance.BackupDefaults) != 1 || cfg.Instance.BackupDefaults[0].Name != "media" {
		t.Fatalf("upgrade config: backups %s marker %+v", cfg.Backups, cfg.Instance)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, up.Done)
	if v, _ := up.Final(ctx); v.Revision != 2 || v.Parameters["REGION"] != "eu" {
		t.Fatalf("upgraded = %+v", v)
	}
	list, err := f.uc.ListTemplates(ctx, org1)
	if err != nil || list.Templates[0].Instances != 1 || list.Templates[0].Behind != 0 {
		t.Fatalf("templates = %+v, %v", list, err)
	}
}
```

Run: `go test ./internal/domain/usecase/templates/`
Expected: FAIL to compile (`undefined: templates.UpdateInput`).

- [ ] **Step 2: Write update and upgrade**

`internal/domain/usecase/templates/update.go`:

```go
package templates

import (
	"context"

	"winterflow/internal/domain/model"
	"winterflow/internal/domain/template"
)

// UpdateInput is POST update-instance and (with Revision) upgrade-instance.
type UpdateInput struct {
	ServerID string
	Key      string
	Revision int64
	Params   map[string]string
	Secrets  map[string]string
}

// live returns the org's instance with key on serverID: model.ErrInstanceNotFound
// when there is none (or it is on another server), model.ErrKeyRetired when
// it was deleted.
func (uc *UseCase) live(ctx context.Context, orgID, serverID, key string) (model.Instance, error) {
	inst, err := uc.repo.GetInstance(ctx, orgID, key)
	if err != nil {
		return model.Instance{}, err
	}
	if inst.ServerID != serverID {
		return model.Instance{}, model.ErrInstanceNotFound
	}
	if inst.State == model.InstanceDeleted {
		return inst, model.ErrKeyRetired
	}
	return inst, nil
}

// UpdateInstance re-renders the instance's revision with new values (merged
// over the stored ones) and redeploys; with in.Revision set it moves to
// that revision (an upgrade), adding the backup defaults of backups the
// new revision brings.
func (uc *UseCase) UpdateInstance(ctx context.Context, userID, orgID string, in UpdateInput) (Outcome, error) {
	inst, err := uc.live(ctx, orgID, in.ServerID, in.Key)
	if err != nil {
		return Outcome{}, err
	}
	tpl, err := uc.repo.GetTemplate(ctx, orgID, inst.TemplateID)
	if err != nil {
		return Outcome{}, err
	}
	_, current, err := uc.revision(ctx, tpl.ID, inst.Revision)
	if err != nil {
		return Outcome{}, err
	}
	revision, body := inst.Revision, current
	marker := &template.Marker{TemplateID: tpl.ID, TemplateName: tpl.Name, Key: inst.Key}
	if in.Revision != 0 && in.Revision != inst.Revision {
		if _, body, err = uc.revision(ctx, tpl.ID, in.Revision); err != nil {
			return Outcome{}, err
		}
		revision = in.Revision
		if marker.BackupDefaults, err = template.NewBackupDefaults(current, body); err != nil {
			return Outcome{}, err
		}
	}
	marker.Revision = revision
	values, err := template.Resolve(body, template.Stored{Params: inst.Params, SecretNames: inst.SecretNames},
		template.Given{Params: in.Params, Secrets: in.Secrets})
	if err != nil {
		return Outcome{}, err
	}
	app, payload, err := template.Render(body, template.Input{AppID: inst.AppID, Values: values, Marker: marker})
	if err != nil {
		return Outcome{}, err
	}
	checked, err := uc.apps.CheckSave(ctx, inst.ServerID, app, payload, false)
	if err != nil {
		return Outcome{}, err
	}
	return uc.dispatchSave(ctx, userID, inst, checked, revision, values)
}
```

Run: `go test -race -count=1 ./internal/domain/usecase/templates/`
Expected: `ok`.

- [ ] **Step 3: Write the handlers and routes**

`internal/app/web/handler/templates/update.go`:

```go
package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	tuc "winterflow/internal/domain/usecase/templates"
)

// UpdateInstanceRequest is the body of POST update-instance.
type UpdateInstanceRequest struct {
	ServerID   string            `json:"server_id" required:"true"`
	Key        string            `json:"key" required:"true"`
	Parameters map[string]string `json:"parameters,omitempty" description:"New plain values, merged over the stored ones."`
	Secrets    map[string]string `json:"secrets,omitempty" description:"New secret values; a secret left out keeps its value."`
	Wait       bool              `json:"wait,omitempty"`
}

// UpgradeInstanceRequest is the body of POST upgrade-instance.
type UpgradeInstanceRequest struct {
	ServerID   string            `json:"server_id" required:"true"`
	Key        string            `json:"key" required:"true"`
	Revision   int64             `json:"revision" required:"true" minimum:"1"`
	Parameters map[string]string `json:"parameters,omitempty" description:"Values for parameters the revision adds or changes."`
	Secrets    map[string]string `json:"secrets,omitempty"`
	Wait       bool              `json:"wait,omitempty"`
}

func (h *Handler) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[UpdateInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	out, err := h.uc.UpdateInstance(r.Context(), userID, orgID, tuc.UpdateInput{
		ServerID: req.ServerID, Key: req.Key, Params: req.Parameters, Secrets: req.Secrets,
	})
	if err != nil {
		h.fail(w, err, "update the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "instance save dispatched")
}

func (h *Handler) UpgradeInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[UpgradeInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	if req.Revision < 1 {
		webutil.Error(w, "revision is required", nil)
		return
	}
	out, err := h.uc.UpdateInstance(r.Context(), userID, orgID, tuc.UpdateInput{
		ServerID: req.ServerID, Key: req.Key, Revision: req.Revision, Params: req.Parameters, Secrets: req.Secrets,
	})
	if err != nil {
		h.fail(w, err, "upgrade the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "instance save dispatched")
}
```

In `internal/app/web/handler/templates/instance_routes.go`, insert after the create-instance entry (the entry ending `Errors: instance,\n\t\t},` whose `Path` is `/api/v1/instance/create-instance`):

```go
		{
			Method: http.MethodPost, Path: "/api/v1/instance/update-instance", Handler: h.UpdateInstance, Auth: api.User,
			Summary: "Redeploy an instance with new parameter values", Tag: "instances", Description: waits,
			Request: UpdateInstanceRequest{}, Accepted: true, Result: command.SaveAppResponse{}, Response: InstanceResult{},
			Errors: instance,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/instance/upgrade-instance", Handler: h.UpgradeInstance, Auth: api.User,
			Summary: "Move an instance to another template revision", Tag: "instances", Description: waits,
			Request: UpgradeInstanceRequest{}, Accepted: true, Result: command.SaveAppResponse{}, Response: InstanceResult{},
			Errors: instance,
		},
```

- [ ] **Step 4: Contract tests and probes**

Append to `internal/app/web/contract/instances_test.go`:

```go
func TestInstanceUpdateAndUpgrade(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	ie.created(t, "tenant-1", "acme", "9001")

	t.Run("update without wait answers 202 and keeps the secret", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/update-instance",
			Body: htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1", Parameters: map[string]string{"VERSION": "2.0"}}}, nil)
		got := instanceOf(t, owner, sid, "tenant-1")
		if got.Parameters["VERSION"] != "2.0" || got.Parameters["NAME"] != "acme" {
			t.Fatalf("after update = %+v", got)
		}
		for _, v := range lastSave(t, e).App.Variables {
			if v.Name == "SECRET_KEY" && string(v.Content) != command.EncryptedPlaceholder {
				t.Fatalf("an update re-sent the secret: %q", v.Content)
			}
		}
	})
	t.Run("update checks the values", func(t *testing.T) {
		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "tenant-1",
			Parameters: map[string]string{"NOPE": "1"}}).expect(t, http.StatusBadRequest)
		owner.post(t, "/api/v1/instance/update-instance", htpl.UpdateInstanceRequest{ServerID: sid, Key: "nope"}).
			expect(t, http.StatusNotFound)
	})
	t.Run("upgrade adds the defaults of new backups only", func(t *testing.T) {
		next := shopBody(strings.Replace(shopCompose, "path: shop.db }\n", "path: shop.db }\n    - { name: media, type: files, volume: data }\n", 1))
		next.Backups["media"] = template.BackupDefault{Enabled: true, Storage: "disk", Every: "6h"}
		next.Backups["db"] = template.BackupDefault{Enabled: true, Storage: "elsewhere"}
		if saved := saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: next, BaseRevision: 1}); saved.Revision != 2 {
			t.Fatalf("saved = %+v", saved)
		}
		var res htpl.InstanceResult
		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 2, Wait: true}).
			expect(t, http.StatusOK).data(t, &res)
		if res.Instance.Revision != 2 || res.Instance.Parameters["VERSION"] != "2.0" {
			t.Fatalf("upgraded = %+v", res.Instance)
		}
		var cfg struct {
			Backups  json.RawMessage `json:"backups"`
			Instance template.Marker `json:"instance"`
		}
		_ = json.Unmarshal(lastSave(t, e).App.Config, &cfg)
		if cfg.Backups != nil || len(cfg.Instance.BackupDefaults) != 1 || cfg.Instance.BackupDefaults[0].Name != "media" || cfg.Instance.Revision != 2 {
			t.Fatalf("upgrade config = %s", lastSave(t, e).App.Config)
		}
	})
	t.Run("an upgrade checks the new defaults' storages and revision", func(t *testing.T) {
		next := shopBody(strings.Replace(shopCompose, "path: shop.db }\n", "path: shop.db }\n    - { name: logs, type: files, volume: data }\n", 1))
		next.Backups["logs"] = template.BackupDefault{Enabled: true, Storage: "nowhere"}
		saveTemplate(t, owner, htpl.SaveTemplateRequest{ID: ie.tplID, Name: "shop", Body: next, BaseRevision: 2})
		r := owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 3}).
			expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "there is no storage") || !strings.Contains(string(r.Body), "nowhere") {
			t.Fatalf("body = %s", r.Body)
		}
		owner.post(t, "/api/v1/instance/upgrade-instance", htpl.UpgradeInstanceRequest{ServerID: sid, Key: "tenant-1", Revision: 9}).
			expect(t, http.StatusNotFound)
	})
}
```

and add `"winterflow/internal/domain/template"` to that file's imports.

In `internal/app/web/contract/matrix_test.go`, add after the `"POST /api/v1/instance/create-instance"` probe:

```go
	"POST /api/v1/instance/update-instance": {valid: func(s string) call {
		return post("/api/v1/instance/update-instance", map[string]any{"server_id": s, "key": "k"})
	}, malformed: bad("POST", "/api/v1/instance/update-instance")},
	"POST /api/v1/instance/upgrade-instance": {valid: func(s string) call {
		return post("/api/v1/instance/upgrade-instance", map[string]any{"server_id": s, "key": "k", "revision": 2})
	}, malformed: bad("POST", "/api/v1/instance/upgrade-instance")},
```

- [ ] **Step 5: Regenerate and run**

Run: `make openapi && go test -count=1 ./internal/app/web -run TestRoutesGolden -update`
Run: `go vet ./... && go test -count=1 ./internal/app/web/... && WF_CONTRACT_GUARD=require go test -count=1 ./internal/app/web/contract/`
Expected: `ok` everywhere.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/usecase/templates internal/app/web/handler/templates internal/app/web/contract \
  api/openapi.yaml internal/app/web/testdata/routes.golden
git commit -m "feat(instances): update values and upgrade to another revision

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Control, delete and unlink instances

**Files:**
- Create: `internal/domain/usecase/templates/lifecycle.go`, `internal/domain/usecase/templates/lifecycle_test.go`
- Create: `internal/app/web/handler/templates/lifecycle.go`; Modify: `internal/app/web/handler/templates/instance_routes.go`
- Modify: `internal/app/web/contract/instances_test.go` (append), `internal/app/web/contract/matrix_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: Task 5 `ControlAppObserved`, `DeleteAppObserved`, `CheckSave`, `DispatchSave`; Task 7 `observe`, `reread`, `view`, `templateName`; Task 8 `live`, test helper `created`.
- Produces:
  - `(*UseCase).ControlInstance(ctx, userID, orgID, serverID, key string, action command.ControlAction) (Outcome, error)`, `DeleteInstance(ctx, userID, orgID, serverID, key string, deleteVolumes, deleteBackups bool) (Outcome, error)`, `UnlinkInstance(ctx, userID, orgID, serverID, key string) (Outcome, error)`.
  - Handler: `ControlInstanceRequest{ServerID, Key string; Action command.ControlAction; Wait bool}`, `DeleteInstanceRequest{ServerID, Key string; DeleteVolumes, DeleteBackups, Wait bool}`, `InstanceKeyRequest{ServerID, Key string; Wait bool}`, `(*Handler).ControlInstance`, `DeleteInstance`, `UnlinkInstance`.
  - Routes: `POST /api/v1/instance/control-instance`, `POST /api/v1/instance/delete-instance`, `POST /api/v1/instance/unlink-instance`.

- [ ] **Step 1: Write the failing usecase tests**

`internal/domain/usecase/templates/lifecycle_test.go`:

```go
package templates_test

import (
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/usecase/templates"
	"winterflow/internal/infra/db/dbtest"
)

func TestControlInstance(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	created(t, f)
	if _, err := f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlUpdate); !errors.Is(err, model.ErrRejected) {
		t.Fatalf("update action: %v", err)
	}
	out, err := f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlStop)
	if err != nil || f.disp.last(t).Type != command.TypeAppControl {
		t.Fatalf("control = %+v, %v", out, err)
	}
	f.disp.reply(t, port.CommandResult{Error: "stop failed"})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.LastError != "stop failed" || v.State != "created" {
		t.Fatalf("after a failed stop = %+v", v)
	}
	out, _ = f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlStart)
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, out.Done)
	if v, _ := out.Final(ctx); v.LastError != "" {
		t.Fatalf("a success keeps the old error: %+v", v)
	}
}

func TestDeleteRetiresAndUnlinkFrees(t *testing.T) {
	f := newFixture(t, nil)
	ctx := dbtest.Ctx(t)
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"})); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.UnlinkInstance(ctx, "u1", org1, srv1, "k1"); !errors.Is(err, model.ErrInstanceNotCreated) {
		t.Fatalf("unlink of a pending instance: %v", err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})

	un, err := f.uc.UnlinkInstance(ctx, "u1", org1, srv1, "k1")
	if err != nil {
		t.Fatal(err)
	}
	req := f.disp.last(t).Payload.(command.SaveAppRequest)
	if strings.Contains(string(req.App.Config), `"instance"`) || strings.Contains(string(req.App.Config), `"template_id"`) {
		t.Fatalf("unlink config = %s", req.App.Config)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, un.Done)
	if v, err := un.Final(ctx); err != nil || v.State != "unlinked" {
		t.Fatalf("unlinked = %+v, %v", v, err)
	}
	if _, err := f.uc.GetInstance(ctx, org1, srv1, "k1"); !errors.Is(err, model.ErrInstanceNotFound) {
		t.Fatalf("after unlink: %v", err)
	}

	created(t, f) // the freed key makes a new instance
	del, err := f.uc.DeleteInstance(ctx, "u1", org1, srv1, "k1", true, false)
	if err != nil || f.disp.last(t).Type != command.TypeAppDelete {
		t.Fatalf("delete = %+v, %v", del, err)
	}
	f.disp.reply(t, port.CommandResult{Success: true})
	received(t, del.Done)
	if v, _ := del.Final(ctx); v.State != "deleted" {
		t.Fatalf("deleted = %+v", v)
	}
	n := f.disp.count()
	again, err := f.uc.DeleteInstance(ctx, "u1", org1, srv1, "k1", true, false)
	if err != nil || again.Instance == nil || again.Instance.State != "deleted" || f.disp.count() != n {
		t.Fatalf("delete again = %+v, %v", again, err)
	}
	if _, err := f.uc.CreateInstance(ctx, "u1", org1, f.create(map[string]string{"NAME": "acme"})); !errors.Is(err, model.ErrKeyRetired) {
		t.Fatalf("a retired key: %v", err)
	}
	if _, err := f.uc.UpdateInstance(ctx, "u1", org1, templates.UpdateInput{ServerID: srv1, Key: "k1"}); !errors.Is(err, model.ErrKeyRetired) {
		t.Fatalf("update of a deleted instance: %v", err)
	}
	if _, err := f.uc.ControlInstance(ctx, "u1", org1, srv1, "k1", command.ControlStart); !errors.Is(err, model.ErrKeyRetired) {
		t.Fatalf("control of a deleted instance: %v", err)
	}
}
```

Run: `go test ./internal/domain/usecase/templates/`
Expected: FAIL to compile (`f.uc.ControlInstance undefined`, ...).

- [ ] **Step 2: Write control, delete and unlink**

`internal/domain/usecase/templates/lifecycle.go`:

```go
package templates

import (
	"context"
	"errors"
	"fmt"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/domain/port"
	"winterflow/internal/domain/template"
)

// ControlInstance starts, stops or restarts the instance's app.
func (uc *UseCase) ControlInstance(ctx context.Context, userID, orgID, serverID, key string, action command.ControlAction) (Outcome, error) {
	switch action {
	case command.ControlStart, command.ControlStop, command.ControlRestart:
	default:
		return Outcome{}, fmt.Errorf("%w: action: one of start, stop, restart", template.ErrInvalid)
	}
	inst, err := uc.live(ctx, orgID, serverID, key)
	if err != nil {
		return Outcome{}, err
	}
	observer, done := uc.observe(func(ctx context.Context, res port.CommandResult) error {
		return uc.repo.SetError(ctx, inst.ID, res.Error)
	})
	id, err := uc.apps.ControlAppObserved(ctx, userID, serverID, inst.AppID, action, observer)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RequestID: id, Done: done, Final: uc.reread(inst)}, nil
}

// DeleteInstance deletes the instance's app (and, as asked, its volumes and
// backups); on success the key is retired. A deleted instance answers as
// it is.
func (uc *UseCase) DeleteInstance(ctx context.Context, userID, orgID, serverID, key string, deleteVolumes, deleteBackups bool) (Outcome, error) {
	inst, err := uc.live(ctx, orgID, serverID, key)
	if errors.Is(err, model.ErrKeyRetired) {
		v := uc.view(inst, uc.templateName(ctx, orgID, inst.TemplateID))
		return Outcome{Instance: &v}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	observer, done := uc.observe(func(ctx context.Context, res port.CommandResult) error {
		if !res.Success {
			return uc.repo.SetError(ctx, inst.ID, res.Error)
		}
		return uc.repo.SetState(ctx, inst.ID, model.InstanceDeleted)
	})
	id, err := uc.apps.DeleteAppObserved(ctx, userID, serverID, inst.AppID, deleteVolumes, deleteBackups, observer)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{RequestID: id, Done: done, Final: uc.reread(inst)}, nil
}

// UnlinkInstance saves the app as an ordinary app (no marker, no template
// id) at its current revision and values; on success the key is free.
func (uc *UseCase) UnlinkInstance(ctx context.Context, userID, orgID, serverID, key string) (Outcome, error) {
	inst, err := uc.live(ctx, orgID, serverID, key)
	if err != nil {
		return Outcome{}, err
	}
	if inst.State != model.InstanceCreated {
		return Outcome{}, model.ErrInstanceNotCreated
	}
	_, body, err := uc.revision(ctx, inst.TemplateID, inst.Revision)
	if err != nil {
		return Outcome{}, err
	}
	values, err := template.Resolve(body, template.Stored{Params: inst.Params, SecretNames: inst.SecretNames}, template.Given{})
	if err != nil {
		return Outcome{}, err
	}
	app, payload, err := template.Render(body, template.Input{AppID: inst.AppID, Values: values})
	if err != nil {
		return Outcome{}, err
	}
	checked, err := uc.apps.CheckSave(ctx, serverID, app, payload, false)
	if err != nil {
		return Outcome{}, err
	}
	observer, done := uc.observe(func(ctx context.Context, res port.CommandResult) error {
		if !res.Success {
			return uc.repo.SetError(ctx, inst.ID, res.Error)
		}
		return uc.repo.SetState(ctx, inst.ID, model.InstanceUnlinked)
	})
	id, err := uc.apps.DispatchSave(ctx, userID, checked, observer)
	if err != nil {
		return Outcome{}, err
	}
	name := uc.templateName(ctx, orgID, inst.TemplateID)
	final := func(context.Context) (InstanceView, error) {
		inst.State = model.InstanceUnlinked
		return uc.view(inst, name), nil
	}
	return Outcome{RequestID: id, Done: done, Final: final}, nil
}
```

Run: `go test -race -count=1 ./internal/domain/usecase/templates/`
Expected: `ok`.

- [ ] **Step 3: Write the handlers and routes**

`internal/app/web/handler/templates/lifecycle.go`:

```go
package templates

import (
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
)

// ControlInstanceRequest is the body of POST control-instance.
type ControlInstanceRequest struct {
	ServerID string                `json:"server_id" required:"true"`
	Key      string                `json:"key" required:"true"`
	Action   command.ControlAction `json:"action" required:"true" enum:"start,stop,restart"`
	Wait     bool                  `json:"wait,omitempty"`
}

// DeleteInstanceRequest is the body of POST delete-instance.
type DeleteInstanceRequest struct {
	ServerID      string `json:"server_id" required:"true"`
	Key           string `json:"key" required:"true"`
	DeleteVolumes bool   `json:"delete_volumes"`
	DeleteBackups bool   `json:"delete_backups"`
	Wait          bool   `json:"wait,omitempty"`
}

// InstanceKeyRequest is the body of POST unlink-instance.
type InstanceKeyRequest struct {
	ServerID string `json:"server_id" required:"true"`
	Key      string `json:"key" required:"true"`
	Wait     bool   `json:"wait,omitempty"`
}

func (h *Handler) ControlInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[ControlInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	out, err := h.uc.ControlInstance(r.Context(), userID, orgID, req.ServerID, req.Key, req.Action)
	if err != nil {
		h.fail(w, err, "control the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "control action dispatched")
}

func (h *Handler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[DeleteInstanceRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	out, err := h.uc.DeleteInstance(r.Context(), userID, orgID, req.ServerID, req.Key, req.DeleteVolumes, req.DeleteBackups)
	if err != nil {
		h.fail(w, err, "delete the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "delete dispatched")
}

func (h *Handler) UnlinkInstance(w http.ResponseWriter, r *http.Request) {
	req, ok := webutil.DecodeBody[InstanceKeyRequest](w, r)
	if !ok {
		return
	}
	userID, orgID, ok := h.serverCaller(w, r, req.ServerID)
	if !ok || !requireKey(w, req.Key) {
		return
	}
	out, err := h.uc.UnlinkInstance(r.Context(), userID, orgID, req.ServerID, req.Key)
	if err != nil {
		h.fail(w, err, "unlink the instance")
		return
	}
	h.respond(w, r, out, req.Wait, "unlink dispatched")
}
```

In `internal/app/web/handler/templates/instance_routes.go`, insert after the upgrade-instance entry:

```go
		{
			Method: http.MethodPost, Path: "/api/v1/instance/control-instance", Handler: h.ControlInstance, Auth: api.User,
			Summary: "Start, stop or restart an instance", Tag: "instances", Description: waits,
			Request: ControlInstanceRequest{}, Accepted: true, Result: command.ControlAppResponse{}, Response: InstanceResult{},
			Errors: instance,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/instance/delete-instance", Handler: h.DeleteInstance, Auth: api.User,
			Summary: "Delete an instance's app; its key stays retired", Tag: "instances", Description: waits,
			Request: DeleteInstanceRequest{}, Accepted: true, Result: command.DeleteAppResponse{}, Response: InstanceResult{},
			Errors: instance,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/instance/unlink-instance", Handler: h.UnlinkInstance, Auth: api.User,
			Summary: "Turn an instance into an ordinary app; its key is freed", Tag: "instances", Description: waits,
			Request: InstanceKeyRequest{}, Accepted: true, Result: command.SaveAppResponse{}, Response: InstanceResult{},
			Errors: instance,
		},
```

- [ ] **Step 4: Contract tests and probes**

Append to `internal/app/web/contract/instances_test.go`:

```go
func TestInstanceLifecycle(t *testing.T) {
	t.Parallel()
	ie := newInstanceEnv(t)
	e, owner, sid := ie.e, ie.owner, ie.sid
	appID := ie.created(t, "tenant-1", "acme", "9001").Instance.AppID

	t.Run("control", func(t *testing.T) {
		owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/instance/control-instance",
			Body: htpl.ControlInstanceRequest{ServerID: sid, Key: "tenant-1", Action: command.ControlRestart}}, nil)
		owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/instance/control-instance", Invalid: true,
			Body: htpl.ControlInstanceRequest{ServerID: sid, Key: "tenant-1", Action: command.ControlUpdate}}).expect(t, http.StatusBadRequest)
		e.agent.fail(command.TypeAppControl, "docker compose stop: boom")
		r := owner.post(t, "/api/v1/instance/control-instance", htpl.ControlInstanceRequest{ServerID: sid, Key: "tenant-1", Action: command.ControlStop, Wait: true}).
			expect(t, http.StatusBadGateway)
		if !strings.Contains(string(r.Body), "boom") || instanceOf(t, owner, sid, "tenant-1").LastError != "docker compose stop: boom" {
			t.Fatalf("502 body = %s", r.Body)
		}
	})
	t.Run("a template with live instances is not deleted", func(t *testing.T) {
		owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: ie.tplID}).expect(t, http.StatusConflict)
	})
	t.Run("unlink frees the key", func(t *testing.T) {
		var res htpl.InstanceResult
		owner.post(t, "/api/v1/instance/unlink-instance", htpl.InstanceKeyRequest{ServerID: sid, Key: "tenant-1", Wait: true}).
			expect(t, http.StatusOK).data(t, &res)
		if res.Instance.State != "unlinked" || res.Instance.AppID != appID {
			t.Fatalf("unlinked = %+v", res.Instance)
		}
		cfg := string(lastSave(t, e).App.Config)
		if strings.Contains(cfg, `"instance"`) || strings.Contains(cfg, `"template_id"`) {
			t.Fatalf("unlink config = %s", cfg)
		}
		owner.get(t, "/api/v1/instance/get-instance", url.Values{"server_id": {sid}, "key": {"tenant-1"}}).expect(t, http.StatusNotFound)
		if apps := listApps(t, owner, sid); apps[0].ID != appID || apps[0].TemplateID != "" {
			t.Fatalf("unlinked app = %+v", apps[0])
		}
	})
	t.Run("a freed key makes a new app; delete retires it", func(t *testing.T) {
		again := ie.created(t, "tenant-1", "bravo", "9002")
		if again.Instance.AppID == appID || again.RequestID == "" {
			t.Fatalf("a freed key took over the unlinked app: %+v", again)
		}
		var res htpl.InstanceResult
		owner.post(t, "/api/v1/instance/delete-instance", htpl.DeleteInstanceRequest{ServerID: sid, Key: "tenant-1", DeleteVolumes: true, Wait: true}).
			expect(t, http.StatusOK).data(t, &res)
		if res.Instance.State != "deleted" {
			t.Fatalf("deleted = %+v", res.Instance)
		}
		dels := e.agent.seen(command.TypeAppDelete)
		if req := decode[command.DeleteAppRequest](dels[len(dels)-1].Payload); req.AppID != again.Instance.AppID || !req.DeleteVolumes {
			t.Fatalf("app.delete = %+v", req)
		}
		owner.post(t, "/api/v1/instance/create-instance", ie.create("tenant-1", "bravo", "9002")).expect(t, http.StatusConflict)
		n := len(e.agent.seen(command.TypeAppDelete))
		owner.post(t, "/api/v1/instance/delete-instance", htpl.DeleteInstanceRequest{ServerID: sid, Key: "tenant-1"}).expect(t, http.StatusOK)
		if len(e.agent.seen(command.TypeAppDelete)) != n {
			t.Fatal("deleting a deleted instance reached the agent")
		}
		owner.post(t, "/api/v1/instance/unlink-instance", htpl.InstanceKeyRequest{ServerID: sid, Key: "tenant-1"}).expect(t, http.StatusConflict)
	})
	t.Run("without live instances the template is deleted", func(t *testing.T) {
		owner.post(t, "/api/v1/template/delete-template", htpl.TemplateIDRequest{ID: ie.tplID}).expect(t, http.StatusOK)
	})
}
```

In `internal/app/web/contract/matrix_test.go`, add after the `"POST /api/v1/instance/upgrade-instance"` probe:

```go
	"POST /api/v1/instance/control-instance": {valid: func(s string) call {
		return post("/api/v1/instance/control-instance", map[string]any{"server_id": s, "key": "k", "action": "start"})
	}, malformed: bad("POST", "/api/v1/instance/control-instance")},
	"POST /api/v1/instance/delete-instance": {valid: func(s string) call {
		return post("/api/v1/instance/delete-instance", map[string]any{"server_id": s, "key": "k"})
	}, malformed: bad("POST", "/api/v1/instance/delete-instance")},
	"POST /api/v1/instance/unlink-instance": {valid: func(s string) call {
		return post("/api/v1/instance/unlink-instance", map[string]any{"server_id": s, "key": "k"})
	}, malformed: bad("POST", "/api/v1/instance/unlink-instance")},
```

- [ ] **Step 5: Regenerate and run**

Run: `make openapi && go test -count=1 ./internal/app/web -run TestRoutesGolden -update`
Run: `go vet ./... && go test -count=1 ./internal/app/web/... && WF_CONTRACT_GUARD=require go test -count=1 ./internal/app/web/contract/`
Expected: `ok` everywhere.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/usecase/templates internal/app/web/handler/templates internal/app/web/contract \
  api/openapi.yaml internal/app/web/testdata/routes.golden
git commit -m "feat(instances): control, delete (retires the key) and unlink (frees it)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Parity transcript, docs and the gates

**Files:**
- Modify: `internal/app/web/contract/parity_test.go`, `internal/app/web/contract/testdata/parity.golden.json`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: everything above. Produces: nothing new.

- [ ] **Step 1: Add templates to the parity scenario**

In `internal/app/web/contract/parity_test.go` add the imports `htpl "winterflow/internal/app/web/handler/templates"`, `"winterflow/internal/domain/template"` and `tuc "winterflow/internal/domain/usecase/templates"`, and before `	var buf bytes.Buffer` add:

```go
	var tpl tuc.SavedTemplate
	rec("save template", p.post(t, "/api/v1/template/save-template", htpl.SaveTemplateRequest{
		Name: "parity", Body: template.Body{Files: map[string]string{"compose.yaml": "services: {app: {image: x}}\n"}, AppName: "parity-app"},
	})).expect(t, http.StatusOK).data(t, &tpl)
	rec("create instance (wait)", p.post(t, "/api/v1/instance/create-instance", htpl.CreateInstanceRequest{
		ServerID: sid, Key: "parity-1", TemplateID: tpl.ID, Wait: true,
	})).expect(t, http.StatusOK)
	rec("list templates", p.get(t, "/api/v1/template/get-templates", nil)).expect(t, http.StatusOK)
```

Run: `go test -count=1 ./internal/app/web/contract -run TestParity`
Expected: FAIL (transcript differs: three new steps).

Run: `go test -count=1 ./internal/app/web/contract -run TestParity -update && git diff internal/app/web/contract/testdata/parity.golden.json`
Expected: three appended steps: `save template` (200, `revision: 1`, `warnings: []`), `create instance (wait)` (200, `instance.state: "created"`, `parameters: {}`, `secrets: {}`, `status_code: 0`, `request_id: "<id-n>"`), `list templates` (200, `instances: 1`, `behind: 0`); timestamps normalize to `<time>Z`.

Then confirm the golden on Postgres:

Run: `make test-pg TEST_PG_ARGS='-run TestParity'`
Expected: `ok` for the contract package (and the db packages).

- [ ] **Step 2: Document templates in CLAUDE.md**

In `CLAUDE.md`, after the **Backups:** paragraph (the one ending `... list and delete the folders of apps not on this server.`), add:

```markdown
**Templates and instances (API only; the agent never sees a template):** a template is an org's stack definition with immutable revisions (`templates`, `template_revisions`; body = files, parameters, app name/icon/color, ingress rows, backup defaults, validated by `internal/domain/template`: ≤1 MiB, RE2 patterns ≤512 B, no default on a secret parameter, undeclared `${VAR}`s in files only warn). An instance (`template_instances`) is an ordinary app rendered from a revision and the caller's values by `template.Render` into the `app.save` payload the editor would send (files unchanged, one variable per parameter, secrets as the caller's ECIES ciphertext or the `<encrypted>` keep placeholder, config blob with the rendered ingress, `template_id` and the `instance` marker) and dispatched through the app usecase's `CheckSave` + `DispatchSave`, so every save-app check applies before anything is claimed. The caller's `key` is unique per org among instances not unlinked (partial unique index); a create claims it only after the checks pass, a replay with the same request hash (server, template, requested revision, plain values, secret *names*, backup overrides) answers the created instance or re-dispatches a pending one, another request is 409, a deleted instance's key stays retired (409), unlink frees it. Instance routes take `wait: true`: the handler holds only its own request on a buffered channel fed by the dispatch result observer (which records the outcome either way), at most 10 minutes, then answers 202; a failed command is 502 and stays as `last_error`. An upgrade sends `instance.backup_defaults` for backups new in the revision, which the agent's save (`applyBackupDefaults`) adds where the app has no entry and then drops. Upgrade-all and rollouts are D1b.
```

- [ ] **Step 3: Run the gates**

Run each and check the expected result:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-d1a; mkdir -p $GOTMPDIR
gofmt -l ./internal ./cmd ./pkg ./tools          # expect: no output
go vet ./...                                      # expect: no output
make sqlc-check                                   # expect: no diff
make openapi && git diff --exit-code api/openapi.yaml   # expect: exit 0 (committed spec is current)
WF_CONTRACT_GUARD=require make test               # expect: every package ok; coverage.out written
make test-pg                                      # expect: ok (contract suite + internal/infra/db on postgres:16)
```

For the diff-coverage gate CI merges the Postgres profile (it is the only coverage of `repository/template/pgsql.go`); reproduce it and run the gate (web is unchanged, but the tool needs an lcov file, so build it):

```bash
make test-pg TEST_PG_ARGS='-race -covermode=atomic -coverpkg=./... -coverprofile=contract-postgres.out'
tail -n +2 contract-postgres.out >> coverage.out
pnpm --dir web install --frozen-lockfile && make web-test
go run ./tools/diffcov --base origin/main --min 80 --go coverage.out --lcov web/coverage/lcov.info
```

Expected: Go changed lines ≥ 80% covered (measured on SQLite alone in the scratch run: 87% over the new files, `pgsql.go` excluded), web: no changed lines. If `make web-test` cannot run locally, CI runs the same gate.

- [ ] **Step 4: Commit**

```bash
git add internal/app/web/contract/parity_test.go internal/app/web/contract/testdata/parity.golden.json CLAUDE.md
git commit -m "test(contract): templates in the parity transcript; docs: templates and instances

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

If CI prints `::notice::raise coverage-baseline.json …`, bump `coverage-baseline.json` in the same PR.

---

## Self-review

- **Spec coverage (D1a):** tables (Task 1; `rollouts` deliberately absent), validation at save with every listed rule and the warning (Tasks 2, 6), rendering incl. variables, secrets, placeholders, catalog record, ingress, config blob, create backups and upgrade defaults (Tasks 3, 4, 5), key claim / replay / 409s (Tasks 1, 7, 9), `wait` (Task 7), template routes except `upgrade-instances` (Task 6), all eight instance routes (Tasks 7–9), errors 400/404/403/409/502 (Tasks 6–9), OpenAPI (Tasks 6–9), contract happy paths, probes, 403 sweep, 404 for another org, no secret in any answer (`noSecrets`), parity (Task 10), repository tests on both engines (Task 1), usecase/unit tests (Tasks 2–9). No new SSE type is needed: instance results are ordinary `operation_result`s.
- **Not in D1a (by the spec's Delivery):** `POST /api/v1/template/upgrade-instances`, `rollouts`, the rollout worker and SSE `rollout` event, the waitable single-app `vars.reload` (D1b); all UI (D1c).
- **Type consistency:** `Outcome.Final`, `observe`, `dispatchSave`, `live`, `reread`, `view`, `templateName` are defined once (Tasks 7–8) and used with the same signatures in Tasks 8–9; handler `respond(w, r, out, wait, msg)` is the same in every route.
