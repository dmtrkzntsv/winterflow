# Storages and backups — design

Date: 2026-10-10. Status: approved in conversation. This is sub-project **C** of
"winterflow as the runtime for twillingate cloud tenants" (A volumes, B domains
merged). It ships as two stacked PRs: **C1 storages**, **C2 backups**.

## Context

A twillingate tenant is one SQLite database in a named volume. twillingate-cloud
replicates every tenant database with one host-level Litestream service. On
winterflow, backups must work for any app, not just SQLite. Postgres, MySQL and
plain volumes come later behind the same interface. Today winterflow has no
object-storage code, no settings store, and never touches the user's compose
file beyond `-f` for git-sourced apps.

Constraints that shape the design:

- **The agent runs as the non-root `winterflow` user** under
  `ProtectSystem=strict`, writing only `AGENT_DATA_DIR` and the Docker socket.
  It cannot open files inside Docker volumes. It reaches them only through
  Docker.
- **Secrets never leave the agent in plaintext** (CLAUDE.md). The browser
  encrypts them to the agent key (ECIES).
- **Idle cost near zero.** No per-app execs in polled paths, and notifications
  only on change.

## Concepts

| Noun | Meaning |
|---|---|
| **Storage** | A place backups and apps can write to, registered per server: a local folder or an S3-compatible bucket. |
| **Recipe** | What *can* be backed up, declared by the template author in the compose file under `x-winterflow.backups`. A recipe never turns itself on. |
| **Override** | Per-deployment settings (on/off, storage, schedule, paths) in the app's `.winterflow/config.json`, edited in the UI. |
| **Backup** | One effective entry: the recipe with its override layered on top. It is identified by its **backup name**, unique within the app. |
| **Restore point** | One stored copy of a backup: a time, a size and a kind (`scheduled`, `manual`, `pre-restore`). |

---

# C1 — Storages

## Storage registration

A storage is stored per server, owned by the agent, like domains. The same
bucket or path may be registered on any number of servers.

```go
// internal/domain/model
type StorageType string // "local" | "s3"

type Storage struct {
    Name  string      `json:"name"`  // ^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$
    Type  StorageType `json:"type"`
    Local *LocalStorage `json:"local,omitempty"`
    S3    *S3Storage    `json:"s3,omitempty"`
}
type LocalStorage struct {
    Path string `json:"path"` // absolute, cleaned, not "/", not under AGENT_DATA_DIR
}
type S3Storage struct {
    Endpoint        string `json:"endpoint"` // https URL; http only for loopback/private addresses
    Region          string `json:"region"`
    Bucket          string `json:"bucket"`
    Prefix          string `json:"prefix"`   // optional, no leading "/", normalized to end with "/"
    AccessKeyID     string `json:"access_key_id"`
    SecretAccessKey string `json:"secret_access_key"` // ECIES ciphertext in transit and at rest; "<encrypted>" on read
    PathStyle       bool   `json:"path_style"` // MinIO and some providers
}
```

- **Files:** `{AGENT_DATA_DIR}/storages/<name>.json`, written atomically (temp
  file, fsync, rename) with mode 0600. The secret stays ECIES ciphertext at
  rest. It is decrypted only in memory, or into a 0600 gitignored env file at
  deploy (see "Storage variables"). `List` skips unreadable files and logs them,
  the same policy as the domains store.
- **The agent's "location" summary** is `path`, or `endpoint/bucket/prefix`. It
  is what the API index and the UI show.

### Agent commands (`internal/domain/command/storage.go`)

| Command | Request | Result |
|---|---|---|
| `storage.list` | — | `[]StorageInfo{Storage (secret masked), Location, Apps []string}`, where `Apps` lists the app ids that use the storage through compose variables (C2: also through a backup) |
| `storage.test` | `Storage` (secret ciphertext, or `<encrypted>` plus the name of an existing storage to reuse its secret) | `{ok, error}`; writes and deletes `<prefix>.winterflow-probe-<rand>` with a 30s deadline |
| `storage.save` | `Storage` | runs `test` first and refuses if it fails; then writes; then redeploys the apps whose compose files reference `WF_STORAGE_<name>_*` (C2: `WF_BACKUP_*` on a backup using it); returns `{redeployed []string, warnings []string}` |
| `storage.delete` | `{name}` | refused while `Apps` is non-empty, with an error naming them |

