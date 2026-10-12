# C2b Restore, Delete, Orphans and the Backups UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user restore any copy of an app's backup in place (the app stopped, its current data saved first as a pre-restore copy), keep or delete an app's backups when the app is deleted (with a daily sweep of deleted apps' copies past the retention ceiling), see and delete the backups of apps no longer on the server, and manage all of it from the web UI.

**Architecture:** The hidden `backup` subcommand that C2a's helper container runs gains `restore sqlite` (write beside, check, keep owner and mode, rename) and `restore files` (extract into `.wf-restore-<id>/` at each mount root after a free-space check, then swap only the backup's paths). The Docker Compose orchestrator's `RestoreBackup` downloads and verifies the point, stops the app, makes the pre-restore copy through C2a's `RunBackup`, runs the restore helper with the decompressed copy on stdin and starts the app again; it runs as a job on C2a's backup scheduler (one job at a time, so never beside a backup of the app), which also runs the new daily sweep. `app.delete` marks (or purges) the app's folders in its backup storages; `storage.orphans`/`delete-orphan` list and delete folders of apps not on the server. The API gains three agent-bound routes; the web UI gains a Backups tab on the app page, a backups choice in the delete-app dialog, a warning dot on the app grid and an orphans dialog in Settings → Storages.

**Tech Stack:** Go 1.25 (`archive/tar`, `syscall.Statfs`, `context.WithCancelCause`, `modernc.org/sqlite`, `github.com/klauspost/compress/zstd` — all already direct dependencies), the `docker` / `docker compose` CLIs (args always `[]string`), React 19 + TypeScript + Vitest/Testing Library (no new packages; native `<select>` in the new dialogs, nothing under `web/src/components/ui/`). No database migration (the last is still `0010`), no new environment variable.

**Spec:** `docs/superpowers/specs/2026-10-10-storages-and-backups-design.md`, section "C2 — Backups" (from "Runtime: the agent runs built-in backups" to the end): "Restore (in place)", "Delete", "Orphans", "API (C2)", "UI (C2)", "Testing". C2a (plan `docs/superpowers/plans/2026-10-11-c2a-backup-engine.md`, ledger `.superpowers/sdd/2026-10-11-c2a-backup-engine/progress.md`) built everything else of C2; this plan, **C2b**, finishes it.

**Base:** branch `feat/backups` at **`b6ef213`** (C1 + C2a, final; PR #14). Every block below was applied task by task, in this order, to a copy of `b6ef213` and its tests run at each step; the tree after Task 12 is byte-identical to the verified one (including `api/openapi.yaml` and `routes.golden`). `Create` blocks are whole files; **replace** blocks are exact find → replace pairs, each `find` occurring exactly once in the file at that point (whitespace and tabs matter — copy them as shown; Go files are gofmt-clean). Verified on that tree: `make test` (race, coverpkg; Docker tests included), `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/...`, `make test-pg`, `make openapi` (no diff), the Docker restore test, `pnpm --dir web run test:coverage`, `lint`, `build`; diff coverage against `b6ef213`: Go 86.9 % (SQLite-only profile; CI adds the Postgres one), web 94.9 %.

## Global Constraints

- Fail closed: every new route checks `webutil.RequireServerAccess` before dispatching (403 for a foreign server, asserted by the contract probes); `server_id` alone is never authorization.
- Secrets never leave the agent in plaintext: restore reads storages through C2a's `openBackupStorage` (secret decrypted in memory only); no manifest, marker, log line or API payload carries one.
- Every network operation and docker exec is bounded: storage calls on the command pipeline run in `withDeadline(Value)` goroutines abandoned at their limit (delete-app's markers/purges: 2 min in all; orphans list: 1 min; delete-orphan: 2 min); a restore is bounded by `RestoreTimeout` (1 h) on the scheduler; restarting an app after a restore by its own 2-minute context; one sweep by 15 min (5 min per storage).
- Nothing blocks the single standalone `InProcessBridge` goroutine: `backup.restore` is `Detached()` and waits on the scheduler, like `backup.run`.
- No per-app exec in a polled path: the sweep runs once a day on the scheduler goroutine and lists each storage once; orphans are listed on request only.
- A restore never leaves an app with neither its old nor its new data: the pre-restore copy is made first (its failure aborts and restarts the app), the files helper swaps only after a complete, verified extraction, and every failure that leaves the data as it was starts the app again. A swap that failed half-way (or a files helper stopped from outside) leaves the app stopped and the error names the pre-restore point.
- Disk space is checked before extracting: the agent sums the archive's entries per helper mount (4 KiB blocks) and the helper refuses a mount with less free space.
- Per-app in-memory state is pruned on deletion: the scheduler's `Forget` drops queued restores and cancels a running one with `ErrAppDeleted` (C2a's `forgetApp` and state pruning stay as they are).
- `.env.example` documents every env var: this plan adds none (`BACKUP_RETENTION_DAYS` is C2a's).
- Coverage: ≥80 % of changed lines per language (`make diffcov DIFFCOV_BASE=b6ef213` once committed); the contract suite passes on SQLite and Postgres; `api/openapi.yaml` regenerated (`make openapi`) and `routes.golden` updated when routes change.
- Never touch `web/src/components/ui/**`; UI copy says "Add …" (Add backup, Add storage).
- Commits: Conventional Commits, each message ending with a blank line and exactly `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A restore stopped from outside.** At its 1-hour limit the scheduler kills the helper; a files helper may have been in the middle of its swap. The app must then stay stopped and the error name the pre-restore point (a sqlite restore is one rename, so its app is started again) — Task 4 `TestRestoreHelperStopped`.
2. **Deleting an app while its restore runs.** `Forget` cancels the restore; the restore's cleanup must not start the containers of an app that `compose down` is about to remove — the cancel cause `ErrAppDeleted` keeps it stopped — Task 4 `TestRestoreStartAgain`, Task 8 `TestForgetStopsARestore`.
3. **Nested mounts.** A files backup of `/data` with a volume at `/data/media` archives `data/media/` twice; restore must put each mount's data back in its own mount and keep the empty mount point in the outer one — Task 3 `TestRestoreFilesNestedMount`, Task 5 `TestRestoreThroughDocker`.
4. **A storage shared by two servers.** The daily sweep and the orphans view must never delete another server's folder by themselves: only folders with a `.deleted` marker are swept, and an app present here is never an orphan — Task 6 `TestSweepDeleted` (`other/`, `app-1/`), `TestOrphans`.
5. **Explicit empty overrides from the UI.** `update-backups` returns an explicit empty `paths`/`exclude` as absent; echoing effective backups back blindly would turn "whole volume" into "inherit the recipe's paths". The UI sends `[]` only for a field that overrides the recipe and omits untouched fields — Task 10 `backups.test.ts` ("sends an explicit empty paths or exclude only when it overrides the recipe"), Task 12 `backup-edit-dialog.test.tsx`.

## Spec rulings (ambiguities resolved here)

- **Restore needs the backup on.** A restore resolves the backup's storage and mounts and makes its pre-restore copy exactly like a run (`runnableBackup`), so a backup that is off can be listed and its copies deleted, but not restored (the UI says "Turn the backup on to restore").
- **A stopped app stays stopped.** `compose stop`/`compose start` happen only when the app's project was running before the restore (spec step 5 would otherwise start an app the user had stopped).
- **Restore's result** is one `operation_result` when it ends (like Back up now); the 202 is the acknowledgement. It is queued on the scheduler behind requests already waiting; a second restore of the same app while one is queued or running is refused at once.
- **Restore time limit:** 1 hour for download + pre-restore copy + put back (a run keeps its 30 minutes); the API awaits 2h 5m, like run-backup.
- **Pre-restore warnings** come back prefixed "the pre-restore copy: …"; a copy whose manifest names another type than the backup now has is refused ("the backup changed type").
- **Files paths missing from the copy** (a path that didn't exist when the copy was made) are left as they are, with a warning — never deleted.
- **Temp folders** of a killed restore (`.wf-restore-*`) are removed at the next restore of that mount; downloads in `backups/tmp` at agent start. Archive entries named `.wf-restore-*` at a mount's top are skipped.
- **Helper capabilities for restore:** C2a's set plus `CHOWN` and `FOWNER` (restoring owners and modes), mounts read-write; still no network, 256 MB, `no-new-privileges`.
- **Free space:** per mount, the sum of the archive's entries for that mount rounded up to 4 KiB blocks (folders one block), computed by the agent from the downloaded archive and passed as `--need`.
- **Which storages a deleted app's backups used:** every backup with a storage, on or off, custom included (a custom tool writes under `<app-id>/` through `WF_BACKUP_*_PREFIX`). With `delete_backups` the whole `<app-id>/` folder goes; `app.delete` reports `backups_deleted` like `volumes_deleted`, so the UI never claims a deletion an older agent ignored.
- **The sweep** applies the ceiling to a point by its own time and to any other object (a custom tool's files) by its mtime; a folder left with nothing but its marker loses the marker. First sweep one day after start, then daily ("once per 24h after start"); `0` disables it.
- **Orphans:** a top-level folder counts only when it holds a restore point or a `.deleted` marker, so an app's own files written through `WF_STORAGE_` never show up; `purge_at` is the newest object + the ceiling (the marker's date when nothing is left), absent without a marker or a ceiling. `delete-orphan` takes `server_id` in the body, as the spec writes it.
- **`app.get` carries what the UI needs:** `backup_targets` (the compose files' volumes and each service's volume/bind mounts, parsed from the YAML — no `docker compose config` exec) and `retention_days` (for "capped at N days on this server").
- **`FromRecipe`** marks every field holding the recipe's value, including one the recipe leaves to its default; the UI badges the rest "changed". The location (`volume`, `service`, `path`, `paths`) is edited and reset as one part.
- **The Backups tab is always shown** (between Configuration and History): an app without backups shows an empty state with Add backup — the only way to add a UI backup to an app whose compose file declares none. Added backups start off; their switch picks the storage.
- **`.winterflow/*.tmp` is never committed** (C2a carry-over: a crash during the atomic write of `config.json`); app files can't live in `.winterflow/`, so the glob takes nothing of theirs.

## File map

| File | Task | Change |
|---|---|---|
| `internal/domain/command/{command,backup,storage,app}.go`, `backup_test.go` | 1 | restore, orphans, delete-orphan, `delete_backups`, `backup_targets`, `retention_days` |
| `internal/domain/backup/recipe/{compose,effective}.go`, `targets.go` (+ test) | 1 | service mounts, `Targets()`, `FromRecipe` for defaults |
| `api/openapi.yaml` | 1, 7 | regenerated |
| `internal/app/backup/cli/cli.go` | 2, 3 | `Run` with stdin; `restore sqlite`; then `restore files`, exit 5 |
| `internal/app/backup/cli/sqlite.go`, `restore_sqlite.go` (+ test) | 2 | sqlite restore |
| `internal/app/backup/cli/restore_files.go` (+ test), `internal/infra/backup/helper/helper.go` | 3 | files restore, `ExitPartial` |
| `internal/infra/orchestrator/docker_compose/restore.go` (+ test), `backuprun.go`, `repository.go`, `gitrepo.go` (+ test) | 4 | the restore engine; `.winterflow/*.tmp` never committed |
| `internal/infra/orchestrator/docker_compose/restore_docker_test.go` | 5 | Docker round trips |
| `internal/infra/orchestrator/docker_compose/backupfolders.go` (+ test), `lifecycle.go`, five `*_test.go` call sites | 6 | delete_backups, markers, sweep, orphans, app.get extras |
| `internal/app/agent/dispatcher.go` | 6, 9 | `DeleteApp` flag; then the new handlers |
| `internal/domain/usecase/app/{backups,usecase}.go` (+ test), `internal/app/web/handler/app/{backups,lifecycle,routes}.go` (+ tests), `internal/app/web/contract/{fakeagent,matrix,restore}_test.go`, `internal/app/web/testdata/routes.golden` | 7 | three routes, delete_backups |
| `internal/app/agent/backup/scheduler.go` (+ test) | 8 | restore jobs, cancel cause, daily sweep |
| `internal/app/agent/backups.go`, `dispatcher_backups_test.go`, `CLAUDE.md` | 9 | wiring, docs |
| `web/src/lib/backups.ts` (+ test) | 10, 12 | types and helpers; then the form checks |
| `web/src/context/{use-backups.ts,apps-context-base.ts,apps-context.tsx}` (+ tests), `web/src/components/{app-grid,delete-app-dialog}.tsx` (+ tests), `web/src/lib/delete-app.ts` (+ test) | 10 | hook, health, grid dot, delete choice |
| `web/src/pages/app-details.tsx` | 10, 12 | delete dialog moved out; then the Backups tab |
| `web/src/components/{storage-dialog,orphans-dialog}.tsx`, `web/src/pages/settings-storages.tsx` (+ tests), `web/src/components/ingress-editor.test.tsx` | 11 | orphans, path-style, flaky test |
| `web/src/components/{app-backups,backup-storage-dialog,backup-edit-dialog,add-backup-dialog,restore-points-dialog}.tsx` (+ tests) | 12 | the Backups tab |

Tasks are strictly sequential: each one's replace blocks are written against the tree the previous tasks left (rows with two task numbers). Tasks 10–12 touch no Go file; they only need Task 7's routes to exist.

## How to run things

- Prefix Go commands with `export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b` (create it first) when `/tmp` is small; add `GOFLAGS=-buildvcs=false` if `go build` complains about VCS stamping in a worktree.
- One package: `go test ./internal/app/backup/cli/`; one test: add `-run TestName`. Full suite: `go test -p 2 ./...` (CI: `make test`, race + coverpkg).
- Contract guard as CI runs it: `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/...`; on Postgres: `make test-pg` (Docker; it removes its container).
- The Docker tests (`TestBackupThroughDocker`, `TestRestoreThroughDocker`) run when `docker info` works and `-short` is off; they build `winterflow/cmd/agent` with `CGO_ENABLED=0` and use `alpine:3.20`.
- Web: `CI=true pnpm --dir web install --frozen-lockfile`, then `CI=true pnpm --dir web run test` (or `pnpm --dir web exec vitest run <files>`), `run lint`, `run build` (then `touch web/dist/.gitkeep`; never commit `web/dist`). On a heavily loaded machine two older tests in `settings-storages.test.tsx` can hit Vitest's 5 s timeout; they pass when run alone.
- Regenerate: `make openapi`; the routes golden: `go test ./internal/app/web/ -run TestRoutesGolden -update`.
- Diff coverage once the tasks are committed: `make diffcov DIFFCOV_BASE=b6ef213`.

---

### Task 1: Command catalog: restore, orphans, delete_backups, backup targets

The wire types every later task uses, plus two recipe changes the UI needs: the compose file's volumes and each service's mounts (`recipe.Targets`, for Add backup's picker, served by `app.get`), and `FromRecipe` counting a field the recipe leaves to its default as the recipe's (so the UI can badge "from template" / "changed" truthfully).

**Files:**
- Create: `internal/domain/backup/recipe/targets.go`
- Create: `internal/domain/backup/recipe/targets_test.go`
- Modify: `internal/domain/backup/recipe/compose.go`
- Modify: `internal/domain/backup/recipe/effective.go`
- Modify: `internal/domain/command/app.go`
- Modify: `internal/domain/command/backup.go`
- Modify: `internal/domain/command/backup_test.go`
- Modify: `internal/domain/command/command.go`
- Modify: `internal/domain/command/storage.go`
- Regenerate: `api/openapi.yaml`

**Interfaces:**
- Consumes: nothing new (C2a's `recipe.BackupConfig` with `*[]string` paths/exclude and `recipe.List`).
- Produces:
  - `command.TypeBackupRestore = "backup.restore"` (`Detached()` true, like `backup.run`), `TypeStorageOrphans = "storage.orphans"`, `TypeStorageDeleteOrphan = "storage.delete-orphan"`.
  - `command.RestoreBackupRequest{AppID, Backup, Point}` (Validate: app id, backup name, `ValidPointID`) → `RestoreBackupResponse{AppID, Backup, RestoredPoint, PreRestorePoint, Warnings}` (JSON `restored_point`, `pre_restore_point,omitempty`).
  - `command.ListOrphansRequest{Name}` → `ListOrphansResponse{Name, Orphans []Orphan}`; `Orphan{AppID, AppName, DeletedAt, PurgeAt, Copies, Bytes, Newest}` (unix seconds; JSON `app_id`, `app_name`, `deleted_at`, `purge_at`, `copies`, `bytes`, `newest`).
  - `command.DeleteOrphanRequest{Name, AppID}` → `DeleteOrphanResponse{Name, AppID, Deleted int}`.
  - `command.DeleteAppRequest.DeleteBackups` (`delete_backups`), `DeleteAppResponse.BackupsDeleted` (`backups_deleted`).
  - `command.GetAppResponse.BackupTargets *recipe.Targets` (`backup_targets`), `RetentionDays int` (`retention_days`).
  - `recipe.Mount{Type, Source, Target}` (`type` is `volume` | `bind`), `recipe.ServiceMounts{Name, Mounts}`, `recipe.Targets{Volumes []string, Services []ServiceMounts}`, `recipe.Compose.Mounts map[string][]Mount`, `func (Compose) Targets() Targets` (sorted; never nil lists).

- [ ] **Step 1: Write the failing tests**

**Create `internal/domain/backup/recipe/targets_test.go`:**

```go
package recipe

import (
	"reflect"
	"testing"
)

func TestTargets(t *testing.T) {
	c := parse(t, `
services:
  web:
    image: example/web
    volumes:
      - data:/data
      - ./media:/app/media:ro
      - /srv/shared/:/shared/
      - ~/notes:/notes
      - ${HOST_DIR}:/host
      - /anonymous
      - type: tmpfs
        target: /tmp
      - type: volume
        source: cache
        target: /var/cache//app/
      - type: bind
        target: /no-source
      - {type: volume, source: data, target: relative}
  worker:
    image: example/worker
volumes:
  data:
  cache:
`, `
services:
  web:
    volumes:
      - other:/data
volumes:
  other:
`)
	got := c.Targets()
	want := Targets{
		Volumes: []string{"cache", "data", "other"},
		Services: []ServiceMounts{
			{Name: "web", Mounts: []Mount{
				{Type: "volume", Source: "other", Target: "/data"},
				{Type: "bind", Source: "${HOST_DIR}", Target: "/host"},
				{Type: "bind", Source: "./media", Target: "/app/media"},
				{Type: "bind", Source: "~/notes", Target: "/notes"},
				{Type: "bind", Source: "/srv/shared/", Target: "/shared"},
				{Type: "volume", Source: "cache", Target: "/var/cache/app"},
			}},
			{Name: "worker", Mounts: []Mount{}},
		},
	}
	// Sorted by target.
	want.Services[0].Mounts = []Mount{
		{Type: "bind", Source: "./media", Target: "/app/media"},
		{Type: "volume", Source: "other", Target: "/data"},
		{Type: "bind", Source: "${HOST_DIR}", Target: "/host"},
		{Type: "bind", Source: "~/notes", Target: "/notes"},
		{Type: "bind", Source: "/srv/shared/", Target: "/shared"},
		{Type: "volume", Source: "cache", Target: "/var/cache/app"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets =\n%+v\nwant\n%+v", got, want)
	}
	if empty := (Compose{}).Targets(); empty.Volumes == nil || empty.Services == nil {
		t.Fatalf("no compose file: %+v", empty)
	}
}

// A field the recipe leaves to its default is the recipe's value until an
// override sets it.
func TestFromRecipeCountsDefaults(t *testing.T) {
	c := parse(t, `
services:
  web: {image: example/web, volumes: [data:/data]}
volumes: {data: {}}
x-winterflow:
  backups:
    - {name: db, type: sqlite, volume: data, path: app.db}
`)
	got, err := Effective(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{FieldEvery, FieldKeep, FieldVolume, FieldService, FieldPath, FieldPaths, FieldExclude} {
		if !got[0].FromRecipe[f] {
			t.Errorf("%s is the recipe's (a default) but not marked so: %+v", f, got[0].FromRecipe)
		}
	}
	got, err = Effective(c, []BackupConfig{{Name: "db", Every: "1h", Keep: &Keep{Daily: ptr(3)}}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].FromRecipe[FieldEvery] || got[0].FromRecipe[FieldKeep] || !got[0].FromRecipe[FieldPath] {
		t.Fatalf("overridden fields: %+v", got[0].FromRecipe)
	}
}
```

**Modify `internal/domain/command/backup_test.go`** — replace:

```go
func TestDetached(t *testing.T) {
	if !TypeBackupRun.Detached() || TypeBackupPoints.Detached() || TypeAppSave.Detached() {
		t.Fatal("only backup.run waits for long work")
	}
```

with:

```go
func TestDetached(t *testing.T) {
	if !TypeBackupRun.Detached() || !TypeBackupRestore.Detached() || TypeBackupPoints.Detached() || TypeAppSave.Detached() ||
		TypeStorageOrphans.Detached() {
		t.Fatal("only backup.run and backup.restore wait for long work")
	}
}

func TestRestoreAndOrphanRequestsValidate(t *testing.T) {
	for _, r := range []interface{ Validate() error }{
		RestoreBackupRequest{AppID: "a1", Backup: "db", Point: "20261011T120000Z-scheduled"},
		ListOrphansRequest{Name: "disk"},
		DeleteOrphanRequest{Name: "disk", AppID: "a1"},
	} {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	for _, tc := range []struct {
		r    interface{ Validate() error }
		want string
	}{
		{RestoreBackupRequest{AppID: "..", Backup: "db", Point: "20261011T120000Z-manual"}, "invalid app id"},
		{RestoreBackupRequest{AppID: "a1", Backup: "DB", Point: "20261011T120000Z-manual"}, "is not a valid backup name"},
		{RestoreBackupRequest{AppID: "a1", Backup: "db", Point: "latest"}, "invalid restore point id"},
		{ListOrphansRequest{Name: "Disk"}, "storage name"},
		{DeleteOrphanRequest{Name: "Disk", AppID: "a1"}, "storage name"},
		{DeleteOrphanRequest{Name: "disk", AppID: "a/b"}, "invalid app id"},
	} {
		if err := tc.r.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.r, err, tc.want)
		}
	}
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test ./internal/domain/...
```

Expected: FAIL — build failed: `undefined: RestoreBackupRequest`, `undefined: Targets` (and friends).

- [ ] **Step 3: Implement**

**Create `internal/domain/backup/recipe/targets.go`:**

```go
package recipe

import (
	"path"
	"regexp"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// Mount is one mount of a service as its compose file writes it: a named
// volume (Source is the volume's key under volumes:) or a bind mount
// (Source is the host path as written, ./media or /srv/media). Anonymous
// volumes and tmpfs mounts hold nothing a backup can keep and are left out.
type Mount struct {
	Type   string `json:"type"` // volume | bind
	Source string `json:"source"`
	Target string `json:"target"`
}

// ServiceMounts is a service and its mounts, by target.
type ServiceMounts struct {
	Name   string  `json:"name"`
	Mounts []Mount `json:"mounts"`
}

// Targets is where a backup added in the UI can point: the compose files'
// volumes (the volume form) and each service's volume and bind mounts (the
// service form). Mount resolution at save checks the choice again.
type Targets struct {
	Volumes  []string        `json:"volumes"`
	Services []ServiceMounts `json:"services"`
}

// Targets lists the volumes and the services' mounts, sorted by name and
// target ([] for none).
func (c Compose) Targets() Targets {
	t := Targets{Volumes: []string{}, Services: []ServiceMounts{}}
	for v := range c.Volumes {
		t.Volumes = append(t.Volumes, v)
	}
	sort.Strings(t.Volumes)
	names := make([]string, 0, len(c.Services))
	for name := range c.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mounts := append([]Mount{}, c.Mounts[name]...)
		sort.Slice(mounts, func(i, j int) bool { return mounts[i].Target < mounts[j].Target })
		t.Services = append(t.Services, ServiceMounts{Name: name, Mounts: mounts})
	}
	return t
}

// volumeKeyRE is a volume key in the short syntax.
var volumeKeyRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// parseMount reads one entry of a service's volumes: the short syntax
// (source:target[:mode]) or the long one (type, source, target). ok is false
// for what a backup can't use (an anonymous volume, tmpfs, an unreadable
// entry).
func parseMount(n *yaml.Node) (Mount, bool) {
	var m Mount
	switch n.Kind {
	case yaml.ScalarNode:
		parts := strings.Split(n.Value, ":")
		if len(parts) < 2 || parts[0] == "" {
			return Mount{}, false
		}
		m.Source, m.Target = parts[0], parts[1]
		// As compose reads it: a name is a volume, anything else (./x, /x,
		// ~/x, an interpolated path) a bind mount.
		m.Type = "volume"
		if !volumeKeyRE.MatchString(m.Source) {
			m.Type = "bind"
		}
	case yaml.MappingNode:
		var long struct {
			Type   string `yaml:"type"`
			Source string `yaml:"source"`
			Target string `yaml:"target"`
		}
		if n.Decode(&long) != nil || long.Source == "" || (long.Type != "volume" && long.Type != "bind") {
			return Mount{}, false
		}
		m = Mount{Type: long.Type, Source: long.Source, Target: long.Target}
	default:
		return Mount{}, false
	}
	if !strings.HasPrefix(m.Target, "/") {
		return Mount{}, false
	}
	m.Target = path.Clean(m.Target)
	return m, true
}

// addMount adds m to a service's mounts; a later compose file's mount at the
// same target replaces the earlier one, as compose merges them.
func addMount(mounts []Mount, m Mount) []Mount {
	for i := range mounts {
		if mounts[i].Target == m.Target {
			mounts[i] = m
			return mounts
		}
	}
	return append(mounts, m)
}
```

**Modify `internal/domain/backup/recipe/compose.go`** — replace (1 of 3):

```go
	Services map[string][]string // service name -> profiles
	Recipe   []BackupConfig
```

with:

```go
	Services map[string][]string // service name -> profiles
	Mounts   map[string][]Mount  // service name -> its volume and bind mounts
	Recipe   []BackupConfig
```

**Modify `internal/domain/backup/recipe/compose.go`** — replace (2 of 3):

```go
func ParseCompose(files ...[]byte) (Compose, error) {
	out := Compose{Volumes: map[string]bool{}, Services: map[string][]string{}}
	for _, raw := range files {
		var doc struct {
			Services map[string]struct {
				Profiles []string `yaml:"profiles"`
			} `yaml:"services"`
```

with:

```go
func ParseCompose(files ...[]byte) (Compose, error) {
	out := Compose{Volumes: map[string]bool{}, Services: map[string][]string{}, Mounts: map[string][]Mount{}}
	for _, raw := range files {
		var doc struct {
			Services map[string]struct {
				Profiles []string    `yaml:"profiles"`
				Volumes  []yaml.Node `yaml:"volumes"`
			} `yaml:"services"`
```

**Modify `internal/domain/backup/recipe/compose.go`** — replace (3 of 3):

```go
			out.Services[name] = s.Profiles
		}
```

with:

```go
			out.Services[name] = s.Profiles
			for _, v := range s.Volumes {
				if m, ok := parseMount(&v); ok {
					out.Mounts[name] = addMount(out.Mounts[name], m)
				}
			}
		}
```

**Modify `internal/domain/backup/recipe/effective.go`** — replace:

```go

// fromRecipe names the fields of c that hold the recipe's value.
func fromRecipe(r, c BackupConfig) map[string]bool {
	from := map[string]bool{}
	set := func(name string, recipeHas, same bool) {
		if recipeHas && same {
			from[name] = true
		}
	}
	set(FieldEvery, r.Every != "", c.Every == r.Every)
	set(FieldKeep, r.Keep != nil, keepEqual(c.Keep, r.Keep))
	set(FieldVolume, r.Volume != "", c.Volume == r.Volume)
	set(FieldService, r.Service != "", c.Service == r.Service)
	set(FieldPath, r.Path != "", c.Path == r.Path)
	set(FieldPaths, r.Paths != nil, slices.Equal(List(c.Paths), List(r.Paths)))
	set(FieldExclude, r.Exclude != nil, slices.Equal(List(c.Exclude), List(r.Exclude)))
	return from
```

with:

```go

// fromRecipe names the fields of c that hold the recipe's value, including
// one the recipe leaves out (its default, or empty) and no override sets:
// the UI badges every other field as changed, with a reset to the recipe.
func fromRecipe(r, c BackupConfig) map[string]bool {
	from := map[string]bool{}
	set := func(name string, same bool) {
		if same {
			from[name] = true
		}
	}
	set(FieldEvery, c.Every == r.Every)
	set(FieldKeep, keepEqual(c.Keep, r.Keep))
	set(FieldVolume, c.Volume == r.Volume)
	set(FieldService, c.Service == r.Service)
	set(FieldPath, c.Path == r.Path)
	set(FieldPaths, slices.Equal(List(c.Paths), List(r.Paths)))
	set(FieldExclude, slices.Equal(List(c.Exclude), List(r.Exclude)))
	return from
```

**Modify `internal/domain/command/app.go`** — replace (1 of 3):

```go
	BackupsError string                   `json:"backups_error,omitempty"`
}
```

with:

```go
	BackupsError string                   `json:"backups_error,omitempty"`
	// BackupTargets are where a backup added in the UI can point (the
	// compose files' volumes and each service's mounts); nil when a compose
	// file can't be read.
	BackupTargets *recipe.Targets `json:"backup_targets,omitempty"`
	// RetentionDays is the server's BACKUP_RETENTION_DAYS (0: no ceiling).
	RetentionDays int `json:"retention_days,omitempty"`
}
```

**Modify `internal/domain/command/app.go`** — replace (2 of 3):

```go
	DeleteVolumes bool `json:"delete_volumes,omitempty"`
}
```

with:

```go
	DeleteVolumes bool `json:"delete_volumes,omitempty"`
	// DeleteBackups also deletes the app's backups (every copy, in every
	// storage its backups use). False keeps them, marked as a deleted app's,
	// for BACKUP_RETENTION_DAYS.
	DeleteBackups bool `json:"delete_backups,omitempty"`
}
```

**Modify `internal/domain/command/app.go`** — replace (3 of 3):

```go
	VolumesDeleted bool `json:"volumes_deleted,omitempty"`
}
```

with:

```go
	VolumesDeleted bool `json:"volumes_deleted,omitempty"`
	// BackupsDeleted is true only when the agent honoured delete_backups
	// (an agent that predates the flag keeps them).
	BackupsDeleted bool `json:"backups_deleted,omitempty"`
}
```

**Modify `internal/domain/command/backup.go`** — replace:

```go
	return false
}
```

with:

```go
	return false
}

// --- backup.restore ---------------------------------------------------------

// RestoreBackupRequest restores one restore point of a backup in place: the
// app is stopped, its current data saved as a pre-restore point, the point's
// data put back, and the app started again. The answer comes when the
// restore ends.
type RestoreBackupRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
	Point  string `json:"point"`
}

func (r RestoreBackupRequest) Validate() error {
	if err := validateBackupRef(r.AppID, r.Backup); err != nil {
		return err
	}
	if !ValidPointID(r.Point) {
		return errors.New("invalid restore point id")
	}
	return nil
}

// RestoreBackupResponse is a finished restore: RestoredPoint is the point put
// back; PreRestorePoint the copy of the data it replaced ("" when there was
// none to save: no database file, or none of the paths). Warnings are paths
// the point did not hold (left as they were) and files skipped.
type RestoreBackupResponse struct {
	AppID           string   `json:"app_id"`
	Backup          string   `json:"backup"`
	RestoredPoint   string   `json:"restored_point"`
	PreRestorePoint string   `json:"pre_restore_point,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}
```

**Modify `internal/domain/command/command.go`** — replace (1 of 3):

```go
	TypeStorageDelete Type = "storage.delete"

```

with:

```go
	TypeStorageDelete Type = "storage.delete"
	// Backups of apps that are not on this server (deleted, or another
	// server's) found in a storage: listed and deleted on request.
	TypeStorageOrphans      Type = "storage.orphans"
	TypeStorageDeleteOrphan Type = "storage.delete-orphan"

```

**Modify `internal/domain/command/command.go`** — replace (2 of 3):

```go
	TypeBackupDeletePoint Type = "backup.delete-point" // delete one restore point

```

with:

```go
	TypeBackupDeletePoint Type = "backup.delete-point" // delete one restore point
	TypeBackupRestore     Type = "backup.restore"      // restore a point in place; answers when the restore ends

```

**Modify `internal/domain/command/command.go`** — replace (3 of 3):

```go
// Detached reports whether a command's answer waits for long work that runs
// elsewhere (backup.run waits for its run on the backup scheduler). Such a
// command is dispatched off the standalone bridge's single goroutine, so it
// never holds up other commands.
func (t Type) Detached() bool { return t == TypeBackupRun }

```

with:

```go
// Detached reports whether a command's answer waits for long work that runs
// elsewhere (backup.run and backup.restore wait for their run on the backup
// scheduler). Such a command is dispatched off the standalone bridge's single
// goroutine, so it never holds up other commands.
func (t Type) Detached() bool { return t == TypeBackupRun || t == TypeBackupRestore }

```

**Modify `internal/domain/command/storage.go`** — replace:

```go
	Secret  bool   `json:"secret"`
}
```

with:

```go
	Secret  bool   `json:"secret"`
}

// --- storage.orphans, storage.delete-orphan ---------------------------------

// ListOrphansRequest lists the backups in a storage whose app is not on this
// server: a deleted app's, or another server's sharing the storage.
type ListOrphansRequest struct {
	Name string `json:"name"`
}

func (r ListOrphansRequest) Validate() error { return model.ValidateStorageName(r.Name) }

// Orphan is one app's backup folder in a storage, the app not being on this
// server. AppName and DeletedAt come from the folder's .deleted marker (an
// app deleted here); PurgeAt is when the daily sweep deletes the last copy
// (the newest copy plus BACKUP_RETENTION_DAYS), 0 without a marker (another
// server's folder is never deleted automatically) or without a ceiling.
// Times are unix seconds; Bytes counts every object in the folder.
type Orphan struct {
	AppID     string `json:"app_id"`
	AppName   string `json:"app_name,omitempty"`
	DeletedAt int64  `json:"deleted_at,omitempty"`
	PurgeAt   int64  `json:"purge_at,omitempty"`
	Copies    int    `json:"copies"`
	Bytes     int64  `json:"bytes"`
	Newest    int64  `json:"newest,omitempty"`
}

// ListOrphansResponse lists a storage's orphans by app id ([] for none).
type ListOrphansResponse struct {
	Name    string   `json:"name"`
	Orphans []Orphan `json:"orphans"`
}

// DeleteOrphanRequest deletes every object of an orphan's folder; the agent
// refuses when the app is on this server.
type DeleteOrphanRequest struct {
	Name  string `json:"name"`
	AppID string `json:"app_id"`
}

func (r DeleteOrphanRequest) Validate() error {
	if err := model.ValidateStorageName(r.Name); err != nil {
		return err
	}
	return validateAppID(r.AppID)
}

// DeleteOrphanResponse names the folder deleted and how many objects it held.
type DeleteOrphanResponse struct {
	Name    string `json:"name"`
	AppID   string `json:"app_id"`
	Deleted int    `json:"deleted"`
}
```

Regenerate `api/openapi.yaml` with `make openapi` (never edit it by hand).

- [ ] **Step 4: Run the tests and checks**

```bash
go test ./internal/domain/...
make openapi
go test ./internal/app/web/...   # TestOpenAPIUpToDate passes on the regenerated file
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/backup/recipe/targets.go \
  internal/domain/backup/recipe/targets_test.go \
  internal/domain/backup/recipe/compose.go \
  internal/domain/backup/recipe/effective.go \
  internal/domain/command/app.go \
  internal/domain/command/backup.go \
  internal/domain/command/backup_test.go \
  internal/domain/command/command.go \
  internal/domain/command/storage.go \
  api/openapi.yaml
git commit -F - <<'MSG'
feat(domain): restore, orphan and delete-backups commands; backup targets for the UI

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 2: Helper: `backup restore sqlite`

The hidden subcommand grows a restore side. `Main` keeps its signature and calls the new `Run(args, stdin, stdout, stderr)`. A sqlite restore writes `<path>.restore-tmp` from stdin, requires `quick_check` = ok and a non-empty schema (opened `immutable=1`, so no `-wal`/`-shm` appear as root), gives it the owner and mode of the file it replaces (or of the folder), removes the old `-wal`, `-shm`, `-journal` (SQLite would replay them onto the new file) and renames it into place. Any failure before the rename leaves the original untouched. Messages name the database by its backup path (`--entry`), never `/src/<n>`.

**Files:**
- Create: `internal/app/backup/cli/restore_sqlite.go`
- Create: `internal/app/backup/cli/restore_sqlite_test.go`
- Modify: `internal/app/backup/cli/cli.go`
- Modify: `internal/app/backup/cli/sqlite.go`

**Interfaces:**
- Consumes: C2a's `dumpSQLite`, `checkSnapshot`, `sqliteDSN`, `scrub`, `list`, test helpers `run`, `openDB`, `count`.
- Produces:
  - `cli.Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int` (`Main` = `Run` with `os.Stdin`).
  - `backup restore sqlite <path> [--entry <name>]`: exit 0 done, 1 refused/failed (original untouched), 2 usage.
  - `var lchown = os.Lchown` (tests replace it), `checkSnapshot(path string, extra ...string)`, `writeNew(path, in) (int64, error)`, `syncDir(dir)`, `removeSQLiteFiles(path)`.

- [ ] **Step 1: Write the failing tests**

**Create `internal/app/backup/cli/restore_sqlite_test.go`:**

```go
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// restore calls Run with stdin and decodes stderr's last line.
func restore(t *testing.T, stdin []byte, args ...string) (int, Result) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(append([]string{"restore"}, args...), bytes.NewReader(stdin), &stdout, &stderr)
	if stdout.Len() != 0 {
		t.Fatalf("a restore wrote to stdout: %q", stdout.String())
	}
	var res Result
	if code != ExitUsage {
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
			t.Fatalf("stderr's last line is not a result: %q", stderr.String())
		}
	}
	return code, res
}

// chowns records the owners restore gives files, for the rest of the test.
type chownLog struct {
	mu    sync.Mutex
	calls map[string][2]int
}

func recordChowns(t *testing.T) *chownLog {
	t.Helper()
	log := &chownLog{calls: map[string][2]int{}}
	prev := lchown
	lchown = func(p string, uid, gid int) error {
		log.mu.Lock()
		log.calls[filepath.Base(p)] = [2]int{uid, gid}
		log.mu.Unlock()
		return nil
	}
	t.Cleanup(func() { lchown = prev })
	return log
}

func (l *chownLog) of(name string) ([2]int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.calls[name]
	return c, ok
}

// A copy dumped while the app writes restores over a newer database: the
// rows are the copy's, the old -wal and -shm are gone, and the file keeps
// the owner and mode of the one it replaced.
func TestRestoreSQLite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if _, err := db.Exec("INSERT INTO items (body) VALUES ('x')"); err != nil {
			t.Fatal(err)
		}
	}
	code, copied, res := run(t, "dump", "sqlite", path)
	if code != ExitOK {
		t.Fatalf("dump: %d %+v", code, res)
	}
	for range 25 {
		if _, err := db.Exec("INSERT INTO items (body) VALUES ('y')"); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(path+ext, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chowns := recordChowns(t)
	code, res = restore(t, copied, "sqlite", path)
	if code != ExitOK || res.RawSize != int64(len(copied)) {
		t.Fatalf("restore: %d %+v", code, res)
	}
	if n := count(t, path); n != 50 {
		t.Fatalf("restored database has %d rows, want 50", n)
	}
	for _, ext := range []string{".restore-tmp", ".restore-tmp-wal", ".restore-tmp-shm"} {
		if _, err := os.Stat(path + ext); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", ext)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode())
	}
	if c, ok := chowns.of("app.db.restore-tmp"); !ok || c != [2]int{os.Getuid(), os.Getgid()} {
		t.Fatalf("owner given = %v (%v)", c, ok)
	}
}

// With no database file yet, the copy takes the folder's owner and a mode
// without its execute bits.
func TestRestoreSQLiteIntoAnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.db")
	db := openDB(t, src)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	recordChowns(t)
	path := filepath.Join(dir, "new.db")
	if code, res := restore(t, raw, "sqlite", path); code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 || count(t, path) != 1 {
		t.Fatalf("mode = %v", info.Mode())
	}
}

// Corrupt, empty and non-SQLite input is refused and the database is left
// as it was.
func TestRestoreSQLiteRefusesBadInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	good, _ := os.ReadFile(path)
	empty := filepath.Join(t.TempDir(), "empty.db")
	edb := openDB(t, empty)
	if _, err := edb.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	edb.Close()
	noTables, _ := os.ReadFile(empty)
	corrupt := append([]byte(nil), good...)
	for i := 100; i < len(corrupt); i++ {
		corrupt[i] ^= 0x5a
	}
	recordChowns(t)
	for name, in := range map[string][]byte{
		"nothing":   nil,
		"text":      []byte(strings.Repeat("not a database ", 100)),
		"corrupt":   corrupt,
		"no tables": noTables,
	} {
		code, res := restore(t, in, "sqlite", path)
		if code != ExitFailed || res.Error == "" {
			t.Errorf("%s: %d %+v", name, code, res)
		}
		if now, _ := os.ReadFile(path); !bytes.Equal(now, good) {
			t.Fatalf("%s: the database changed", name)
		}
		if left, _ := filepath.Glob(path + ".restore-tmp*"); len(left) != 0 {
			t.Fatalf("%s: left %v", name, left)
		}
	}
	if code, res := restore(t, good, "sqlite", dir, "--entry", "data/app.db"); code != ExitFailed || res.Error != "/data/app.db is not a file" {
		t.Fatalf("a folder: %d %+v", code, res)
	}
	if code, res := restore(t, good, "sqlite", filepath.Join(dir, "gone", "app.db"), "--entry", "/data/gone/app.db"); code != ExitFailed ||
		res.Error != "the folder of /data/gone/app.db: no such file or directory" {
		t.Fatalf("no folder: %d %+v", code, res)
	}
}

func TestRestoreSQLiteUsage(t *testing.T) {
	for _, args := range [][]string{{"sqlite"}, {"sqlite", "--x"}, {"sqlite", "/a", "/b"}, {"sqlite", "/a", "--since", "x"}} {
		var stderr bytes.Buffer
		if code := Run(append([]string{"restore"}, args...), strings.NewReader(""), io.Discard, &stderr); code != ExitUsage || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr.String())
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test ./internal/app/backup/cli/
```

Expected: FAIL — build failed: `undefined: Run`, `undefined: lchown`.

- [ ] **Step 3: Implement**

**Create `internal/app/backup/cli/restore_sqlite.go`:**

```go
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// lchown changes an owner without following a symlink (replaced in tests,
// which can't give files away).
var lchown = os.Lchown

func restoreSQLiteCmd(args []string, stdin io.Reader) (Result, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Result{}, fmt.Errorf("%w: the database path comes first", errUsage)
	}
	fs := flag.NewFlagSet("restore sqlite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	entry := fs.String("entry", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return Result{}, fmt.Errorf("%w: restore sqlite takes <path> [--entry <name>]", errUsage)
	}
	return restoreSQLite(args[0], strings.Trim(*entry, "/"), stdin)
}

// restoreSQLite replaces the database at path with the copy read from in.
// The copy is written beside it as <path>.restore-tmp and must pass
// quick_check with a non-empty schema; it takes the owner and mode of the
// file it replaces (of the folder, when there is none), the old -wal, -shm
// and -journal files are removed (SQLite would otherwise replay them onto
// the new file), and a rename puts it in place. Any failure before the
// rename leaves the original untouched. entry is the database's name in the
// backup, which messages use instead of the helper's /src/<n> path.
func restoreSQLite(path, entry string, in io.Reader) (Result, error) {
	shown := path
	if entry != "" {
		shown = "/" + entry
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return Result{}, fmt.Errorf("the folder of %s: %w", shown, scrub(err))
	}
	if !dir.IsDir() {
		return Result{}, fmt.Errorf("the folder of %s is not a folder", shown)
	}
	owner, perm := dir, dir.Mode().Perm()&0o666
	switch info, err := os.Lstat(path); {
	case err == nil && !info.Mode().IsRegular():
		return Result{}, fmt.Errorf("%s is not a file", shown)
	case err == nil:
		owner, perm = info, info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return Result{}, fmt.Errorf("%s: %w", shown, scrub(err))
	}
	tmp := path + ".restore-tmp"
	removeSQLiteFiles(tmp)
	defer removeSQLiteFiles(tmp)
	n, err := writeNew(tmp, in)
	if err != nil {
		return Result{}, err
	}
	if n == 0 {
		return Result{}, errors.New("the copy is empty")
	}
	// immutable: SQLite opens it without locks and never creates a -wal or
	// -shm beside it (as root).
	if err := checkSnapshot(tmp, "immutable=1"); err != nil {
		return Result{}, fmt.Errorf("the copy can't be restored: %w", err)
	}
	if st, ok := owner.Sys().(*syscall.Stat_t); ok {
		if err := lchown(tmp, int(st.Uid), int(st.Gid)); err != nil {
			return Result{}, fmt.Errorf("give the copy its owner: %w", scrub(err))
		}
	}
	if err := os.Chmod(tmp, perm); err != nil {
		return Result{}, scrub(err)
	}
	for _, ext := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(path + ext); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Result{}, fmt.Errorf("remove the old %s file: %w", ext, scrub(err))
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return Result{}, fmt.Errorf("put the copy in place: %w", scrub(err))
	}
	syncDir(filepath.Dir(path))
	return Result{RawSize: n}, nil
}

// removeSQLiteFiles removes a database file and the files SQLite keeps
// beside it.
func removeSQLiteFiles(path string) {
	for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + ext)
	}
}

// writeNew creates path (it must not exist; a symlink there is refused)
// with in's bytes, synced to disk.
func writeNew(path string, in io.Reader) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return 0, fmt.Errorf("write the copy: %w", scrub(err))
	}
	n, err := io.Copy(f, in)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("write the copy: %w", scrub(err))
	}
	return n, nil
}

// syncDir makes a rename in dir durable (best effort).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
```

**Modify `internal/app/backup/cli/cli.go`** — replace (1 of 4):

```go
//	backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
//
// Exit codes: 0 done, 3 unchanged since --since (nothing written), 4 nothing
// to back up (the database file or every path is missing), 2 bad usage,
// 1 any other failure.
package cli
```

with:

```go
//	backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
//	backup restore sqlite <path> [--entry <name>]    (the copy on stdin)
//
// Exit codes: 0 done, 3 unchanged since --since (nothing written), 4 nothing
// to back up (the database file or every path is missing), 2 bad usage,
// 1 any other failure (a restore then changed nothing).
package cli
```

**Modify `internal/app/backup/cli/cli.go`** — replace (2 of 4):

```go

// Main runs `backup <args>` and returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "dump" {
		return usage(stderr)
```

with:

```go

// Main runs `backup <args>` with the process's stdin and returns the exit
// code.
func Main(args []string, stdout, stderr io.Writer) int {
	return Run(args, os.Stdin, stdout, stderr)
}

// Run runs `backup <args>`: a dump writes to stdout, a restore reads stdin.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		return usage(stderr)
```

**Modify `internal/app/backup/cli/cli.go`** — replace (3 of 4):

```go
	)
	switch args[1] {
	case "sqlite":
		res, err = dumpSQLiteCmd(args[2:], stdout)
	case "files":
		res, err = dumpFilesCmd(args[2:], stdout)
	default:
```

with:

```go
	)
	switch args[0] + " " + args[1] {
	case "dump sqlite":
		res, err = dumpSQLiteCmd(args[2:], stdout)
	case "dump files":
		res, err = dumpFilesCmd(args[2:], stdout)
	case "restore sqlite":
		res, err = restoreSQLiteCmd(args[2:], stdin)
	default:
```

**Modify `internal/app/backup/cli/cli.go`** — replace (4 of 4):

```go
  backup dump sqlite <path> [--entry <name>] [--since <fingerprint>]
  backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]`)
	return ExitUsage
```

with:

```go
  backup dump sqlite <path> [--entry <name>] [--since <fingerprint>]
  backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
  backup restore sqlite <path> [--entry <name>]`)
	return ExitUsage
```

**Modify `internal/app/backup/cli/sqlite.go`** — replace:

```go

// checkSnapshot requires an intact copy with at least one schema object.
func checkSnapshot(path string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, "mode=ro"))
	if err != nil {
```

with:

```go

// checkSnapshot requires an intact copy with at least one schema object;
// extra are more DSN parameters.
func checkSnapshot(path string, extra ...string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, append([]string{"mode=ro"}, extra...)...))
	if err != nil {
```

- [ ] **Step 4: Run the tests and checks**

```bash
go test ./internal/app/backup/cli/
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/app/backup/cli/restore_sqlite.go \
  internal/app/backup/cli/restore_sqlite_test.go \
  internal/app/backup/cli/cli.go \
  internal/app/backup/cli/sqlite.go
git commit -F - <<'MSG'
feat(agent): hidden backup restore sqlite subcommand

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 3: Helper: `backup restore files` (extract beside, check room, swap)

The files restore takes the C2a dump's own `--src/--entry` addressing plus each path's mount root (`--root`), a restore id (`--id`, the temp folder `.wf-restore-<id>` at each mount root) and the room each mount needs (`--need <mount>=<bytes>`, computed by the agent from the archive). It tolerates everything the C2a dump writes: several mounts, nested mounts (the archive holds the nested mount point's folder twice — once as an empty folder of the outer mount, once as the inner mount's root: the entry goes to the inner mount, and the outer mount keeps the empty mount point), excludes (excluded entries are simply absent), and the zero padding of a file that shrank while it was archived (restored as archived). Entries are mapped to the target with the longest matching entry name; absolute, `..`, unclean, outside-the-paths and through-a-symlink entries are refused; any failure before the swap removes the temp folders and leaves every mount untouched (exit 1); a failure during the swap is exit 5 (`ExitPartial`).

**Files:**
- Create: `internal/app/backup/cli/restore_files.go`
- Create: `internal/app/backup/cli/restore_files_test.go`
- Modify: `internal/app/backup/cli/cli.go`
- Modify: `internal/infra/backup/helper/helper.go`

**Interfaces:**
- Consumes: Task 2's `Run`, `lchown`, `syncDir`; C2a's `list`, `warner`, `displayName`, `scrub`, `filesTree`, `run`.
- Produces:
  - `helper.ExitPartial = 5` (re-exported as `cli.ExitPartial`); `errPartial`.
  - `backup restore files --id <id> (--root <mount> --src <source> --entry <entry name>)... [--need <mount>=<bytes>...]` (the tar on stdin).
  - `var freeSpace func(path string) (uint64, error)` (statfs; tests replace it), `restoreTmpPrefix = ".wf-restore-"`.

- [ ] **Step 1: Write the failing tests**

**Create `internal/app/backup/cli/restore_files_test.go`:**

```go
package cli

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dumpOf archives sources the way a backup run does.
func dumpOf(t *testing.T, args ...string) []byte {
	t.Helper()
	code, out, res := run(t, append([]string{"dump", "files"}, args...)...)
	if code != ExitOK {
		t.Fatalf("dump: %d %+v", code, res)
	}
	return out
}

// snapshot is a tree as restore must reproduce it: every entry's kind,
// mode, mtime, link target and content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		v := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, _ := os.Readlink(p)
			v += " -> " + link
		case info.Mode().IsRegular():
			body, _ := os.ReadFile(p)
			v += " " + info.ModTime().UTC().Format(time.RFC3339Nano) + " " + string(body)
		case info.IsDir() && rel != ".":
			v += " " + info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		out[rel] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A two-mount backup restores over changed data: ownership, modes, mtimes
// and symlinks come back, files of the mounts outside the backup's paths
// are left alone, and no temp folder remains.
func TestRestoreFilesRoundTrip(t *testing.T) {
	src0, src1 := filesTree(t)
	if err := os.Remove(filepath.Join(src0, "media", "pipe")); err != nil {
		t.Fatal(err)
	}
	archive := dumpOf(t, "--src", src0+"/media", "--entry", "app/media", "--src", src1+"/config", "--entry", "data/config")
	before0, before1 := snapshot(t, filepath.Join(src0, "media")), snapshot(t, filepath.Join(src1, "config"))

	// The app changes things after the copy.
	if err := os.WriteFile(filepath.Join(src0, "media", "a.jpg"), []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src0, "media", "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(src1, "config")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src0, "other.txt"), []byte("sibling, changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	chowns := recordChowns(t)
	code, res := restore(t, archive, "files", "--id", "t1",
		"--root", src0, "--src", src0+"/media", "--entry", "app/media",
		"--root", src1, "--src", src1+"/config", "--entry", "/data/config/",
		"--need", src0+"=1000", "--need", src1+"=10")
	if code != ExitOK || res.RawSize != int64(len(archive)) || len(res.Warnings) != 0 {
		t.Fatalf("restore: %d %+v", code, res)
	}
	if after := snapshot(t, filepath.Join(src0, "media")); !mapsEqual(after, before0) {
		t.Fatalf("media:\n%v\nwant\n%v", after, before0)
	}
	if after := snapshot(t, filepath.Join(src1, "config")); !mapsEqual(after, before1) {
		t.Fatalf("config:\n%v\nwant\n%v", after, before1)
	}
	if body, _ := os.ReadFile(filepath.Join(src0, "other.txt")); string(body) != "sibling, changed" {
		t.Fatalf("a file outside the backup's paths changed: %q", body)
	}
	for _, root := range []string{src0, src1} {
		if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
			t.Fatalf("temp folders left: %v", left)
		}
	}
	if c, ok := chowns.of("a.jpg"); !ok || c != [2]int{os.Getuid(), os.Getgid()} {
		t.Fatalf("a.jpg owner = %v %v", c, ok)
	}
	if _, ok := chowns.of("link.jpg"); !ok {
		t.Fatal("the symlink's owner was not restored")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// The whole-volume form replaces every top-level entry of the volume.
func TestRestoreFilesWholeVolume(t *testing.T) {
	src0, _ := filesTree(t)
	if err := os.Remove(filepath.Join(src0, "media", "pipe")); err != nil {
		t.Fatal(err)
	}
	archive := dumpOf(t, "--src", src0, "--entry", "")
	before := snapshot(t, src0)
	if err := os.RemoveAll(filepath.Join(src0, "media")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src0, "extra.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A temp folder a killed restore left behind is cleaned up.
	if err := os.MkdirAll(filepath.Join(src0, restoreTmpPrefix+"old", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	recordChowns(t)
	if code, res := restore(t, archive, "files", "--id", "t2", "--root", src0, "--src", src0, "--entry", ""); code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	if after := snapshot(t, src0); !mapsEqual(after, before) {
		t.Fatalf("volume:\n%v\nwant\n%v", snapshot(t, src0), before)
	}
}

// A nested mount (a volume at /data/media inside the one at /data) is a
// second source: the dump archives data/media/ twice (the empty mount point
// in the outer volume, then the inner volume's root). Restore takes both
// and keeps the mount point in the outer volume.
func TestRestoreFilesNestedMount(t *testing.T) {
	root := t.TempDir()
	outer, inner := filepath.Join(root, "0"), filepath.Join(root, "1")
	for _, f := range []struct{ path, body string }{
		{"0/app.yml", "k: v"}, {"0/sub/notes.txt", "notes"}, {"1/photo.jpg", "jpeg"}, {"1/2026/a.jpg", "a"},
	} {
		writeFileT(t, root, f.path, f.body)
	}
	if err := os.Mkdir(filepath.Join(outer, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive := dumpOf(t, "--src", outer, "--entry", "data", "--src", inner, "--entry", "data/media")
	dirs := 0
	for tr := tar.NewReader(bytes.NewReader(archive)); ; {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if h.Name == "data/media/" {
			dirs++
		}
	}
	if dirs != 2 {
		t.Fatalf("the dump archived data/media/ %d times, want 2", dirs)
	}
	beforeOuter, beforeInner := snapshot(t, outer), snapshot(t, inner)
	for _, p := range []string{filepath.Join(outer, "sub"), filepath.Join(inner, "2026"), filepath.Join(outer, "media")} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	writeFileT(t, root, "1/stray.jpg", "x")
	recordChowns(t)
	code, res := restore(t, archive, "files", "--id", "n1",
		"--root", outer, "--src", outer, "--entry", "data",
		"--root", inner, "--src", inner, "--entry", "data/media")
	if code != ExitOK || len(res.Warnings) != 0 {
		t.Fatalf("%d %+v", code, res)
	}
	if after := snapshot(t, outer); !mapsEqual(after, beforeOuter) {
		t.Fatalf("outer:\n%v\nwant\n%v", after, beforeOuter)
	}
	if after := snapshot(t, inner); !mapsEqual(after, beforeInner) {
		t.Fatalf("inner:\n%v\nwant\n%v", after, beforeInner)
	}
}

// writeFileT writes body at root/rel, making its folders.
func writeFileT(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tarOf builds an archive by hand.
func tarOf(t *testing.T, entries ...*tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		body := h.PAXRecords["body"]
		h.PAXRecords = nil
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func file(name, body string) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, PAXRecords: map[string]string{"body": body}}
}

// An archive that is malformed, leaves the backup's paths or writes through
// a symlink is refused before anything is swapped: the mount is untouched
// and no temp folder remains.
func TestRestoreFilesRefusals(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	target := t.TempDir()
	good := file("app/media/new.txt", "new")
	for name, tc := range map[string]struct {
		archive []byte
		want    string
	}{
		"dot-dot":   {tarOf(t, good, file("app/media/../../etc/passwd", "x")), "is not a relative path"},
		"parent":    {tarOf(t, file("../x", "x")), "is not a relative path"},
		"absolute":  {tarOf(t, file("/app/media/x", "x")), "is not a relative path"},
		"outside":   {tarOf(t, good, file("app/other/x", "x")), "outside the backup's paths"},
		"prefix":    {tarOf(t, file("app/mediax/x", "x")), "outside the backup's paths"},
		"truncated": {tarOf(t, good)[:700], "read the archive"},
		"through a symlink": {tarOf(t,
			&tar.Header{Name: "app/media/link", Typeflag: tar.TypeSymlink, Linkname: target},
			file("app/media/link/evil", "x")), "through a symlink"},
		"over a symlink": {tarOf(t,
			&tar.Header{Name: "app/media/link", Typeflag: tar.TypeSymlink, Linkname: filepath.Join(target, "f")},
			file("app/media/link", "x")), "through a symlink"},
	} {
		recordChowns(t)
		code, res := restore(t, tc.archive, "files", "--id", "r1", "--root", root, "--src", root+"/media", "--entry", "app/media")
		if code != ExitFailed || !strings.Contains(res.Error, tc.want) {
			t.Errorf("%s: %d %+v, want %q", name, code, res, tc.want)
		}
		if body, err := os.ReadFile(filepath.Join(root, "media", "keep.txt")); err != nil || string(body) != "original" {
			t.Fatalf("%s: the mount changed: %q %v", name, body, err)
		}
		if _, err := os.Stat(filepath.Join(root, "media", "new.txt")); !os.IsNotExist(err) {
			t.Fatalf("%s: a file was restored", name)
		}
		if left, _ := filepath.Glob(filepath.Join(root, restoreTmpPrefix+"*")); len(left) != 0 {
			t.Fatalf("%s: temp folders left: %v", name, left)
		}
		if entries, _ := os.ReadDir(target); len(entries) != 0 {
			t.Fatalf("%s: wrote through the symlink", name)
		}
	}
}

// A mount without room for the copy beside the current data is refused
// before anything is written.
func TestRestoreFilesNeedsRoom(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/keep.txt", "original")
	prev := freeSpace
	freeSpace = func(p string) (uint64, error) {
		if p != root {
			t.Errorf("free space asked of %s", p)
		}
		return 1 << 20, nil
	}
	t.Cleanup(func() { freeSpace = prev })
	code, res := restore(t, tarOf(t, file("app/media/new.txt", "new")), "files", "--id", "r2",
		"--root", root, "--src", root+"/media", "--entry", "app/media", "--need", root+"=3000000")
	if code != ExitFailed || !strings.Contains(res.Error, "not enough free space to restore /app/media: it needs 2.9 MiB next to the current data, 1.0 MiB is free") {
		t.Fatalf("%d %+v", code, res)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("the mount changed: %v", entries)
	}
	if got := human(512); got != "512 B" {
		t.Fatalf("human(512) = %s", got)
	}
}

// A path the copy doesn't hold is left as it is, with a warning; anything
// that is not a file, folder or symlink is skipped; a file the dump padded
// (it shrank while archived) comes back at its archived size.
func TestRestoreFilesMissingPathAndOddEntries(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, root, "media/a.txt", "old")
	writeFileT(t, root, "config/c.yml", "untouched")
	recordChowns(t)
	archive := tarOf(t,
		&tar.Header{Name: "app/media/", Typeflag: tar.TypeDir, Mode: 0o755},
		file("app/media/a.txt", "abc\x00\x00"),
		&tar.Header{Name: "app/media/dev", Typeflag: tar.TypeChar},
		&tar.Header{Name: "app/media/" + restoreTmpPrefix + "x/", Typeflag: tar.TypeDir, Mode: 0o755},
	)
	code, res := restore(t, archive, "files", "--id", "r3",
		"--root", root, "--src", root+"/media", "--entry", "app/media",
		"--root", root, "--src", root+"/config", "--entry", "app/config")
	if code != ExitOK || len(res.Warnings) != 2 || !strings.Contains(res.Warnings[0], "/app/media/dev: skipped") ||
		res.Warnings[1] != "/app/config: not in the copy, left as it is" {
		t.Fatalf("%d %+v", code, res)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "media", "a.txt")); string(body) != "abc\x00\x00" {
		t.Fatalf("a.txt = %q", body)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "config", "c.yml")); string(body) != "untouched" {
		t.Fatalf("c.yml = %q", body)
	}
}

// A failure while swapping (here: the old path can't be removed) reports a
// partial restore.
func TestRestoreFilesPartialSwap(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	root := t.TempDir()
	writeFileT(t, root, "a/b/old.txt", "old")
	if err := os.Chmod(filepath.Join(root, "a"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "a"), 0o755) })
	recordChowns(t)
	code, res := restore(t, tarOf(t, file("x/new.txt", "new")), "files", "--id", "r4",
		"--root", root, "--src", root+"/a/b", "--entry", "x")
	if code != ExitPartial || !strings.Contains(res.Error, "/x was partly replaced") {
		t.Fatalf("%d %+v", code, res)
	}
}

// A path below a symlink is refused before extraction.
func TestRestoreFilesRefusesASymlinkedParent(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	code, res := restore(t, tarOf(t, file("x/new.txt", "new")), "files", "--id", "r5",
		"--root", root, "--src", root+"/a/b", "--entry", "x")
	if code != ExitFailed || res.Error != "refused: a folder above /x is not a folder (a symlink?), so it can't be restored there" {
		t.Fatalf("%d %+v", code, res)
	}
}

func TestRestoreFilesUsage(t *testing.T) {
	for _, args := range [][]string{
		{"files"}, {"files", "--id", "x", "--src", "/a", "--entry", "e"},
		{"files", "--root", "/a", "--src", "/a", "--entry", "e"},
		{"files", "--id", "Bad_Id", "--root", "/a", "--src", "/a", "--entry", "e"},
		{"files", "--id", "x", "--root", "/a", "--src", "/b", "--entry", "e"},
		{"files", "--id", "x", "--root", "rel", "--src", "rel/x", "--entry", "e"},
		{"files", "--id", "x", "--root", "/a", "--src", "/a", "--entry", "e", "--need", "/a"},
		{"files", "--id", "x", "--root", "/a", "--src", "/a", "--entry", "e", "--need", "/a=lots"},
	} {
		var stderr bytes.Buffer
		if code := Run(append([]string{"restore"}, args...), strings.NewReader(""), io.Discard, &stderr); code != ExitUsage || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr.String())
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test ./internal/app/backup/cli/
```

Expected: FAIL — build failed: `undefined: restoreTmpPrefix`, `undefined: freeSpace`, `undefined: ExitPartial`.

- [ ] **Step 3: Implement**

**Create `internal/app/backup/cli/restore_files.go`:**

```go
package cli

import (
	"archive/tar"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// errPartial is a files restore that failed while swapping (exit 5).
var errPartial = errors.New("partial")

// restoreTmpPrefix starts the temp folder a files restore extracts into, at
// the root of each mount: .wf-restore-<id>.
const restoreTmpPrefix = ".wf-restore-"

// freeSpace is the room left on the file system holding path (replaced in
// tests).
var freeSpace = func(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

var restoreIDRE = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// target is one path of a files restore: src in the helper, below (or at)
// the mount root, holding the archive's entries named entry ("" = the
// archive root, a whole volume).
type target struct {
	root  string
	src   string
	sub   string // src relative to root, "" = the mount itself
	entry string
}

func (t target) display() string { return displayName(t.entry) }

func restoreFilesCmd(args []string, stdin io.Reader) (Result, error) {
	fs := flag.NewFlagSet("restore files", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var roots, srcs, entries, needs list
	fs.Var(&roots, "root", "")
	fs.Var(&srcs, "src", "")
	fs.Var(&entries, "entry", "")
	fs.Var(&needs, "need", "")
	id := fs.String("id", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || len(srcs) == 0 || len(srcs) != len(entries) || len(srcs) != len(roots) {
		return Result{}, fmt.Errorf("%w: restore files takes --root <mount> --src <source> --entry <entry name> triples", errUsage)
	}
	if !restoreIDRE.MatchString(*id) {
		return Result{}, fmt.Errorf("%w: restore files needs --id (lowercase letters, digits, hyphens)", errUsage)
	}
	targets := make([]target, 0, len(srcs))
	for i, src := range srcs {
		root, src := filepath.Clean(roots[i]), filepath.Clean(src)
		sub, err := filepath.Rel(root, src)
		if !filepath.IsAbs(root) || err != nil || sub == ".." || strings.HasPrefix(sub, "../") {
			return Result{}, fmt.Errorf("%w: --src %q is not below its --root %q", errUsage, srcs[i], roots[i])
		}
		if sub == "." {
			sub = ""
		}
		targets = append(targets, target{root: root, src: src, sub: filepath.ToSlash(sub), entry: strings.Trim(entries[i], "/")})
	}
	need := map[string]uint64{}
	for _, n := range needs {
		i := strings.LastIndex(n, "=")
		bytes, err := strconv.ParseUint(n[i+1:], 10, 64)
		if i <= 0 || err != nil {
			return Result{}, fmt.Errorf("%w: --need takes <mount>=<bytes>", errUsage)
		}
		need[filepath.Clean(n[:i])] += bytes
	}
	return restoreFiles(targets, *id, need, stdin)
}

// restoreFiles replaces the targets' paths with the archive read from in.
// Everything is first extracted into .wf-restore-<id>/ at the root of each
// mount (after checking each mount has room for its share, need), refusing
// names that are absolute, hold "..", lie outside the targets, or would be
// written through a symlink; a failure there removes the temp folders and
// leaves every mount untouched. Then each path is swapped: what is there is
// removed and the restored entry moved in (for a whole mount, every entry
// at its root but the temp folder). A failure while swapping is errPartial.
// Paths the archive doesn't hold are left as they are, with a warning.
func restoreFiles(targets []target, id string, need map[string]uint64, in io.Reader) (Result, error) {
	var warns warner
	tmpName := restoreTmpPrefix + id
	roots := map[string]string{} // mount root -> its temp folder
	for _, t := range targets {
		roots[t.root] = filepath.Join(t.root, tmpName)
	}
	for _, t := range targets {
		if err := checkParents(t); err != nil {
			return Result{}, err
		}
	}
	for root := range roots {
		if err := checkRoom(root, need[root], targets); err != nil {
			return Result{}, err
		}
	}
	defer func() {
		for _, tmp := range roots {
			_ = os.RemoveAll(tmp)
		}
	}()
	for root, tmp := range roots {
		removeStale(root)
		if err := os.Mkdir(tmp, 0o700); err != nil {
			return Result{}, fmt.Errorf("make the restore folder: %w", scrub(err))
		}
	}
	x := extraction{targets: targets, roots: roots, warn: warns.add, seen: map[int]bool{}, rootHeaders: map[string]*tar.Header{}}
	n, err := x.extract(in)
	if err != nil {
		return Result{}, err
	}
	for i, t := range targets {
		if !x.seen[i] {
			warns.add(t.display() + ": not in the copy, left as it is")
		}
	}
	if err := swap(targets, roots, x.seen); err != nil {
		return Result{Warnings: warns.result()}, fmt.Errorf("%w: %w", err, errPartial)
	}
	for root, h := range x.rootHeaders {
		if err := applyMeta(root, h); err != nil {
			warns.add(fmt.Sprintf("/%s: owner, mode or times not restored: %v", name(h), scrub(err)))
		}
	}
	return Result{RawSize: n, Warnings: warns.result()}, nil
}

// checkParents refuses a path whose folders below the mount root are not
// real folders: a symlink there would move the restore elsewhere.
func checkParents(t target) error {
	if t.sub == "" {
		return nil
	}
	p := t.root
	parts := strings.Split(t.sub, "/")
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", t.display(), scrub(err))
		}
		if !info.IsDir() {
			return fmt.Errorf("refused: a folder above %s is not a folder (a symlink?), so it can't be restored there", t.display())
		}
	}
	return nil
}

// checkRoom requires the mount at root to have room for the restored copy
// next to the current data.
func checkRoom(root string, need uint64, targets []target) error {
	if need == 0 {
		return nil
	}
	free, err := freeSpace(root)
	if err != nil {
		return fmt.Errorf("check the free space for the restore: %w", scrub(err))
	}
	if free < need {
		name := root
		for _, t := range targets {
			if t.root == root {
				name = t.display()
				break
			}
		}
		return fmt.Errorf("not enough free space to restore %s: it needs %s next to the current data, %s is free", name, human(need), human(free))
	}
	return nil
}

func human(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// removeStale removes the temp folders a killed restore left at root.
func removeStale(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), restoreTmpPrefix) {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// extraction is one archive being extracted into the temp folders.
type extraction struct {
	targets     []target
	roots       map[string]string
	warn        func(string)
	seen        map[int]bool           // targets the archive holds entries of
	rootHeaders map[string]*tar.Header // a mount root's own entry, applied after the swap
	dirs        []extractedDir         // folders, their metadata applied last
}

type extractedDir struct {
	path string
	hdr  *tar.Header
}

// extract reads the archive into the temp folders; it returns the archive's
// size.
func (x *extraction) extract(in io.Reader) (int64, error) {
	cr := &countReader{r: in}
	tr := tar.NewReader(cr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("read the archive: %w", err)
		}
		if err := x.entry(h, tr); err != nil {
			return 0, err
		}
	}
	// Deepest first: a folder's mtime changes while its children are made,
	// and a read-only folder must stay writable until they are.
	for i := len(x.dirs) - 1; i >= 0; i-- {
		if err := applyMeta(x.dirs[i].path, x.dirs[i].hdr); err != nil {
			return 0, fmt.Errorf("restore /%s: %w", name(x.dirs[i].hdr), scrub(err))
		}
	}
	return cr.n, nil
}

// match is the target holding an archive name: the one with the longest
// entry that is the name or a folder above it. rel is the name below that
// entry.
func (x *extraction) match(name string, proper bool) (int, string, bool) {
	best, rel := -1, ""
	for i, t := range x.targets {
		var r string
		switch {
		case t.entry == "":
			r = name
		case name == t.entry && !proper:
		case strings.HasPrefix(name, t.entry+"/"):
			r = strings.TrimPrefix(name, t.entry+"/")
		default:
			continue
		}
		if best < 0 || len(t.entry) > len(x.targets[best].entry) {
			best, rel = i, r
		}
	}
	return best, rel, best >= 0
}

// entry extracts one archive entry.
func (x *extraction) entry(h *tar.Header, r io.Reader) error {
	name := strings.TrimSuffix(h.Name, "/")
	if name == "" || strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("refused: the archive entry %q is not a relative path", h.Name)
	}
	i, rel, ok := x.match(name, false)
	if !ok {
		return fmt.Errorf("refused: the archive entry /%s is outside the backup's paths", name)
	}
	x.seen[i] = true
	t := x.targets[i]
	if err := x.place(t, rel, h, r); err != nil {
		return err
	}
	// A nested mount's own folder is also an (empty) folder of the mount
	// above it: the dump archives it twice, once from each. Keep the outer
	// copy too, so the mount point is there after the swap.
	if h.Typeflag == tar.TypeDir && rel == "" && t.sub == "" {
		if j, outerRel, ok := x.match(name, true); ok && x.targets[j].root != t.root {
			return x.place(x.targets[j], outerRel, h, nil)
		}
	}
	return nil
}

// place writes entry h (rel below target t) into t's temp folder.
func (x *extraction) place(t target, rel string, h *tar.Header, r io.Reader) error {
	dest := path.Join(t.sub, rel)
	if dest == "" || dest == "." {
		if h.Typeflag == tar.TypeDir {
			x.rootHeaders[t.root] = h
		}
		return nil
	}
	if strings.HasPrefix(strings.SplitN(dest, "/", 2)[0], restoreTmpPrefix) {
		x.warn(fmt.Sprintf("/%s: skipped (a restore's temp folder)", h.Name))
		return nil
	}
	tmp := x.roots[t.root]
	full, err := makeParents(tmp, dest)
	if err != nil {
		return err
	}
	switch h.Typeflag {
	case tar.TypeDir:
		info, err := os.Lstat(full)
		switch {
		case err == nil && info.IsDir():
		case err == nil:
			if err := os.Remove(full); err != nil {
				return err
			}
			fallthrough
		default:
			if err := os.Mkdir(full, 0o700); err != nil {
				return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
			}
		}
		x.dirs = append(x.dirs, extractedDir{path: full, hdr: h})
	case tar.TypeReg:
		if r == nil {
			return nil
		}
		if info, err := os.Lstat(full); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("refused: /%s would be written through a symlink or over a folder", name(h))
		}
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
		_, err = io.CopyN(f, r, h.Size)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
		if err := applyMeta(full, h); err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
	case tar.TypeSymlink:
		if info, err := os.Lstat(full); err == nil {
			if info.IsDir() {
				return fmt.Errorf("refused: /%s is a folder and a symlink in the archive", name(h))
			}
			if err := os.Remove(full); err != nil {
				return err
			}
		}
		if err := os.Symlink(h.Linkname, full); err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
		if err := lchown(full, h.Uid, h.Gid); err != nil {
			return fmt.Errorf("restore /%s: %w", name(h), scrub(err))
		}
	default:
		x.warn(fmt.Sprintf("/%s: skipped (not a file, folder or symlink)", name(h)))
	}
	return nil
}

func name(h *tar.Header) string { return strings.TrimSuffix(h.Name, "/") }

// makeParents makes the folders above rel inside tmp, refusing a symlink on
// the way (it would lead the restore out of tmp), and returns rel's path.
func makeParents(tmp, rel string) (string, error) {
	p := tmp
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		p = filepath.Join(p, part)
		info, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(p, 0o700); err != nil {
				return "", err
			}
		case err != nil:
			return "", err
		case !info.IsDir():
			return "", fmt.Errorf("refused: /%s would be written through a symlink", rel)
		}
	}
	return filepath.Join(p, parts[len(parts)-1]), nil
}

// applyMeta gives a restored file or folder the archive's owner, mode and
// times (owner first: chown clears setuid bits).
func applyMeta(p string, h *tar.Header) error {
	if err := lchown(p, h.Uid, h.Gid); err != nil {
		return err
	}
	mode := h.FileInfo().Mode() & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
	if err := os.Chmod(p, mode); err != nil {
		return err
	}
	atime := h.AccessTime
	if atime.IsZero() {
		atime = h.ModTime
	}
	return os.Chtimes(p, atime, h.ModTime)
}

// swap moves each restored path into place, shortest first: a path inside
// another one moved with it.
func swap(targets []target, roots map[string]string, seen map[int]bool) error {
	order := make([]int, 0, len(targets))
	for i := range targets {
		if seen[i] {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return len(targets[order[a]].sub) < len(targets[order[b]].sub) })
	for _, i := range order {
		t := targets[i]
		tmp := roots[t.root]
		if t.sub == "" {
			if err := swapMount(t.root, tmp); err != nil {
				return fmt.Errorf("%s was partly replaced: %w", t.display(), scrub(err))
			}
			continue
		}
		src := filepath.Join(tmp, t.sub)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue // moved with a path above it
		}
		dst := filepath.Join(t.root, t.sub)
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("%s was partly replaced: %w", t.display(), scrub(err))
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("%s was removed but not restored: %w", t.display(), scrub(err))
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("%s was removed but not restored: %w", t.display(), scrub(err))
		}
	}
	for root := range roots {
		syncDir(root)
	}
	return nil
}

// swapMount replaces everything at a mount's root (but restore temp
// folders) with tmp's entries.
func swapMount(root, tmp string) error {
	old, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range old {
		if strings.HasPrefix(e.Name(), restoreTmpPrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	restored, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	for _, e := range restored {
		if err := os.Rename(filepath.Join(tmp, e.Name()), filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
```

**Modify `internal/app/backup/cli/cli.go`** — replace (1 of 5):

```go
//	backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
//	backup restore sqlite <path> [--entry <name>]    (the copy on stdin)
//
// Exit codes: 0 done, 3 unchanged since --since (nothing written), 4 nothing
// to back up (the database file or every path is missing), 2 bad usage,
// 1 any other failure (a restore then changed nothing).
package cli
```

with:

```go
//	backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
//	backup restore sqlite <path>                     (the copy on stdin)
//	backup restore files --id <id> (--root <mount> --src <source> --entry <entry name>)... [--need <mount>=<bytes>...]
//	                                                 (the archive on stdin)
//
// Exit codes: 0 done, 3 unchanged since --since (nothing written), 4 nothing
// to back up (the database file or every path is missing), 5 a files
// restore failed while swapping (the paths are partly replaced), 2 bad
// usage, 1 any other failure (a restore then changed nothing).
package cli
```

**Modify `internal/app/backup/cli/cli.go`** — replace (2 of 5):

```go
	ExitMissing   = helper.ExitMissing
)
```

with:

```go
	ExitMissing   = helper.ExitMissing
	ExitPartial   = helper.ExitPartial
)
```

**Modify `internal/app/backup/cli/cli.go`** — replace (3 of 5):

```go
		res, err = restoreSQLiteCmd(args[2:], stdin)
	default:
```

with:

```go
		res, err = restoreSQLiteCmd(args[2:], stdin)
	case "restore files":
		res, err = restoreFilesCmd(args[2:], stdin)
	default:
```

**Modify `internal/app/backup/cli/cli.go`** — replace (4 of 5):

```go
		res.Error, code = strings.TrimSuffix(err.Error(), ": "+errMissing.Error()), ExitMissing
	case err != nil:
```

with:

```go
		res.Error, code = strings.TrimSuffix(err.Error(), ": "+errMissing.Error()), ExitMissing
	case errors.Is(err, errPartial):
		res.Error, code = strings.TrimSuffix(err.Error(), ": "+errPartial.Error()), ExitPartial
	case err != nil:
```

**Modify `internal/app/backup/cli/cli.go`** — replace (5 of 5):

```go
  backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
  backup restore sqlite <path> [--entry <name>]`)
	return ExitUsage
```

with:

```go
  backup dump files (--src <source> --entry <entry name>)... [--exclude <pattern>...] [--since <fingerprint>]
  backup restore sqlite <path>
  backup restore files --id <id> (--root <mount> --src <source> --entry <entry name>)... [--need <mount>=<bytes>...]`)
	return ExitUsage
```

**Modify `internal/infra/backup/helper/helper.go`** — replace:

```go
	ExitMissing   = 4 // nothing to back up: the database file or every path is missing
)
```

with:

```go
	ExitMissing   = 4 // nothing to back up: the database file or every path is missing
	ExitPartial   = 5 // a files restore failed while swapping: its paths are partly replaced
)
```

- [ ] **Step 4: Run the tests and checks**

```bash
go test ./internal/app/backup/cli/
go test ./internal/infra/backup/...
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/app/backup/cli/restore_files.go \
  internal/app/backup/cli/restore_files_test.go \
  internal/app/backup/cli/cli.go \
  internal/infra/backup/helper/helper.go
git commit -F - <<'MSG'
feat(agent): hidden backup restore files subcommand: extract beside, check room, swap

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 4: Engine: restore a point in place

`RestoreBackup` follows the spec's steps: download the point into `{AGENT_DATA_DIR}/backups/tmp/` checking size and sha256 against the manifest (a mismatch, an unreadable archive or a manifest of another type aborts before anything stops), stop the app if it runs (`compose stop`), make a `pre-restore` copy (a normal `RunBackup`; a failure aborts and starts the app again; nothing to back up — exit 4, now `ErrNothingToBackUp` — only warns), run the restore helper (`docker run -i`, the mounts read-write, plus `CHOWN`/`FOWNER`) with the decompressed copy on stdin, start the app again. A failure that leaves the data as it was starts the app again on a context of its own (2 min) unless the restore's context was cancelled with `ErrAppDeleted` (the scheduler's Forget, Task 8). A partial swap — or a files helper stopped from outside, which may have been swapping — leaves the app stopped and names the pre-restore point. Also: `RemoveHelpers` (agent start) removes leftover downloads, and the orchestrator's commits never pick up `.winterflow/*.tmp` (a crash during fsutil.WriteFile of config.json — C2a carry-over).

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/restore.go`
- Create: `internal/infra/orchestrator/docker_compose/restore_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/backuprun.go`
- Modify: `internal/infra/orchestrator/docker_compose/gitrepo.go`
- Modify: `internal/infra/orchestrator/docker_compose/gitrepo_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/repository.go`

**Interfaces:**
- Consumes: Tasks 1–3 (`command.RestoreBackupResponse`, helper `restore` subcommands, `helper.ExitPartial`); C2a's `appBackups`, `runnableBackup`, `openBackupStorage`, `resolveBackups`, `listFolder`, `readManifest`, `ensureHelperImage`, `checkHelperVolumes`, `helperLimits`, `csvField`, `RunBackup`, test fixtures `newBackupRepo`, `fakeHelper`, `backupCompose`, `configJSON`.
- Produces:
  - `dockercompose.RestoreJob{AppID, Backup, Point string}`; `(*Repository).RestoreBackup(ctx, RestoreJob) (command.RestoreBackupResponse, error)` — refusals before anything stops are `ErrNotRunnable` for an unknown app/backup state, plain errors otherwise.
  - `dockercompose.ErrAppDeleted` (the cancel cause that keeps a stopped app stopped), `dockercompose.ErrNothingToBackUp` (RunBackup's exit-4 error).
  - Seams: `Repository.runRestoreHelper func(ctx, name string, args []string, stdin io.Reader) (exit int, stderr []byte, err error)` (default `runDockerRestoreHelper`), `runDockerIO`; `(*Repository).restoreTmpDir()`, `removeRestoreDownloads()`.
  - `tempArtifacts = ".winterflow/*.tmp"` in `neverCommitted`.

- [ ] **Step 1: Write the failing tests**

**Create `internal/infra/orchestrator/docker_compose/restore_test.go`:**

```go
package dockercompose

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
)

// fakeRestorer stands in for the restore helper's `docker run`: it records
// the arguments and stdin, and answers exit with stderr (a result line by
// default). onRun runs first.
type fakeRestorer struct {
	mu     sync.Mutex
	calls  [][]string
	stdin  []byte
	exit   int
	stderr string
	onRun  func()
}

func (f *fakeRestorer) run(_ context.Context, _ string, args []string, in io.Reader) (int, []byte, error) {
	body, err := io.ReadAll(in)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls, f.stdin = append(f.calls, args), body
	if f.onRun != nil {
		f.onRun()
	}
	if err != nil {
		return 1, nil, err
	}
	if f.stderr == "" {
		return f.exit, []byte(`{"raw_size":1,"warnings":["/app/media/dev: skipped"]}` + "\n"), nil
	}
	return f.exit, []byte(f.stderr), nil
}

// restoreRepo is a backupRepo whose app is up, with compose calls recorded
// and a fake restore helper.
type restoreRepo struct {
	*backupRepo
	restorer *fakeRestorer
	compose  []string
	up       bool
}

func newRestoreRepo(t *testing.T) *restoreRepo {
	t.Helper()
	rr := &restoreRepo{backupRepo: newBackupRepo(t), restorer: &fakeRestorer{}, up: true}
	rr.runRestoreHelper = rr.restorer.run
	rr.runningProjects = func(context.Context) (map[string]bool, error) {
		return map[string]bool{"wf-app-1": rr.up}, nil
	}
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, args[len(args)-1])
		return nil
	}
	return rr
}

// point makes a copy of name now and moves the clock on an hour.
func (rr *restoreRepo) point(t *testing.T, name string) string {
	t.Helper()
	res, err := rr.run(t, name, command.PointScheduled)
	if err != nil || res.Point == nil {
		t.Fatalf("backup: %+v, %v", res, err)
	}
	rr.clock = rr.clock.Add(time.Hour)
	return res.Point.ID
}

func (rr *restoreRepo) restore(ctx context.Context, name, point string) (command.RestoreBackupResponse, error) {
	return rr.RestoreBackup(ctx, RestoreJob{AppID: "app-1", Backup: name, Point: point})
}

// tmpLeft lists what remains in backups/tmp.
func (rr *restoreRepo) tmpLeft() []string {
	entries, _ := os.ReadDir(rr.restoreTmpDir())
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRestoreSQLitePoint(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.helper.payload = "NEWER-SNAPSHOT" // what the pre-restore copy saves
	rr.helper.fp = "sqlite:2"
	resp, err := rr.restore(context.Background(), "database", id)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RestoredPoint != id || resp.PreRestorePoint != "20261011T130000Z-pre-restore" || resp.AppID != "app-1" || resp.Backup != "database" ||
		!slices.Equal(resp.Warnings, []string{"the pre-restore copy: a note", "/app/media/dev: skipped"}) {
		t.Fatalf("response = %+v", resp)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
	if string(rr.restorer.stdin) != "SQLITE-SNAPSHOT" {
		t.Fatalf("the helper read %q", rr.restorer.stdin)
	}
	args := strings.Join(rr.restorer.calls[0], " ")
	for _, part := range []string{
		"run --rm -i --name wf-restore-",
		"--cap-drop ALL --cap-add DAC_OVERRIDE --cap-add SETUID --cap-add SETGID --security-opt no-new-privileges --cap-add CHOWN --cap-add FOWNER",
		"--label winterflow.role=backup-helper --label winterflow.app=app-1",
		"--mount type=volume,source=wf-app-1_data,target=/src/0 --mount",
		"alpine:3.20 /wf backup restore sqlite /src/0/app.db --entry app.db",
	} {
		if !strings.Contains(args, part) {
			t.Errorf("args lack %q:\n%s", part, args)
		}
	}
	if !slices.Contains(rr.stored(t), "app-1/database/20261011T130000Z-pre-restore.json") {
		t.Fatalf("no pre-restore point: %v", rr.stored(t))
	}
	if left := rr.tmpLeft(); len(left) != 0 {
		t.Fatalf("downloads left: %v", left)
	}
}

// A stopped app stays stopped: nothing to stop, nothing to start.
func TestRestoreLeavesAStoppedAppStopped(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.up = false
	if _, err := rr.restore(context.Background(), "database", id); err != nil {
		t.Fatal(err)
	}
	if len(rr.compose) != 0 {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A copy whose data doesn't match its manifest is refused before the app
// is touched.
func TestRestoreRefusesADamagedPoint(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	data := filepath.Join(rr.disk, "app-1", "database", id+".db.zst")
	raw, _ := os.ReadFile(data)
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(data, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "is damaged: its data does not match its manifest") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.compose) != 0 || len(rr.restorer.calls) != 0 || len(rr.tmpLeft()) != 0 {
		t.Fatalf("compose %v, helper %d, tmp %v", rr.compose, len(rr.restorer.calls), rr.tmpLeft())
	}
}

func TestRestoreRefusals(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	for name, tc := range map[string]struct {
		backup, point, want string
	}{
		"unknown point":  {"database", "20200101T000000Z-manual", "has no restore point 20200101T000000Z-manual"},
		"backup off":     {"replica", id, "the backup replica is off"},
		"unknown backup": {"nope", id, "the app has no backup nope"},
	} {
		if _, err := rr.restore(context.Background(), tc.backup, tc.point); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := rr.RestoreBackup(context.Background(), RestoreJob{AppID: "gone", Backup: "database", Point: id}); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("unknown app: %v", err)
	}
	// A point whose manifest names another type (the backup changed type).
	mpath := filepath.Join(rr.disk, "app-1", "database", id+".json")
	raw, _ := os.ReadFile(mpath)
	var m layout.Manifest
	_ = json.Unmarshal(raw, &m)
	m.Type = "files"
	raw, _ = json.Marshal(m)
	if err := os.WriteFile(mpath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rr.restore(context.Background(), "database", id); err == nil || !strings.Contains(err.Error(), "is a files copy, but the backup database is a sqlite backup now") {
		t.Fatalf("type change: %v", err)
	}
	if len(rr.compose) != 0 {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A failed pre-restore copy restores nothing and starts the app again;
// nothing to save (no database file yet) only warns.
func TestRestorePreRestoreCopy(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.helper.exit, rr.helper.stderr = helper.ExitFailed, `{"error":"the copy failed its integrity check"}`+"\n"
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "the pre-restore copy failed, so nothing was restored: the copy failed its integrity check") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) || len(rr.restorer.calls) != 0 {
		t.Fatalf("compose %v, helper %d", rr.compose, len(rr.restorer.calls))
	}

	rr.compose = nil
	rr.helper.exit, rr.helper.stderr = helper.ExitMissing, `{"error":"database file not found: /src/0/app.db"}`+"\n"
	resp, err := rr.restore(context.Background(), "database", id)
	if err != nil || resp.PreRestorePoint != "" || !slices.Contains(resp.Warnings, "nothing was saved before the restore: database file not found: /src/0/app.db") {
		t.Fatalf("missing database: %+v, %v", resp, err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) || len(rr.restorer.calls) != 1 {
		t.Fatalf("compose %v, helper %d", rr.compose, len(rr.restorer.calls))
	}
}

// A helper that fails before swapping leaves the data as it was: the app is
// started again. One that fails while swapping leaves the app stopped and
// names the pre-restore point.
func TestRestoreHelperFailures(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	rr.restorer.exit, rr.restorer.stderr = helper.ExitFailed, `{"error":"the copy can't be restored: file is not a database"}`+"\n"
	_, err := rr.restore(context.Background(), "database", id)
	if err == nil || err.Error() != "the restore failed, the app's data is unchanged: the copy can't be restored: file is not a database" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}

	rr.compose = nil
	rr.clock = rr.clock.Add(time.Hour)
	rr.restorer.exit, rr.restorer.stderr = helper.ExitPartial, `{"error":"/app/media was partly replaced: permission denied"}`+"\n"
	_, err = rr.restore(context.Background(), "database", id)
	if err == nil || !strings.Contains(err.Error(), "the restore stopped half-way: /app/media was partly replaced: permission denied. The app was left stopped; restore the pre-restore point 20261011T140000Z-pre-restore to go back") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}

	// Docker itself failing, and a helper with no result line.
	rr.compose = nil
	rr.restorer.exit, rr.restorer.stderr = 125, "docker: Error response from daemon\n"
	if _, err := rr.restore(context.Background(), "database", id); err == nil || !strings.Contains(err.Error(), "the helper exited with 125: docker: Error response from daemon") {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A helper stopped from outside (its time limit) may have been swapping: a
// files restore leaves the app stopped and names the pre-restore point; a
// database (one rename) starts the app again.
func TestRestoreHelperStopped(t *testing.T) {
	rr := newRestoreRepo(t)
	stopped := errors.New("stopped after its time limit: context deadline exceeded")
	rr.runRestoreHelper = func(_ context.Context, _ string, _ []string, in io.Reader) (int, []byte, error) {
		_, _ = io.Copy(io.Discard, in)
		return -1, nil, stopped
	}
	id := rr.point(t, "database")
	_, err := rr.restore(context.Background(), "database", id)
	if !errors.Is(err, stopped) || !strings.Contains(err.Error(), "the database is the old one or the restored one") {
		t.Fatalf("sqlite: %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
	rr.compose = nil
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	_ = tw.WriteHeader(&tar.Header{Name: "app/media/", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.Close()
	rr.helper.payload, rr.helper.fp = archive.String(), "files:1"
	id = rr.point(t, "media")
	_, err = rr.restore(context.Background(), "media", id)
	if !errors.Is(err, stopped) || !strings.Contains(err.Error(), "the paths may be partly replaced. The app was left stopped; restore the pre-restore point 20261011T140000Z-pre-restore to go back") {
		t.Fatalf("files: %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A restore stopped because its app is being deleted doesn't start it
// again; a start that fails is a warning once the data is back.
func TestRestoreStartAgain(t *testing.T) {
	rr := newRestoreRepo(t)
	id := rr.point(t, "database")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	rr.restorer.exit, rr.restorer.stderr = helper.ExitFailed, `{"error":"stopped"}`+"\n"
	rr.restorer.onRun = func() { cancel(ErrAppDeleted) }
	if _, err := rr.restore(ctx, "database", id); err == nil {
		t.Fatal("no error")
	}
	if !slices.Equal(rr.compose, []string{"stop"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}

	rr.compose, rr.restorer.onRun = nil, nil
	rr.restorer.exit, rr.restorer.stderr = 0, ""
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, args[len(args)-1])
		if args[len(args)-1] == "start" {
			return errors.New("port is already allocated")
		}
		return nil
	}
	resp, err := rr.restore(context.Background(), "database", id)
	if err != nil || !slices.Contains(resp.Warnings, "the data was restored, but the app did not start again: start the app again: port is already allocated") {
		t.Fatalf("%+v, %v", resp, err)
	}
	// A failed stop is reported and the app started again.
	rr.compose = nil
	rr.runCompose = func(_ context.Context, _ string, args []string) error {
		rr.compose = append(rr.compose, args[len(args)-1])
		if args[len(args)-1] == "stop" {
			return errors.New("daemon busy")
		}
		return nil
	}
	if _, err := rr.restore(context.Background(), "database", id); err == nil || err.Error() != "stop the app: daemon busy" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(rr.compose, []string{"stop", "start"}) {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// A files restore names its mounts, paths and the room each mount needs.
func TestRestoreFilesPoint(t *testing.T) {
	rr := newRestoreRepo(t)
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	for _, h := range []*tar.Header{
		{Name: "app/media/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "app/media/a.jpg", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5000},
		{Name: "app/media/b.jpg", Typeflag: tar.TypeReg, Mode: 0o644, Size: 10},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(make([]byte, h.Size)); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	rr.helper.payload, rr.helper.fp = archive.String(), "files:1"
	id := rr.point(t, "media")
	if _, err := rr.restore(context.Background(), "media", id); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(rr.restorer.calls[0], " ")
	if !strings.Contains(args, "--mount type=bind,source=/data/apps-data/app-1/media,target=/src/0 --mount") ||
		!strings.Contains(args, "/wf backup restore files --id ") ||
		!strings.HasSuffix(args, "--root /src/0 --src /src/0 --entry app/media --need /src/0=16384") {
		t.Fatalf("args = %s", args)
	}
	if !bytes.Equal(rr.restorer.stdin, archive.Bytes()) {
		t.Fatal("the helper did not get the archive")
	}
	// An archive that can't be read is refused before the app stops.
	rr.compose = nil
	rr.helper.fp = "files:2"
	rr.helper.payload = "not a tar archive, just text long enough to be a header block for tar reading purposes ........" + strings.Repeat(".", 500)
	id = rr.point(t, "media")
	if _, err := rr.restore(context.Background(), "media", id); err == nil || !strings.Contains(err.Error(), "archive can't be read") {
		t.Fatalf("err = %v", err)
	}
	if len(rr.compose) != 0 {
		t.Fatalf("compose calls = %v", rr.compose)
	}
}

// Downloads left by an agent that stopped mid-restore go at the next start.
func TestRemoveRestoreDownloads(t *testing.T) {
	r := newTestRepo(t)
	writeFileT(t, r.restoreTmpDir(), "20261011T120000Z-manual-abc.zst", "partial")
	r.removeRestoreDownloads()
	if _, err := os.Stat(r.restoreTmpDir()); !os.IsNotExist(err) {
		t.Fatalf("backups/tmp is still there: %v", err)
	}
}
```

**Modify `internal/infra/orchestrator/docker_compose/gitrepo_test.go`** — replace:

```go
		}
	}
}
```

with:

```go
		}
	}
}

// The temp file of an atomic write a crash left in .winterflow/ is never
// committed; a .tmp file of the app's own is.
func TestGitCommitAllExcludesAtomicWriteLeftovers(t *testing.T) {
	dir := t.TempDir()
	if err := gitEnsure(dir); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, dir, ".winterflow/config.json", "{}\n")
	writeFileT(t, dir, ".winterflow/config.json.tmp", "{\"half\n")
	writeFileT(t, dir, "data/cache.tmp", "the app's\n")
	if _, err := gitCommitAll(dir, "crashed write"); err != nil {
		t.Fatal(err)
	}
	if tracked(t, dir, ".winterflow/config.json.tmp") || !tracked(t, dir, ".winterflow/config.json") || !tracked(t, dir, "data/cache.tmp") {
		t.Fatal("wrong files committed")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test -short ./internal/infra/orchestrator/docker_compose/
```

Expected: FAIL — build failed: `rr.runRestoreHelper undefined`, `undefined: RestoreJob`, `undefined: ErrAppDeleted`.

- [ ] **Step 3: Implement**

**Create `internal/infra/orchestrator/docker_compose/restore.go`:**

```go
package dockercompose

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
)

// ErrAppDeleted is the cause a restore's context is cancelled with when its
// app is being deleted: the app is then not started again.
var ErrAppDeleted = errors.New("the app is being deleted")

// restartTimeout bounds starting an app again after a restore, on a context
// of its own (the restore's may be over).
const restartTimeout = 2 * time.Minute

// restoreLimits are a restore helper's: a dump's, plus what putting files
// back needs (give them their owner; set the mode and times of files it
// doesn't own).
var restoreLimits = append(slices.Clone(helperLimits), "--cap-add", "CHOWN", "--cap-add", "FOWNER")

// RestoreJob is one restore: Point of the app's backup.
type RestoreJob struct {
	AppID  string
	Backup string
	Point  string
}

// restoreTmpDir holds restore downloads ({AGENT_DATA_DIR}/backups/tmp).
func (r *Repository) restoreTmpDir() string {
	return filepath.Join(r.cfg.GetAgentDataDir(), "backups", "tmp")
}

// removeRestoreDownloads removes the downloads a restore cut short (the
// agent stopped mid-restore) left in backups/tmp.
func (r *Repository) removeRestoreDownloads() {
	if err := os.RemoveAll(r.restoreTmpDir()); err != nil {
		r.log.Warn("remove leftover restore downloads", "error", err)
	}
}

// RestoreBackup puts a restore point back in place:
//
//  1. the point is downloaded and its sha256 checked against its manifest
//     (a mismatch aborts before anything is stopped);
//  2. a running app is stopped (compose stop);
//  3. the current data is saved as a pre-restore point (a normal run of
//     kind pre-restore); when that fails the app is started again and
//     nothing is restored (nothing to save — no database file, none of the
//     paths — skips it);
//  4. the helper puts the copy back (sqlite: replaces the file; files:
//     extracts beside the data, checking free space, then swaps);
//  5. the app is started again.
//
// On any failure that leaves the data as it was, the app is started again
// (unless the restore was stopped because the app is being deleted). A
// files restore that failed half-way through its swap leaves the app
// stopped, and the error names the pre-restore point to go back to. ctx
// bounds the whole restore.
func (r *Repository) RestoreBackup(ctx context.Context, job RestoreJob) (command.RestoreBackupResponse, error) {
	resp := command.RestoreBackupResponse{AppID: job.AppID, Backup: job.Backup, RestoredPoint: job.Point}
	if !r.appExists(job.AppID) {
		return resp, notRunnable("app " + job.AppID + " not found")
	}
	dir := r.appDataDir(job.AppID)
	files := r.appComposeFiles(dir)
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return resp, err
	}
	b, err := runnableBackup(cfg.backups, job.Backup)
	if err != nil {
		return resp, err
	}
	st, err := r.openBackupStorage(b.Storage)
	if err != nil {
		return resp, err
	}
	plans, err := r.resolveBackups(ctx, job.AppID, files, []recipe.EffectiveBackup{b})
	if err != nil {
		return resp, err
	}
	plan := plans[b.Name]
	points, err := listFolder(ctx, st, layout.Folder(job.AppID, b.Name))
	if err != nil {
		return resp, err
	}
	i := slices.IndexFunc(points, func(p layout.Point) bool { return p.ID == job.Point && p.Complete() })
	if i < 0 {
		return resp, fmt.Errorf("the backup %s has no restore point %s", b.Name, job.Point)
	}
	point := points[i]
	m, err := readManifest(ctx, st, point.ManifestKey)
	if err != nil {
		return resp, fmt.Errorf("read the restore point's manifest: %w", err)
	}
	if m.Type != b.Type {
		return resp, fmt.Errorf("the restore point is a %s copy, but the backup %s is a %s backup now", m.Type, b.Name, b.Type)
	}
	if err := r.ensureHelperImage(ctx); err != nil {
		return resp, err
	}
	if err := r.checkHelperVolumes(ctx, plan); err != nil {
		return resp, err
	}
	exe, err := r.helperBinary()
	if err != nil {
		return resp, fmt.Errorf("find the agent executable: %w", err)
	}
	local, err := r.downloadPoint(ctx, st, point, m)
	if err != nil {
		return resp, err
	}
	defer os.Remove(local)
	var needs map[int]uint64
	if b.Type == recipe.TypeFiles {
		if needs, err = archiveNeeds(local, plan); err != nil {
			return resp, err
		}
	}

	psCtx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	up, err := r.runningProjects(psCtx)
	cancel()
	if err != nil {
		return resp, fmt.Errorf("check whether the app is running: %w", err)
	}
	running := up[projectName(job.AppID)]
	if running {
		if err := r.composeRun(ctx, job.AppID, "stop"); err != nil {
			return resp, r.startAgain(ctx, job.AppID, fmt.Errorf("stop the app: %w", err))
		}
	}
	// startAgain, if the app was running: on every failure below that
	// leaves its data as it was.
	fail := func(err error) error {
		if running {
			return r.startAgain(ctx, job.AppID, err)
		}
		return err
	}

	pre, err := r.RunBackup(ctx, BackupJob{AppID: job.AppID, Backup: b.Name, Kind: command.PointPreRestore})
	switch {
	case errors.Is(err, ErrNothingToBackUp):
		resp.Warnings = append(resp.Warnings, "nothing was saved before the restore: "+err.Error())
	case err != nil:
		return resp, fail(fmt.Errorf("the pre-restore copy failed, so nothing was restored: %w", err))
	case pre.Point != nil:
		resp.PreRestorePoint = pre.Point.ID
	}
	for _, w := range pre.Warnings {
		resp.Warnings = append(resp.Warnings, "the pre-restore copy: "+w)
	}

	name := "wf-restore-" + strconv.FormatInt(r.now().UnixNano(), 36)
	args := restoreArgs(name, job.AppID, exe, plan, b, needs)
	exit, stderr, err := r.runZstdRestore(ctx, name, args, local)
	res, parsed := helper.ParseResult(stderr)
	resp.Warnings = append(resp.Warnings, res.Warnings...)
	back := "the copy saved before it"
	if resp.PreRestorePoint != "" {
		back = "the pre-restore point " + resp.PreRestorePoint
	}
	switch {
	case err == nil && exit == helper.ExitOK && parsed:
	case err == nil && exit == helper.ExitPartial:
		return resp, fmt.Errorf("the restore stopped half-way: %s. The app was left stopped; restore %s to go back", reason(res, stderr, exit), back)
	case err != nil && b.Type == recipe.TypeFiles:
		// Stopped from outside (its time limit): it may have been swapping.
		return resp, fmt.Errorf("the restore helper was stopped (%w), so the paths may be partly replaced. The app was left stopped; restore %s to go back", err, back)
	case err != nil:
		// A database is swapped by one rename: it is the old or the restored one.
		return resp, fail(fmt.Errorf("the restore helper was stopped (%w): the database is the old one or the restored one", err))
	default:
		return resp, fail(fmt.Errorf("the restore failed, the app's data is unchanged: %s", reason(res, stderr, exit)))
	}
	if running {
		if err := r.startAgain(ctx, job.AppID, nil); err != nil {
			resp.Warnings = append(resp.Warnings, "the data was restored, but the app did not start again: "+err.Error())
		}
	}
	return resp, nil
}

// reason is why a helper failed: its result's error, else its exit code and
// last lines.
func reason(res helper.Result, stderr []byte, exit int) string {
	if res.Error != "" {
		return res.Error
	}
	return fmt.Sprintf("the helper exited with %d: %s", exit, lastLines(stderr, 3))
}

// startAgain starts a stopped app again (compose start) on a context of its
// own, bounded by restartTimeout: the restore's may have ended. An app being
// deleted is left alone. It returns cause, or the start's failure joined to
// it.
func (r *Repository) startAgain(ctx context.Context, appID string, cause error) error {
	if errors.Is(context.Cause(ctx), ErrAppDeleted) {
		return cause
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restartTimeout)
	defer cancel()
	if err := r.composeRun(sctx, appID, "start"); err != nil {
		err = fmt.Errorf("start the app again: %w", err)
		if cause == nil {
			return err
		}
		return fmt.Errorf("%w (and %v)", cause, err)
	}
	return cause
}

// downloadPoint copies a point's data object into backups/tmp, checking its
// size and sha256 against the manifest. It returns the local file (the
// caller removes it).
func (r *Repository) downloadPoint(ctx context.Context, st store.Store, p layout.Point, m layout.Manifest) (string, error) {
	if err := os.MkdirAll(r.restoreTmpDir(), 0o700); err != nil {
		return "", err
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	local := filepath.Join(r.restoreTmpDir(), p.ID+"-"+hex.EncodeToString(rnd[:])+".zst")
	f, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(local)
		}
	}()
	rc, err := st.Get(ctx, p.DataKey)
	if err != nil {
		f.Close()
		return "", fmt.Errorf("download the restore point: %w", err)
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), rc)
	rc.Close()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("download the restore point: %w", err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); n != m.Size || got != m.SHA256 {
		return "", fmt.Errorf("the restore point %s is damaged: its data does not match its manifest (%d bytes, sha256 %s; the manifest says %d bytes, %s)", p.ID, n, got, m.Size, m.SHA256)
	}
	ok = true
	return local, nil
}

// archiveNeeds reads a downloaded files archive's headers and sums, per
// helper mount, the room its entries take once extracted (each file rounded
// up to 4 KiB blocks, each folder one block). The helper refuses to start a
// restore a mount has no room for.
func archiveNeeds(local string, plan helperPlan) (map[int]uint64, error) {
	f, err := os.Open(local)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	needs := map[int]uint64{}
	tr := tar.NewReader(dec)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return needs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("the restore point's archive can't be read: %w", err)
		}
		name := strings.TrimSuffix(h.Name, "/")
		best := -1
		for i, p := range plan.Paths {
			if (p.Entry == "" || name == p.Entry || strings.HasPrefix(name, p.Entry+"/")) &&
				(best < 0 || len(p.Entry) > len(plan.Paths[best].Entry)) {
				best = i
			}
		}
		if best < 0 {
			continue // the helper refuses it
		}
		const block = 4096
		size := uint64(block)
		if h.Typeflag == tar.TypeReg {
			size = (uint64(max(h.Size, 0)) + block - 1) / block * block
		}
		needs[plan.Paths[best].Mount] += size
	}
}

// restoreArgs is the `docker run` of one restore: the backup's sources
// mounted read-write at /src/<n>, the agent's binary at /wf, the copy on
// stdin (-i).
func restoreArgs(name, appID, exe string, plan helperPlan, b recipe.EffectiveBackup, needs map[int]uint64) []string {
	args := []string{"run", "--rm", "-i", "--name", name}
	args = append(args, restoreLimits...)
	args = append(args, "--label", helper.RoleLabel, "--label", helper.AppLabel+"="+appID)
	for i, m := range plan.Mounts {
		spec := "type=volume," + csvField("source="+m.Volume)
		if m.Bind != "" {
			spec = "type=bind," + csvField("source="+m.Bind)
		}
		args = append(args, "--mount", spec+",target=/src/"+strconv.Itoa(i))
	}
	args = append(args, "--mount", "type=bind,"+csvField("source="+exe)+",target=/wf,readonly", helper.Image, "/wf", "backup", "restore")
	if b.Type == recipe.TypeSQLite {
		return append(args, "sqlite", plan.Paths[0].Src(), "--entry", plan.Paths[0].Entry)
	}
	args = append(args, "files", "--id", strings.TrimPrefix(name, "wf-restore-"))
	for _, p := range plan.Paths {
		args = append(args, "--root", "/src/"+strconv.Itoa(p.Mount), "--src", p.Src(), "--entry", p.Entry)
	}
	for i := range plan.Mounts {
		if n := needs[i]; n > 0 {
			args = append(args, "--need", "/src/"+strconv.Itoa(i)+"="+strconv.FormatUint(n, 10))
		}
	}
	return args
}

// runZstdRestore runs the restore helper with the decompressed copy on its
// stdin.
func (r *Repository) runZstdRestore(ctx context.Context, name string, args []string, local string) (int, []byte, error) {
	f, err := os.Open(local)
	if err != nil {
		return -1, nil, err
	}
	defer f.Close()
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return -1, nil, err
	}
	defer dec.Close()
	return r.runRestoreHelper(ctx, name, args, dec)
}
```

**Modify `internal/infra/orchestrator/docker_compose/backuprun.go`** — replace (1 of 5):

```go
var ErrNotRunnable = errors.New("backup not runnable")

```

with:

```go
var ErrNotRunnable = errors.New("backup not runnable")

// ErrNothingToBackUp marks a run that found nothing to copy: the database
// file is missing, or none of the paths exist (helper exit 4). A restore
// skips its pre-restore copy on it.
var ErrNothingToBackUp = errors.New("nothing to back up")

// nothingToBackUp is an ErrNothingToBackUp with the helper's reason.
type nothingToBackUp string

func (e nothingToBackUp) Error() string        { return string(e) }
func (e nothingToBackUp) Is(target error) bool { return target == ErrNothingToBackUp }

```

**Modify `internal/infra/orchestrator/docker_compose/backuprun.go`** — replace (2 of 5):

```go
		return out, nil
	case exit == helper.ExitOK && encErr != nil:
```

with:

```go
		return out, nil
	case exit == helper.ExitMissing && parsed && res.Error != "":
		return out, nothingToBackUp(res.Error)
	case exit == helper.ExitOK && encErr != nil:
```

**Modify `internal/infra/orchestrator/docker_compose/backuprun.go`** — replace (3 of 5):

```go
func runDockerHelper(ctx context.Context, name string, args []string, out io.Writer) (int, []byte, error) {
	stderr := &tailBuffer{max: 64 << 10}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = out, stderr
	cmd.Cancel = func() error {
```

with:

```go
func runDockerHelper(ctx context.Context, name string, args []string, out io.Writer) (int, []byte, error) {
	return runDockerIO(ctx, name, args, nil, out)
}

// runDockerRestoreHelper is runDockerHelper for a restore: in is the
// helper's stdin (its args carry -i), its stdout is discarded.
func runDockerRestoreHelper(ctx context.Context, name string, args []string, in io.Reader) (int, []byte, error) {
	return runDockerIO(ctx, name, args, in, io.Discard)
}

// runDockerIO runs a helper's `docker run` with stdin in (nil: none) and
// stdout out.
func runDockerIO(ctx context.Context, name string, args []string, in io.Reader, out io.Writer) (int, []byte, error) {
	stderr := &tailBuffer{max: 64 << 10}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	cmd.Cancel = func() error {
```

**Modify `internal/infra/orchestrator/docker_compose/backuprun.go`** — replace (4 of 5):

```go
		code := ee.ExitCode()
		if code != helper.ExitOK && code != helper.ExitUnchanged && code != helper.ExitMissing {
			removeHelper(name)
```

with:

```go
		code := ee.ExitCode()
		if code != helper.ExitOK && code != helper.ExitUnchanged && code != helper.ExitMissing && code != helper.ExitPartial {
			removeHelper(name)
```

**Modify `internal/infra/orchestrator/docker_compose/backuprun.go`** — replace (5 of 5):

```go
// RemoveHelpers removes helper containers left behind by an agent that
// stopped mid-run: one `docker ps` and at most one `docker rm`.
func (r *Repository) RemoveHelpers(ctx context.Context) error {
	return r.removeHelpers(ctx)
```

with:

```go
// RemoveHelpers removes helper containers left behind by an agent that
// stopped mid-run (one `docker ps` and at most one `docker rm`) and the
// restore downloads it left in backups/tmp.
func (r *Repository) RemoveHelpers(ctx context.Context) error {
	r.removeRestoreDownloads()
	return r.removeHelpers(ctx)
```

**Modify `internal/infra/orchestrator/docker_compose/gitrepo.go`** — replace (1 of 2):

```go
	"path/filepath"
	"time"
```

with:

```go
	"path/filepath"
	"slices"
	"time"
```

**Modify `internal/infra/orchestrator/docker_compose/gitrepo.go`** — replace (2 of 2):

```go

// neverCommitted are derivedArtifacts as go-git patterns. go-git does not
// read .git/info/exclude, so they ride on every commit's worktree.
var neverCommitted = func() []gitignore.Pattern {
	out := make([]gitignore.Pattern, 0, len(derivedArtifacts))
	for _, rel := range derivedArtifacts {
		out = append(out, gitignore.ParsePattern("/"+rel, nil))
```

with:

```go

// tempArtifacts are the temp files of the agent's own atomic writes in
// .winterflow/ (fsutil.WriteFile's config.json.tmp): a crash mid-write
// leaves one behind, and the next commit must not pick it up. App files
// can't live in .winterflow/ (safeRel), so the glob takes nothing of theirs.
const tempArtifacts = ".winterflow/*.tmp"

// neverCommitted are derivedArtifacts and tempArtifacts as go-git
// patterns. go-git does not read .git/info/exclude, so they ride on every
// commit's worktree.
var neverCommitted = func() []gitignore.Pattern {
	out := make([]gitignore.Pattern, 0, len(derivedArtifacts)+1)
	for _, rel := range append(slices.Clone(derivedArtifacts), tempArtifacts) {
		out = append(out, gitignore.ParsePattern("/"+rel, nil))
```

**Modify `internal/infra/orchestrator/docker_compose/repository.go`** — replace (1 of 3):

```go
//	storages/{name}.json         -- registered storages (0600, secret encrypted)
//
```

with:

```go
//	storages/{name}.json         -- registered storages (0600, secret encrypted)
//	backups/state.json           -- the backup scheduler's state
//	backups/tmp/                 -- restore downloads (removed after each restore)
//
```

**Modify `internal/infra/orchestrator/docker_compose/repository.go`** — replace (2 of 3):

```go
	runHelper        func(ctx context.Context, name string, args []string, stdout io.Writer) (exit int, stderr []byte, err error)
	docker           func(ctx context.Context, args ...string) error
```

with:

```go
	runHelper        func(ctx context.Context, name string, args []string, stdout io.Writer) (exit int, stderr []byte, err error)
	runRestoreHelper func(ctx context.Context, name string, args []string, stdin io.Reader) (exit int, stderr []byte, err error)
	docker           func(ctx context.Context, args ...string) error
```

**Modify `internal/infra/orchestrator/docker_compose/repository.go`** — replace (3 of 3):

```go
	r := &Repository{
		cfg:           cfg,
		log:           log,
		sourceChecks:  make(map[string]time.Time),
		versionCache:  make(map[string]versionEntry),
		storages:      storages.NewRegistry(filepath.Join(cfg.GetAgentDataDir(), "storages")),
		storageLimits: defaultStorageLimits,
		openStorage:   store.Open,
		runCompose:    dockerIn,
		composeOutput: dockerOutputIn,
		mountCache:    make(map[string]mountCacheEntry),
		hostStat:      os.Stat,
		runHelper:     runDockerHelper,
		docker:        dockerCombined,
		helperBinary:  agentExecutable,
		now:           time.Now,
	}
```

with:

```go
	r := &Repository{
		cfg:              cfg,
		log:              log,
		sourceChecks:     make(map[string]time.Time),
		versionCache:     make(map[string]versionEntry),
		storages:         storages.NewRegistry(filepath.Join(cfg.GetAgentDataDir(), "storages")),
		storageLimits:    defaultStorageLimits,
		openStorage:      store.Open,
		runCompose:       dockerIn,
		composeOutput:    dockerOutputIn,
		mountCache:       make(map[string]mountCacheEntry),
		hostStat:         os.Stat,
		runHelper:        runDockerHelper,
		runRestoreHelper: runDockerRestoreHelper,
		docker:           dockerCombined,
		helperBinary:     agentExecutable,
		now:              time.Now,
	}
```

- [ ] **Step 4: Run the tests and checks**

```bash
go test -short ./internal/infra/orchestrator/docker_compose/
go build ./...
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/orchestrator/docker_compose/restore.go \
  internal/infra/orchestrator/docker_compose/restore_test.go \
  internal/infra/orchestrator/docker_compose/backuprun.go \
  internal/infra/orchestrator/docker_compose/gitrepo.go \
  internal/infra/orchestrator/docker_compose/gitrepo_test.go \
  internal/infra/orchestrator/docker_compose/repository.go
git commit -F - <<'MSG'
feat(agent): restore a backup point in place, pre-restore copy first

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 5: Docker integration: restore round trips through real helpers

Real `alpine:3.20` helpers running the freshly built static agent: a sqlite database on a named volume owned by uid 1000 (with a stale `-wal` the app left) and files on a bind mount with a named volume nested inside it. Each restore must put the backed-up data back exactly, keep ownership, keep the nested mount point, leave no temp folder or download, keep a pre-restore copy, and stop/start the app around it. Skipped without Docker or in `-short`; CI runs it.

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/restore_docker_test.go`

**Interfaces:**
- Consumes: Task 4 `RestoreBackup`; C2a's `requireDocker`, `buildAgent`, `dockerT`, `saveConfig`, `withBackups`, `localStorage`, `writeFileT`.
- Produces:
  nothing (test only).

- [ ] **Step 1: Write the failing tests**

**Create `internal/infra/orchestrator/docker_compose/restore_docker_test.go`:**

```go
package dockercompose

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
)

// TestRestoreThroughDocker restores through real helper containers: a
// sqlite database on a named volume (owned by an app user), and files on a
// bind mount with a named volume nested inside it. Each restore puts the
// backed-up data back, keeps a pre-restore copy, and stops and starts the
// app around it.
func TestRestoreThroughDocker(t *testing.T) {
	requireDocker(t)
	r := newTestRepo(t)
	agent := buildAgent(t)
	r.helperBinary = func() (string, error) { return agent, nil }
	var compose []string
	r.runningProjects = func(context.Context) (map[string]bool, error) { return map[string]bool{"wf-itg": true}, nil }
	r.runCompose = func(_ context.Context, _ string, args []string) error {
		compose = append(compose, args[len(args)-1])
		return nil
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	vol, thumbs := "wf-itg-restore-"+stamp, "wf-itg-thumbs-"+stamp
	for _, v := range []string{vol, thumbs} {
		dockerT(t, "volume", "create", v)
		t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", v).Run() })
	}

	// The database: 3 rows, copied into the volume as uid 1000.
	src := t.TempDir()
	makeDB := func(name string, rows int) {
		db, err := sql.Open("sqlite", "file:"+filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		for range rows {
			if _, err := db.Exec("INSERT INTO items DEFAULT VALUES"); err != nil {
				t.Fatal(err)
			}
		}
	}
	makeDB("app.db", 3)
	makeDB("later.db", 7)
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", src+":/h:ro", helper.Image, "sh", "-c", "cp /h/app.db /v/app.db && chown -R 1000:1000 /v")

	// The files: a bind mount with the thumbs volume mounted inside it.
	media := t.TempDir()
	writeFileT(t, media, "a.txt", "alpha")
	writeFileT(t, media, "sub/b.txt", "beta")
	if err := os.Mkdir(filepath.Join(media, "thumbs"), 0o755); err != nil {
		t.Fatal(err)
	}
	dockerT(t, "run", "--rm", "-v", thumbs+":/t", helper.Image, "sh", "-c", "echo small > /t/t1.jpg && mkdir /t/2026 && echo deep > /t/2026/t2.jpg")

	disk := t.TempDir()
	if err := r.storages.Save(localStorage("disk", disk), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "itg", fmt.Sprintf(`services:
  app:
    image: %s
    volumes:
      - data:/data
      - %s:/media
      - thumbs:/media/thumbs
volumes:
  data:
    name: %s
    external: true
  thumbs:
    name: %s
    external: true
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: app.db
    - name: media
      type: files
      service: app
      paths: [/media]
`, helper.Image, media, vol, thumbs), withBackups(
		`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	clock := time.Now().UTC().Truncate(time.Second)
	r.now = func() time.Time { clock = clock.Add(time.Second); return clock }

	db, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "database", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "media", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}

	// The app writes on: a bigger database, edited and new files.
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", src+":/h:ro", helper.Image, "sh", "-c",
		"cp /h/later.db /v/app.db && echo stale > /v/app.db-wal && chown -R 1000:1000 /v")
	writeFileT(t, media, "a.txt", "edited")
	writeFileT(t, media, "new.txt", "new")
	dockerT(t, "run", "--rm", "-v", thumbs+":/t", helper.Image, "sh", "-c", "echo changed > /t/t1.jpg && rm -r /t/2026")

	resp, err := r.RestoreBackup(ctx, RestoreJob{AppID: "itg", Backup: "database", Point: db.Point.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.PreRestorePoint == "" || !strings.HasSuffix(resp.PreRestorePoint, "-pre-restore") {
		t.Fatalf("response = %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(disk, "itg", "database", resp.PreRestorePoint+".json")); err != nil {
		t.Fatalf("no pre-restore copy: %v", err)
	}
	// The volume holds the backed-up database, owned as before, no -wal.
	out := dockerT(t, "run", "--rm", "-v", vol+":/v", helper.Image, "sh", "-c", "ls -A /v; stat -c %u:%g /v/app.db")
	if out != "app.db\n1000:1000" {
		t.Fatalf("volume after restore: %q", out)
	}
	check := filepath.Join(t.TempDir(), "check")
	if err := os.Mkdir(check, 0o777); err != nil {
		t.Fatal(err)
	}
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", check+":/c", helper.Image, "sh", "-c", "cp /v/app.db /c/app.db && chmod 666 /c/app.db")
	cdb, err := sql.Open("sqlite", "file:"+filepath.Join(check, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := cdb.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil || n != 3 {
		t.Fatalf("restored rows = %d, %v", n, err)
	}
	cdb.Close()

	resp, err = r.RestoreBackup(ctx, RestoreJob{AppID: "itg", Backup: "media", Point: files.Point.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resp.PreRestorePoint, "-pre-restore") {
		t.Fatalf("response = %+v", resp)
	}
	for rel, want := range map[string]string{"a.txt": "alpha", "sub/b.txt": "beta"} {
		if body, err := os.ReadFile(filepath.Join(media, rel)); err != nil || string(body) != want {
			t.Fatalf("%s = %q, %v", rel, body, err)
		}
	}
	if _, err := os.Stat(filepath.Join(media, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("a file made after the copy survived the restore")
	}
	if info, err := os.Stat(filepath.Join(media, "thumbs")); err != nil || !info.IsDir() {
		t.Fatalf("the nested mount point is gone: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(media, ".wf-restore-*")); len(left) != 0 {
		t.Fatalf("temp folders left: %v", left)
	}
	if out := dockerT(t, "run", "--rm", "-v", thumbs+":/t", helper.Image, "sh", "-c", "cat /t/t1.jpg /t/2026/t2.jpg; ls -A /t"); out != "small\ndeep\n2026\nt1.jpg" {
		t.Fatalf("thumbs after restore: %q", out)
	}
	if !slices.Equal(compose, []string{"stop", "start", "stop", "start"}) {
		t.Fatalf("compose calls = %v", compose)
	}
	if left, _ := os.ReadDir(r.restoreTmpDir()); len(left) != 0 {
		t.Fatalf("downloads left: %v", left)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test -run TestRestoreThroughDocker -v ./internal/infra/orchestrator/docker_compose/
```

Expected: PASS already if Tasks 2–4 are right (this task adds coverage, not behavior); run it before committing anyway — it is the only end-to-end check of the helper's real mounts, capabilities and ownership.

- [ ] **Step 3: Implement**

Nothing to implement: the behavior is Task 2–4's; this task proves it through real containers.

- [ ] **Step 4: Run the tests and checks**

```bash
go test -run TestRestoreThroughDocker -v ./internal/infra/orchestrator/docker_compose/   # ~35 s, needs Docker
docker ps -a --filter label=winterflow.role=backup-helper -q   # prints nothing
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/orchestrator/docker_compose/restore_docker_test.go
git commit -F - <<'MSG'
test(agent): restore through real helper containers (sqlite, nested files mounts)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 6: Engine: deleted apps' backups (mark or delete), daily sweep, orphans

`DeleteApp` gains `deleteBackups`. Before the folder goes it reads the storages the app's backups use (any backup with a storage, on or off, custom too: a custom tool writes under the same `<app-id>/` through `WF_BACKUP_*_PREFIX`); after teardown it deletes `<app-id>/` in each (with `delete_backups`) or writes `<app-id>/.deleted` (`{deleted_at, app_name}`). Failures are warnings, bounded by one 2-minute `withDeadlineValue` (the command pipeline must never hang on a dead storage). `SweepDeleted` (Task 8 calls it daily) applies `BACKUP_RETENTION_DAYS` to marked folders only: a point by its own time, any other object by its mtime; a folder left with only its marker loses the marker too. `ListOrphans` lists the folders whose app is not on this server and that hold a point or a marker (an app's own files written through `WF_STORAGE_` don't count); `DeleteOrphan` refuses an app this server has. `app.get` now reports `backup_targets` and `retention_days`. The dispatcher's delete handler passes the flag through (its full wiring is Task 9).

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/backupfolders.go`
- Create: `internal/infra/orchestrator/docker_compose/backupfolders_test.go`
- Modify: `internal/app/agent/dispatcher.go`
- Modify: `internal/infra/orchestrator/docker_compose/backupconfig_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/deploy_integration_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/lifecycle.go`
- Modify: `internal/infra/orchestrator/docker_compose/repository_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/volumes_integration_test.go`
- Modify: `internal/infra/orchestrator/docker_compose/volumes_test.go`

**Interfaces:**
- Consumes: Task 1 types; C2a's `layout.Points`, `layout.Complete`, `withDeadline(Value)`, `openBackupStorage`, `appBackups`, `appName`, `composeContents`; Task 4's test fixtures.
- Produces:
  - `(*Repository).DeleteApp(ctx, appID string, deleteVolumes, deleteBackups bool) (DeleteResult, error)`; `DeleteResult.BackupsDeleted`.
  - `DeletedMarkerName = ".deleted"`, `DeletedMarker{DeletedAt time.Time, AppName string}`.
  - `(*Repository).SweepDeleted(ctx) []string` (problems per storage; nil with no ceiling), `ListOrphans(ctx, name string) ([]command.Orphan, error)` (never nil), `DeleteOrphan(ctx, name, appID string) (int, error)`.

- [ ] **Step 1: Write the failing tests**

**Create `internal/infra/orchestrator/docker_compose/backupfolders_test.go`:**

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
)

// putObject writes a storage object at disk/key with mtime.
func putObject(t *testing.T, disk, key, body string, mtime time.Time) {
	t.Helper()
	writeFileT(t, disk, key, body)
	if err := os.Chtimes(filepath.Join(disk, key), mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// putPoint writes a complete point (data object and manifest) made at at.
func putPoint(t *testing.T, disk, app, backup string, at time.Time, kind command.PointKind) {
	t.Helper()
	id := at.UTC().Format("20060102T150405Z") + "-" + string(kind)
	putObject(t, disk, app+"/"+backup+"/"+id+".db.zst", "data", at)
	putObject(t, disk, app+"/"+backup+"/"+id+".json", "{}", at)
}

// Deleting an app keeps its backups, marked with the app's name and when it
// was deleted; delete_backups removes them.
func TestDeleteAppSettlesBackups(t *testing.T) {
	b := newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	res, err := b.DeleteApp(context.Background(), "app-1", false, false)
	if err != nil || res.BackupsDeleted || len(res.Warnings) != 0 {
		t.Fatalf("delete: %+v, %v", res, err)
	}
	raw, err := os.ReadFile(filepath.Join(b.disk, "app-1", DeletedMarkerName))
	if err != nil {
		t.Fatal(err)
	}
	var m DeletedMarker
	if err := json.Unmarshal(raw, &m); err != nil || m.AppName != "demo" || !m.DeletedAt.Equal(b.clock) {
		t.Fatalf("marker = %s (%v)", raw, err)
	}
	if got := b.stored(t); len(got) != 3 {
		t.Fatalf("stored = %v", got)
	}

	b = newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	res, err = b.DeleteApp(context.Background(), "app-1", false, true)
	if err != nil || !res.BackupsDeleted || len(res.Warnings) != 0 {
		t.Fatalf("delete with backups: %+v, %v", res, err)
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("stored = %v", got)
	}
	// An app this agent doesn't have deletes no backups.
	if res, err := b.DeleteApp(context.Background(), "nope", false, true); err != nil || res.BackupsDeleted {
		t.Fatalf("unknown app: %+v, %v", res, err)
	}
}

// A storage that fails is a warning: the app is deleted anyway.
func TestDeleteAppBackupWarnings(t *testing.T) {
	b := newBackupRepo(t)
	b.runCompose = func(context.Context, string, []string) error { return nil }
	if _, err := b.storages.Delete("disk"); err != nil {
		t.Fatal(err)
	}
	res, err := b.DeleteApp(context.Background(), "app-1", false, false)
	if err != nil || len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "backups in disk: mark the backups of the deleted app: there is no storage disk") {
		t.Fatalf("%+v, %v", res, err)
	}
	if b.appExists("app-1") {
		t.Fatal("the app is still there")
	}
}

func TestSweepDeleted(t *testing.T) {
	b := newBackupRepo(t)
	now := b.clock
	day := 24 * time.Hour
	marker := `{"deleted_at":"2026-01-01T00:00:00Z","app_name":"Shop"}`
	// gone-1: deleted, one copy past the ceiling (180 days), one within.
	putObject(t, b.disk, "gone-1/.deleted", marker, now)
	putPoint(t, b.disk, "gone-1", "db", now.Add(-200*day), command.PointScheduled)
	putPoint(t, b.disk, "gone-1", "db", now.Add(-10*day), command.PointManual)
	// gone-2: deleted, everything old, a custom tool's file too.
	putObject(t, b.disk, "gone-2/.deleted", marker, now)
	putPoint(t, b.disk, "gone-2", "db", now.Add(-300*day), command.PointScheduled)
	putObject(t, b.disk, "gone-2/replica/generations/x.wal", "wal", now.Add(-190*day))
	// other: no marker (another server's): never deleted.
	putPoint(t, b.disk, "other", "db", now.Add(-400*day), command.PointScheduled)
	// app-1 is on this server: a stray marker doesn't make it sweepable.
	putObject(t, b.disk, "app-1/.deleted", marker, now)
	putPoint(t, b.disk, "app-1", "database", now.Add(-400*day), command.PointManual)

	if problems := b.SweepDeleted(context.Background()); len(problems) != 0 {
		t.Fatal(problems)
	}
	got := b.stored(t)
	want := []string{
		"app-1/.deleted", "app-1/database/20250906T120000Z-manual.db.zst", "app-1/database/20250906T120000Z-manual.json",
		"gone-1/.deleted", "gone-1/db/20261001T120000Z-manual.db.zst", "gone-1/db/20261001T120000Z-manual.json",
		"other/db/20250906T120000Z-scheduled.db.zst", "other/db/20250906T120000Z-scheduled.json",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("stored =\n%v\nwant\n%v", got, want)
	}

	// No ceiling: no sweep.
	t.Setenv("BACKUP_RETENTION_DAYS", "0")
	putPoint(t, b.disk, "gone-1", "db", now.Add(-900*day), command.PointScheduled)
	if problems := b.SweepDeleted(context.Background()); problems != nil || len(b.stored(t)) != len(want)+2 {
		t.Fatalf("swept with no ceiling: %v %v", problems, b.stored(t))
	}
	// A storage that can't be opened is reported.
	t.Setenv("BACKUP_RETENTION_DAYS", "30")
	if err := os.RemoveAll(b.disk); err != nil {
		t.Fatal(err)
	}
	if problems := b.SweepDeleted(context.Background()); len(problems) != 1 || !strings.HasPrefix(problems[0], "disk: ") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestOrphans(t *testing.T) {
	b := newBackupRepo(t)
	now := b.clock
	day := 24 * time.Hour
	putObject(t, b.disk, "gone-1/.deleted", `{"deleted_at":"2026-10-01T00:00:00Z","app_name":"Shop"}`, now)
	putPoint(t, b.disk, "gone-1", "db", now.Add(-20*day), command.PointScheduled)
	putPoint(t, b.disk, "gone-1", "media", now.Add(-5*day), command.PointManual)
	putObject(t, b.disk, "gone-1/db/20261011T110000Z-scheduled.db.zst", "incomplete", now) // no manifest
	putPoint(t, b.disk, "other", "db", now.Add(-2*day), command.PointScheduled)
	putObject(t, b.disk, "empty/.deleted", `{"deleted_at":"2026-10-02T00:00:00Z","app_name":"Gone"}`, now)
	putObject(t, b.disk, "uploads/photo.jpg", "an app's own files", now)
	putObject(t, b.disk, "top-level.txt", "x", now)
	putPoint(t, b.disk, "app-1", "database", now.Add(-day), command.PointManual)

	got, err := b.ListOrphans(context.Background(), "disk")
	if err != nil {
		t.Fatal(err)
	}
	want := []command.Orphan{
		{AppID: "empty", AppName: "Gone", DeletedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Unix(),
			PurgeAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Unix()},
		{AppID: "gone-1", AppName: "Shop", DeletedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(),
			PurgeAt: now.Add(-time.Hour + 180*day).Unix(), Copies: 2, Bytes: 4*2 + 2*2 + 10, Newest: now.Add(-5 * day).Unix()},
		{AppID: "other", Copies: 1, Bytes: 6, Newest: now.Add(-2 * day).Unix()},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("orphans =\n%+v\nwant\n%+v", got, want)
	}

	if _, err := b.DeleteOrphan(context.Background(), "disk", "app-1"); err == nil || !strings.Contains(err.Error(), "the app demo is on this server") {
		t.Fatalf("own app: %v", err)
	}
	n, err := b.DeleteOrphan(context.Background(), "disk", "gone-1")
	if err != nil || n != 6 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(b.disk, "gone-1")); !os.IsNotExist(err) {
		t.Fatal("the orphan's folder is still there")
	}
	if _, err := b.ListOrphans(context.Background(), "nope"); err == nil || !strings.Contains(err.Error(), "there is no storage nope") {
		t.Fatalf("unknown storage: %v", err)
	}
}

// app.get reports where a new backup can point and the server's ceiling.
func TestGetAppReportsBackupTargets(t *testing.T) {
	b := newBackupRepo(t)
	t.Setenv("BACKUP_RETENTION_DAYS", "90")
	resp, err := b.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.RetentionDays != 90 || resp.BackupTargets == nil || !slices.Equal(resp.BackupTargets.Volumes, []string{"data"}) ||
		len(resp.BackupTargets.Services) != 2 || resp.BackupTargets.Services[1].Name != "web" ||
		len(resp.BackupTargets.Services[1].Mounts) != 2 {
		t.Fatalf("response = %+v %+v", resp.RetentionDays, resp.BackupTargets)
	}
}
```

**Modify `internal/infra/orchestrator/docker_compose/backupconfig_test.go`** — replace:

```go
	}
	if _, err := r.DeleteApp(ctx, "app-1", true); err != nil {
		t.Fatal(err)
```

with:

```go
	}
	if _, err := r.DeleteApp(ctx, "app-1", true, false); err != nil {
		t.Fatal(err)
```

**Modify `internal/infra/orchestrator/docker_compose/deploy_integration_test.go`** — replace:

```go
	t.Cleanup(func() {
		_, _ = repo.DeleteApp(context.Background(), appID, false)
	})
```

with:

```go
	t.Cleanup(func() {
		_, _ = repo.DeleteApp(context.Background(), appID, false, false)
	})
```

**Modify `internal/infra/orchestrator/docker_compose/repository_test.go`** — replace:

```go

	if _, err := r.DeleteApp(context.Background(), "app-1", false); err != nil {
		t.Fatal(err)
```

with:

```go

	if _, err := r.DeleteApp(context.Background(), "app-1", false, false); err != nil {
		t.Fatal(err)
```

**Modify `internal/infra/orchestrator/docker_compose/volumes_integration_test.go`** — replace (1 of 2):

```go
	t.Cleanup(func() {
		_, _ = repo.DeleteApp(context.Background(), appID, true)
		_ = exec.Command("docker", "volume", "rm", "-f", projectName(appID)+"_data").Run()
```

with:

```go
	t.Cleanup(func() {
		_, _ = repo.DeleteApp(context.Background(), appID, true, false)
		_ = exec.Command("docker", "volume", "rm", "-f", projectName(appID)+"_data").Run()
```

**Modify `internal/infra/orchestrator/docker_compose/volumes_integration_test.go`** — replace (2 of 2):

```go

	res, err := repo.DeleteApp(ctx, appID, true)
	if err != nil {
```

with:

```go

	res, err := repo.DeleteApp(ctx, appID, true, false)
	if err != nil {
```

**Modify `internal/infra/orchestrator/docker_compose/volumes_test.go`** — replace (1 of 2):

```go
	t.Setenv("PATH", t.TempDir()) // no docker binary → every exec fails
	res, err := r.DeleteApp(context.Background(), "app-1", true)
	if err != nil {
```

with:

```go
	t.Setenv("PATH", t.TempDir()) // no docker binary → every exec fails
	res, err := r.DeleteApp(context.Background(), "app-1", true, false)
	if err != nil {
```

**Modify `internal/infra/orchestrator/docker_compose/volumes_test.go`** — replace (2 of 2):

```go
	t.Setenv("PATH", t.TempDir()) // any docker exec would yield a warning
	res, err := r.DeleteApp(context.Background(), "missing", true)
	if err != nil {
```

with:

```go
	t.Setenv("PATH", t.TempDir()) // any docker exec would yield a warning
	res, err := r.DeleteApp(context.Background(), "missing", true, false)
	if err != nil {
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test -short ./internal/infra/orchestrator/docker_compose/
```

Expected: FAIL — build failed: `too many arguments in call to b.DeleteApp`, `undefined: DeletedMarkerName`, `b.SweepDeleted undefined`.

- [ ] **Step 3: Implement**

**Create `internal/infra/orchestrator/docker_compose/backupfolders.go`:**

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
)

// An app's backups live in <storage>/<app-id>/. When the app is deleted
// without its backups, the folder gets a .deleted marker: the daily sweep
// then applies BACKUP_RETENTION_DAYS to it and removes it once empty.
// Folders without a marker are never deleted automatically — they may be
// another server's.

// DeletedMarkerName is the marker's name in an app's folder.
const DeletedMarkerName = ".deleted"

// DeletedMarker is the marker's content.
type DeletedMarker struct {
	DeletedAt time.Time `json:"deleted_at"`
	AppName   string    `json:"app_name"`
}

// Time limits of the folder operations, each run in a goroutine abandoned at
// its limit (see withDeadline): delete-app and delete-orphan run on the
// command pipeline, the sweep on the backup goroutine.
const (
	folderTimeout      = 2 * time.Minute // delete-app's markers or purges, all storages; delete-orphan
	orphanListTimeout  = time.Minute     // listing a storage for its orphans
	sweepStorageBudget = 5 * time.Minute // the daily sweep of one storage
	deleteBatch        = 1000            // keys per Delete call
)

// appBackupStorages names the storages an app's backups use (any backup
// with a storage, on or off, custom included: a custom tool writes under
// the same folder through WF_BACKUP_*_PREFIX). File reads only.
func (r *Repository) appBackupStorages(appID string) []string {
	dir := r.appDataDir(appID)
	cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
	if err != nil {
		r.log.Warn("delete: the app's backups can't be read, its backup folders are left as they are", "app_id", appID, "error", err)
		return nil
	}
	var out []string
	for _, b := range cfg.backups {
		if b.Storage != "" && !slices.Contains(out, b.Storage) {
			out = append(out, b.Storage)
		}
	}
	sort.Strings(out)
	return out
}

// settleBackups deletes a deleted app's backup folder in each storage
// (purge), or writes its .deleted marker there. Failures are warnings.
func (r *Repository) settleBackups(ctx context.Context, appID, appName string, storages []string, purge bool) []string {
	if len(storages) == 0 {
		return nil
	}
	what := "mark the backups of the deleted app"
	if purge {
		what = "delete the app's backups"
	}
	warnings, err := withDeadlineValue(ctx, folderTimeout, what, func(ctx context.Context) ([]string, error) {
		var out []string
		for _, name := range storages {
			st, err := r.openBackupStorage(name)
			if err == nil {
				if purge {
					_, err = deleteFolder(ctx, st, appID+"/")
				} else {
					err = writeMarker(ctx, st, appID, DeletedMarker{DeletedAt: r.now().UTC(), AppName: appName})
				}
			}
			if err != nil {
				out = append(out, fmt.Sprintf("backups in %s: %s: %v", name, what, err))
			}
		}
		return out, nil
	})
	if err != nil {
		return []string{err.Error()}
	}
	return warnings
}

func writeMarker(ctx context.Context, st store.Store, appID string, m DeletedMarker) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return st.Put(ctx, appID+"/"+DeletedMarkerName, strings.NewReader(string(raw)+"\n"))
}

func readMarker(ctx context.Context, st store.Store, key string) (DeletedMarker, error) {
	rc, err := st.Get(ctx, key)
	if err != nil {
		return DeletedMarker{}, err
	}
	defer rc.Close()
	var m DeletedMarker
	err = json.NewDecoder(io.LimitReader(rc, 64<<10)).Decode(&m)
	return m, err
}

// deleteFolder deletes every object under prefix, in batches; it returns
// how many there were.
func deleteFolder(ctx context.Context, st store.Store, prefix string) (int, error) {
	objs, err := st.List(ctx, prefix)
	if err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(objs))
	for _, o := range objs {
		keys = append(keys, o.Key)
	}
	return len(keys), deleteKeys(ctx, st, keys)
}

func deleteKeys(ctx context.Context, st store.Store, keys []string) error {
	for len(keys) > 0 {
		n := min(len(keys), deleteBatch)
		if err := st.Delete(ctx, keys[:n]...); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}

// appFolder is the objects of one app's folder in a storage.
type appFolder struct {
	appID   string
	marker  string // the .deleted marker's key ("" = none)
	objects []store.Object
	points  []layout.Point // across its backups, every kind, complete or not
}

// appFolders groups a storage's objects by app folder (keys outside one —
// a top-level object — are not an app's).
func appFolders(objs []store.Object) []*appFolder {
	byApp := map[string]*appFolder{}
	var order []string
	byBackup := map[string][]store.Object{}
	for _, o := range objs {
		app, rest, ok := strings.Cut(o.Key, "/")
		if !ok || app == "" {
			continue
		}
		f := byApp[app]
		if f == nil {
			f = &appFolder{appID: app}
			byApp[app] = f
			order = append(order, app)
		}
		if rest == DeletedMarkerName {
			f.marker = o.Key
			continue
		}
		f.objects = append(f.objects, o)
		if backup, _, ok := strings.Cut(rest, "/"); ok {
			byBackup[app+"/"+backup+"/"] = append(byBackup[app+"/"+backup+"/"], o)
		}
	}
	for folder, list := range byBackup {
		app, _, _ := strings.Cut(folder, "/")
		byApp[app].points = append(byApp[app].points, layout.Points(list, folder)...)
	}
	sort.Strings(order)
	out := make([]*appFolder, 0, len(order))
	for _, app := range order {
		out = append(out, byApp[app])
	}
	return out
}

// newest is the time of the folder's newest object: a point's own time,
// any other object's mtime (zero when it has none but its marker).
func (f *appFolder) newest() time.Time {
	var t time.Time
	for _, o := range f.objects {
		ot := o.ModTime
		if p, ok := f.pointOf(o.Key); ok {
			ot = p.Time
		}
		if ot.After(t) {
			t = ot
		}
	}
	return t
}

// pointOf finds the point a key belongs to.
func (f *appFolder) pointOf(key string) (layout.Point, bool) {
	for _, p := range f.points {
		if p.DataKey == key || p.ManifestKey == key {
			return p, true
		}
	}
	return layout.Point{}, false
}

// SweepDeleted applies BACKUP_RETENTION_DAYS to the folders of deleted apps
// (those with a .deleted marker) in every storage: what is older than the
// ceiling is deleted — a point by its own time, any other object (a custom
// tool's) by its mtime — and a folder left with nothing but its marker is
// removed with it. The backup goroutine runs it once a day; 0 disables it.
// It returns what failed, per storage.
func (r *Repository) SweepDeleted(ctx context.Context) []string {
	days := r.cfg.GetBackupRetentionDays()
	if days <= 0 {
		return nil
	}
	list, err := r.storages.List()
	if err != nil {
		return []string{"sweep: " + err.Error()}
	}
	var problems []string
	for _, s := range list {
		err := withDeadline(ctx, sweepStorageBudget, "sweep "+s.Name, func(ctx context.Context) error {
			st, err := r.openBackupStorage(s.Name)
			if err != nil {
				return err
			}
			return r.sweepStorage(ctx, st, days)
		})
		if err != nil {
			problems = append(problems, s.Name+": "+err.Error())
		}
	}
	return problems
}

func (r *Repository) sweepStorage(ctx context.Context, st store.Store, days int) error {
	objs, err := st.List(ctx, "")
	if err != nil {
		return err
	}
	cutoff := r.now().Add(-time.Duration(days) * 24 * time.Hour)
	for _, f := range appFolders(objs) {
		if f.marker == "" || r.appExists(f.appID) {
			continue
		}
		var old []string
		left := 0
		for _, o := range f.objects {
			t := o.ModTime
			if p, ok := f.pointOf(o.Key); ok {
				t = p.Time
			}
			if t.Before(cutoff) {
				old = append(old, o.Key)
			} else {
				left++
			}
		}
		if left == 0 {
			old = append(old, f.marker) // last: a failure keeps the folder marked
		}
		if err := deleteKeys(ctx, st, old); err != nil {
			return fmt.Errorf("app %s: %w", f.appID, err)
		}
	}
	return nil
}

// ListOrphans lists the backup folders in a storage whose app is not on this
// server. A folder counts when it holds a restore point or a .deleted marker
// (an app's own files written through WF_STORAGE_ variables don't make one).
// On request only, within orphanListTimeout.
func (r *Repository) ListOrphans(ctx context.Context, name string) ([]command.Orphan, error) {
	return withDeadlineValue(ctx, orphanListTimeout, "list the backups of other apps", func(ctx context.Context) ([]command.Orphan, error) {
		st, err := r.openBackupStorage(name)
		if err != nil {
			return nil, err
		}
		objs, err := st.List(ctx, "")
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", name, err)
		}
		days := r.cfg.GetBackupRetentionDays()
		out := []command.Orphan{}
		for _, f := range appFolders(objs) {
			if !command.ValidAppID(f.appID) || r.appExists(f.appID) || (f.marker == "" && len(f.points) == 0) {
				continue
			}
			o := command.Orphan{AppID: f.appID}
			for _, obj := range f.objects {
				o.Bytes += obj.Size
			}
			for _, p := range layout.Complete(f.points) {
				o.Copies++
				o.Newest = max(o.Newest, p.Time.Unix())
			}
			if f.marker != "" {
				if m, err := readMarker(ctx, st, f.marker); err == nil {
					o.AppName, o.DeletedAt = m.AppName, m.DeletedAt.Unix()
				}
				if days > 0 {
					purge := f.newest()
					if purge.IsZero() {
						purge = time.Unix(o.DeletedAt, 0) // nothing left: the next sweep
					} else {
						purge = purge.Add(time.Duration(days) * 24 * time.Hour)
					}
					o.PurgeAt = purge.Unix()
				}
			}
			out = append(out, o)
		}
		return out, nil
	})
}

// DeleteOrphan deletes an orphan's folder in a storage: every object under
// <app-id>/. It refuses for an app on this server (delete the app, or its
// restore points, instead).
func (r *Repository) DeleteOrphan(ctx context.Context, name, appID string) (int, error) {
	if r.appExists(appID) {
		return 0, fmt.Errorf("the app %s is on this server: its backups are not orphans", r.appName(r.appDataDir(appID), appID))
	}
	return withDeadlineValue(ctx, folderTimeout, "delete the backups", func(ctx context.Context) (int, error) {
		st, err := r.openBackupStorage(name)
		if err != nil {
			return 0, err
		}
		return deleteFolder(ctx, st, appID+"/")
	})
}
```

**Modify `internal/app/agent/dispatcher.go`** — replace:

```go
			d.backups.Forget(in.AppID) // stop a backup of it before its volumes go
			res, err := d.orch.DeleteApp(ctx, in.AppID, in.DeleteVolumes)
			d.backups.Forget(in.AppID) // and drop what a reload meanwhile read back
			resp := command.DeleteAppResponse{AppID: in.AppID, Warnings: res.Warnings, VolumesDeleted: res.VolumesDeleted}
			if err != nil {
```

with:

```go
			d.backups.Forget(in.AppID) // stop a backup of it before its volumes go
			res, err := d.orch.DeleteApp(ctx, in.AppID, in.DeleteVolumes, in.DeleteBackups)
			d.backups.Forget(in.AppID) // and drop what a reload meanwhile read back
			resp := command.DeleteAppResponse{AppID: in.AppID, Warnings: res.Warnings,
				VolumesDeleted: res.VolumesDeleted, BackupsDeleted: res.BackupsDeleted}
			if err != nil {
```

**Modify `internal/infra/orchestrator/docker_compose/lifecycle.go`** — replace (1 of 5):

```go

	"winterflow/internal/domain/command"
```

with:

```go

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
```

**Modify `internal/infra/orchestrator/docker_compose/lifecycle.go`** — replace (2 of 5):

```go
// failed (never an error); VolumesDeleted reports whether the volume sweep
// actually ran.
type DeleteResult struct {
	Warnings       []string
	VolumesDeleted bool
}
```

with:

```go
// failed (never an error); VolumesDeleted reports whether the volume sweep
// actually ran, BackupsDeleted whether the backups were deleted (not just
// marked).
type DeleteResult struct {
	Warnings       []string
	VolumesDeleted bool
	BackupsDeleted bool
}
```

**Modify `internal/infra/orchestrator/docker_compose/lifecycle.go`** — replace (3 of 5):

```go
// destroy orphan volumes an admin deliberately kept (those go through the
// volume registry).
func (r *Repository) DeleteApp(ctx context.Context, appID string, deleteVolumes bool) (DeleteResult, error) {
	// Decided before anything is removed: sweep only an app this agent has.
	res := DeleteResult{VolumesDeleted: deleteVolumes && r.appExists(appID)}
	downErr := r.composeDown(ctx, appID, deleteVolumes)
```

with:

```go
// destroy orphan volumes an admin deliberately kept (those go through the
// volume registry). The app's backups, in every storage its backups use,
// are deleted with deleteBackups, else marked as a deleted app's (the daily
// sweep removes them past BACKUP_RETENTION_DAYS); failures there are
// warnings too.
func (r *Repository) DeleteApp(ctx context.Context, appID string, deleteVolumes, deleteBackups bool) (DeleteResult, error) {
	// Decided before anything is removed: sweep only an app this agent has.
	exists := r.appExists(appID)
	res := DeleteResult{VolumesDeleted: deleteVolumes && exists, BackupsDeleted: deleteBackups && exists}
	var backupStorages []string
	appName := appID
	if exists {
		backupStorages = r.appBackupStorages(appID)
		appName = r.appName(r.appDataDir(appID), appID)
	}
	downErr := r.composeDown(ctx, appID, deleteVolumes)
```

**Modify `internal/infra/orchestrator/docker_compose/lifecycle.go`** — replace (4 of 5):

```go
	r.forgetApp(appID)
	return res, nil
```

with:

```go
	r.forgetApp(appID)
	res.Warnings = append(res.Warnings, r.settleBackups(ctx, appID, appName, backupStorages, deleteBackups)...)
	return res, nil
```

**Modify `internal/infra/orchestrator/docker_compose/lifecycle.go`** — replace (5 of 5):

```go
	}
	return resp, nil
```

with:

```go
	}
	// Where a backup added in the UI can point, and the server's ceiling
	// (the UI notes retention tiers that reach past it).
	if contents, err := composeContents(dir, files); err == nil {
		if comp, err := recipe.ParseCompose(contents...); err == nil && !comp.Unparsed {
			t := comp.Targets()
			resp.BackupTargets = &t
		}
	}
	resp.RetentionDays = r.cfg.GetBackupRetentionDays()
	return resp, nil
```

- [ ] **Step 4: Run the tests and checks**

```bash
go test -short ./internal/infra/orchestrator/docker_compose/ ./internal/app/agent/...
go vet ./...
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/infra/orchestrator/docker_compose/backupfolders.go \
  internal/infra/orchestrator/docker_compose/backupfolders_test.go \
  internal/app/agent/dispatcher.go \
  internal/infra/orchestrator/docker_compose/backupconfig_test.go \
  internal/infra/orchestrator/docker_compose/deploy_integration_test.go \
  internal/infra/orchestrator/docker_compose/lifecycle.go \
  internal/infra/orchestrator/docker_compose/repository_test.go \
  internal/infra/orchestrator/docker_compose/volumes_integration_test.go \
  internal/infra/orchestrator/docker_compose/volumes_test.go
git commit -F - <<'MSG'
feat(agent): keep or delete a deleted app's backups; daily sweep and orphans

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 7: API: restore-backup, list-orphans, delete-orphan; delete-app's delete_backups

Three agent-bound routes, all behind `RequireServerAccess` (403 probes): `POST /api/v1/app/restore-backup {server_id, app_id, backup, point}` (detached: awaited 2h 5m like run-backup), `GET /api/v1/storage/list-orphans?server_id&name`, `POST /api/v1/storage/delete-orphan {server_id, name, app_id}` (server_id in the body, as the spec writes it — the C2a backup routes' `dispatchServerBody`). `delete-app` passes `delete_backups`. Contract tests, probes, the fake agent's defaults, the routes golden and `api/openapi.yaml` follow.

**Files:**
- Create: `internal/app/web/contract/restore_test.go`
- Modify: `internal/app/web/contract/fakeagent_test.go`
- Modify: `internal/app/web/contract/matrix_test.go`
- Modify: `internal/app/web/handler/app/backups.go`
- Modify: `internal/app/web/handler/app/backups_test.go`
- Modify: `internal/app/web/handler/app/lifecycle.go`
- Modify: `internal/app/web/handler/app/routes.go`
- Modify: `internal/app/web/handler/app/routes_test.go`
- Modify: `internal/domain/usecase/app/backups.go`
- Modify: `internal/domain/usecase/app/usecase.go`
- Modify: `internal/domain/usecase/app/usecase_test.go`
- Regenerate: `api/openapi.yaml`
- Regenerate: `internal/app/web/testdata/routes.golden`

**Interfaces:**
- Consumes: Task 1 types; C2a's `dispatchServerBody`, `serverFromQuery`, `backupRunWait`.
- Produces:
  - Handler types `RestoreBackupRequest{ServerID; command.RestoreBackupRequest}`, `DeleteOrphanRequest{ServerID; command.DeleteOrphanRequest}`, `OrphansQuery{ServerID, Name}`; handlers `RestoreBackup`, `ListOrphans`, `DeleteOrphan`; `DeleteAppRequest.DeleteBackups`.
  - Usecase `RestoreBackup`, `ListOrphans`, `DeleteOrphan`; `DeleteApp(ctx, userID, serverID, appID string, deleteVolumes, deleteBackups bool)`.

- [ ] **Step 1: Write the failing tests**

**Create `internal/app/web/contract/restore_test.go`:**

```go
package contract_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
)

func TestRestoreAndOrphanRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)

	t.Run("restore-backup answers when the restore ends", func(t *testing.T) {
		var res command.RestoreBackupResponse
		owner.succeed(t, post("/api/v1/app/restore-backup", map[string]any{
			"server_id": sid, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"}), &res)
		if res.RestoredPoint != "20261011T120000Z-scheduled" || res.PreRestorePoint != "20261011T140000Z-pre-restore" {
			t.Fatalf("restore = %+v", res)
		}
		got := e.agent.seen(command.TypeBackupRestore)
		if in := decode[command.RestoreBackupRequest](got[len(got)-1].Payload); in.AppID != anApp || in.Backup != "database" {
			t.Fatalf("agent got %+v", in)
		}
		const partial = "the restore stopped half-way: /app/media was partly replaced. The app was left stopped; " +
			"restore the pre-restore point 20261011T140000Z-pre-restore to go back"
		e.agent.fail(command.TypeBackupRestore, partial)
		owner.failWith(t, post("/api/v1/app/restore-backup", map[string]any{
			"server_id": sid, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"}), partial)
		n := e.agent.count()
		for body, want := range map[string]string{
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backup":"database","point":"latest"}`:            "invalid restore point id",
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backup":"DB","point":"20261011T120000Z-manual"}`: "is not a valid backup name",
			`{"app_id":"` + anApp + `","backup":"database","point":"20261011T120000Z-manual"}`:                     "server_id is required",
		} {
			r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/restore-backup", Body: body}).expect(t, http.StatusBadRequest)
			if !strings.Contains(string(r.Body), want) {
				t.Errorf("%s: body = %s, want %q", body, r.Body, want)
			}
		}
		if e.agent.count() != n {
			t.Fatal("a refused restore reached the agent")
		}
	})
	t.Run("list-orphans", func(t *testing.T) {
		var res command.ListOrphansResponse
		owner.succeed(t, get("/api/v1/storage/list-orphans", url.Values{"server_id": {sid}, "name": {"disk"}}), &res)
		if res.Name != "disk" || len(res.Orphans) != 1 || res.Orphans[0].AppName != "Shop" || res.Orphans[0].Copies != 3 {
			t.Fatalf("orphans = %+v", res)
		}
		owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/storage/list-orphans", Invalid: true,
			Query: url.Values{"server_id": {sid}, "name": {"Bad Name"}}}).expect(t, http.StatusBadRequest)
	})
	t.Run("delete-orphan", func(t *testing.T) {
		var res command.DeleteOrphanResponse
		owner.succeed(t, post("/api/v1/storage/delete-orphan", map[string]any{"server_id": sid, "name": "disk", "app_id": "gone-app"}), &res)
		if res.AppID != "gone-app" || res.Deleted != 6 {
			t.Fatalf("deleted = %+v", res)
		}
		e.agent.fail(command.TypeStorageDeleteOrphan, "the app Shop is on this server: its backups are not orphans")
		owner.failWith(t, post("/api/v1/storage/delete-orphan", map[string]any{"server_id": sid, "name": "disk", "app_id": anApp}),
			"the app Shop is on this server: its backups are not orphans")
		r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/storage/delete-orphan", Invalid: true,
			Body: map[string]any{"server_id": sid, "name": "disk", "app_id": "../x"}}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "invalid app id") {
			t.Fatalf("body = %s", r.Body)
		}
	})
	t.Run("delete-app carries delete_backups", func(t *testing.T) {
		owner.succeed(t, post("/api/v1/app/delete-app", map[string]any{"server_id": sid, "app_id": anApp, "delete_backups": true}), nil)
		got := e.agent.seen(command.TypeAppDelete)
		if in := decode[command.DeleteAppRequest](got[len(got)-1].Payload); !in.DeleteBackups || in.DeleteVolumes {
			t.Fatalf("agent got %+v", in)
		}
	})
}
```

**Modify `internal/app/web/contract/fakeagent_test.go`** — replace (1 of 2):

```go
		}
	case command.TypeAgentUpdate:
```

with:

```go
		}
	case command.TypeBackupRestore:
		return func(raw json.RawMessage) reply {
			in := decode[command.RestoreBackupRequest](raw)
			return reply{Payload: command.RestoreBackupResponse{AppID: in.AppID, Backup: in.Backup, RestoredPoint: in.Point,
				PreRestorePoint: "20261011T140000Z-pre-restore"}}
		}
	case command.TypeStorageOrphans:
		return func(raw json.RawMessage) reply {
			return reply{Payload: command.ListOrphansResponse{Name: decode[command.ListOrphansRequest](raw).Name, Orphans: []command.Orphan{
				{AppID: "gone-app", AppName: "Shop", DeletedAt: 1790812800, PurgeAt: 1806364800, Copies: 3, Bytes: 4096, Newest: 1790812000}}}}
		}
	case command.TypeStorageDeleteOrphan:
		return func(raw json.RawMessage) reply {
			in := decode[command.DeleteOrphanRequest](raw)
			return reply{Payload: command.DeleteOrphanResponse{Name: in.Name, AppID: in.AppID, Deleted: 6}}
		}
	case command.TypeAgentUpdate:
```

**Modify `internal/app/web/contract/fakeagent_test.go`** — replace (2 of 2):

```go
		command.TypeBackupUpdate, command.TypeBackupPoints, command.TypeBackupRun, command.TypeBackupDeletePoint,
	} {
```

with:

```go
		command.TypeBackupUpdate, command.TypeBackupPoints, command.TypeBackupRun, command.TypeBackupDeletePoint,
		command.TypeBackupRestore, command.TypeStorageOrphans, command.TypeStorageDeleteOrphan,
	} {
```

**Modify `internal/app/web/contract/matrix_test.go`** — replace:

```go
	}, malformed: bad("POST", "/api/v1/app/delete-restore-point")},
	"GET /api/v1/domain/get-domains": {valid: func(s string) call { return get("/api/v1/domain/get-domains", sq(s)) }, malformed: bad("GET", "/api/v1/domain/get-domains")},
```

with:

```go
	}, malformed: bad("POST", "/api/v1/app/delete-restore-point")},
	"POST /api/v1/app/restore-backup": {valid: func(s string) call {
		return post("/api/v1/app/restore-backup", map[string]any{"server_id": s, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"})
	}, malformed: bad("POST", "/api/v1/app/restore-backup")},
	"GET /api/v1/storage/list-orphans": {valid: func(s string) call {
		return get("/api/v1/storage/list-orphans", url.Values{"server_id": {s}, "name": {"disk"}})
	}, malformed: bad("GET", "/api/v1/storage/list-orphans")},
	"POST /api/v1/storage/delete-orphan": {valid: func(s string) call {
		return post("/api/v1/storage/delete-orphan", map[string]any{"server_id": s, "name": "disk", "app_id": "gone-app"})
	}, malformed: bad("POST", "/api/v1/storage/delete-orphan")},
	"GET /api/v1/domain/get-domains": {valid: func(s string) call { return get("/api/v1/domain/get-domains", sq(s)) }, malformed: bad("GET", "/api/v1/domain/get-domains")},
```

**Modify `internal/app/web/handler/app/backups_test.go`** — replace (1 of 4):

```go
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)), command.TypeBackupDeletePoint},
	} {
```

with:

```go
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)), command.TypeBackupDeletePoint},
		{"restore", func(h *Handler) http.HandlerFunc { return h.RestoreBackup },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)), command.TypeBackupRestore},
		{"orphans", func(h *Handler) http.HandlerFunc { return h.ListOrphans },
			httptest.NewRequest("GET", "/x?server_id=s1&name=disk", nil), command.TypeStorageOrphans},
		{"delete orphan", func(h *Handler) http.HandlerFunc { return h.DeleteOrphan },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","name":"disk","app_id":"gone"}`)), command.TypeStorageDeleteOrphan},
	} {
```

**Modify `internal/app/web/handler/app/backups_test.go`** — replace (2 of 4):

```go
	del := route(func(h *Handler) http.HandlerFunc { return h.DeleteRestorePoint })
	for _, tc := range []struct {
```

with:

```go
	del := route(func(h *Handler) http.HandlerFunc { return h.DeleteRestorePoint })
	restore := route(func(h *Handler) http.HandlerFunc { return h.RestoreBackup })
	orphans := route(func(h *Handler) http.HandlerFunc { return h.ListOrphans })
	delOrphan := route(func(h *Handler) http.HandlerFunc { return h.DeleteOrphan })
	for _, tc := range []struct {
```

**Modify `internal/app/web/handler/app/backups_test.go`** — replace (3 of 4):

```go
		{"points no backup", points, "GET", "/x?server_id=s1&app_id=a1", "", http.StatusBadRequest, "is not a valid backup name"},
	} {
```

with:

```go
		{"points no backup", points, "GET", "/x?server_id=s1&app_id=a1", "", http.StatusBadRequest, "is not a valid backup name"},
		{"restore bad point", restore, "POST", "/x", `{"server_id":"s1","app_id":"a1","backup":"db","point":"latest"}`, http.StatusBadRequest, "invalid restore point id"},
		{"restore foreign server", restore, "POST", "/x", `{"server_id":"other","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`, http.StatusForbidden, ""},
		{"orphans bad name", orphans, "GET", "/x?server_id=s1&name=Disk", "", http.StatusBadRequest, "is not a valid storage name"},
		{"orphans foreign server", orphans, "GET", "/x?server_id=other&name=disk", "", http.StatusForbidden, ""},
		{"delete orphan bad app", delOrphan, "POST", "/x", `{"server_id":"s1","name":"disk","app_id":"a/b"}`, http.StatusBadRequest, "invalid app id"},
		{"delete orphan foreign server", delOrphan, "POST", "/x", `{"server_id":"other","name":"disk","app_id":"gone"}`, http.StatusForbidden, ""},
	} {
```

**Modify `internal/app/web/handler/app/backups_test.go`** — replace (4 of 4):

```go
			strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`)))),
		"failed to list restore points": do(failing.ListRestorePoints, authed(httptest.NewRequest("GET", "/x?server_id=s1&app_id=a1&backup=db", nil))),
	} {
```

with:

```go
			strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`)))),
		"failed to list restore points":   do(failing.ListRestorePoints, authed(httptest.NewRequest("GET", "/x?server_id=s1&app_id=a1&backup=db", nil))),
		"failed to list orphaned backups": do(failing.ListOrphans, authed(httptest.NewRequest("GET", "/x?server_id=s1&name=disk", nil))),
		"failed to restore backup": do(failing.RestoreBackup, authed(httptest.NewRequest("POST", "/x",
			strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)))),
	} {
```

**Modify `internal/app/web/handler/app/routes_test.go`** — replace:

```go
		"POST /api/v1/app/delete-restore-point": false,
		"GET /api/v1/domain/get-domains":        false,
```

with:

```go
		"POST /api/v1/app/delete-restore-point": false,
		"POST /api/v1/app/restore-backup":       false,
		"GET /api/v1/storage/list-orphans":      false,
		"POST /api/v1/storage/delete-orphan":    false,
		"GET /api/v1/domain/get-domains":        false,
```

**Modify `internal/domain/usecase/app/usecase_test.go`** — replace (1 of 2):

```go

	_, err := uc.DeleteApp(context.Background(), "u", "s", "app-7", false)
	if err != nil {
```

with:

```go

	_, err := uc.DeleteApp(context.Background(), "u", "s", "app-7", false, false)
	if err != nil {
```

**Modify `internal/domain/usecase/app/usecase_test.go`** — replace (2 of 2):

```go

	if _, err := uc.DeleteApp(context.Background(), "u", "s", "app-7", true); err != nil {
		t.Fatal(err)
	}
	want := command.DeleteAppRequest{AppID: "app-7", DeleteVolumes: true}
	if got, ok := disp.last.Payload.(command.DeleteAppRequest); !ok || got != want {
```

with:

```go

	if _, err := uc.DeleteApp(context.Background(), "u", "s", "app-7", true, false); err != nil {
		t.Fatal(err)
	}
	want := command.DeleteAppRequest{AppID: "app-7", DeleteVolumes: true}
	if got, ok := disp.last.Payload.(command.DeleteAppRequest); !ok || got != want {
		t.Fatalf("payload = %#v, want %#v", disp.last.Payload, want)
	}
	if _, err := uc.DeleteApp(context.Background(), "u", "s", "app-7", false, true); err != nil {
		t.Fatal(err)
	}
	want = command.DeleteAppRequest{AppID: "app-7", DeleteBackups: true}
	if got, ok := disp.last.Payload.(command.DeleteAppRequest); !ok || got != want {
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test ./internal/app/web/... ./internal/domain/...
```

Expected: FAIL — build failed: `h.RestoreBackup undefined`, `too many arguments in call to uc.DeleteApp`.

- [ ] **Step 3: Implement**

**Modify `internal/app/web/handler/app/backups.go`** — replace (1 of 2):

```go

func (r UpdateBackupsRequest) server() string      { return r.ServerID }
func (r RunBackupRequest) server() string          { return r.ServerID }
func (r DeleteRestorePointRequest) server() string { return r.ServerID }

```

with:

```go

// RestoreBackupRequest is the body of POST /api/v1/app/restore-backup.
type RestoreBackupRequest struct {
	ServerID string `json:"server_id"`
	command.RestoreBackupRequest
}

// DeleteOrphanRequest is the body of POST /api/v1/storage/delete-orphan.
type DeleteOrphanRequest struct {
	ServerID string `json:"server_id"`
	command.DeleteOrphanRequest
}

// OrphansQuery is the query of GET /api/v1/storage/list-orphans.
type OrphansQuery struct {
	ServerID string `query:"server_id" required:"true"`
	Name     string `query:"name" required:"true" description:"Storage name."`
}

func (r UpdateBackupsRequest) server() string      { return r.ServerID }
func (r RunBackupRequest) server() string          { return r.ServerID }
func (r DeleteRestorePointRequest) server() string { return r.ServerID }
func (r RestoreBackupRequest) server() string      { return r.ServerID }
func (r DeleteOrphanRequest) server() string       { return r.ServerID }

```

**Modify `internal/app/web/handler/app/backups.go`** — replace (2 of 2):

```go
	accepted(w, "list restore points dispatched", reqID)
}
```

with:

```go
	accepted(w, "list restore points dispatched", reqID)
}

// RestoreBackup restores a restore point in place; the outcome arrives over
// SSE when the restore ends.
func (h *Handler) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "restore backup", func(ctx context.Context, userID, serverID string, req RestoreBackupRequest) (string, error) {
		return h.usecase.RestoreBackup(ctx, userID, serverID, req.RestoreBackupRequest)
	})
}

// ListOrphans lists the backups in a storage whose app is not on the
// server; they arrive over SSE.
func (h *Handler) ListOrphans(w http.ResponseWriter, r *http.Request) {
	req := command.ListOrphansRequest{Name: r.URL.Query().Get("name")}
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.ListOrphans(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to list orphaned backups", nil)
		return
	}
	accepted(w, "list orphaned backups dispatched", reqID)
}

// DeleteOrphan deletes an orphan's backups from a storage.
func (h *Handler) DeleteOrphan(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "delete orphaned backups", func(ctx context.Context, userID, serverID string, req DeleteOrphanRequest) (string, error) {
		return h.usecase.DeleteOrphan(ctx, userID, serverID, req.DeleteOrphanRequest)
	})
}
```

**Modify `internal/app/web/handler/app/lifecycle.go`** — replace (1 of 2):

```go
	DeleteVolumes bool   `json:"delete_volumes"`
}
```

with:

```go
	DeleteVolumes bool   `json:"delete_volumes"`
	DeleteBackups bool   `json:"delete_backups"`
}
```

**Modify `internal/app/web/handler/app/lifecycle.go`** — replace (2 of 2):

```go

	requestID, err := h.usecase.DeleteApp(r.Context(), userID, req.ServerID, req.AppID, req.DeleteVolumes)
	if err != nil {
```

with:

```go

	requestID, err := h.usecase.DeleteApp(r.Context(), userID, req.ServerID, req.AppID, req.DeleteVolumes, req.DeleteBackups)
	if err != nil {
```

**Modify `internal/app/web/handler/app/routes.go`** — replace (1 of 3):

```go
			Method: http.MethodPost, Path: "/api/v1/app/delete-app", Handler: h.DeleteApp, Auth: api.User,
			Summary: "Delete an app (optionally its volumes)", Tag: "apps",
			Request: DeleteAppRequest{}, Accepted: true, Result: command.DeleteAppResponse{}, Errors: server,
```

with:

```go
			Method: http.MethodPost, Path: "/api/v1/app/delete-app", Handler: h.DeleteApp, Auth: api.User,
			Summary: "Delete an app (optionally its volumes and its backups)", Tag: "apps",
			Description: "Without `delete_backups` the app's backups are kept, marked as a deleted app's: the agent's daily sweep " +
				"deletes them once older than its BACKUP_RETENTION_DAYS.",
			Request: DeleteAppRequest{}, Accepted: true, Result: command.DeleteAppResponse{}, Errors: server,
```

**Modify `internal/app/web/handler/app/routes.go`** — replace (2 of 3):

```go
			Request:     RunBackupRequest{}, Accepted: true, Result: command.RunBackupResponse{}, Errors: server,
		},
```

with:

```go
			Request:     RunBackupRequest{}, Accepted: true, Result: command.RunBackupResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/restore-backup", Handler: h.RestoreBackup, Auth: api.User,
			Summary: "Restore a restore point in place (the app is stopped, its data saved as a pre-restore point first)", Tag: "backups",
			Description: "The result arrives when the restore ends. On a failure that leaves the data as it was, the app is " +
				"started again; a files restore that fails half-way leaves the app stopped and names the pre-restore point to restore.",
			Request: RestoreBackupRequest{}, Accepted: true, Result: command.RestoreBackupResponse{}, Errors: server,
		},
```

**Modify `internal/app/web/handler/app/routes.go`** — replace (3 of 3):

```go
		{
			Method: http.MethodGet, Path: "/api/v1/domains/check", Handler: h.CheckDomain, Auth: api.User,
```

with:

```go
		{
			Method: http.MethodGet, Path: "/api/v1/storage/list-orphans", Handler: h.ListOrphans, Auth: api.User,
			Summary: "List the backups in a storage whose app is not on this server", Tag: "storages",
			Query: OrphansQuery{}, Accepted: true, Result: command.ListOrphansResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/storage/delete-orphan", Handler: h.DeleteOrphan, Auth: api.User,
			Summary: "Delete the backups of an app that is not on this server from a storage", Tag: "storages",
			Request: DeleteOrphanRequest{}, Accepted: true, Result: command.DeleteOrphanResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domains/check", Handler: h.CheckDomain, Auth: api.User,
```

**Modify `internal/domain/usecase/app/backups.go`** — replace:

```go
		AgentID: serverID, UserID: userID, Type: command.TypeBackupDeletePoint, Payload: req,
	})
}
```

with:

```go
		AgentID: serverID, UserID: userID, Type: command.TypeBackupDeletePoint, Payload: req,
	})
}

// RestoreBackup dispatches backup.restore; its result arrives when the
// restore ends (it runs on the backup scheduler, after any run ahead of it).
func (uc *UseCase) RestoreBackup(ctx context.Context, userID, serverID string, req command.RestoreBackupRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupRestore, Payload: req, Wait: backupRunWait,
	})
}

// ListOrphans dispatches storage.orphans.
func (uc *UseCase) ListOrphans(ctx context.Context, userID, serverID string, req command.ListOrphansRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeStorageOrphans, Payload: req,
	})
}

// DeleteOrphan dispatches storage.delete-orphan.
func (uc *UseCase) DeleteOrphan(ctx context.Context, userID, serverID string, req command.DeleteOrphanRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeStorageDeleteOrphan, Payload: req,
	})
}
```

**Modify `internal/domain/usecase/app/usecase.go`** — replace:

```go
// DeleteApp dispatches an app.delete to the agent and, on success, removes the
// app from the DB cache.
func (uc *UseCase) DeleteApp(ctx context.Context, userID, serverID, appID string, deleteVolumes bool) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppDelete,
		Payload: command.DeleteAppRequest{AppID: appID, DeleteVolumes: deleteVolumes},
		OnResult: func(res port.CommandResult) {
```

with:

```go
// DeleteApp dispatches an app.delete to the agent and, on success, removes the
// app from the DB cache. deleteBackups also deletes the app's backups (else
// the agent marks them as a deleted app's).
func (uc *UseCase) DeleteApp(ctx context.Context, userID, serverID, appID string, deleteVolumes, deleteBackups bool) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID,
		UserID:  userID,
		Type:    command.TypeAppDelete,
		Payload: command.DeleteAppRequest{AppID: appID, DeleteVolumes: deleteVolumes, DeleteBackups: deleteBackups},
		OnResult: func(res port.CommandResult) {
```

Regenerate `api/openapi.yaml` with `make openapi` (never edit it by hand).

Update `internal/app/web/testdata/routes.golden` with `go test ./internal/app/web/ -run TestRoutesGolden -update` and check that only the new routes' lines were added.

- [ ] **Step 4: Run the tests and checks**

```bash
go test ./internal/app/web/ -run TestRoutesGolden -update   # then review the 3 added lines
make openapi
WF_CONTRACT_GUARD=require go test ./internal/app/web/... ./internal/domain/...
make test-pg   # Docker; contract suite on Postgres
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/app/web/contract/restore_test.go \
  internal/app/web/contract/fakeagent_test.go \
  internal/app/web/contract/matrix_test.go \
  internal/app/web/handler/app/backups.go \
  internal/app/web/handler/app/backups_test.go \
  internal/app/web/handler/app/lifecycle.go \
  internal/app/web/handler/app/routes.go \
  internal/app/web/handler/app/routes_test.go \
  internal/domain/usecase/app/backups.go \
  internal/domain/usecase/app/usecase.go \
  internal/domain/usecase/app/usecase_test.go \
  api/openapi.yaml \
  internal/app/web/testdata/routes.golden
git commit -F - <<'MSG'
feat(api): restore-backup, list-orphans and delete-orphan routes; delete-app takes delete_backups

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 8: Scheduler: restores in turn, cancelled with a cause, and the daily sweep

Restores ride the scheduler's queue, so a restore never runs beside a backup (the per-app lock of the spec): `Restore(app, backup, point)` queues behind the requests already waiting, refuses a second restore of the same app while one is queued or running, and refuses a blocked app (being deleted, or a stuck run). A restore gets `RestoreTimeout` (1h: download, pre-restore copy, put back) instead of the 30-minute run limit, is not a backup attempt (state.json untouched), and its waiter gets `Outcome.Restore`. Every job's context now carries a cancel cause: `Forget` cancels with `dockercompose.ErrAppDeleted`, which tells `RestoreBackup` not to start an app that is being deleted. The daily sweep (`SweepDeleted`, 15-minute bound) runs on the same goroutine, first one day after start, then every day; the single timer waits for it too.

**Files:**
- Modify: `internal/app/agent/backup/scheduler.go`
- Modify: `internal/app/agent/backup/scheduler_test.go`

**Interfaces:**
- Consumes: Task 4 `RestoreJob`, `RestoreBackup`, `ErrAppDeleted`; Task 6 `SweepDeleted`.
- Produces:
  - `backup.Engine` gains `RestoreBackup(ctx, dockercompose.RestoreJob) (command.RestoreBackupResponse, error)` and `SweepDeleted(ctx) []string` (`*dockercompose.Repository` has both).
  - `(*Scheduler).Restore(appID, name, point string) <-chan Outcome`; `Outcome.Restore command.RestoreBackupResponse`; `RestoreTimeout = time.Hour`.

- [ ] **Step 1: Write the failing tests**

**Modify `internal/app/agent/backup/scheduler_test.go`** — replace (1 of 3):

```go
	result    func(dockercompose.BackupJob) (dockercompose.BackupResult, error)
}

func newEngine(plan ...dockercompose.PlannedBackup) *fakeEngine {
	return &fakeEngine{plan: plan, runs: make(chan dockercompose.BackupJob, 100)}
}
```

with:

```go
	result    func(dockercompose.BackupJob) (dockercompose.BackupResult, error)
	restores  chan restoreCall
	sweeps    atomic.Int32
}

func newEngine(plan ...dockercompose.PlannedBackup) *fakeEngine {
	return &fakeEngine{plan: plan, runs: make(chan dockercompose.BackupJob, 100), restores: make(chan restoreCall, 100)}
}

// restoreCall is one RestoreBackup the engine got: the job, its context's
// deadline and, once it returned, why its context ended (nil: it didn't).
type restoreCall struct {
	job      dockercompose.RestoreJob
	deadline time.Time
	cause    error
}

// RestoreBackup records the restore and blocks like RunBackup (on block, or
// until its context ends, then reports the context's cause).
func (e *fakeEngine) RestoreBackup(ctx context.Context, job dockercompose.RestoreJob) (command.RestoreBackupResponse, error) {
	deadline, _ := ctx.Deadline()
	e.mu.Lock()
	block := e.block
	e.mu.Unlock()
	resp := command.RestoreBackupResponse{AppID: job.AppID, Backup: job.Backup, RestoredPoint: job.Point, PreRestorePoint: "pre"}
	if block != nil {
		e.restores <- restoreCall{job: job, deadline: deadline}
		select {
		case <-block:
		case <-ctx.Done():
			e.restores <- restoreCall{job: job, cause: context.Cause(ctx)}
			return resp, ctx.Err()
		}
		return resp, nil
	}
	e.restores <- restoreCall{job: job, deadline: deadline}
	return resp, nil
}

// SweepDeleted counts the sweeps.
func (e *fakeEngine) SweepDeleted(context.Context) []string {
	e.sweeps.Add(1)
	return []string{"disk: unreachable"}
}
```

**Modify `internal/app/agent/backup/scheduler_test.go`** — replace (2 of 3):

```go
	ran(t, e)
	if wait := idle(t, clock); wait != idleWait {
		t.Fatalf("wait with the app stuck = %s", wait)
```

with:

```go
	ran(t, e)
	// Only the daily sweep (a day after start) is left to wait for.
	if wait := idle(t, clock); wait != sweepEvery-5*time.Minute {
		t.Fatalf("wait with the app stuck = %s", wait)
```

**Modify `internal/app/agent/backup/scheduler_test.go`** — replace (3 of 3):

```go
	if j := ran(t, e); j.AppID != "app-1" || j.Kind != command.PointScheduled {
		t.Fatalf("run = %+v", j)
	}
}
```

with:

```go
	if j := ran(t, e); j.AppID != "app-1" || j.Kind != command.PointScheduled {
		t.Fatalf("run = %+v", j)
	}
}

func restored(t *testing.T, e *fakeEngine) restoreCall {
	t.Helper()
	select {
	case c := <-e.restores:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no restore")
		return restoreCall{}
	}
}

func outcome(t *testing.T, ch <-chan Outcome) Outcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("no outcome")
		return Outcome{}
	}
}

// A restore waits for the run ahead of it (one at a time: it never overlaps
// a backup of its app), gets the restore time limit, answers with the
// restore's result and is no backup attempt.
func TestRestoreRunsInTurn(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block = make(chan struct{})
	s, clock, dir := start(t, e)
	idle(t, clock)
	run := s.RunNow("app-1", "db")
	ran(t, e)
	res := s.Restore("app-1", "db", "20261011T120000Z-manual")
	if o := outcome(t, s.Restore("app-1", "other", "20261011T120000Z-manual")); o.Err == nil ||
		o.Err.Error() != "a restore of this app is already queued or running" {
		t.Fatalf("second restore: %+v", o)
	}
	select {
	case c := <-e.restores:
		t.Fatalf("the restore started during a run: %+v", c)
	case <-time.After(50 * time.Millisecond):
	}
	e.block <- struct{}{} // the run ends
	if o := outcome(t, run); o.Err != nil {
		t.Fatal(o.Err)
	}
	c := restored(t, e)
	if c.job != (dockercompose.RestoreJob{AppID: "app-1", Backup: "db", Point: "20261011T120000Z-manual"}) || time.Until(c.deadline) <= RunTimeout {
		t.Fatalf("restore call = %+v (a restore gets %s)", c, RestoreTimeout)
	}
	if o := outcome(t, s.Restore("app-1", "db", "20261011T130000Z-manual")); o.Err == nil {
		t.Fatal("a restore while one runs was queued")
	}
	e.block <- struct{}{}
	o := outcome(t, res)
	if o.Err != nil || o.Restore.RestoredPoint != "20261011T120000Z-manual" || o.Restore.PreRestorePoint != "pre" {
		t.Fatalf("outcome = %+v", o)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "backups", "state.json"))
	var st map[string]State
	_ = json.Unmarshal(raw, &st)
	// The manual run is the only attempt.
	if a := st["app-1/db"].LastAttempt; !a.Equal(t0) {
		t.Fatalf("state = %+v", st)
	}
}

// Deleting the app stops its restore with ErrAppDeleted as the cause, so
// the restore leaves the app stopped; the app takes no new restore.
func TestForgetStopsARestore(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "1h"))
	e.block = make(chan struct{})
	s, clock, _ := start(t, e)
	idle(t, clock)
	res := s.Restore("app-1", "db", "20261011T120000Z-manual")
	restored(t, e)
	s.Forget("app-1")
	if c := restored(t, e); !errors.Is(c.cause, dockercompose.ErrAppDeleted) {
		t.Fatalf("cause = %v", c.cause)
	}
	if o := outcome(t, res); o.Err == nil {
		t.Fatal("the stopped restore succeeded")
	}
	if o := outcome(t, s.Restore("app-1", "db", "20261011T120000Z-manual")); !errors.Is(o.Err, errDeleting) {
		t.Fatalf("restore of a deleted app: %v", o.Err)
	}
}

// Once a day (the first a day after start) the deleted apps' backups are
// swept on the scheduler's goroutine.
func TestDailySweep(t *testing.T) {
	e := newEngine()
	_, clock, _ := start(t, e)
	if wait := idle(t, clock); wait != sweepEvery {
		t.Fatalf("wait = %s", wait)
	}
	clock.Advance(sweepEvery)
	if wait := idle(t, clock); wait != sweepEvery || e.sweeps.Load() != 1 {
		t.Fatalf("wait = %s after %d sweeps", wait, e.sweeps.Load())
	}
	clock.Advance(sweepEvery)
	idle(t, clock)
	if e.sweeps.Load() != 2 {
		t.Fatalf("%d sweeps", e.sweeps.Load())
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test -race ./internal/app/agent/backup/
```

Expected: FAIL — build failed: `undefined: RestoreTimeout`, `s.Restore undefined`.

- [ ] **Step 3: Implement**

**Modify `internal/app/agent/backup/scheduler.go`** — replace (1 of 14):

```go
// source poller and independent of the command pipeline, that runs every
// enabled sqlite and files backup on its schedule and the Back up now
// requests, strictly one run at a time. It wakes on a single timer set to
// the earliest due time, keeps each backup's last attempt and success in
// {AGENT_DATA_DIR}/backups/state.json, and reports each backup's health for
// apps.status.
package backup
```

with:

```go
// source poller and independent of the command pipeline, that runs every
// enabled sqlite and files backup on its schedule, the Back up now and
// restore requests, strictly one at a time (so a restore never overlaps a
// backup of its app), and once a day the sweep of deleted apps' backups. It
// wakes on a single timer set to the earliest due time, keeps each backup's
// last attempt and success in {AGENT_DATA_DIR}/backups/state.json, and
// reports each backup's health for apps.status.
package backup
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (2 of 14):

```go
	RunBackup(ctx context.Context, job dockercompose.BackupJob) (dockercompose.BackupResult, error)
}
```

with:

```go
	RunBackup(ctx context.Context, job dockercompose.BackupJob) (dockercompose.BackupResult, error)
	RestoreBackup(ctx context.Context, job dockercompose.RestoreJob) (command.RestoreBackupResponse, error)
	SweepDeleted(ctx context.Context) []string
}
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (3 of 14):

```go
	RunTimeout = 30 * time.Minute
	// abandonAfter is how long past its deadline a run gets to stop before
```

with:

```go
	RunTimeout = 30 * time.Minute
	// RestoreTimeout bounds a restore: the download, the pre-restore copy
	// (itself a run) and putting the data back.
	RestoreTimeout = time.Hour
	// sweepEvery is how often deleted apps' backups are swept, the first
	// time one interval after start; sweepTimeout bounds one sweep.
	sweepEvery   = 24 * time.Hour
	sweepTimeout = 15 * time.Minute
	// abandonAfter is how long past its deadline a run gets to stop before
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (4 of 14):

```go

// Outcome is what a Back up now request learns when its run ends.
type Outcome struct {
	Result command.RunBackupResponse
	Err    error
}
```

with:

```go

// Outcome is what a Back up now (Result) or restore (Restore) request
// learns when its run ends.
type Outcome struct {
	Result  command.RunBackupResponse
	Restore command.RestoreBackupResponse
	Err     error
}
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (5 of 14):

```go
	kind    command.PointKind
	waiters []chan Outcome
	ctx     context.Context
	cancel  context.CancelFunc
	awaited bool // a Forget already waited out its grace on this job
```

with:

```go
	kind    command.PointKind
	point   string        // a restore of this point, not a backup run
	limit   time.Duration // its time limit
	waiters []chan Outcome
	ctx     context.Context
	cancel  context.CancelFunc
	// forget stops the job because its app is being deleted: its context's
	// cause is then dockercompose.ErrAppDeleted (a restore cut short leaves
	// such an app stopped).
	forget  func()
	awaited bool // a Forget already waited out its grace on this job
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (6 of 14):

```go
type Scheduler struct {
	engine     Engine
	path       string
	log        *logger.Logger
	clock      Clock
	runTimeout time.Duration
	grace      time.Duration

```

with:

```go
type Scheduler struct {
	engine         Engine
	path           string
	log            *logger.Logger
	clock          Clock
	runTimeout     time.Duration
	restoreTimeout time.Duration
	grace          time.Duration
	// nextSweep is when deleted apps' backups are swept next.
	nextSweep time.Time

```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (7 of 14):

```go
		engine: engine, path: filepath.Join(dataDir, "backups", "state.json"), log: log,
		clock: realClock{}, runTimeout: RunTimeout, grace: abandonAfter,
		wake: make(chan struct{}, 1), state: map[string]*State{}, dirty: true,
```

with:

```go
		engine: engine, path: filepath.Join(dataDir, "backups", "state.json"), log: log,
		clock: realClock{}, runTimeout: RunTimeout, restoreTimeout: RestoreTimeout, grace: abandonAfter,
		wake: make(chan struct{}, 1), state: map[string]*State{}, dirty: true,
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (8 of 14):

```go
	if r := s.running; r != nil && r.key.app == appID {
		r.cancel()
		wait = append(wait, r)
```

with:

```go
	if r := s.running; r != nil && r.key.app == appID {
		r.forget()
		wait = append(wait, r)
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (9 of 14):

```go

// Run runs backups until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	for ctx.Err() == nil {
		s.refresh()
		now := s.clock.Now()
		if j := s.next(ctx, now); j != nil {
```

with:

```go

// Restore queues a restore of an app's backup point behind the requests
// already queued, and returns where its outcome arrives. It fails at once
// while a restore of the same app is queued or running.
func (s *Scheduler) Restore(appID, name, point string) <-chan Outcome {
	ch := make(chan Outcome, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		ch <- Outcome{Err: errStopped}
		return ch
	}
	if err := s.blockedLocked(appID, s.clock.Now()); err != nil {
		ch <- Outcome{Err: err}
		return ch
	}
	dup := s.running != nil && s.running.key.app == appID && s.running.point != ""
	for _, j := range s.queue {
		dup = dup || (j.key.app == appID && j.point != "")
	}
	if dup {
		ch <- Outcome{Err: errors.New("a restore of this app is already queued or running")}
		return ch
	}
	s.queue = append(s.queue, &job{key: key{appID, name}, point: point, waiters: []chan Outcome{ch}})
	s.signal()
	return ch
}

// Run runs backups until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	s.nextSweep = s.clock.Now().Add(sweepEvery)
	for ctx.Err() == nil {
		s.refresh()
		now := s.clock.Now()
		if !now.Before(s.nextSweep) {
			s.sweep(ctx)
			s.nextSweep = now.Add(sweepEvery)
			continue
		}
		if j := s.next(ctx, now); j != nil {
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (10 of 14):

```go
func (s *Scheduler) startLocked(parent context.Context, j *job) *job {
	j.ctx, j.cancel = context.WithTimeout(parent, s.runTimeout)
	j.exited = make(chan struct{})
	s.running = j
	return j
}
```

with:

```go
func (s *Scheduler) startLocked(parent context.Context, j *job) *job {
	limit := s.runTimeout
	if j.point != "" {
		limit = s.restoreTimeout
	}
	cctx, cancelCause := context.WithCancelCause(parent)
	ctx, stop := context.WithTimeout(cctx, limit)
	j.ctx, j.limit = ctx, limit
	j.cancel = func() { stop(); cancelCause(nil) }
	j.forget = func() { cancelCause(dockercompose.ErrAppDeleted) }
	j.exited = make(chan struct{})
	s.running = j
	return j
}

// sweep deletes what is past BACKUP_RETENTION_DAYS in deleted apps' backup
// folders (bounded by sweepTimeout), logging what failed.
func (s *Scheduler) sweep(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, sweepTimeout)
	defer cancel()
	for _, p := range s.engine.SweepDeleted(ctx) {
		s.log.Warn("sweep of deleted apps' backups", "problem", p)
	}
}
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (11 of 14):

```go
	defer s.mu.Unlock()
	wait := idleWait
	for app, t := range s.deleting {
```

with:

```go
	defer s.mu.Unlock()
	wait := min(idleWait, max(s.nextSweep.Sub(now), 0))
	for app, t := range s.deleting {
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (12 of 14):

```go
	type done struct {
		res dockercompose.BackupResult
		err error
	}
	ch := make(chan done, 1)
	go func() {
		res, err := s.engine.RunBackup(j.ctx, dockercompose.BackupJob{AppID: j.key.app, Backup: j.key.name, Kind: j.kind})
		ch <- done{res, err}
		s.mu.Lock()
```

with:

```go
	type done struct {
		res     dockercompose.BackupResult
		restore command.RestoreBackupResponse
		err     error
	}
	ch := make(chan done, 1)
	go func() {
		var d done
		if j.point != "" {
			d.restore, d.err = s.engine.RestoreBackup(j.ctx, dockercompose.RestoreJob{AppID: j.key.app, Backup: j.key.name, Point: j.point})
		} else {
			d.res, d.err = s.engine.RunBackup(j.ctx, dockercompose.BackupJob{AppID: j.key.app, Backup: j.key.name, Kind: j.kind})
		}
		ch <- d
		s.mu.Lock()
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (13 of 14):

```go
			default:
				d.err = fmt.Errorf("the run did not stop after its time limit of %s", s.runTimeout)
				s.markStuck(j)
```

with:

```go
			default:
				d.err = fmt.Errorf("the run did not stop after its time limit of %s", j.limit)
				s.markStuck(j)
```

**Modify `internal/app/agent/backup/scheduler.go`** — replace (14 of 14):

```go
	}
	s.record(j, start, d.res, d.err, d.err != nil && parent.Err() != nil)
}
```

with:

```go
	}
	stopping := d.err != nil && parent.Err() != nil
	if j.point != "" {
		s.recordRestore(j, d.restore, d.err, stopping)
		return
	}
	s.record(j, start, d.res, d.err, stopping)
}

// recordRestore answers a restore's waiter. A restore is no backup attempt:
// the state is left alone (its pre-restore copy is a point, not a run of the
// schedule).
func (s *Scheduler) recordRestore(j *job, res command.RestoreBackupResponse, err error, stopping bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = nil
	if stopping {
		err = errStopped
	}
	if errors.Is(err, dockercompose.ErrNotRunnable) {
		s.dirty = true
	}
	finish(j, Outcome{Restore: res, Err: err})
}
```

- [ ] **Step 4: Run the tests and checks**

```bash
go test -race -count=3 ./internal/app/agent/backup/
go build ./...
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add internal/app/agent/backup/scheduler.go \
  internal/app/agent/backup/scheduler_test.go
git commit -F - <<'MSG'
feat(agent): restores and the daily sweep on the backup scheduler

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 9: Agent wiring: backup.restore, storage.orphans, storage.delete-orphan

The dispatcher registers the three commands. `backup.restore` waits for its outcome on the scheduler exactly like `backup.run` (bounded by `runWait`, 2h); being `Detached()`, the in-process bridge already runs it off its single goroutine and the gRPC agent already replies on the current stream (C2a). CLAUDE.md documents the new surface.

**Files:**
- Modify: `CLAUDE.md`
- Modify: `internal/app/agent/backups.go`
- Modify: `internal/app/agent/dispatcher.go`
- Modify: `internal/app/agent/dispatcher_backups_test.go`

**Interfaces:**
- Consumes: Task 8 `Scheduler.Restore`; Task 6 `ListOrphans`, `DeleteOrphan`.
- Produces:
  - `agent.Backups` gains `Restore(appID, name, point string) <-chan backup.Outcome` (`noBackups` answers `errNoBackups`); `(*Dispatcher).restoreBackup`.

- [ ] **Step 1: Write the failing tests**

**Modify `internal/app/agent/dispatcher_backups_test.go`** — replace (1 of 6):

```go
	runs     []string
	outcome  backup.Outcome
```

with:

```go
	runs     []string
	restores []string
	outcome  backup.Outcome
```

**Modify `internal/app/agent/dispatcher_backups_test.go`** — replace (2 of 6):

```go
	}()
	return ch
```

with:

```go
	}()
	return ch
}
func (f *fakeBackups) Restore(appID, name, point string) <-chan backup.Outcome {
	f.mu.Lock()
	f.restores = append(f.restores, appID+"/"+name+"@"+point)
	o := f.outcome
	f.mu.Unlock()
	ch := make(chan backup.Outcome, 1)
	ch <- o
	return ch
```

**Modify `internal/app/agent/dispatcher_backups_test.go`** — replace (3 of 6):

```go
	}
	// A request whose caller gave up stops waiting.
```

with:

```go
	}
	// A restore waits for its outcome on the scheduler.
	fb.outcome = backup.Outcome{Restore: command.RestoreBackupResponse{AppID: "app-1", Backup: "database",
		RestoredPoint: "20261011T120000Z-manual", PreRestorePoint: "20261011T130000Z-pre-restore"}}
	resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "database", Point: "20261011T120000Z-manual"})
	var restoredResp command.RestoreBackupResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &restoredResp) != nil ||
		restoredResp.PreRestorePoint != "20261011T130000Z-pre-restore" || fb.restores[0] != "app-1/database@20261011T120000Z-manual" {
		t.Fatalf("backup.restore = %+v %s", resp.Base, resp.Payload)
	}
	fb.outcome = backup.Outcome{Err: errors.New("the restore failed, the app's data is unchanged")}
	if resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "database",
		Point: "20261011T120000Z-manual"}); resp.Base.Detail != "the restore failed, the app's data is unchanged" {
		t.Fatalf("failed restore = %+v", resp.Base)
	}

	// Orphans: none in the storage while the app is here; its folder is
	// refused.
	resp = dispatchJSON(t, d, command.TypeStorageOrphans, command.ListOrphansRequest{Name: "disk"})
	var orphans command.ListOrphansResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &orphans) != nil || orphans.Orphans == nil || len(orphans.Orphans) != 0 {
		t.Fatalf("storage.orphans = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeStorageDeleteOrphan, command.DeleteOrphanRequest{Name: "disk", AppID: "app-1"})
	if resp.Base.ResponseCode == ok || !strings.Contains(resp.Base.Detail, "is on this server") {
		t.Fatalf("storage.delete-orphan = %+v", resp.Base)
	}
	resp = dispatchJSON(t, d, command.TypeStorageDeleteOrphan, command.DeleteOrphanRequest{Name: "disk", AppID: "gone"})
	var deleted command.DeleteOrphanResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &deleted) != nil || deleted.AppID != "gone" || deleted.Deleted != 0 {
		t.Fatalf("storage.delete-orphan gone = %+v %s", resp.Base, resp.Payload)
	}

	// A request whose caller gave up stops waiting.
```

**Modify `internal/app/agent/dispatcher_backups_test.go`** — replace (4 of 6):

```go
	}
	resp = dispatchJSON(t, d, command.TypeAppDelete, command.DeleteAppRequest{AppID: "app-1"})
	if len(fb.forgets) != 2 || fb.forgets[0] != "app-1" {
		t.Fatalf("delete: forgets = %v (%+v)", fb.forgets, resp.Base)
	}

	// Invalid payloads never reach the orchestrator or the scheduler.
	for typ, req := range map[command.Type]any{
		command.TypeBackupUpdate:      command.UpdateBackupsRequest{AppID: "..", Backups: nil},
		command.TypeBackupPoints:      command.ListRestorePointsRequest{AppID: "app-1", Backup: "Bad"},
		command.TypeBackupRun:         command.RunBackupRequest{AppID: "a/b", Backup: "db"},
		command.TypeBackupDeletePoint: command.DeleteRestorePointRequest{AppID: "app-1", Backup: "db", Point: "../x"},
	} {
```

with:

```go
	}
	resp = dispatchJSON(t, d, command.TypeAppDelete, command.DeleteAppRequest{AppID: "app-1", DeleteBackups: true})
	var deletedApp command.DeleteAppResponse
	if len(fb.forgets) != 2 || fb.forgets[0] != "app-1" || json.Unmarshal(resp.Payload, &deletedApp) != nil || !deletedApp.BackupsDeleted {
		t.Fatalf("delete: forgets = %v (%+v %s)", fb.forgets, resp.Base, resp.Payload)
	}

	// Invalid payloads never reach the orchestrator or the scheduler.
	for typ, req := range map[command.Type]any{
		command.TypeBackupUpdate:        command.UpdateBackupsRequest{AppID: "..", Backups: nil},
		command.TypeBackupPoints:        command.ListRestorePointsRequest{AppID: "app-1", Backup: "Bad"},
		command.TypeBackupRun:           command.RunBackupRequest{AppID: "a/b", Backup: "db"},
		command.TypeBackupDeletePoint:   command.DeleteRestorePointRequest{AppID: "app-1", Backup: "db", Point: "../x"},
		command.TypeBackupRestore:       command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "latest"},
		command.TypeStorageOrphans:      command.ListOrphansRequest{Name: "Disk"},
		command.TypeStorageDeleteOrphan: command.DeleteOrphanRequest{Name: "disk", AppID: "../x"},
	} {
```

**Modify `internal/app/agent/dispatcher_backups_test.go`** — replace (5 of 6):

```go
		t.Fatalf("backup.run = %+v", resp.Base)
	}
```

with:

```go
		t.Fatalf("backup.run = %+v", resp.Base)
	}
	resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || resp.Base.Detail != errNoBackups.Error() {
		t.Fatalf("backup.restore = %+v", resp.Base)
	}
```

**Modify `internal/app/agent/dispatcher_backups_test.go`** — replace (6 of 6):

```go
		t.Fatal("a stopped scheduler left the request waiting")
	}
}
```

with:

```go
		t.Fatal("a stopped scheduler left the request waiting")
	}
	resp = dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "stopping") {
		t.Fatalf("restore on a stopped scheduler = %+v", resp.Base)
	}
}

// A restore request is bounded like a run: a scheduler that never answers
// fails it at the limit.
func TestRestoreBackupIsBounded(t *testing.T) {
	d := newTestDispatcher(t)
	d.SetBackups(backup.New(d.orch, t.TempDir(), d.log))
	d.runWait = 50 * time.Millisecond
	resp := dispatchJSON(t, d, command.TypeBackupRestore, command.RestoreBackupRequest{AppID: "app-1", Backup: "db", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || !strings.Contains(resp.Base.Detail, "deadline exceeded") {
		t.Fatalf("unanswered restore = %+v", resp.Base)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
export GOTMPDIR=$HOME/.cache/wf-gotmp-c2b; go test -race ./internal/app/agent/...
```

Expected: FAIL — `backup.restore = ... unsupported command type: backup.restore` (and the same for `storage.orphans`, `storage.delete-orphan`).

- [ ] **Step 3: Implement**

**Modify `CLAUDE.md`** — replace (1 of 2):

```markdown

**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; ingress — `domain.list/create/delete` (registered domains, see below); storages — `storage.list/test/save/delete` (see below); backups — `backup.update/points/run/delete-point` (see below); agent — `agent.update` (self-update). All are agent-bound (202 + `request_id`, result over SSE).

```

with:

```markdown

**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes, `delete_backups` its backups), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; ingress — `domain.list/create/delete` (registered domains, see below); storages — `storage.list/test/save/delete/orphans/delete-orphan` (see below); backups — `backup.update/points/run/restore/delete-point` (see below); agent — `agent.update` (self-update). All are agent-bound (202 + `request_id`, result over SSE).

```

**Modify `CLAUDE.md`** — replace (2 of 2):

```markdown

**Backups:** a compose file declares what can be backed up under `x-winterflow.backups` (the recipe: `sqlite`, `files`, `custom`; parsed and validated by `internal/domain/backup/recipe`, shared by the API's save-app 400s and the agent); per-deployment settings (on/off, storage, schedule, paths, UI-added backups) are overrides in the committed `.winterflow/config.json` → `backups`, replaced as a whole by `backup.update` and kept across app-editor saves (`keepBackups`). `${WF_BACKUP_<NAME>_<FIELD>}` resolves to the backup's storage (PREFIX narrowed to `<app-id>/<backup>/`, empty while off); an enabled `custom` backup activates the compose profile `wf-backup-<name>` (passed with the user's `COMPOSE_PROFILES` as `--profile`; a disabled one's service is removed with `rm -fs`, since `up --remove-orphans` leaves it). The agent runs `sqlite`/`files` backups itself: the scheduler (`internal/app/agent/backup`, one goroutine, one timer, one run at a time, `{AGENT_DATA_DIR}/backups/state.json`) calls `RunBackup`, which mounts the app's own volumes/binds (resolved from one cached `docker compose config`) into a capped `alpine:3.20` helper running this binary's hidden `backup dump` subcommand (the binary must be static, `CGO_ENABLED=0` as `make build`, `scripts/install.sh` and goreleaser build it: alpine has no glibc loader, and the agent refuses to run a backup from a dynamic ELF; `internal/app/backup/cli`, exit 3 unchanged / 4 missing), zstd-compresses its stdout to the storage with a manifest written last (`internal/infra/backup/layout`), then prunes by tiers under `BACKUP_RETENTION_DAYS`. `backup.run` is a detached command: the in-process bridge runs it off its single goroutine, and the API awaits its result for 2h (`DispatchInput.Wait`; the agent bounds the wait the same, and a stopped scheduler fails it at once). Health rides on `apps.status` (`AppStatus.Backups`).

```

with:

```markdown

**Backups:** a compose file declares what can be backed up under `x-winterflow.backups` (the recipe: `sqlite`, `files`, `custom`; parsed and validated by `internal/domain/backup/recipe`, shared by the API's save-app 400s and the agent); per-deployment settings (on/off, storage, schedule, paths, UI-added backups) are overrides in the committed `.winterflow/config.json` → `backups`, replaced as a whole by `backup.update` and kept across app-editor saves (`keepBackups`). `${WF_BACKUP_<NAME>_<FIELD>}` resolves to the backup's storage (PREFIX narrowed to `<app-id>/<backup>/`, empty while off); an enabled `custom` backup activates the compose profile `wf-backup-<name>` (passed with the user's `COMPOSE_PROFILES` as `--profile`; a disabled one's service is removed with `rm -fs`, since `up --remove-orphans` leaves it). The agent runs `sqlite`/`files` backups itself: the scheduler (`internal/app/agent/backup`, one goroutine, one timer, one run at a time, `{AGENT_DATA_DIR}/backups/state.json`) calls `RunBackup`, which mounts the app's own volumes/binds (resolved from one cached `docker compose config`) into a capped `alpine:3.20` helper running this binary's hidden `backup dump` subcommand (the binary must be static, `CGO_ENABLED=0` as `make build`, `scripts/install.sh` and goreleaser build it: alpine has no glibc loader, and the agent refuses to run a backup from a dynamic ELF; `internal/app/backup/cli`, exit 3 unchanged / 4 missing), zstd-compresses its stdout to the storage with a manifest written last (`internal/infra/backup/layout`), then prunes by tiers under `BACKUP_RETENTION_DAYS`. `backup.run` is a detached command: the in-process bridge runs it off its single goroutine, and the API awaits its result for 2h (`DispatchInput.Wait`; the agent bounds the wait the same, and a stopped scheduler fails it at once). Health rides on `apps.status` (`AppStatus.Backups`). `backup.restore` is detached too and runs on the same scheduler (never beside a backup): `RestoreBackup` downloads the point to `backups/tmp` and checks its sha256, stops a running app, saves the current data as a `pre-restore` point (a run; its failure aborts and starts the app again), then a helper with `CHOWN`/`FOWNER` runs `backup restore` — sqlite replaces the file (quick_check, owner and mode kept, `-wal`/`-shm` removed), files extracts into `.wf-restore-<id>/` at each mount root (free space checked, `..`/absolute/outside/through-symlink entries refused) and swaps only the backup's paths (exit 5 = partly swapped: the app stays stopped). Any failure that leaves the data as it was starts the app again, unless the scheduler's `Forget` (app delete) cancelled it with `ErrAppDeleted`. Deleting an app marks its backup folders `<app-id>/.deleted` (or purges them with `delete_backups`); the scheduler's daily sweep applies `BACKUP_RETENTION_DAYS` to marked folders only, and `storage.orphans`/`delete-orphan` list and delete the folders of apps not on this server.

```

**Modify `internal/app/agent/backups.go`** — replace (1 of 3):

```go
	RunNow(appID, name string) <-chan backup.Outcome
	// Decorate adds each app's backup health to its status.
```

with:

```go
	RunNow(appID, name string) <-chan backup.Outcome
	// Restore queues a restore of a point; its outcome arrives when the
	// restore ends.
	Restore(appID, name, point string) <-chan backup.Outcome
	// Decorate adds each app's backup health to its status.
```

**Modify `internal/app/agent/backups.go`** — replace (2 of 3):

```go
func (noBackups) RunNow(string, string) <-chan backup.Outcome {
	ch := make(chan backup.Outcome, 1)
```

with:

```go
func (noBackups) RunNow(string, string) <-chan backup.Outcome {
	ch := make(chan backup.Outcome, 1)
	ch <- backup.Outcome{Err: errNoBackups}
	return ch
}
func (noBackups) Restore(string, string, string) <-chan backup.Outcome {
	ch := make(chan backup.Outcome, 1)
```

**Modify `internal/app/agent/backups.go`** — replace (3 of 3):

```go
		return command.RunBackupResponse{AppID: in.AppID, Backup: in.Backup}, ctx.Err()
	}
```

with:

```go
		return command.RunBackupResponse{AppID: in.AppID, Backup: in.Backup}, ctx.Err()
	}
}

// restoreBackup waits for a restore request (on the backup scheduler, like
// backup.run: backup.restore is a detached command), bounded like it.
func (d *Dispatcher) restoreBackup(ctx context.Context, in command.RestoreBackupRequest) (command.RestoreBackupResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, d.runWait)
	defer cancel()
	select {
	case o := <-d.backups.Restore(in.AppID, in.Backup, in.Point):
		return o.Restore, o.Err
	case <-ctx.Done():
		return command.RestoreBackupResponse{AppID: in.AppID, Backup: in.Backup, RestoredPoint: in.Point}, ctx.Err()
	}
```

**Modify `internal/app/agent/dispatcher.go`** — replace (1 of 2):

```go
			return command.DeleteStorageResponse{Name: in.Name}, d.orch.DeleteStorage(ctx, in.Name)
		}),

		command.TypeBackupUpdate: handle(d, func(ctx context.Context, in command.UpdateBackupsRequest) (command.UpdateBackupsResponse, error) {
```

with:

```go
			return command.DeleteStorageResponse{Name: in.Name}, d.orch.DeleteStorage(ctx, in.Name)
		}),
		command.TypeStorageOrphans: handle(d, func(ctx context.Context, in command.ListOrphansRequest) (command.ListOrphansResponse, error) {
			orphans, err := d.orch.ListOrphans(ctx, in.Name)
			return command.ListOrphansResponse{Name: in.Name, Orphans: orphans}, err
		}),
		command.TypeStorageDeleteOrphan: handle(d, func(ctx context.Context, in command.DeleteOrphanRequest) (command.DeleteOrphanResponse, error) {
			n, err := d.orch.DeleteOrphan(ctx, in.Name, in.AppID)
			return command.DeleteOrphanResponse{Name: in.Name, AppID: in.AppID, Deleted: n}, err
		}),

		command.TypeBackupUpdate: handle(d, func(ctx context.Context, in command.UpdateBackupsRequest) (command.UpdateBackupsResponse, error) {
```

**Modify `internal/app/agent/dispatcher.go`** — replace (2 of 2):

```go
		}),
		command.TypeBackupRun: handle(d, d.runBackup),
		command.TypeBackupDeletePoint: handle(d, func(ctx context.Context, in command.DeleteRestorePointRequest) (command.DeleteRestorePointResponse, error) {
```

with:

```go
		}),
		command.TypeBackupRun:     handle(d, d.runBackup),
		command.TypeBackupRestore: handle(d, d.restoreBackup),
		command.TypeBackupDeletePoint: handle(d, func(ctx context.Context, in command.DeleteRestorePointRequest) (command.DeleteRestorePointResponse, error) {
```

- [ ] **Step 4: Run the tests and checks**

```bash
go test -race ./internal/app/agent/...
go test -p 2 ./...
WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/...
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md \
  internal/app/agent/backups.go \
  internal/app/agent/dispatcher.go \
  internal/app/agent/dispatcher_backups_test.go
git commit -F - <<'MSG'
feat(agent): backup.restore and the storage orphan commands

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 10: Web: backups API hook, backup health, delete-app backups choice

The plumbing the two UI tasks build on. `lib/backups.ts` holds the shapes and pure helpers (labels, retention ceiling note, the update-backups list builders — an explicit empty `paths`/`exclude` is sent as `[]` only when it overrides the recipe, C2a carry-over). `useBackups()` calls the backup routes (server_id in the body of POSTs, the query of GETs; Back up now and restore wait up to 2h05m). The apps context keeps each app's backup health from `get-apps-status` and the `apps_status` push; the app grid shows a small amber dot on an app with a late or failing backup; the delete-app dialog (moved to its own component so it can be tested) gains "Also delete its backups" under the volumes choice, and never claims a deletion an older agent ignored.

**Files:**
- Create: `web/src/components/app-grid.test.tsx`
- Create: `web/src/components/delete-app-dialog.test.tsx`
- Create: `web/src/components/delete-app-dialog.tsx`
- Create: `web/src/context/use-backups.test.tsx`
- Create: `web/src/context/use-backups.ts`
- Create: `web/src/lib/backups.test.ts`
- Create: `web/src/lib/backups.ts`
- Create: `web/src/lib/delete-app.test.ts`
- Create: `web/src/lib/delete-app.ts`
- Modify: `web/src/components/app-grid.tsx`
- Modify: `web/src/context/apps-context-base.ts`
- Modify: `web/src/context/apps-context.test.tsx`
- Modify: `web/src/context/apps-context.tsx`
- Modify: `web/src/pages/app-details.tsx`

**Interfaces:**
- Consumes: Task 7 routes; the existing `useNotifications().waitFor`, `readApiError`, `AppsContext`.
- Produces:
  - `web/src/lib/backups.ts`: types `BackupType`, `BackupState`, `Keep`, `RecipeField`, `EffectiveBackup`, `BackupConfig`, `BackupHealth`, `PointKind`, `RestorePoint`, `RunBackupResult`, `RestoreResult`, `Orphan`, `Mount`, `BackupTargets`; `MANUAL`, `SCHEDULES`, `durationMs`, `scheduleLabel`, `everyError`, `changed`, `configOf`, `configsWith`, `resetField`, `keepDays`, `cappedNote`, `keepLabel`, `locationLabel`, `relativeAge`, `formatBytes`, `expiresNote`, `KIND_LABELS`, `STATE_LABELS`, `needsAttention`, `backupNameError`.
  - `web/src/context/use-backups.ts`: `useBackups()` → `{updateBackups, listRestorePoints, runBackup, restoreBackup, deleteRestorePoint, listOrphans, deleteOrphan, serverStorages}`; `type Backups`; `BACKUP_RUN_TIMEOUT_MS`, `BACKUP_UPDATE_TIMEOUT_MS`.
  - Apps context: `backupsByApp: Record<string, BackupHealth[]>`; `remove(appId, {deleteVolumes, deleteBackups})` → `{warnings, volumesDeleted, backupsDeleted}`; `AppDetailPayload.backups/backups_error/backup_targets/retention_days`.
  - `components/delete-app-dialog.tsx`: `DeleteAppDialog`; `lib/delete-app.ts`: `DeleteChoice`, `DeleteOutcome`, `deleteOutcome`.

- [ ] **Step 1: Write the failing tests**

**Create `web/src/components/app-grid.test.tsx`:**

```tsx
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { AppGrid } from "@/components/app-grid";
import { AppsContext, type AppsContextValue } from "@/context/apps-context-base";

function renderGrid(over: Partial<AppsContextValue>) {
  const value = {
    apps: [
      { id: "a1", name: "Shop", icon: "", color: "" },
      { id: "a2", name: "Blog", icon: "", color: "" },
    ],
    statusByApp: { a1: 1, a2: 1 },
    backupsByApp: {},
    loading: false,
    error: null,
    ...over,
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppGrid />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

describe("AppGrid", () => {
  it("puts a warning dot on apps with a late or failing backup", () => {
    renderGrid({ backupsByApp: { a1: [{ name: "db", state: "failing", last_error: "disk full" }], a2: [{ name: "db", state: "ok" }] } });
    expect(screen.getAllByRole("img", { name: "A backup is late or failing" })).toHaveLength(1);
    expect(screen.getByRole("link", { name: /Shop/ })).toHaveAttribute("title", "Shop: Running (a backup is late or failing)");
    expect(screen.getByRole("link", { name: /Blog/ })).toHaveAttribute("title", "Blog: Running");
  });

  it("shows no dot without backups", () => {
    renderGrid({});
    expect(screen.queryByRole("img", { name: "A backup is late or failing" })).not.toBeInTheDocument();
  });
});
```

**Create `web/src/components/delete-app-dialog.test.tsx`:**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { DeleteAppDialog } from "@/components/delete-app-dialog";

describe("DeleteAppDialog", () => {
  it("keeps volumes and backups unless ticked", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn();
    render(<DeleteAppDialog name="Shop" disabled={false} onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    const backups = screen.getByRole("checkbox", { name: "Also delete its backups" });
    expect(backups).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Also delete its volumes" })).not.toBeChecked();
    // Under the volumes choice.
    expect(screen.getAllByRole("checkbox").map((c) => c.id)).toEqual(["delete-volumes", "delete-backups"]);
    await user.click(screen.getByRole("button", { name: "Delete app" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: false, withBackups: false });
  });

  it("asks for the backups too once ticked", async () => {
    const user = userEvent.setup({ delay: null });
    const onConfirm = vi.fn();
    render(<DeleteAppDialog name="Shop" disabled={false} onConfirm={onConfirm} />);
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("checkbox", { name: "Also delete its backups" }));
    expect(screen.getByRole("button", { name: "Delete app and backups" })).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app, volumes and backups" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: true });

    // A new opening starts unticked again.
    await user.click(screen.getByRole("button", { name: /Delete Shop/ }));
    await user.click(screen.getByRole("checkbox", { name: "Also delete its volumes" }));
    await user.click(screen.getByRole("button", { name: "Delete app and volumes" }));
    expect(onConfirm).toHaveBeenLastCalledWith({ withVolumes: true, withBackups: false });
  });
});
```

**Create `web/src/context/use-backups.test.tsx`:**

```tsx
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { BACKUP_RUN_TIMEOUT_MS, useBackups } from "@/context/use-backups";

vi.mock("@/context/use-servers", () => ({ useServers: () => ({ activeServerId: "srv-1" }) }));
const waitFor = vi.fn();
vi.mock("@/context/use-notifications", () => ({ useNotifications: () => ({ waitFor }) }));

type Sent = { url: string; method: string; body: unknown };
let sent: Sent[] = [];
const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = String(input);
  sent.push({ url, method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
  if (url.includes("/api/v1/storage/get-storages")) {
    return new Response(JSON.stringify({ data: { storages: [{ name: "disk" }, { name: "s3" }] } }), { status: 200 });
  }
  return new Response(JSON.stringify({ data: { request_id: "r1" } }), { status: 202 });
});

describe("useBackups", () => {
  beforeEach(() => {
    sent = [];
    waitFor.mockReset();
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("sends server_id in the body of the POST routes", async () => {
    const { result } = renderHook(() => useBackups());
    waitFor.mockResolvedValueOnce({ status: 0, payload: { backups: [{ name: "db" }], redeployed: true } });
    expect(await result.current.updateBackups("a1", [{ name: "db", enabled: true, storage: "disk" }])).toEqual({
      backups: [{ name: "db" }],
      redeployed: true,
      warnings: [],
    });
    expect(sent[0]).toEqual({
      url: expect.stringMatching(/\/api\/v1\/app\/update-backups$/),
      method: "POST",
      body: { server_id: "srv-1", app_id: "a1", backups: [{ name: "db", enabled: true, storage: "disk" }] },
    });

    waitFor.mockResolvedValueOnce({ status: 0, payload: { unchanged: true } });
    expect(await result.current.runBackup("a1", "db")).toEqual({ unchanged: true, point: null, warnings: [] });
    expect(waitFor).toHaveBeenLastCalledWith("r1", BACKUP_RUN_TIMEOUT_MS);

    waitFor.mockResolvedValueOnce({ status: 0, payload: { restored_point: "p1", pre_restore_point: "p2", warnings: ["w"] } });
    expect(await result.current.restoreBackup("a1", "db", "p1")).toEqual({ restored_point: "p1", pre_restore_point: "p2", warnings: ["w"] });
    expect(sent[2].body).toEqual({ server_id: "srv-1", app_id: "a1", backup: "db", point: "p1" });

    waitFor.mockResolvedValueOnce({ status: 0, payload: {} });
    await result.current.deleteRestorePoint("a1", "db", "p1");
    waitFor.mockResolvedValueOnce({ status: 0, payload: { deleted: 6 } });
    expect(await result.current.deleteOrphan("disk", "gone")).toBe(6);
    expect(sent[4].body).toEqual({ server_id: "srv-1", name: "disk", app_id: "gone" });
  });

  it("puts server_id in the query of the GET routes", async () => {
    const { result } = renderHook(() => useBackups());
    waitFor.mockResolvedValueOnce({ status: 0, payload: { points: null } });
    expect(await result.current.listRestorePoints("a1", "db")).toEqual([]);
    expect(sent[0].url).toContain("/api/v1/app/list-restore-points?server_id=srv-1&app_id=a1&backup=db");
    waitFor.mockResolvedValueOnce({ status: 0, payload: { orphans: [{ app_id: "gone" }] } });
    expect(await result.current.listOrphans("disk")).toEqual([{ app_id: "gone" }]);
    expect(sent[1].url).toContain("/api/v1/storage/list-orphans?server_id=srv-1&name=disk");
    expect(await result.current.serverStorages()).toEqual(["disk", "s3"]);
  });

  it("surfaces refusals and failed results", async () => {
    const { result } = renderHook(() => useBackups());
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ message: "invalid restore point id" }), { status: 400 }));
    await expect(result.current.restoreBackup("a1", "db", "x")).rejects.toThrow("invalid restore point id");
    waitFor.mockResolvedValueOnce({ status: 1, error: "a backup of db is already queued or running" });
    await expect(result.current.runBackup("a1", "db")).rejects.toThrow("a backup of db is already queued or running");
    waitFor.mockResolvedValueOnce({ status: 1 });
    await expect(result.current.listOrphans("disk")).rejects.toThrow("Operation failed");
    fetchMock.mockResolvedValueOnce(new Response(JSON.stringify({ data: {} }), { status: 202 }));
    await expect(result.current.listOrphans("disk")).rejects.toThrow("No request id");
    fetchMock.mockResolvedValueOnce(new Response("down", { status: 502 }));
    await expect(result.current.serverStorages()).rejects.toThrow("Request failed: 502");
  });
});
```

**Create `web/src/lib/backups.test.ts`:**

```ts
import { describe, expect, it } from "vitest";

import {
  backupNameError,
  cappedNote,
  configOf,
  configsWith,
  durationMs,
  everyError,
  expiresNote,
  formatBytes,
  keepDays,
  keepLabel,
  locationLabel,
  needsAttention,
  relativeAge,
  resetField,
  scheduleLabel,
  type EffectiveBackup,
} from "@/lib/backups";

const all = { every: true, keep: true, volume: true, service: true, path: true, paths: true, exclude: true };

const database: EffectiveBackup = {
  name: "database",
  type: "sqlite",
  enabled: true,
  storage: "disk",
  every: "15m",
  keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
  volume: "data",
  path: "app.db",
  in_recipe: true,
  from_recipe: { ...all, every: false },
};
const media: EffectiveBackup = {
  name: "media",
  type: "files",
  enabled: false,
  storage: "",
  every: "24h",
  keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
  service: "web",
  paths: ["/app/media"],
  exclude: ["*.tmp"],
  in_recipe: true,
  from_recipe: all,
};
const audit: EffectiveBackup = {
  name: "audit",
  type: "sqlite",
  enabled: true,
  storage: "s3",
  every: "1h",
  keep: { recent: "24h0m0s", daily: 3, monthly: 0 },
  volume: "data",
  path: "audit.db",
  in_recipe: false,
  from_recipe: {},
};
const replica: EffectiveBackup = { name: "replica", type: "custom", enabled: true, storage: "s3", service: "litestream", in_recipe: true, from_recipe: all };

describe("schedules", () => {
  it("parses Go durations", () => {
    expect(durationMs("1h30m")).toBe(5_400_000);
    expect(durationMs("24h0m0s")).toBe(86_400_000);
    expect(durationMs("250ms")).toBe(250);
    expect(durationMs("")).toBeNaN();
    expect(durationMs("1 hour")).toBeNaN();
  });
  it("reads every as a label", () => {
    expect(scheduleLabel("manual")).toBe("Manual");
    expect(scheduleLabel(undefined)).toBe("Manual");
    expect(scheduleLabel("1h0m0s")).toBe("Every hour");
    expect(scheduleLabel("24h")).toBe("Every day");
    expect(scheduleLabel("90m")).toBe("Every 90m");
  });
  it("checks every like the recipe", () => {
    expect(everyError("manual")).toBe("");
    expect(everyError("5m")).toBe("");
    expect(everyError("4m")).toBe("Back up at most every 5 minutes.");
    expect(everyError("daily")).toBe("Use a duration like 15m, 2h or 24h, or Manual.");
  });
});

describe("the backups list", () => {
  it("keeps a recipe backup as its on/off, storage and overrides", () => {
    expect(configOf(database)).toEqual({ name: "database", enabled: true, storage: "disk", every: "15m" });
    expect(configOf(media)).toEqual({ name: "media" });
    expect(configOf(replica)).toEqual({ name: "replica", enabled: true, storage: "s3" });
  });
  it("keeps a UI-added backup in full", () => {
    expect(configOf(audit)).toEqual({
      name: "audit",
      type: "sqlite",
      enabled: true,
      storage: "s3",
      every: "1h",
      keep: { recent: "24h0m0s", daily: 3, monthly: 0 },
      volume: "data",
      path: "audit.db",
    });
    const custom: EffectiveBackup = { ...replica, name: "mine", in_recipe: false, from_recipe: {} };
    expect(configOf(custom)).toEqual({ name: "mine", type: "custom", enabled: true, storage: "s3", service: "litestream" });
  });
  it("sends an explicit empty paths or exclude only when it overrides the recipe", () => {
    const whole: EffectiveBackup = { ...media, service: undefined, volume: "data", paths: undefined, exclude: undefined, from_recipe: { ...all, volume: false, service: false, paths: false, exclude: false } };
    expect(configOf(whole)).toEqual({ name: "media", volume: "data", paths: [], exclude: [] });
    const recipeWhole: EffectiveBackup = { ...whole, from_recipe: all };
    expect(configOf(recipeWhole)).toEqual({ name: "media" });
    const added: EffectiveBackup = { ...whole, in_recipe: false, from_recipe: {} };
    expect(configOf(added)).toEqual({ name: "media", type: "files", every: "24h", keep: { recent: "24h0m0s", daily: 14, monthly: 6 }, volume: "data" });
  });
  it("moves a changed location as a whole", () => {
    const moved: EffectiveBackup = { ...media, service: undefined, volume: "data", paths: ["media"], from_recipe: { ...all, volume: false, service: false, paths: false } };
    expect(configOf(moved)).toEqual({ name: "media", volume: "data", paths: ["media"] });
  });
  it("replaces one entry, drops empty ones, removes and appends", () => {
    const list = [database, media, audit];
    expect(configsWith(list, "media", { name: "media", enabled: true, storage: "disk" })).toEqual([
      configOf(database),
      { name: "media", enabled: true, storage: "disk" },
      configOf(audit),
    ]);
    expect(configsWith(list, "audit", null)).toEqual([configOf(database)]);
    expect(configsWith(list, "new", { name: "new", type: "files", volume: "data" })).toEqual([
      configOf(database),
      configOf(audit),
      { name: "new", type: "files", volume: "data" },
    ]);
  });
  it("resets a field to the recipe, the location as a whole", () => {
    const c = { name: "media", enabled: true, every: "1h", service: "web", paths: ["/x"], exclude: [] };
    expect(resetField(c, "every")).toEqual({ name: "media", enabled: true, service: "web", paths: ["/x"], exclude: [] });
    expect(resetField(c, "paths")).toEqual({ name: "media", enabled: true, every: "1h", exclude: [] });
  });
});

describe("retention", () => {
  it("measures how far back it reaches", () => {
    expect(keepDays(undefined)).toBe(186);
    expect(keepDays({ recent: "720h", daily: 3, monthly: 0 })).toBe(30);
    expect(keepDays({ daily: 400, monthly: 1 })).toBe(400);
  });
  it("notes the server's ceiling only when the tiers reach past it", () => {
    expect(cappedNote(undefined, 180)).toBe("Capped at 180 days on this server.");
    expect(cappedNote({ daily: 7, monthly: 0 }, 180)).toBe("");
    expect(cappedNote(undefined, 0)).toBe("");
  });
  it("reads as a label", () => {
    expect(keepLabel({ recent: "24h0m0s", daily: 14, monthly: 6 })).toBe("24h, 14 daily, 6 monthly");
    expect(keepLabel({ recent: "36h30m0s", daily: 1, monthly: 0 })).toBe("36h30m, 1 daily, 0 monthly");
    expect(keepLabel({ recent: "45m0s" })).toBe("45m, 14 daily, 6 monthly");
    expect(keepLabel(null)).toBe("24h, 14 daily, 6 monthly");
  });
});

describe("labels", () => {
  it("reads a location", () => {
    expect(locationLabel(database)).toBe("volume data: app.db");
    expect(locationLabel(media)).toBe("service web: /app/media");
    expect(locationLabel({ type: "files", volume: "data", paths: [] })).toBe("volume data (all of it)");
    expect(locationLabel({ type: "files", volume: "data" })).toBe("volume data (all of it)");
    expect(locationLabel(replica)).toBe("service litestream");
    expect(locationLabel({ type: "sqlite", service: "web" })).toBe("service web");
  });
  it("reads ages, sizes and expiry", () => {
    const now = 1_800_000_000_000;
    const s = now / 1000;
    expect(relativeAge(s - 10, now)).toBe("just now");
    expect(relativeAge(s - 240, now)).toBe("4 min ago");
    expect(relativeAge(s - 7200, now)).toBe("2 h ago");
    expect(relativeAge(s - 86_400, now)).toBe("yesterday");
    expect(relativeAge(s - 3 * 86_400, now)).toBe("3 days ago");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1500)).toBe("1.5 kB");
    expect(formatBytes(25_000_000)).toBe("25 MB");
    expect(formatBytes(3e15)).toBe("3000 TB");
    const point = { id: "x", time: s, kind: "manual" as const, size: 1 };
    expect(expiresNote(point, now)).toBe("");
    expect(expiresNote({ ...point, expires: s + 30 * 86_400 }, now)).toBe("");
    expect(expiresNote({ ...point, expires: s + 3 * 86_400 }, now)).toBe("expires in 3 days");
    expect(expiresNote({ ...point, expires: s + 3600 }, now)).toBe("expires in 1 day");
    expect(expiresNote({ ...point, expires: s - 10 }, now)).toBe("expires today");
  });
  it("flags late and failing backups", () => {
    expect(needsAttention(undefined)).toBe(false);
    expect(needsAttention([{ name: "a", state: "ok" }, { name: "b", state: "never" }])).toBe(false);
    expect(needsAttention([{ name: "a", state: "late" }])).toBe(true);
    expect(needsAttention([{ name: "a", state: "failing" }])).toBe(true);
  });
  it("checks a new backup's name", () => {
    expect(backupNameError("audit", ["database"])).toBe("");
    expect(backupNameError("Audit", [])).toContain("lowercase");
    expect(backupNameError("database", ["database"])).toBe("The app already has a backup named database.");
  });
});
```

**Create `web/src/lib/delete-app.test.ts`:**

```ts
import { describe, expect, it } from "vitest";

import { deleteOutcome } from "@/lib/delete-app";

const res = (over = {}) => ({ warnings: [] as string[], volumesDeleted: false, backupsDeleted: false, ...over });

describe("deleteOutcome", () => {
  it("names what was deleted", () => {
    expect(deleteOutcome({ withVolumes: false, withBackups: false }, res())).toEqual({ level: "success", title: "App deleted" });
    expect(deleteOutcome({ withVolumes: true, withBackups: true }, res({ volumesDeleted: true, backupsDeleted: true }))).toEqual({
      level: "success",
      title: "App and volumes and backups deleted",
    });
    expect(deleteOutcome({ withVolumes: false, withBackups: true }, res({ backupsDeleted: true })).title).toBe("App and backups deleted");
  });
  it("never claims an ignored request", () => {
    const out = deleteOutcome({ withVolumes: true, withBackups: true }, res({ volumesDeleted: true }));
    expect(out).toMatchObject({ level: "warning", title: "App deleted, but its backups were kept" });
    expect(deleteOutcome({ withVolumes: true, withBackups: true }, res()).title).toBe("App deleted, but its volumes and backups were kept");
  });
  it("passes the cleanup warnings on", () => {
    expect(deleteOutcome({ withVolumes: false, withBackups: false }, res({ warnings: ["a", "b"] }))).toEqual({
      level: "warning",
      title: "App deleted with cleanup warnings",
      description: "a\nb",
    });
  });
});
```

**Modify `web/src/context/apps-context.test.tsx`** — replace (1 of 4):

```tsx
import type { ReactNode } from "react";
import { renderHook, waitFor as waitUntil } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
```

with:

```tsx
import type { ReactNode } from "react";
import { act, renderHook, waitFor as waitUntil } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
```

**Modify `web/src/context/apps-context.test.tsx`** — replace (2 of 4):

```tsx
const waitFor = vi.fn();
vi.mock("@/context/use-notifications", () => ({
  useNotifications: () => ({ subscribe: () => () => {}, waitFor, connected: true }),
}));
```

with:

```tsx
const waitFor = vi.fn();
const handlers: ((n: { type: string; ref?: string; payload?: unknown }) => void)[] = [];
vi.mock("@/context/use-notifications", () => ({
  useNotifications: () => ({
    subscribe: (h: (typeof handlers)[number]) => {
      handlers.push(h);
      return () => {};
    },
    waitFor,
    connected: true,
  }),
}));
```

**Modify `web/src/context/apps-context.test.tsx`** — replace (3 of 4):

```tsx
  if (url.includes("/api/v1/app/get-apps?")) return json(200, { data: { apps: [] } });
  if (url.includes("/api/v1/app/get-apps-status")) return json(200, { data: { apps: [] } });
  if (url.includes("/api/v1/app/refresh-apps")) return json(202, { data: {} });
```

with:

```tsx
  if (url.includes("/api/v1/app/get-apps?")) return json(200, { data: { apps: [] } });
  if (url.includes("/api/v1/app/get-apps-status")) {
    return json(200, {
      data: {
        apps: [
          { app_id: "a1", status_code: 1, backups: [{ name: "db", state: "late", last_success: 1 }] },
          { app_id: "a2", status_code: 5, backups: null },
        ],
      },
    });
  }
  if (url.includes("/api/v1/app/delete-app")) return json(202, { data: { request_id: "del-1" } });
  if (url.includes("/api/v1/app/refresh-apps")) return json(202, { data: {} });
```

**Modify `web/src/context/apps-context.test.tsx`** — replace (4 of 4):

```tsx
  });
});
```

with:

```tsx
  });
});

describe("AppsProvider backups", () => {
  beforeEach(() => vi.stubGlobal("fetch", fetchMock));
  afterEach(() => vi.unstubAllGlobals());

  it("keeps each app's backup health from the status and its pushes", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.backupsByApp.a1).toEqual([{ name: "db", state: "late", last_success: 1 }]));
    expect(result.current.statusByApp).toEqual({ a1: 1, a2: 5 });
    expect(result.current.backupsByApp.a2).toBeUndefined();

    act(() => {
      for (const h of handlers) {
        h({ type: "apps_status", payload: { server_id: "srv-1", apps: [{ app_id: "a2", status_code: 1, backups: [{ name: "media", state: "failing" }] }] } });
        h({ type: "apps_status", payload: { server_id: "other", apps: [] } });
      }
    });
    await waitUntil(() => expect(result.current.backupsByApp).toEqual({ a2: [{ name: "media", state: "failing" }] }));
  });

  it("asks to delete the backups with the app and reports whether they were", async () => {
    const { result } = renderHook(() => useApps(), { wrapper });
    await waitUntil(() => expect(result.current.loading).toBe(false));
    waitFor.mockResolvedValueOnce({ status: 0, payload: { warnings: [], volumes_deleted: false, backups_deleted: true } });
    await expect(result.current.remove("a1", { deleteBackups: true })).resolves.toEqual({
      warnings: [],
      volumesDeleted: false,
      backupsDeleted: true,
    });
    // fetch's second argument, which the mock's signature leaves out.
    const del = fetchMock.mock.calls.find(([u]) => String(u).includes("/api/v1/app/delete-app")) as unknown as [string, RequestInit];
    expect(JSON.parse(String(del[1].body))).toEqual({
      app_id: "a1",
      delete_volumes: false,
      delete_backups: true,
      server_id: "srv-1",
    });
  });
});
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
CI=true pnpm --dir web exec vitest run src/lib src/context src/components/app-grid.test.tsx src/components/delete-app-dialog.test.tsx
```

Expected: FAIL — `Failed to resolve import "@/lib/backups"` (and the other new modules).

- [ ] **Step 3: Implement**

**Create `web/src/components/delete-app-dialog.tsx`:**

```tsx
import { useState } from "react";
import { Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import type { DeleteChoice } from "@/lib/delete-app";
import { cn } from "@/lib/utils";

// DeleteAppDialog confirms deleting the app and, opt-in, its volumes and its
// backups.
export function DeleteAppDialog({
  name,
  disabled,
  onConfirm,
}: {
  name: string;
  disabled: boolean;
  onConfirm: (choice: DeleteChoice) => void;
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button
          variant="outline"
          className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
          disabled={disabled}
        >
          <Trash2 className="size-4" /> Delete {name}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <DeleteAppConfirm name={name} onConfirm={onConfirm} />
      </AlertDialogContent>
    </AlertDialog>
  );
}

// DeleteAppConfirm owns the choices. AlertDialogContent unmounts it on
// close, so every opening starts with the safe default (unticked).
function DeleteAppConfirm({ name, onConfirm }: { name: string; onConfirm: (choice: DeleteChoice) => void }) {
  const [withVolumes, setWithVolumes] = useState(false);
  const [withBackups, setWithBackups] = useState(false);
  const label = withVolumes && withBackups
    ? "Delete app, volumes and backups"
    : withVolumes
      ? "Delete app and volumes"
      : withBackups
        ? "Delete app and backups"
        : "Delete app";
  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>Delete {name}?</AlertDialogTitle>
        <AlertDialogDescription>
          Its containers are stopped and removed, along with the deployment and every stored revision. This cannot be
          undone.
        </AlertDialogDescription>
      </AlertDialogHeader>
      <Choice
        id="delete-volumes"
        checked={withVolumes}
        onChange={setWithVolumes}
        label="Also delete its volumes"
        hint="Permanently erases the data in this app's Docker volumes. Unticked, the volumes are kept; you can delete them later in Server Settings → Volumes."
      />
      <Choice
        id="delete-backups"
        checked={withBackups}
        onChange={setWithBackups}
        label="Also delete its backups"
        hint="Permanently deletes every copy of this app in its backup storages. Unticked, the copies are kept until they reach this server's retention limit; you can delete them sooner in Server Settings → Storages."
      />
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction
          className={buttonVariants({ variant: "destructive" })}
          onClick={() => onConfirm({ withVolumes, withBackups })}
        >
          {label}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  );
}

// Choice is one opt-in: neutral until ticked, then unmistakably
// destructive — the safe choice (keep the data) is the default.
function Choice({
  id,
  checked,
  onChange,
  label,
  hint,
}: {
  id: string;
  checked: boolean;
  onChange: (v: boolean) => void;
  label: string;
  hint: string;
}) {
  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-md border p-3 transition-colors",
        checked && "border-destructive/50 bg-destructive/5",
      )}
    >
      <Checkbox
        id={id}
        className="mt-px data-[state=checked]:border-destructive data-[state=checked]:bg-destructive data-[state=checked]:text-destructive-foreground"
        aria-describedby={`${id}-hint`}
        checked={checked}
        onCheckedChange={(v) => onChange(v === true)}
      />
      <div className="grid gap-1">
        <Label htmlFor={id} className={cn("cursor-pointer leading-snug", checked && "text-destructive")}>
          {label}
        </Label>
        <p id={`${id}-hint`} className="text-sm leading-snug text-muted-foreground">
          {hint}
        </p>
      </div>
    </div>
  );
}
```

**Create `web/src/context/use-backups.ts`:**

```ts
import { useCallback, useMemo } from "react";

import { useNotifications } from "@/context/use-notifications";
import { useServers } from "@/context/use-servers";
import { readApiError } from "@/lib/api";
import type {
  BackupConfig,
  EffectiveBackup,
  Orphan,
  RestorePoint,
  RestoreResult,
  RunBackupResult,
} from "@/lib/backups";
import { apiBaseUrl } from "@/config";

const base = apiBaseUrl.endsWith("/") ? apiBaseUrl.slice(0, -1) : apiBaseUrl;

// How long to wait for a result. A Back up now or a restore answers when it
// ends, after any backup queued ahead of it: the agent bounds the wait at 2
// hours. update-backups may redeploy the app (10 minutes on the agent).
export const BACKUP_RUN_TIMEOUT_MS = 125 * 60_000;
export const BACKUP_UPDATE_TIMEOUT_MS = 11 * 60_000;

export type BackupsUpdated = { backups: EffectiveBackup[]; redeployed: boolean; warnings: string[] };

// useBackups exposes the backup routes of the active server: an app's
// backups list, its restore points, Back up now, restore, and the backups of
// apps not on the server. Each dispatches an agent command and awaits its
// SSE result. These routes take server_id in the body (POST) or the query
// (GET).
export function useBackups() {
  const { activeServerId: serverId } = useServers();
  const { waitFor } = useNotifications();

  const dispatch = useCallback(
    async <T>(path: string, params: Record<string, unknown>, opts: { post?: boolean; timeoutMs?: number } = {}): Promise<T> => {
      if (!serverId) throw new Error("No active server");
      let url = `${base}${path}`;
      let init: RequestInit = { credentials: "include" };
      if (opts.post) {
        init = {
          ...init,
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ server_id: serverId, ...params }),
        };
      } else {
        const q = new URLSearchParams({ server_id: serverId });
        for (const [k, v] of Object.entries(params)) q.set(k, String(v));
        url += `?${q.toString()}`;
      }
      const res = await fetch(url, init);
      if (!res.ok) throw await readApiError(res);
      const accepted = (await res.json()) as { data?: { request_id?: string } };
      const ref = accepted.data?.request_id;
      if (!ref) throw new Error("No request id");
      const result = await waitFor(ref, opts.timeoutMs);
      if (result.status && result.status !== 0) throw new Error(result.error || "Operation failed");
      return result.payload as T;
    },
    [serverId, waitFor],
  );

  // updateBackups replaces the app's backups list as a whole.
  const updateBackups = useCallback(
    async (appId: string, backups: BackupConfig[]): Promise<BackupsUpdated> => {
      const res = await dispatch<{ backups?: EffectiveBackup[] | null; redeployed?: boolean; warnings?: string[] | null }>(
        "/api/v1/app/update-backups",
        { app_id: appId, backups },
        { post: true, timeoutMs: BACKUP_UPDATE_TIMEOUT_MS },
      );
      return { backups: res.backups ?? [], redeployed: res.redeployed ?? false, warnings: res.warnings ?? [] };
    },
    [dispatch],
  );

  const listRestorePoints = useCallback(
    async (appId: string, backup: string): Promise<RestorePoint[]> => {
      const res = await dispatch<{ points?: RestorePoint[] | null }>("/api/v1/app/list-restore-points", { app_id: appId, backup });
      return res.points ?? [];
    },
    [dispatch],
  );

  // runBackup resolves when the run ends. A backup already queued or running
  // is refused (the error says so).
  const runBackup = useCallback(
    async (appId: string, backup: string): Promise<RunBackupResult> => {
      const res = await dispatch<{ unchanged?: boolean; point?: RestorePoint | null; warnings?: string[] | null }>(
        "/api/v1/app/run-backup",
        { app_id: appId, backup },
        { post: true, timeoutMs: BACKUP_RUN_TIMEOUT_MS },
      );
      return { unchanged: res.unchanged ?? false, point: res.point ?? null, warnings: res.warnings ?? [] };
    },
    [dispatch],
  );

  // restoreBackup resolves when the restore ends.
  const restoreBackup = useCallback(
    async (appId: string, backup: string, point: string): Promise<RestoreResult> => {
      const res = await dispatch<{ restored_point: string; pre_restore_point?: string; warnings?: string[] | null }>(
        "/api/v1/app/restore-backup",
        { app_id: appId, backup, point },
        { post: true, timeoutMs: BACKUP_RUN_TIMEOUT_MS },
      );
      return { restored_point: res.restored_point, pre_restore_point: res.pre_restore_point, warnings: res.warnings ?? [] };
    },
    [dispatch],
  );

  const deleteRestorePoint = useCallback(
    async (appId: string, backup: string, point: string): Promise<void> => {
      await dispatch("/api/v1/app/delete-restore-point", { app_id: appId, backup, point }, { post: true });
    },
    [dispatch],
  );

  const listOrphans = useCallback(
    async (storage: string): Promise<Orphan[]> => {
      const res = await dispatch<{ orphans?: Orphan[] | null }>("/api/v1/storage/list-orphans", { name: storage });
      return res.orphans ?? [];
    },
    [dispatch],
  );

  const deleteOrphan = useCallback(
    async (storage: string, appId: string): Promise<number> => {
      const res = await dispatch<{ deleted?: number }>("/api/v1/storage/delete-orphan", { name: storage, app_id: appId }, { post: true });
      return res.deleted ?? 0;
    },
    [dispatch],
  );

  // serverStorages reads the server's storage names from the API's index
  // (no agent round-trip): the storage picker's choices.
  const serverStorages = useCallback(async (): Promise<string[]> => {
    if (!serverId) throw new Error("No active server");
    const res = await fetch(`${base}/api/v1/storage/get-storages?server_id=${encodeURIComponent(serverId)}`, {
      credentials: "include",
    });
    if (!res.ok) throw await readApiError(res);
    const body = (await res.json()) as { data?: { storages?: { name: string }[] | null } };
    return (body.data?.storages ?? []).map((s) => s.name);
  }, [serverId]);

  return useMemo(
    () => ({
      updateBackups,
      listRestorePoints,
      runBackup,
      restoreBackup,
      deleteRestorePoint,
      listOrphans,
      deleteOrphan,
      serverStorages,
    }),
    [updateBackups, listRestorePoints, runBackup, restoreBackup, deleteRestorePoint, listOrphans, deleteOrphan, serverStorages],
  );
}

export type Backups = ReturnType<typeof useBackups>;
```

**Create `web/src/lib/backups.ts`:**

```ts
// Backups: what an app's compose file declares under x-winterflow.backups
// (the recipe), the app's own settings on top (enabled, storage, schedule,
// retention, location — the overrides), and the agent's copies (restore
// points). Mirrors internal/domain/backup/recipe and
// internal/domain/command/backup.go; the agent checks everything again.

export type BackupType = "sqlite" | "files" | "custom";

export type BackupState = "off" | "running" | "never" | "ok" | "late" | "failing";

export type Keep = { recent?: string; daily?: number; monthly?: number };

// RecipeField is a field the recipe may set and an override may change.
export type RecipeField = "every" | "keep" | "volume" | "service" | "path" | "paths" | "exclude";

// EffectiveBackup is a backup as it runs: the recipe, the overrides on top,
// the defaults applied. from_recipe marks the fields holding the recipe's
// value; in_recipe is false for a backup added in the UI.
export type EffectiveBackup = {
  name: string;
  type: BackupType;
  enabled: boolean;
  storage: string;
  every?: string;
  keep?: Keep | null;
  volume?: string;
  service?: string;
  path?: string;
  paths?: string[] | null;
  exclude?: string[] | null;
  in_recipe: boolean;
  from_recipe: Partial<Record<RecipeField, boolean>> | null;
};

// BackupConfig is one entry of the list update-backups replaces: an
// override of a recipe backup (only what it changes, never type) or a
// backup added in the UI (type and every field it needs).
export type BackupConfig = {
  name: string;
  type?: BackupType;
  enabled?: boolean;
  storage?: string;
  every?: string;
  keep?: Keep;
  volume?: string;
  service?: string;
  path?: string;
  paths?: string[];
  exclude?: string[];
};

// BackupHealth is one backup's health in apps.status.
export type BackupHealth = {
  name: string;
  state: BackupState;
  last_success?: number; // unix seconds
  last_error?: string;
};

export type PointKind = "scheduled" | "manual" | "pre-restore";

// RestorePoint is one stored copy. time and expires are unix seconds
// (expires 0/absent: never); size is the stored size in bytes.
export type RestorePoint = { id: string; time: number; kind: PointKind; size: number; expires?: number };

export type RunBackupResult = { unchanged: boolean; point?: RestorePoint | null; warnings: string[] };

export type RestoreResult = { restored_point: string; pre_restore_point?: string; warnings: string[] };

// Orphan is an app's backup folder in a storage whose app is not on this
// server (times unix seconds; purge_at 0: never deleted automatically).
export type Orphan = {
  app_id: string;
  app_name?: string;
  deleted_at?: number;
  purge_at?: number;
  copies: number;
  bytes: number;
  newest?: number;
};

// Mount is a service's volume or bind mount as its compose file writes it.
export type Mount = { type: "volume" | "bind"; source: string; target: string };

// BackupTargets are where a backup added in the UI can point.
export type BackupTargets = { volumes: string[]; services: { name: string; mounts: Mount[] }[] };

export const MANUAL = "manual";

// SCHEDULES are the schedule picker's choices; a value outside them (set in
// the compose file) is shown as is.
export const SCHEDULES: { value: string; label: string }[] = [
  { value: MANUAL, label: "Manual" },
  { value: "15m", label: "Every 15 minutes" },
  { value: "30m", label: "Every 30 minutes" },
  { value: "1h", label: "Every hour" },
  { value: "6h", label: "Every 6 hours" },
  { value: "12h", label: "Every 12 hours" },
  { value: "24h", label: "Every day" },
];

// durationMs parses a Go duration ("1h30m", "15m0s", "24h0m0s"); NaN when
// it isn't one.
export function durationMs(d: string): number {
  const re = /(\d+(?:\.\d+)?)(ms|h|m|s)/g;
  const unit: Record<string, number> = { h: 3_600_000, m: 60_000, s: 1000, ms: 1 };
  let total = 0;
  let consumed = 0;
  for (const m of d.matchAll(re)) {
    total += parseFloat(m[1]) * unit[m[2]];
    consumed += m[0].length;
  }
  return d && consumed === d.length ? total : NaN;
}

// scheduleLabel reads a backup's every: "Manual", a picker label, or
// "Every <duration>".
export function scheduleLabel(every: string | undefined): string {
  if (!every || every === MANUAL) return "Manual";
  const ms = durationMs(every);
  const known = SCHEDULES.find((s) => s.value !== MANUAL && durationMs(s.value) === ms);
  return known ? known.label : `Every ${every}`;
}

// everyError mirrors the recipe's rule: manual, or a duration of at least
// 5 minutes; "" means valid.
export function everyError(every: string): string {
  if (every === MANUAL) return "";
  const ms = durationMs(every);
  if (Number.isNaN(ms)) return "Use a duration like 15m, 2h or 24h, or Manual.";
  return ms < 5 * 60_000 ? "Back up at most every 5 minutes." : "";
}

// changed reports whether a field holds an override (not the recipe's value).
export function changed(b: EffectiveBackup, field: RecipeField): boolean {
  return b.in_recipe && !b.from_recipe?.[field];
}

// configOf is the list entry that keeps b as it is: a UI-added backup in
// full, a recipe backup's on/off, storage and the fields it overrides.
export function configOf(b: EffectiveBackup): BackupConfig {
  const c: BackupConfig = { name: b.name };
  if (b.enabled) c.enabled = true;
  if (b.storage) c.storage = b.storage;
  const take = (f: RecipeField) => !b.in_recipe || changed(b, f);
  if (!b.in_recipe) c.type = b.type;
  if (b.type === "custom") {
    if (!b.in_recipe && b.service) c.service = b.service;
    return c;
  }
  if (take("every") && b.every) c.every = b.every;
  if (take("keep") && b.keep) c.keep = { ...b.keep };
  // The location moves as a whole: volume or service, and its paths.
  if (take("volume") || take("service")) {
    if (b.volume) c.volume = b.volume;
    if (b.service) c.service = b.service;
  }
  if (take("path") && b.path) c.path = b.path;
  // An explicit empty list (the whole volume, no excludes) comes back
  // absent: an override of it is sent as [] again, never invented.
  if (b.type === "files") {
    if (take("paths") && (b.paths || changed(b, "paths"))) c.paths = [...(b.paths ?? [])];
    if (take("exclude") && (b.exclude || changed(b, "exclude"))) c.exclude = [...(b.exclude ?? [])];
  }
  return c;
}

// configsWith is the whole list update-backups takes: every backup kept as
// it is, but name, which is replaced by next (null: removed — only a
// UI-added backup can go). A name not in the list is appended (Add backup).
// Entries that say nothing beyond their name are left out.
export function configsWith(backups: EffectiveBackup[], name: string, next: BackupConfig | null): BackupConfig[] {
  const out: BackupConfig[] = [];
  let found = false;
  for (const b of backups) {
    if (b.name === name) {
      found = true;
      if (next) out.push(next);
      continue;
    }
    out.push(configOf(b));
  }
  if (!found && next) out.push(next);
  return out.filter((c) => Object.keys(c).length > 1);
}

// resetField drops a field's override from a backup's entry, so it follows
// the recipe again. The location fields go together.
export function resetField(c: BackupConfig, field: RecipeField): BackupConfig {
  const next = { ...c };
  const drop: RecipeField[] =
    field === "volume" || field === "service" || field === "path" || field === "paths" ? ["volume", "service", "path", "paths"] : [field];
  for (const f of drop) delete next[f];
  return next;
}

// keepDays is how far back a retention reaches, in days: the recent window,
// the daily tier and the monthly one (31 days a month).
export function keepDays(keep: Keep | null | undefined): number {
  const recent = durationMs(keep?.recent || "24h") / 86_400_000;
  return Math.max(recent || 1, keep?.daily ?? 14, (keep?.monthly ?? 6) * 31);
}

// cappedNote is the note under a retention that reaches past the server's
// ceiling (retention days, 0 = none), else "".
export function cappedNote(keep: Keep | null | undefined, ceiling: number): string {
  return ceiling > 0 && keepDays(keep) > ceiling ? `Capped at ${ceiling} days on this server.` : "";
}

// keepLabel reads a retention: "24h, 14 daily, 6 monthly".
export function keepLabel(keep: Keep | null | undefined): string {
  // Go writes 24h as 24h0m0s: drop the zero minutes and seconds.
  const recent = keep?.recent ? keep.recent.replace(/(\d)m0s$/, "$1m").replace(/h0m$/, "h") : "24h";
  return `${recent}, ${keep?.daily ?? 14} daily, ${keep?.monthly ?? 6} monthly`;
}

// locationLabel reads where a backup's data is: "volume data: app.db" or
// "service web: /app/media, /app/config"; a whole volume reads "volume
// data (all of it)".
export function locationLabel(b: Pick<EffectiveBackup, "type" | "volume" | "service" | "path" | "paths">): string {
  const where = b.volume ? `volume ${b.volume}` : b.service ? `service ${b.service}` : "";
  if (b.type === "custom") return where;
  const paths = b.type === "sqlite" ? (b.path ? [b.path] : []) : (b.paths ?? []);
  if (paths.length === 0) return b.volume ? `${where} (all of it)` : where;
  return `${where}: ${paths.join(", ")}`;
}

// relativeAge reads a unix time as "4 min ago" (now in ms).
export function relativeAge(unix: number, now: number = Date.now()): string {
  const secs = Math.max(0, Math.round(now / 1000 - unix));
  if (secs < 60) return "just now";
  if (secs < 3600) return `${Math.floor(secs / 60)} min ago`;
  if (secs < 86_400) return `${Math.floor(secs / 3600)} h ago`;
  const days = Math.floor(secs / 86_400);
  return days === 1 ? "yesterday" : `${days} days ago`;
}

// formatBytes reads a size: "512 B", "1.5 MB".
export function formatBytes(n: number): string {
  if (n < 1000) return `${n} B`;
  const units = ["kB", "MB", "GB", "TB"];
  let v = n / 1000;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

// expiresNote is "expires in N days" for a copy within 14 days of the
// server's ceiling (now in ms), else "".
export function expiresNote(p: RestorePoint, now: number = Date.now()): string {
  if (!p.expires) return "";
  const days = Math.ceil((p.expires * 1000 - now) / 86_400_000);
  if (days > 14) return "";
  if (days <= 0) return "expires today";
  return days === 1 ? "expires in 1 day" : `expires in ${days} days`;
}

export const KIND_LABELS: Record<PointKind, string> = {
  scheduled: "Scheduled",
  manual: "Manual",
  "pre-restore": "Before a restore",
};

export const STATE_LABELS: Record<BackupState, string> = {
  off: "Off",
  running: "Running",
  never: "Not run yet",
  ok: "OK",
  late: "Late",
  failing: "Failing",
};

// needsAttention reports whether an app's backups include a late or failing
// one (the app grid's warning dot).
export function needsAttention(health: BackupHealth[] | undefined): boolean {
  return (health ?? []).some((h) => h.state === "late" || h.state === "failing");
}

const NAME_RE = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;

// backupNameError mirrors recipe.ValidateName plus uniqueness in the app;
// "" means valid.
export function backupNameError(name: string, taken: readonly string[]): string {
  if (!NAME_RE.test(name)) return "Use 1-32 lowercase letters, digits and inner hyphens, like database.";
  return taken.includes(name) ? `The app already has a backup named ${name}.` : "";
}
```

**Create `web/src/lib/delete-app.ts`:**

```ts
import type { DeleteAppResult } from "@/context/apps-context-base";

// DeleteChoice is what to delete with the app.
export type DeleteChoice = { withVolumes: boolean; withBackups: boolean };

// DeleteOutcome is the toast a finished delete shows.
export type DeleteOutcome = { level: "success" | "warning"; title: string; description?: string };

// deleteOutcome reads a delete's result. An agent that predates volume or
// backup deletion ignores the flag (it reports false): the user must not be
// told the data is gone.
export function deleteOutcome(choice: DeleteChoice, res: DeleteAppResult): DeleteOutcome {
  const kept: string[] = [];
  if (choice.withVolumes && !res.volumesDeleted) kept.push("volumes");
  if (choice.withBackups && !res.backupsDeleted) kept.push("backups");
  if (kept.length > 0) {
    return {
      level: "warning",
      title: `App deleted, but its ${kept.join(" and ")} were kept`,
      description: "This server's agent is too old to delete them. Update the agent, then remove them in Server Settings.",
    };
  }
  if (res.warnings.length) {
    return { level: "warning", title: "App deleted with cleanup warnings", description: res.warnings.join("\n") };
  }
  const also = [choice.withVolumes && "volumes", choice.withBackups && "backups"].filter(Boolean);
  return { level: "success", title: also.length ? `App and ${also.join(" and ")} deleted` : "App deleted" };
}
```

**Modify `web/src/components/app-grid.tsx`** — replace (1 of 4):

```tsx
import { useApps } from "@/context/use-apps";
import { cn } from "@/lib/utils";
```

with:

```tsx
import { useApps } from "@/context/use-apps";
import { needsAttention } from "@/lib/backups";
import { cn } from "@/lib/utils";
```

**Modify `web/src/components/app-grid.tsx`** — replace (2 of 4):

```tsx
export function AppGrid() {
  const { apps, statusByApp, loading, error } = useApps();

```

with:

```tsx
export function AppGrid() {
  const { apps, statusByApp, backupsByApp, loading, error } = useApps();

```

**Modify `web/src/components/app-grid.tsx`** — replace (3 of 4):

```tsx
          AppStatusCode.Unknown;
        return (
          <Link
            key={app.id}
            to={`/app/${app.id}`}
            title={`${app.name}: ${status.label}`}
            className="app-card-in block"
```

with:

```tsx
          AppStatusCode.Unknown;
        // A late or failing backup: a small dot, never the card's color
        // (backup health doesn't change the app's status).
        const backupWarning = needsAttention(backupsByApp?.[app.id]);
        return (
          <Link
            key={app.id}
            to={`/app/${app.id}`}
            title={`${app.name}: ${status.label}${backupWarning ? " (a backup is late or failing)" : ""}`}
            className="app-card-in block"
```

**Modify `web/src/components/app-grid.tsx`** — replace (4 of 4):

```tsx
              className={cn(
                "flex h-32 w-32 flex-col items-center border-2 p-4 transition-all hover:rotate-3 hover:bg-muted/50",
                status.border,
              )}
            >
              <div className="flex flex-1 flex-col items-center gap-2">
```

with:

```tsx
              className={cn(
                "relative flex h-32 w-32 flex-col items-center border-2 p-4 transition-all hover:rotate-3 hover:bg-muted/50",
                status.border,
              )}
            >
              {backupWarning ? (
                <span
                  role="img"
                  aria-label="A backup is late or failing"
                  className="absolute top-2 right-2 size-2 rounded-full bg-amber-500 ring-2 ring-background"
                />
              ) : null}
              <div className="flex flex-1 flex-col items-center gap-2">
```

**Modify `web/src/context/apps-context-base.ts`** — replace (1 of 5):

```ts

import type { StorageVariable } from "@/types/app-config";
```

with:

```ts

import type { BackupHealth, BackupTargets, EffectiveBackup } from "@/lib/backups";
import type { StorageVariable } from "@/types/app-config";
```

**Modify `web/src/context/apps-context-base.ts`** — replace (2 of 5):

```ts
  volumesDeleted: boolean;
};
```

with:

```ts
  volumesDeleted: boolean;
  backupsDeleted: boolean;
};
```

**Modify `web/src/context/apps-context-base.ts`** — replace (3 of 5):

```ts
  statusByApp: Record<string, number>;
  loading: boolean;
```

with:

```ts
  statusByApp: Record<string, number>;
  // backupsByApp maps app id -> the health of each of its backups (live,
  // with the status).
  backupsByApp: Record<string, BackupHealth[]>;
  loading: boolean;
```

**Modify `web/src/context/apps-context-base.ts`** — replace (4 of 5):

```ts
  // remove resolves with cleanup warnings (e.g. a volume still in use) and
  // whether the agent honoured deleteVolumes — an older agent ignores the
  // flag, so volumesDeleted stays false and the caller must not claim success.
  remove: (
    appId: string,
    opts?: { deleteVolumes?: boolean },
  ) => Promise<DeleteAppResult>;
```

with:

```ts
  // remove resolves with cleanup warnings (e.g. a volume still in use) and
  // whether the agent honoured deleteVolumes and deleteBackups — an older
  // agent ignores the flags, so they stay false and the caller must not
  // claim success.
  remove: (
    appId: string,
    opts?: { deleteVolumes?: boolean; deleteBackups?: boolean },
  ) => Promise<DeleteAppResult>;
```

**Modify `web/src/context/apps-context-base.ts`** — replace (5 of 5):

```ts
  storage_variables?: StorageVariable[] | null;
  revision: number;
```

with:

```ts
  storage_variables?: StorageVariable[] | null;
  // The app's backups (recipe + its settings), why they can't be read,
  // where a new one can point, and the server's BACKUP_RETENTION_DAYS.
  backups?: EffectiveBackup[] | null;
  backups_error?: string;
  backup_targets?: BackupTargets | null;
  retention_days?: number;
  revision: number;
```

**Modify `web/src/context/apps-context.tsx`** — replace (1 of 9):

```tsx
import { readApiError } from "@/lib/api";

```

with:

```tsx
import { readApiError } from "@/lib/api";
import type { BackupHealth } from "@/lib/backups";

```

**Modify `web/src/context/apps-context.tsx`** — replace (2 of 9):

```tsx
  status_code: number;
};

```

with:

```tsx
  status_code: number;
  backups?: BackupHealth[] | null;
};

// byApp splits a status list into the status codes and the backup health
// by app id.
function byApp(list: AppStatusResponse[]) {
  const status: Record<string, number> = {};
  const backups: Record<string, BackupHealth[]> = {};
  for (const s of list) {
    status[s.app_id] = s.status_code;
    if (s.backups?.length) backups[s.app_id] = s.backups;
  }
  return { status, backups };
}

```

**Modify `web/src/context/apps-context.tsx`** — replace (3 of 9):

```tsx
  const [statusByApp, setStatusByApp] = useState<Record<string, number>>({});
  const [loading, setLoading] = useState(true);
```

with:

```tsx
  const [statusByApp, setStatusByApp] = useState<Record<string, number>>({});
  const [backupsByApp, setBackupsByApp] = useState<Record<string, BackupHealth[]>>({});
  const [loading, setLoading] = useState(true);
```

**Modify `web/src/context/apps-context.tsx`** — replace (4 of 9):

```tsx
    if (!isAuthenticated || !activeServerId) {
      if (mounted.current) setStatusByApp({});
      return;
```

with:

```tsx
    if (!isAuthenticated || !activeServerId) {
      if (mounted.current) {
        setStatusByApp({});
        setBackupsByApp({});
      }
      return;
```

**Modify `web/src/context/apps-context.tsx`** — replace (5 of 9):

```tsx
      if (!mounted.current) return;
      const map: Record<string, number> = {};
      for (const s of result.data?.apps ?? []) {
        map[s.app_id] = s.status_code;
      }
      setStatusByApp(map);
    } catch {
```

with:

```tsx
      if (!mounted.current) return;
      const { status, backups } = byApp(result.data?.apps ?? []);
      setStatusByApp(status);
      setBackupsByApp(backups);
    } catch {
```

**Modify `web/src/context/apps-context.tsx`** — replace (6 of 9):

```tsx
            server_id?: string;
            apps?: { app_id: string; status_code: number }[] | null;
          }
        | undefined;
      if (!p?.server_id || p.server_id !== activeServerId) return;
      const map: Record<string, number> = {};
      for (const s of p.apps ?? []) {
        map[s.app_id] = s.status_code;
      }
      setStatusByApp(map);
    });
```

with:

```tsx
            server_id?: string;
            apps?: AppStatusResponse[] | null;
          }
        | undefined;
      if (!p?.server_id || p.server_id !== activeServerId) return;
      const { status, backups } = byApp(p.apps ?? []);
      setStatusByApp(status);
      setBackupsByApp(backups);
    });
```

**Modify `web/src/context/apps-context.tsx`** — replace (7 of 9):

```tsx
      appId: string,
      opts?: { deleteVolumes?: boolean },
    ): Promise<DeleteAppResult> => {
      const { payload } = await dispatchAndWait("delete-app", {
        app_id: appId,
        delete_volumes: opts?.deleteVolumes ?? false,
      });
      const res = payload as
        | { warnings?: string[]; volumes_deleted?: boolean }
        | undefined;
      return {
        warnings: res?.warnings ?? [],
        volumesDeleted: res?.volumes_deleted ?? false,
      };
```

with:

```tsx
      appId: string,
      opts?: { deleteVolumes?: boolean; deleteBackups?: boolean },
    ): Promise<DeleteAppResult> => {
      const { payload } = await dispatchAndWait("delete-app", {
        app_id: appId,
        delete_volumes: opts?.deleteVolumes ?? false,
        delete_backups: opts?.deleteBackups ?? false,
      });
      const res = payload as
        | { warnings?: string[]; volumes_deleted?: boolean; backups_deleted?: boolean }
        | undefined;
      return {
        warnings: res?.warnings ?? [],
        volumesDeleted: res?.volumes_deleted ?? false,
        backupsDeleted: res?.backups_deleted ?? false,
      };
```

**Modify `web/src/context/apps-context.tsx`** — replace (8 of 9):

```tsx
    () => ({
      apps,
      statusByApp,
      loading,
      error,
      refresh,
```

with:

```tsx
    () => ({
      apps,
      statusByApp,
      backupsByApp,
      loading,
      error,
      refresh,
```

**Modify `web/src/context/apps-context.tsx`** — replace (9 of 9):

```tsx
    [
      apps,
      statusByApp,
      loading,
      error,
      refresh,
```

with:

```tsx
    [
      apps,
      statusByApp,
      backupsByApp,
      loading,
      error,
      refresh,
```

**Modify `web/src/pages/app-details.tsx`** — replace (1 of 9):

```tsx
  Square,
  Trash2,
} from "lucide-react";
```

with:

```tsx
  Square,
} from "lucide-react";
```

**Modify `web/src/pages/app-details.tsx`** — replace (2 of 9):

```tsx
import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Spinner } from "@/components/ui/spinner";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
```

with:

```tsx
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Spinner } from "@/components/ui/spinner";
import { Input } from "@/components/ui/input";
import {
```

**Modify `web/src/pages/app-details.tsx`** — replace (3 of 9):

```tsx
import { AppStatusIndicator } from "@/components/app-status-badge";
import { useApps } from "@/context/use-apps";
```

with:

```tsx
import { AppStatusIndicator } from "@/components/app-status-badge";
import { DeleteAppDialog } from "@/components/delete-app-dialog";
import { useApps } from "@/context/use-apps";
```

**Modify `web/src/pages/app-details.tsx`** — replace (4 of 9):

```tsx
import { readApiError } from "@/lib/api";
import { cn } from "@/lib/utils";
```

with:

```tsx
import { readApiError } from "@/lib/api";
import { deleteOutcome, type DeleteChoice } from "@/lib/delete-app";
import { cn } from "@/lib/utils";
```

**Modify `web/src/pages/app-details.tsx`** — replace (5 of 9):

```tsx
  onRename: (appId: string, name: string) => Promise<void>;
  onDelete: (opts: { deleteVolumes: boolean }) => Promise<DeleteAppResult>;
}) {
```

with:

```tsx
  onRename: (appId: string, name: string) => Promise<void>;
  onDelete: (opts: { deleteVolumes: boolean; deleteBackups: boolean }) => Promise<DeleteAppResult>;
}) {
```

**Modify `web/src/pages/app-details.tsx`** — replace (6 of 9):

```tsx

  const del = async (withVolumes: boolean) => {
    setBusy(true);
    try {
      const { warnings, volumesDeleted } = await onDelete({
        deleteVolumes: withVolumes,
      });
      if (withVolumes && !volumesDeleted) {
        // An agent that predates volume deletion ignores the flag.
        toast.warning("App deleted, but its volumes were kept", {
          description:
            "This server's agent is too old to delete volumes. Update the agent, then remove them in Server Settings → Volumes.",
        });
      } else if (warnings.length) {
        toast.warning("App deleted with cleanup warnings", {
          description: warnings.join("\n"),
        });
      } else {
        toast.success(withVolumes ? "App and volumes deleted" : "App deleted");
      }
```

with:

```tsx

  const del = async (choice: DeleteChoice) => {
    setBusy(true);
    try {
      const res = await onDelete({ deleteVolumes: choice.withVolumes, deleteBackups: choice.withBackups });
      const out = deleteOutcome(choice, res);
      if (out.level === "warning") {
        toast.warning(out.title, { description: out.description });
      } else {
        toast.success(out.title);
      }
```

**Modify `web/src/pages/app-details.tsx`** — replace (7 of 9):

```tsx
            Stops the containers and removes the deployment and its history.
            Volumes are kept unless you choose otherwise.
          </p>
```

with:

```tsx
            Stops the containers and removes the deployment and its history.
            Volumes and backups are kept unless you choose otherwise.
          </p>
```

**Modify `web/src/pages/app-details.tsx`** — replace (8 of 9):

```tsx
            disabled={busy}
            onConfirm={(withVolumes) => void del(withVolumes)}
          />
```

with:

```tsx
            disabled={busy}
            onConfirm={(choice) => void del(choice)}
          />
```

**Modify `web/src/pages/app-details.tsx`** — replace (9 of 9):

```tsx
}

// DeleteAppDialog confirms deleting the app and, opt-in, its volumes.
function DeleteAppDialog({
  name,
  disabled,
  onConfirm,
}: {
  name: string;
  disabled: boolean;
  onConfirm: (withVolumes: boolean) => void;
}) {
  return (
    <AlertDialog>
      <AlertDialogTrigger asChild>
        <Button variant="outline" className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive" disabled={disabled}>
          <Trash2 className="size-4" /> Delete {name}
        </Button>
      </AlertDialogTrigger>
      <AlertDialogContent>
        <DeleteAppConfirm name={name} onConfirm={onConfirm} />
      </AlertDialogContent>
    </AlertDialog>
  );
}

// DeleteAppConfirm owns the volumes choice. AlertDialogContent unmounts it on
// close, so every opening starts with the safe default (unticked).
function DeleteAppConfirm({
  name,
  onConfirm,
}: {
  name: string;
  onConfirm: (withVolumes: boolean) => void;
}) {
  const [withVolumes, setWithVolumes] = useState(false);
  return (
    <>
      <AlertDialogHeader>
        <AlertDialogTitle>Delete {name}?</AlertDialogTitle>
        <AlertDialogDescription>
          Its containers are stopped and removed, along with the
          deployment and every stored revision. This cannot be undone.
        </AlertDialogDescription>
      </AlertDialogHeader>
      {/* Neutral until ticked, then unmistakably destructive: the
          safe choice (keep the data) is the default. */}
      <div
        className={cn(
          "flex items-start gap-3 rounded-md border p-3 transition-colors",
          withVolumes && "border-destructive/50 bg-destructive/5",
        )}
      >
        <Checkbox
          id="delete-volumes"
          className="mt-px data-[state=checked]:border-destructive data-[state=checked]:bg-destructive data-[state=checked]:text-destructive-foreground"
          aria-describedby="delete-volumes-hint"
          checked={withVolumes}
          onCheckedChange={(v) => setWithVolumes(v === true)}
        />
        <div className="grid gap-1">
          <Label
            htmlFor="delete-volumes"
            className={cn(
              "cursor-pointer leading-snug",
              withVolumes && "text-destructive",
            )}
          >
            Also delete its volumes
          </Label>
          <p
            id="delete-volumes-hint"
            className="text-sm leading-snug text-muted-foreground"
          >
            Permanently erases the data in this app's Docker volumes.
            Unticked, the volumes are kept; you can delete them later
            in Server Settings → Volumes.
          </p>
        </div>
      </div>
      <AlertDialogFooter>
        <AlertDialogCancel>Cancel</AlertDialogCancel>
        <AlertDialogAction
          className={buttonVariants({ variant: "destructive" })}
          onClick={() => onConfirm(withVolumes)}
        >
          {withVolumes ? "Delete app and volumes" : "Delete app"}
        </AlertDialogAction>
      </AlertDialogFooter>
    </>
  );
}
```

with:

```tsx
}
```

- [ ] **Step 4: Run the tests and checks**

```bash
CI=true pnpm --dir web exec vitest run src/lib src/context src/components/app-grid.test.tsx src/components/delete-app-dialog.test.tsx
pnpm --dir web exec tsc --noEmit -p tsconfig.app.json
pnpm --dir web run lint
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/app-grid.test.tsx \
  web/src/components/delete-app-dialog.test.tsx \
  web/src/components/delete-app-dialog.tsx \
  web/src/context/use-backups.test.tsx \
  web/src/context/use-backups.ts \
  web/src/lib/backups.test.ts \
  web/src/lib/backups.ts \
  web/src/lib/delete-app.test.ts \
  web/src/lib/delete-app.ts \
  web/src/components/app-grid.tsx \
  web/src/context/apps-context-base.ts \
  web/src/context/apps-context.test.tsx \
  web/src/context/apps-context.tsx \
  web/src/pages/app-details.tsx
git commit -F - <<'MSG'
feat(web): backup health on the app grid, delete-app backups choice, backups API hook

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 11: Web: orphaned backups in Settings → Storages; path-style follows the user

Each storage row gains "Backups of apps not on this server" (an archive icon): a dialog listing the orphans (app name from the marker or "Unknown app", id, copies and size, newest copy, when the sweep deletes them — or that another server's folder is never deleted automatically) with a confirmed Delete per row. The C1 leftover: on an add, path-style follows the endpoint only until the user ticks or unticks it (before, a tick on a host name was undone by typing an IP and then a host name). Also the CI flake in `ingress-editor.test.tsx` ("re-checks an uncovered hostname…"): the edited hostname is checked after a 400 ms debounce, which left the default 1 s `findBy`/`waitFor` little room on a loaded runner — the waits get 4 s.

**Files:**
- Create: `web/src/components/orphans-dialog.test.tsx`
- Create: `web/src/components/orphans-dialog.tsx`
- Modify: `web/src/components/ingress-editor.test.tsx`
- Modify: `web/src/components/storage-dialog.tsx`
- Modify: `web/src/pages/settings-storages.test.tsx`
- Modify: `web/src/pages/settings-storages.tsx`

**Interfaces:**
- Consumes: Task 10 `useBackups`, `Backups`, `Orphan`, `formatBytes`, `relativeAge`.
- Produces:
  - `components/orphans-dialog.tsx`: `OrphansDialog({backups, storage, open, returnFocus?, onOpenChange})`.

- [ ] **Step 1: Write the failing tests**

**Create `web/src/components/orphans-dialog.test.tsx`:**

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { OrphansDialog } from "@/components/orphans-dialog";
import type { Backups } from "@/context/use-backups";
import type { Orphan } from "@/lib/backups";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) },
}));

const now = Math.floor(Date.now() / 1000);
const ORPHANS: Orphan[] = [
  { app_id: "gone-1", app_name: "Old shop", deleted_at: now - 86_400, purge_at: now + 30 * 86_400, copies: 2, bytes: 2048, newest: now - 3600 },
  { app_id: "other-server", copies: 1, bytes: 500, newest: now - 600 },
  { app_id: "gone-2", app_name: "Blog", deleted_at: now - 86_400, purge_at: now - 60, copies: 0, bytes: 0 },
  { app_id: "gone-3", app_name: "Wiki", deleted_at: now - 86_400, copies: 1, bytes: 10 },
];

function fake(over: Partial<Backups> = {}): Backups {
  return {
    listOrphans: vi.fn().mockResolvedValue(ORPHANS),
    deleteOrphan: vi.fn().mockResolvedValue(6),
    ...over,
  } as unknown as Backups;
}

describe("OrphansDialog", () => {
  beforeEach(() => {
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it("lists each app's copies and when they go", async () => {
    render(<OrphansDialog backups={fake()} storage="disk" open onOpenChange={() => {}} />);
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Old shop")).toBeInTheDocument();
    expect(within(dialog).getByText("other-server")).toBeInTheDocument();
    expect(within(dialog).getByText("Unknown app")).toBeInTheDocument();
    expect(within(dialog).getByText("Not deleted automatically (another server's?)")).toBeInTheDocument();
    expect(within(dialog).getByText("At the next daily sweep")).toBeInTheDocument();
    expect(within(dialog).getByText("Kept: no retention limit on this server")).toBeInTheDocument();
    expect(within(dialog).getByText(/^Deleted on /)).toBeInTheDocument();
    expect(within(dialog).getByText("2 · 2.0 kB")).toBeInTheDocument();
    expect(within(dialog).getByText("1 h ago")).toBeInTheDocument();
  });

  it("deletes an orphan after a confirmation", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake();
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of Old shop" }));
    const confirm = screen.getByRole("alertdialog");
    expect(confirm).toHaveTextContent("Delete every copy of Old shop (2 copies, 2.0 kB) from disk?");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Delete the backups of other-server" }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("(1 copy, 500 B)");
    await user.click(screen.getByRole("button", { name: "Delete backups" }));
    await waitFor(() => expect(screen.queryByText("other-server")).not.toBeInTheDocument());
    expect(backups.deleteOrphan).toHaveBeenCalledWith("disk", "other-server");
    expect(toastSuccess).toHaveBeenCalledWith("Backups of other-server deleted", { description: "6 objects removed from disk." });
  });

  it("reports a refused delete and a failed list", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake({ deleteOrphan: vi.fn().mockRejectedValue(new Error("the app Shop is on this server")) });
    render(<OrphansDialog backups={backups} storage="disk" open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: "Delete the backups of Old shop" }));
    await user.click(screen.getByRole("button", { name: "Delete backups" }));
    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Couldn't delete the backups", { description: "the app Shop is on this server" }));
    expect(screen.getByRole("button", { name: "Delete the backups of Old shop" })).toBeInTheDocument();

    render(<OrphansDialog backups={fake({ listOrphans: vi.fn().mockRejectedValue(new Error("the storage did not answer")) })} storage="s3" open onOpenChange={() => {}} />);
    expect(await screen.findByText("the storage did not answer")).toBeInTheDocument();
  });

  it("says when there is nothing", async () => {
    render(<OrphansDialog backups={fake({ listOrphans: vi.fn().mockResolvedValue([]) })} storage="disk" open onOpenChange={() => {}} />);
    expect(await screen.findByText("Every backup in this storage belongs to an app on this server.")).toBeInTheDocument();
  });
});
```

**Modify `web/src/components/ingress-editor.test.tsx`** — replace (1 of 2):

```tsx
    await user.type(screen.getByLabelText("Hostname"), "shop.other.app");
    await screen.findByText(/No domain on this server covers this hostname/);
    const loads = calls("/api/v1/domain/get-domains").length;
```

with:

```tsx
    await user.type(screen.getByLabelText("Hostname"), "shop.other.app");
    // An edited hostname is checked after a 400 ms debounce, which leaves the
    // default 1 s wait little room on a loaded CI runner: wait longer.
    await screen.findByText(/No domain on this server covers this hostname/, undefined, { timeout: 4000 });
    const loads = calls("/api/v1/domain/get-domains").length;
```

**Modify `web/src/components/ingress-editor.test.tsx`** — replace (2 of 2):

```tsx

    await waitFor(() =>
      expect(screen.queryByText(/No domain on this server covers/)).not.toBeInTheDocument(),
    );
    expect(calls("/api/v1/domain/get-domains").length).toBe(loads + 1);
    expect(await screen.findByText("Auto TLS")).toBeInTheDocument();
    expect(screen.getByText("*.other.app")).toBeInTheDocument();
```

with:

```tsx

    await waitFor(() => expect(screen.queryByText(/No domain on this server covers/)).not.toBeInTheDocument(), {
      timeout: 4000,
    });
    expect(calls("/api/v1/domain/get-domains").length).toBe(loads + 1);
    expect(await screen.findByText("Auto TLS", undefined, { timeout: 4000 })).toBeInTheDocument();
    expect(screen.getByText("*.other.app")).toBeInTheDocument();
```

**Modify `web/src/pages/settings-storages.test.tsx`** — replace (1 of 2):

```tsx
  },
}));

const setupUser = () => userEvent.setup({ delay: null });
```

with:

```tsx
  },
}));

const backups = vi.hoisted(() => ({ listOrphans: vi.fn(), deleteOrphan: vi.fn() }));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));

const setupUser = () => userEvent.setup({ delay: null });
```

**Modify `web/src/pages/settings-storages.test.tsx`** — replace (2 of 2):

```tsx

  it("announces only the test result, not the button", async () => {
```

with:

```tsx

  it("keeps the user's path-style when the endpoint moves to an IP and back", async () => {
    const user = setupUser();
    const dialog = await openAdd(user);
    await user.click(within(dialog).getByRole("radio", { name: "S3-compatible" }));
    const endpoint = within(dialog).getByLabelText("Endpoint");
    const pathStyle = within(dialog).getByRole("checkbox", { name: "Path-style addressing" });
    await user.type(endpoint, "https://s3.example.com");
    await user.click(pathStyle); // ticked on a host name: the user's choice
    await user.clear(endpoint);
    await user.type(endpoint, "http://10.0.0.5:9000");
    expect(pathStyle).toBeChecked();
    await user.clear(endpoint);
    await user.type(endpoint, "https://s3.example.com");
    expect(pathStyle).toBeChecked();
  });

  it("shows a storage's backups of apps not on this server", async () => {
    const user = setupUser();
    backups.listOrphans.mockResolvedValue([{ app_id: "gone-1", app_name: "Old shop", copies: 2, bytes: 2048, deleted_at: 1790812800, purge_at: 1806364800 }]);
    renderCard(fakeDocker());
    await user.click(await screen.findByRole("button", { name: "Backups of apps not on this server in disk" }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Old shop")).toBeInTheDocument();
    expect(backups.listOrphans).toHaveBeenCalledWith("disk");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Backups of apps not on this server in disk" })).toHaveFocus();
  });

  it("announces only the test result, not the button", async () => {
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
CI=true pnpm --dir web exec vitest run src/components/orphans-dialog.test.tsx src/pages/settings-storages.test.tsx src/components/ingress-editor.test.tsx
```

Expected: FAIL — `Failed to resolve import "@/components/orphans-dialog"`; the new storage-dialog test fails with the checkbox unticked.

- [ ] **Step 3: Implement**

**Create `web/src/components/orphans-dialog.tsx`:**

```tsx
import { useCallback, useEffect, useState, type RefObject } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Backups } from "@/context/use-backups";
import { formatBytes, relativeAge, type Orphan } from "@/lib/backups";
import { returnFocusTo } from "@/lib/focus";
import { cn } from "@/lib/utils";

const day = (unix: number) => new Date(unix * 1000).toLocaleDateString();

// purgeLabel says when the daily sweep deletes an orphan's last copy: a
// deleted app's folder goes at the retention limit; another server's is
// never deleted automatically.
function purgeLabel(o: Orphan, now: number): string {
  if (!o.deleted_at) return "Not deleted automatically (another server's?)";
  if (!o.purge_at) return "Kept: no retention limit on this server";
  return o.purge_at * 1000 <= now ? "At the next daily sweep" : `Deleted on ${day(o.purge_at)}`;
}

// OrphansDialog lists a storage's "Backups of apps not on this server": a
// deleted app's (named from its marker) or another server's sharing the
// storage, each deletable after a confirmation. The parent remounts it
// (key) per opening; returnFocus is where focus goes back on close.
export function OrphansDialog({
  backups,
  storage,
  open,
  returnFocus,
  onOpenChange,
}: {
  backups: Backups;
  storage: string;
  open: boolean;
  returnFocus?: RefObject<HTMLElement | null>;
  onOpenChange: (open: boolean) => void;
}) {
  const [items, setItems] = useState<Orphan[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<Orphan | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setError("");
    try {
      setItems(await backups.listOrphans(storage));
    } catch (e) {
      setError(e instanceof Error ? e.message : "The storage could not be listed.");
    }
  }, [backups, storage]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const remove = async (o: Orphan) => {
    setBusy(true);
    try {
      const n = await backups.deleteOrphan(storage, o.app_id);
      toast.success(`Backups of ${o.app_name || o.app_id} deleted`, { description: `${n} objects removed from ${storage}.` });
      setItems((prev) => prev && prev.filter((x) => x.app_id !== o.app_id));
      setConfirm(null);
    } catch (e) {
      toast.error("Couldn't delete the backups", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(false);
    }
  };

  const now = Date.now();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-3xl" onCloseAutoFocus={returnFocusTo(returnFocus)}>
        <DialogHeader>
          <DialogTitle>Backups of apps not on this server</DialogTitle>
          <DialogDescription>
            In <code className="font-mono text-foreground">{storage}</code>: copies of apps deleted here, or of another
            server's apps sharing this storage.
          </DialogDescription>
        </DialogHeader>
        {confirm ? (
          <div role="alertdialog" aria-labelledby="orphan-confirm" className="grid gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4">
            <p id="orphan-confirm" className="text-sm">
              Delete every copy of <strong>{confirm.app_name || confirm.app_id}</strong> ({confirm.copies}{" "}
              {confirm.copies === 1 ? "copy" : "copies"}, {formatBytes(confirm.bytes)}) from {storage}? This cannot be undone.
            </p>
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setConfirm(null)} disabled={busy}>
                Cancel
              </Button>
              <Button size="sm" className={buttonVariants({ variant: "destructive", size: "sm" })} onClick={() => void remove(confirm)} disabled={busy}>
                {busy ? "Deleting…" : "Delete backups"}
              </Button>
            </div>
          </div>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : items === null ? (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        ) : items.length === 0 ? (
          <p className="rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
            Every backup in this storage belongs to an app on this server.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>App</TableHead>
                <TableHead className="hidden sm:table-cell">Copies</TableHead>
                <TableHead className="hidden sm:table-cell">Newest</TableHead>
                <TableHead>Deleted</TableHead>
                <TableHead className="w-px">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((o) => (
                <TableRow key={o.app_id}>
                  <TableCell>
                    <div className="font-medium [overflow-wrap:anywhere]">{o.app_name || "Unknown app"}</div>
                    <div className="font-mono text-xs text-muted-foreground [overflow-wrap:anywhere]">{o.app_id}</div>
                    {o.deleted_at ? <div className="text-xs text-muted-foreground">Deleted {day(o.deleted_at)}</div> : null}
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">
                    {o.copies} · {formatBytes(o.bytes)}
                  </TableCell>
                  <TableCell className="hidden sm:table-cell">{o.newest ? relativeAge(o.newest, now) : "—"}</TableCell>
                  <TableCell className={cn("text-sm", !o.deleted_at && "text-muted-foreground")}>{purgeLabel(o, now)}</TableCell>
                  <TableCell className="py-0.5 text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive hover:text-destructive"
                      aria-label={`Delete the backups of ${o.app_name || o.app_id}`}
                      onClick={() => setConfirm(o)}
                      disabled={busy}
                    >
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

**Modify `web/src/components/storage-dialog.tsx`** — replace (1 of 3):

```tsx
  const [saveError, setSaveError] = useState("");
  // The furthest field (DRAFT_FIELDS index) the user changed or left: a
```

with:

```tsx
  const [saveError, setSaveError] = useState("");
  // Path-style follows the endpoint on an add until the user sets it.
  const [pathStyleSet, setPathStyleSet] = useState(false);
  // The furthest field (DRAFT_FIELDS index) the user changed or left: a
```

**Modify `web/src/components/storage-dialog.tsx`** — replace (2 of 3):

```tsx
                    // An IP address or localhost has no bucket.host names: on
                    // an add, path-style follows the endpoint while it still
                    // has the endpoint's default (the user didn't set it).
                    set(
                      !editing && draft.pathStyle === pathStyleDefault(draft.endpoint)
                        ? { endpoint: v, pathStyle: pathStyleDefault(v) }
                        : { endpoint: v },
                      "endpoint",
```

with:

```tsx
                    // An IP address or localhost has no bucket.host names: on
                    // an add, path-style follows the endpoint until the user
                    // ticks or unticks it — then it never moves by itself.
                    set(
                      !editing && !pathStyleSet ? { endpoint: v, pathStyle: pathStyleDefault(v) } : { endpoint: v },
                      "endpoint",
```

**Modify `web/src/components/storage-dialog.tsx`** — replace (3 of 3):

```tsx
                    checked={draft.pathStyle}
                    onCheckedChange={(v) => set({ pathStyle: v === true })}
                    aria-label="Path-style addressing"
```

with:

```tsx
                    checked={draft.pathStyle}
                    onCheckedChange={(v) => {
                      setPathStyleSet(true);
                      set({ pathStyle: v === true });
                    }}
                    aria-label="Path-style addressing"
```

**Modify `web/src/pages/settings-storages.tsx`** — replace (1 of 5):

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
```

with:

```tsx
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Archive, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { toast } from "sonner";
```

**Modify `web/src/pages/settings-storages.tsx`** — replace (2 of 5):

```tsx
import { AppLinks } from "@/components/app-links";
import { StorageDialog } from "@/components/storage-dialog";
import { type Docker } from "@/context/use-docker";
```

with:

```tsx
import { AppLinks } from "@/components/app-links";
import { OrphansDialog } from "@/components/orphans-dialog";
import { StorageDialog } from "@/components/storage-dialog";
import { useBackups } from "@/context/use-backups";
import { type Docker } from "@/context/use-docker";
```

**Modify `web/src/pages/settings-storages.tsx`** — replace (3 of 5):

```tsx
  const [toDelete, setToDelete] = useState<Storage | null>(null);
  // The dialogs have no trigger of their own (the storage dialog remounts
```

with:

```tsx
  const [toDelete, setToDelete] = useState<Storage | null>(null);
  // The storage whose backups of apps not on this server are shown.
  const [orphansOf, setOrphansOf] = useState<string | null>(null);
  const backups = useBackups();
  // The dialogs have no trigger of their own (the storage dialog remounts
```

**Modify `web/src/pages/settings-storages.tsx`** — replace (4 of 5):

```tsx
                        className="size-8 text-muted-foreground"
                        aria-label={`Edit storage ${s.name}`}
```

with:

```tsx
                        className="size-8 text-muted-foreground"
                        aria-label={`Backups of apps not on this server in ${s.name}`}
                        title="Backups of apps not on this server"
                        onClick={(e) => {
                          opener.current = e.currentTarget;
                          setOrphansOf(s.name);
                        }}
                      >
                        <Archive className="size-4" />
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-8 text-muted-foreground"
                        aria-label={`Edit storage ${s.name}`}
```

**Modify `web/src/pages/settings-storages.tsx`** — replace (5 of 5):

```tsx
      />
      <AlertDialog open={toDelete !== null} onOpenChange={(o) => !o && setToDelete(null)}>
```

with:

```tsx
      />
      {orphansOf ? (
        <OrphansDialog
          key={orphansOf}
          backups={backups}
          storage={orphansOf}
          open
          returnFocus={opener}
          onOpenChange={(o) => !o && setOrphansOf(null)}
        />
      ) : null}
      <AlertDialog open={toDelete !== null} onOpenChange={(o) => !o && setToDelete(null)}>
```

- [ ] **Step 4: Run the tests and checks**

```bash
CI=true pnpm --dir web exec vitest run src/components/orphans-dialog.test.tsx src/pages/settings-storages.test.tsx src/components/ingress-editor.test.tsx
pnpm --dir web exec tsc --noEmit -p tsconfig.app.json
pnpm --dir web run lint
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/orphans-dialog.test.tsx \
  web/src/components/orphans-dialog.tsx \
  web/src/components/ingress-editor.test.tsx \
  web/src/components/storage-dialog.tsx \
  web/src/pages/settings-storages.test.tsx \
  web/src/pages/settings-storages.tsx
git commit -F - <<'MSG'
feat(web): orphaned backups in Settings → Storages; path-style stays as the user set it

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```

### Task 12: Web: the app's Backups tab

A Backups tab on the app page (between Configuration and History; always present — see the rulings), loading `app.get`'s effective backups, targets and ceiling. Each row: an enable switch (on → a storage picker of the server's storages from the API index, or a link to Settings → Storages when there are none; off → saved at once), name and type, location, storage, schedule ("Manual" for `every: manual`, "Its own tool" for custom), a health badge with "last backup 4 min ago" and the last error, and — for sqlite/files — Back up now, Copies (points list: time, kind, size, "expires in N days"; Restore with a confirmation saying the app stops and its current data is first saved as a pre-restore copy; Delete with a confirmation), and Edit (schedule, retention with "Capped at N days on this server", location; each part badged "from template" / "changed" with Reset to template; Remove for a backup added here). "Add backup" adds a sqlite or files entry from a picker of the compose file's volumes and each service's mounts, then the path(s); it is added off. Every change sends the whole list through `configsWith`, so untouched backups keep exactly their stored overrides. Native `<select>`s keep the dialogs testable in jsdom (no change to `components/ui`).

**Files:**
- Create: `web/src/components/add-backup-dialog.test.tsx`
- Create: `web/src/components/add-backup-dialog.tsx`
- Create: `web/src/components/app-backups.test.tsx`
- Create: `web/src/components/app-backups.tsx`
- Create: `web/src/components/backup-edit-dialog.test.tsx`
- Create: `web/src/components/backup-edit-dialog.tsx`
- Create: `web/src/components/backup-storage-dialog.test.tsx`
- Create: `web/src/components/backup-storage-dialog.tsx`
- Create: `web/src/components/restore-points-dialog.test.tsx`
- Create: `web/src/components/restore-points-dialog.tsx`
- Modify: `web/src/lib/backups.test.ts`
- Modify: `web/src/lib/backups.ts`
- Modify: `web/src/pages/app-details.tsx`

**Interfaces:**
- Consumes: Task 10's `lib/backups.ts`, `useBackups`, `backupsByApp`, `getApp`.
- Produces:
  - `lib/backups.ts` gains `selectClass`, `pathsFrom`, `keepError`, `locationError`.
  - Components `AppBackups({appId})`, `BackupStorageDialog`, `BackupEditDialog`, `AddBackupDialog`, `RestorePointsDialog`; the `backups` tab of `pages/app-details.tsx`.

- [ ] **Step 1: Write the failing tests**

**Create `web/src/components/add-backup-dialog.test.tsx`:**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AddBackupDialog } from "@/components/add-backup-dialog";
import type { BackupTargets } from "@/lib/backups";

const targets: BackupTargets = {
  volumes: ["data"],
  services: [
    { name: "web", mounts: [{ type: "bind", source: "./uploads", target: "/app/uploads" }] },
    { name: "worker", mounts: [] },
  ],
};

function renderDialog(t: BackupTargets | null = targets) {
  const onAdd = vi.fn();
  render(<AddBackupDialog targets={t} taken={["database"]} open busy={false} onOpenChange={() => {}} onAdd={onAdd} />);
  return onAdd;
}

describe("AddBackupDialog", () => {
  it("adds a files backup of a service's mount", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog();
    await user.type(screen.getByLabelText("Name"), "Uploads");
    await user.click(screen.getByRole("radio", { name: "Folders and files" }));
    const place = screen.getByLabelText("Where the data is");
    expect(screen.getByRole("option", { name: "Volume data" })).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Service worker" })).not.toBeInTheDocument();
    await user.selectOptions(place, "mount:web:/app/uploads");
    expect(screen.getByLabelText("Paths, one per line")).toHaveValue("/app/uploads");
    expect(screen.getByText("Paths are in the web container.")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Schedule"), "24h");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith({ name: "uploads", type: "files", every: "24h", service: "web", paths: ["/app/uploads"] });
  });

  it("adds a sqlite backup in a volume", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog();
    await user.type(screen.getByLabelText("Name"), "audit");
    await user.selectOptions(screen.getByLabelText("Where the data is"), "volume:data");
    await user.type(screen.getByLabelText("Database file"), "audit.db");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(onAdd).toHaveBeenCalledWith({ name: "audit", type: "sqlite", every: "1h", volume: "data", path: "audit.db" });
  });

  it("says what is missing once the user tries", async () => {
    const user = userEvent.setup({ delay: null });
    const onAdd = renderDialog(null);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Name"), "database");
    await user.click(screen.getByRole("button", { name: "Add backup" }));
    expect(screen.getByRole("alert")).toHaveTextContent("The app already has a backup named database.");
    expect(screen.getByRole("button", { name: "Add backup" })).toBeDisabled();
    await user.clear(screen.getByLabelText("Name"));
    await user.type(screen.getByLabelText("Name"), "db2");
    expect(screen.getByRole("alert")).toHaveTextContent("Pick where the data is.");
    expect(onAdd).not.toHaveBeenCalled();
  });
});
```

**Create `web/src/components/app-backups.test.tsx`:**

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AppBackups } from "@/components/app-backups";
import { AppsContext, type AppDetailPayload, type AppsContextValue } from "@/context/apps-context-base";
import type { BackupHealth, EffectiveBackup } from "@/lib/backups";

const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
const backups = vi.hoisted(() => ({
  updateBackups: vi.fn(),
  runBackup: vi.fn(),
  serverStorages: vi.fn(),
  listRestorePoints: vi.fn(),
  restoreBackup: vi.fn(),
  deleteRestorePoint: vi.fn(),
}));
vi.mock("@/context/use-backups", () => ({ useBackups: () => backups }));

const all = { every: true, keep: true, volume: true, service: true, path: true, paths: true, exclude: true };
const LIST: EffectiveBackup[] = [
  {
    name: "database",
    type: "sqlite",
    enabled: true,
    storage: "disk",
    every: "1h",
    keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
    volume: "data",
    path: "app.db",
    in_recipe: true,
    from_recipe: all,
  },
  {
    name: "media",
    type: "files",
    enabled: false,
    storage: "",
    every: "manual",
    keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
    service: "web",
    paths: ["/app/media"],
    in_recipe: true,
    from_recipe: all,
  },
  { name: "replica", type: "custom", enabled: true, storage: "s3", service: "litestream", in_recipe: true, from_recipe: all },
];
const HEALTH: BackupHealth[] = [
  { name: "database", state: "failing", last_success: Math.floor(Date.now() / 1000) - 240, last_error: "disk full" },
  { name: "replica", state: "ok" },
];

function renderBackups(detail: Partial<AppDetailPayload> = {}, getApp?: AppsContextValue["getApp"]) {
  const value = {
    getApp: getApp ?? vi.fn().mockResolvedValue({ backups: LIST, backup_targets: { volumes: ["data"], services: [] }, retention_days: 180, ...detail }),
    backupsByApp: { a1: HEALTH },
  } as unknown as AppsContextValue;
  return render(
    <MemoryRouter>
      <AppsContext.Provider value={value}>
        <AppBackups appId="a1" />
      </AppsContext.Provider>
    </MemoryRouter>,
  );
}

const row = (name: string) => screen.getByText(name, { selector: "span.font-mono" }).closest("tr")!;

describe("AppBackups", () => {
  beforeEach(() => {
    for (const f of Object.values(backups)) f.mockReset();
    for (const f of Object.values(toast)) f.mockReset();
    backups.updateBackups.mockImplementation(async (_app: string, list: { name: string; enabled?: boolean; storage?: string }[]) => ({
      backups: LIST.map((b) => {
        const e = list.find((c) => c.name === b.name);
        return { ...b, enabled: e?.enabled ?? false, storage: e?.storage ?? "" };
      }),
      redeployed: false,
      warnings: [],
    }));
  });

  it("lists each backup with its storage, schedule, location and health", async () => {
    renderBackups();
    const db = within(await screen.findByText("database", { selector: "span.font-mono" }).then((e) => e.closest("tr")!));
    expect(db.getByText("Failing")).toBeInTheDocument();
    expect(db.getByText("last backup 4 min ago")).toBeInTheDocument();
    expect(db.getByText("disk full")).toBeInTheDocument();
    expect(db.getByText("Every hour")).toBeInTheDocument();
    expect(db.getByText("volume data: app.db")).toBeInTheDocument();
    expect(db.getByRole("switch", { name: "Back up database" })).toBeChecked();
    const media = within(row("media"));
    expect(media.getByText("Manual")).toBeInTheDocument();
    expect(media.getByText("Off")).toBeInTheDocument();
    expect(media.getByRole("button", { name: "Back up media now" })).toBeDisabled();
    const replica = within(row("replica"));
    expect(replica.getByText("Its own tool")).toBeInTheDocument();
    expect(replica.getByText("OK")).toBeInTheDocument();
    expect(replica.queryByRole("button", { name: /Edit/ })).not.toBeInTheDocument();
  });

  it("turns a backup on with a storage, and off", async () => {
    const user = userEvent.setup({ delay: null });
    backups.serverStorages.mockResolvedValue(["disk", "offsite"]);
    renderBackups();
    await user.click(await screen.findByRole("switch", { name: "Back up media" }));
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(await within(dialog).findByLabelText("Storage"), "offsite");
    await user.click(within(dialog).getByRole("button", { name: "Turn on" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("media turned on", { description: undefined }));
    expect(backups.updateBackups).toHaveBeenLastCalledWith("a1", [
      { name: "database", enabled: true, storage: "disk" },
      { name: "media", enabled: true, storage: "offsite" },
      { name: "replica", enabled: true, storage: "s3" },
    ]);
    expect(within(row("media")).getByRole("switch")).toBeChecked();

    await user.click(within(row("database")).getByRole("switch"));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database turned off", { description: undefined }));
    expect(backups.updateBackups).toHaveBeenLastCalledWith("a1", [
      { name: "database", storage: "disk" },
      { name: "media", enabled: true, storage: "offsite" },
      { name: "replica", enabled: true, storage: "s3" },
    ]);
  });

  it("reports a refused save and its warnings", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    backups.updateBackups.mockRejectedValueOnce(new Error("backup database: /data is not on a mount"));
    await user.click(await screen.findByRole("switch", { name: "Back up database" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't save the backups", { description: "backup database: /data is not on a mount" }),
    );
    backups.updateBackups.mockResolvedValueOnce({ backups: LIST, redeployed: true, warnings: ["redeploy failed: port in use"] });
    await user.click(screen.getByRole("switch", { name: "Back up database" }));
    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith("database turned off", { description: "The app was redeployed.\nredeploy failed: port in use" }),
    );
  });

  it("backs up now and says when nothing changed or a run is refused", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    backups.runBackup.mockResolvedValueOnce({ unchanged: true, point: null, warnings: [] });
    await user.click(await screen.findByRole("button", { name: "Back up database now" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database: nothing changed since the last copy", { description: undefined }));
    backups.runBackup.mockResolvedValueOnce({ unchanged: false, point: { id: "p" }, warnings: ["a note"] });
    await user.click(screen.getByRole("button", { name: "Back up database now" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database backed up", { description: "a note" }));
    backups.runBackup.mockRejectedValueOnce(new Error("a backup of database is already queued or running"));
    await user.click(screen.getByRole("button", { name: "Back up database now" }));
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't back up database", { description: "a backup of database is already queued or running" }),
    );
  });

  it("edits a backup and opens its copies", async () => {
    const user = userEvent.setup({ delay: null });
    backups.listRestorePoints.mockResolvedValue([]);
    renderBackups();
    await user.click(await screen.findByRole("button", { name: "Edit database" }));
    await user.selectOptions(screen.getByLabelText("Schedule"), "6h");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("database saved", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1][0]).toEqual({ name: "database", enabled: true, storage: "disk", every: "6h" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Copies of database" }));
    expect(await screen.findByText("No copies yet. Back up now to make one.")).toBeInTheDocument();
    expect(backups.listRestorePoints).toHaveBeenCalledWith("a1", "database");
  });

  it("adds a backup", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups();
    await user.click(await screen.findByRole("button", { name: /Add backup/ }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Name"), "audit");
    await user.selectOptions(within(dialog).getByLabelText("Where the data is"), "volume:data");
    await user.type(within(dialog).getByLabelText("Database file"), "audit.db");
    await user.click(within(dialog).getByRole("button", { name: "Add backup" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("audit added", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1].at(-1)).toEqual({ name: "audit", type: "sqlite", every: "1h", volume: "data", path: "audit.db" });
  });

  it("removes a backup added here", async () => {
    const user = userEvent.setup({ delay: null });
    const added: EffectiveBackup = { ...LIST[1], name: "uploads", volume: "data", service: undefined, paths: ["up"], in_recipe: false, from_recipe: {} };
    renderBackups({ backups: [LIST[0], added] });
    await user.click(await screen.findByRole("button", { name: "Edit uploads" }));
    await user.click(screen.getByRole("button", { name: "Remove backup" }));
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith("uploads removed", { description: undefined }));
    expect(backups.updateBackups.mock.calls[0][1]).toEqual([{ name: "database", enabled: true, storage: "disk" }]);
  });

  it("shows an empty state, a recipe error, and a load failure with a retry", async () => {
    const user = userEvent.setup({ delay: null });
    renderBackups({ backups: [], backups_error: "x-winterflow.backups[0].volume: \"gone\" is not a volume" });
    expect(await screen.findByText(/This app has no backups/)).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent('The backups can\'t be read: x-winterflow.backups[0].volume: "gone" is not a volume');

    const getApp = vi.fn().mockRejectedValueOnce(new Error("agent offline")).mockResolvedValue({ backups: LIST });
    renderBackups({}, getApp);
    await user.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(getApp).toHaveBeenCalledTimes(2));
    expect(await screen.findAllByRole("switch", { name: "Back up database" })).not.toHaveLength(0);
  });
});
```

**Create `web/src/components/backup-edit-dialog.test.tsx`:**

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { BackupEditDialog } from "@/components/backup-edit-dialog";
import type { BackupTargets, EffectiveBackup } from "@/lib/backups";

const all = { every: true, keep: true, volume: true, service: true, path: true, paths: true, exclude: true };
const targets: BackupTargets = {
  volumes: ["data", "media"],
  services: [{ name: "web", mounts: [{ type: "volume", source: "data", target: "/data" }] }],
};
const database: EffectiveBackup = {
  name: "database",
  type: "sqlite",
  enabled: true,
  storage: "disk",
  every: "15m",
  keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
  volume: "data",
  path: "app.db",
  in_recipe: true,
  from_recipe: { ...all, every: false },
};

function renderDialog(backup: EffectiveBackup, opts: { days?: number; onRemove?: () => void } = {}) {
  const onSave = vi.fn();
  render(
    <BackupEditDialog
      backup={backup}
      retentionDays={opts.days ?? 0}
      targets={targets}
      open
      busy={false}
      onOpenChange={() => {}}
      onSave={onSave}
      onRemove={opts.onRemove}
    />,
  );
  return onSave;
}

describe("BackupEditDialog", () => {
  it("badges each part from template or changed, and resets one to the template", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog(database);
    expect(screen.getAllByText("from template")).toHaveLength(2); // retention, location
    expect(screen.getByText("changed")).toBeInTheDocument(); // schedule
    expect(screen.getByLabelText("Schedule")).toHaveValue("15m");
    await user.click(screen.getByRole("button", { name: /Reset to template/ }));
    expect(screen.getByText("template's value on save")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk" });
  });

  it("saves what the user changed as an override", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog({ ...database, from_recipe: all });
    await user.selectOptions(screen.getByLabelText("Schedule"), "manual");
    const daily = screen.getByLabelText("Daily copies");
    await user.clear(daily);
    await user.type(daily, "30");
    expect(screen.getAllByText("changed")).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "database",
      enabled: true,
      storage: "disk",
      every: "manual",
      keep: { recent: "24h0m0s", daily: 30, monthly: 6 },
    });
  });

  it("moves the backup to a service with absolute paths", async () => {
    const user = userEvent.setup({ delay: null });
    const onSave = renderDialog({ ...database, from_recipe: all });
    await user.selectOptions(screen.getByLabelText("Location kind"), "service");
    expect(screen.getByLabelText("Service")).toHaveValue("web");
    const paths = screen.getByLabelText(/Database file/);
    await user.clear(paths);
    await user.type(paths, "data/app.db");
    expect(screen.getByRole("alert")).toHaveTextContent("data/app.db: use an absolute path in the container");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await user.clear(paths);
    await user.type(paths, "/data/app.db");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({ name: "database", enabled: true, storage: "disk", service: "web", path: "/data/app.db" });
  });

  it("notes the server's ceiling and refuses a bad retention", async () => {
    const user = userEvent.setup({ delay: null });
    renderDialog(database, { days: 90 });
    expect(screen.getByText("Capped at 90 days on this server.")).toBeInTheDocument();
    const monthly = screen.getByLabelText("Monthly copies");
    await user.clear(monthly);
    await user.type(monthly, "0");
    expect(screen.queryByText("Capped at 90 days on this server.")).not.toBeInTheDocument();
    await user.type(monthly, "x");
    expect(screen.getByRole("alert")).toHaveTextContent("Use whole numbers from 0 to 3650");
  });

  it("keeps a UI-added backup whole and can remove it", async () => {
    const user = userEvent.setup({ delay: null });
    const onRemove = vi.fn();
    const added: EffectiveBackup = {
      name: "uploads",
      type: "files",
      enabled: false,
      storage: "",
      every: "6h",
      keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
      volume: "media",
      exclude: ["*.tmp"],
      in_recipe: false,
      from_recipe: {},
    };
    const onSave = renderDialog(added, { onRemove });
    expect(screen.queryByText("from template")).not.toBeInTheDocument();
    expect(screen.getByLabelText(/Paths, one per line/)).toHaveAttribute("placeholder", "empty: the whole volume");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(onSave).toHaveBeenCalledWith({
      name: "uploads",
      type: "files",
      exclude: ["*.tmp"],
      every: "6h",
      keep: { recent: "24h0m0s", daily: 14, monthly: 6 },
      volume: "media",
      paths: [],
    });
    await user.click(screen.getByRole("button", { name: "Remove backup" }));
    expect(onRemove).toHaveBeenCalled();
  });

  it("shows a schedule outside the picker as it is", () => {
    renderDialog({ ...database, every: "90m" });
    expect(screen.getByLabelText("Schedule")).toHaveValue("90m");
    expect(screen.getByRole("option", { name: "Every 90m" })).toBeInTheDocument();
  });
});
```

**Create `web/src/components/backup-storage-dialog.test.tsx`:**

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import { BackupStorageDialog } from "@/components/backup-storage-dialog";

function renderDialog(load: () => Promise<string[]>, current = "") {
  const onPick = vi.fn();
  render(
    <MemoryRouter>
      <BackupStorageDialog backup="database" current={current} open load={load} onOpenChange={() => {}} onPick={onPick} />
    </MemoryRouter>,
  );
  return onPick;
}

describe("BackupStorageDialog", () => {
  it("picks one of the server's storages", async () => {
    const user = userEvent.setup({ delay: null });
    const onPick = renderDialog(async () => ["disk", "offsite"], "offsite");
    const select = await screen.findByLabelText("Storage");
    expect(select).toHaveValue("offsite");
    await user.selectOptions(select, "disk");
    await user.click(screen.getByRole("button", { name: "Turn on" }));
    expect(onPick).toHaveBeenCalledWith("disk");
  });

  it("starts on the first storage when the last one is gone", async () => {
    renderDialog(async () => ["disk"], "removed");
    expect(await screen.findByLabelText("Storage")).toHaveValue("disk");
  });

  it("points to Settings → Storages when there are none", async () => {
    renderDialog(async () => []);
    const link = await screen.findByRole("link", { name: "Add a storage in Settings → Storages" });
    expect(link).toHaveAttribute("href", "/settings?tab=storages");
    expect(screen.getByRole("button", { name: "Turn on" })).toBeDisabled();
  });

  it("says when the storages can't be loaded", async () => {
    renderDialog(() => Promise.reject(new Error("server offline")));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("server offline"));
  });
});
```

**Create `web/src/components/restore-points-dialog.test.tsx`:**

```tsx
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { RestorePointsDialog } from "@/components/restore-points-dialog";
import type { Backups } from "@/context/use-backups";
import type { EffectiveBackup, RestorePoint } from "@/lib/backups";

const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock("sonner", () => ({
  toast: { success: (...a: unknown[]) => toastSuccess(...a), error: (...a: unknown[]) => toastError(...a) },
}));

const now = Math.floor(Date.now() / 1000);
const POINTS: RestorePoint[] = [
  { id: "p2", time: now - 600, kind: "manual", size: 2048, expires: now + 3 * 86_400 },
  { id: "p1", time: now - 86_400, kind: "pre-restore", size: 900 },
];
const database: EffectiveBackup = {
  name: "database",
  type: "sqlite",
  enabled: true,
  storage: "disk",
  every: "1h",
  volume: "data",
  path: "app.db",
  in_recipe: true,
  from_recipe: {},
};
const when = (p: RestorePoint) => new Date(p.time * 1000).toLocaleString();

function fake(over: Partial<Backups> = {}): Backups {
  return {
    listRestorePoints: vi.fn().mockResolvedValue(POINTS),
    restoreBackup: vi.fn().mockResolvedValue({ restored_point: "p2", pre_restore_point: "p3", warnings: ["/x: skipped"] }),
    deleteRestorePoint: vi.fn().mockResolvedValue(undefined),
    ...over,
  } as unknown as Backups;
}

describe("RestorePointsDialog", () => {
  beforeEach(() => {
    toastSuccess.mockReset();
    toastError.mockReset();
  });

  it("lists the copies with their kind, size and expiry", async () => {
    render(<RestorePointsDialog appId="a1" backup={database} backups={fake()} open onOpenChange={() => {}} />);
    expect(await screen.findByText(when(POINTS[0]))).toBeInTheDocument();
    expect(screen.getByText("Manual")).toBeInTheDocument();
    expect(screen.getByText("Before a restore")).toBeInTheDocument();
    expect(screen.getByText("2.0 kB")).toBeInTheDocument();
    expect(screen.getByText(/expires in 3 days/)).toBeInTheDocument();
  });

  it("restores after a confirmation naming the pre-restore copy", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake();
    const onRestored = vi.fn();
    render(<RestorePointsDialog appId="a1" backup={database} backups={backups} open onOpenChange={() => {}} onRestored={onRestored} />);
    await user.click(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[0])}` }));
    const confirm = screen.getByRole("alertdialog");
    expect(confirm).toHaveTextContent("The app stops while its database is replaced, then starts again.");
    expect(confirm).toHaveTextContent("first saved as a pre-restore copy");
    await user.click(within(confirm).getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(onRestored).toHaveBeenCalled());
    expect(backups.restoreBackup).toHaveBeenCalledWith("a1", "database", "p2");
    expect(toastSuccess).toHaveBeenCalledWith(`database restored to ${when(POINTS[0])}`, {
      description: "The data it replaced was saved as p3.\n/x: skipped",
    });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("reports a failed restore and keeps the confirmation", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake({ restoreBackup: vi.fn().mockRejectedValue(new Error("the restore failed, the app's data is unchanged")) });
    render(<RestorePointsDialog appId="a1" backup={{ ...database, type: "files" }} backups={backups} open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[1])}` }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("while its files are replaced");
    await user.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(toastError).toHaveBeenCalledWith("Restore failed", { description: "the restore failed, the app's data is unchanged" }),
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("deletes a copy after a confirmation", async () => {
    const user = userEvent.setup({ delay: null });
    const backups = fake();
    render(<RestorePointsDialog appId="a1" backup={database} backups={backups} open onOpenChange={() => {}} />);
    await user.click(await screen.findByRole("button", { name: `Delete the copy of ${when(POINTS[1])}` }));
    expect(screen.getByRole("alertdialog")).toHaveTextContent("(900 B)");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: `Delete the copy of ${when(POINTS[1])}` }));
    await user.click(screen.getByRole("button", { name: "Delete copy" }));
    await waitFor(() => expect(screen.queryByText("Before a restore")).not.toBeInTheDocument());
    expect(backups.deleteRestorePoint).toHaveBeenCalledWith("a1", "database", "p1");
    expect(toastSuccess).toHaveBeenCalledWith("Copy deleted");
  });

  it("can't restore a backup that is off, and says when there are no copies or the list fails", async () => {
    render(<RestorePointsDialog appId="a1" backup={{ ...database, enabled: false }} backups={fake()} open onOpenChange={() => {}} />);
    expect(await screen.findByRole("button", { name: `Restore the copy of ${when(POINTS[0])}` })).toBeDisabled();
    render(
      <RestorePointsDialog appId="a1" backup={database} backups={fake({ listRestorePoints: vi.fn().mockResolvedValue([]) })} open onOpenChange={() => {}} />,
    );
    expect(await screen.findByText("No copies yet. Back up now to make one.")).toBeInTheDocument();
    render(
      <RestorePointsDialog
        appId="a1"
        backup={database}
        backups={fake({ listRestorePoints: vi.fn().mockRejectedValue(new Error("list the restore points: the storage did not answer within 30s")) })}
        open
        onOpenChange={() => {}}
      />,
    );
    expect(await screen.findByText(/the storage did not answer within 30s/)).toBeInTheDocument();
  });
});
```

**Modify `web/src/lib/backups.test.ts`** — replace (1 of 2):

```ts
  keepDays,
  keepLabel,
  locationLabel,
  needsAttention,
  relativeAge,
```

with:

```ts
  keepDays,
  keepError,
  keepLabel,
  locationError,
  locationLabel,
  needsAttention,
  pathsFrom,
  relativeAge,
```

**Modify `web/src/lib/backups.test.ts`** — replace (2 of 2):

```ts
    expect(backupNameError("database", ["database"])).toBe("The app already has a backup named database.");
  });
});
```

with:

```ts
    expect(backupNameError("database", ["database"])).toBe("The app already has a backup named database.");
  });
});

describe("the backup forms' checks", () => {
  it("splits paths by line or comma", () => {
    expect(pathsFrom(" /a \n\n/b, /c ")).toEqual(["/a", "/b", "/c"]);
    expect(pathsFrom("")).toEqual([]);
  });
  it("checks a retention", () => {
    expect(keepError("24h", "14", "6")).toBe("");
    expect(keepError("", "0", "0")).toBe("");
    expect(keepError("a day", "14", "6")).toBe("Keep every copy for a duration like 24h or 72h.");
    expect(keepError("24h", "-1", "6")).toContain("whole numbers");
    expect(keepError("24h", "14", "4000")).toContain("whole numbers");
  });
  it("checks a location", () => {
    expect(locationError("sqlite", "volume", "data", ["app.db"])).toBe("");
    expect(locationError("files", "volume", "data", [])).toBe("");
    expect(locationError("files", "volume", "", [])).toBe("Pick a volume.");
    expect(locationError("files", "service", "", [])).toBe("Pick a service.");
    expect(locationError("sqlite", "volume", "data", [])).toBe("Enter the database file's path.");
    expect(locationError("files", "service", "web", [])).toBe("Enter the paths to back up.");
    expect(locationError("files", "service", "web", ["app"])).toBe("app: use an absolute path in the container, like /app/data.");
    expect(locationError("files", "volume", "data", ["/abs"])).toBe("/abs: use a path inside the volume, like app.db or media/.");
    expect(locationError("files", "volume", "data", ["a/../b"])).toContain("inside the volume");
  });
});
```

- [ ] **Step 2: Run them and watch them fail**

Run:

```bash
CI=true pnpm --dir web exec vitest run src/components src/lib
```

Expected: FAIL — `Failed to resolve import "@/components/app-backups"` (and the dialogs); `pathsFrom is not a function`.

- [ ] **Step 3: Implement**

**Create `web/src/components/add-backup-dialog.tsx`:**

```tsx
import { useState } from "react";
import { CircleX } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  backupNameError,
  everyError,
  locationError,
  MANUAL,
  pathsFrom,
  SCHEDULES,
  selectClass,
  type BackupConfig,
  type BackupTargets,
} from "@/lib/backups";

// A place is "volume:<key>" or "mount:<service>:<target>".
const volumePlace = (v: string) => `volume:${v}`;
const mountPlace = (service: string, target: string) => `mount:${service}:${target}`;

// AddBackupDialog adds a sqlite or files backup the compose file doesn't
// declare. The picker lists the compose file's volumes and each service's
// mounts (volumes and bind mounts); the paths come after. The backup is
// added off: its switch picks the storage. taken are the app's backup
// names. The parent remounts it per opening.
export function AddBackupDialog({
  targets,
  taken,
  open,
  busy,
  onOpenChange,
  onAdd,
}: {
  targets: BackupTargets | null;
  taken: readonly string[];
  open: boolean;
  busy: boolean;
  onOpenChange: (open: boolean) => void;
  onAdd: (entry: BackupConfig) => void;
}) {
  const [name, setName] = useState("");
  const [type, setType] = useState<"sqlite" | "files">("sqlite");
  const [place, setPlace] = useState("");
  const [pathsText, setPathsText] = useState("");
  const [every, setEvery] = useState("1h");
  const [shown, setShown] = useState(false); // the user tried to add

  const volumes = targets?.volumes ?? [];
  const services = (targets?.services ?? []).filter((s) => s.mounts.length > 0);
  const [kind, key] = place.split(":");
  const where = kind === "volume" ? "volume" : "service";
  const target = kind === "volume" ? key : (key ?? "");
  const paths = pathsFrom(pathsText);
  const problem =
    backupNameError(name.trim(), taken) ||
    (place ? locationError(type, where, target, paths) : "Pick where the data is.") ||
    everyError(every);

  const pick = (next: string) => {
    setPlace(next);
    // A mount's target starts the path in the container.
    const [k, , ...t] = next.split(":");
    if (k === "mount" && !pathsText.trim()) setPathsText(type === "sqlite" ? `${t.join(":")}/` : t.join(":"));
  };

  const add = () => {
    setShown(true);
    if (problem || busy) return;
    const entry: BackupConfig = { name: name.trim(), type, every, [where]: target };
    if (type === "sqlite") entry.path = paths[0];
    else if (paths.length > 0) entry.paths = paths;
    onAdd(entry);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            add();
          }}
        >
          <DialogHeader>
            <DialogTitle>Add backup</DialogTitle>
            <DialogDescription>
              Back up a database file or folders the compose file doesn't declare. It is added off: turn it on to pick its
              storage.
            </DialogDescription>
          </DialogHeader>
          <fieldset disabled={busy} className="grid min-w-0 gap-4">
            <div className="grid gap-1.5">
              <Label htmlFor="add-backup-name">Name</Label>
              <Input
                id="add-backup-name"
                autoComplete="off"
                placeholder="e.g. uploads"
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
              />
            </div>
            <div className="grid gap-1.5">
              <Label id="add-backup-type">Type</Label>
              <RadioGroup
                aria-labelledby="add-backup-type"
                className="flex gap-6"
                value={type}
                onValueChange={(v) => setType(v as "sqlite" | "files")}
              >
                {(
                  [
                    ["sqlite", "SQLite database"],
                    ["files", "Folders and files"],
                  ] as const
                ).map(([v, label]) => (
                  <div key={v} className="flex items-center gap-2">
                    <RadioGroupItem value={v} id={`add-backup-${v}`} />
                    <Label htmlFor={`add-backup-${v}`} className="font-normal">
                      {label}
                    </Label>
                  </div>
                ))}
              </RadioGroup>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="add-backup-place">Where the data is</Label>
              <select id="add-backup-place" className={selectClass} value={place} onChange={(e) => pick(e.target.value)}>
                <option value="" disabled>
                  Pick a volume or a mount…
                </option>
                {volumes.length > 0 ? (
                  <optgroup label="Volumes">
                    {volumes.map((v) => (
                      <option key={v} value={volumePlace(v)}>
                        Volume {v}
                      </option>
                    ))}
                  </optgroup>
                ) : null}
                {services.map((s) => (
                  <optgroup key={s.name} label={`Service ${s.name}`}>
                    {s.mounts.map((m) => (
                      <option key={m.target} value={mountPlace(s.name, m.target)}>
                        {m.target} ({m.type === "volume" ? `volume ${m.source}` : m.source})
                      </option>
                    ))}
                  </optgroup>
                ))}
              </select>
              {kind === "mount" ? (
                <p className="text-xs text-muted-foreground">Paths are in the {key} container.</p>
              ) : null}
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="add-backup-paths">{type === "sqlite" ? "Database file" : "Paths, one per line"}</Label>
              <Textarea
                id="add-backup-paths"
                rows={type === "sqlite" ? 1 : 3}
                className="font-mono text-sm"
                placeholder={where === "volume" ? (type === "sqlite" ? "app.db" : "empty: the whole volume") : "/app/data"}
                value={pathsText}
                onChange={(e) => setPathsText(e.target.value)}
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="add-backup-every">Schedule</Label>
              <select id="add-backup-every" className={selectClass} value={every} onChange={(e) => setEvery(e.target.value)}>
                {SCHEDULES.map((s) => (
                  <option key={s.value} value={s.value}>
                    {s.value === MANUAL ? "Manual (Back up now only)" : s.label}
                  </option>
                ))}
              </select>
            </div>
          </fieldset>
          {shown && problem ? (
            <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
              <CircleX className="mt-0.5 size-4 shrink-0" /> {problem}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || (shown && !!problem)}>
              {busy ? "Adding…" : "Add backup"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
```

**Create `web/src/components/app-backups.tsx`:**

```tsx
import { useCallback, useEffect, useState } from "react";
import { Archive, HardDriveDownload, Pencil, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { AddBackupDialog } from "@/components/add-backup-dialog";
import { BackupEditDialog } from "@/components/backup-edit-dialog";
import { BackupStorageDialog } from "@/components/backup-storage-dialog";
import { RestorePointsDialog } from "@/components/restore-points-dialog";
import { useApps } from "@/context/use-apps";
import { useBackups } from "@/context/use-backups";
import {
  configOf,
  configsWith,
  locationLabel,
  relativeAge,
  scheduleLabel,
  STATE_LABELS,
  type BackupConfig,
  type BackupHealth,
  type BackupState,
  type BackupTargets,
  type EffectiveBackup,
} from "@/lib/backups";
import { cn } from "@/lib/utils";

const STATE_STYLE: Record<BackupState, string> = {
  ok: "border-emerald-500/40 text-emerald-700 dark:text-emerald-400",
  running: "border-sky-500/40 text-sky-700 dark:text-sky-400",
  never: "text-muted-foreground",
  off: "text-muted-foreground",
  late: "border-amber-500/50 text-amber-700 dark:text-amber-400",
  failing: "border-destructive/50 text-destructive",
};

type Detail = { list: EffectiveBackup[]; targets: BackupTargets | null; days: number; error: string };

// Open is which dialog shows, for which backup.
type Open =
  | { kind: "enable"; name: string }
  | { kind: "edit"; name: string }
  | { kind: "points"; name: string }
  | { kind: "add" }
  | null;

// AppBackups is the app page's Backups tab: each backup (declared by the
// compose file, or added here) with its storage, schedule, last copy and
// health; a switch to turn it on (picking a storage) or off; Back up now;
// its copies (restore, delete); an editor for schedule, retention and
// location; and Add backup.
export function AppBackups({ appId }: { appId: string }) {
  const { getApp, backupsByApp } = useApps();
  const backups = useBackups();
  const [detail, setDetail] = useState<Detail | null>(null);
  const [loadError, setLoadError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<string | null>(null); // the backup being saved ("" = the list)
  const [running, setRunning] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<Open>(null);
  const [openKey, setOpenKey] = useState(0);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError("");
    try {
      const d = await getApp(appId);
      setDetail({ list: d.backups ?? [], targets: d.backup_targets ?? null, days: d.retention_days ?? 0, error: d.backups_error ?? "" });
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : "Failed to load the backups");
    } finally {
      setLoading(false);
    }
  }, [appId, getApp]);

  useEffect(() => {
    void load();
  }, [load]);

  const show = (next: Open) => {
    setOpenKey((k) => k + 1);
    setOpen(next);
  };

  const list = detail?.list ?? [];
  const health = new Map((backupsByApp?.[appId] ?? []).map((h) => [h.name, h]));

  // save replaces the app's whole list, with name's entry replaced by entry
  // (null: removed; a new name: added).
  const save = async (name: string, entry: BackupConfig | null, done: string) => {
    setBusy(name);
    try {
      const res = await backups.updateBackups(appId, configsWith(list, name, entry));
      setDetail((d) => (d ? { ...d, list: res.backups } : d));
      setOpen(null);
      const description = [res.redeployed ? "The app was redeployed." : "", ...res.warnings].filter(Boolean).join("\n");
      if (res.warnings.length > 0) toast.warning(done, { description });
      else toast.success(done, { description: description || undefined });
    } catch (e) {
      toast.error("Couldn't save the backups", { description: e instanceof Error ? e.message : undefined });
    } finally {
      setBusy(null);
    }
  };

  const runNow = async (name: string) => {
    setRunning((s) => new Set(s).add(name));
    try {
      const res = await backups.runBackup(appId, name);
      const title = res.unchanged ? `${name}: nothing changed since the last copy` : `${name} backed up`;
      toast.success(title, { description: res.warnings.join("\n") || undefined });
    } catch (e) {
      toast.error(`Couldn't back up ${name}`, { description: e instanceof Error ? e.message : undefined });
    } finally {
      setRunning((s) => {
        const next = new Set(s);
        next.delete(name);
        return next;
      });
    }
  };

  const toggle = (b: EffectiveBackup, on: boolean) => {
    if (on) {
      show({ kind: "enable", name: b.name });
      return;
    }
    const off = configOf(b);
    delete off.enabled;
    void save(b.name, off, `${b.name} turned off`);
  };

  if (loading && !detail) {
    return (
      <div className="flex h-40 items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (loadError && !detail) {
    return (
      <div className="flex h-40 flex-col items-center justify-center gap-3">
        <p className="text-sm text-destructive">{loadError}</p>
        <Button size="sm" variant="outline" onClick={() => void load()}>
          Try again
        </Button>
      </div>
    );
  }

  const current = open && open.kind !== "add" ? list.find((b) => b.name === open.name) : undefined;
  return (
    <div className="max-w-5xl space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <p className="max-w-2xl text-sm text-muted-foreground">
          The compose file declares what can be backed up; turn a backup on to copy it to one of this server's storages on
          its schedule.
        </p>
        <div className="flex items-center gap-2">
          <Button
            size="icon"
            variant="ghost"
            className="size-8 text-muted-foreground"
            aria-label="Refresh backups"
            title="Refresh backups"
            onClick={() => void load()}
            disabled={loading}
          >
            <RefreshCw className={cn("size-4", loading && "animate-spin")} />
          </Button>
          <Button size="sm" onClick={() => show({ kind: "add" })} disabled={busy !== null}>
            <Plus className="size-4" /> Add backup
          </Button>
        </div>
      </div>
      {detail?.error ? (
        <p role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive [overflow-wrap:anywhere]">
          The backups can't be read: {detail.error}
        </p>
      ) : null}
      {list.length === 0 ? (
        <p className="rounded-lg border border-dashed px-4 py-10 text-center text-sm text-muted-foreground">
          This app has no backups. Its compose file can declare them under <code className="font-mono">x-winterflow.backups</code>,
          or add one here.
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-px">
                <span className="sr-only">On</span>
              </TableHead>
              <TableHead>Backup</TableHead>
              <TableHead className="hidden md:table-cell">Storage</TableHead>
              <TableHead className="hidden sm:table-cell">Schedule</TableHead>
              <TableHead>Last backup</TableHead>
              <TableHead className="w-px">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((b) => (
              <BackupRow
                key={b.name}
                backup={b}
                health={health.get(b.name)}
                busy={busy !== null}
                running={running.has(b.name)}
                onToggle={(on) => toggle(b, on)}
                onRun={() => void runNow(b.name)}
                onPoints={() => show({ kind: "points", name: b.name })}
                onEdit={() => show({ kind: "edit", name: b.name })}
              />
            ))}
          </TableBody>
        </Table>
      )}
      {open?.kind === "enable" && current ? (
        <BackupStorageDialog
          key={openKey}
          backup={current.name}
          current={current.storage}
          open
          load={backups.serverStorages}
          onOpenChange={(o) => !o && setOpen(null)}
          onPick={(storage) => void save(current.name, { ...configOf(current), enabled: true, storage }, `${current.name} turned on`)}
        />
      ) : null}
      {open?.kind === "edit" && current ? (
        <BackupEditDialog
          key={openKey}
          backup={current}
          retentionDays={detail?.days ?? 0}
          targets={detail?.targets ?? null}
          open
          busy={busy !== null}
          onOpenChange={(o) => !o && setOpen(null)}
          onSave={(entry) => void save(current.name, entry, `${current.name} saved`)}
          onRemove={current.in_recipe ? undefined : () => void save(current.name, null, `${current.name} removed`)}
        />
      ) : null}
      {open?.kind === "points" && current ? (
        <RestorePointsDialog
          key={openKey}
          appId={appId}
          backup={current}
          backups={backups}
          open
          onOpenChange={(o) => !o && setOpen(null)}
        />
      ) : null}
      {open?.kind === "add" ? (
        <AddBackupDialog
          key={openKey}
          targets={detail?.targets ?? null}
          taken={list.map((b) => b.name)}
          open
          busy={busy !== null}
          onOpenChange={(o) => !o && setOpen(null)}
          onAdd={(entry) => void save(entry.name, entry, `${entry.name} added`)}
        />
      ) : null}
    </div>
  );
}

// BackupRow is one backup: its switch, name and type, storage, schedule,
// last copy with health, and actions (built-in backups only: winterflow
// neither runs nor lists a custom backup's copies).
function BackupRow({
  backup: b,
  health,
  busy,
  running,
  onToggle,
  onRun,
  onPoints,
  onEdit,
}: {
  backup: EffectiveBackup;
  health: BackupHealth | undefined;
  busy: boolean;
  running: boolean;
  onToggle: (on: boolean) => void;
  onRun: () => void;
  onPoints: () => void;
  onEdit: () => void;
}) {
  const builtin = b.type !== "custom";
  const state: BackupState = running ? "running" : (health?.state ?? (b.enabled ? "never" : "off"));
  return (
    <TableRow>
      <TableCell>
        <Switch checked={b.enabled} disabled={busy} aria-label={`Back up ${b.name}`} onCheckedChange={onToggle} />
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono">{b.name}</span>
          <Badge variant="outline">{b.type}</Badge>
          {b.in_recipe ? null : <Badge variant="secondary">added here</Badge>}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground [overflow-wrap:anywhere]">{locationLabel(b)}</div>
      </TableCell>
      <TableCell className="hidden md:table-cell">{b.storage || <span className="text-muted-foreground">—</span>}</TableCell>
      <TableCell className="hidden sm:table-cell">{builtin ? scheduleLabel(b.every) : "Its own tool"}</TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="outline" className={STATE_STYLE[state]} title={health?.last_error}>
            {STATE_LABELS[state]}
          </Badge>
          {health?.last_success ? (
            <span className="text-xs text-muted-foreground">last backup {relativeAge(health.last_success)}</span>
          ) : null}
        </div>
        {state === "failing" && health?.last_error ? (
          <div className="mt-1 text-xs text-destructive [overflow-wrap:anywhere]">{health.last_error}</div>
        ) : null}
      </TableCell>
      <TableCell className="whitespace-nowrap py-0.5 text-right">
        {builtin ? (
          <>
            <Button
              size="icon"
              variant="ghost"
              className="size-8 text-muted-foreground"
              aria-label={`Back up ${b.name} now`}
              title="Back up now"
              disabled={!b.enabled || running}
              onClick={onRun}
            >
              {running ? <Spinner className="size-4" /> : <HardDriveDownload className="size-4" />}
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-8 text-muted-foreground"
              aria-label={`Copies of ${b.name}`}
              title="Copies (restore, delete)"
              disabled={!b.storage}
              onClick={onPoints}
            >
              <Archive className="size-4" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-8 text-muted-foreground"
              aria-label={`Edit ${b.name}`}
              title="Edit"
              disabled={busy}
              onClick={onEdit}
            >
              <Pencil className="size-4" />
            </Button>
          </>
        ) : null}
      </TableCell>
    </TableRow>
  );
}
```

**Create `web/src/components/backup-edit-dialog.tsx`:**

```tsx
import { useState, type ReactNode } from "react";
import { CircleX, RotateCcw } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  cappedNote,
  changed,
  configOf,
  everyError,
  keepError,
  locationError,
  MANUAL,
  pathsFrom,
  SCHEDULES,
  selectClass,
  type BackupConfig,
  type BackupTargets,
  type EffectiveBackup,
} from "@/lib/backups";

// Group is a part of the form that overrides the recipe as a whole.
type Group = "schedule" | "retention" | "location";

const GROUP_FIELDS = {
  schedule: ["every"],
  retention: ["keep"],
  location: ["volume", "service", "path", "paths"],
} as const;

// BackupEditDialog edits a sqlite or files backup's schedule, retention and
// location. For a backup of the compose file each part is badged "from
// template" or "changed", and "Reset to template" drops the change (it
// takes the template's value when saved). A backup added here can be
// removed. The parent remounts it per opening; onSave gets the backup's
// whole new list entry.
export function BackupEditDialog({
  backup,
  retentionDays,
  targets,
  open,
  busy,
  onOpenChange,
  onSave,
  onRemove,
}: {
  backup: EffectiveBackup;
  retentionDays: number;
  targets: BackupTargets | null;
  open: boolean;
  busy: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (entry: BackupConfig) => void;
  onRemove?: () => void;
}) {
  const b = backup;
  const [every, setEvery] = useState(b.every ?? "1h");
  const [recent, setRecent] = useState(b.keep?.recent ?? "24h");
  const [daily, setDaily] = useState(String(b.keep?.daily ?? 14));
  const [monthly, setMonthly] = useState(String(b.keep?.monthly ?? 6));
  const [where, setWhere] = useState<"volume" | "service">(b.volume ? "volume" : "service");
  const [target, setTarget] = useState(b.volume ?? b.service ?? "");
  const [pathsText, setPathsText] = useState(b.type === "sqlite" ? (b.path ?? "") : (b.paths ?? []).join("\n"));
  const [edited, setEdited] = useState<Set<Group>>(new Set());
  const [reset, setReset] = useState<Set<Group>>(new Set());

  const touch = (g: Group) => {
    setEdited((s) => new Set(s).add(g));
    setReset((s) => {
      const next = new Set(s);
      next.delete(g);
      return next;
    });
  };
  const resetGroup = (g: Group) => {
    setReset((s) => new Set(s).add(g));
    setEdited((s) => {
      const next = new Set(s);
      next.delete(g);
      return next;
    });
  };
  const isChanged = (g: Group) => !reset.has(g) && (edited.has(g) || GROUP_FIELDS[g].some((f) => changed(b, f)));

  const paths = pathsFrom(pathsText);
  const problem =
    everyError(every) ||
    keepError(recent, daily, monthly) ||
    (reset.has("location") ? "" : locationError(b.type, where, target, paths));
  const keep = { recent: recent.trim() || "24h", daily: Number(daily), monthly: Number(monthly) };
  const note = cappedNote(keep, retentionDays);

  const entry = (): BackupConfig => {
    // Fields this form doesn't edit (exclude) keep their override.
    const c = configOf(b);
    for (const g of Object.keys(GROUP_FIELDS) as Group[]) {
      for (const f of GROUP_FIELDS[g]) delete c[f];
      if (b.in_recipe && !isChanged(g)) continue;
      if (g === "schedule") c.every = every;
      if (g === "retention") c.keep = keep;
      if (g === "location") {
        c[where] = target;
        if (b.type === "sqlite") c.path = paths[0];
        else c.paths = paths;
      }
    }
    return c;
  };

  const services = targets?.services ?? [];
  const section = (g: Group, title: string, body: ReactNode) => (
    <fieldset className="grid gap-2 rounded-md border p-3">
      <div className="flex min-h-6 flex-wrap items-center gap-2">
        <legend className="text-sm font-medium">{title}</legend>
        {b.in_recipe ? (
          isChanged(g) ? (
            <>
              <Badge variant="secondary">changed</Badge>
              <Button type="button" variant="ghost" size="sm" className="ml-auto h-7" onClick={() => resetGroup(g)}>
                <RotateCcw className="size-3.5" /> Reset to template
              </Button>
            </>
          ) : (
            <Badge variant="outline">{reset.has(g) ? "template's value on save" : "from template"}</Badge>
          )
        ) : null}
      </div>
      {body}
    </fieldset>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!problem && !busy) onSave(entry());
          }}
        >
          <DialogHeader>
            <DialogTitle>Edit backup {b.name}</DialogTitle>
            <DialogDescription>
              {b.type === "sqlite" ? "A SQLite database file" : "Folders and files"}
              {b.in_recipe ? ", declared by the compose file." : ", added here."}
            </DialogDescription>
          </DialogHeader>
          <fieldset disabled={busy} className="grid min-w-0 gap-3">
            {section(
              "schedule",
              "Schedule",
              <select
                aria-label="Schedule"
                className={selectClass}
                value={every}
                onChange={(e) => {
                  setEvery(e.target.value);
                  touch("schedule");
                }}
              >
                {(SCHEDULES.some((s) => s.value === every) ? SCHEDULES : [...SCHEDULES, { value: every, label: `Every ${every}` }]).map(
                  (s) => (
                    <option key={s.value} value={s.value}>
                      {s.value === MANUAL ? "Manual (Back up now only)" : s.label}
                    </option>
                  ),
                )}
              </select>,
            )}
            {section(
              "retention",
              "Keep",
              <>
                <div className="grid grid-cols-3 gap-2">
                  {(
                    [
                      ["Every copy for", recent, setRecent, "keep-recent"],
                      ["Daily copies", daily, setDaily, "keep-daily"],
                      ["Monthly copies", monthly, setMonthly, "keep-monthly"],
                    ] as const
                  ).map(([label, value, set, id]) => (
                    <div key={id} className="grid gap-1">
                      <Label htmlFor={id} className="text-xs font-normal text-muted-foreground">
                        {label}
                      </Label>
                      <Input
                        id={id}
                        value={value}
                        onChange={(e) => {
                          set(e.target.value);
                          touch("retention");
                        }}
                      />
                    </div>
                  ))}
                </div>
                {note ? <p className="text-xs text-muted-foreground">{note}</p> : null}
              </>,
            )}
            {section(
              "location",
              "Location",
              <>
                <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-2">
                  <select
                    aria-label="Location kind"
                    className={selectClass}
                    value={where}
                    onChange={(e) => {
                      const next = e.target.value as "volume" | "service";
                      setWhere(next);
                      setTarget(next === "volume" ? (targets?.volumes[0] ?? "") : (services[0]?.name ?? ""));
                      touch("location");
                    }}
                  >
                    <option value="volume">Volume</option>
                    <option value="service">Service</option>
                  </select>
                  <select
                    aria-label={where === "volume" ? "Volume" : "Service"}
                    className={selectClass}
                    value={target}
                    onChange={(e) => {
                      setTarget(e.target.value);
                      touch("location");
                    }}
                  >
                    {[...new Set([target, ...(where === "volume" ? (targets?.volumes ?? []) : services.map((s) => s.name))])]
                      .filter(Boolean)
                      .map((n) => (
                        <option key={n} value={n}>
                          {n}
                        </option>
                      ))}
                  </select>
                </div>
                <div className="grid gap-1">
                  <Label htmlFor="backup-paths" className="text-xs font-normal text-muted-foreground">
                    {b.type === "sqlite" ? "Database file" : "Paths, one per line"}
                    {where === "volume" ? " (inside the volume)" : " (in the container)"}
                  </Label>
                  <Textarea
                    id="backup-paths"
                    rows={b.type === "sqlite" ? 1 : 3}
                    className="font-mono text-sm"
                    placeholder={where === "volume" ? (b.type === "sqlite" ? "app.db" : "empty: the whole volume") : "/app/data"}
                    value={pathsText}
                    onChange={(e) => {
                      setPathsText(e.target.value);
                      touch("location");
                    }}
                  />
                </div>
              </>,
            )}
          </fieldset>
          {problem ? (
            <p role="alert" className="flex items-start gap-1.5 text-sm text-destructive">
              <CircleX className="mt-0.5 size-4 shrink-0" /> {problem}
            </p>
          ) : null}
          <DialogFooter className="gap-2 sm:justify-between">
            {onRemove ? (
              <Button type="button" variant="ghost" className="text-destructive hover:text-destructive" disabled={busy} onClick={onRemove}>
                Remove backup
              </Button>
            ) : (
              <span />
            )}
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!!problem || busy}>
                {busy ? "Saving…" : "Save"}
              </Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
```

**Create `web/src/components/backup-storage-dialog.tsx`:**

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { selectClass } from "@/lib/backups";

// BackupStorageDialog turns a backup on: it picks one of the server's
// storages (load lists their names), or points to Settings → Storages when
// there are none. The parent remounts it per opening.
export function BackupStorageDialog({
  backup,
  current,
  open,
  load,
  onOpenChange,
  onPick,
}: {
  backup: string;
  current: string;
  open: boolean;
  load: () => Promise<string[]>;
  onOpenChange: (open: boolean) => void;
  onPick: (storage: string) => void;
}) {
  const [names, setNames] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const [picked, setPicked] = useState(current);

  useEffect(() => {
    if (!open) return;
    let live = true;
    load().then(
      (list) => {
        if (!live) return;
        setNames(list);
        setPicked((p) => (p && list.includes(p) ? p : (list[0] ?? "")));
      },
      (e: unknown) => live && setError(e instanceof Error ? e.message : "The storages could not be loaded."),
    );
    return () => {
      live = false;
    };
  }, [open, load]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Turn on {backup}</DialogTitle>
          <DialogDescription>Pick the storage its copies go to.</DialogDescription>
        </DialogHeader>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : names === null ? (
          <div className="flex h-16 items-center justify-center">
            <Spinner />
          </div>
        ) : names.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            This server has no storage yet.{" "}
            <Link to="/settings?tab=storages" className="font-medium text-foreground underline underline-offset-4">
              Add a storage in Settings → Storages
            </Link>
            , then turn the backup on.
          </p>
        ) : (
          <div className="grid gap-1.5">
            <Label htmlFor="backup-storage">Storage</Label>
            <select id="backup-storage" className={selectClass} value={picked} onChange={(e) => setPicked(e.target.value)}>
              {names.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!picked} onClick={() => onPick(picked)}>
            Turn on
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

**Create `web/src/components/restore-points-dialog.tsx`:**

```tsx
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { buttonVariants } from "@/components/ui/button-variants";
import { Spinner } from "@/components/ui/spinner";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { Backups } from "@/context/use-backups";
import { expiresNote, formatBytes, KIND_LABELS, relativeAge, type EffectiveBackup, type RestorePoint } from "@/lib/backups";

type Confirm = { action: "restore" | "delete"; point: RestorePoint };

const when = (p: RestorePoint) => new Date(p.time * 1000).toLocaleString();

// RestorePointsDialog lists a backup's copies (time, kind, size; "expires
// in N days" near the server's ceiling), each restorable — after a
// confirmation saying the app stops and its current data is first saved as
// a pre-restore point — and deletable. A backup that is off can't be
// restored (its storage and mounts are only resolved while it is on).
// onRestored runs after a restore (the app's status changed). The parent
// remounts it per opening.
export function RestorePointsDialog({
  appId,
  backup,
  backups,
  open,
  onOpenChange,
  onRestored,
}: {
  appId: string;
  backup: EffectiveBackup;
  backups: Backups;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRestored?: () => void;
}) {
  const [points, setPoints] = useState<RestorePoint[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<Confirm | null>(null);
  const [busy, setBusy] = useState<"restore" | "delete" | null>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      setPoints(await backups.listRestorePoints(appId, backup.name));
    } catch (e) {
      setError(e instanceof Error ? e.message : "The copies could not be listed.");
    }
  }, [appId, backup.name, backups]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const run = async ({ action, point }: Confirm) => {
    setBusy(action);
    try {
      if (action === "restore") {
        const res = await backups.restoreBackup(appId, backup.name, point.id);
        const saved = res.pre_restore_point ? `The data it replaced was saved as ${res.pre_restore_point}.` : "";
        toast.success(`${backup.name} restored to ${when(point)}`, {
          description: [saved, ...res.warnings].filter(Boolean).join("\n") || undefined,
        });
        onRestored?.();
        await load();
      } else {
        await backups.deleteRestorePoint(appId, backup.name, point.id);
        toast.success("Copy deleted");
        setPoints((prev) => prev && prev.filter((p) => p.id !== point.id));
      }
      setConfirm(null);
    } catch (e) {
      toast.error(action === "restore" ? "Restore failed" : "Couldn't delete the copy", {
        description: e instanceof Error ? e.message : undefined,
      });
    } finally {
      setBusy(null);
    }
  };

  const what = backup.type === "sqlite" ? "database" : "files";
  return (
    <Dialog open={open} onOpenChange={(o) => busy !== "restore" && onOpenChange(o)}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Copies of {backup.name}</DialogTitle>
          <DialogDescription>Newest first, in {backup.storage || "its storage"}.</DialogDescription>
        </DialogHeader>
        {confirm ? (
          <div role="alertdialog" aria-labelledby="point-confirm" className="grid gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-4">
            {confirm.action === "restore" ? (
              <p id="point-confirm" className="text-sm">
                Restore {backup.name} to the copy of <strong>{when(confirm.point)}</strong>? The app stops while its {what}{" "}
                {what === "database" ? "is" : "are"} replaced, then starts again. The current {what} {what === "database" ? "is" : "are"}{" "}
                first saved as a <strong>pre-restore</strong> copy, so you can go back.
              </p>
            ) : (
              <p id="point-confirm" className="text-sm">
                Delete the copy of <strong>{when(confirm.point)}</strong> ({formatBytes(confirm.point.size)})? It can't be restored
                afterwards.
              </p>
            )}
            {busy === "restore" ? (
              <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
                <Spinner className="size-4" /> Restoring: the app is stopped until it is done.
              </p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setConfirm(null)} disabled={busy !== null}>
                Cancel
              </Button>
              <Button size="sm" className={buttonVariants({ variant: "destructive", size: "sm" })} onClick={() => void run(confirm)} disabled={busy !== null}>
                {confirm.action === "restore" ? (busy ? "Restoring…" : "Restore") : busy ? "Deleting…" : "Delete copy"}
              </Button>
            </div>
          </div>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : points === null ? (
          <div className="flex h-24 items-center justify-center">
            <Spinner />
          </div>
        ) : points.length === 0 ? (
          <p className="rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
            No copies yet. Back up now to make one.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Time</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead className="hidden sm:table-cell">Size</TableHead>
                <TableHead className="w-px">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {points.map((p) => {
                const expires = expiresNote(p);
                return (
                  <TableRow key={p.id}>
                    <TableCell>
                      <div>{when(p)}</div>
                      <div className="text-xs text-muted-foreground">
                        {relativeAge(p.time)}
                        {expires ? <span className="text-amber-700 dark:text-amber-400"> · {expires}</span> : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <Badge variant={p.kind === "pre-restore" ? "secondary" : "outline"}>{KIND_LABELS[p.kind]}</Badge>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">{formatBytes(p.size)}</TableCell>
                    <TableCell className="whitespace-nowrap py-0.5 text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Restore the copy of ${when(p)}`}
                        disabled={busy !== null || !backup.enabled}
                        title={backup.enabled ? undefined : "Turn the backup on to restore"}
                        onClick={() => setConfirm({ action: "restore", point: p })}
                      >
                        Restore
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive hover:text-destructive"
                        aria-label={`Delete the copy of ${when(p)}`}
                        disabled={busy !== null}
                        onClick={() => setConfirm({ action: "delete", point: p })}
                      >
                        Delete
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy === "restore"} onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
```

**Modify `web/src/lib/backups.ts`** — replace:

```ts
  return taken.includes(name) ? `The app already has a backup named ${name}.` : "";
}
```

with:

```ts
  return taken.includes(name) ? `The app already has a backup named ${name}.` : "";
}

// selectClass styles a native <select> like the design system's inputs.
export const selectClass =
  "h-9 w-full min-w-0 rounded-md border border-input bg-transparent px-3 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50";

// pathsFrom splits a paths field (one per line, or comma-separated) into
// its trimmed, non-empty paths.
export function pathsFrom(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((p) => p.trim())
    .filter(Boolean);
}

// keepError checks a retention's fields like the recipe; "" means valid.
export function keepError(recent: string, daily: string, monthly: string): string {
  const ms = durationMs(recent.trim());
  if (recent.trim() && (Number.isNaN(ms) || ms < 0)) return "Keep every copy for a duration like 24h or 72h.";
  for (const n of [daily, monthly]) {
    if (!/^\d+$/.test(n.trim()) || Number(n) > 3650) return "Use whole numbers from 0 to 3650 for the daily and monthly copies.";
  }
  return "";
}

// locationError checks a backup's place like the recipe: a volume's paths
// are relative to it (none: the whole volume, files only), a service's are
// absolute paths in its container; "" means valid.
export function locationError(type: BackupType, where: "volume" | "service", target: string, paths: string[]): string {
  if (!target) return where === "volume" ? "Pick a volume." : "Pick a service.";
  if (type === "sqlite" && paths.length !== 1) return "Enter the database file's path.";
  if (where === "service" && paths.length === 0) return "Enter the paths to back up.";
  for (const p of paths) {
    if (where === "service" && !p.startsWith("/")) return `${p}: use an absolute path in the container, like /app/data.`;
    if (where === "volume" && (p.startsWith("/") || p.split("/").includes(".."))) {
      return `${p}: use a path inside the volume, like app.db or media/.`;
    }
  }
  return "";
}
```

**Modify `web/src/pages/app-details.tsx`** — replace (1 of 4):

```tsx
import { LogsView, type LogLine } from "@/components/logs-view";
import { AppEditorPanel } from "@/components/app-editor-panel";
```

with:

```tsx
import { LogsView, type LogLine } from "@/components/logs-view";
import { AppBackups } from "@/components/app-backups";
import { AppEditorPanel } from "@/components/app-editor-panel";
```

**Modify `web/src/pages/app-details.tsx`** — replace (2 of 4):

```tsx

const TABS = ["logs", "editor", "history", "settings"] as const;

```

with:

```tsx

const TABS = ["logs", "editor", "backups", "history", "settings"] as const;

```

**Modify `web/src/pages/app-details.tsx`** — replace (3 of 4):

```tsx
          </TabsTrigger>
          <TabsTrigger value="history" className={tabTriggerClass}>
```

with:

```tsx
          </TabsTrigger>
          <TabsTrigger value="backups" className={tabTriggerClass}>
            Backups
          </TabsTrigger>
          <TabsTrigger value="history" className={tabTriggerClass}>
```

**Modify `web/src/pages/app-details.tsx`** — replace (4 of 4):

```tsx
          <AppEditorPanel appId={app.id} />
        </TabsContent>
```

with:

```tsx
          <AppEditorPanel appId={app.id} />
        </TabsContent>
        <TabsContent value="backups" className="mt-6">
          <AppBackups appId={app.id} />
        </TabsContent>
```

- [ ] **Step 4: Run the tests and checks**

```bash
CI=true pnpm --dir web run test
CI=true pnpm --dir web run lint
CI=true pnpm --dir web run build && touch web/dist/.gitkeep   # never commit web/dist
```

Expected: every command passes (`ok` / no output from the diff checks); `gofmt -l .` and `go vet ./...` stay clean for Go tasks, `tsc` and `eslint` for web tasks.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/add-backup-dialog.test.tsx \
  web/src/components/add-backup-dialog.tsx \
  web/src/components/app-backups.test.tsx \
  web/src/components/app-backups.tsx \
  web/src/components/backup-edit-dialog.test.tsx \
  web/src/components/backup-edit-dialog.tsx \
  web/src/components/backup-storage-dialog.test.tsx \
  web/src/components/backup-storage-dialog.tsx \
  web/src/components/restore-points-dialog.test.tsx \
  web/src/components/restore-points-dialog.tsx \
  web/src/lib/backups.test.ts \
  web/src/lib/backups.ts \
  web/src/pages/app-details.tsx
git commit -F - <<'MSG'
feat(web): the app's Backups tab: enable, edit, back up now, copies, restore, add

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
MSG
```
