# Templates, instances and rollouts — design

Date: 2026-10-11. Status: approved in conversation, pending written-spec review.

## Context

This is sub-project **D1** of "winterflow as the runtime for twillingate cloud
tenants". The twillingate-cloud portal will create each tenant through
winterflow's API instead of its own systemd host agent (portal spec
`twillingate-cloud/docs/superpowers/specs/2026-10-08-cloud-portal-design.md`,
§9). A tenant is the `ghcr.io/dmtrkzntsv/twillingate` image with its SQLite
volume, served at `<name>.twillingate.app`. The first deployment is one
standalone host; distributed comes later.

Already in place: delete with volumes and backups (A), registered domains with
TLS (B), storages and scheduled backups with restore (C), shared org/server
variables and secrets (#16), personal access tokens.

What is missing is a way to define a stack once and create, change, upgrade,
stop and delete copies of it over an API that a program can drive safely.

### The rest of D (separate specs)

| # | Piece |
|---|---|
| D1 | **This spec:** templates, instances, the instance API, the rollout queue |
| D2 | Hosting edges: 410 with CORS for stopped and unknown hosts, `Host` rewrite on `/mcp*` |
| D3 | Scoped tokens (least privilege for the portal's token) |
| D4 | Restore a backup into a new app |

## Goals

1. **Templates:** an org-level definition of a stack (files, parameters, app
   name, ingress, backup defaults), with immutable revisions.
2. **Instances:** apps created from a template revision with parameter values.
   An instance's files are owned by its template until it is unlinked.
3. **An API a program can drive:** each instance is addressed by the caller's
   own `key`; a retried create never makes a second app; calls can wait for
   the outcome.
4. **Rollouts:** "Upgrade all instances" of a template, one app at a time,
   stopping at the first failure. Shared-variable reloads move onto the same
   queue.
5. **UI:** template pages, "From template" in Add app, the instance's app page,
   "Save as template" on an existing app.

## Non-goals

- A template expression language. Compose substitutes `${VAR}`; the API only
  substitutes plain parameters into the app name, ingress hosts and ingress
  ports.
- Typed parameters (number, yes/no, list). Values are strings; `pattern`
  validates them.
- Merging hand edits with template changes. An instance's files are read-only
  until it is unlinked.
- A declarative "desired list" API. Calls are imperative, per instance.
- Automatic upgrades when a template changes. Upgrades are explicit, per
  instance or through "Upgrade all".
- Everything listed for D2–D4.

## Concepts

- **Template:** belongs to an org; has a unique lowercase name and a sequence
  of **revisions** (1, 2, 3…). A revision is immutable; saving a template adds
  a revision.
- **Instance:** an app created from a template revision. It records its
  template, its revision, its `key` and its plain parameter values. It is an
  ordinary app in every other respect (logs, metrics, history, backups,
  start/stop, delete).
- **Key:** the caller's identifier for an instance (the portal's instance id),
  unique within the org among instances that are not unlinked.
- **Unlink:** turns an instance into an ordinary, freely editable app. One-way.
- **Rollout:** a queued, resumable, one-app-at-a-time job: upgrade a template's
  instances, or reload the apps a shared-variable change affects.

## Template body

A revision's body is JSON. The twillingate template as an example (shown as
YAML for readability):

```yaml
files:
  compose.yaml: |
    services:
      app:
        image: ghcr.io/dmtrkzntsv/twillingate:${VERSION}
        mem_limit: ${MEMORY_MAX}
        environment:
          PUBLIC_URL: https://${NAME}.twillingate.app
          CONSOLE_AUTH_DSN: oauth://app.twillingate.dev?resource=https://${NAME}.twillingate.app
          DATABASE_DSN: sqlite:///data/twillingate.db
          RETENTION_EVENTS_AGGREGATE_DAYS: ${RETENTION_DAYS}
          INGEST_DISABLED: ${INGEST_DISABLED}
        ports: ["127.0.0.1:${PORT}:8080"]
        volumes: [data:/data]
    volumes: { data: {} }
    x-winterflow:
      backups:
        - { name: db, type: sqlite, volume: data, path: twillingate.db, every: 1h }
parameters:
  - { name: NAME, required: true, pattern: "^[a-z]{3,20}$", description: "Instance name" }
  - { name: VERSION, required: true }
  - { name: PORT, required: true, pattern: "^[1-9][0-9]{0,4}$", description: "Host port the ingress forwards to" }
  - { name: MEMORY_MAX, default: 256m }
  - { name: RETENTION_DAYS, default: "365" }
  - { name: INGEST_DISABLED, default: "" }
  # a secret: { name: SESSION_KEY, secret: true, required: true }
app_name: ${NAME}
icon: chart
color: "#2563eb"
ingress:
  - { host: "${NAME}.twillingate.app", service: app, port: "${PORT}" }
backups:
  db: { enabled: true, storage: tenants }
note: "bump image to v0.15"   # optional, per revision
```