- On save, `<encrypted>` as the secret keeps the stored value. That's how
  editing works without re-entering the key.
- **Renaming is not supported.** To rename, delete the storage and add it
  again.

### Drivers (`internal/infra/backup/store`)

```go
type Store interface {
    Put(ctx context.Context, key string, r io.Reader) error // streamed
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    List(ctx context.Context, prefix string) ([]Object, error) // Object{Key, Size, ModTime}
    Delete(ctx context.Context, keys ...string) error
}
func Open(s model.Storage, secret string) (Store, error)
```

- **`local`:** files under `path`. `Put` writes `<key>.tmp`, fsyncs, then
  renames. Directories are created 0700 and files 0600.
- **`s3`:** `github.com/minio/minio-go/v7` (Apache-2.0) with streaming
  multipart upload. Every call carries the caller's context deadline.
- Keys are always relative and go through `safeRel`. A key never escapes
  `path` or `prefix`.

### Writable local folders

The systemd unit gains `ReadWritePaths=-/mnt` and `ReadWritePaths=-/media`,
the usual mount roots for USB disks and NFS/CIFS shares. A path outside those
fails `storage.test` with an error explaining the systemd drop-in that grants
it (`docs/recipes/backup-storage.md`). Run `scripts/install_test.sh` after
changing the unit.

### API side

- **Index:** table `server_storages(server_id, name, type, location,
  updated_at)`, primary key `(server_id, name)`, never holding a secret.
  Migration `20261011000001`.
- **Sync** works like `server_domains`:
  - `storage.list`'s result replaces the server's rows;
  - `save` upserts a row and `delete` removes it;
  - the agent-online sync in `bootstrap/domainsync.go` is generalized into
    `indexsync.go`, which runs both lists with the existing retry on
    `bus.ErrAgentNotConnected`.
- **Routes:** all check `RequireServerAccess`. Agent-bound routes return
  `202 {request_id}` and deliver their result as SSE `operation_result`.

  | Route | Notes |
  |---|---|
  | `GET /api/v1/storage/get-storages?server_id=` | from the index, synchronous |
  | `GET /api/v1/storage/list?server_id=` | agent-bound; refreshes the index |
  | `POST /api/v1/storage/test` | agent-bound |
  | `POST /api/v1/storage/save` | agent-bound |
  | `POST /api/v1/storage/delete` | agent-bound |

## Storage variables (`WF_STORAGE_*`)

An app's compose file may reference any storage on its server by name:

`${WF_STORAGE_<STORAGE>_<FIELD>}`

- `<STORAGE>` is the storage name upper-cased, with `-` replaced by `_`.
  Underscores aren't allowed in names, so two names never map to the same
  variable.
- `<FIELD>` is one of:

  | Field | Applies to |
  |---|---|
  | `TYPE`, `PREFIX` | every type |
  | `PATH` | `local` |
  | `ENDPOINT`, `REGION`, `BUCKET`, `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY` | `s3` |

  A field that doesn't apply to the storage's type resolves to empty.

Rules:

- **Only referenced values are materialized.** At deploy the agent scans the
  compose file for references with
  `\$\{?WF_(STORAGE|BACKUP)_[A-Z0-9_]+` and writes only those variables to the
  gitignored `.winterflow/storage.env` (0600). It passes that file as one more
  `--env-file`, ahead of `.env.secrets`. The file is deleted when there are no
  references. An app never receives credentials for a storage it doesn't
  mention.
- **A reference to a storage that doesn't exist on the server fails the save**
  with the variable name. The check runs:
  - **API-side** (400) when the compose file is in the request;
  - **agent-side** at deploy for every app, including git-sourced ones. This
    is what catches storages deleted or renamed out from under a compose file.
- **Reserved prefixes:** user variables may not start with `WF_STORAGE_` or
  `WF_BACKUP_`. The save returns 400, as for `COMPOSE_PROJECT_NAME` today.
- **The app's Variables panel** lists referenced storage variables as
  read-only rows: "provided by storage `hetzner`", with secrets shown as
  `<encrypted>`. They come from `app.get` (`StorageVariables []{Name, Storage,
  Secret bool}`), never with secret values.
