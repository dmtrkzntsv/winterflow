# Org- and server-level shared variables and secrets — design

Date: 2026-10-11. Status: approved in conversation, pending written-spec review.

## Context

Today variables and secrets exist only per app. They live in the app's git
repo on the agent (`apps-data/{appID}/`): plain values in the committed `.env`,
secrets as ECIES ciphertext in the committed `.winterflow/secrets.json`,
decrypted at deploy into the gitignored `.env.secrets` (0600). Compose
interpolates `${VAR}` from both via explicit `--env-file` flags. The API
database stores no variable values. There is no org- or server-level config.

## Goals

Two new scopes above the app, each holding plain variables and secrets:

- **Org:** set once, available to apps on every server of the org.
- **Server (machine):** set once per server, available to apps on that server.

They serve:

1. **Shared credentials** (SMTP, S3/R2 keys, API tokens) without pasting them
   into every app.
2. **Templates** (twillingate sub-project D): template-instantiated apps write
   `${SMTP_PASS}` and pick up org/server values.
3. **Machine facts:** per-server values plus a small set of read-only built-ins.
4. **Rotation:** change a value once; apps that use it are flagged stale and can
   be reloaded with one action.

## Non-goals

- Explicit per-app linking (`${org.X}` syntax). Consumption is implicit.
- The API ever seeing a secret's plaintext. There is no API master key.
- Automatic agent-to-agent re-encryption for newly added servers.
- Automatic redeploy on change. Reload is explicit (or a save-dialog opt-in).
- Compose `env_file:` / container-env injection. As today, values reach a
  container only where the compose file references them.

## Naming

- **Agent side and wire:** "vars". Folder `{AGENT_DATA_DIR}/vars/`, commands
  `vars.sync` / `vars.reload`, SSE events `vars.*`, column
  `servers.vars_revision`.
- **Database, routes and UI:** "shared variables", to stay distinct from app
  variables. Tables `shared_variables` / `shared_secret_ciphertexts`, routes
  `…/list-shared-variables` etc. The UI says "Variables" on the server settings
  tab and "Org variables" on the org page.
- **Per-app rendered files:** `.env.shared`, `.env.shared.secrets`.

## Precedence

The most specific value wins:

```
built-ins  <  org  <  server  <  app
```

Shadowing crosses the plain/secret boundary: a server plain variable overrides
an org secret of the same name, and vice versa.

**Reserved names** are rejected with a 400 on save: `COMPOSE_PROJECT_NAME` and
anything starting with `WF_`. Names must match `^[A-Za-z_][A-Za-z0-9_]*$`.

**Built-ins** are computed by the agent at render time and are read-only:

| Name | Source |
|---|---|
| `WF_SERVER_ID` | snapshot |
| `WF_SERVER_NAME` | snapshot |
| `WF_ORG_ID` | snapshot |
| `WF_APP_ID` | per app |
| `WF_APP_SLUG` | per app |
| `WF_APP_DIR` | per app (absolute app folder) |
| `WF_DATA_DIR` | `AGENT_DATA_DIR` |
| `WF_TZ` | host timezone |

## Data model

One migration, `NNNN_shared_variables.sql`, identical in `migrations/sqlite` and
`migrations/pgsql`. The number is chosen at merge time, since PRs #12, #13 and
#14 each claim 0010.

```
shared_variables
  id            text primary key
  org_id        → organizations ON DELETE CASCADE
  server_id     → servers ON DELETE CASCADE, NULL = org scope
  name          text
  is_secret     bool
  value         text NULL        -- plain variables only; NULL for secrets
  description   text
  updated_at, updated_by
  UNIQUE (org_id, server_id, name)              -- server scope
  UNIQUE (org_id, name) WHERE server_id IS NULL -- org scope (partial index)

shared_secret_ciphertexts
  variable_id   → shared_variables(id) ON DELETE CASCADE
  server_id     → servers ON DELETE CASCADE
  ciphertext    text             -- base64 ECIES for that server's key
  PRIMARY KEY (variable_id, server_id)

servers.vars_revision  integer not null default 0
```

**Why two tables.** The browser encrypts a secret once per server, so an org
secret across three servers is three different ciphertexts, each readable only
by its own server. The API must keep them:

- to deliver to servers that are offline at save time;
- to rebuild a reinstalled agent's snapshot on reconnect;
- to report which servers lack a copy without asking every agent.

**Shape rules:**

- A server-scope secret has exactly one ciphertext row, for its own server.
- An org-scope secret has one row per server it has been encrypted for. A
  server of the org with a public key but no row is **missing** that secret.