- `files`: the app's files, exactly what an app save carries (compose file
  first; other files allowed).
- `parameters`: each has `name`, optional `secret`, `required`, `default`,
  `pattern`, `description`.
- `app_name`, `icon`, `color`: the app's catalog record. `app_name` may use
  `${PARAM}` of plain parameters.
- `ingress`: host → port rows, rendered into the app's ingress config. Hosts
  may use `${PARAM}` of plain parameters. `port` is a number or `${PARAM}` of a
  plain parameter. `service` is informational: winterflow's ingress forwards a
  hostname to `127.0.0.1:<port>` on the host, so each instance publishes its own
  host port (`ports: ["127.0.0.1:${PORT}:8080"]` above).
- `backups`: per recipe backup name, the defaults applied when the instance is
  created: `enabled`, `storage` (a storage name, resolved on the instance's
  server), optional `every`/`keep` overrides as in C.

### Validation at save (400, naming the field)

- Template name: `^[a-z][a-z0-9-]{0,62}$`, unique in the org.
- Parameter names: `^[A-Z][A-Z0-9_]*$`, unique, not reserved
  (`COMPOSE_PROJECT_NAME`, anything starting with `WF_`).
- `pattern`: compiles with Go's `regexp` (RE2), at most 512 bytes. A `default`
  must match its parameter's `pattern`.
- A secret parameter has no `default` (a template never holds a secret value).
- `${VAR}` references in `files` (any `${VAR…}` form, `$VAR`; `$$VAR` is an
  escape; the scanner shared variables use) are **not** refused when
  undeclared, because templates are meant to use shared variables
  (`${SMTP_PASS}`), which are resolved per server at deploy. The save answers
  a **warning** listing each reference that is not a parameter, not a `WF_`
  name, not an org shared variable, and has no default of its own
  (`${X:-y}`): "SMTP_PASS is not a parameter; it must come from a shared
  variable on the server". This catches typos without blocking server-level
  variables.
- `app_name`, ingress hosts and ingress ports reference only plain parameters.
- The compose recipe passes C's recipe validation (`internal/domain/backup/recipe`);
  every `backups` default names a backup the recipe has.
- Ingress rows pass the existing ingress validation once rendered with each
  parameter's default or, lacking one, a placeholder that matches nothing
  (host syntax only; coverage is checked per server at create).
- The body is at most 1 MiB.

## Data model

One migration, `0014_templates.sql`, identical in `migrations/sqlite` and
`migrations/pgsql` (renumbered at merge if something lands first).

```
templates
  id            text primary key
  org_id        → organizations ON DELETE CASCADE
  name          text
  created_at, created_by
  UNIQUE (org_id, name)

template_revisions
  template_id   → templates ON DELETE CASCADE
  revision      integer          -- 1, 2, 3… per template
  body          text             -- JSON, ≤ 1 MiB
  note          text
  created_at, created_by
  PRIMARY KEY (template_id, revision)

template_instances
  id            text primary key
  org_id        → organizations ON DELETE CASCADE
  key           text
  app_id        text             -- reserved before dispatch
  server_id     → servers ON DELETE CASCADE
  template_id   → templates ON DELETE SET NULL, NULL after the template is gone
                                 -- (only deleted/unlinked rows can outlive it)
  revision      integer
  params        text             -- JSON: plain parameter values only
  secret_names  text             -- JSON: names of secret parameters given
  request_hash  text             -- replay check, see "Key"
  state         text             -- pending | created | deleted | unlinked
  created_at, updated_at
  UNIQUE (org_id, key) WHERE state <> 'unlinked'   -- partial index

rollouts
  id              text primary key
  org_id          → organizations ON DELETE CASCADE
  kind            text           -- template_upgrade | vars_reload
  scope           text           -- JSON, per kind (see Rollouts)
  scope_key       text           -- template id, or the org id for vars_reload
  state           text           -- queued | running | done | stopped | failed
  stop_requested  bool
  current_app_id  text NULL
  done, skipped   integer
  error           text
  lease_owner     text NULL
  lease_until     timestamp NULL
  created_at, created_by, finished_at
  UNIQUE (kind, scope_key) WHERE state IN ('queued','running')
```