- **Compose invocation** always names the compose file explicitly:
  `-f <compose file>` for every app, including non-source ones. The file is
  detected the same way compose does it, so C2 can append its own files.
  `run.sh` mirrors the same flags.

## UI (C1)

- **Settings → Storages:** a page next to Domains with the same layout
  (per-server sections, empty state, table).
  - **Columns:** name, type, location, used by (app chips), actions.
  - **Add storage** dialog:
    - type tabs *Local folder* and *S3-compatible*;
    - S3 fields: endpoint, region, bucket, prefix, access key, secret, path-style;
    - a **Test connection** button showing a pass or fail inline;
    - **Save**, which tests again.
  - **Edit:** the same dialog. The secret field is empty with the hint
    "leave empty to keep". If apps reference the storage, it warns "N apps
    will be redeployed".
  - **Delete:** disabled with a tooltip while the storage is in use, and
    otherwise confirmed.
  - **Secrets:** encrypted in the browser with the server key
    (`get-public-key`), the same as registry passwords, and cleared from React
    state when the dialog closes.
- **App editor → Variables:** the read-only storage rows. Autocomplete for
  `${WF_STORAGE_` in the compose editor is not needed.

---

# C2 — Backups

## Recipe (`x-winterflow.backups`)

```yaml
x-winterflow:
  backups:
    - name: database          # backup name: ^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$, unique in the app
      type: sqlite            # sqlite | custom  (postgres, mysql, volume: later)
      volume: data            # sqlite: a key under top-level `volumes:` of this compose file
      path: twillingate.db    # sqlite: relative path inside the volume (safeRel)
      every: 1h               # sqlite: Go duration, >= 5m; default 1h
      keep:                   # sqlite: default shown
        recent: 24h           #   keep every copy younger than this
        daily: 14             #   then the newest copy of each of the last N UTC days
        monthly: 6            #   then the newest copy of each of the last N UTC months
    - name: replica
      type: custom
      service: litestream     # custom: a service of this compose file
```

- **Parsing** lives in one package, `internal/domain/backup/recipe`, used by
  both sides: the API (400 at save when the compose file is in the request) and
  the agent (at save and deploy, which covers git-sourced apps, whose compose
  file comes from `source/`).
- **Errors** fail the save with the field path, e.g.
  `x-winterflow.backups[0].volume: "dat" is not a volume of this compose file`.
  These are errors:
  - unknown keys or types;
  - duplicate names;
  - dangling `volume` or `service` references;
  - `every` below 5m;
  - an unparsable `keep`.
- **A `custom` service must list the profile `wf-backup-<backup name>`** in its
  `profiles:`. Otherwise it would run with backups off, so its absence is an
  error too.
- **The resolved Docker volume name** comes from `docker compose config
  --format json`, run once per save or deploy, never in a polled path. It
  honors `name:`, `external:` and the project prefix.

## Overrides (`.winterflow/config.json` → `backups`)

```json
"backups": {
  "database": { "enabled": true, "storage": "hetzner", "every": "15m" },
  "extra":    { "type": "sqlite", "volume": "data", "path": "audit.db", "enabled": false }
}
```

- **An override may set** `enabled`, `storage`, `every`, `keep`, `volume` and
  `path` (and `service` for custom). An entry whose name is not in the recipe
  must carry `type`; it is a UI-added backup.
- **The effective backup** is the recipe, then the override field by field.
  `enabled` defaults to `false`.
- **`enabled: true` requires `storage`** naming a storage on the app's server.
  Otherwise the save gets a 400.
- **Validation** is the same as for the recipe, against the effective entry.
- **When the recipe changes**, overrides of fields the user didn't touch
  follow the new recipe. An override whose backup name has disappeared from
  the recipe is dropped at the next save, with a warning, unless it carries
  `type`.
- **The override is committed** with the app (history, rollback) and is part
  of `app.get` as `backups: []EffectiveBackup{Name, Type, Enabled, Storage,
  Every, Keep, Volume, Path, Service, FromRecipe map[field]bool}`.

### Backup variables (`WF_BACKUP_*`)