- `vars_revision` is bumped for every server affected by a change: one server
  for a server-scope change, all org servers for an org-scope change.

**Server key change.** When the API records a `public_key` capability that
differs from the stored one, it deletes every `shared_secret_ciphertexts` row
for that server (org and server scope) and bumps its revision. Those secrets
show as missing and must be re-entered; pushing undecryptable ciphertext is
never attempted.

## Crypto

The model is **browser fan-out**. The API stores ciphertext only and never sees
plaintext.

**Saving a secret:**

1. The browser calls `GET /api/v1/org/list-encryption-keys` and receives
   `{servers: [{server_id, name, public_key}], pending_servers: [{server_id,
   name}]}`. Pending servers have no `public_key` capability yet (never
   connected).
2. It encrypts the value once per keyed server with the existing
   `web/src/lib/ecies.ts`, through a new `encryptForServers(value, keys)` helper.
3. It sends `{name, is_secret: true, ciphertexts: {server_id: ct, …},
   description}`. A server-scope secret is the same flow with one key.

**Coverage rules (API):**

- Every `server_id` key in `ciphertexts` must belong to the caller's org,
  otherwise 403.
- A **new value** must cover every keyed server of the org at that moment,
  otherwise 400 with the uncovered ids. A rotation can't leave stale copies.
- A **gap fill** (partial map) is accepted only when every key is a server
  currently missing that secret. It never overwrites an existing ciphertext.
- A save without `ciphertexts` keeps every stored ciphertext (description-only
  edit).
- Rename and plain↔secret changes are not supported for an existing secret;
  delete and recreate instead.

**New server joins the org.** It has no ciphertext for any existing org secret,
so each reports `missing_on: [new_server]`. The UI prompts for re-entry (see
UI). Until then, an app on that server referencing the name gets compose's
empty or default value, and the agent logs a warning naming the variable, never
its value.

**Agent decrypt.** At render time, with its mTLS private key
(`crypto.DecryptWithPrivateKey(cfg.GetAgentKeyPath(), ct)`), into 0600 files
only. A ciphertext that fails to decrypt is skipped and reported (see sync).

No API route ever returns a secret value; secrets are listed as
`<encrypted>`.

## API

All routes are JWT/PAT-authenticated. Server-addressed routes use
`webutil.RequireServerAccess`; every `server_id` inside a body (including
`ciphertexts` keys) is ownership-checked. Agent-bound actions return 202; their
results arrive over SSE.

| Route | Behaviour |
|---|---|
| `GET /api/v1/org/list-shared-variables` | Org scope. Secrets as `<encrypted>`, each with `missing_on: [server_id]` and per-server sync state. |
| `POST /api/v1/org/save-shared-variable` | Upsert `{name, is_secret, value?, ciphertexts?, description, reload?: bool}`. |
| `POST /api/v1/org/delete-shared-variable` | `{id, reload?: bool}`. |
| `GET /api/v1/org/list-encryption-keys` | Keys for the browser fan-out (above). |
| `GET /api/v1/server/list-shared-variables?server_id=` | Server scope, plus the effective org values (read-only, `overridden` flag), built-in names and values, and the server's missing org secrets. |
| `POST /api/v1/server/save-shared-variable` | Server-scope upsert, same body as org. |
| `POST /api/v1/server/delete-shared-variable` | `{server_id, id, reload?: bool}`. |
| `POST /api/v1/shared-variables/resync` | `{server_id?}` or `{variable_id?}`, or neither for all org servers. Pushes the full snapshot to the servers in scope. 202. |
| `POST /api/v1/shared-variables/reload` | `{server_id, names?: [], app_ids?: []}`, or `{variable_id}` for every server the variable reaches. Sends `vars.reload`. 202. |

**Save and delete semantics.** The response is 200 once the database write
commits; nothing waits on agents. The API then bumps `vars_revision` and pushes
`vars.sync` to the affected servers. With `reload: true` (the save dialog's
"Reload affected apps now", default off), it follows each successful sync with
`vars.reload {names: [name]}`.

**SSE events** (each needs an `api.EventPayload` entry in
`handler/notification/routes.go`):

- `vars.synced {server_id, revision, undecryptable: [name]}`
- `vars.sync_failed {server_id, error}`
- `vars.reloaded {server_id, app_id, ok, error?, names: [name]}`, one per app.

`make openapi` after adding routes and events.

## Delivery: API to agent

**Command `vars.sync`.** The payload is the server's full effective set, defined
in `internal/domain/command`:

```
{ revision, server_id, server_name, org_id,
  org:    [{name, is_secret, value | ciphertext}],
  server: [{name, is_secret, value | ciphertext}] }
```