`apps.template_id` (present today, unused) is filled for instances and cleared
on unlink.

The app's config blob also carries an `instance` marker,
`{template_id, template_name, revision, key}`, so the editor and the app page
know an app is an instance without a lookup. The agent stores and ignores it.

## Rendering

The API turns (template revision, parameter values, backup overrides) into an
ordinary `app.save` payload. **The agent has no notion of templates.**

- **Files:** copied unchanged. Compose substitutes `${VAR}` at deploy from the
  app's `.env`, `.env.secrets` and the shared-variable files.
- **Variables:** every parameter becomes an app variable named after it. Plain
  values (given, or the default) are plain variables; secret values are
  encrypted variables carrying the caller's ciphertext unchanged. On update and
  upgrade, a secret not given again is sent as the `<encrypted>` placeholder,
  which keeps the agent's stored value.
- **Catalog record:** `app_name`, `icon`, `color`, with plain parameters
  substituted into `app_name`, the ingress hosts and the ingress ports.
- **Config blob:** the rendered ingress, the `instance` marker, and on create
  the backup overrides (template defaults, then the call's overrides).
- **Backups on update and upgrade:** a backup that already has an override
  keeps it (including Backups-tab changes); a backup new in the revision gets
  its template default. Today `keepBackups` keeps all stored overrides when the
  config has no `backups` key; the save path learns to add defaults for names
  that have none (see "Open items").

Every check `save-app` makes applies unchanged: reserved variable names, domain
coverage (B), a host already taken on the server, a missing storage (C).

## Key, replay and wait

**Create:**

1. Check `RequireServerAccess`, the template (caller's org), the revision
   (latest by default) and the parameter values.
2. Render; run the `save-app` checks.
3. In one transaction, claim the key: insert a `template_instances` row
   (`pending`) with a newly generated `app_id`. A unique-index violation means
   the key exists: go to **Replay**.
4. Dispatch `app.save` with that `app_id`. On success the row becomes
   `created` and the app record is persisted as today.

**Replay** (the key exists and is not unlinked):

- `request_hash` covers server, template, revision, the plain values and the
  **names** of the secrets given (ECIES ciphertexts differ on every
  encryption, so their bytes cannot be compared).
- Same hash and `created`: answer the existing instance (200), nothing is
  redeployed.
- Same hash and `pending` (an earlier attempt never confirmed): dispatch the
  save again with the same `app_id` and this request's payload. A save with an
  id is an idempotent edit.
- Different hash: **409** "key in use; update the instance instead".
- `deleted`: **409** "this key belonged to a deleted instance".

**Wait:** every instance call accepts `wait: true`. The API then awaits the
agent's result, bounded at 10 minutes (an image pull), and answers 200 with the
outcome or the agent's error with a 4xx/5xx. If the bound passes it answers
202 with the `request_id`, and the caller falls back to `get-instance`.
Without `wait`, calls answer 202 with a `request_id` and the result arrives
over SSE, as today.

## API

All routes need a user (JWT or PAT). The org is the caller's primary org, as
for shared variables. A template, key or rollout of another org answers 404.

### Templates

| Route | Body / query | Does |
|---|---|---|
| `GET /api/v1/template/get-templates` | | name, latest revision, instance count, instances behind the latest |
| `GET /api/v1/template/get-template` | `id`, `revision?` | the revision's body (latest by default) and the revision list |
| `POST /api/v1/template/save-template` | `{id?, name, body, note?, base_revision}` | adds a revision; `base_revision` ≠ latest → 409 (a concurrent edit) |
| `POST /api/v1/template/delete-template` | `{id}` | refused (409) while an instance is `pending` or `created` |
| `POST /api/v1/template/upgrade-instances` | `{id, revision}` | queues a `template_upgrade` rollout; 409 while one is active for the template |

### Instances

Every route takes `server_id` and checks `RequireServerAccess`.

| Route | Body / query | Does |
|---|---|---|
| `POST /api/v1/instance/create-instance` | `{server_id, key, template_id, revision?, parameters, secrets, backups?, wait?}` | create, or replay (above) |
| `POST /api/v1/instance/update-instance` | `{server_id, key, parameters, secrets?, backups?, wait?}` | same revision, new values (merged with the stored ones) |
| `POST /api/v1/instance/upgrade-instance` | `{server_id, key, revision, parameters?, secrets?, backups?, wait?}` | move to another revision; a newly required parameter without a default must be given |
| `POST /api/v1/instance/control-instance` | `{server_id, key, action, wait?}` | `start`, `stop`, `restart` (today's app control) |
| `POST /api/v1/instance/delete-instance` | `{server_id, key, delete_volumes, delete_backups, wait?}` | today's app delete; the key is retired (`deleted`) |
| `POST /api/v1/instance/unlink-instance` | `{server_id, key, wait?}` | saves the app without the `instance` marker; the key is freed (`unlinked`) |
| `GET /api/v1/instance/get-instance` | `server_id`, `key` | app id, template, revision, plain values, secret names (values `<encrypted>`), state, app status, last error |
| `GET /api/v1/instance/get-instances` | `server_id?`, `template_id?` | the list, for the template page and the portal's sweeps |

`parameters` is `{NAME: value}` for plain parameters; `secrets` is
`{NAME: ciphertext}`, encrypted with the server's public key
(`GET /api/v1/server/get-public-key`), only for secret parameters. `backups` is
`{backup: {storage}}`, overriding the template's default storage.

A **pending** instance (its create has not succeeded yet) is rendered on update
and upgrade like a create: it must resend every secret (the agent holds no
stored value to keep), the full backups list is sent, and `backups` overrides
are accepted only while the instance is pending.

Checks at create, update and upgrade (400, naming the parameter): required
parameters present, unknown names refused, values matching their `pattern`,
secrets given only for secret parameters, plus every `save-app` check on the
rendered app.

### Rollouts

| Route | Does |
|---|---|
| `GET /api/v1/rollout/get-rollouts` | `kind`, `scope_key?`: the active rollout and the last 20 ended ones |
| `POST /api/v1/rollout/stop-rollout` | `{id}`: sets `stop_requested` |

The shared-variable routes keep their paths. Their reload effects (the
`reload: true` flag on save and delete, and `POST /api/v1/shared-variables/reload`)
now queue or extend a `vars_reload` rollout instead of dispatching
`vars.reload` to every server.

### SSE

- `rollout {id, kind, scope_key, state, done, skipped, total, current_app_id, error}`,
  to the org, whenever counts or state change.
- Instance calls without `wait` deliver results like any other agent command.

Each new event gets its `api.EventPayload` entry; `make openapi` after the
routes.

## Rollouts

One queue in the database, two kinds.

| Kind | `scope` | Next app | Step |
|---|---|---|---|
| `template_upgrade` | `{template_id, revision}` | the oldest-created instance of the template in state `created` with a lower revision | re-render at the target revision and save; skipped (counted) if the revision requires a value it lacks |
| `vars_reload` | `{names: [], server_ids: []}` | the next app with a running container on those servers whose `stale_vars` (API status cache) includes one of the names | `vars.reload {app_ids: [id]}`, awaited |

**Worker:** one goroutine per API process.

- It wakes on an in-process signal when a rollout is queued or extended, and
  once a minute otherwise (one indexed query when idle).
- It claims a rollout with one statement:
  `UPDATE rollouts SET lease_owner = :me, lease_until = :now + 2m, state = 'running'
  WHERE id = :id AND state IN ('queued','running') AND (lease_until IS NULL OR lease_until < :now)`.
  It renews the lease while it works. This holds with several API replicas.
- No transaction is held while waiting on an agent.

**A step:**

1. Pick the next app (re-evaluated fresh each step, so an app handled by other
   means meanwhile is not picked again).
2. Record it as `current_app_id`; dispatch; await the result (10 minutes).
3. Wait until the status cache reports the app running (status Active) on a
   report received after the step, at most 5 minutes. Restarting, Problematic
   or Stopped at the bound fails the step.
4. On success, count it and continue. On failure, set `failed` with the app
   and the error, and stop. A `stop_requested` rollout becomes `stopped` after
   the step in flight.
5. No next app: `done`.

**Resume:** a rollout whose lease expired (the API stopped) is claimed again.
The step re-evaluates: an instance already on the target revision and running
is not picked; a stale app already reloaded is no longer stale.

**One active rollout per scope:** the partial unique index on
`(kind, scope_key)`. A `template_upgrade` for a template with an active one
answers 409. A `vars_reload` while one is active for the org **extends** it:
its names and servers are added to the scope (in one statement), because the
next app is chosen from live staleness.

**Pruning:** ended rollouts beyond the last 20 per `(kind, scope_key)` are
deleted when a rollout ends.

### Agent change: a waitable single-app reload

`vars.reload` with exactly one entry in `app_ids` becomes a detached command,
like `backup.run`: it is queued on the agent's existing reload worker (so the
`InProcessBridge` goroutine is never blocked), and its reply carries that
app's outcome. The reply is bounded by the worker's 10-minute per-app deadline.
The bulk form (no `app_ids`) stays for compatibility; the UI no longer uses
it.

## UI

**Org: Templates** (`/org/templates`, linked next to Members and Variables).

- **List:** name, latest revision, "12 instances · 3 behind", **Add template**.
- **Template page:**
  - **Editor:** the app editor's file editing; a **Parameters** table (name,
    secret, required, default, pattern, description); app name, icon and color;
    **Ingress** rows; **Backup defaults** (one row per recipe backup: on/off,
    storage name). **Save** adds a revision with an optional note; a concurrent
    edit shows "someone saved r4 meanwhile; reload".
  - **Revisions:** the list, each with a diff against the previous one.
  - **Instances:** per server, revision and status, per-row **Upgrade**, and
    **Upgrade all to rN** (confirms first, then the rollout strip with Stop).
  - **Delete:** offered only without live instances.

**Server: Add app → From template.**

- Pick a template and revision (latest by default).
- A form from the parameters: required first, `pattern` checked as typed,
  secret fields masked and encrypted for the server in the browser.
- Per backup default, a select of this server's storages, preselected to the
  template's storage.
- The hosts it will get, shown before Create, with B's coverage error inline.
- The browser generates the `key` (a UUID) for UI-created instances.
- Create waits like today's create, then opens the app.

**App page of an instance.**

- Header badge "twillingate · r3", linking to the template; "r5 available"
  when behind.
- **Configuration:** files read-only with "Managed by template twillingate";
  in place of the editor, a **Parameters** form (Save re-renders and
  redeploys; secrets keep their value unless replaced).
- **Upgrade to rN** when behind; asks for newly required values.
- **Settings → Unlink from template:** confirms ("the app becomes an ordinary
  app you can edit freely; template upgrades no longer reach it"), then the
  editor is enabled and the badge is gone.
- Everything else (Backups, Logs, Metrics, History, start/stop, delete) is as
  for any app. Delete also retires the key, from the instance routes and from
  `delete-app` alike.

**Existing app: Settings → Save as template.**

- Asks for a template name; builds revision 1 in the browser from what the
  editor already holds: files copied; each app variable becomes a parameter
  with its value as the default; each secret becomes a secret parameter,
  required, without a default; the app's hosts become literal ingress rows;
  the app's backup overrides become backup defaults.
- Opens the new template's editor for review (for example, to replace the
  varying part of a host with `${NAME}`). The app itself stays an ordinary app.

**Rollout strip:** "Upgrading 3 of 12: bakomi… [Stop]" or "Reloading 2 of 5:
shop on srv-2… [Stop]", on the template page, the server's Variables tab and
the org Variables page. A failure names the app and links to it.

## Errors

| Status | When |
|---|---|
| 400 | a named field: missing required value, value not matching its pattern, unknown parameter, secret for a plain parameter, host not covered, missing storage, invalid template body |
| 404 | unknown template, key or rollout, or one of another org |
| 403 | a server the caller's org does not own |
| 409 | key in use with different values, retired key, concurrent template edit, active rollout for the template, template with live instances, `save-app` or `rollback-app` on an instance's app |
| agent error | with `wait`, the deploy's own failure; without it, over SSE |

## Security and resource invariants

- Every instance route checks `RequireServerAccess`; template and rollout
  routes are scoped to the caller's org; 403/404 tests per route.
- Secret parameter values reach the API only as ciphertext for one server and
  pass unchanged into the app's encrypted variables. No route returns them;
  `get-instance` lists their names only. A template never holds a secret value.
- Rendering substitutes only plain parameters, only into the app name,
  ingress hosts and ingress ports, and the results go through the existing
  validation. There is no expression language.
- A secret that does not have the ECIES wire format's shape is refused (400),
  so a plaintext never reaches an app's history; a plain value or default may
  not contain `$`, which compose would interpolate from the app's `.env`.
- An instance's app changes only through its instance: `save-app` and
  `rollback-app` refuse it (409) while the instance is `pending` or
  `created`; `delete-app` deletes the instance too (its key is retired).
- Regexes are RE2, at most 512 bytes; template bodies at most 1 MiB.
- Every wait is bounded: `wait` 10 minutes; rollout steps 10 minutes for the
  command and 5 for health; leases 2 minutes.
- The idle rollout worker costs one indexed query a minute. SSE `rollout`
  events fire only on change.

## Testing

**Go unit:** template validation (every rule above); rendering (variables,
secrets, placeholders on update, catalog record, ingress, backup defaults vs
kept overrides); request hash (secret names, not bytes); the key claim under
two concurrent creates (one app); replay of `pending` and `created`; the 409
cases; the rollout worker (claim, lease renewal and takeover after expiry,
resume, stop, first failure, skip, one active per scope, `vars_reload`
extension, pruning) with `-race`.

**Repository** (`dbtest.Open`, SQLite and Postgres via `make test-pg`): unique
template names, the partial unique indexes on keys and active rollouts, the
lease claim statement, template delete refused with live instances, and `template_id` nulled on the retired rows.

**Contract** (`internal/app/web/contract`): happy path and probes for every new
route; 403 for a foreign server; 404 for another org's template and key; no
response containing a secret; `TestParity` golden; `make openapi`.

**Agent:** the waitable single-app `vars.reload` (queued on the worker, reply
carries the outcome, bridge not blocked).

**Web (Vitest):** template editor (parameters table, ingress, backup defaults,
concurrent-edit message); From template form (pattern, secrets encrypted,
hosts preview); instance app page (read-only files, Parameters form, Upgrade,
Unlink); Save as template body; rollout strip and Stop.

**Manual, before each PR:** on a throwaway instance, run twillingate as a
normal app, Save as template, then with a PAT and `curl`: create with `key`
and `wait`, replay the same call (same app), update a value, upgrade, and
delete with volumes and backups. Then Upgrade all onto a deliberately broken
revision and watch it stop at the first instance. Rotate a shared secret used
by two apps and watch the reload rollout.

## Delivery

Three stacked PRs:

1. **D1a, templates and instances API:** tables, validation, rendering, the
   key flow, `wait`, contract tests. Usable from `curl` and the portal.
2. **D1b, rollout queue:** Upgrade all, the waitable single-app reload, and
   shared-variable reloads moved onto the queue.
3. **D1c, UI:** the Templates pages, From template, the instance app page,
   Save as template, the rollout strip.

## Open items for planning

- How `wait` is implemented on the API side: the reply manager already wakes a
  blocked dispatch (`DispatchInput.Wait`); the handler must hold the request
  for it without blocking other requests.
- Adding template backup defaults on update and upgrade without the API
  reading the app's current overrides: likely an `instance.backup_defaults`
  key that the agent's save path merges for backup names that have no
  override.
- Whether a Docker healthcheck's `unhealthy` maps to a non-Active app status
  today; if not, the rollout's health wait treats Active as running only.
- The migration number against whatever merges first.