`${WF_BACKUP_<BACKUP>_<FIELD>}` takes the same fields as `WF_STORAGE_*`, taken
from **the storage the backup uses**. `PREFIX` is
`<storage prefix><app-id>/<backup name>/`.

| Reference | Result |
|---|---|
| a backup name not declared in the effective config | the save fails with a 400 |
| a declared backup that is **off** | every field resolves to empty (its service sits behind an off profile) |
| a declared backup that is **on** | the storage's values |

Enabling a `custom` backup adds `wf-backup-<backup name>` to the
`COMPOSE_PROFILES` the agent passes to compose. Disabling it removes the
profile and runs `up -d --remove-orphans`, so the service stops.

## Runtime: the agent runs built-in backups

There is no sidecar. The agent schedules every enabled built-in backup, and a
short-lived **helper container** gives it access to the volume for the
duration of one run.

### Scheduler (`internal/app/agent/backup`)

- **One goroutine** started next to the source poller, in both standalone and
  agent. It is independent of the command pipeline, so `InProcessBridge` is
  never blocked by a backup.
- **One timer** set to the earliest due time across all backups. There is no
  per-app ticker.
- **Next run:** `last_attempt + every`. For a backup never run, the time it
  was enabled plus a stable per-backup offset, a hash of `app-id/backup name`
  mod `every`, so 40 tenants don't fire together.
- **Runs are strictly one at a time**, in due order. A per-app lock is shared
  with restore and delete.
- **It reloads its plan** when an app is saved, rolled back, renamed or
  deleted, and when a storage is saved or deleted. Bookkeeping for a deleted
  app is pruned in `forgetApp`.
- **State** is kept in `{AGENT_DATA_DIR}/backups/state.json`, keyed by
  app/backup:
  - `last_attempt`, `last_success`, `last_error`, `last_size`;
  - the source `fingerprint`;
  - `enabled_at`.

  It is written atomically after every run and pruned on app delete.
- **On start** the agent removes leftover helpers with one call:
  `docker rm -f $(docker ps -aq --filter label=winterflow.role=backup-helper)`.

### One run (type `sqlite`)

1. **Start the helper:**

   ```
   docker run --rm --network none --memory 256m --cpu-shares 256
     --label winterflow.role=backup-helper --label winterflow.app=<app-id>
     -v <docker volume>:/data -v <agent executable>:/wf:ro
     alpine:3.20 /wf backup dump sqlite /data/<path> --since <fingerprint>
   ```

   - The image is the constant `alpine:3.20`, hard-coded.
   - The agent executable is `os.Executable()` with symlinks resolved. It is
     `CGO_ENABLED=0` and static, and it's the same binary as the agent, so its
     version always matches.
   - `standalone` and `agent` both gain the hidden `backup` subcommand
     (`internal/app/backup/cli`).
2. **Inside the helper:**
   - **Unchanged check:** stat `<path>` and `<path>-wal`. If size and mtime
     match `--since`, print `{"unchanged":true}` to stderr and exit 3.
   - **Missing file:** if `<path>` doesn't exist, exit 4 ("database file not
     found").
   - **Copy:** `VACUUM INTO '/tmp/snap.db'` through `modernc.org/sqlite`, with
     `busy_timeout=30s`. In WAL mode this doesn't block the app's writer.
   - **Check:** `PRAGMA quick_check` must be `ok`, and `sqlite_master` must be
     non-empty.
   - **Output:** write the snapshot to stdout, and the new fingerprint plus raw
     size as JSON on stderr's last line.
3. **The agent** reads stdout and compresses it with zstd
   (`github.com/klauspost/compress`, BSD/Apache) while computing its sha256.
   It streams the result to the storage as
   `<prefix><app-id>/<backup name>/<UTC yyyymmddThhmmssZ>-<kind>.db.zst`, then
   writes the manifest beside it (`….json`: sha256, size, raw size, type,
   agent version, fingerprint). The manifest is written last, so **a point
   without a manifest is incomplete**: it is ignored, and pruned after 24h.
4. **Prune** by `keep`:
   - `scheduled` copies follow the tiers;
   - `manual` and `pre-restore` copies are kept for 30 days;
   - the newest complete copy is never deleted.