Only ciphertexts for the target server are included. The agent replies with
`{revision, undecryptable: [name]}`; the API marks undecryptable names as
missing on that server by deleting their ciphertext rows.

**Triggers:**

1. Any save or delete affecting the server.
2. **Agent connect.** Same trigger and retry as `internal/infra/bootstrap/domainsync.go`:
   dispatched when an agent comes online, retried on `ErrAgentNotConnected`.
   This is how an agent picks up values after start, reinstall or a network
   blip; the gRPC link is API-initiated, so agents don't pull.
3. **Heartbeat drift.** The agent reports its snapshot revision in its
   heartbeat. If it's behind `servers.vars_revision`, the API pushes again,
   which heals a missed push within one heartbeat with no polling timer.
   Planning verifies the heartbeat can carry the field; a capability field is
   the fallback.
4. Manual resync (`/shared-variables/resync`).

**Standalone.** Identical code path: `vars.sync` rides the `InProcessBridge`,
and the snapshot lives in the local `AGENT_DATA_DIR`.

## Agent

### On-disk layout

```
{AGENT_DATA_DIR}/
  vars/
    snapshot.json                -- 0600; last vars.sync: revision, ids, names,
                                 --   plain values, secrets as ciphertext only
  apps-data/{appID}/
    .env.shared                  -- gitignored, 0644: rendered plain values
    .env.shared.secrets          -- gitignored, 0600: rendered decrypted secrets
    (existing files unchanged)
```

- `snapshot.json` is written atomically (temp file, fsync, rename). A snapshot
  with a revision lower than the stored one is ignored, so a reconnect sync
  racing a change push can't roll values back.
- `snapshot.json` never holds plaintext.
- Both rendered files are added to the generated `.gitignore` next to
  `.env.secrets`. App history never records shared values, and a rollback
  deploys against the current set.

### Rendering

At render time the agent resolves one map per app (built-ins, then org, then
server; the later entry wins). It then **keeps only the names the app
references**, and splits the result into `.env.shared` (plain) and
`.env.shared.secrets` (decrypted). Writing only referenced names limits what a
compromised app folder exposes and makes the stale check a direct file
comparison. Built-ins are always written, whether referenced or not, since they
contain nothing sensitive.

Rendering happens only on the paths that already call `materializeSecrets`:
save, start, restart, redeploy, rollback, the source auto-update poller, and
`vars.reload`. **Never on `vars.sync`.**

Compose wiring, in `composeArgs` and `.winterflow/run.sh` (later files override
earlier ones; a missing file is omitted):

```
--env-file .env.shared  --env-file .env.shared.secrets  --env-file .env  --env-file .env.secrets
```

### Referenced names

A name is referenced by an app when `$NAME`, `${NAME}`, or any `${NAME…}` form
(`:-`, `-`, `:?`, `?`, `:+`, `+`) appears in:

- the compose file the app actually runs (the committed config, or
  `source/<compose file>` for git-sourced apps);
- the app's committed `.env`, since compose interpolates `.env` values (for
  example `URL=https://${BASE_DOMAIN}`).

`$$NAME` is an escape, not a reference. The referenced-name set per app is
cached in memory keyed by HEAD plus `source.lock`, so scans read files only for
apps that changed. No process is forked in this path. Deleting an app prunes its
entry alongside `Repository.forgetApp`.

### Stale detection

An app is **stale** when its rendered `.env.shared` / `.env.shared.secrets`
differ from what the current snapshot would render for it. The comparison is
done in memory (secrets decrypted, never hashed to disk). The agent recomputes
staleness after each `vars.sync` and after each render, using the cached
referenced-name sets. Re-encrypting an unchanged value, or changing a name the
app doesn't reference, leaves it fresh. Stopped apps are reported stale too;
they pick up new values on their next start.

The stale set is reported to the API with the app status the agent already
sends. It is cached and only fanned out on change, per the
`status.Cache.SetAppStatus` rule.

### Reload

Command `vars.reload {names?: [], app_ids?: []}`: with neither, "every stale
running app on this server". The handler queues work and replies immediately.

A single **background worker goroutine** per agent performs reloads:

- one app at a time, under the existing per-app lock;
- re-render, then `compose up -d` (compose recreates only services whose
  resolved config changed);
- only **running** apps are reloaded;
- a 10-minute deadline per app;
- **coalescing:** work arriving mid-run is merged; the worker re-evaluates the
  queue after the current app, so no app reloads twice for one change;
- deleting an app drops its queued work;
- exits on ctx cancel.

Each app's outcome is reported as `vars.reloaded`. Planning checks whether the
source auto-update poller's reporting channel can be reused. A failed reload
leaves the app on its previous containers, as a failed `compose up` does today,
and the app shows an error naming the variable change that triggered it.

The worker never runs inside the dispatcher, so the standalone
`InProcessBridge`'s single goroutine is never blocked by a reload pass.

## UI

**Server: new "Variables" tab** in `/settings`, next to Registries, Networks,
Volumes, Domains and Agent.

- Three groups:
  - *This server*: editable.
  - *Inherited from org*: read-only, links to the org page, marked
    "overridden" when a server variable shadows it.
  - *Built-in*: read-only `WF_*` with values.
- Add/edit dialog: name, value, secret toggle, description, and "Reload affected
  apps now" (default off). For an existing secret, the name, toggle and value
  are locked; "Replace value" sends a new value, anything else is delete and
  recreate.
- Banner when org secrets are missing here: "N org secrets aren't available on
  this server", with "Provide values".
- Per-row sync state (synced / pending / failed) from SSE.
- Actions: "Resync" (this server), "Reload stale apps (N)", per-row "Reload apps
  using this".

**Org: new `/org/variables` page**, linked next to Members.

- The same table without the inherited group.
- Each secret shows coverage: "all servers" or "missing on srv-3".
- "Provide value" dialog: encrypts for the missing servers only (gap fill) and
  names them.
- A new value encrypts for every keyed server and confirms first: "will be
  pushed to 3 servers (1 pending, no key yet)".
- Actions: "Resync all servers", per-row "Resync" and "Reload apps using this".

**App page.**

- Badge on stale apps: "Shared variables changed — reload to apply", with a
  Reload button.
- In the editor, under Variables: a collapsed, read-only "Shared variables
  available to this app" list (name and scope; never a secret value). An app
  variable with a shared name shows "overrides org/server value".
- The compose hint becomes "use ${VAR}, including org/server variables".

**Shared code:** `encryptForServers` in `web/src/lib/`, a `VariablesTable`
component used by both pages, strings in `locales`.

## Security and resource invariants

- Ownership: every server-addressed route and every `server_id` in a body is
  checked against the caller's org; 403 tests per route.
- Secrets: ciphertext only in the API and in `snapshot.json`; plaintext only in
  0600 per-app files; never returned by any route, never logged.
- No process forks on sync or stale detection; file scans are cached by HEAD.
- Every agent-side reload has a deadline; the worker is a single goroutine that
  selects on `ctx.Done()`.
- SSE fan-out only on change.

## Testing

**Go unit:**

- Resolver: precedence, plain/secret shadowing, reserved-name rejection,
  undecryptable secrets skipped and reported.
- Reference scanner: every `${NAME…}` form, `$$NAME`, names inside `.env`
  values; cache invalidation on HEAD / `source.lock` change.
- Stale detection: changed referenced name → stale; unreferenced change or
  re-encryption → fresh.
- Snapshot store: atomic write, 0600, older revision ignored.
- Reload worker: coalescing, deadline, stopped apps skipped, deletion prunes
  queued work, clean exit on cancel (`-race`).
- `composeArgs` and `run.sh`: exact env-file order, missing files omitted.
- ECIES round trip with a browser-format ciphertext fixture.

**Repository** (`dbtest.Open`, SQLite and Postgres via `make test-pg`):
uniqueness including the org-scope partial index, cascades on server delete,
`vars_revision` bumps, ciphertext purge on key change.

**Contract** (`internal/app/web/contract`): happy path per route, `probes` for
input validation, 403 for foreign servers (server-scope routes, `ciphertexts`
keys, resync, reload), 400 for incomplete coverage of a new value, gap fill
accepted, no response contains a secret. `TestParity` golden; `make openapi`.

**Usecase:** a save bumps revisions and dispatches `vars.sync` to the right
servers; agent connect triggers sync; a behind-revision heartbeat triggers a
re-push; `undecryptable` marks names missing; `reload: true` sends `vars.reload`
after sync.

**Web (Vitest):** `encryptForServers`; `VariablesTable` groups and overridden
marking; missing-coverage badge and the Provide-value dialog sending a partial
map; reload checkbox default off; stale badge and Reload.

**Manual, before PR:** on a throwaway instance, create an org secret, use it in
an app's compose, rotate it, see the app marked stale, reload, and verify the
new value inside the container. Register a second server and walk the missing
badge and Provide-value flow.

## Open items for planning

- Whether the heartbeat can carry `vars_revision` (fallback: a capability).
- Whether the auto-update poller's reporting channel can carry `vars.reloaded`
  and the stale set.
- Migration number, decided against whatever merges first among #12–#14.