5. **Record** state and update health.

**Deadlines:** 30 minutes for the whole run (dump plus upload), and 5 minutes
for the image pull on first use. A run past its deadline is killed
(`docker rm -f` of its helper) and counts as a failure.

**Exit 3** (unchanged) counts as a success without upload: `last_success`
advances, so an idle tenant stays `ok`.

### Health

Per backup, one of these states:

| State | Meaning |
|---|---|
| `off` | the backup is disabled |
| `running` | a run is in progress |
| `ok` | the last run succeeded within `2 × every` |
| `late` | no success within `2 × every`; never-run backups count from `enabled_at` |
| `failing` | the last attempt failed |

`command.AppStatus` gains `Backups []BackupHealth{Name, State, LastSuccess,
LastError}`. The agent's status reporter includes it, and `status.Cache`
treats a change in it like any other status change, so SSE `apps_status` is
sent only on change. `late` is evaluated when the reporter collects, every
30s, with no extra timer. Backup health never changes the app's own
`StatusCode`. `custom` backups report `off`, or `ok` while their service is
running, because winterflow can't observe their freshness.

### Restore points

`backup.points {app_id, backup}` lists `<prefix><app-id>/<backup>/` and pairs
objects with their manifests. It returns `[]{ID, Time, Kind, Size}`, newest
first. It runs only on request, with a 30s deadline.

### Back up now

`backup.run {app_id, backup}` queues a `manual` run at the front of the
scheduler. The command returns once the run is queued; the outcome arrives as
SSE `operation_result` when the run ends. Further pushes are not needed beyond
the existing health change.

### Restore (in place)

`backup.restore {app_id, backup, point}` runs on the backup goroutine under the
app lock. The command acknowledges immediately; the final outcome is delivered
as SSE `operation_result` with the same `request_id`.

1. Download the point and verify its sha256 against the manifest (to
   `{AGENT_DATA_DIR}/backups/tmp/`, deleted afterwards). A mismatch aborts
   before anything is stopped.
2. `compose stop`.
3. Run a `pre-restore` backup of the current file (a normal run with kind
   `pre-restore`, ignoring the unchanged check). **If it fails, abort**:
   `compose start`, and report. A missing database file skips this step.
4. Run the helper `/wf backup restore sqlite /data/<path>` with the
   decompressed copy on stdin. The helper:
   - writes `<path>.restore-tmp`;
   - runs `quick_check` on it and requires a non-empty `sqlite_master`;
   - `chown`s it to the uid:gid of the file it replaces (or of the parent
     directory if there is none) and copies the mode;
   - removes `<path>-wal` and `<path>-shm`;
   - renames the temp file over `<path>`.

   Any failure before the rename leaves the original in place.
5. `compose start`. Report `{pre_restore_point, restored_point}`.

### Delete

- **`app.delete` gains `delete_backups`** (default `false`). The handler,
  usecase and command all carry it.
- **With `delete_backups`,** after the app is torn down the agent deletes
  `<prefix><app-id>/` in every storage the app's backups used. A failure is a
  warning, not an error, like the volume sweep.
- **Without it,** the agent writes `<prefix><app-id>/.deleted`
  (`{"deleted_at": …, "app_name": …}`) in each of those storages.
- **Daily sweep** on the scheduler goroutine, once per 24h after start: for each
  storage, list `<prefix>*/.deleted` markers and delete each folder whose
  marker is older than `BACKUP_ORPHAN_RETENTION_DAYS`. The default is 180;
  `0` disables the sweep. **Folders without a marker are never deleted
  automatically.**

### Orphans

| Command | Does |
|---|---|
| `storage.orphans {name}` | lists `<prefix>` top-level folders whose app id is not on this server: `{AppID, AppName (from .deleted, if any), DeletedAt, PurgeAt, Copies, Bytes, Newest}`; on request only |
| `storage.delete-orphan {name, app_id}` | refused if the app exists on this server |

## API (C2)

All routes check `RequireServerAccess` and return `202` plus a `request_id`,
with the result as SSE `operation_result`.

| Route | Request |
|---|---|
| `POST /api/v1/app/update-backups` | `{server_id, app_id, backups: map[name]Override}`; commits a new revision; redeploys only if `COMPOSE_PROFILES` or `storage.env` changed; otherwise reloads the scheduler |
| `GET /api/v1/app/list-restore-points?server_id&app_id&backup` | |
| `POST /api/v1/app/run-backup` | `{server_id, app_id, backup}` |
| `POST /api/v1/app/restore-backup` | `{server_id, app_id, backup, point}` |
| `GET /api/v1/storage/list-orphans?server_id&name` | |
| `POST /api/v1/storage/delete-orphan` | `{server_id, name, app_id}` |
| `POST /api/v1/app/delete-app` | gains `delete_backups` |
| `GET /api/v1/app/get-apps-status` | gains `backups` per app |

## UI (C2)

- **App page → Backups section**, shown when the app has any backup:
  - **Each row** shows the backup name, type, storage, schedule, "last backup
    4 min ago" and a health badge (`ok`, `late`, `failing`, `running`, `off`).
  - **Enable switch:** opens a storage picker of this server's storages, with
    a link to Settings → Storages when there are none.
  - **Edit:** schedule, retention, volume and path. Each field is badged "from
    template" or "changed", with *Reset to template*.
  - **Back up now.**
  - **Restore:** a points list (time, kind, size), then a confirmation stating
    that the app stops and the current database is first saved as a
    `pre-restore` point.
  - **Add backup:** a UI-added `sqlite` entry.
- **App grid:** a small warning dot on cards with a `late` or `failing`
  backup.
- **Delete app dialog:** an "Also delete backups" checkbox, unchecked, under
  "Also delete volumes".
- **Settings → Storages → a storage → "Backups of apps not on this server":**
  the orphan table with per-row delete.

## Configuration

`.env.example` gains one variable:

- `BACKUP_ORPHAN_RETENTION_DAYS=180`: days to keep the backups of apps
  deleted without "delete backups"; `0` keeps them forever. Read by
  standalone and agent.

The helper image is the hard-coded constant `alpine:3.20`, not configurable.

## Testing

The usual gates apply: at least 80% of changed lines covered (Go and web), the
contract suite on SQLite and Postgres, and `api/openapi.yaml` up to date.

- **Unit (Go):**
  - storage validation and the store's atomic files;
  - variable scanning and resolution, including off-means-empty, the reserved
    prefixes and missing storages;
  - recipe parsing and validation, with every error path;
  - merging overrides on top of the recipe;
  - tiered retention, as table tests over synthetic point lists;
  - the scheduler on a fake clock: offsets, ordering, unchanged, deadline
    kill, plan reload, `forgetApp` pruning;
  - health transitions;
  - the orphan sweep.
- **Drivers:**
  - `local` on a temp directory;
  - `s3` against `github.com/johannesboyne/gofakes3` (MIT) in-process: put,
    list, get, delete, multipart, prefix confinement.
- **The `backup` subcommand,** called in-process:
  - dump while a second connection writes continuously in WAL mode, then
    restore and compare row counts;
  - restore refuses corrupt, empty and non-SQLite input;
  - ownership and mode are preserved;
  - exit codes 3 and 4.
- **Docker integration:** a dump and restore round trip through a real
  `docker run`, skipped when Docker is unavailable. It runs in CI.
- **Contract suite:** every new route gets a happy path plus `probes`
  (401, foreign-org 403, 400), with SSE results checked end to end. The fake
  agent learns `storage.*` and `backup.*`, and `server_storages` joins the
  parity golden.
- **Web (Vitest):** the Storages page and dialog (test, save, edit keeping the
  secret, delete-blocked), the Variables rows, the Backups section (enable
  with a storage, edit and reset, back up now, the restore confirmation), and
  the delete-app checkbox.

## Out of scope

| Item | Where it goes |
|---|---|
| `postgres` (`postgres:<major>-alpine`, `pg_dump -Fc` on the app network), `mysql`, `volume` types | later PRs on the same helper and stdin/stdout contract |
| Restoring into a new app; restoring an orphaned backup | D |
| More than one storage per backup | |
| Client-side encryption of backup objects | follow-up; until then, protection at rest is the provider's |
| A `list`/`restore` contract for `custom` backups | |
| A published helper image | |
| Per-storage orphan retention | |
