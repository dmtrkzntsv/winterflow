# C2a Backup Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an app's compose file declare backups (`x-winterflow.backups`), let each deployment turn them on with a storage and a schedule, and have the agent run the `sqlite` and `files` backups itself — on a schedule or on request — into the server's storages, with tiered retention under a server-wide ceiling, restore points to list and delete, and backup health on the app status.

**Architecture:** A shared `internal/domain/backup/recipe` package parses and validates the recipe and the per-app overrides (kept in the committed `.winterflow/config.json` → `backups`) for both the API (400s at save) and the agent. The Docker Compose orchestrator resolves `WF_BACKUP_*` variables and `wf-backup-<name>` profiles at every deploy, maps backup paths to the app's own mounts with one cached `docker compose config`, and runs one backup by mounting those sources into a capped `alpine:3.20` helper that executes this very binary's hidden `backup dump` subcommand; the agent zstd-compresses the helper's stdout straight into the storage (`internal/infra/backup/layout`: data object, then manifest) and prunes. A scheduler goroutine (`internal/app/agent/backup`) — one timer, one run at a time, `state.json`, never on the command pipeline — drives the runs and reports health, which rides on `apps.status`. The API gains four agent-bound routes; `backup.run` answers when its run ends, so it is a *detached* command (own goroutine on the standalone bridge, 2-hour result wait in the API).

**Tech Stack:** Go 1.25; `go.yaml.in/yaml/v3` v3.0.5 (MIT/Apache-2.0) for compose files; `github.com/klauspost/compress/zstd` v1.19.2 (BSD-3-Clause/Apache-2.0/MIT) for compression; `modernc.org/sqlite` (pure Go, keeps `CGO_ENABLED=0` binaries static) for `VACUUM INTO` in the helper; `archive/tar`; the `docker` / `docker compose` CLIs (args always `[]string`). Both new modules are already in `go.sum` (indirect today). No web changes, no database migration.

**Spec:** `docs/superpowers/specs/2026-10-10-storages-and-backups-design.md` — section "C2 — Backups" (C1 for context). This plan, **C2a**, covers the recipe, overrides, `WF_BACKUP_` variables and profiles, mount resolution, the helper and its subcommand, the scheduler, one run for `sqlite`/`files`, retention and the ceiling, health, restore points (list, delete), Back up now, the four API routes, and the deferred C1 item "mount-point detection for local storages". **C2b** (a later plan on the same branch) covers restore in place, `delete_backups` with `.deleted` markers and the daily sweep, orphans, and all of C2's UI.

**Base:** branch `feat/backups` at `7cb8018` (main + the spec + C1). Every snippet below was built and its tests run, task by task in this order, on a copy of that commit; the tree at the end of Task 11 is byte-identical to the verified one (including `api/openapi.yaml`, `routes.golden`, `go.mod`, `go.sum`). `Create` blocks are whole files; `replace` blocks are exact find → replace pairs (each `find` occurs exactly once at that point; whitespace matters — copy them as shown); `Append` blocks go at the end of the file.

## Global Constraints

- Backup name: `^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`, unique in the app (`recipe.ValidateName`); types exactly `sqlite | files | custom` (postgres/mysql later).
- `every`: a Go duration `>= 5m`, or `manual` (never scheduled); default `1h`. `keep` defaults: `recent: 24h`, `daily: 14`, `monthly: 6`.
- A recipe never turns itself on: `enabled` defaults to `false`; `enabled: true` requires `storage` naming a storage on the app's server (400 otherwise).
- A backup names exactly one of `volume` or `service`; `volume` paths are relative (`safeRel`-style: clean, no `..`), `service` paths absolute container paths; errors at save for a path not on a mount, inside a single-file bind, or on tmpfs. The helper only mounts sources the app's compose file already mounts.
- A `custom` service must list the profile `wf-backup-<backup name>`; enabling the backup adds that profile, disabling removes it and stops the service.
- `${WF_BACKUP_<BACKUP>_<FIELD>}`: the fields of `WF_STORAGE_*` from the backup's storage, `PREFIX` = `<storage prefix><app-id>/<backup name>/`; undeclared backup → 400; declared and off → every field empty.
- Helper: `docker run --rm --network none --memory 256m --cpu-shares 256 --label winterflow.role=backup-helper --label winterflow.app=<app-id>` (plus `--cap-drop ALL --cap-add DAC_OVERRIDE --cap-add SETUID --cap-add SETGID --security-opt no-new-privileges`), sources at `/src/<n>` (read-only for `files`), the agent executable (`os.Executable()`, symlinks resolved, static) at `/wf:ro`, image the hard-coded constant `alpine:3.20`. Exit `3` = unchanged (a success without upload), `4` = missing.
- Objects: `<prefix><app-id>/<backup name>/<UTC yyyymmddThhmmssZ>-<kind>.<db|tar>.zst`, manifest beside it written **last** (sha256, size, raw size, type, agent version, fingerprint; never a secret); a point without a manifest is incomplete, ignored, pruned after 24h.
- Retention: `BACKUP_RETENTION_DAYS` (default 180, `0` = none; read by standalone and agent; documented in `.env.example`) overrides every rule; scheduled copies follow the tiers and the newest complete scheduled copy is kept; manual copies only by the ceiling (or by hand); pre-restore copies 30 days; an unchanged scheduled backup uploads anyway once its newest scheduled copy is older than half the ceiling.
- Deadlines: 30 minutes per run (dump + upload; a run past it has its helper `docker rm -f`'d), 5 minutes for the first image pull, 30 s for listing/deleting restore points and for `docker compose config`, 10 minutes for `backup.update` including its redeploy. Nothing blocks the standalone `InProcessBridge` goroutine: storage calls that can hang on a dead mount are abandoned at their deadline (`detached`), and `backup.run` is detached.
- Idle cost near zero: one scheduler goroutine and one timer (no per-app ticker, no polling); `docker compose config` only at deploy, `backup.update` and run time, cached by HEAD; status collection never execs (health comes from memory); one run at a time.
- Fail closed: every route checks `webutil.RequireServerAccess` (403 for a foreign server, asserted by the contract probes); a storage's S3 secret is decrypted only in the agent's memory — never logged, never in a manifest, never in the API database.
- Per-app in-memory bookkeeping is pruned on app delete (`forgetApp`: the mount cache; the scheduler's `Forget`: state, queue, the running job).
- Gates: ≥80% of changed lines covered (`make diffcov DIFFCOV_BASE=7cb8018`), contract suite on SQLite and Postgres, `api/openapi.yaml` regenerated (`make openapi`) after route or response-type changes; no web change, so never touch `web/src/components/ui/`.
- Commits: Conventional Commits, each message ending with a blank line and exactly `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **The app editor knows nothing of backups.** Its save sends the whole config blob without `backups`; replacing `config.json` with it would silently turn every backup off on every edit. The agent keeps the stored list when the blob has no `backups` key — Task 6 `TestKeepBackups`, `TestSaveKeepsAndCleansBackups`.
2. **Copies deleted by hand while the data is unchanged.** If the unchanged check compared with `state.json`, a backup whose copies were all deleted (or whose storage was swapped) would report "unchanged" forever and keep nothing. It compares with the newest complete copy's manifest instead — Task 8 `TestRunBackupStoresAPoint` (its last block).
3. **Profiles.** `docker compose up --remove-orphans` leaves the service of a profile that was turned off running (checked with compose v5.5.1), and an explicit `--profile` makes compose ignore the user's own `COMPOSE_PROFILES` — Task 6 `TestComposeUpProfiles`, `TestComposeProfiles`.
4. **Deleting an app while its backup runs.** The helper holds the app's volume (`down -v` would fail) and its outcome would be recorded for a deleted app. The dispatcher forgets the app before and after the delete; `Forget` stops the run and prunes state — Task 9 `TestReloadAndForget`, Task 10 `TestDispatchBackupCommands`.
5. **A plan made stale without a Reload** (a git-sourced app's upstream changed through the source poller): a scheduled backup that is now off must not make the scheduler spin. A run refused before it starts (`ErrNotRunnable`) rereads the plan and counts as an attempt for scheduling, never as a failure — Task 9 `TestNotRunnableRereadsThePlan`.

## Spec rulings (ambiguities resolved here)

- **Back up now's result** is one `operation_result` when the run ends (no separate "queued" message): the HTTP 202 is the acknowledgement. The command is detached (`command.Type.Detached`) and the API awaits it 2 h (`port.DispatchInput.Wait`), since the 10-minute pending TTL would drop it.
- **Override merging**: field by field, except that setting `volume` or `service` replaces the recipe's addressing (both fields), and `keep` replaces the recipe's `keep` as a whole (missing sub-fields take their defaults). `type` on a recipe entry is accepted only when equal to the recipe's (a UI-added backup the template later adopted).
- **Stale override vs incomplete UI-added backup**: at app save, an entry naming no recipe backup and carrying no `type` is dropped with a warning (spec: "dropped at the next save"); in `backup.update` (the user's own list) it is an error ("a new backup needs its type").
- **Recipe keys**: `enabled` and `storage` in a recipe are refused ("a recipe never turns a backup on"); other `x-winterflow` keys are ignored; a compose file that isn't valid YAML declares no recipe (compose reports it at deploy); an override compose file's entries replace the main file's by name.
- **API-side `WF_BACKUP_` checks**: the app editor doesn't send the backups list, so a name outside the recipe is judged only when the request carries the list or the app is new; otherwise the agent (which has the stored list) decides at save/deploy.
- **Mount resolution** runs for enabled `sqlite`/`files` backups only, via `docker compose … --profile '*' config --format json` (so a profiled service's mounts resolve), at deploy, `backup.update` and run time, cached per app by HEAD + `storage.env` + `.env.secrets`. A deploy whose enabled backup can't reach its data fails.
- **Custom backup health**: `ok` while one of the app's `wf-<app-id>-<service>-N` containers runs, else `off` (spec: "off, or ok while their service is running").
- **Mount-point detection** compares the folder's `st_dev` with that of `/`, and only under `/mnt` and `/media`: comparing with the parent would refuse every folder inside a mounted disk (the recommended `/mnt/usb/winterflow`), and folders elsewhere were allowed explicitly with a systemd drop-in.
- **Helper hardening beyond the spec's command line**: dropped capabilities, `no-new-privileges`, `--mount` instead of `-v` (a missing bind source fails instead of being created), a missing Docker volume refused (`docker volume inspect`) instead of created empty, and the sqlite dump switches to the database file's owner so SQLite never leaves root-owned `-wal`/`-shm` files in the app's volume.
- **Attempts**: a manual run counts as an attempt (next scheduled run = last attempt + every); a run refused before it starts is not a failure.
- **`backup.update` and the deployed mark**: it redeploys only a running app without undeployed changes, and only when profiles or `storage.env` change; otherwise its commit advances the deployed mark, so a backups-only commit doesn't look like a draft to `storage.save`.
- **Storage users**: an enabled backup uses its storage — `storage.list` shows the app, `storage.delete` is refused, `storage.save` redeploys it.
- **Point ids** are `<yyyymmddThhmmssZ>-<kind>`, manifests `<id>.json`; `RestorePoint` carries `expires` (the ceiling), for C2b's "expires in N days".

## File map

| File | Task | Change |
|---|---|---|
| `internal/infra/backup/store/local.go` (+ test), `docs/recipes/backup-storage.md` | 1 | refuse a folder on the system disk under `/mnt`, `/media`; `MountRoots` |
| `go.mod` | 2, 8 | `go.yaml.in/yaml/v3`, then `github.com/klauspost/compress` become direct |
| `internal/domain/model/errors.go` (+ test) | 2 | `Rejected` |
| `internal/domain/backup/recipe/{recipe,compose,effective}.go` (+ `recipe_test.go`) | 2 | new: recipe types, parsing, validation, merge |
| `internal/domain/model/storage.go` (+ test) | 3 | `WF_BACKUP_` references and values |
| `internal/domain/command/{backup.go,backup_test.go}`, `command.go`, `app.go`, `storage.go` | 3 | backup commands, health, `Detached` |
| `api/openapi.yaml` | 3, 11 | regenerated |
| `internal/infra/backup/layout/layout.go` (+ test), `pkg/config/config.go` (+ test), `.env.example` | 4 | object layout, points, retention; `BACKUP_RETENTION_DAYS` |
| `internal/infra/backup/helper/helper.go` (+ test) | 5 | new: helper contract |
| `internal/app/backup/cli/{cli,sqlite,files}.go` (+ `cli_test.go`), `cmd/standalone/main.go` | 5 | new: hidden `backup dump` |
| `cmd/agent/main.go` | 5, 10 | `backup` subcommand; then the scheduler |
| `internal/infra/orchestrator/docker_compose/backupconfig.go` (+ test) | 6 | new: overrides, profiles |
| `…/docker_compose/operations.go` | 6, 7 | profiles, save/undo, compose seam; then mount check at deploy |
| `…/docker_compose/repository.go` | 6, 7, 8 | seams: `runCompose`; `composeOutput` + mount cache; helper seams |
| `…/docker_compose/storageenv.go` | 3, 6 | `nil` declarations; then `WF_BACKUP_` values |
| `…/docker_compose/storages.go` | 1, 3, 6 | `store.MountRoots`; skip backup refs; backups use storages |
| `…/docker_compose/{store,runhelper,lifecycle}.go`, `runhelper_test.go`, `storages_test.go` | 6 | keep backups on save; profiles in `run.sh`; `GetApp` backups |
| `…/docker_compose/storageenv_test.go` | 3, 6 | expectations |
| `…/docker_compose/mounts.go` (+ test) | 7 | new: `docker compose config` → helper plan |
| `…/docker_compose/{backuprun,backups}.go` (+ tests), `backup_docker_test.go` | 8 | new: the engine |
| `internal/app/agent/backup/scheduler.go` (+ test) | 9 | new: scheduler |
| `internal/app/agent/backups.go`, `dispatcher_backups_test.go` | 10 | new: wiring |
| `internal/app/agent/dispatcher.go` | 6, 10 | save warnings; then backup handlers and hooks |
| `internal/app/agent/inprocess.go` (+ test), `internal/infra/bootstrap/standalone.go` | 10 | detached commands; scheduler start |
| `CLAUDE.md` | 1, 10 | storages line; command surface + Backups paragraph |
| `internal/domain/port/command.go`, `internal/infra/transport/dispatch/manager.go` (+ test) | 11 | `DispatchInput.Wait` |
| `internal/domain/usecase/app/backups.go` (+ test), `storages.go`, `usecase.go` | 3, 11 | `nil` declarations; then backup usecases and save-app checks |
| `internal/app/web/handler/app/backups.go` (+ test), `routes.go`, `routes_test.go`, `internal/app/web/testdata/routes.golden` | 11 | four routes |
| `internal/app/web/contract/{backups_test,fakeagent_test,matrix_test}.go` | 11 | contract tests, fake agent, probes |

Tasks are strictly sequential: each one's `replace` blocks are written against the tree the previous tasks left (the rows above with two task numbers).

## How to run things

- Prefix Go commands with `export GOTMPDIR=$HOME/.cache/wf-gotmp GOFLAGS=-buildvcs=false` when `/tmp` is small or the checkout is a worktree without VCS stamping (create the folder first).
- One package: `go test ./internal/domain/backup/recipe/`; one test: add `-run TestName`.
- Full suite as CI runs it: `make test` (race, `-coverpkg=./...`); the contract guard as CI runs it: `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/`; on Postgres: `make test-pg` (Docker).
- The Docker integration test (`TestBackupThroughDocker`) runs whenever `docker info` works and `-short` is off; it builds `winterflow/cmd/agent` with `CGO_ENABLED=0` (≈40 s cold) and uses `alpine:3.20`.
- Diff coverage for this branch: commit, then `make diffcov DIFFCOV_BASE=7cb8018` (Go must reach 80%; no web lines change).
- Regenerate: `make openapi`; the routes golden: `go test ./internal/app/web/ -run TestRoutesGolden -update`.


---

### Task 1: Mount-point detection for local storages

**Files:**
- Modify: `internal/infra/backup/store/local.go` (checkRoot → also `checkMounted`; `MountRoots`, `deviceOf`, `inside`, `notMountedError`)
- Test: `internal/infra/backup/store/local_test.go` (append)
- Modify: `internal/infra/orchestrator/docker_compose/storages.go` (its private `mountRoots` becomes `store.MountRoots`)
- Modify: `docs/recipes/backup-storage.md`, `CLAUDE.md` (Storages paragraph)

**Interfaces:**
- Consumes: nothing new.
- Produces: `store.MountRoots []string` (`/mnt`, `/media`), used by `explainWrite` in docker_compose; `local.checkRoot` now also fails with `notMountedError` ("the storage folder X is on the system disk, not on a mounted disk or share (is the disk mounted?)") for a folder under a mount root whose `st_dev` equals that of `/`. The error does **not** match `fs.ErrNotExist` (so `checkStorageFolders` reports it verbatim through its default branch).

- [ ] **Step 1: Write the failing tests**

**Append to `internal/infra/backup/store/local_test.go`:**

```go

// stubDevices makes deviceOf answer from devs (paths not in it fail) and the
// mount roots be roots, for the rest of the test.
func stubDevices(t *testing.T, devs map[string]uint64, roots ...string) {
	t.Helper()
	prevDev, prevRoots := deviceOf, MountRoots
	deviceOf = func(p string) (uint64, error) {
		if d, ok := devs[p]; ok {
			return d, nil
		}
		return 0, fs.ErrPermission
	}
	MountRoots = roots
	t.Cleanup(func() { deviceOf, MountRoots = prevDev, prevRoots })
}

// A disk's mount point under /mnt while the disk is not mounted is an empty
// folder on the system disk: refused, before and after Open.
func TestLocalStoreRefusesAnUnmountedMountPoint(t *testing.T) {
	root := t.TempDir()
	mnt := filepath.Join(root, "mnt")
	usb := filepath.Join(mnt, "usb")
	if err := os.MkdirAll(usb, 0o700); err != nil {
		t.Fatal(err)
	}
	devs := map[string]uint64{"/": 1, usb: 1}
	stubDevices(t, devs, mnt)

	_, err := openLocal(usb)
	var nm notMountedError
	if !errors.As(err, &nm) || !strings.Contains(err.Error(), "is on the system disk") || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unmounted mount point: %v", err)
	}

	// Mounted: another filesystem than "/".
	devs[usb] = 2
	st, err := openLocal(usb)
	if err != nil {
		t.Fatalf("mounted disk: %v", err)
	}
	if err := st.Put(context.Background(), "a/b", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	// Unmounted after Open: every call re-checks.
	devs[usb] = 1
	if err := st.Put(context.Background(), "a/c", strings.NewReader("x")); !errors.As(err, &nm) {
		t.Fatalf("put after unmount: %v", err)
	}
	if _, err := st.List(context.Background(), ""); !errors.As(err, &nm) {
		t.Fatalf("list after unmount: %v", err)
	}
	// A folder inside the mounted disk shares the disk's device.
	inner := filepath.Join(usb, "winterflow")
	if err := os.Mkdir(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	devs[usb], devs[inner] = 2, 2
	if _, err := openLocal(inner); err != nil {
		t.Fatalf("folder on the disk: %v", err)
	}
	// A device that can't be read is an error, not a pass.
	delete(devs, inner)
	if _, err := openLocal(inner); err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("unreadable device: %v", err)
	}
}

// Folders outside the mount roots are never compared: the operator allowed
// them explicitly.
func TestLocalStoreChecksOnlyUnderMountRoots(t *testing.T) {
	root := t.TempDir()
	srv := filepath.Join(root, "srv")
	if err := os.Mkdir(srv, 0o700); err != nil {
		t.Fatal(err)
	}
	stubDevices(t, map[string]uint64{"/": 1, srv: 1}, filepath.Join(root, "mnt"))
	if _, err := openLocal(srv); err != nil {
		t.Fatalf("folder outside the mount roots: %v", err)
	}
	// "/" unreadable: nothing to compare with.
	stubDevices(t, map[string]uint64{}, root)
	if _, err := openLocal(srv); err != nil {
		t.Fatalf("no device for /: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/backup/store/`

Expected: build failure: `undefined: deviceOf`, `undefined: MountRoots`, `undefined: notMountedError`.

- [ ] **Step 3: Implement**

**In `internal/infra/backup/store/local.go`, replace:**

```go
	"sort"
	"strings"

	"winterflow/pkg/fsutil"
```

**with:**

```go
	"sort"
	"strings"
	"syscall"

	"winterflow/pkg/fsutil"
```

**In `internal/infra/backup/store/local.go`, replace:**

```go
// checkRoot requires the root to be an existing folder. Every call re-checks
// it, so writes fail once the folder itself is gone (a folder on a disk that
// was unplugged after Open) instead of landing elsewhere. It cannot tell an
// empty leftover mount point from a real folder: that is why
// docs/recipes/backup-storage.md tells users to register a folder created
// inside the mounted disk, never the mount point. A missing root matches
// fs.ErrNotExist.
func (l *local) checkRoot() error {
	info, err := os.Stat(l.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return missingFolderError(l.root)
		}
		return fmt.Errorf("cannot open %s: %w", l.root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", l.root)
	}
	return nil
}
```

**with:**

```go
// checkRoot requires the root to be an existing folder and, under a mount
// root, a folder on a mounted disk (checkMounted). Every call re-checks it,
// so writes fail once the folder itself is gone (a folder on a disk that was
// unplugged after Open) instead of landing elsewhere. A missing root matches
// fs.ErrNotExist.
func (l *local) checkRoot() error {
	info, err := os.Stat(l.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return missingFolderError(l.root)
		}
		return fmt.Errorf("cannot open %s: %w", l.root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", l.root)
	}
	return checkMounted(l.root)
}

// MountRoots are the folders disks and network shares are mounted under: the
// systemd unit lets the agent write there (ReadWritePaths=-/mnt -/media).
var MountRoots = []string{"/mnt", "/media"}

// deviceOf is the id of the filesystem path lives on (st_dev); replaced in
// tests.
var deviceOf = func(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil
}

// checkMounted refuses a folder under a mount root that is on the same
// filesystem as "/": the mount point of a disk that is not mounted (after a
// reboot, or unplugged) is an empty folder on the system disk, and backups
// written there would quietly fill it. Comparing with the folder's parent
// instead would refuse every folder inside a mounted disk (the recommended
// /mnt/usb/winterflow); comparing with "/" accepts those and still refuses a
// bare, unmounted /mnt/usb. Folders outside the mount roots are not checked:
// the operator allowed them with a systemd drop-in.
func checkMounted(folder string) error {
	for _, root := range MountRoots {
		if !inside(folder, root) {
			continue
		}
		sys, err := deviceOf("/")
		if err != nil {
			return nil // nothing to compare with
		}
		dev, err := deviceOf(folder)
		if err != nil {
			return fmt.Errorf("cannot open %s: %w", folder, err)
		}
		if dev == sys {
			return notMountedError(folder)
		}
		return nil
	}
	return nil
}

// inside reports whether path is dir or below it.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// notMountedError is a folder under /mnt or /media that is on the system
// disk: usually a disk that is not mounted.
type notMountedError string

func (e notMountedError) Error() string {
	return "the storage folder " + string(e) + " is on the system disk, not on a mounted disk or share (is the disk mounted?)"
}
```

**In `internal/infra/orchestrator/docker_compose/storages.go`, replace:**

```go
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto"
```

**with:**

```go
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/backup/store"
	"winterflow/pkg/crypto"
```

**In `internal/infra/orchestrator/docker_compose/storages.go`, delete:**

```go
// mountRoots are the folders the systemd unit lets the agent write
// (ReadWritePaths=-/mnt -/media): the usual mount points of USB disks and
// network shares.
var mountRoots = []string{"/mnt", "/media"}

```

**In `internal/infra/orchestrator/docker_compose/storages.go`, replace:**

```go
	for _, root := range mountRoots {
```

**with:**

```go
	for _, root := range store.MountRoots {
```

**In `docs/recipes/backup-storage.md`, replace:**

````markdown
**Register a folder inside the mounted disk, never the mount point
itself.** Create it after mounting, e.g. `/mnt/usb/winterflow` for a disk
mounted on `/mnt/usb`. When the disk isn't mounted (after a reboot, or
unplugged), the mount point is still there as an empty folder on the system
disk, and every check winterflow makes can accept it: backups and apps
would then quietly fill the system disk. A folder created on the disk disappears
with it, so the test, deploys and writes fail instead.
````

**with:**

````markdown
**Register a folder inside the mounted disk, never the mount point
itself.** Create it after mounting, e.g. `/mnt/usb/winterflow` for a disk
mounted on `/mnt/usb`. When the disk isn't mounted (after a reboot, or
unplugged), the mount point is still there as an empty folder on the system
disk. A folder created on the disk disappears with it, so the test, deploys,
backups and writes fail instead of quietly filling the system disk.

Under `/mnt` and `/media` winterflow also refuses a folder that is on the
same filesystem as `/` ("is on the system disk, not on a mounted disk or
share"), so even a registered mount point fails while its disk is not
mounted. Folders elsewhere (allowed with a drop-in, below) are not checked
this way.
````

**In `CLAUDE.md`, replace:**

````markdown
**Storages:** a storage is a local folder or an S3-compatible bucket registered per server (`{AGENT_DATA_DIR}/storages/<name>.json`, 0600, secret ECIES-encrypted at rest; mirrored without secrets into the API's `server_storages`). Drivers live in `internal/infra/backup/store` (`local`, `s3` via minio-go); every save runs the 30s probe test first. Compose files reference storages as `${WF_STORAGE_<NAME>_<FIELD>}`: only referenced values are materialized at every deploy into the gitignored, never-committed `.winterflow/storage.env` (passed between `.env` and `.env.secrets`); user variables may not start with `WF_STORAGE_`/`WF_BACKUP_`; save-app 400s on a reference to a missing storage. Compose files are always passed with explicit `-f` (`composeFiles`, compose's own detection order plus override), and `run.sh` mirrors the flags. `storage.save` redeploys the running apps that reference it (stopped apps and drafts are left alone); `storage.delete` is refused while an app references it. A deploy is refused while a referenced local storage's folder is missing (10s-bounded stat, so docker never creates a bind source on the system disk).
````

**with:**

````markdown
**Storages:** a storage is a local folder or an S3-compatible bucket registered per server (`{AGENT_DATA_DIR}/storages/<name>.json`, 0600, secret ECIES-encrypted at rest; mirrored without secrets into the API's `server_storages`). Drivers live in `internal/infra/backup/store` (`local`, `s3` via minio-go); every save runs the 30s probe test first. Compose files reference storages as `${WF_STORAGE_<NAME>_<FIELD>}`: only referenced values are materialized at every deploy into the gitignored, never-committed `.winterflow/storage.env` (passed between `.env` and `.env.secrets`); user variables may not start with `WF_STORAGE_`/`WF_BACKUP_`; save-app 400s on a reference to a missing storage. Compose files are always passed with explicit `-f` (`composeFiles`, compose's own detection order plus override), and `run.sh` mirrors the flags. `storage.save` redeploys the running apps that reference it (stopped apps and drafts are left alone); `storage.delete` is refused while an app references it. A deploy is refused while a referenced local storage's folder is missing (10s-bounded stat, so docker never creates a bind source on the system disk); under `/mnt` and `/media` a folder on the same filesystem as `/` (an unmounted mount point) is refused too.
````

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/infra/backup/store/ ./internal/infra/orchestrator/docker_compose/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/infra/backup/store/local_test.go internal/infra/backup/store/local.go internal/infra/orchestrator/docker_compose/storages.go docs/recipes/backup-storage.md CLAUDE.md
git commit -F - <<'EOF'
fix(storages): refuse a local storage folder on the system disk under /mnt and /media

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 2: The backup recipe: parse, validate, merge overrides

**Files:**
- Create: `internal/domain/backup/recipe/recipe.go` (types, defaults, `ValidateName`, `validate`), `compose.go` (`ParseCompose`), `effective.go` (`Effective`, `Validate`, `Check`, `Clean`, `Declared`), `recipe_test.go`
- Modify: `internal/domain/model/errors.go` (+ `Rejected`), `internal/domain/model/errors_test.go`
- Modify: `go.mod` (`go.yaml.in/yaml/v3` becomes a direct dependency; already in `go.sum`)

**Interfaces:**
- Consumes: `model.ErrRejected`.
- Produces (package `recipe`, import `winterflow/internal/domain/backup/recipe`):
  - `type Type string`; `TypeSQLite = "sqlite"`, `TypeFiles = "files"`, `TypeCustom = "custom"`; `Manual = "manual"`; `DefaultEvery = "1h"`; `MinEvery = 5*time.Minute`; `DefaultRecent = 24h`, `DefaultDaily = 14`, `DefaultMonthly = 6`; `ProfilePrefix = "wf-backup-"`; `Profile(name) string`
  - `ErrInvalid` (matches `model.ErrRejected`; every recipe/override error wraps it and names the field, e.g. `x-winterflow.backups[0].volume: "dat" is not a volume of this compose file`, `backups[1].every: ...`)
  - `type Keep struct{ Recent string; Daily, Monthly *int }` (JSON/YAML `recent`, `daily`, `monthly`); `(*Keep).Retention() Retention`; `type Retention struct{ Recent time.Duration; Daily, Monthly int }`
  - `type BackupConfig struct{ Name; Type; Enabled bool; Storage, Every string; Keep *Keep; Volume, Service, Path string; Paths, Exclude []string }` (JSON `name`, `type,omitempty`, `enabled,omitempty`, `storage,omitempty`, `every,omitempty`, `keep,omitempty`, `volume,omitempty`, `service,omitempty`, `path,omitempty`, `paths,omitempty`, `exclude,omitempty`) — one list entry, recipe or override
  - `type EffectiveBackup struct{ Name; Type; Enabled bool; Storage, Every string; Keep *Keep; Volume, Service, Path string; Paths, Exclude []string; InRecipe bool; FromRecipe map[string]bool }`; `(EffectiveBackup).Interval() time.Duration` (0 = manual/custom); `(EffectiveBackup).Builtin() bool` (sqlite or files)
  - `ValidateName(name string) error`
  - `type Compose struct{ Volumes map[string]bool; Services map[string][]string /* profiles */; Recipe []BackupConfig }`; `ParseCompose(files ...[]byte) (Compose, error)`
  - `Effective(comp Compose, overrides []BackupConfig) ([]EffectiveBackup, error)`; `Validate(comp Compose) error`; `Check(comp, overrides) ([]EffectiveBackup, error)` (strict: a name outside the recipe needs `type`); `Clean(comp, overrides) (kept []BackupConfig, warnings []string)`; `Declared([]EffectiveBackup) map[string]EffectiveBackup`
  - `Field*` constants for `FromRecipe` keys: `every`, `keep`, `volume`, `service`, `path`, `paths`, `exclude`
  - `model.Rejected(msg string) error`

- [ ] **Step 1: Add the YAML module as a direct dependency**

```bash
go get go.yaml.in/yaml/v3@v3.0.5
go mod tidy
```

Expected: `go.mod` moves `go.yaml.in/yaml/v3 v3.0.5` from the indirect block into the main `require` block; `go.sum` is unchanged (the module is already there). It is MIT/Apache-2.0.

- [ ] **Step 2: Write the failing tests**

**In `internal/domain/model/errors_test.go`, replace:**

```go
	if errors.Is(errors.New("db down"), ErrRejected) {
		t.Error("an internal error is rejected")
	}
}
```

**with:**

```go
	if errors.Is(errors.New("db down"), ErrRejected) {
		t.Error("an internal error is rejected")
	}
	other := Rejected("invalid backup")
	if !errors.Is(fmt.Errorf("%w: x", other), ErrRejected) || other.Error() != "invalid backup" || errors.Is(other, ErrStorageRef) {
		t.Errorf("Rejected = %v", other)
	}
}
```

**Create `internal/domain/backup/recipe/recipe_test.go`:**

```go
package recipe

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/model"
)

const compose = `
services:
  web:
    image: example/web
    volumes: [data:/data, ./media:/app/media]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
volumes:
  data:
  cache: {}
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: twillingate.db
      every: 1h
      keep:
        recent: 24h
        daily: 14
        monthly: 6
    - name: media
      type: files
      service: web
      paths: [/app/media, /data/config]
      exclude: [/app/media/cache/, "*.tmp"]
      every: 24h
    - name: replica
      type: custom
      service: litestream
`

func parse(t *testing.T, files ...string) Compose {
	t.Helper()
	raw := make([][]byte, len(files))
	for i, f := range files {
		raw[i] = []byte(f)
	}
	c, err := ParseCompose(raw...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ptr(n int) *int { return &n }

func TestParseCompose(t *testing.T) {
	c := parse(t, compose)
	if !c.Volumes["data"] || !c.Volumes["cache"] || len(c.Volumes) != 2 {
		t.Fatalf("volumes = %v", c.Volumes)
	}
	if !reflect.DeepEqual(c.Services["litestream"], []string{"wf-backup-replica"}) || c.Services["web"] != nil {
		t.Fatalf("services = %v", c.Services)
	}
	want := []BackupConfig{
		{Name: "database", Type: TypeSQLite, Volume: "data", Path: "twillingate.db", Every: "1h",
			Keep: &Keep{Recent: "24h", Daily: ptr(14), Monthly: ptr(6)}},
		{Name: "media", Type: TypeFiles, Service: "web", Paths: []string{"/app/media", "/data/config"},
			Exclude: []string{"/app/media/cache/", "*.tmp"}, Every: "24h"},
		{Name: "replica", Type: TypeCustom, Service: "litestream"},
	}
	if !reflect.DeepEqual(c.Recipe, want) {
		t.Fatalf("recipe = %+v", c.Recipe)
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	// No x-winterflow, a null one, a null list, invalid YAML: no recipe.
	for _, f := range []string{"services: {a: {}}", "x-winterflow:\n", "x-winterflow:\n  backups:\n", "x-winterflow: {other: 1}", ":\n  - ["} {
		if c := parse(t, f); len(c.Recipe) != 0 {
			t.Errorf("%q: recipe = %+v", f, c.Recipe)
		}
	}
}

// The override file adds services and volumes and replaces recipe entries
// by name.
func TestParseComposeOverrideFile(t *testing.T) {
	override := `
services:
  worker: {}
volumes:
  extra:
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: extra
      path: other.db
    - name: added
      type: files
      volume: extra
`
	c := parse(t, compose, override)
	if !c.Volumes["extra"] || !c.Volumes["data"] {
		t.Fatalf("volumes = %v", c.Volumes)
	}
	if _, ok := c.Services["worker"]; !ok {
		t.Fatal("override service missing")
	}
	if len(c.Recipe) != 4 || c.Recipe[0].Volume != "extra" || c.Recipe[3].Name != "added" {
		t.Fatalf("recipe = %+v", c.Recipe)
	}
}

func TestParseComposeErrors(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"x-winterflow: [1]", "x-winterflow: must be a mapping"},
		{"x-winterflow:\n  backups: {name: a}", "x-winterflow.backups: must be a list of backups"},
		{"x-winterflow:\n  backups: [db]", "x-winterflow.backups[0]: must be a mapping"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, colour: red}", "x-winterflow.backups[0].colour: unknown key"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, enabled: true}", "x-winterflow.backups[0].enabled: a recipe never turns a backup on"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, storage: s3}", "x-winterflow.backups[0].storage: a recipe never turns a backup on"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, keep: {weekly: 2}}", "x-winterflow.backups[0].keep.weekly: unknown key"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite, keep: 3}", "x-winterflow.backups[0]: line 3: cannot unmarshal"},
		{"x-winterflow:\n  backups:\n    - {name: db, type: files, paths: /a}", "x-winterflow.backups[0]: line 3: cannot unmarshal !!str `/a` into []string"},
		{"x-winterflow:\n  backups:\n    - {name: DB, type: sqlite}", `x-winterflow.backups[0].name: "DB" is not a valid backup name`},
		{"x-winterflow:\n  backups:\n    - {name: db, type: sqlite}\n    - {name: db, type: files}", `x-winterflow.backups[1].name: "db" is used by another backup`},
	} {
		_, err := ParseCompose([]byte(tc.yaml))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.yaml, err, tc.want)
		}
	}
}

// Every value error, through Validate (the recipe alone).
func TestValidateErrors(t *testing.T) {
	head := "services:\n  web: {}\n  tool: {profiles: [other]}\nvolumes:\n  data:\nx-winterflow:\n  backups:\n    - "
	for _, tc := range []struct{ entry, want string }{
		{"{name: a}", "x-winterflow.backups[0].type: a backup needs a type"},
		{"{name: a, type: postgres}", `x-winterflow.backups[0].type: unknown type "postgres"`},
		{"{name: a, type: sqlite, volume: data, service: web, path: a.db}", "set either volume or service, not both"},
		{"{name: a, type: sqlite, path: a.db}", "set volume (a volume of the compose file) or service"},
		{"{name: a, type: sqlite, volume: dat, path: a.db}", `x-winterflow.backups[0].volume: "dat" is not a volume of this compose file`},
		{"{name: a, type: sqlite, service: api, path: /a.db}", `x-winterflow.backups[0].service: "api" is not a service of this compose file`},
		{"{name: a, type: sqlite, volume: data}", "x-winterflow.backups[0].path: a sqlite backup needs path"},
		{"{name: a, type: sqlite, volume: data, path: a.db, paths: [b]}", "a sqlite backup takes path"},
		{"{name: a, type: sqlite, volume: data, path: /a.db}", `x-winterflow.backups[0].path: "/a.db" is not a path inside the volume`},
		{"{name: a, type: sqlite, volume: data, path: ../a.db}", "is not a path inside the volume"},
		{"{name: a, type: sqlite, volume: data, path: a/../b.db}", "is not a path inside the volume"},
		{"{name: a, type: sqlite, service: web, path: a.db}", `x-winterflow.backups[0].path: "a.db" is not a path inside the service's container`},
		{"{name: a, type: sqlite, service: web, path: /}", "is not a path inside the service's container"},
		{"{name: a, type: sqlite, volume: data, path: a.db, every: 1m}", "x-winterflow.backups[0].every: 1m is too often: at least 5m0s"},
		{"{name: a, type: sqlite, volume: data, path: a.db, every: hourly}", `"hourly" is neither a duration like 1h or 30m nor manual`},
		{"{name: a, type: sqlite, volume: data, path: a.db, keep: {recent: soon}}", `x-winterflow.backups[0].keep.recent: "soon" is not a duration`},
		{"{name: a, type: sqlite, volume: data, path: a.db, keep: {daily: -1}}", "x-winterflow.backups[0].keep.daily: -1 is not a count"},
		{"{name: a, type: sqlite, volume: data, path: a.db, keep: {monthly: 9999}}", "x-winterflow.backups[0].keep.monthly: 9999 is not a count"},
		{"{name: a, type: files, volume: data, path: a}", "x-winterflow.backups[0].path: a files backup takes paths"},
		{"{name: a, type: files, service: web}", "x-winterflow.backups[0].paths: a files backup addressed by service needs paths"},
		{"{name: a, type: files, service: web, paths: [/a, /a]}", `x-winterflow.backups[0].paths[1]: "/a" is listed twice`},
		{"{name: a, type: files, volume: data, exclude: ['[']}", `x-winterflow.backups[0].exclude[0]: "[" is not a valid pattern`},
		{"{name: a, type: files, volume: data, exclude: ['']}", `x-winterflow.backups[0].exclude[0]: "" is not a pattern`},
		{"{name: a, type: custom}", "x-winterflow.backups[0].service: a custom backup needs service"},
		{"{name: a, type: custom, service: web, every: 1h}", "a custom backup takes only service"},
		{"{name: a, type: custom, service: api}", `"api" is not a service of this compose file`},
		{"{name: a, type: custom, service: tool}", `service "tool" must list the profile "wf-backup-a" in its profiles`},
	} {
		c, err := ParseCompose([]byte(head + tc.entry))
		if err == nil {
			err = Validate(c)
		}
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.entry, err, tc.want)
		}
	}
	// Too many paths.
	c := parse(t, "volumes: {data: }")
	many := make([]string, maxListLen+1)
	for i := range many {
		many[i] = "p" + strings.Repeat("x", i)
	}
	c.Recipe = []BackupConfig{{Name: "a", Type: TypeFiles, Volume: "data", Paths: many}}
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "at most 64 paths") {
		t.Fatalf("too many paths: %v", err)
	}
	// Control characters never pass.
	c.Recipe = []BackupConfig{{Name: "a", Type: TypeFiles, Volume: "data", Paths: []string{"a\nb"}}}
	if err := Validate(c); err == nil {
		t.Fatal("a newline in a path passed")
	}
}

func TestEffective(t *testing.T) {
	c := parse(t, compose)
	got, err := Effective(c, []BackupConfig{
		{Name: "database", Enabled: true, Storage: "hetzner", Every: "15m"},
		{Name: "audit", Type: TypeSQLite, Volume: "data", Path: "audit.db", Enabled: true, Storage: "backup-disk"},
		{Name: "gone", Every: "2h"}, // stale override: skipped
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d backups: %+v", len(got), got)
	}
	db := got[0]
	if db.Name != "database" || !db.Enabled || db.Storage != "hetzner" || db.Every != "15m" || db.Interval() != 15*time.Minute ||
		!db.InRecipe || db.FromRecipe[FieldEvery] || !db.FromRecipe[FieldVolume] || !db.FromRecipe[FieldPath] || !db.FromRecipe[FieldKeep] {
		t.Fatalf("database = %+v", db)
	}
	media := got[1]
	if media.Enabled || media.Storage != "" || media.Every != "24h" || *media.Keep.Daily != DefaultDaily || media.Keep.Recent != "24h0m0s" ||
		!media.FromRecipe[FieldExclude] || !media.FromRecipe[FieldService] || !media.Builtin() {
		t.Fatalf("media = %+v (keep %+v)", media, media.Keep)
	}
	replica := got[2]
	if replica.Type != TypeCustom || replica.Every != "" || replica.Keep != nil || replica.Interval() != 0 || replica.Builtin() {
		t.Fatalf("replica = %+v", replica)
	}
	audit := got[3]
	if audit.InRecipe || len(audit.FromRecipe) != 0 || audit.Every != DefaultEvery || audit.Interval() != time.Hour {
		t.Fatalf("audit = %+v", audit)
	}
	if d := Declared(got); len(d) != 4 || d["audit"].Path != "audit.db" {
		t.Fatalf("declared = %+v", d)
	}
}

func TestEffectiveOverrideRules(t *testing.T) {
	c := parse(t, compose)
	// Moving a backup to a service needs its paths too.
	got, err := Effective(c, []BackupConfig{{Name: "database", Service: "web", Path: "/data/twillingate.db"}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Volume != "" || got[0].Service != "web" || got[0].FromRecipe[FieldVolume] || got[0].FromRecipe[FieldService] {
		t.Fatalf("moved = %+v", got[0])
	}
	if got[0].Every != "1h" || !got[0].FromRecipe[FieldEvery] {
		t.Fatalf("every should follow the recipe: %+v", got[0])
	}
	for _, tc := range []struct {
		o    []BackupConfig
		want string
	}{
		{[]BackupConfig{{Name: "database", Service: "web"}}, `backups[0].path: "twillingate.db" is not a path inside the service's container`},
		{[]BackupConfig{{Name: "database", Type: TypeFiles}}, "backups[0].type: database is a sqlite backup in the compose file: its type can't change"},
		{[]BackupConfig{{Name: "database", Enabled: true}}, "backups[0].storage: an enabled backup needs a storage"},
		{[]BackupConfig{{Name: "database", Storage: "Bad_Name"}}, `backups[0].storage: "Bad_Name" is not a valid storage name`},
		{[]BackupConfig{{Name: "database"}, {Name: "database"}}, `backups[1].name: "database" is listed twice`},
		{[]BackupConfig{{Name: "x_y"}}, `backups[0].name: "x_y" is not a valid backup name`},
		{[]BackupConfig{{Name: "new", Type: TypeFiles, Service: "web"}}, "backups[0].paths: a files backup addressed by service needs paths"},
		{[]BackupConfig{{Name: "database", Every: "1s"}}, "backups[0].every: 1s is too often"},
	} {
		if _, err := Effective(c, tc.o); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.o, err, tc.want)
		}
	}
	// The same type on a recipe entry is accepted (a UI-added backup the
	// template later adopted).
	if _, err := Effective(c, []BackupConfig{{Name: "database", Type: TypeSQLite}}); err != nil {
		t.Fatal(err)
	}
	// An invalid recipe fails whatever the overrides.
	c.Recipe[0].Path = ""
	if _, err := Effective(c, nil); err == nil || !strings.Contains(err.Error(), "x-winterflow.backups[0].path") {
		t.Fatalf("invalid recipe: %v", err)
	}
}

func TestCheckRefusesAnIncompleteNewBackup(t *testing.T) {
	c := parse(t, compose)
	if _, err := Check(c, []BackupConfig{{Name: "gone", Every: "2h"}}); err == nil ||
		!strings.Contains(err.Error(), `backups[0]: the compose file has no backup "gone": a new backup needs its type`) {
		t.Fatalf("err = %v", err)
	}
	got, err := Check(c, []BackupConfig{{Name: "media", Enabled: true, Storage: "disk"}})
	if err != nil || !got[1].Enabled {
		t.Fatalf("check = %+v, %v", got, err)
	}
}

func TestClean(t *testing.T) {
	c := parse(t, compose)
	kept, warnings := Clean(c, []BackupConfig{
		// Same as the recipe except storage and enabled: only those stay.
		{Name: "database", Type: TypeSQLite, Enabled: true, Storage: "hetzner", Every: "1h", Volume: "data",
			Path: "twillingate.db", Keep: &Keep{Recent: "24h", Daily: ptr(14), Monthly: ptr(6)}},
		// Says nothing: dropped.
		{Name: "media", Paths: []string{"/app/media", "/data/config"}},
		// Changed fields stay.
		{Name: "replica", Enabled: true, Storage: "hetzner"},
		{Name: "gone", Every: "2h"},
		{Name: "audit", Type: TypeSQLite, Volume: "data", Path: "audit.db"},
	})
	want := []BackupConfig{
		{Name: "database", Enabled: true, Storage: "hetzner"},
		{Name: "replica", Enabled: true, Storage: "hetzner"},
		{Name: "audit", Type: TypeSQLite, Volume: "data", Path: "audit.db"},
	}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept = %+v", kept)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `no longer has the backup "gone"`) {
		t.Fatalf("warnings = %v", warnings)
	}
	kept, _ = Clean(c, []BackupConfig{{Name: "media", Exclude: []string{}, Every: "6h", Keep: &Keep{Daily: ptr(3)}, Service: "web", Volume: ""},
		{Name: "database", Volume: "data", Path: "other.db"}})
	if len(kept) != 2 || kept[0].Exclude == nil || kept[0].Every != "6h" || kept[0].Keep == nil || kept[0].Service != "" ||
		kept[1].Volume != "" || kept[1].Path != "other.db" {
		t.Fatalf("kept = %+v", kept)
	}
}

func TestRetention(t *testing.T) {
	var k *Keep
	if r := k.Retention(); r != (Retention{Recent: 24 * time.Hour, Daily: 14, Monthly: 6}) {
		t.Fatalf("defaults = %+v", r)
	}
	if r := (&Keep{Recent: "2h", Daily: ptr(0)}).Retention(); r != (Retention{Recent: 2 * time.Hour, Daily: 0, Monthly: 6}) {
		t.Fatalf("partial = %+v", r)
	}
	if (EffectiveBackup{Every: Manual}).Interval() != 0 {
		t.Fatal("manual has an interval")
	}
	if Profile("replica") != "wf-backup-replica" {
		t.Fatal(Profile("replica"))
	}
}

func TestErrInvalidIsARejection(t *testing.T) {
	_, err := ParseCompose([]byte("x-winterflow: [1]"))
	if !errors.Is(err, model.ErrRejected) {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/domain/model/ ./internal/domain/backup/recipe/`

Expected: build failure: `undefined: Rejected`; the recipe package has no non-test files yet (`no non-test Go files`).

- [ ] **Step 4: Implement**

**In `internal/domain/model/errors.go`, replace:**

```go
func rejected(msg string) error { return &rejection{msg: msg} }
```

**with:**

```go
func rejected(msg string) error { return &rejection{msg: msg} }

// Rejected makes a sentinel error for another package that matches
// ErrRejected (an invalid backup recipe, say).
func Rejected(msg string) error { return rejected(msg) }
```

**Create `internal/domain/backup/recipe/recipe.go`:**

```go
// Package recipe parses and validates backups: the recipe a compose file
// declares under x-winterflow.backups (what can be backed up), the overrides
// an app keeps in .winterflow/config.json under "backups" (per-deployment
// settings and backups added in the UI), and the effective backups the two
// make together. Both the API (400 at save when the compose file is in the
// request) and the agent (at save and deploy) use it; resolving paths to
// Docker mounts is the agent's job.
package recipe

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"winterflow/internal/domain/model"
)

// Type is a backup type.
type Type string

const (
	TypeSQLite Type = "sqlite" // one SQLite database file, copied with VACUUM INTO
	TypeFiles  Type = "files"  // folders and files, archived with tar
	TypeCustom Type = "custom" // the app's own tool, run as a service behind a profile
)

// Manual is the every value of a backup that is never scheduled: it runs
// only through Back up now.
const Manual = "manual"

// Defaults and limits of the schedule and the retention.
const (
	DefaultEvery   = "1h"
	MinEvery       = 5 * time.Minute
	DefaultRecent  = 24 * time.Hour
	DefaultDaily   = 14
	DefaultMonthly = 6
	maxTier        = 3650
	maxPathLen     = 1024
	maxListLen     = 64
)

// ProfilePrefix starts the compose profile of a custom backup:
// wf-backup-<backup name>. Enabling the backup activates it.
const ProfilePrefix = "wf-backup-"

// Profile is the compose profile a custom backup's service must list.
func Profile(name string) string { return ProfilePrefix + name }

// ErrInvalid marks a recipe or override that can't be used; the message
// names the field (x-winterflow.backups[0].volume, backups[1].every). It
// matches model.ErrRejected: the API answers it with a 400.
var ErrInvalid = model.Rejected("invalid backup")

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

// Keep is a retention: every copy younger than Recent, then the newest copy
// of each of the last Daily UTC days and of the last Monthly UTC months. A
// field left out takes its default.
type Keep struct {
	Recent  string `json:"recent,omitempty" yaml:"recent"`
	Daily   *int   `json:"daily,omitempty" yaml:"daily"`
	Monthly *int   `json:"monthly,omitempty" yaml:"monthly"`
}

// Retention is a Keep with its defaults applied.
type Retention struct {
	Recent  time.Duration
	Daily   int
	Monthly int
}

// Retention resolves k (nil = every default). Callers validate k first;
// an unparsable Recent counts as the default.
func (k *Keep) Retention() Retention {
	r := Retention{Recent: DefaultRecent, Daily: DefaultDaily, Monthly: DefaultMonthly}
	if k == nil {
		return r
	}
	if d, err := time.ParseDuration(k.Recent); err == nil && k.Recent != "" {
		r.Recent = d
	}
	if k.Daily != nil {
		r.Daily = *k.Daily
	}
	if k.Monthly != nil {
		r.Monthly = *k.Monthly
	}
	return r
}

// BackupConfig is one entry of a backups list: a recipe entry (compose
// file) or an override (config.json, which alone may carry Enabled and
// Storage). An override of a recipe backup sets only the fields it changes;
// a backup added in the UI carries Type and every field its type needs.
type BackupConfig struct {
	Name    string   `json:"name" yaml:"name"`
	Type    Type     `json:"type,omitempty" yaml:"type"`
	Enabled bool     `json:"enabled,omitempty" yaml:"-"`
	Storage string   `json:"storage,omitempty" yaml:"-"`
	Every   string   `json:"every,omitempty" yaml:"every"`
	Keep    *Keep    `json:"keep,omitempty" yaml:"keep"`
	Volume  string   `json:"volume,omitempty" yaml:"volume"`
	Service string   `json:"service,omitempty" yaml:"service"`
	Path    string   `json:"path,omitempty" yaml:"path"`
	Paths   []string `json:"paths,omitempty" yaml:"paths"`
	Exclude []string `json:"exclude,omitempty" yaml:"exclude"`
}

// EffectiveBackup is one backup as it runs: the recipe with its override
// layered on top and the defaults applied. FromRecipe names the fields whose
// value comes from the recipe (not overridden); InRecipe is false for a
// backup added in the UI.
type EffectiveBackup struct {
	Name       string          `json:"name"`
	Type       Type            `json:"type"`
	Enabled    bool            `json:"enabled"`
	Storage    string          `json:"storage"`
	Every      string          `json:"every,omitempty"`
	Keep       *Keep           `json:"keep,omitempty"`
	Volume     string          `json:"volume,omitempty"`
	Service    string          `json:"service,omitempty"`
	Path       string          `json:"path,omitempty"`
	Paths      []string        `json:"paths,omitempty"`
	Exclude    []string        `json:"exclude,omitempty"`
	InRecipe   bool            `json:"in_recipe"`
	FromRecipe map[string]bool `json:"from_recipe"`
}

// Interval is the schedule: 0 for a manual backup (and a custom one, which
// winterflow does not run).
func (b EffectiveBackup) Interval() time.Duration {
	if b.Every == "" || b.Every == Manual {
		return 0
	}
	d, _ := time.ParseDuration(b.Every)
	return d
}

// Builtin reports whether winterflow itself runs the backup.
func (b EffectiveBackup) Builtin() bool { return b.Type == TypeSQLite || b.Type == TypeFiles }

var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidateName checks a backup name: like a storage name, lowercase letters,
// digits and inner hyphens, at most 32 characters, so WF_BACKUP_<NAME>_
// variables never collide.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%q is not a valid backup name: use 1-32 lowercase letters, digits and inner hyphens, like database", name)
	}
	return nil
}

// validate checks one effective entry (Type set) against the compose file.
// field prefixes the error ("x-winterflow.backups[0]", "backups[1]").
func validate(c BackupConfig, comp Compose, field string) error {
	if err := ValidateName(c.Name); err != nil {
		return invalid(field+".name", "%v", err)
	}
	if c.Storage != "" && !nameRE.MatchString(c.Storage) {
		return invalid(field+".storage", "%q is not a valid storage name", c.Storage)
	}
	if c.Enabled && c.Storage == "" {
		return invalid(field+".storage", "an enabled backup needs a storage")
	}
	switch c.Type {
	case TypeCustom:
		return validateCustom(c, comp, field)
	case TypeSQLite, TypeFiles:
	case "":
		return invalid(field+".type", "a backup needs a type: sqlite, files or custom")
	default:
		return invalid(field+".type", "unknown type %q: use sqlite, files or custom", c.Type)
	}
	if err := validateEvery(c.Every, field); err != nil {
		return err
	}
	if err := validateKeep(c.Keep, field); err != nil {
		return err
	}
	switch {
	case c.Volume != "" && c.Service != "":
		return invalid(field, "set either volume or service, not both")
	case c.Volume == "" && c.Service == "":
		return invalid(field, "set volume (a volume of the compose file) or service (a service whose mounts hold the data)")
	case c.Volume != "" && !comp.Volumes[c.Volume]:
		return invalid(field+".volume", "%q is not a volume of this compose file", c.Volume)
	case c.Service != "":
		if _, ok := comp.Services[c.Service]; !ok {
			return invalid(field+".service", "%q is not a service of this compose file", c.Service)
		}
	}
	inVolume := c.Volume != ""
	checkPath := func(f, p string) error {
		if inVolume {
			return validateRelPath(f, p)
		}
		return validateAbsPath(f, p)
	}
	if c.Type == TypeSQLite {
		if len(c.Paths) > 0 || len(c.Exclude) > 0 {
			return invalid(field, "a sqlite backup takes path (the database file), not paths or exclude")
		}
		if c.Path == "" {
			return invalid(field+".path", "a sqlite backup needs path: the database file")
		}
		return checkPath(field+".path", c.Path)
	}
	if c.Path != "" {
		return invalid(field+".path", "a files backup takes paths, a list of folders and files")
	}
	if !inVolume && len(c.Paths) == 0 {
		return invalid(field+".paths", "a files backup addressed by service needs paths inside the service's container")
	}
	if len(c.Paths) > maxListLen || len(c.Exclude) > maxListLen {
		return invalid(field, "at most %d paths and %d exclude patterns", maxListLen, maxListLen)
	}
	seen := map[string]bool{}
	for i, p := range c.Paths {
		f := fmt.Sprintf("%s.paths[%d]", field, i)
		if err := checkPath(f, p); err != nil {
			return err
		}
		if seen[p] {
			return invalid(f, "%q is listed twice", p)
		}
		seen[p] = true
	}
	for i, p := range c.Exclude {
		if err := validatePattern(fmt.Sprintf("%s.exclude[%d]", field, i), p); err != nil {
			return err
		}
	}
	return nil
}

func validateCustom(c BackupConfig, comp Compose, field string) error {
	if c.Volume != "" || c.Path != "" || len(c.Paths) > 0 || len(c.Exclude) > 0 || c.Every != "" || c.Keep != nil {
		return invalid(field, "a custom backup takes only service: the service that runs the backup tool")
	}
	if c.Service == "" {
		return invalid(field+".service", "a custom backup needs service: the service that runs the backup tool")
	}
	profiles, ok := comp.Services[c.Service]
	if !ok {
		return invalid(field+".service", "%q is not a service of this compose file", c.Service)
	}
	for _, p := range profiles {
		if p == Profile(c.Name) {
			return nil
		}
	}
	return invalid(field+".service", "service %q must list the profile %q in its profiles, so it runs only while the backup is enabled", c.Service, Profile(c.Name))
}

func validateEvery(every, field string) error {
	if every == "" || every == Manual {
		return nil
	}
	d, err := time.ParseDuration(every)
	if err != nil {
		return invalid(field+".every", "%q is neither a duration like 1h or 30m nor manual", every)
	}
	if d < MinEvery {
		return invalid(field+".every", "%s is too often: at least %s", every, MinEvery)
	}
	return nil
}

func validateKeep(k *Keep, field string) error {
	if k == nil {
		return nil
	}
	if k.Recent != "" {
		if d, err := time.ParseDuration(k.Recent); err != nil || d < 0 {
			return invalid(field+".keep.recent", "%q is not a duration like 24h", k.Recent)
		}
	}
	for _, t := range []struct {
		name string
		v    *int
	}{{"daily", k.Daily}, {"monthly", k.Monthly}} {
		if t.v != nil && (*t.v < 0 || *t.v > maxTier) {
			return invalid(field+".keep."+t.name, "%d is not a count between 0 and %d", *t.v, maxTier)
		}
	}
	return nil
}

// validateRelPath checks a path inside a volume: relative, clean, no "..".
func validateRelPath(field, p string) error {
	if p == "" || len(p) > maxPathLen || hasControl(p) || strings.HasPrefix(p, "/") || path.Clean(p) != p ||
		p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return invalid(field, "%q is not a path inside the volume: use a relative path like app.db or media/uploads (omit paths for the whole volume)", p)
	}
	return nil
}

// validateAbsPath checks a path inside a service's container: absolute and
// clean (so no ".."), not "/".
func validateAbsPath(field, p string) error {
	if len(p) > maxPathLen || hasControl(p) || !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" {
		return invalid(field, "%q is not a path inside the service's container: use an absolute path like /app/media", p)
	}
	return nil
}

// validatePattern checks an exclude glob (path.Match syntax).
func validatePattern(field, p string) error {
	if p == "" || len(p) > maxPathLen || hasControl(p) {
		return invalid(field, "%q is not a pattern", p)
	}
	if _, err := path.Match(strings.Trim(p, "/"), ""); err != nil {
		return invalid(field, "%q is not a valid pattern: %v", p, err)
	}
	return nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
```

**Create `internal/domain/backup/recipe/compose.go`:**

```go
package recipe

import (
	"fmt"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// Compose is what backups need from an app's compose files: the top-level
// volume keys, each service's profiles, and the recipe.
type Compose struct {
	Volumes  map[string]bool
	Services map[string][]string // service name -> profiles
	Recipe   []BackupConfig
}

// recipeKeys are the keys a recipe entry may have. enabled and storage are
// per-deployment settings: a recipe never turns a backup on.
var recipeKeys = map[string]bool{
	"name": true, "type": true, "volume": true, "service": true, "path": true,
	"paths": true, "exclude": true, "every": true, "keep": true,
}

var keepKeys = map[string]bool{"recent": true, "daily": true, "monthly": true}

// ParseCompose reads the compose files in the order compose loads them (the
// main file, then its override). Volumes and services are merged; recipe
// entries of a later file replace those of the same name. A file that is not
// valid YAML contributes nothing: docker compose reports it at deploy. The
// recipe is checked only for its shape here (keys, types, duplicate names);
// Validate checks the values.
func ParseCompose(files ...[]byte) (Compose, error) {
	out := Compose{Volumes: map[string]bool{}, Services: map[string][]string{}}
	for _, raw := range files {
		var doc struct {
			Services map[string]struct {
				Profiles []string `yaml:"profiles"`
			} `yaml:"services"`
			Volumes     map[string]any `yaml:"volumes"`
			XWinterflow yaml.Node      `yaml:"x-winterflow"`
		}
		if yaml.Unmarshal(raw, &doc) != nil {
			continue
		}
		for name := range doc.Volumes {
			out.Volumes[name] = true
		}
		for name, s := range doc.Services {
			out.Services[name] = s.Profiles
		}
		entries, err := parseRecipe(&doc.XWinterflow)
		if err != nil {
			return Compose{}, err
		}
		for _, e := range entries {
			replaced := false
			for i := range out.Recipe {
				if out.Recipe[i].Name == e.Name {
					out.Recipe[i], replaced = e, true
				}
			}
			if !replaced {
				out.Recipe = append(out.Recipe, e)
			}
		}
	}
	return out, nil
}

// parseRecipe decodes x-winterflow.backups. Other x-winterflow keys are left
// to later features.
func parseRecipe(x *yaml.Node) ([]BackupConfig, error) {
	if x.Kind == 0 || x.Tag == "!!null" {
		return nil, nil
	}
	if x.Kind != yaml.MappingNode {
		return nil, invalid("x-winterflow", "must be a mapping")
	}
	var list *yaml.Node
	for i := 0; i+1 < len(x.Content); i += 2 {
		if x.Content[i].Value == "backups" {
			list = x.Content[i+1]
		}
	}
	if list == nil || list.Tag == "!!null" {
		return nil, nil
	}
	if list.Kind != yaml.SequenceNode {
		return nil, invalid("x-winterflow.backups", "must be a list of backups")
	}
	out := make([]BackupConfig, 0, len(list.Content))
	seen := map[string]bool{}
	for i, item := range list.Content {
		field := fmt.Sprintf("x-winterflow.backups[%d]", i)
		if item.Kind != yaml.MappingNode {
			return nil, invalid(field, "must be a mapping with name and type")
		}
		if err := checkKeys(item, recipeKeys, field); err != nil {
			return nil, err
		}
		var c BackupConfig
		if err := item.Decode(&c); err != nil {
			return nil, invalid(field, "%s", yamlError(err))
		}
		if c.Keep != nil {
			for j := 0; j+1 < len(item.Content); j += 2 {
				if item.Content[j].Value == "keep" {
					if err := checkKeys(item.Content[j+1], keepKeys, field+".keep"); err != nil {
						return nil, err
					}
				}
			}
		}
		if err := ValidateName(c.Name); err != nil {
			return nil, invalid(field+".name", "%v", err)
		}
		if seen[c.Name] {
			return nil, invalid(field+".name", "%q is used by another backup", c.Name)
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	return out, nil
}

// checkKeys refuses keys outside allowed in a mapping node.
func checkKeys(m *yaml.Node, allowed map[string]bool, field string) error {
	if m.Kind != yaml.MappingNode {
		return invalid(field, "must be a mapping")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i].Value
		if allowed[k] {
			continue
		}
		if k == "enabled" || k == "storage" {
			return invalid(field+"."+k, "a recipe never turns a backup on: enable it and pick its storage in the app's backups")
		}
		return invalid(field+"."+k, "unknown key")
	}
	return nil
}

// yamlError drops the "yaml: unmarshal errors:" preamble.
func yamlError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:")
	return strings.Join(strings.Fields(msg), " ")
}
```

**Create `internal/domain/backup/recipe/effective.go`:**

```go
package recipe

import (
	"fmt"
	"slices"
)

// Field names of FromRecipe.
const (
	FieldEvery   = "every"
	FieldKeep    = "keep"
	FieldVolume  = "volume"
	FieldService = "service"
	FieldPath    = "path"
	FieldPaths   = "paths"
	FieldExclude = "exclude"
)

// Effective layers the overrides on the compose file's recipe and applies
// the defaults: every recipe backup (off unless its override enables it),
// then the backups added in the UI, in override order. An override naming
// a backup the recipe no longer has and carrying no type is stale and
// skipped (Clean drops it at the next save). The result is validated; the
// error names the override's field when the entry has one, else the
// recipe's.
func Effective(comp Compose, overrides []BackupConfig) ([]EffectiveBackup, error) {
	if err := Validate(comp); err != nil {
		return nil, err
	}
	byName, err := indexOverrides(overrides)
	if err != nil {
		return nil, err
	}
	out := make([]EffectiveBackup, 0, len(comp.Recipe)+len(overrides))
	inRecipe := map[string]bool{}
	for i, r := range comp.Recipe {
		inRecipe[r.Name] = true
		field := fmt.Sprintf("x-winterflow.backups[%d]", i)
		c := r
		if j, ok := byName[r.Name]; ok {
			field = fmt.Sprintf("backups[%d]", j)
			o := overrides[j]
			if o.Type != "" && o.Type != r.Type {
				return nil, invalid(field+".type", "%s is a %s backup in the compose file: its type can't change", r.Name, r.Type)
			}
			c = merge(r, o)
		}
		if err := validate(c, comp, field); err != nil {
			return nil, err
		}
		out = append(out, finish(c, true, fromRecipe(r, c)))
	}
	for j, o := range overrides {
		if inRecipe[o.Name] || o.Type == "" {
			continue
		}
		if err := validate(o, comp, fmt.Sprintf("backups[%d]", j)); err != nil {
			return nil, err
		}
		out = append(out, finish(o, false, map[string]bool{}))
	}
	return out, nil
}

// Validate checks every recipe entry on its own, defaults applied (off, no
// storage).
func Validate(comp Compose) error {
	for i, r := range comp.Recipe {
		if err := validate(r, comp, fmt.Sprintf("x-winterflow.backups[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// Check is the strict form for a backups list a user submits: on top of
// Effective, an entry naming no recipe backup must carry its type (it is a
// new backup, not an override of one).
func Check(comp Compose, overrides []BackupConfig) ([]EffectiveBackup, error) {
	names := map[string]bool{}
	for _, r := range comp.Recipe {
		names[r.Name] = true
	}
	for j, o := range overrides {
		if !names[o.Name] && o.Type == "" {
			return nil, invalid(fmt.Sprintf("backups[%d]", j), "the compose file has no backup %q: a new backup needs its type", o.Name)
		}
	}
	return Effective(comp, overrides)
}

// Clean is the backups list to store: stale overrides (a name the recipe no
// longer has, without type) are dropped with a warning; an override of a
// recipe backup keeps only the fields that differ from the recipe (and no
// type); a disabled entry left with nothing to say is dropped.
func Clean(comp Compose, overrides []BackupConfig) (kept []BackupConfig, warnings []string) {
	recipe := map[string]BackupConfig{}
	for _, r := range comp.Recipe {
		recipe[r.Name] = r
	}
	kept = []BackupConfig{}
	for _, o := range overrides {
		r, ok := recipe[o.Name]
		if !ok {
			if o.Type == "" {
				warnings = append(warnings, fmt.Sprintf("the compose file no longer has the backup %q: its settings were dropped", o.Name))
				continue
			}
			kept = append(kept, o)
			continue
		}
		if m := minimize(r, o); !m.empty() {
			kept = append(kept, m)
		}
	}
	return kept, warnings
}

// indexOverrides maps names to positions, refusing duplicates and bad names.
func indexOverrides(overrides []BackupConfig) (map[string]int, error) {
	out := map[string]int{}
	for j, o := range overrides {
		if err := ValidateName(o.Name); err != nil {
			return nil, invalid(fmt.Sprintf("backups[%d].name", j), "%v", err)
		}
		if _, dup := out[o.Name]; dup {
			return nil, invalid(fmt.Sprintf("backups[%d].name", j), "%q is listed twice", o.Name)
		}
		out[o.Name] = j
	}
	return out, nil
}

// merge is the recipe entry r with o's fields on top. Setting volume or
// service moves the backup to that addressing (the other one is cleared).
func merge(r, o BackupConfig) BackupConfig {
	c := r
	c.Enabled, c.Storage = o.Enabled, o.Storage
	if o.Every != "" {
		c.Every = o.Every
	}
	if o.Keep != nil {
		c.Keep = o.Keep
	}
	if o.Volume != "" || o.Service != "" {
		c.Volume, c.Service = o.Volume, o.Service
	}
	if o.Path != "" {
		c.Path = o.Path
	}
	if o.Paths != nil {
		c.Paths = o.Paths
	}
	if o.Exclude != nil {
		c.Exclude = o.Exclude
	}
	return c
}

// minimize keeps of o what differs from the recipe entry r.
func minimize(r, o BackupConfig) BackupConfig {
	m := BackupConfig{Name: o.Name, Enabled: o.Enabled, Storage: o.Storage}
	if o.Every != "" && o.Every != r.Every {
		m.Every = o.Every
	}
	if o.Keep != nil && !keepEqual(o.Keep, r.Keep) {
		m.Keep = o.Keep
	}
	if (o.Volume != "" || o.Service != "") && (o.Volume != r.Volume || o.Service != r.Service) {
		m.Volume, m.Service = o.Volume, o.Service
	}
	if o.Path != "" && o.Path != r.Path {
		m.Path = o.Path
	}
	if o.Paths != nil && !slices.Equal(o.Paths, r.Paths) {
		m.Paths = o.Paths
	}
	if o.Exclude != nil && !slices.Equal(o.Exclude, r.Exclude) {
		m.Exclude = o.Exclude
	}
	return m
}

// empty reports whether an override says nothing beyond its name.
func (c BackupConfig) empty() bool {
	return !c.Enabled && c.Storage == "" && c.Type == "" && c.Every == "" && c.Keep == nil &&
		c.Volume == "" && c.Service == "" && c.Path == "" && c.Paths == nil && c.Exclude == nil
}

func keepEqual(a, b *Keep) bool {
	return a.Retention() == b.Retention()
}

// fromRecipe names the fields of c that hold the recipe's value.
func fromRecipe(r, c BackupConfig) map[string]bool {
	from := map[string]bool{}
	set := func(name string, recipeHas, same bool) {
		if recipeHas && same {
			from[name] = true
		}
	}
	set(FieldEvery, r.Every != "", c.Every == r.Every)
	set(FieldKeep, r.Keep != nil, c.Keep == r.Keep)
	set(FieldVolume, r.Volume != "", c.Volume == r.Volume)
	set(FieldService, r.Service != "", c.Service == r.Service)
	set(FieldPath, r.Path != "", c.Path == r.Path)
	set(FieldPaths, r.Paths != nil, slices.Equal(c.Paths, r.Paths))
	set(FieldExclude, r.Exclude != nil, slices.Equal(c.Exclude, r.Exclude))
	return from
}

// finish turns a validated entry into its effective form: every and keep
// defaulted for the built-in types.
func finish(c BackupConfig, inRecipe bool, from map[string]bool) EffectiveBackup {
	b := EffectiveBackup{
		Name: c.Name, Type: c.Type, Enabled: c.Enabled, Storage: c.Storage,
		Volume: c.Volume, Service: c.Service, Path: c.Path, Paths: c.Paths, Exclude: c.Exclude,
		InRecipe: inRecipe, FromRecipe: from,
	}
	if c.Type != TypeCustom {
		b.Every = c.Every
		if b.Every == "" {
			b.Every = DefaultEvery
		}
		r := c.Keep.Retention()
		daily, monthly := r.Daily, r.Monthly
		b.Keep = &Keep{Recent: r.Recent.String(), Daily: &daily, Monthly: &monthly}
	}
	return b
}

// Declared maps the effective backups by name: what WF_BACKUP_ references
// resolve against.
func Declared(backups []EffectiveBackup) map[string]EffectiveBackup {
	out := make(map[string]EffectiveBackup, len(backups))
	for _, b := range backups {
		out[b.Name] = b
	}
	return out
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -cover ./internal/domain/model/ ./internal/domain/backup/recipe/`

Expected: PASS; recipe coverage ≈ 97%.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add go.mod internal/domain/model/errors.go internal/domain/model/errors_test.go internal/domain/backup/recipe
git commit -F - <<'EOF'
feat(domain): backup recipe (x-winterflow.backups) parsing, validation and overrides

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 3: WF_BACKUP_ variables and the backup command catalog

**Files:**
- Modify: `internal/domain/model/storage.go` (`StorageRef.Backup`, `BackupVarName`, `ParseStorageRef` parses `WF_BACKUP_`, `CheckStorageRefs` gains `backupDeclared`, `BackupFieldValue`), `storage_test.go`
- Create: `internal/domain/command/backup.go`, `backup_test.go`
- Modify: `internal/domain/command/command.go` (4 types, `Type.Detached`), `app.go` (`AppStatus.Backups`, `GetAppResponse.Backups/BackupsError`), `storage.go` (`StorageVariable.Backup`)
- Modify (callers keep their behaviour): `internal/domain/usecase/app/storages.go`, `internal/infra/orchestrator/docker_compose/storageenv.go`, `storages.go`, `storageenv_test.go`
- Regenerate: `api/openapi.yaml`

**Interfaces:**
- Consumes: `recipe.EffectiveBackup`, `recipe.BackupConfig`, `recipe.ValidateName` (Task 2).
- Produces:
  - `model.StorageRef{Var, Storage, Backup, Field string}` (exactly one of Storage/Backup set); `model.BackupVarName(name, field string) string`; `model.CheckStorageRefs(refs []string, exists func(string) bool, backupDeclared func(string) bool) ([]StorageRef, error)` (nil `backupDeclared` declares none); `model.BackupFieldValue(s Storage, field, secret, appID, backup string) string` (PREFIX = `<storage prefix><app-id>/<backup>/`)
  - `command.TypeBackupUpdate = "backup.update"`, `TypeBackupPoints = "backup.points"`, `TypeBackupRun = "backup.run"`, `TypeBackupDeletePoint = "backup.delete-point"`; `(command.Type).Detached() bool` (true only for `backup.run`)
  - `command.BackupState` + `BackupOff|BackupRunning|BackupNever|BackupOK|BackupLate|BackupFailing`; `command.BackupHealth{Name, State, LastSuccess int64, LastError}`; `command.PointKind` + `PointScheduled|PointManual|PointPreRestore`; `command.RestorePoint{ID, Time, Kind, Size, Expires}`; `command.ValidPointID(id) bool`
  - `command.UpdateBackupsRequest{AppID; Backups []recipe.BackupConfig}` / `UpdateBackupsResponse{AppID, Revision; Redeployed bool; Backups []recipe.EffectiveBackup; Warnings}`; `ListRestorePointsRequest{AppID, Backup}` / `ListRestorePointsResponse{AppID, Backup, Points}`; `RunBackupRequest{AppID, Backup}` / `RunBackupResponse{AppID, Backup; Unchanged bool; Point *RestorePoint; Warnings}`; `DeleteRestorePointRequest{AppID, Backup, Point}` / `DeleteRestorePointResponse{AppID, Backup, Point}` — every request has `Validate()`
  - `command.AppStatus.Backups []BackupHealth` (`backups,omitempty`); `command.GetAppResponse.Backups []recipe.EffectiveBackup`, `.BackupsError string`; `command.StorageVariable.Backup string` (`backup,omitempty`)

- [ ] **Step 1: Write the failing tests**

**In `internal/domain/model/storage_test.go`, replace:**

```go
		"WF_STORAGE_HETZNER_SECRET_ACCESS_KEY": {Storage: "hetzner", Field: FieldSecretAccessKey},
	}
```

**with:**

```go
		"WF_STORAGE_HETZNER_SECRET_ACCESS_KEY": {Storage: "hetzner", Field: FieldSecretAccessKey},
		"WF_BACKUP_DB_PATH":                    {Backup: "db", Field: FieldPath},
		"WF_BACKUP_MEDIA_FILES_PREFIX":         {Backup: "media-files", Field: FieldPrefix},
	}
```

**In `internal/domain/model/storage_test.go`, replace:**

```go
		"WF_BACKUP_DB_PATH":        `there is no backup "db" in this app`,
		"WF_BACKUP_MEDIA_FILES":    `there is no backup "media-files" in this app`,
```

**with:**

```go
		"WF_BACKUP_MEDIA_FILES":    "is not a backup variable",
		"WF_BACKUP__X_TYPE":        "is not a backup variable",
```

**In `internal/domain/model/storage_test.go`, replace:**

```go
	refs, err := CheckStorageRefs([]string{"WF_STORAGE_HETZNER_BUCKET", "WF_STORAGE_HETZNER_TYPE"}, exists)
	if err != nil || len(refs) != 2 || refs[0].Field != FieldBucket {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	_, err = CheckStorageRefs([]string{"WF_STORAGE_HETZNER_BUCKET", "WF_STORAGE_GONE_PATH"}, exists)
	if !errors.Is(err, ErrStorageRef) || err.Error() != `unresolved storage variable: ${WF_STORAGE_GONE_PATH}: there is no storage "gone" on this server` {
		t.Fatalf("err = %v", err)
	}
	if _, err := CheckStorageRefs([]string{"WF_BACKUP_DB_PATH"}, exists); !errors.Is(err, ErrStorageRef) {
		t.Fatalf("backup ref: %v", err)
	}
```

**with:**

```go
	refs, err := CheckStorageRefs([]string{"WF_STORAGE_HETZNER_BUCKET", "WF_STORAGE_HETZNER_TYPE"}, exists, nil)
	if err != nil || len(refs) != 2 || refs[0].Field != FieldBucket {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	_, err = CheckStorageRefs([]string{"WF_STORAGE_HETZNER_BUCKET", "WF_STORAGE_GONE_PATH"}, exists, nil)
	if !errors.Is(err, ErrStorageRef) || err.Error() != `unresolved storage variable: ${WF_STORAGE_GONE_PATH}: there is no storage "gone" on this server` {
		t.Fatalf("err = %v", err)
	}
	// WF_BACKUP_ references resolve against the app's declared backups,
	// whatever storages exist.
	_, err = CheckStorageRefs([]string{"WF_BACKUP_DB_PATH"}, exists, nil)
	if !errors.Is(err, ErrStorageRef) || !strings.Contains(err.Error(), `${WF_BACKUP_DB_PATH}: there is no backup "db" in this app`) {
		t.Fatalf("undeclared backup: %v", err)
	}
	declared := func(n string) bool { return n == "db" }
	refs, err = CheckStorageRefs([]string{"WF_BACKUP_DB_PATH", "WF_STORAGE_HETZNER_TYPE"}, exists, declared)
	if err != nil || len(refs) != 2 || refs[0].Backup != "db" || refs[0].Storage != "" {
		t.Fatalf("declared backup: %+v, %v", refs, err)
	}
	if _, err := CheckStorageRefs([]string{"WF_BACKUP_MEDIA_PATH"}, exists, declared); err == nil {
		t.Fatal("another backup passed")
	}
```

**In `internal/domain/model/storage_test.go`, replace:**

```go
	if got := StorageVarName("backup-disk", FieldPath); got != "WF_STORAGE_BACKUP_DISK_PATH" {
		t.Fatalf("var name = %q", got)
	}
```

**with:**

```go
	if got := StorageVarName("backup-disk", FieldPath); got != "WF_STORAGE_BACKUP_DISK_PATH" {
		t.Fatalf("var name = %q", got)
	}
	if got := BackupVarName("media-files", FieldPrefix); got != "WF_BACKUP_MEDIA_FILES_PREFIX" {
		t.Fatalf("backup var name = %q", got)
	}
	// A backup's PREFIX is its own folder in the storage; other fields are
	// the storage's.
	for _, tc := range []struct {
		s           Storage
		field, want string
	}{
		{s, FieldPrefix, "p/app-1/db/"}, {l, FieldPrefix, "app-1/db/"}, {s, FieldBucket, "backups"},
		{s, FieldSecretAccessKey, "plain"}, {l, FieldPath, "/mnt/b"},
	} {
		if got := BackupFieldValue(tc.s, tc.field, "plain", "app-1", "db"); got != tc.want {
			t.Errorf("backup %s %s = %q, want %q", tc.s.Type, tc.field, got, tc.want)
		}
	}
```

**Create `internal/domain/command/backup_test.go`:**

```go
package command

import (
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
)

func TestBackupRequestsValidate(t *testing.T) {
	ok := []interface{ Validate() error }{
		UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "s3"}, {Name: "media"}}},
		UpdateBackupsRequest{AppID: "a1"},
		ListRestorePointsRequest{AppID: "a1", Backup: "db"},
		RunBackupRequest{AppID: "a1", Backup: "db"},
		DeleteRestorePointRequest{AppID: "a1", Backup: "db", Point: "20261011T120000Z-manual"},
	}
	for _, r := range ok {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	for _, tc := range []struct {
		r    interface{ Validate() error }
		want string
	}{
		{UpdateBackupsRequest{AppID: ".."}, "invalid app id"},
		{UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "DB"}}}, "backups[0].name"},
		{UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db"}, {Name: "db"}}}, `backups[1].name: "db" is listed twice`},
		{UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db", Enabled: true}}}, "backups[0].storage: an enabled backup needs a storage"},
		{ListRestorePointsRequest{AppID: "a/b", Backup: "db"}, "invalid app id"},
		{RunBackupRequest{AppID: "a1", Backup: ""}, "is not a valid backup name"},
		{DeleteRestorePointRequest{AppID: "a1", Backup: "db", Point: "../x"}, "invalid restore point id"},
	} {
		if err := tc.r.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.r, err, tc.want)
		}
	}
}

func TestValidPointID(t *testing.T) {
	for _, id := range []string{"20261011T120000Z-scheduled", "20261011T120000Z-manual", "20261011T120000Z-pre-restore"} {
		if !ValidPointID(id) {
			t.Errorf("%s should be valid", id)
		}
	}
	for _, id := range []string{"", "20261011T120000Z", "20261011T120000Z-other", "2026101aT120000Z-manual", "20261011X120000Z-manual",
		"20261011T120000Y-manual", "20261011T120000Z_manual", "../20261011T120000Z-manual"} {
		if ValidPointID(id) {
			t.Errorf("%s should be invalid", id)
		}
	}
}

func TestDetached(t *testing.T) {
	if !TypeBackupRun.Detached() || TypeBackupPoints.Detached() || TypeAppSave.Detached() {
		t.Fatal("only backup.run waits for long work")
	}
}
```

**In `internal/infra/orchestrator/docker_compose/storageenv_test.go`, replace:**

```go
	want := []command.StorageVariable{
		{Name: "WF_STORAGE_BACKUP_DISK_PATH", Storage: "backup-disk"},
```

**with:**

```go
	want := []command.StorageVariable{
		{Name: "WF_BACKUP_DB_PATH", Backup: "db"},
		{Name: "WF_STORAGE_BACKUP_DISK_PATH", Storage: "backup-disk"},
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/domain/model/ ./internal/domain/command/`

Expected: build failure: `too many arguments in call to CheckStorageRefs`, `undefined: BackupVarName`, `undefined: UpdateBackupsRequest`, ….

- [ ] **Step 3: Implement**

**In `internal/domain/model/storage.go`, replace:**

```go
// StorageVarName is the variable for field of the storage named name.
func StorageVarName(name, field string) string {
	return storageVarPrefix + varToken(name) + "_" + field
}
```

**with:**

```go
// StorageVarName is the variable for field of the storage named name.
func StorageVarName(name, field string) string {
	return storageVarPrefix + varToken(name) + "_" + field
}

// BackupVarName is the variable for field of the storage the app's backup
// named name uses.
func BackupVarName(name, field string) string {
	return backupVarPrefix + varToken(name) + "_" + field
}
```

**In `internal/domain/model/storage.go`, replace:**

```go
// StorageRef is one parsed WF_STORAGE_<STORAGE>_<FIELD> reference.
type StorageRef struct {
	Var     string // the variable name, e.g. WF_STORAGE_BACKUP_DISK_PATH
	Storage string // the storage name, e.g. backup-disk
	Field   string // e.g. PATH
}
```

**with:**

```go
// StorageRef is one parsed WF_STORAGE_<STORAGE>_<FIELD> or
// WF_BACKUP_<BACKUP>_<FIELD> reference: exactly one of Storage and Backup
// is set.
type StorageRef struct {
	Var     string // the variable name, e.g. WF_STORAGE_BACKUP_DISK_PATH
	Storage string // the storage name, e.g. backup-disk
	Backup  string // the backup name of a WF_BACKUP_ reference, e.g. database
	Field   string // e.g. PATH
}
```

**In `internal/domain/model/storage.go`, replace:**

```go
// ParseStorageRef splits a referenced variable into storage and field. A
// WF_BACKUP_ reference names a backup of the app; there are none until
// backups exist (C2), so it is refused like any undeclared backup.
func ParseStorageRef(v string) (StorageRef, error) {
	rest, isStorage := strings.CutPrefix(v, storageVarPrefix)
	if !isStorage {
		if backup, ok := strings.CutPrefix(v, backupVarPrefix); ok {
			if token, _, ok := splitField(backup); ok {
				backup = token
			}
			return StorageRef{}, fmt.Errorf("%w: ${%s}: there is no backup %q in this app", ErrStorageRef, v, tokenName(backup))
		}
		return StorageRef{}, fmt.Errorf("%w: %s is not a storage variable", ErrStorageRef, v)
	}
```

**with:**

```go
// ParseStorageRef splits a referenced variable into storage (WF_STORAGE_)
// or backup (WF_BACKUP_) and field.
func ParseStorageRef(v string) (StorageRef, error) {
	if rest, ok := strings.CutPrefix(v, backupVarPrefix); ok {
		token, field, ok := splitField(rest)
		name := tokenName(token)
		if !ok || ValidateStorageName(name) != nil {
			return StorageRef{}, fmt.Errorf("%w: ${%s} is not a backup variable: use WF_BACKUP_<BACKUP>_<FIELD> with FIELD one of %s",
				ErrStorageRef, v, strings.Join(storageFields, ", "))
		}
		return StorageRef{Var: v, Backup: name, Field: field}, nil
	}
	rest, isStorage := strings.CutPrefix(v, storageVarPrefix)
	if !isStorage {
		return StorageRef{}, fmt.Errorf("%w: %s is not a storage variable", ErrStorageRef, v)
	}
```

**In `internal/domain/model/storage.go`, replace:**

```go
// CheckStorageRefs parses every reference and requires its storage to
// exist; the first problem is returned (wrapping ErrStorageRef).
func CheckStorageRefs(refs []string, exists func(name string) bool) ([]StorageRef, error) {
	out := make([]StorageRef, 0, len(refs))
	for _, v := range refs {
		ref, err := ParseStorageRef(v)
		if err != nil {
			return nil, err
		}
		if !exists(ref.Storage) {
			return nil, fmt.Errorf("%w: ${%s}: there is no storage %q on this server", ErrStorageRef, v, ref.Storage)
		}
		out = append(out, ref)
	}
	return out, nil
}
```

**with:**

```go
// CheckStorageRefs parses every reference: a WF_STORAGE_ one must name a
// storage that exists, a WF_BACKUP_ one a backup the app declares
// (backupDeclared; nil declares none). The first problem is returned
// (wrapping ErrStorageRef).
func CheckStorageRefs(refs []string, exists func(name string) bool, backupDeclared func(name string) bool) ([]StorageRef, error) {
	out := make([]StorageRef, 0, len(refs))
	for _, v := range refs {
		ref, err := ParseStorageRef(v)
		if err != nil {
			return nil, err
		}
		if ref.Backup != "" {
			if backupDeclared == nil || !backupDeclared(ref.Backup) {
				return nil, fmt.Errorf("%w: ${%s}: there is no backup %q in this app", ErrStorageRef, v, ref.Backup)
			}
		} else if !exists(ref.Storage) {
			return nil, fmt.Errorf("%w: ${%s}: there is no storage %q on this server", ErrStorageRef, v, ref.Storage)
		}
		out = append(out, ref)
	}
	return out, nil
}
```

**In `internal/domain/model/storage.go`, replace:**

```go
// ReservedVariableError refuses a user variable named like a storage
```

**with:**

```go
// BackupFieldValue is the value of field for the app's backup named backup,
// stored in s: the storage's values, with PREFIX narrowed to the backup's
// own folder, <storage prefix><app-id>/<backup>/.
func BackupFieldValue(s Storage, field, secret, appID, backup string) string {
	if field == FieldPrefix {
		return StorageFieldValue(s, FieldPrefix, "") + appID + "/" + backup + "/"
	}
	return StorageFieldValue(s, field, secret)
}

// ReservedVariableError refuses a user variable named like a storage
```

**Create `internal/domain/command/backup.go`:**

```go
package command

import (
	"errors"
	"fmt"

	"winterflow/internal/domain/backup/recipe"
)

// Backups: the agent runs an app's enabled sqlite and files backups on
// schedule (or on request) and stores each copy — a restore point — in the
// storage the backup names, under <storage prefix><app-id>/<backup>/.

// BackupState is a backup's health.
type BackupState string

const (
	BackupOff     BackupState = "off"     // disabled
	BackupRunning BackupState = "running" // a run is in progress
	BackupNever   BackupState = "never"   // enabled, not run yet
	BackupOK      BackupState = "ok"      // the last run succeeded (within 2 × every when scheduled)
	BackupLate    BackupState = "late"    // scheduled, no success within 2 × every
	BackupFailing BackupState = "failing" // the last attempt failed
)

// BackupHealth is one backup of an app in apps.status. LastSuccess is unix
// seconds (0 = never).
type BackupHealth struct {
	Name        string      `json:"name"`
	State       BackupState `json:"state"`
	LastSuccess int64       `json:"last_success,omitempty"`
	LastError   string      `json:"last_error,omitempty"`
}

// PointKind is why a restore point was made.
type PointKind string

const (
	PointScheduled  PointKind = "scheduled"
	PointManual     PointKind = "manual"
	PointPreRestore PointKind = "pre-restore"
)

// RestorePoint is one stored copy of a backup. ID names it in
// delete-point (and restore); Time is unix seconds; Size is the stored
// (compressed) size in bytes; Expires is when BACKUP_RETENTION_DAYS deletes
// it (unix seconds, 0 = never).
type RestorePoint struct {
	ID      string    `json:"id"`
	Time    int64     `json:"time"`
	Kind    PointKind `json:"kind"`
	Size    int64     `json:"size"`
	Expires int64     `json:"expires,omitempty"`
}

// validateBackupRef checks the app id and backup name of a backup-scoped
// request.
func validateBackupRef(appID, backup string) error {
	if err := validateAppID(appID); err != nil {
		return err
	}
	return recipe.ValidateName(backup)
}

// --- backup.update ----------------------------------------------------------

// UpdateBackupsRequest replaces an app's backups list (its overrides) as a
// whole; the agent validates it against the compose file's recipe and the
// server's storages, commits it as a new revision, and redeploys only when
// the compose profiles or the storage variables change.
type UpdateBackupsRequest struct {
	AppID   string                `json:"app_id"`
	Backups []recipe.BackupConfig `json:"backups"`
}

func (r UpdateBackupsRequest) Validate() error {
	if err := validateAppID(r.AppID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, b := range r.Backups {
		if err := recipe.ValidateName(b.Name); err != nil {
			return fmt.Errorf("backups[%d].name: %w", i, err)
		}
		if seen[b.Name] {
			return fmt.Errorf("backups[%d].name: %q is listed twice", i, b.Name)
		}
		seen[b.Name] = true
		if b.Enabled && b.Storage == "" {
			return fmt.Errorf("backups[%d].storage: an enabled backup needs a storage", i)
		}
	}
	return nil
}

// UpdateBackupsResponse is the new revision and the effective backups.
// Redeployed tells whether the app was brought up again; Warnings are
// overrides dropped and redeploys skipped or failed.
type UpdateBackupsResponse struct {
	AppID      string                   `json:"app_id"`
	Revision   string                   `json:"revision"`
	Redeployed bool                     `json:"redeployed"`
	Backups    []recipe.EffectiveBackup `json:"backups"`
	Warnings   []string                 `json:"warnings,omitempty"`
}

// --- backup.points ----------------------------------------------------------

// ListRestorePointsRequest lists a backup's complete restore points.
type ListRestorePointsRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
}

func (r ListRestorePointsRequest) Validate() error { return validateBackupRef(r.AppID, r.Backup) }

// ListRestorePointsResponse lists the points newest first ([] for none).
type ListRestorePointsResponse struct {
	AppID  string         `json:"app_id"`
	Backup string         `json:"backup"`
	Points []RestorePoint `json:"points"`
}

// --- backup.run -------------------------------------------------------------

// RunBackupRequest runs a backup now (a manual copy). It joins a run of the
// same backup that is already running or queued.
type RunBackupRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
}

func (r RunBackupRequest) Validate() error { return validateBackupRef(r.AppID, r.Backup) }

// RunBackupResponse is the outcome of the run. Unchanged means the data had
// not changed since the last copy, so nothing was uploaded; otherwise Point
// is the new restore point.
type RunBackupResponse struct {
	AppID     string        `json:"app_id"`
	Backup    string        `json:"backup"`
	Unchanged bool          `json:"unchanged"`
	Point     *RestorePoint `json:"point,omitempty"`
	Warnings  []string      `json:"warnings,omitempty"`
}

// --- backup.delete-point ----------------------------------------------------

// DeleteRestorePointRequest deletes one restore point (any kind) and its
// manifest.
type DeleteRestorePointRequest struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
	Point  string `json:"point"`
}

func (r DeleteRestorePointRequest) Validate() error {
	if err := validateBackupRef(r.AppID, r.Backup); err != nil {
		return err
	}
	if !ValidPointID(r.Point) {
		return errors.New("invalid restore point id")
	}
	return nil
}

type DeleteRestorePointResponse struct {
	AppID  string `json:"app_id"`
	Backup string `json:"backup"`
	Point  string `json:"point"`
}

// ValidPointID reports whether id names a restore point:
// <yyyymmddThhmmssZ>-<kind>.
func ValidPointID(id string) bool {
	if len(id) < 17 || id[8] != 'T' || id[15] != 'Z' || id[16] != '-' {
		return false
	}
	for i, c := range id[:15] {
		if i != 8 && (c < '0' || c > '9') {
			return false
		}
	}
	switch PointKind(id[17:]) {
	case PointScheduled, PointManual, PointPreRestore:
		return true
	}
	return false
}
```

**In `internal/domain/command/command.go`, replace:**

```go
	// Container images.
```

**with:**

```go
	// Backups.
	TypeBackupUpdate      Type = "backup.update"       // replace an app's backups list (overrides)
	TypeBackupPoints      Type = "backup.points"       // list a backup's restore points
	TypeBackupRun         Type = "backup.run"          // run a backup now; answers when the run ends
	TypeBackupDeletePoint Type = "backup.delete-point" // delete one restore point

	// Container images.
```

**In `internal/domain/command/command.go`, replace:**

```go
// ContentTypeJSON is the envelope content type used for every command payload.
```

**with:**

```go
// Detached reports whether a command's answer waits for long work that runs
// elsewhere (backup.run waits for its run on the backup scheduler). Such a
// command is dispatched off the standalone bridge's single goroutine, so it
// never holds up other commands.
func (t Type) Detached() bool { return t == TypeBackupRun }

// ContentTypeJSON is the envelope content type used for every command payload.
```

**In `internal/domain/command/app.go`, replace:**

```go
import (
	"fmt"
	"strings"

	"winterflow/internal/domain/model"
)
```

**with:**

```go
import (
	"fmt"
	"strings"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/model"
)
```

**In `internal/domain/command/app.go`, replace:**

```go
	StorageVariables []StorageVariable `json:"storage_variables,omitempty"`
}
```

**with:**

```go
	StorageVariables []StorageVariable `json:"storage_variables,omitempty"`
	// Backups are the app's effective backups (recipe + overrides);
	// BackupsError says why they could not be read (an invalid recipe).
	Backups      []recipe.EffectiveBackup `json:"backups,omitempty"`
	BackupsError string                   `json:"backups_error,omitempty"`
}
```

**In `internal/domain/command/app.go`, replace:**

```go
type AppStatus struct {
	AppID      string              `json:"app_id"`
	StatusCode ContainerStatusCode `json:"status_code"`
	Containers []ContainerStatus   `json:"containers"`
}
```

**with:**

```go
type AppStatus struct {
	AppID      string              `json:"app_id"`
	StatusCode ContainerStatusCode `json:"status_code"`
	Containers []ContainerStatus   `json:"containers"`
	// Backups is the health of each of the app's backups (none: omitted).
	// It never changes StatusCode.
	Backups []BackupHealth `json:"backups,omitempty"`
}
```

**In `internal/domain/command/storage.go`, replace:**

```go
// StorageVariable is a WF_STORAGE_ variable an app's compose file references,
// as app.get reports it for the Variables panel: never with a value.
type StorageVariable struct {
	Name    string `json:"name"`
	Storage string `json:"storage"`
	Secret  bool   `json:"secret"`
}
```

**with:**

```go
// StorageVariable is a WF_STORAGE_ or WF_BACKUP_ variable an app's compose
// file references, as app.get reports it for the Variables panel: never
// with a value. Backup names the backup of a WF_BACKUP_ variable (Storage
// is then the storage that backup uses, "" while it has none).
type StorageVariable struct {
	Name    string `json:"name"`
	Storage string `json:"storage"`
	Backup  string `json:"backup,omitempty"`
	Secret  bool   `json:"secret"`
}
```

**In `internal/domain/usecase/app/storages.go`, replace:**

```go
	_, err = model.CheckStorageRefs(refs, func(name string) bool { return have[name] })
```

**with:**

```go
	_, err = model.CheckStorageRefs(refs, func(name string) bool { return have[name] }, nil)
```

**In `internal/infra/orchestrator/docker_compose/storageenv.go`, replace:**

```go
		return ok
	})
	if readErr != nil {
```

**with:**

```go
		return ok
	}, nil)
	if readErr != nil {
```

**In `internal/infra/orchestrator/docker_compose/storageenv.go`, replace:**

```go
		out = append(out, command.StorageVariable{Name: ref.Var, Storage: ref.Storage, Secret: ref.Secret()})
```

**with:**

```go
		out = append(out, command.StorageVariable{Name: ref.Var, Storage: ref.Storage, Backup: ref.Backup, Secret: ref.Secret()})
```

**In `internal/infra/orchestrator/docker_compose/storages.go`, replace:**

```go
			if err != nil || seen[ref.Storage] {
```

**with:**

```go
			if err != nil || ref.Storage == "" || seen[ref.Storage] {
```

- [ ] **Step 4: Regenerate the OpenAPI document**

```bash
make openapi
```

Expected: `api/openapi.yaml` gains the `BackupHealth`, `EffectiveBackup` and `Keep` schemas, `AppStatus.backups`, `GetAppResponse.backups`/`backups_error` and `StorageVariable.backup`.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/domain/... ./internal/infra/orchestrator/docker_compose/ ./internal/app/web/`

Expected: PASS (`TestOpenAPIUpToDate` included). `WF_BACKUP_` references are still refused everywhere: every caller passes `nil` declarations until Tasks 6 and 11.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/domain/model/storage_test.go internal/domain/command/backup_test.go internal/infra/orchestrator/docker_compose/storageenv_test.go internal/domain/model/storage.go internal/domain/command/backup.go internal/domain/command/command.go internal/domain/command/app.go internal/domain/command/storage.go internal/domain/usecase/app/storages.go internal/infra/orchestrator/docker_compose/storageenv.go internal/infra/orchestrator/docker_compose/storages.go api/openapi.yaml
git commit -F - <<'EOF'
feat(domain): WF_BACKUP_ variables and the backup command catalog

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 4: Storage layout, restore points, retention and BACKUP_RETENTION_DAYS

**Files:**
- Create: `internal/infra/backup/layout/layout.go`, `layout_test.go`
- Modify: `pkg/config/config.go` (+ `GetBackupRetentionDays`), `pkg/config/config_test.go`
- Modify: `.env.example` (`BACKUP_RETENTION_DAYS`)

**Interfaces:**
- Consumes: `recipe.Type`, `recipe.Retention` (Task 2); `command.PointKind`, `command.RestorePoint`, `command.ValidPointID` (Task 3); `store.Object` (C1).
- Produces (package `layout`): `Folder(appID, backup) string` (`<app-id>/<backup>/`); `DataExt(recipe.Type) string` (`.db.zst` / `.tar.zst`); `ManifestExt = ".json"`; `PointID(t time.Time, kind command.PointKind) string` (`20261011T103045Z-manual`); `ParsePointID(id) (time.Time, command.PointKind, bool)`; `DataKey(appID, backup, id string, t recipe.Type) string`; `ManifestKey(appID, backup, id string) string`; `type Manifest struct{ AppID, Backup string; Type recipe.Type; Kind command.PointKind; Created time.Time; SHA256 string; Size, RawSize int64; Fingerprint, AgentVersion string }`; `type Point struct{ ID string; Time time.Time; Kind command.PointKind; DataKey, ManifestKey string; Size int64 }` with `Complete() bool`, `Keys() []string`, `Info(ceilingDays int) command.RestorePoint`; `Points(objects []store.Object, folder string) []Point` (newest first); `Complete([]Point) []Point`; `Newest(points, kinds...) (Point, bool)`; `type Policy struct{ Keep recipe.Retention; CeilingDays int }`; `Prune(points []Point, p Policy, now time.Time) []Point`; `NeedsRefresh(points []Point, ceilingDays int, now time.Time) bool`; `Expires(p Point, ceilingDays int) time.Time`; `IncompleteAfter = 24h`; `PreRestoreKeep = 30 days`
- `(*config.ServerConfig).GetBackupRetentionDays() int` (default 180; 0 = no ceiling; negative or unparsable = default)

- [ ] **Step 1: Write the failing tests**

**Create `internal/infra/backup/layout/layout_test.go`:**

```go
package layout

import (
	"reflect"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/store"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestKeysAndIDs(t *testing.T) {
	ts := at("2026-10-11T12:30:45+02:00")
	id := PointID(ts, command.PointManual)
	if id != "20261011T103045Z-manual" {
		t.Fatalf("id = %s", id)
	}
	got, kind, ok := ParsePointID(id)
	if !ok || !got.Equal(ts) || kind != command.PointManual {
		t.Fatalf("parse = %v %v %v", got, kind, ok)
	}
	if _, _, ok := ParsePointID("20261341T103045Z-manual"); ok {
		t.Fatal("month 13 parsed")
	}
	if _, _, ok := ParsePointID("x"); ok {
		t.Fatal("garbage parsed")
	}
	if k := DataKey("app-1", "db", id, recipe.TypeSQLite); k != "app-1/db/20261011T103045Z-manual.db.zst" {
		t.Fatalf("sqlite key = %s", k)
	}
	if k := DataKey("app-1", "media", id, recipe.TypeFiles); k != "app-1/media/20261011T103045Z-manual.tar.zst" {
		t.Fatalf("files key = %s", k)
	}
	if k := ManifestKey("app-1", "db", id); k != "app-1/db/20261011T103045Z-manual.json" {
		t.Fatalf("manifest key = %s", k)
	}
}

func TestPoints(t *testing.T) {
	folder := Folder("app-1", "db")
	objs := []store.Object{
		{Key: folder + "20261010T000000Z-scheduled.db.zst", Size: 10},
		{Key: folder + "20261010T000000Z-scheduled.json", Size: 1},
		{Key: folder + "20261011T000000Z-manual.db.zst", Size: 20}, // no manifest yet
		{Key: folder + "20261009T000000Z-pre-restore.json"},        // data gone
		{Key: folder + "notes.txt"},
		{Key: folder + "sub/20261010T000000Z-scheduled.db.zst"},
		{Key: "app-1/other/20261010T000000Z-scheduled.db.zst"},
		{Key: folder + "20261010T000000Z-weekly.db.zst"},
	}
	got := Points(objs, folder)
	want := []Point{
		{ID: "20261011T000000Z-manual", Time: at("2026-10-11T00:00:00Z"), Kind: command.PointManual, DataKey: folder + "20261011T000000Z-manual.db.zst", Size: 20},
		{ID: "20261010T000000Z-scheduled", Time: at("2026-10-10T00:00:00Z"), Kind: command.PointScheduled,
			DataKey: folder + "20261010T000000Z-scheduled.db.zst", ManifestKey: folder + "20261010T000000Z-scheduled.json", Size: 10},
		{ID: "20261009T000000Z-pre-restore", Time: at("2026-10-09T00:00:00Z"), Kind: command.PointPreRestore, ManifestKey: folder + "20261009T000000Z-pre-restore.json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("points =\n%+v\nwant\n%+v", got, want)
	}
	if c := Complete(got); len(c) != 1 || c[0].ID != "20261010T000000Z-scheduled" {
		t.Fatalf("complete = %+v", c)
	}
	if n, ok := Newest(got, command.PointManual, command.PointScheduled); !ok || n.Kind != command.PointScheduled {
		t.Fatalf("newest = %+v %v", n, ok)
	}
	if _, ok := Newest(got, command.PointPreRestore); ok {
		t.Fatal("an incomplete point counted as newest")
	}
	if k := got[2].Keys(); len(k) != 1 || k[0] != folder+"20261009T000000Z-pre-restore.json" {
		t.Fatalf("keys = %v", k)
	}
	info := got[1].Info(180)
	if info.ID != got[1].ID || info.Size != 10 || info.Time != at("2026-10-10T00:00:00Z").Unix() ||
		info.Expires != at("2027-04-08T00:00:00Z").Unix() {
		t.Fatalf("info = %+v", info)
	}
	if got[1].Info(0).Expires != 0 {
		t.Fatal("no ceiling expires")
	}
}

// point is a complete point of kind made at ts.
func point(ts string, kind command.PointKind) Point {
	t := at(ts)
	id := PointID(t, kind)
	return Point{ID: id, Time: t, Kind: kind, DataKey: id + ".db.zst", ManifestKey: id + ".json"}
}

func ids(ps []Point) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func TestPruneTiers(t *testing.T) {
	now := at("2026-10-11T12:00:00Z")
	// Hourly copies over the last 3 days, then one a day for 3 more days,
	// then one per month for a few months.
	var points []Point
	for h := 0; h < 72; h++ {
		points = append(points, point(now.Add(-time.Duration(h)*time.Hour-30*time.Minute).Format(time.RFC3339), command.PointScheduled))
	}
	for _, ts := range []string{"2026-10-07T23:00:00Z", "2026-10-07T10:00:00Z", "2026-10-06T10:00:00Z", "2026-09-15T10:00:00Z",
		"2026-09-01T10:00:00Z", "2026-08-20T10:00:00Z", "2026-03-01T10:00:00Z"} {
		points = append(points, point(ts, command.PointScheduled))
	}
	policy := Policy{Keep: recipe.Retention{Recent: 24 * time.Hour, Daily: 5, Monthly: 3}}
	del := map[string]bool{}
	for _, p := range Prune(points, policy, now) {
		del[p.ID] = true
	}
	kept := []string{}
	for _, p := range points {
		if !del[p.ID] {
			kept = append(kept, p.ID)
		}
	}
	// Recent: 24 copies (11:30 back to 12:30 the day before). Daily (5 days,
	// today = day 0): the newest of each day, of which 10-11's and 10-10's
	// (23:30) are recent already, then 10-09 23:30, 10-08 23:30 and 10-07
	// 23:00. Monthly (3 months): October's newest is kept already, then
	// September 15th and August 20th. March is past both tiers.
	want := []string{}
	for h := 0; h < 24; h++ {
		want = append(want, PointID(now.Add(-time.Duration(h)*time.Hour-30*time.Minute), command.PointScheduled))
	}
	want = append(want, "20261009T233000Z-scheduled", "20261008T233000Z-scheduled",
		"20261007T230000Z-scheduled", "20260915T100000Z-scheduled", "20260820T100000Z-scheduled")
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept =\n%v\nwant\n%v", kept, want)
	}
}

func TestPruneKindsAndCeiling(t *testing.T) {
	now := at("2026-10-11T12:00:00Z")
	incompleteOld := point("2026-10-10T10:00:00Z", command.PointManual)
	incompleteOld.ManifestKey = ""
	incompleteNew := point("2026-10-11T11:00:00Z", command.PointScheduled)
	incompleteNew.ManifestKey = ""
	points := []Point{
		incompleteNew,
		incompleteOld,
		point("2026-09-01T10:00:00Z", command.PointManual),     // manual: only the ceiling
		point("2026-09-01T09:00:00Z", command.PointPreRestore), // older than 30 days
		point("2026-09-20T09:00:00Z", command.PointPreRestore), // younger
		point("2026-01-01T10:00:00Z", command.PointScheduled),  // newest scheduled: kept even past every tier
		point("2025-12-31T10:00:00Z", command.PointScheduled),  // past every tier
		point("2025-01-01T10:00:00Z", command.PointManual),     // past the ceiling
	}
	policy := Policy{Keep: recipe.Retention{Recent: time.Hour, Daily: 1, Monthly: 1}, CeilingDays: 365}
	got := ids(Prune(points, policy, now))
	want := []string{"20261010T100000Z-manual", "20260901T090000Z-pre-restore", "20251231T100000Z-scheduled", "20250101T100000Z-manual"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pruned = %v, want %v", got, want)
	}
	// The ceiling overrides keeping the newest scheduled copy.
	policy.CeilingDays = 200
	got = ids(Prune(points, policy, now))
	if got[len(got)-3] != "20260101T100000Z-scheduled" {
		t.Fatalf("pruned under a 200 day ceiling = %v", got)
	}
	// No ceiling: the manual copy from 2025 stays.
	policy.CeilingDays = 0
	for _, id := range ids(Prune(points, policy, now)) {
		if id == "20250101T100000Z-manual" {
			t.Fatal("a manual copy was pruned without a ceiling")
		}
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := at("2026-10-11T12:00:00Z")
	old := []Point{point("2026-07-01T00:00:00Z", command.PointScheduled), point("2026-10-10T00:00:00Z", command.PointManual)}
	if !NeedsRefresh(old, 180, now) {
		t.Fatal("a scheduled copy older than half the ceiling needs a refresh")
	}
	if NeedsRefresh(old, 0, now) {
		t.Fatal("no ceiling, no refresh")
	}
	if NeedsRefresh(old, 400, now) {
		t.Fatal("younger than half the ceiling")
	}
	if NeedsRefresh(old[1:], 180, now) {
		t.Fatal("no scheduled copy: nothing to refresh")
	}
	if e := Expires(old[0], 0); !e.IsZero() {
		t.Fatal("no ceiling never expires")
	}
}
```

**In `pkg/config/config_test.go`, replace:**

```go
func TestCertPathsDeriveFromCertDir(t *testing.T) {
```

**with:**

```go
func TestGetBackupRetentionDays(t *testing.T) {
	c := NewServerConfig("standalone")
	for v, want := range map[string]int{"": 180, "30": 30, "0": 0, "-1": 180, "a year": 180} {
		t.Setenv("BACKUP_RETENTION_DAYS", v)
		if got := c.GetBackupRetentionDays(); got != want {
			t.Errorf("BACKUP_RETENTION_DAYS=%q: %d, want %d", v, got, want)
		}
	}
	unsetenv(t, "BACKUP_RETENTION_DAYS")
	if got := c.GetBackupRetentionDays(); got != 180 {
		t.Fatalf("unset: %d", got)
	}
}

func TestCertPathsDeriveFromCertDir(t *testing.T) {
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/backup/layout/ ./pkg/config/`

Expected: build failure: `no non-test Go files` for layout; `c.GetBackupRetentionDays undefined`.

- [ ] **Step 3: Implement**

**Create `internal/infra/backup/layout/layout.go`:**

```go
// Package layout is how backups are stored in a storage: one folder per
// app backup (<app-id>/<backup>/, below the storage's own folder or prefix),
// one data object per restore point (<yyyymmddThhmmssZ>-<kind>.<db|tar>.zst)
// and its manifest beside it (<id>.json), written last. It pairs listed
// objects into points and decides which points retention deletes. Backup
// runs use it to write, and restore (C2b) to read.
package layout

import (
	"sort"
	"strings"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/store"
)

// timeFormat is the UTC timestamp a point id starts with.
const timeFormat = "20060102T150405Z"

// ManifestExt ends a manifest's key.
const ManifestExt = ".json"

// IncompleteAfter is how long a point without a manifest (a run that died
// mid-upload) is left alone before retention deletes it.
const IncompleteAfter = 24 * time.Hour

// PreRestoreKeep is how long a pre-restore point is kept.
const PreRestoreKeep = 30 * 24 * time.Hour

// Folder is the folder of an app's backup, relative to the storage.
func Folder(appID, backup string) string { return appID + "/" + backup + "/" }

// DataExt is the data object's extension for a backup type.
func DataExt(t recipe.Type) string {
	if t == recipe.TypeSQLite {
		return ".db.zst"
	}
	return ".tar.zst"
}

// PointID names the point made at t (UTC, to the second) of kind.
func PointID(t time.Time, kind command.PointKind) string {
	return t.UTC().Format(timeFormat) + "-" + string(kind)
}

// ParsePointID splits a point id; ok is false for anything else.
func ParsePointID(id string) (time.Time, command.PointKind, bool) {
	if !command.ValidPointID(id) {
		return time.Time{}, "", false
	}
	t, err := time.Parse(timeFormat, id[:16])
	if err != nil {
		return time.Time{}, "", false
	}
	return t, command.PointKind(id[17:]), true
}

// DataKey is the key of a point's data object.
func DataKey(appID, backup, id string, t recipe.Type) string {
	return Folder(appID, backup) + id + DataExt(t)
}

// ManifestKey is the key of a point's manifest.
func ManifestKey(appID, backup, id string) string {
	return Folder(appID, backup) + id + ManifestExt
}

// Manifest describes a point's data object; a point is complete once its
// manifest exists. It never holds a secret.
type Manifest struct {
	AppID        string            `json:"app_id"`
	Backup       string            `json:"backup"`
	Type         recipe.Type       `json:"type"`
	Kind         command.PointKind `json:"kind"`
	Created      time.Time         `json:"created"`
	SHA256       string            `json:"sha256"`
	Size         int64             `json:"size"`     // stored (compressed) bytes
	RawSize      int64             `json:"raw_size"` // the database or tar stream before compression
	Fingerprint  string            `json:"fingerprint"`
	AgentVersion string            `json:"agent_version"`
}

// Point is one restore point as found in a backup's folder.
type Point struct {
	ID          string
	Time        time.Time
	Kind        command.PointKind
	DataKey     string // "" when the data object is missing
	ManifestKey string // "" when the manifest is missing
	Size        int64  // the data object's size
}

// Complete reports whether both the data object and its manifest exist.
func (p Point) Complete() bool { return p.DataKey != "" && p.ManifestKey != "" }

// Keys are the point's existing objects.
func (p Point) Keys() []string {
	var out []string
	for _, k := range []string{p.DataKey, p.ManifestKey} {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Info is the point as the API lists it, with when the ceiling deletes it.
func (p Point) Info(ceilingDays int) command.RestorePoint {
	info := command.RestorePoint{ID: p.ID, Time: p.Time.Unix(), Kind: p.Kind, Size: p.Size}
	if e := Expires(p, ceilingDays); !e.IsZero() {
		info.Expires = e.Unix()
	}
	return info
}

// Points pairs the objects listed under folder into points, newest first.
// Keys that are not a point's data object or manifest are ignored.
func Points(objects []store.Object, folder string) []Point {
	byID := map[string]*Point{}
	for _, o := range objects {
		name, ok := strings.CutPrefix(o.Key, folder)
		if !ok || strings.Contains(name, "/") {
			continue
		}
		id, isManifest := strings.CutSuffix(name, ManifestExt)
		if !isManifest {
			var found bool
			for _, ext := range []string{DataExt(recipe.TypeSQLite), DataExt(recipe.TypeFiles)} {
				if base, ok := strings.CutSuffix(name, ext); ok {
					id, found = base, true
				}
			}
			if !found {
				continue
			}
		}
		t, kind, ok := ParsePointID(id)
		if !ok {
			continue
		}
		p := byID[id]
		if p == nil {
			p = &Point{ID: id, Time: t, Kind: kind}
			byID[id] = p
		}
		if isManifest {
			p.ManifestKey = o.Key
		} else {
			p.DataKey, p.Size = o.Key, o.Size
		}
	}
	out := make([]Point, 0, len(byID))
	for _, p := range byID {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// Complete filters the complete points.
func Complete(points []Point) []Point {
	var out []Point
	for _, p := range points {
		if p.Complete() {
			out = append(out, p)
		}
	}
	return out
}

// Newest is the newest complete point of one of kinds (ok false: none).
func Newest(points []Point, kinds ...command.PointKind) (Point, bool) {
	for _, p := range points { // newest first
		if p.Complete() && contains(kinds, p.Kind) {
			return p, true
		}
	}
	return Point{}, false
}

func contains(kinds []command.PointKind, k command.PointKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}

// Policy is a retention: the backup's tiers and the server's ceiling
// (BACKUP_RETENTION_DAYS, 0 = none).
type Policy struct {
	Keep        recipe.Retention
	CeilingDays int
}

// ceiling is the age past which every copy goes (0 = none).
func (p Policy) ceiling() time.Duration { return time.Duration(p.CeilingDays) * 24 * time.Hour }

// Prune returns the points retention deletes at now:
//   - any point older than the ceiling, whatever its kind (the ceiling
//     overrides every rule below);
//   - an incomplete point older than IncompleteAfter;
//   - a pre-restore point older than PreRestoreKeep;
//   - a scheduled point no tier keeps: every copy younger than Keep.Recent,
//     then the newest of each of the last Keep.Daily UTC days and the last
//     Keep.Monthly UTC months; the newest complete scheduled copy is always
//     kept (below the ceiling).
//
// Manual points go only by the ceiling (or by hand).
func Prune(points []Point, p Policy, now time.Time) []Point {
	now = now.UTC()
	ceiling := p.ceiling()
	keepDay, keepMonth := map[int]bool{}, map[int]bool{}
	newestScheduled := true
	var out []Point
	for _, pt := range points { // newest first
		age := now.Sub(pt.Time)
		switch {
		case ceiling > 0 && age > ceiling:
			out = append(out, pt)
		case !pt.Complete():
			if age > IncompleteAfter {
				out = append(out, pt)
			}
		case pt.Kind == command.PointPreRestore:
			if age > PreRestoreKeep {
				out = append(out, pt)
			}
		case pt.Kind == command.PointScheduled:
			day, month := daysBetween(pt.Time, now), monthsBetween(pt.Time, now)
			keep := newestScheduled || age < p.Keep.Recent
			if day < p.Keep.Daily && !keepDay[day] {
				keepDay[day], keep = true, true
			}
			if month < p.Keep.Monthly && !keepMonth[month] {
				keepMonth[month], keep = true, true
			}
			newestScheduled = false
			if !keep {
				out = append(out, pt)
			}
		}
	}
	return out
}

// daysBetween counts UTC calendar days from t to now (0 = the same day).
func daysBetween(t, now time.Time) int {
	y1, m1, d1 := t.UTC().Date()
	y2, m2, d2 := now.Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a) / (24 * time.Hour))
}

// monthsBetween counts UTC calendar months from t to now (0 = the same month).
func monthsBetween(t, now time.Time) int {
	y1, m1, _ := t.UTC().Date()
	y2, m2, _ := now.Date()
	return (y2-y1)*12 + int(m2-m1)
}

// NeedsRefresh reports whether a run must upload a copy even when nothing
// changed: the newest complete scheduled copy is older than half the
// ceiling, so an idle backup's only copy is replaced before the ceiling
// deletes it. Never with no ceiling, or with no scheduled copy yet (the run
// uploads anyway: there is nothing to compare with).
func NeedsRefresh(points []Point, ceilingDays int, now time.Time) bool {
	if ceilingDays <= 0 {
		return false
	}
	p, ok := Newest(points, command.PointScheduled)
	return ok && now.Sub(p.Time) > time.Duration(ceilingDays)*12*time.Hour
}

// Expires is when a point reaches the ceiling (zero time: never).
func Expires(p Point, ceilingDays int) time.Time {
	if ceilingDays <= 0 {
		return time.Time{}
	}
	return p.Time.Add(time.Duration(ceilingDays) * 24 * time.Hour)
}
```

**In `pkg/config/config.go`, replace:**

```go
// GetAppsDir holds the rendered, ready-to-run deployments:
```

**with:**

```go
// GetBackupRetentionDays is the ceiling on the age of every backup copy
// (BACKUP_RETENTION_DAYS, default 180): no copy of any kind, of any app, is
// kept longer. 0 means no ceiling; a negative or unparsable value is the
// default. Read by standalone and agent.
func (c *ServerConfig) GetBackupRetentionDays() int {
	if v, err := strconv.Atoi(os.Getenv("BACKUP_RETENTION_DAYS")); err == nil && v >= 0 {
		return v
	}
	return 180
}

// GetAppsDir holds the rendered, ready-to-run deployments:
```

**In `.env.example`, replace:**

```sh
# Docker credential store directory honored when listing registry image tags
# (standard Docker variable; default ~/.docker).
#DOCKER_CONFIG=
```

**with:**

```sh
# Docker credential store directory honored when listing registry image tags
# (standard Docker variable; default ~/.docker).
#DOCKER_CONFIG=

# Ceiling on the age of backup copies, in days (standalone/agent): no copy of
# any kind, of any app (deleted apps' backups included), is kept longer,
# whatever the backup's own retention says. A scheduled backup whose data
# hasn't changed uploads a fresh copy once its newest one is older than half
# of this, so it always keeps one. 0 = no ceiling. Default 180.
#BACKUP_RETENTION_DAYS=180
```

- [ ] **Step 4: Run the tests**

Run: `go test -cover ./internal/infra/backup/layout/ ./pkg/config/`

Expected: PASS; layout coverage 100%.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/infra/backup/layout/layout_test.go pkg/config/config_test.go internal/infra/backup/layout/layout.go pkg/config/config.go .env.example
git commit -F - <<'EOF'
feat(agent): backup storage layout, restore points and tiered retention

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 5: The helper protocol and the hidden `backup dump` subcommand

**Files:**
- Create: `internal/infra/backup/helper/helper.go`, `helper_test.go` (the agent ↔ helper contract: image, labels, exit codes, `Result`)
- Create: `internal/app/backup/cli/cli.go`, `sqlite.go`, `files.go`, `cli_test.go`
- Modify: `cmd/standalone/main.go`, `cmd/agent/main.go` (the hidden `backup` subcommand; both binaries can run the agent, and the helper runs whichever one is running)

**Interfaces:**
- Consumes: `modernc.org/sqlite` (already in `go.mod`; pure Go, so `CGO_ENABLED=0` release binaries stay static).
- Produces:
  - package `helper` (`winterflow/internal/infra/backup/helper`): `Image = "alpine:3.20"`; `RoleLabel = "winterflow.role=backup-helper"`; `AppLabel = "winterflow.app"`; `ExitOK=0`, `ExitFailed=1`, `ExitUsage=2`, `ExitUnchanged=3`, `ExitMissing=4`; `type Result struct{ Unchanged bool; Fingerprint string; RawSize int64; Warnings []string; Error string }` (JSON `unchanged`, `fingerprint`, `raw_size`, `warnings`, `error`, all omitempty); `ParseResult(stderr []byte) (Result, bool)` (stderr's last non-empty line)
  - package `cli` (`winterflow/internal/app/backup/cli`): `Main(args []string, stdout, stderr io.Writer) int` for `backup dump sqlite <path> [--since fp]` and `backup dump files --path <src>=<entry>... [--exclude p]... [--since fp]`; it writes the copy to stdout and ends stderr with a JSON `helper.Result`
  - sqlite fingerprint: `sqlite:<hex>` of the size+mtime of the file and its `-wal`; files fingerprint: `files:<hex>` of every entry's name, size, mode, mtime (and symlink target)

- [ ] **Step 1: Write the failing tests**

**Create `internal/infra/backup/helper/helper_test.go`:**

```go
package helper

import "testing"

func TestParseResult(t *testing.T) {
	res, ok := ParseResult([]byte("some log\n{\"fingerprint\":\"files:ab\",\"raw_size\":12,\"warnings\":[\"w\"]}\n\n"))
	if !ok || res.Fingerprint != "files:ab" || res.RawSize != 12 || len(res.Warnings) != 1 {
		t.Fatalf("result = %+v %v", res, ok)
	}
	for _, s := range []string{"", "docker: Error response from daemon: no such image", "{broken"} {
		if _, ok := ParseResult([]byte(s)); ok {
			t.Errorf("%q parsed", s)
		}
	}
}
```

**Create `internal/app/backup/cli/cli_test.go`:**

```go
package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// run calls Main and decodes stderr's last line.
func run(t *testing.T, args ...string) (int, []byte, Result) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Main(args, &stdout, &stderr)
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	var res Result
	if code != ExitUsage {
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
			t.Fatalf("stderr's last line is not a result: %q", stderr.String())
		}
	}
	return code, stdout.Bytes(), res
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, path string) int {
	t.Helper()
	var n int
	if err := openDB(t, path).QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A copy taken while a second connection keeps writing in WAL mode is
// consistent and holds at least every row committed before it started.
func TestDumpSQLiteWhileWriting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatal(err)
	}
	var written atomic.Int64
	insert := func() {
		if _, err := db.Exec("INSERT INTO items (body) VALUES (?)", strings.Repeat("x", 512)); err != nil {
			t.Error(err)
			return
		}
		written.Add(1)
	}
	for range 500 {
		insert()
	}
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		for ctx.Err() == nil {
			insert()
		}
	})
	before := written.Load()
	code, out, res := run(t, "dump", "sqlite", path)
	stop()
	wg.Wait()
	if code != ExitOK || res.Error != "" {
		t.Fatalf("code %d, result %+v", code, res)
	}
	if res.RawSize != int64(len(out)) || !strings.HasPrefix(res.Fingerprint, "sqlite:") {
		t.Fatalf("result = %+v (stdout %d bytes)", res, len(out))
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if n := count(t, copyPath); int64(n) < before || int64(n) > written.Load() {
		t.Fatalf("copy has %d rows; %d were written before the dump, %d in all", n, before, written.Load())
	}
	// The snapshot temp file is gone.
	if left, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "wf-snap-*")); len(left) != 0 {
		t.Fatalf("snapshot left behind: %v", left)
	}
}

func TestDumpSQLiteUnchangedAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db := openDB(t, path)
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	code, _, res := run(t, "dump", "sqlite", path)
	if code != ExitOK {
		t.Fatalf("first dump: %d %+v", code, res)
	}
	code, out, again := run(t, "dump", "sqlite", path, "--since", res.Fingerprint)
	if code != ExitUnchanged || !again.Unchanged || len(out) != 0 {
		t.Fatalf("unchanged: code %d, %+v, %d bytes", code, again, len(out))
	}
	if _, err := db.Exec("INSERT INTO items DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, "dump", "sqlite", path, "--since", res.Fingerprint); code != ExitOK {
		t.Fatalf("a write must change the fingerprint: %d", code)
	}
	code, _, missing := run(t, "dump", "sqlite", filepath.Join(dir, "nope.db"))
	if code != ExitMissing || !strings.Contains(missing.Error, "database file not found") || strings.HasSuffix(missing.Error, "missing") {
		t.Fatalf("missing: %d %+v", code, missing)
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.db")); !os.IsNotExist(err) {
		t.Fatal("a dump created the missing database")
	}
}

func TestDumpSQLiteRefusesWhatIsNotADatabase(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string][]byte{
		"text.db":  []byte("this is not a database, just some text that is long enough to have a header....."),
		"empty.db": nil,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		code, out, res := run(t, "dump", "sqlite", path)
		if code != ExitFailed || res.Error == "" || len(out) != 0 {
			t.Errorf("%s: code %d, %+v, %d bytes", name, code, res, len(out))
		}
	}
	if code, _, res := run(t, "dump", "sqlite", dir); code != ExitFailed || !strings.Contains(res.Error, "is not a file") {
		t.Fatalf("a folder: %d %+v", code, res)
	}
}

// files lays out two mounts: /src/0 (a volume: media + cache + a FIFO) and
// /src/1 (config).
func filesTree(t *testing.T) (src0, src1 string) {
	t.Helper()
	root := t.TempDir()
	src0, src1 = filepath.Join(root, "0"), filepath.Join(root, "1")
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, f := range []struct {
		path string
		mode os.FileMode
		body string
	}{
		{"0/media/a.jpg", 0o640, "jpeg"},
		{"0/media/sub/b.txt", 0o600, "bee"},
		{"0/media/skip.tmp", 0o644, "tmp"},
		{"0/media/cache/c.bin", 0o644, "cached"},
		{"0/other.txt", 0o644, "not backed up"},
		{"1/config/app.yml", 0o644, "k: v"},
	} {
		p := filepath.Join(root, f.path)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a.jpg", filepath.Join(src0, "media", "link.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(src0, "media", "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	return src0, src1
}

func readTar(t *testing.T, raw []byte) map[string]*tar.Header {
	t.Helper()
	out := map[string]*tar.Header{}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		h.PAXRecords = map[string]string{"body": string(body)}
		out[h.Name] = h
	}
}

func TestDumpFiles(t *testing.T) {
	src0, src1 := filesTree(t)
	args := []string{"dump", "files",
		"--path", src0 + "/media=app/media", "--path", src1 + "/config=data/config",
		"--exclude", "/app/media/cache/", "--exclude", "*.tmp"}
	code, out, res := run(t, args...)
	if code != ExitOK || res.RawSize != int64(len(out)) || !strings.HasPrefix(res.Fingerprint, "files:") {
		t.Fatalf("code %d, %+v", code, res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "/app/media/pipe: skipped (a named pipe)") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	got := readTar(t, out)
	names := []string{}
	for n := range got {
		names = append(names, n)
	}
	for _, want := range []string{"app/media/", "app/media/a.jpg", "app/media/sub/", "app/media/sub/b.txt", "app/media/link.jpg", "data/config/", "data/config/app.yml"} {
		if got[want] == nil {
			t.Errorf("%s missing from %v", want, names)
		}
	}
	for _, not := range []string{"app/media/skip.tmp", "app/media/cache/", "app/media/cache/c.bin", "other.txt", "app/media/pipe"} {
		if got[not] != nil {
			t.Errorf("%s should not be archived", not)
		}
	}
	a := got["app/media/a.jpg"]
	if a.Mode&0o777 != 0o640 || !a.ModTime.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) ||
		a.Uid != os.Getuid() || a.Gid != os.Getgid() || a.PAXRecords["body"] != "jpeg" || a.Uname != "" {
		t.Fatalf("a.jpg header = %+v", a)
	}
	if l := got["app/media/link.jpg"]; l.Typeflag != tar.TypeSymlink || l.Linkname != "a.jpg" {
		t.Fatalf("link = %+v", l)
	}
	if d := got["app/media/sub/"]; d.Typeflag != tar.TypeDir || d.Mode&0o777 != 0o750 {
		t.Fatalf("dir = %+v", d)
	}

	// Unchanged, then changed by a touch.
	code, out, again := run(t, append(args, "--since", res.Fingerprint)...)
	if code != ExitUnchanged || !again.Unchanged || len(out) != 0 {
		t.Fatalf("unchanged: %d %+v", code, again)
	}
	later := time.Now()
	if err := os.Chtimes(filepath.Join(src1, "config", "app.yml"), later, later); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, append(args, "--since", res.Fingerprint)...); code != ExitOK {
		t.Fatalf("a touched file must change the fingerprint: %d", code)
	}
	// An excluded file changing does not.
	_, _, res = run(t, args...)
	if err := os.WriteFile(filepath.Join(src0, "media", "skip.tmp"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, append(args, "--since", res.Fingerprint)...); code != ExitUnchanged {
		t.Fatalf("an excluded file changed the fingerprint: %d", code)
	}
}

// A whole volume (entry "") archives names relative to the volume root.
func TestDumpFilesWholeVolume(t *testing.T) {
	src0, _ := filesTree(t)
	code, out, res := run(t, "dump", "files", "--path", src0+"=", "--exclude", "media/")
	if code != ExitOK {
		t.Fatalf("%d %+v", code, res)
	}
	got := readTar(t, out)
	if len(got) != 1 || got["other.txt"] == nil {
		t.Fatalf("archive = %v", got)
	}
}

func TestDumpFilesMissingPaths(t *testing.T) {
	src0, _ := filesTree(t)
	code, out, res := run(t, "dump", "files", "--path", src0+"/media/a.jpg=app/media/a.jpg", "--path", src0+"/gone=app/gone")
	if code != ExitOK || len(res.Warnings) != 1 || res.Warnings[0] != "/app/gone: not found" {
		t.Fatalf("partly missing: %d %+v", code, res)
	}
	if got := readTar(t, out); len(got) != 1 || got["app/media/a.jpg"] == nil {
		t.Fatalf("archive = %v", got)
	}
	code, _, res = run(t, "dump", "files", "--path", src0+"/gone=app/gone", "--path", src0+"/nope=")
	if code != ExitMissing || res.Error != "none of the backup's paths exist" || len(res.Warnings) != 2 || res.Warnings[1] != "the volume: not found" {
		t.Fatalf("all missing: %d %+v", code, res)
	}
}

func TestExcluded(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dir     bool
		pattern string
		want    bool
	}{
		{"app/media/cache", true, "/app/media/cache/", true},
		{"app/media/cache", false, "/app/media/cache/", false},
		{"app/x.tmp", false, "*.tmp", true},
		{"app/sub/x.tmp", false, "app/*.tmp", false},
		{"app/x.tmp", false, "app/*.tmp", true},
		{"app/x.txt", false, "*.tmp", false},
		{"app", true, "/", false},
	} {
		if got := excluded(tc.name, tc.dir, []string{tc.pattern}); got != tc.want {
			t.Errorf("%s (dir %v) ~ %q = %v", tc.name, tc.dir, tc.pattern, got)
		}
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		nil, {"dump"}, {"restore", "sqlite"}, {"dump", "postgres"},
		{"dump", "sqlite"}, {"dump", "sqlite", "--since", "x"}, {"dump", "sqlite", "/a", "extra"},
		{"dump", "files"}, {"dump", "files", "--path", "relative=x"}, {"dump", "files", "--path", "/a"},
	} {
		var stderr bytes.Buffer
		if code := Main(args, io.Discard, &stderr); code != ExitUsage || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%v: code %d, stderr %q", args, code, stderr.String())
		}
	}
}

func TestWarningsAreCapped(t *testing.T) {
	dir := t.TempDir()
	for i := range maxWarnings + 5 {
		if err := syscall.Mkfifo(filepath.Join(dir, "p"+strings.Repeat("x", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, res := run(t, "dump", "files", "--path", dir+"=")
	if len(res.Warnings) != maxWarnings+1 || res.Warnings[maxWarnings] != "and 5 more" {
		t.Fatalf("warnings = %d: %v", len(res.Warnings), res.Warnings[len(res.Warnings)-1])
	}
}

// A file that shrinks between the walk and its copy is padded so the
// archive stays readable.
func TestWriteEntryPadsAShrunkFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(p)
	if err := os.WriteFile(p, []byte("01234"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	warning, err := writeEntry(tw, entry{name: "f", path: p, info: info})
	if err != nil || !strings.Contains(warning, "changed while it was archived") {
		t.Fatalf("warning %q, err %v", warning, err)
	}
	_ = tw.Close()
	if got := readTar(t, buf.Bytes())["f"]; got.Size != 10 || got.PAXRecords["body"] != "01234\x00\x00\x00\x00\x00" {
		t.Fatalf("entry = %+v", got)
	}
	// A file gone before its copy is skipped.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if warning, err := writeEntry(tw, entry{name: "f", path: p, info: info}); err != nil || !strings.Contains(warning, "skipped") {
		t.Fatalf("vanished: %q %v", warning, err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/backup/helper/ ./internal/app/backup/cli/`

Expected: build failure: `no non-test Go files` in both packages.

- [ ] **Step 3: Implement**

**Create `internal/infra/backup/helper/helper.go`:**

```go
// Package helper is the contract between the agent and its backup helper
// container: the image, the labels, and how the `backup` subcommand running
// inside reports (exit codes, and a JSON Result as stderr's last line).
package helper

import (
	"bytes"
	"encoding/json"
)

// Image is the helper's image: hard-coded, never configurable.
const Image = "alpine:3.20"

// Labels on every helper container; RoleLabel finds leftovers.
const (
	RoleLabel = "winterflow.role=backup-helper"
	AppLabel  = "winterflow.app"
)

// Exit codes of the `backup` subcommand.
const (
	ExitOK        = 0
	ExitFailed    = 1
	ExitUsage     = 2
	ExitUnchanged = 3 // the source matches --since: nothing written
	ExitMissing   = 4 // nothing to back up: the database file or every path is missing
)

// Result is the JSON line a dump ends stderr with.
type Result struct {
	Unchanged   bool     `json:"unchanged,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	RawSize     int64    `json:"raw_size,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// ParseResult reads the Result from a helper's stderr: its last non-empty
// line. ok is false when that line is not a Result (docker failed before
// the helper ran).
func ParseResult(stderr []byte) (Result, bool) {
	lines := bytes.Split(bytes.TrimSpace(stderr), []byte("\n"))
	last := bytes.TrimSpace(lines[len(lines)-1])
	var res Result
	if len(last) == 0 || last[0] != '{' || json.Unmarshal(last, &res) != nil {
		return Result{}, false
	}
	return res, true
}
```

**Create `internal/app/backup/cli/cli.go`:**

```go
// Package cli is the hidden `backup` subcommand of the standalone and agent
// binaries. The agent runs it inside a short-lived helper container
// (alpine, --network none, the app's mounts under /src/<n>, the binary
// itself mounted read-only at /wf): it can reach the data in Docker volumes
// that the agent's own user cannot open. A dump writes the copy to stdout
// and, as stderr's last line, a JSON Result.
//
//	backup dump sqlite <path> [--since <fingerprint>]
//	backup dump files --path <src>=<entry name>... [--exclude <pattern>...] [--since <fingerprint>]
//
// Exit codes: 0 done, 3 unchanged since --since (nothing written), 4 nothing
// to back up (the database file or every path is missing), 2 bad usage,
// 1 any other failure.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"winterflow/internal/infra/backup/helper"
)

// Exit codes (helper.Exit*), re-exported for this package's readers.
const (
	ExitOK        = helper.ExitOK
	ExitFailed    = helper.ExitFailed
	ExitUsage     = helper.ExitUsage
	ExitUnchanged = helper.ExitUnchanged
	ExitMissing   = helper.ExitMissing
)

// Result is the JSON line a dump ends stderr with.
type Result = helper.Result

// errMissing is a dump with nothing to back up (exit 4).
var errMissing = errors.New("missing")

// errUnchanged is a dump whose source matches --since (exit 3).
var errUnchanged = errors.New("unchanged")

// maxWarnings caps the warnings a Result carries.
const maxWarnings = 20

// Main runs `backup <args>` and returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "dump" {
		return usage(stderr)
	}
	var (
		res Result
		err error
	)
	switch args[1] {
	case "sqlite":
		res, err = dumpSQLiteCmd(args[2:], stdout)
	case "files":
		res, err = dumpFilesCmd(args[2:], stdout)
	default:
		return usage(stderr)
	}
	code := ExitOK
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprintln(stderr, err)
		return usage(stderr)
	case errors.Is(err, errUnchanged):
		res, code = Result{Unchanged: true}, ExitUnchanged
	case errors.Is(err, errMissing):
		res.Error, code = strings.TrimSuffix(err.Error(), ": "+errMissing.Error()), ExitMissing
	case err != nil:
		res.Error, code = err.Error(), ExitFailed
	}
	if len(res.Warnings) > maxWarnings {
		more := len(res.Warnings) - maxWarnings
		res.Warnings = append(res.Warnings[:maxWarnings], fmt.Sprintf("and %d more", more))
	}
	line, _ := json.Marshal(res)
	fmt.Fprintln(stderr, string(line))
	return code
}

var errUsage = errors.New("usage")

func usage(stderr io.Writer) int {
	fmt.Fprintln(stderr, `usage:
  backup dump sqlite <path> [--since <fingerprint>]
  backup dump files --path <src>=<entry name>... [--exclude <pattern>...] [--since <fingerprint>]`)
	return ExitUsage
}

// list is a repeatable string flag.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func dumpSQLiteCmd(args []string, stdout io.Writer) (Result, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return Result{}, fmt.Errorf("%w: the database path comes first", errUsage)
	}
	path := args[0]
	fs := flag.NewFlagSet("dump sqlite", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	since := fs.String("since", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 {
		return Result{}, fmt.Errorf("%w: dump sqlite takes <path> [--since <fingerprint>]", errUsage)
	}
	return dumpSQLite(path, *since, os.TempDir(), stdout)
}

func dumpFilesCmd(args []string, stdout io.Writer) (Result, error) {
	fs := flag.NewFlagSet("dump files", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var paths, exclude list
	fs.Var(&paths, "path", "")
	fs.Var(&exclude, "exclude", "")
	since := fs.String("since", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || len(paths) == 0 {
		return Result{}, fmt.Errorf("%w: dump files takes --path <src>=<entry name>...", errUsage)
	}
	sources := make([]source, 0, len(paths))
	for _, p := range paths {
		src, entry, ok := strings.Cut(p, "=")
		if !ok || !strings.HasPrefix(src, "/") {
			return Result{}, fmt.Errorf("%w: --path %q: use <absolute source>=<entry name>", errUsage, p)
		}
		sources = append(sources, source{path: src, entry: strings.Trim(entry, "/")})
	}
	return dumpFiles(sources, exclude, *since, stdout)
}
```

**Create `internal/app/backup/cli/sqlite.go`:**

```go
package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // the pure-Go "sqlite" driver: the binary stays static
)

// busyTimeout bounds how long the copy waits for the app's writer.
const busyTimeout = 30 * time.Second

// dumpSQLite copies the database at path with VACUUM INTO (a consistent
// snapshot that, in WAL mode, doesn't block the app's writer) into a temp
// file in tmpDir, checks it, and writes it to stdout. The fingerprint is
// the size and mtime of the database and its -wal file, taken before the
// copy: equal to since means unchanged.
func dumpSQLite(path, since, tmpDir string, stdout io.Writer) (Result, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{}, fmt.Errorf("database file not found: %s: %w", path, errMissing)
	}
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() {
		return Result{}, fmt.Errorf("%s is not a file", path)
	}
	fp := sqliteFingerprint(path, info)
	if since != "" && fp == since {
		return Result{}, errUnchanged
	}
	// The helper runs as root; open the database as its owner, so the -wal
	// and -shm files SQLite may create belong to the app, not to root.
	if err := becomeOwner(info); err != nil {
		return Result{}, err
	}
	snap, err := os.CreateTemp(tmpDir, "wf-snap-*.db")
	if err != nil {
		return Result{}, err
	}
	snapPath := snap.Name()
	_ = snap.Close()
	_ = os.Remove(snapPath) // VACUUM INTO needs a new file
	defer os.Remove(snapPath)
	if err := vacuumInto(path, snapPath); err != nil {
		return Result{}, err
	}
	if err := checkSnapshot(snapPath); err != nil {
		return Result{}, err
	}
	f, err := os.Open(snapPath)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	n, err := io.Copy(stdout, f)
	if err != nil {
		return Result{}, fmt.Errorf("write the copy: %w", err)
	}
	return Result{Fingerprint: fp, RawSize: n}, nil
}

// sqliteFingerprint hashes the size and mtime of the database and its -wal
// file (absent: "-").
func sqliteFingerprint(path string, info fs.FileInfo) string {
	parts := []string{fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())}
	if wal, err := os.Stat(path + "-wal"); err == nil {
		parts = append(parts, fmt.Sprintf("%d:%d", wal.Size(), wal.ModTime().UnixNano()))
	} else {
		parts = append(parts, "-")
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "/")))
	return "sqlite:" + hex.EncodeToString(sum[:16])
}

// sqliteDSN opens an existing file (mode=rw: never creates one) with the
// busy timeout.
func sqliteDSN(path string, extra ...string) string {
	q := url.Values{}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	for _, e := range extra {
		k, v, _ := strings.Cut(e, "=")
		q.Add(k, v)
	}
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.Clean(path))
	return "file:" + escaped + "?" + q.Encode()
}

func vacuumInto(path, dst string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, "mode=rw"))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dst, "'", "''")+"'"); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	return nil
}

// checkSnapshot requires an intact copy with at least one schema object.
func checkSnapshot(path string) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, "mode=ro"))
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("check the copy: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("the copy failed its integrity check: %s", result)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master").Scan(&n); err != nil {
		return fmt.Errorf("check the copy: %w", err)
	}
	if n == 0 {
		return errors.New("the database is empty (no tables)")
	}
	return nil
}

// becomeOwner drops root for the file's owner. A non-root process (tests,
// a rootless helper) keeps its identity.
func becomeOwner(info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 || st.Uid == 0 {
		return nil
	}
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("drop supplementary groups: %w", err)
	}
	if err := syscall.Setgid(int(st.Gid)); err != nil {
		return fmt.Errorf("switch to group %d: %w", st.Gid, err)
	}
	if err := syscall.Setuid(int(st.Uid)); err != nil {
		return fmt.Errorf("switch to user %d: %w", st.Uid, err)
	}
	return nil
}
```

**Create `internal/app/backup/cli/files.go`:**

```go
package cli

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// source is one --path: an absolute path in the helper (a mount, /src/<n>,
// or below it) and the name it has in the archive ("" = the archive root,
// for a whole volume).
type source struct {
	path  string
	entry string
}

// entry is one file system object to archive.
type entry struct {
	name string // archive name, "/"-separated, no leading "/"
	path string // where it is in the helper
	info fs.FileInfo
	link string // a symlink's target
}

// dumpFiles walks the sources once (the fingerprint: every entry's name,
// size, mode and mtime), then writes a tar stream of them to stdout. Symlinks
// are stored as links, never followed; sockets, devices and FIFOs are
// skipped with a warning; files are read while the app runs.
func dumpFiles(sources []source, exclude []string, since string, stdout io.Writer) (Result, error) {
	var (
		res     Result
		entries []entry
		found   int
	)
	for _, s := range sources {
		got, warnings, err := walk(s, exclude)
		if errors.Is(err, fs.ErrNotExist) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: not found", displayName(s.entry)))
			continue
		}
		if err != nil {
			return Result{}, err
		}
		found++
		entries = append(entries, got...)
		res.Warnings = append(res.Warnings, warnings...)
	}
	if found == 0 {
		return res, fmt.Errorf("none of the backup's paths exist: %w", errMissing)
	}
	res.Fingerprint = filesFingerprint(entries)
	if since != "" && res.Fingerprint == since {
		return Result{}, errUnchanged
	}
	cw := &countWriter{w: stdout}
	tw := tar.NewWriter(cw)
	for _, e := range entries {
		warning, err := writeEntry(tw, e)
		if err != nil {
			return Result{}, err
		}
		if warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
	}
	if err := tw.Close(); err != nil {
		return Result{}, fmt.Errorf("write the archive: %w", err)
	}
	res.RawSize = cw.n
	return res, nil
}

func displayName(entry string) string {
	if entry == "" {
		return "the volume"
	}
	return "/" + entry
}

// walk lists a source: the path itself (unless it is the archive root) and,
// for a folder, everything below it in lexical order. Excluded entries are
// left out (a folder with its contents); special files become warnings.
func walk(s source, exclude []string) ([]entry, []string, error) {
	if _, err := os.Lstat(s.path); err != nil {
		return nil, nil, err
	}
	var (
		out      []entry
		warnings []string
	)
	err := filepath.WalkDir(s.path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p != s.path {
				return nil // removed meanwhile
			}
			if p != s.path {
				warnings = append(warnings, fmt.Sprintf("%s: %v", p, err))
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(s.path, p)
		name := s.entry
		if rel != "." {
			name = path.Join(s.entry, filepath.ToSlash(rel))
		}
		if name == "" { // the root of a whole volume
			return nil
		}
		if excluded(name, d.IsDir(), exclude) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // removed meanwhile
		}
		e := entry{name: name, path: p, info: info}
		switch m := info.Mode(); {
		case m.IsRegular(), m.IsDir():
		case m&fs.ModeSymlink != 0:
			if e.link, err = os.Readlink(p); err != nil {
				return nil
			}
		default:
			warnings = append(warnings, fmt.Sprintf("/%s: skipped (%s)", name, kindOf(m)))
			return nil
		}
		out = append(out, e)
		return nil
	})
	return out, warnings, err
}

func kindOf(m fs.FileMode) string {
	switch {
	case m&fs.ModeSocket != 0:
		return "a socket"
	case m&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case m&fs.ModeDevice != 0, m&fs.ModeCharDevice != 0:
		return "a device"
	}
	return "not a file"
}

// excluded matches an archive name against the exclude patterns
// (path.Match syntax). A leading "/" is ignored (names have none); a
// pattern ending in "/" matches folders only; a pattern without "/" matches
// the last name element, one with "/" the whole name.
func excluded(name string, dir bool, patterns []string) bool {
	for _, p := range patterns {
		dirOnly := strings.HasSuffix(p, "/")
		p = strings.Trim(p, "/")
		if p == "" || (dirOnly && !dir) {
			continue
		}
		target := name
		if !strings.Contains(p, "/") {
			target = path.Base(name)
		}
		if ok, _ := path.Match(p, target); ok {
			return true
		}
	}
	return false
}

// filesFingerprint hashes every entry's name, size, mode and mtime.
func filesFingerprint(entries []entry) string {
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%d\x00%o\x00%d\x00%s\n", e.name, e.info.Size(), uint32(e.info.Mode()), e.info.ModTime().UnixNano(), e.link)
	}
	return "files:" + hex.EncodeToString(h.Sum(nil)[:16])
}

// writeEntry archives one entry with its owner, mode and mtime. A file that
// vanished is skipped, one that shrank is padded, both with a warning.
func writeEntry(tw *tar.Writer, e entry) (string, error) {
	var f *os.File
	if e.info.Mode().IsRegular() {
		var err error
		if f, err = os.Open(e.path); err != nil {
			return fmt.Sprintf("/%s: skipped: %v", e.name, err), nil
		}
		defer f.Close()
	}
	hdr, err := tar.FileInfoHeader(e.info, e.link)
	if err != nil {
		return fmt.Sprintf("/%s: skipped: %v", e.name, err), nil
	}
	hdr.Name = e.name
	if e.info.IsDir() {
		hdr.Name += "/"
	}
	hdr.Uname, hdr.Gname = "", ""
	hdr.Format = tar.FormatPAX
	if err := tw.WriteHeader(hdr); err != nil {
		return "", fmt.Errorf("write the archive: %w", err)
	}
	if f == nil {
		return "", nil
	}
	n, err := io.CopyN(tw, f, hdr.Size)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("archive /%s: %w", e.name, err)
	}
	if _, err := io.CopyN(tw, zeros{}, hdr.Size-n); err != nil {
		return "", fmt.Errorf("write the archive: %w", err)
	}
	return fmt.Sprintf("/%s: changed while it was archived", e.name), nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
```

**In `cmd/standalone/main.go`, replace:**

```go
	"time"
	"winterflow/internal/app/web"
```

**with:**

```go
	"time"
	backupcli "winterflow/internal/app/backup/cli"
	"winterflow/internal/app/web"
```

**In `cmd/standalone/main.go`, replace:**

```go
	case "version", "-v", "--version":
```

**with:**

```go
	case "backup":
		// Hidden: what the agent runs inside a backup helper container.
		os.Exit(backupcli.Main(args, os.Stdout, os.Stderr))
	case "version", "-v", "--version":
```

**In `cmd/agent/main.go`, replace:**

```go
	appagent "winterflow/internal/app/agent"
	ingresscaddy "winterflow/internal/infra/ingress/caddy"
```

**with:**

```go
	appagent "winterflow/internal/app/agent"
	backupcli "winterflow/internal/app/backup/cli"
	ingresscaddy "winterflow/internal/infra/ingress/caddy"
```

**In `cmd/agent/main.go`, replace:**

```go
func main() {
	err := godotenv.Load()
```

**with:**

```go
func main() {
	// Hidden: what the agent runs inside a backup helper container (the
	// binary is mounted there at /wf), before anything reads config or logs.
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		os.Exit(backupcli.Main(os.Args[2:], os.Stdout, os.Stderr))
	}
	err := godotenv.Load()
```

- [ ] **Step 4: Run the tests**

Run: `go test -race -cover ./internal/infra/backup/helper/ ./internal/app/backup/cli/ && go build -buildvcs=false ./cmd/... && go run -buildvcs=false ./cmd/standalone backup; echo "exit $?"`

Expected: PASS (cli coverage ≈ 84%; `becomeOwner`'s root branch only runs in the helper and is exercised by Task 8's Docker test); the last command prints the usage and `exit 2`.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/infra/backup/helper/helper_test.go internal/app/backup/cli/cli_test.go internal/infra/backup/helper/helper.go internal/app/backup/cli/cli.go internal/app/backup/cli/sqlite.go internal/app/backup/cli/files.go cmd/standalone/main.go cmd/agent/main.go
git commit -F - <<'EOF'
feat(agent): hidden backup dump subcommand (sqlite VACUUM INTO, files tar)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 6: Backups in the deployment: overrides, WF_BACKUP_ values, profiles

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/backupconfig.go`, `backupconfig_test.go`
- Modify: `operations.go` (`composeArgsFor` takes profiles; `composeUp` validates backups, resolves `WF_BACKUP_`, passes profiles, removes off custom services; `save`/`undoSave`/`SaveAppResult`; `composeExec` → `runCompose` seam + `dockerIn`), `repository.go` (`runCompose` field), `store.go` (`keepBackups` on save), `runhelper.go` (+ profiles), `storageenv.go` (`renderStorageEnv`, `WF_BACKUP_` values, `storageVariables` with backups), `lifecycle.go` (`GetApp` reports backups), `storages.go` (backups use storages)
- Modify tests: `runhelper_test.go`, `storageenv_test.go`, `storages_test.go`
- Modify: `internal/app/agent/dispatcher.go` (app.save returns the save's warnings)

**Interfaces:**
- Consumes: `recipe.*` (Task 2); `model.CheckStorageRefs(refs, exists, backupDeclared)`, `model.BackupFieldValue` (Task 3).
- Produces (package `dockercompose`):
  - `type appBackupConfig struct{ compose recipe.Compose; overrides []recipe.BackupConfig; backups []recipe.EffectiveBackup }`; `(r).appBackups(dir string, files []string) (appBackupConfig, error)`; `(r).readOverrides(dir) ([]recipe.BackupConfig, error)`; `(r).writeOverrides(dir, []recipe.BackupConfig) error`; `keepBackups(next, prev []byte) []byte`; `(r).cleanBackups(dir) ([]string, error)`
  - `backupProfiles([]recipe.EffectiveBackup) []string`; `(r).composeProfiles(dir string, backups []recipe.EffectiveBackup) []string`; `offServices(comp, backups, active []string) []string`; `(r).stopOffServices(ctx, appID string, files []string, cfg appBackupConfig, active []string) error`
  - `(r).composeArgsFor(appID string, files, profiles []string, verb ...string) []string` (new `profiles` parameter); `runHelperScript(project string, files, profiles []string)`; `(r).writeRunHelper(appID string, files, profiles []string)`
  - `(r).materializeStorageEnv(ctx, dir string, files []string, backups []recipe.EffectiveBackup) error`; `(r).renderStorageEnv(ctx, dir string, files []string, backups []recipe.EffectiveBackup, checkFolders bool) ([]byte, error)` (nil: no references); `storageVariables(refs []string, backups []recipe.EffectiveBackup)`
  - `(r).SaveAppResult(ctx, app command.AppPayload, draft bool) (hash string, warnings []string, err error)`; `(r).save(app) (string, []string, error)`; `(r).undoSave(dir)`
  - `Repository.runCompose func(ctx, dir string, args []string) error` (default `dockerIn`)

- [ ] **Step 1: Write the failing tests**

**Create `internal/infra/orchestrator/docker_compose/backupconfig_test.go`:**

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/crypto/cryptotest"
)

// backupCompose declares a sqlite backup on a volume, a files backup through
// a service, and a custom backup run by a profiled service that references
// its storage through WF_BACKUP_ variables.
const backupCompose = `services:
  web:
    image: example/web
    volumes: [data:/data, ./media:/app/media]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
    environment:
      BUCKET: ${WF_BACKUP_REPLICA_BUCKET}
      PREFIX: ${WF_BACKUP_REPLICA_PREFIX}
      KEY: ${WF_BACKUP_REPLICA_SECRET_ACCESS_KEY}
volumes:
  data:
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: app.db
    - name: media
      type: files
      service: web
      paths: [/app/media]
    - name: replica
      type: custom
      service: litestream
`

// saveConfig saves app appID with compose and the given config blob.
func saveConfig(t *testing.T, r *Repository, appID, compose, config string) ([]string, error) {
	t.Helper()
	_, warnings, err := r.save(command.AppPayload{
		AppID:  appID,
		Config: []byte(config),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
	})
	return warnings, err
}

const plainConfig = `{"name":"demo","files":[{"filename":"compose.yml"}]}`

func withBackups(overrides string) string {
	return `{"name":"demo","files":[{"filename":"compose.yml"}],"backups":` + overrides + `}`
}

func TestKeepBackups(t *testing.T) {
	prev := []byte(`{"name":"a","backups":[{"name":"db","enabled":true,"storage":"s3"}]}`)
	got := keepBackups([]byte(`{"name":"b"}`), prev)
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil || m["name"] != "b" || m["backups"] == nil {
		t.Fatalf("kept = %s", got)
	}
	// A blob with its own list (even empty) wins; junk passes through.
	for _, next := range []string{`{"name":"b","backups":[]}`, `not json`, `null`} {
		if got := keepBackups([]byte(next), prev); string(got) != next {
			t.Errorf("%s -> %s", next, got)
		}
	}
	if got := keepBackups([]byte(`{"name":"b"}`), []byte(`{"name":"a"}`)); string(got) != `{"name":"b"}` {
		t.Fatalf("nothing to keep: %s", got)
	}
}

// The app editor doesn't know the backups: a save keeps the stored list,
// drops overrides of backups the compose file no longer has (a warning),
// and keeps a backup added in the UI.
func TestSaveKeepsAndCleansBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(
		`[{"name":"database","enabled":true,"storage":"hetzner"},{"name":"media","every":"6h"},`+
			`{"name":"audit","type":"sqlite","volume":"data","path":"audit.db"}]`)); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	withoutMedia := strings.Replace(backupCompose, `    - name: media
      type: files
      service: web
      paths: [/app/media]
`, "", 1)
	warnings, err := saveConfig(t, r, "app-1", withoutMedia, plainConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `no longer has the backup "media"`) {
		t.Fatalf("warnings = %v", warnings)
	}
	got, err := r.readOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []recipe.BackupConfig{
		{Name: "database", Enabled: true, Storage: "hetzner"},
		{Name: "audit", Type: recipe.TypeSQLite, Volume: "data", Path: "audit.db"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %+v", got)
	}
	// The cleaned list is what was committed: nothing is left to commit.
	head, _ := gitHead(dir)
	if again, err := gitCommitAll(dir, "noop"); err != nil || again != head {
		t.Fatalf("the save left changes behind: %v", err)
	}
}

// A save whose compose file makes the backups invalid is refused and leaves
// nothing behind: an existing app keeps its last commit, a new one is gone.
func TestSaveRefusesInvalidBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	head, _ := gitHead(dir)
	bad := strings.Replace(backupCompose, "volume: data", "volume: gone", 1)
	_, err := saveConfig(t, r, "app-1", bad, plainConfig)
	if !errors.Is(err, recipe.ErrInvalid) || !strings.Contains(err.Error(), `x-winterflow.backups[0].volume: "gone" is not a volume`) {
		t.Fatalf("err = %v", err)
	}
	if now, _ := gitHead(dir); now != head {
		t.Fatal("a refused save committed")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "compose.yml"))
	if string(raw) != backupCompose {
		t.Fatal("a refused save left its compose file in the worktree")
	}
	// An override the compose file can't satisfy is refused too.
	_, err = saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true}]`))
	if err == nil || !strings.Contains(err.Error(), "backups[0].storage: an enabled backup needs a storage") {
		t.Fatalf("err = %v", err)
	}
	if _, err := saveConfig(t, r, "app-2", bad, plainConfig); err == nil {
		t.Fatal("a new app with invalid backups was saved")
	}
	if _, err := os.Stat(r.appDataDir("app-2")); !os.IsNotExist(err) {
		t.Fatal("a refused new app left its folder")
	}
}

func TestMaterializeStorageEnvBackupVariables(t *testing.T) {
	r, pub := newSecretRepo(t)
	storageFoldersExist(t, r)
	withStorages(t, r, func(p string) string { return cryptotest.Encrypt(t, pub, p) })
	dir := r.appDataDir("app-1")
	read := func() string {
		t.Helper()
		if err := materialize(context.Background(), r, dir); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, storageEnvRel))
		return string(raw)
	}
	// Off: every field empty (its service sits behind an off profile).
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "WF_BACKUP_REPLICA_BUCKET=\nWF_BACKUP_REPLICA_PREFIX=\nWF_BACKUP_REPLICA_SECRET_ACCESS_KEY=\n" {
		t.Fatalf("off = %q", got)
	}
	// On: the storage's values, the prefix narrowed to the backup's folder.
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"hetzner"}]`)); err != nil {
		t.Fatal(err)
	}
	want := "WF_BACKUP_REPLICA_BUCKET=tenants\nWF_BACKUP_REPLICA_PREFIX=wf/app-1/replica/\nWF_BACKUP_REPLICA_SECRET_ACCESS_KEY=s3-plain\n"
	if got := read(); got != want {
		t.Fatalf("on = %q", got)
	}
	// On, with a storage the server doesn't have: the deploy is refused.
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"gone"}]`)); err != nil {
		t.Fatal(err)
	}
	err := materialize(context.Background(), r, dir)
	if !errors.Is(err, model.ErrStorageRef) || !strings.Contains(err.Error(), `the backup replica uses the storage "gone", which this server doesn't have`) {
		t.Fatalf("missing storage: %v", err)
	}
	// A backup the app doesn't declare.
	writeFileT(t, dir, "compose.yml", backupCompose+"# ${WF_BACKUP_OTHER_TYPE}\n")
	if err := materialize(context.Background(), r, dir); err == nil || !strings.Contains(err.Error(), `there is no backup "other" in this app`) {
		t.Fatalf("undeclared: %v", err)
	}
}

func TestComposeProfiles(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	dir := r.appDataDir("app-1")
	writeFileT(t, dir, envRel, "COMPOSE_PROFILES=extra, wf-backup-replica,debug\n")
	off := []recipe.EffectiveBackup{{Name: "replica", Type: recipe.TypeCustom}}
	if got := r.composeProfiles(dir, off); got != nil {
		t.Fatalf("no custom backup on: %v", got)
	}
	on := []recipe.EffectiveBackup{{Name: "replica", Type: recipe.TypeCustom, Enabled: true}, {Name: "db", Type: recipe.TypeSQLite, Enabled: true}}
	if got := r.composeProfiles(dir, on); !reflect.DeepEqual(got, []string{"debug", "extra", "wf-backup-replica"}) {
		t.Fatalf("profiles = %v", got)
	}
	// .env.secrets wins over .env.
	writeFileT(t, dir, envSecretsRel, "COMPOSE_PROFILES=secret\n")
	if got := r.composeProfiles(dir, on); !reflect.DeepEqual(got, []string{"secret", "wf-backup-replica"}) {
		t.Fatalf("profiles = %v", got)
	}
}

func TestOffServices(t *testing.T) {
	comp := recipe.Compose{Services: map[string][]string{
		"litestream": {"wf-backup-replica"},
		"shared":     {"wf-backup-a", "wf-backup-b"},
		"user":       {"wf-backup-c", "extra"},
	}}
	backups := []recipe.EffectiveBackup{
		{Name: "replica", Type: recipe.TypeCustom, Service: "litestream"},
		{Name: "a", Type: recipe.TypeCustom, Service: "shared", Enabled: true},
		{Name: "b", Type: recipe.TypeCustom, Service: "shared"},
		{Name: "c", Type: recipe.TypeCustom, Service: "user"},
		{Name: "db", Type: recipe.TypeSQLite},
	}
	if got := offServices(comp, backups, []string{"wf-backup-a", "extra"}); !reflect.DeepEqual(got, []string{"litestream"}) {
		t.Fatalf("off = %v", got)
	}
}

// composeCalls records the compose commands a deploy runs.
type composeCalls struct {
	mu   sync.Mutex
	args [][]string
}

func (c *composeCalls) install(r *Repository) {
	r.runCompose = func(_ context.Context, _ string, args []string) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.args = append(c.args, args)
		return nil
	}
}

// A deploy activates the enabled custom backups' profiles (with the user's)
// and removes the service of a custom backup that was turned off.
func TestComposeUpProfiles(t *testing.T) {
	r, pub := newSecretRepo(t)
	storageFoldersExist(t, r)
	withStorages(t, r, func(p string) string { return cryptotest.Encrypt(t, pub, p) })
	var calls composeCalls
	calls.install(r)
	if _, err := r.SaveApp(context.Background(), command.AppPayload{AppID: "app-1",
		Config: []byte(withBackups(`[{"name":"replica","enabled":true,"storage":"hetzner"}]`)),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupCompose)}}}); err != nil {
		t.Fatal(err)
	}
	up := strings.Join(calls.args[0], " ")
	if !strings.Contains(up, "--env-file .winterflow/storage.env --profile wf-backup-replica up -d --remove-orphans") || len(calls.args) != 1 {
		t.Fatalf("compose calls = %v", calls.args)
	}
	script, _ := os.ReadFile(filepath.Join(r.appDataDir("app-1"), runRel))
	if !strings.Contains(string(script), "--profile 'wf-backup-replica'") {
		t.Fatalf("run.sh = %s", script)
	}
	calls.args = nil
	if _, err := r.SaveApp(context.Background(), command.AppPayload{AppID: "app-1",
		Config: []byte(withBackups(`[]`)),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupCompose)}}}); err != nil {
		t.Fatal(err)
	}
	if len(calls.args) != 2 || strings.Contains(strings.Join(calls.args[0], " "), "--profile") {
		t.Fatalf("compose calls = %v", calls.args)
	}
	if rm := strings.Join(calls.args[1], " "); !strings.HasSuffix(rm, "--profile wf-backup-replica rm --force --stop litestream") {
		t.Fatalf("rm = %s", rm)
	}
}

func TestGetAppReportsBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"hetzner"}]`)); err != nil {
		t.Fatal(err)
	}
	resp, err := r.GetApp(context.Background(), "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Backups) != 3 || resp.Backups[2].Name != "replica" || !resp.Backups[2].Enabled || resp.BackupsError != "" {
		t.Fatalf("backups = %+v (%s)", resp.Backups, resp.BackupsError)
	}
	var replica []command.StorageVariable
	for _, v := range resp.StorageVariables {
		if v.Backup == "replica" {
			replica = append(replica, v)
		}
	}
	if len(replica) != 3 || replica[0].Storage != "hetzner" || !replica[2].Secret {
		t.Fatalf("backup variables = %+v", replica)
	}
	// A recipe broken outside a save (a git-sourced app's upstream) is
	// reported, not hidden.
	writeFileT(t, r.appDataDir("app-1"), "compose.yml", strings.Replace(backupCompose, "volume: data", "volume: gone", 1))
	resp, _ = r.GetApp(context.Background(), "app-1")
	if !strings.Contains(resp.BackupsError, `"gone" is not a volume`) || resp.Backups != nil {
		t.Fatalf("broken recipe: %+v %q", resp.Backups, resp.BackupsError)
	}
}

// An enabled backup uses its storage: listed among the users, and the
// storage can't be removed meanwhile.
func TestStorageUsersIncludeBackups(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","storage":"nas"}]`)); err != nil {
		t.Fatal(err)
	}
	users, err := r.storageUsers()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(users["disk"], []string{"app-1"}) || users["nas"] != nil {
		t.Fatalf("users = %v", users)
	}
	if err := r.storages.Save(localStorage("disk", t.TempDir()), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteStorage(context.Background(), "disk"); err == nil || !strings.Contains(err.Error(), "disk is used by demo") {
		t.Fatalf("delete = %v", err)
	}
	// An app whose backups can't be read blocks the delete (fail closed).
	writeFileT(t, r.appDataDir("app-1"), "compose.yml", strings.Replace(backupCompose, "volume: data", "volume: gone", 1))
	if err := r.DeleteStorage(context.Background(), "disk"); err == nil || !strings.Contains(err.Error(), "can't be checked") {
		t.Fatalf("delete = %v", err)
	}
}
```

**In `internal/infra/orchestrator/docker_compose/runhelper_test.go`, replace:**

```go
	got := string(runHelperScript("wf-app-1", []string{"compose.yml", "compose.override.yml"}))
```

**with:**

```go
	got := string(runHelperScript("wf-app-1", []string{"compose.yml", "compose.override.yml"}, nil))
```

**In `internal/infra/orchestrator/docker_compose/runhelper_test.go`, replace:**

```go
	odd := string(runHelperScript("wf-app-1", []string{"source/it's $(here).yml"}))
```

**with:**

```go
	odd := string(runHelperScript("wf-app-1", []string{"source/it's $(here).yml"}, nil))
```

**In `internal/infra/orchestrator/docker_compose/runhelper_test.go`, replace:**

```go
	writeFileT(t, dir, runRel, string(runHelperScript("wf-x", []string{"compose.yml"})))
```

**with:**

```go
	writeFileT(t, dir, runRel, string(runHelperScript("wf-x", []string{"compose.yml"}, nil)))
```

**In `internal/infra/orchestrator/docker_compose/runhelper_test.go`, replace:**

```go
	if err := r.writeRunHelper("app-1", r.appComposeFiles(dir)); err != nil {
```

**with:**

```go
	if err := r.writeRunHelper("app-1", r.appComposeFiles(dir), nil); err != nil {
```

**In `internal/infra/orchestrator/docker_compose/storageenv_test.go`, replace:**

```go
// materialize is materializeStorageEnv for the app in dir.
func materialize(ctx context.Context, r *Repository, dir string) error {
	return r.materializeStorageEnv(ctx, dir, r.appComposeFiles(dir))
}
```

**with:**

```go
// materialize is materializeStorageEnv for the app in dir, with its
// backups.
func materialize(ctx context.Context, r *Repository, dir string) error {
	files := r.appComposeFiles(dir)
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return err
	}
	return r.materializeStorageEnv(ctx, dir, files, cfg.backups)
}
```

**In `internal/infra/orchestrator/docker_compose/storages_test.go`, replace:**

```go
	if err == nil || err.Error() != "disk is used by Shop, Blog: remove its WF_STORAGE_ variables from those apps first" {
```

**with:**

```go
	if err == nil || err.Error() != "disk is used by Shop, Blog: remove its WF_STORAGE_ variables from those apps and turn off their backups to it first" {
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/orchestrator/docker_compose/`

Expected: build failure: `r.save undefined`, `r.appBackups undefined`, `too many arguments in call to runHelperScript`, `undefined: keepBackups`, ….

- [ ] **Step 3: Implement**

**Create `internal/infra/orchestrator/docker_compose/backupconfig.go`:**

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"winterflow/internal/domain/backup/recipe"
)

// backupsKey is the key of the backups list (the overrides) in an app's
// committed config.json.
const backupsKey = "backups"

// appBackupConfig is what an app's compose files and config.json say about
// its backups.
type appBackupConfig struct {
	compose   recipe.Compose
	overrides []recipe.BackupConfig
	backups   []recipe.EffectiveBackup
}

// readOverrides is the backups list of the app's config.json (none when it
// has no such key).
func (r *Repository) readOverrides(dir string) ([]recipe.BackupConfig, error) {
	_, raw, err := r.readAppConfig(dir)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Backups []recipe.BackupConfig `json:"backups"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", configRel, err)
	}
	return cfg.Backups, nil
}

// appBackups reads and validates an app's backups: the recipe in its compose
// files with the config.json overrides on top. File reads only.
func (r *Repository) appBackups(dir string, files []string) (appBackupConfig, error) {
	contents, err := composeContents(dir, files)
	if err != nil {
		return appBackupConfig{}, fmt.Errorf("read compose files: %w", err)
	}
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return appBackupConfig{}, err
	}
	overrides, err := r.readOverrides(dir)
	if err != nil {
		return appBackupConfig{}, err
	}
	backups, err := recipe.Effective(comp, overrides)
	if err != nil {
		return appBackupConfig{}, err
	}
	return appBackupConfig{compose: comp, overrides: overrides, backups: backups}, nil
}

// writeOverrides replaces the backups list in the app's config.json (none:
// the key is removed). Every other key is kept as is.
func (r *Repository) writeOverrides(dir string, overrides []recipe.BackupConfig) error {
	cfg, _, err := r.readAppConfig(dir)
	if err != nil {
		return err
	}
	if len(overrides) == 0 {
		delete(cfg, backupsKey)
	} else {
		cfg[backupsKey] = overrides
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, configRel), raw, 0o644)
}

// keepBackups carries the stored backups list into a new config blob that
// has none: the app editor sends the whole config but doesn't know the
// backups, which are edited on their own (backup.update).
func keepBackups(next, prev []byte) []byte {
	var n map[string]json.RawMessage
	if json.Unmarshal(next, &n) != nil || n == nil {
		return next
	}
	if _, ok := n[backupsKey]; ok {
		return next
	}
	var p map[string]json.RawMessage
	if json.Unmarshal(prev, &p) != nil || p[backupsKey] == nil {
		return next
	}
	n[backupsKey] = p[backupsKey]
	out, err := json.Marshal(n)
	if err != nil {
		return next
	}
	return out
}

// cleanBackups validates the app's backups after a save wrote its files and
// drops overrides of backups the recipe no longer has (warnings).
func (r *Repository) cleanBackups(dir string) ([]string, error) {
	contents, err := composeContents(dir, r.appComposeFiles(dir))
	if err != nil {
		return nil, fmt.Errorf("read compose files: %w", err)
	}
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return nil, err
	}
	overrides, err := r.readOverrides(dir)
	if err != nil {
		return nil, err
	}
	kept, warnings := recipe.Clean(comp, overrides)
	if _, err := recipe.Effective(comp, kept); err != nil {
		return nil, err
	}
	if len(kept) != len(overrides) || (len(kept) > 0 && !reflect.DeepEqual(kept, overrides)) {
		if err := r.writeOverrides(dir, kept); err != nil {
			return nil, err
		}
	}
	return warnings, nil
}

// backupProfiles are the compose profiles of the app's enabled custom
// backups, sorted.
func backupProfiles(backups []recipe.EffectiveBackup) []string {
	var out []string
	for _, b := range backups {
		if b.Type == recipe.TypeCustom && b.Enabled {
			out = append(out, recipe.Profile(b.Name))
		}
	}
	sort.Strings(out)
	return out
}

// composeProfiles is what to pass as --profile: nothing while no custom
// backup is on (compose then reads COMPOSE_PROFILES from the env files as
// before); otherwise the user's COMPOSE_PROFILES plus the backups' profiles,
// since an explicit --profile makes compose ignore COMPOSE_PROFILES.
func (r *Repository) composeProfiles(dir string, backups []recipe.EffectiveBackup) []string {
	profiles := backupProfiles(backups)
	if len(profiles) == 0 {
		return nil
	}
	user := ""
	for _, rel := range []string{envRel, envSecretsRel} {
		if raw, err := os.ReadFile(filepath.Join(dir, rel)); err == nil {
			if v, ok := parseEnv(raw)["COMPOSE_PROFILES"]; ok {
				user = v
			}
		}
	}
	for _, p := range strings.Split(user, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(profiles, p) {
			profiles = append(profiles, p)
		}
	}
	sort.Strings(profiles)
	return profiles
}

// offServices are the services of disabled custom backups that no active
// profile keeps: `up --remove-orphans` leaves a service whose profile was
// turned off running, so the deploy removes it explicitly.
func offServices(comp recipe.Compose, backups []recipe.EffectiveBackup, active []string) []string {
	var out []string
	for _, b := range backups {
		if b.Type != recipe.TypeCustom || b.Enabled || slices.Contains(out, b.Service) {
			continue
		}
		on := false
		for _, p := range comp.Services[b.Service] {
			on = on || slices.Contains(active, p)
		}
		if !on {
			out = append(out, b.Service)
		}
	}
	sort.Strings(out)
	return out
}

// stopOffServices removes the containers of offServices (one compose call
// with their profiles active so compose knows them). Not an error when
// they don't run.
func (r *Repository) stopOffServices(ctx context.Context, appID string, files []string, cfg appBackupConfig, active []string) error {
	services := offServices(cfg.compose, cfg.backups, active)
	if len(services) == 0 {
		return nil
	}
	var profiles []string
	for _, b := range cfg.backups {
		if b.Type == recipe.TypeCustom && !b.Enabled {
			profiles = append(profiles, recipe.Profile(b.Name))
		}
	}
	args := r.composeArgsFor(appID, files, profiles, append([]string{"rm", "--force", "--stop"}, services...)...)
	return r.composeExec(ctx, appID, args)
}
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
	openStorage     func(s model.Storage, secret string) (store.Store, error)
	runningProjects func(ctx context.Context) (map[string]bool, error)
	redeploy        func(ctx context.Context, appID string) error
}
```

**with:**

```go
	openStorage     func(s model.Storage, secret string) (store.Store, error)
	runningProjects func(ctx context.Context) (map[string]bool, error)
	redeploy        func(ctx context.Context, appID string) error
	// runCompose runs one docker compose command in an app's folder
	// (replaced in tests to record the arguments).
	runCompose func(ctx context.Context, dir string, args []string) error
}
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
		openStorage:   store.Open,
	}
```

**with:**

```go
		openStorage:   store.Open,
		runCompose:    dockerIn,
	}
```

**In `internal/infra/orchestrator/docker_compose/operations.go`, replace:**

```go
func (r *Repository) SaveApp(ctx context.Context, app command.AppPayload) (string, error) {
	hash, err := r.saveWithoutDeploy(app)
	if err != nil {
		return "", err
	}
	if err := r.composeUp(ctx, app.AppID); err != nil {
		return "", fmt.Errorf("docker compose up: %w", err)
	}
	r.markDeployed(app.AppID, hash)
	return hash, nil
}

// SaveAppDraft persists and commits without deploying: the commit becomes a
// draft sitting ahead of the deployed mark until the user deploys it.
func (r *Repository) SaveAppDraft(ctx context.Context, app command.AppPayload) (string, error) {
	return r.saveWithoutDeploy(app)
}
```

**with:**

```go
func (r *Repository) SaveApp(ctx context.Context, app command.AppPayload) (string, error) {
	hash, _, err := r.SaveAppResult(ctx, app, false)
	return hash, err
}

// SaveAppDraft persists and commits without deploying: the commit becomes a
// draft sitting ahead of the deployed mark until the user deploys it.
func (r *Repository) SaveAppDraft(ctx context.Context, app command.AppPayload) (string, error) {
	hash, _, err := r.SaveAppResult(ctx, app, true)
	return hash, err
}

// SaveAppResult is SaveApp (or, draft, SaveAppDraft) with the save's
// warnings.
func (r *Repository) SaveAppResult(ctx context.Context, app command.AppPayload, draft bool) (string, []string, error) {
	hash, warnings, err := r.save(app)
	if err != nil || draft {
		return hash, warnings, err
	}
	if err := r.composeUp(ctx, app.AppID); err != nil {
		return "", warnings, fmt.Errorf("docker compose up: %w", err)
	}
	r.markDeployed(app.AppID, hash)
	return hash, warnings, nil
}
```

**In `internal/infra/orchestrator/docker_compose/operations.go`, replace:**

```go
func (r *Repository) saveWithoutDeploy(app command.AppPayload) (string, error) {
	dir := r.appDataDir(app.AppID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := gitEnsure(dir); err != nil {
		return "", fmt.Errorf("init app repo: %w", err)
	}

	store, err := r.writeAppStore(dir, app)
	if err != nil {
		return "", fmt.Errorf("write app store: %w", err)
	}

	// Git-sourced app: sync the upstream and pin its head BEFORE committing,
	// so the lock lands inside this save's commit.
	if spec := r.appSourceSpec(dir); spec != nil {
		if _, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), ""); err != nil {
			return "", fmt.Errorf("sync source: %w", err)
		}
	}

	name := r.appName(dir, app.AppID)
	hash, err := gitCommitAll(dir, "save "+name)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	if err := r.materializeSecrets(dir, store); err != nil {
		return "", fmt.Errorf("materialize secrets: %w", err)
	}
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", app.AppID, name); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", app.AppID, "error", err)
	}
	return hash, nil
}
```

**with:**

```go
func (r *Repository) saveWithoutDeploy(app command.AppPayload) (string, error) {
	hash, _, err := r.save(app)
	return hash, err
}

// save is saveWithoutDeploy with the warnings of the save: backup settings
// dropped because the compose file no longer has their backup.
func (r *Repository) save(app command.AppPayload) (string, []string, error) {
	dir := r.appDataDir(app.AppID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	if err := gitEnsure(dir); err != nil {
		return "", nil, fmt.Errorf("init app repo: %w", err)
	}

	store, err := r.writeAppStore(dir, app)
	if err != nil {
		return "", nil, fmt.Errorf("write app store: %w", err)
	}

	// Git-sourced app: sync the upstream and pin its head BEFORE committing,
	// so the lock lands inside this save's commit.
	if spec := r.appSourceSpec(dir); spec != nil {
		if _, err := r.ensureSource(context.Background(), dir, *spec, r.sourceTokenPlaintext(dir), ""); err != nil {
			return "", nil, fmt.Errorf("sync source: %w", err)
		}
	}

	// The backups must be valid against the compose file being saved (for
	// a git-sourced app, the one just pulled); nothing is committed if not.
	warnings, err := r.cleanBackups(dir)
	if err != nil {
		r.undoSave(dir)
		return "", nil, err
	}

	name := r.appName(dir, app.AppID)
	hash, err := gitCommitAll(dir, "save "+name)
	if err != nil {
		return "", nil, fmt.Errorf("commit: %w", err)
	}

	if err := r.materializeSecrets(dir, store); err != nil {
		return "", nil, fmt.Errorf("materialize secrets: %w", err)
	}
	if _, err := ensureAppSymlink(r.cfg.GetAppsDir(), "apps-data", app.AppID, name); err != nil {
		r.log.Warn("failed to update app symlink", "app_id", app.AppID, "error", err)
	}
	return hash, warnings, nil
}

// undoSave puts an app's folder back the way its last commit left it after
// a save that was refused before committing; a new app (no commit yet) is
// removed, so no half-saved app is ever listed.
func (r *Repository) undoSave(dir string) {
	head, err := gitHead(dir)
	if err != nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			r.log.Warn("undo a refused save", "dir", dir, "error", rmErr)
		}
		return
	}
	if err := gitRestore(dir, head); err != nil {
		r.log.Warn("undo a refused save", "dir", dir, "error", err)
	}
}
```

**In `internal/infra/orchestrator/docker_compose/operations.go`, replace:**

```go
func (r *Repository) composeArgs(appID string, verb ...string) []string {
	return r.composeArgsFor(appID, r.appComposeFiles(r.appDataDir(appID)), verb...)
}

// composeArgsFor is composeArgs with the app's compose files already
// resolved.
func (r *Repository) composeArgsFor(appID string, files []string, verb ...string) []string {
```

**with:**

```go
func (r *Repository) composeArgs(appID string, verb ...string) []string {
	return r.composeArgsFor(appID, r.appComposeFiles(r.appDataDir(appID)), nil, verb...)
}

// composeArgsFor is composeArgs with the app's compose files already
// resolved and the profiles to activate (--profile; none: compose's own
// COMPOSE_PROFILES handling).
func (r *Repository) composeArgsFor(appID string, files, profiles []string, verb ...string) []string {
```

**In `internal/infra/orchestrator/docker_compose/operations.go`, replace:**

```go
			args = append(args, "--env-file", rel)
		}
	}
	return append(args, verb...)
}
```

**with:**

```go
			args = append(args, "--env-file", rel)
		}
	}
	for _, p := range profiles {
		args = append(args, "--profile", p)
	}
	return append(args, verb...)
}
```

**In `internal/infra/orchestrator/docker_compose/operations.go`, replace:**

```go
// composeExec runs docker with args in the app's folder.
func (r *Repository) composeExec(ctx context.Context, appID string, args []string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = r.appDataDir(appID)
```

**with:**

```go
// composeExec runs docker with args in the app's folder.
func (r *Repository) composeExec(ctx context.Context, appID string, args []string) error {
	return r.runCompose(ctx, r.appDataDir(appID), args)
}

// dockerIn runs docker with args in dir; the error carries its output.
func dockerIn(ctx context.Context, dir string, args []string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
```

**In `internal/infra/orchestrator/docker_compose/operations.go`, replace:**

```go
	dir := r.appDataDir(appID)
	files := r.appComposeFiles(dir)
	// The storage variables are resolved on every deploy: a storage removed
	// or changed since the last one must not be served stale.
	if err := r.materializeStorageEnv(ctx, dir, files); err != nil {
		return err
	}
	// Refresh the manual-run helper alongside every deploy; never fail one on it.
	if err := r.writeRunHelper(appID, files); err != nil {
		r.log.Warn("write manual-run helper", "app_id", appID, "error", err)
	}
	return r.composeExec(ctx, appID, r.composeArgsFor(appID, files, "up", "-d", "--remove-orphans"))
}
```

**with:**

```go
	dir := r.appDataDir(appID)
	files := r.appComposeFiles(dir)
	// The backups are validated on every deploy: a git-sourced app's compose
	// file changes upstream.
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return err
	}
	// The storage variables are resolved on every deploy: a storage removed
	// or changed since the last one must not be served stale.
	if err := r.materializeStorageEnv(ctx, dir, files, cfg.backups); err != nil {
		return err
	}
	profiles := r.composeProfiles(dir, cfg.backups)
	// Refresh the manual-run helper alongside every deploy; never fail one on it.
	if err := r.writeRunHelper(appID, files, profiles); err != nil {
		r.log.Warn("write manual-run helper", "app_id", appID, "error", err)
	}
	if err := r.composeExec(ctx, appID, r.composeArgsFor(appID, files, profiles, "up", "-d", "--remove-orphans")); err != nil {
		return err
	}
	return r.stopOffServices(ctx, appID, files, cfg, profiles)
}
```

**In `internal/infra/orchestrator/docker_compose/store.go`, replace:**

```go
	if err := os.WriteFile(filepath.Join(dir, configRel), app.Config, 0o644); err != nil {
```

**with:**

```go
	prevConfig, _ := os.ReadFile(filepath.Join(dir, configRel))
	if err := os.WriteFile(filepath.Join(dir, configRel), keepBackups(app.Config, prevConfig), 0o644); err != nil {
```

**In `internal/infra/orchestrator/docker_compose/runhelper.go`, replace:**

```go
// runHelperScript renders the helper for a project and its compose files
// (names are single-quoted for the shell). The env files are added in the
// order winterflow passes them, each only when it exists.
func runHelperScript(project string, files []string) []byte {
	cmd := "exec docker compose --project-name " + project
	for _, f := range files {
		cmd += " -f " + shellQuote(f)
	}
```

**with:**

```go
// runHelperScript renders the helper for a project, its compose files and
// the profiles winterflow activates (names are single-quoted for the
// shell). The env files are added in the order winterflow passes them, each
// only when it exists.
func runHelperScript(project string, files, profiles []string) []byte {
	cmd := "exec docker compose --project-name " + project
	for _, f := range files {
		cmd += " -f " + shellQuote(f)
	}
	for _, p := range profiles {
		cmd += " --profile " + shellQuote(p)
	}
```

**In `internal/infra/orchestrator/docker_compose/runhelper.go`, replace:**

```go
// writeRunHelper (re)writes the app's manual-run helper for its compose
// files.
func (r *Repository) writeRunHelper(appID string, files []string) error {
	dir := r.appDataDir(appID)
	script := runHelperScript(projectName(appID), files)
```

**with:**

```go
// writeRunHelper (re)writes the app's manual-run helper for its compose
// files and profiles.
func (r *Repository) writeRunHelper(appID string, files, profiles []string) error {
	dir := r.appDataDir(appID)
	script := runHelperScript(projectName(appID), files, profiles)
```

**In `internal/infra/orchestrator/docker_compose/storageenv.go`, replace:**

```go
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)
```

**with:**

```go
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)
```

**In `internal/infra/orchestrator/docker_compose/storageenv.go`, replace:**

```go
// materializeStorageEnv writes storage.env with exactly the variables the
// compose files reference — an app never receives credentials for a
// storage it doesn't mention — or removes it when they reference none. A
// reference to a storage this server doesn't have (deleted, or added to a
// git-sourced compose file upstream) fails the deploy, and so does a
// referenced local storage whose folder is missing (see checkStorageFolders).
// An unchanged storage.env is not rewritten.
func (r *Repository) materializeStorageEnv(ctx context.Context, dir string, files []string) error {
	refs, err := r.storageRefs(dir, files)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, storageEnvRel)
	if len(refs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	// Only the referenced registrations are read.
	byName := map[string]model.Storage{}
	var readErr error
	parsed, err := model.CheckStorageRefs(refs, func(name string) bool {
		if _, ok := byName[name]; ok {
			return true
		}
		s, ok, err := r.storages.Get(name)
		if err != nil && readErr == nil {
			readErr = err
		}
		if ok {
			byName[name] = s
		}
		return ok
	}, nil)
	if readErr != nil {
		return readErr
	}
	if err != nil {
		return err
	}
	var folders []model.Storage
	for _, s := range byName {
		if s.Type == model.StorageLocal && s.Local != nil {
			folders = append(folders, s)
		}
	}
	sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
	if err := r.checkStorageFolders(ctx, folders); err != nil {
		return err
	}
	secrets := map[string]string{} // each storage's secret decrypted once
	vars := make([]resolvedItem, 0, len(parsed))
	for _, ref := range parsed {
		s := byName[ref.Storage]
		secret := ""
		if ref.Secret() && s.S3 != nil {
			if _, done := secrets[s.Name]; !done {
				if secrets[s.Name], err = r.storageSecret(s); err != nil {
					return err
				}
			}
			secret = secrets[s.Name]
		}
		vars = append(vars, resolvedItem{name: ref.Var, content: []byte(model.StorageFieldValue(s, ref.Field, secret))})
	}
	content, err := marshalStorageEnv(vars)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(old, content) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	// WriteFile's perm only applies on create.
	return os.Chmod(path, 0o600)
}

```

**with:**

```go
// materializeStorageEnv writes storage.env with exactly the variables the
// compose files reference — an app never receives credentials for a
// storage it doesn't mention — or removes it when they reference none. A
// WF_BACKUP_ variable takes its value from the storage of one of the app's
// backups (backups), or is empty while that backup is off. A reference to a
// storage this server doesn't have (deleted, or added to a git-sourced
// compose file upstream) or to a backup the app doesn't declare fails the
// deploy, and so does a referenced local storage whose folder is missing
// (see checkStorageFolders). An unchanged storage.env is not rewritten.
func (r *Repository) materializeStorageEnv(ctx context.Context, dir string, files []string, backups []recipe.EffectiveBackup) error {
	content, err := r.renderStorageEnv(ctx, dir, files, backups, true)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, storageEnvRel)
	if content == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(old, content) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	// WriteFile's perm only applies on create.
	return os.Chmod(path, 0o600)
}

// renderStorageEnv is the content materializeStorageEnv writes (nil: no
// references). checkFolders also checks the referenced local storages'
// folders.
func (r *Repository) renderStorageEnv(ctx context.Context, dir string, files []string, backups []recipe.EffectiveBackup, checkFolders bool) ([]byte, error) {
	refs, err := r.storageRefs(dir, files)
	if err != nil || len(refs) == 0 {
		return nil, err
	}
	declared := recipe.Declared(backups)
	// Only the referenced registrations are read.
	byName := map[string]model.Storage{}
	var readErr error
	lookup := func(name string) bool {
		if _, ok := byName[name]; ok {
			return true
		}
		s, ok, err := r.storages.Get(name)
		if err != nil && readErr == nil {
			readErr = err
		}
		if ok {
			byName[name] = s
		}
		return ok
	}
	parsed, err := model.CheckStorageRefs(refs, lookup, func(name string) bool { _, ok := declared[name]; return ok })
	if readErr != nil {
		return nil, readErr
	}
	if err != nil {
		return nil, err
	}
	// A backup that is on resolves to its storage, which must exist.
	for _, ref := range parsed {
		if b := declared[ref.Backup]; ref.Backup != "" && b.Enabled && !lookup(b.Storage) {
			if readErr != nil {
				return nil, readErr
			}
			return nil, fmt.Errorf("%w: ${%s}: the backup %s uses the storage %q, which this server doesn't have", model.ErrStorageRef, ref.Var, b.Name, b.Storage)
		}
	}
	if checkFolders {
		var folders []model.Storage
		for _, s := range byName {
			if s.Type == model.StorageLocal && s.Local != nil {
				folders = append(folders, s)
			}
		}
		sort.Slice(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
		if err := r.checkStorageFolders(ctx, folders); err != nil {
			return nil, err
		}
	}
	appID := filepath.Base(dir)
	secrets := map[string]string{} // each storage's secret decrypted once
	vars := make([]resolvedItem, 0, len(parsed))
	for _, ref := range parsed {
		name, b := ref.Storage, declared[ref.Backup]
		if ref.Backup != "" {
			if !b.Enabled {
				vars = append(vars, resolvedItem{name: ref.Var}) // off: empty
				continue
			}
			name = b.Storage
		}
		s := byName[name]
		secret := ""
		if ref.Secret() && s.S3 != nil {
			if _, done := secrets[s.Name]; !done {
				if secrets[s.Name], err = r.storageSecret(s); err != nil {
					return nil, err
				}
			}
			secret = secrets[s.Name]
		}
		value := model.StorageFieldValue(s, ref.Field, secret)
		if ref.Backup != "" {
			value = model.BackupFieldValue(s, ref.Field, secret, appID, ref.Backup)
		}
		vars = append(vars, resolvedItem{name: ref.Var, content: []byte(value)})
	}
	return marshalStorageEnv(vars)
}

```

**In `internal/infra/orchestrator/docker_compose/storageenv.go`, replace:**

```go
// storageVariables describes the referenced WF_STORAGE_ variables for the
// Variables panel: names and storages, never values.
func storageVariables(refs []string) []command.StorageVariable {
	var out []command.StorageVariable
	for _, v := range refs {
		ref, err := model.ParseStorageRef(v)
		if err != nil {
			continue
		}
		out = append(out, command.StorageVariable{Name: ref.Var, Storage: ref.Storage, Backup: ref.Backup, Secret: ref.Secret()})
	}
	return out
}
```

**with:**

```go
// storageVariables describes the referenced WF_STORAGE_ and WF_BACKUP_
// variables for the Variables panel: names and storages (a backup's: the
// one it uses while on), never values.
func storageVariables(refs []string, backups []recipe.EffectiveBackup) []command.StorageVariable {
	declared := recipe.Declared(backups)
	var out []command.StorageVariable
	for _, v := range refs {
		ref, err := model.ParseStorageRef(v)
		if err != nil {
			continue
		}
		storage := ref.Storage
		if b, ok := declared[ref.Backup]; ok && b.Enabled {
			storage = b.Storage
		}
		out = append(out, command.StorageVariable{Name: ref.Var, Storage: storage, Backup: ref.Backup, Secret: ref.Secret()})
	}
	return out
}
```

**In `internal/infra/orchestrator/docker_compose/lifecycle.go`, replace:**

```go
	resp.App = payload
	// The WF_STORAGE_ variables the compose files reference: read-only rows
	// in the editor. An unreadable compose file just shows none.
	if refs, err := r.storageRefs(dir, r.appComposeFiles(dir)); err == nil {
		resp.StorageVariables = storageVariables(refs)
	}
	return resp, nil
```

**with:**

```go
	resp.App = payload
	files := r.appComposeFiles(dir)
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		resp.BackupsError = err.Error()
	}
	resp.Backups = cfg.backups
	// The WF_STORAGE_ / WF_BACKUP_ variables the compose files reference:
	// read-only rows in the editor. An unreadable compose file just shows
	// none.
	if refs, err := r.storageRefs(dir, files); err == nil {
		resp.StorageVariables = storageVariables(refs, cfg.backups)
	}
	return resp, nil
```

**In `internal/infra/orchestrator/docker_compose/storages.go`, replace:**

```go
// storageUsers maps each storage name to the ids of the apps (in id order)
// whose compose files reference it. File reads only, on request — never
// polled. An app whose compose files can't be read is reported in the error
// while the map keeps every app read; callers that must not miss a user
// refuse on the error.
```

**with:**

```go
// storageUsers maps each storage name to the ids of the apps (in id order)
// whose compose files reference it or whose enabled backups use it. File
// reads only, on request — never polled. An app whose compose files can't
// be read is reported in the error while the map keeps every app read;
// callers that must not miss a user refuse on the error.
```

**In `internal/infra/orchestrator/docker_compose/storages.go`, replace:**

```go
		dir := filepath.Join(r.cfg.GetAppsDataDir(), e.Name())
		refs, err := r.storageRefs(dir, r.appComposeFiles(dir))
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		seen := map[string]bool{}
		for _, v := range refs {
			ref, err := model.ParseStorageRef(v)
			if err != nil || ref.Storage == "" || seen[ref.Storage] {
				continue
			}
			seen[ref.Storage] = true
			users[ref.Storage] = append(users[ref.Storage], e.Name())
		}
```

**with:**

```go
		dir := filepath.Join(r.cfg.GetAppsDataDir(), e.Name())
		files := r.appComposeFiles(dir)
		refs, err := r.storageRefs(dir, files)
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		var used []string
		for _, v := range refs {
			if ref, err := model.ParseStorageRef(v); err == nil && ref.Storage != "" {
				used = append(used, ref.Storage)
			}
		}
		cfg, err := r.appBackups(dir, files)
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
		}
		for _, b := range cfg.backups {
			if b.Enabled {
				used = append(used, b.Storage)
			}
		}
		seen := map[string]bool{}
		for _, name := range used {
			if !seen[name] {
				seen[name] = true
				users[name] = append(users[name], e.Name())
			}
		}
```

**In `internal/infra/orchestrator/docker_compose/storages.go`, replace:**

```go
		return fmt.Errorf("%s is used by %s: remove its WF_STORAGE_ variables from those apps first", name, strings.Join(names, ", "))
```

**with:**

```go
		return fmt.Errorf("%s is used by %s: remove its WF_STORAGE_ variables from those apps and turn off their backups to it first", name, strings.Join(names, ", "))
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
			save := d.orch.SaveApp
			if in.Draft {
				save = d.orch.SaveAppDraft
			}
			hash, err := save(ctx, in.App)
			resp := command.SaveAppResponse{AppID: in.App.AppID, Revision: hash}
			if err == nil {
				resp.Warnings = d.reloadIngress(ctx)
			}
			return resp, err
```

**with:**

```go
			hash, warnings, err := d.orch.SaveAppResult(ctx, in.App, in.Draft)
			resp := command.SaveAppResponse{AppID: in.App.AppID, Revision: hash, Warnings: warnings}
			if err == nil {
				resp.Warnings = append(resp.Warnings, d.reloadIngress(ctx)...)
			}
			return resp, err
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/infra/orchestrator/docker_compose/ ./internal/app/agent/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/infra/orchestrator/docker_compose/backupconfig_test.go internal/infra/orchestrator/docker_compose/runhelper_test.go internal/infra/orchestrator/docker_compose/storageenv_test.go internal/infra/orchestrator/docker_compose/storages_test.go internal/infra/orchestrator/docker_compose/backupconfig.go internal/infra/orchestrator/docker_compose/repository.go internal/infra/orchestrator/docker_compose/operations.go internal/infra/orchestrator/docker_compose/store.go internal/infra/orchestrator/docker_compose/runhelper.go internal/infra/orchestrator/docker_compose/storageenv.go internal/infra/orchestrator/docker_compose/lifecycle.go internal/infra/orchestrator/docker_compose/storages.go internal/app/agent/dispatcher.go
git commit -F - <<'EOF'
feat(agent): backups in the deployment: overrides, WF_BACKUP_ values and profiles

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 7: Mount resolution through `docker compose config`

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/mounts.go`, `mounts_test.go`
- Modify: `repository.go` (`composeOutput` seam, `mountCache` + its pruning in `forgetApp`), `operations.go` (`composeUp` refuses a deploy whose enabled backup can't reach its data)

**Interfaces:**
- Consumes: `appBackups`, `composeArgsFor(appID, files, profiles, verb...)` (Task 6); `recipe.EffectiveBackup` (Task 2).
- Produces (package `dockercompose`): `type composeModel` (services' `volumes` + top-level `volumes` with resolved `name`); `type helperMount struct{ Volume, Bind string }`; `type helperPath struct{ Mount int; Sub, Entry string }` with `Src() string` (`/src/<n>[/<sub>]`); `type helperPlan struct{ Mounts []helperMount; Paths []helperPath }`; `resolveBackup(m composeModel, b recipe.EffectiveBackup, isFile func(string) bool) (helperPlan, error)`; `(r).appComposeModel(ctx, appID string, files []string) (composeModel, error)` (one `docker compose … --profile * config --format json`, 30s, cached by HEAD + `storage.env` + `.env.secrets`); `(r).resolveBackups(ctx, appID string, files []string, backups []recipe.EffectiveBackup) (map[string]helperPlan, error)` (enabled sqlite/files only; no exec without one); `Repository.composeOutput func(ctx, dir string, args []string) ([]byte, error)` (default `dockerOutputIn`)

- [ ] **Step 1: Write the failing tests**

**Create `internal/infra/orchestrator/docker_compose/mounts_test.go`:**

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
)

// configJSON is `docker compose --profile '*' config --format json` of an
// app wf-app-1 whose project directory is /data/apps-data/app-1 (relative
// binds resolved against it; a git-sourced app's against source/).
const configJSON = `{
  "name": "wf-app-1",
  "services": {
    "web": {
      "image": "example/web",
      "volumes": [
        {"type": "volume", "source": "data", "target": "/data", "volume": {}},
        {"type": "bind", "source": "/data/apps-data/app-1/media", "target": "/app/media", "bind": {}},
        {"type": "bind", "source": "/srv/shared", "target": "/srv/shared", "read_only": true, "bind": {}},
        {"type": "bind", "source": "/data/apps-data/app-1/settings.json", "target": "/app/settings.json", "bind": {}},
        {"type": "tmpfs", "target": "/tmp/cache"},
        {"type": "volume", "source": "ext", "target": "/ext", "volume": {}},
        {"type": "volume", "source": "named", "target": "/named", "volume": {}},
        {"type": "volume", "target": "/anon", "volume": {}},
        {"type": "npipe", "source": "x", "target": "/pipe"}
      ]
    },
    "worker": {
      "image": "example/web",
      "volumes": [{"type": "bind", "source": "/data/apps-data/app-1/source/uploads", "target": "/uploads"}]
    }
  },
  "volumes": {
    "data": {"name": "wf-app-1_data"},
    "ext": {"name": "ext", "external": true},
    "named": {"name": "custom-name"}
  }
}`

func testModel(t *testing.T) composeModel {
	t.Helper()
	var m composeModel
	if err := json.Unmarshal([]byte(configJSON), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func isSettingsFile(p string) bool { return strings.HasSuffix(p, "/settings.json") }

func TestResolveBackup(t *testing.T) {
	m := testModel(t)
	for _, tc := range []struct {
		name string
		b    recipe.EffectiveBackup
		want helperPlan
	}{
		{"sqlite on a volume, the project prefix applied",
			recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Volume: "data", Path: "app.db"},
			helperPlan{Mounts: []helperMount{{Volume: "wf-app-1_data"}}, Paths: []helperPath{{Sub: "app.db", Entry: "app.db"}}}},
		{"a whole volume, name: honored",
			recipe.EffectiveBackup{Name: "all", Type: recipe.TypeFiles, Volume: "named"},
			helperPlan{Mounts: []helperMount{{Volume: "custom-name"}}, Paths: []helperPath{{}}}},
		{"files on an external volume",
			recipe.EffectiveBackup{Name: "ext", Type: recipe.TypeFiles, Volume: "ext", Paths: []string{"a", "b/c"}},
			helperPlan{Mounts: []helperMount{{Volume: "ext"}}, Paths: []helperPath{{Sub: "a", Entry: "a"}, {Sub: "b/c", Entry: "b/c"}}}},
		{"sqlite through a service's volume",
			recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Service: "web", Path: "/data/sub/app.db"},
			helperPlan{Mounts: []helperMount{{Volume: "wf-app-1_data"}}, Paths: []helperPath{{Sub: "sub/app.db", Entry: "data/sub/app.db"}}}},
		{"files spanning a relative bind, an absolute bind and a volume; one mount each",
			recipe.EffectiveBackup{Name: "media", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/app/media", "/srv/shared/x", "/data/cfg", "/app/media/sub"}},
			helperPlan{
				Mounts: []helperMount{{Bind: "/data/apps-data/app-1/media"}, {Bind: "/srv/shared"}, {Volume: "wf-app-1_data"}},
				Paths: []helperPath{{Mount: 0, Entry: "app/media"}, {Mount: 1, Sub: "x", Entry: "srv/shared/x"},
					{Mount: 2, Sub: "cfg", Entry: "data/cfg"}, {Mount: 0, Sub: "sub", Entry: "app/media/sub"}}}},
		{"a git-sourced app's bind into source/",
			recipe.EffectiveBackup{Name: "up", Type: recipe.TypeFiles, Service: "worker", Paths: []string{"/uploads"}},
			helperPlan{Mounts: []helperMount{{Bind: "/data/apps-data/app-1/source/uploads"}}, Paths: []helperPath{{Entry: "uploads"}}}},
	} {
		got, err := resolveBackup(m, tc.b, isSettingsFile)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %+v, %v\nwant %+v", tc.name, got, err, tc.want)
		}
	}
	if src := (helperPath{Mount: 2, Sub: "a/b"}).Src(); src != "/src/2/a/b" {
		t.Fatalf("src = %s", src)
	}
	if src := (helperPath{Mount: 0}).Src(); src != "/src/0" {
		t.Fatalf("src = %s", src)
	}
}

func TestResolveBackupErrors(t *testing.T) {
	m := testModel(t)
	for _, tc := range []struct {
		b    recipe.EffectiveBackup
		want string
	}{
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/etc/app"}},
			"backup a: /etc/app is not on a mount of service web: the container's own files are lost"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/app/settings.json"}},
			"is a single-file bind mount of service web (/data/apps-data/app-1/settings.json), which can't be replaced from another container: mount its directory instead"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/tmp/cache/x"}},
			"/tmp/cache/x is on a tmpfs mount of service web"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/anon"}},
			"is on an anonymous volume of service web"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "web", Paths: []string{"/pipe"}},
			"is on a npipe mount"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeSQLite, Service: "web", Path: "/data"},
			"/data is the mount itself, not the database file inside it"},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Service: "api", Paths: []string{"/x"}},
			`compose has no service "api"`},
		{recipe.EffectiveBackup{Name: "a", Type: recipe.TypeFiles, Volume: "gone"},
			`compose has no volume "gone"`},
	} {
		if _, err := resolveBackup(m, tc.b, isSettingsFile); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.b, err, tc.want)
		}
	}
}

// fakeComposeConfig answers `docker compose config` with out (or err) and
// counts the calls.
type fakeComposeConfig struct {
	out   string
	err   error
	calls [][]string
}

func (f *fakeComposeConfig) install(r *Repository) {
	r.composeOutput = func(_ context.Context, _ string, args []string) ([]byte, error) {
		f.calls = append(f.calls, args)
		return []byte(f.out), f.err
	}
}

// The model comes from one compose config per commit: cached until HEAD or
// a derived env file changes, forgotten with the app.
func TestAppComposeModelIsCached(t *testing.T) {
	r := newTestRepo(t)
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	fake := &fakeComposeConfig{out: configJSON}
	fake.install(r)
	dir := r.appDataDir("app-1")
	cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
	if err != nil {
		t.Fatal(err)
	}
	resolve := func() map[string]helperPlan {
		t.Helper()
		plans, err := r.resolveBackups(context.Background(), "app-1", r.appComposeFiles(dir), cfg.backups)
		if err != nil {
			t.Fatal(err)
		}
		return plans
	}
	plans := resolve()
	if len(plans) != 1 || plans["database"].Mounts[0].Volume != "wf-app-1_data" {
		t.Fatalf("plans = %+v", plans)
	}
	if args := strings.Join(fake.calls[0], " "); !strings.Contains(args, "--profile * config --format json") {
		t.Fatalf("args = %s", args)
	}
	resolve()
	if len(fake.calls) != 1 {
		t.Fatalf("compose config ran %d times for one commit", len(fake.calls))
	}
	writeFileT(t, dir, storageEnvRel, "WF_STORAGE_X_TYPE=local\n")
	resolve()
	if len(fake.calls) != 2 {
		t.Fatal("a changed storage.env must re-read the model")
	}
	writeFileT(t, dir, "notes.txt", "x")
	if _, err := gitCommitAll(dir, "another"); err != nil {
		t.Fatal(err)
	}
	resolve()
	if len(fake.calls) != 3 {
		t.Fatal("a new commit must re-read the model")
	}
	r.forgetApp("app-1")
	resolve()
	if len(fake.calls) != 4 {
		t.Fatal("forgetApp must drop the model")
	}
	// Nothing enabled to resolve: no exec at all.
	if _, err := r.resolveBackups(context.Background(), "app-1", nil, []recipe.EffectiveBackup{{Name: "x", Type: recipe.TypeSQLite}}); err != nil || len(fake.calls) != 4 {
		t.Fatalf("resolved a disabled backup: %v, %d calls", err, len(fake.calls))
	}
	fake.out = "not json"
	r.forgetApp("app-1")
	if _, err := r.resolveBackups(context.Background(), "app-1", nil, cfg.backups); err == nil {
		t.Fatal("bad compose config output passed")
	}
	fake.err = errors.New("exit status 1: no such service")
	if _, err := r.resolveBackups(context.Background(), "app-1", nil, cfg.backups); err == nil || !strings.Contains(err.Error(), "no such service") {
		t.Fatalf("err = %v", err)
	}
}

// A deploy is refused when an enabled backup can't reach its data.
func TestComposeUpRefusesAnUnreachableBackup(t *testing.T) {
	r := newTestRepo(t)
	var calls composeCalls
	calls.install(r)
	(&fakeComposeConfig{out: configJSON}).install(r)
	_, err := r.SaveApp(context.Background(), command.AppPayload{AppID: "app-1",
		Config: []byte(withBackups(`[{"name":"media","enabled":true,"storage":"disk","paths":["/etc/app"]}]`)),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupCompose)}}})
	if err == nil || !strings.Contains(err.Error(), "/etc/app is not on a mount of service web") {
		t.Fatalf("err = %v", err)
	}
	if len(calls.args) != 0 {
		t.Fatalf("compose up ran: %v", calls.args)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/orchestrator/docker_compose/`

Expected: build failure: `undefined: composeModel`, `undefined: resolveBackup`, `r.composeOutput undefined`, ….

- [ ] **Step 3: Implement**

**Create `internal/infra/orchestrator/docker_compose/mounts.go`:**

```go
package dockercompose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"winterflow/internal/domain/backup/recipe"
)

// composeConfigTimeout bounds one `docker compose config`.
const composeConfigTimeout = 30 * time.Second

// composeModel is what backups read of `docker compose config --format
// json`: compose has resolved every name (the project prefix, name:,
// external:) and made relative bind sources absolute.
type composeModel struct {
	Services map[string]struct {
		Volumes []composeMount `json:"volumes"`
	} `json:"services"`
	Volumes map[string]struct {
		Name string `json:"name"`
	} `json:"volumes"`
}

// composeMount is one entry of a service's volumes.
type composeMount struct {
	Type   string `json:"type"` // volume | bind | tmpfs | ...
	Source string `json:"source"`
	Target string `json:"target"`
}

// helperMount is one source the helper container mounts, at /src/<n>: a
// Docker volume or an absolute host path, both already mounted by the app.
type helperMount struct {
	Volume string
	Bind   string
}

// helperPath is one path of a backup in the helper: Sub below mount Mount
// ("" = the mount itself), archived as Entry ("" = the archive root).
type helperPath struct {
	Mount int
	Sub   string
	Entry string
}

// Src is the path in the helper.
func (p helperPath) Src() string {
	src := "/src/" + strconv.Itoa(p.Mount)
	if p.Sub != "" {
		src += "/" + p.Sub
	}
	return src
}

// helperPlan is how the helper reaches a backup's data.
type helperPlan struct {
	Mounts []helperMount
	Paths  []helperPath
}

// mountCacheEntry is an app's composeModel, valid while key (HEAD and the
// derived env files) is unchanged.
type mountCacheEntry struct {
	key   string
	model composeModel
}

// resolveBackup maps a sqlite or files backup's paths to the sources the
// helper mounts. isFile reports whether a host path is a regular file (a
// single-file bind mount).
func resolveBackup(m composeModel, b recipe.EffectiveBackup, isFile func(string) bool) (helperPlan, error) {
	var plan helperPlan
	if b.Volume != "" {
		v, ok := m.Volumes[b.Volume]
		if !ok || v.Name == "" {
			return helperPlan{}, fmt.Errorf("backup %s: compose has no volume %q", b.Name, b.Volume)
		}
		plan.Mounts = []helperMount{{Volume: v.Name}}
		for _, p := range backupPaths(b) {
			plan.Paths = append(plan.Paths, helperPath{Sub: p, Entry: p})
		}
		if len(plan.Paths) == 0 { // the whole volume
			plan.Paths = []helperPath{{}}
		}
		return plan, nil
	}
	svc, ok := m.Services[b.Service]
	if !ok {
		return helperPlan{}, fmt.Errorf("backup %s: compose has no service %q", b.Name, b.Service)
	}
	index := map[helperMount]int{}
	for _, p := range backupPaths(b) {
		mnt, ok := mountOf(svc.Volumes, p)
		if !ok {
			return helperPlan{}, fmt.Errorf("backup %s: %s is not on a mount of service %s: the container's own files are lost whenever it is recreated, so mount a volume or a folder there", b.Name, p, b.Service)
		}
		var hm helperMount
		switch mnt.Type {
		case "volume":
			v, ok := m.Volumes[mnt.Source]
			if mnt.Source == "" || !ok || v.Name == "" {
				return helperPlan{}, fmt.Errorf("backup %s: %s is on an anonymous volume of service %s, which can't be backed up: give the volume a name", b.Name, p, b.Service)
			}
			hm.Volume = v.Name
		case "bind":
			if isFile(mnt.Source) {
				return helperPlan{}, fmt.Errorf("backup %s: %s is a single-file bind mount of service %s (%s), which can't be replaced from another container: mount its directory instead", b.Name, p, b.Service, mnt.Source)
			}
			hm.Bind = mnt.Source
		case "tmpfs":
			return helperPlan{}, fmt.Errorf("backup %s: %s is on a tmpfs mount of service %s, which keeps nothing to back up", b.Name, p, b.Service)
		default:
			return helperPlan{}, fmt.Errorf("backup %s: %s is on a %s mount of service %s, which can't be backed up", b.Name, p, mnt.Type, b.Service)
		}
		sub := strings.TrimPrefix(strings.TrimPrefix(p, mnt.Target), "/")
		if b.Type == recipe.TypeSQLite && sub == "" {
			return helperPlan{}, fmt.Errorf("backup %s: %s is the mount itself, not the database file inside it", b.Name, p)
		}
		n, seen := index[hm]
		if !seen {
			n = len(plan.Mounts)
			index[hm] = n
			plan.Mounts = append(plan.Mounts, hm)
		}
		plan.Paths = append(plan.Paths, helperPath{Mount: n, Sub: sub, Entry: strings.TrimPrefix(p, "/")})
	}
	return plan, nil
}

// backupPaths is a backup's path (sqlite) or paths (files).
func backupPaths(b recipe.EffectiveBackup) []string {
	if b.Type == recipe.TypeSQLite {
		return []string{b.Path}
	}
	return b.Paths
}

// mountOf finds the mount holding the container path p: the one with the
// longest target that is p or a folder above it.
func mountOf(mounts []composeMount, p string) (composeMount, bool) {
	var best composeMount
	found := false
	for _, m := range mounts {
		t := path.Clean(m.Target)
		if !strings.HasPrefix(t, "/") {
			continue
		}
		if (t == p || t == "/" || strings.HasPrefix(p, t+"/")) && (!found || len(t) > len(best.Target)) {
			best, found = m, true
			best.Target = t
		}
	}
	return best, found
}

// isRegularFile reports whether a host path is a regular file; anything it
// can't stat counts as a folder.
func isRegularFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// appComposeModel is the app's composeModel, from one `docker compose
// config` (every profile active, so a profiled service's mounts resolve
// too), cached while HEAD and the derived env files are unchanged: it runs
// at deploy, at a backups update and, after a restart, at the first backup
// run — never on a timer.
func (r *Repository) appComposeModel(ctx context.Context, appID string, files []string) (composeModel, error) {
	dir := r.appDataDir(appID)
	key := r.composeModelKey(dir)
	r.mountMu.Lock()
	e, ok := r.mountCache[appID]
	r.mountMu.Unlock()
	if ok && key != "" && e.key == key {
		return e.model, nil
	}
	ctx, cancel := context.WithTimeout(ctx, composeConfigTimeout)
	defer cancel()
	out, err := r.composeOutput(ctx, dir, r.composeArgsFor(appID, files, []string{"*"}, "config", "--format", "json"))
	if err != nil {
		return composeModel{}, fmt.Errorf("docker compose config: %w", err)
	}
	var m composeModel
	if err := json.Unmarshal(out, &m); err != nil {
		return composeModel{}, fmt.Errorf("docker compose config: %w", err)
	}
	if key != "" {
		r.mountMu.Lock()
		r.mountCache[appID] = mountCacheEntry{key: key, model: m}
		r.mountMu.Unlock()
	}
	return m, nil
}

// composeModelKey changes whenever what compose config reads may have:
// the commit and the derived env files. "" when there is no commit.
func (r *Repository) composeModelKey(dir string) string {
	head, err := gitHead(dir)
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(head))
	for _, rel := range []string{storageEnvRel, envSecretsRel} {
		raw, _ := os.ReadFile(filepath.Join(dir, rel))
		h.Write([]byte{0})
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// resolveBackups maps every enabled sqlite and files backup of the app to
// its helper plan. Without such a backup it runs nothing.
func (r *Repository) resolveBackups(ctx context.Context, appID string, files []string, backups []recipe.EffectiveBackup) (map[string]helperPlan, error) {
	out := map[string]helperPlan{}
	var m *composeModel
	for _, b := range backups {
		if !b.Enabled || !b.Builtin() {
			continue
		}
		if m == nil {
			got, err := r.appComposeModel(ctx, appID, files)
			if err != nil {
				return nil, err
			}
			m = &got
		}
		plan, err := resolveBackup(*m, b, isRegularFile)
		if err != nil {
			return nil, err
		}
		out[b.Name] = plan
	}
	return out, nil
}

// dockerOutputIn runs docker with args in dir and returns stdout; the error
// carries stderr.
func dockerOutputIn(ctx context.Context, dir string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
	// runCompose runs one docker compose command in an app's folder
	// (replaced in tests to record the arguments).
	runCompose func(ctx context.Context, dir string, args []string) error
}
```

**with:**

```go
	// runCompose runs one docker compose command in an app's folder
	// (replaced in tests to record the arguments); composeOutput runs one
	// and returns its stdout (docker compose config).
	runCompose    func(ctx context.Context, dir string, args []string) error
	composeOutput func(ctx context.Context, dir string, args []string) ([]byte, error)

	// mountCache holds each app's resolved compose model for backups,
	// keyed by HEAD and the derived env files (see appComposeModel).
	mountMu    sync.Mutex
	mountCache map[string]mountCacheEntry
}
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
		runCompose:    dockerIn,
	}
```

**with:**

```go
		runCompose:    dockerIn,
		composeOutput: dockerOutputIn,
		mountCache:    make(map[string]mountCacheEntry),
	}
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
	r.versionMu.Lock()
	delete(r.versionCache, appID)
	r.versionMu.Unlock()
}
```

**with:**

```go
	r.versionMu.Lock()
	delete(r.versionCache, appID)
	r.versionMu.Unlock()
	r.mountMu.Lock()
	delete(r.mountCache, appID)
	r.mountMu.Unlock()
}
```

**In `internal/infra/orchestrator/docker_compose/operations.go`, replace:**

```go
	if err := r.materializeStorageEnv(ctx, dir, files, cfg.backups); err != nil {
		return err
	}
	profiles := r.composeProfiles(dir, cfg.backups)
```

**with:**

```go
	if err := r.materializeStorageEnv(ctx, dir, files, cfg.backups); err != nil {
		return err
	}
	// Every enabled sqlite and files backup must reach its data through the
	// app's own mounts.
	if _, err := r.resolveBackups(ctx, appID, files, cfg.backups); err != nil {
		return err
	}
	profiles := r.composeProfiles(dir, cfg.backups)
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/infra/orchestrator/docker_compose/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/infra/orchestrator/docker_compose/mounts_test.go internal/infra/orchestrator/docker_compose/mounts.go internal/infra/orchestrator/docker_compose/repository.go internal/infra/orchestrator/docker_compose/operations.go
git commit -F - <<'EOF'
feat(agent): resolve backup paths to the app's own mounts via docker compose config

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 8: The backup engine: run, restore points, update

**Files:**
- Modify: `go.mod` (`github.com/klauspost/compress` becomes direct)
- Create: `internal/infra/orchestrator/docker_compose/backuprun.go` (`RunBackup`, the helper container, zstd + sha256 upload, manifest, prune, `RemoveHelpers`), `backuprun_test.go`
- Create: `internal/infra/orchestrator/docker_compose/backups.go` (`ListRestorePoints`, `DeleteRestorePoint`, `UpdateBackups`, `BackupPlan`, `ServiceContainerPrefix`), `backups_test.go`
- Create: `internal/infra/orchestrator/docker_compose/backup_docker_test.go` (real helper containers; skipped without Docker or in `-short`)
- Modify: `repository.go` (helper seams: `helperImageReady`, `runHelper`, `docker`, `helperBinary`, `now`)

**Interfaces:**
- Consumes: `helper.*` (Task 5); `layout.*`, `GetBackupRetentionDays` (Task 4); `resolveBackups`, `helperPlan` (Task 7); `appBackups`, `renderStorageEnv`, `composeProfiles`, `writeOverrides`, `runningProjects`, `redeploy`, `markDeployed` (Task 6, C1); `detached`, `execOutput`, `dockerCombined` (existing).
- Produces (package `dockercompose`, used by Tasks 9–10):
  - `type BackupJob struct{ AppID, Backup string; Kind command.PointKind }`; `type BackupResult struct{ Unchanged bool; Point *command.RestorePoint; Fingerprint string; Size int64; Warnings []string }`
  - `(r *Repository) RunBackup(ctx, BackupJob) (BackupResult, error)`; `ErrNotRunnable` (matches a run refused before it started: off, custom, unknown backup or app)
  - `(r) ListRestorePoints(ctx, appID, backup string) ([]command.RestorePoint, error)` (30s); `(r) DeleteRestorePoint(ctx, appID, backup, pointID string) error` (30s); `(r) UpdateBackups(ctx, appID string, overrides []recipe.BackupConfig) (command.UpdateBackupsResponse, error)` (10-minute budget)
  - `type PlannedBackup struct{ AppID string; recipe.EffectiveBackup }`; `(r) BackupPlan() ([]PlannedBackup, error)`; `(r) RemoveHelpers(ctx) error`; `ServiceContainerPrefix(appID, service string) string`
  - `withDeadline(parent, d, what, fn) error` (abandons fn at the deadline, see `detached`)

- [ ] **Step 1: Make zstd a direct dependency**

```bash
go get github.com/klauspost/compress@v1.19.2
go mod tidy
```

Expected: `go.mod` moves `github.com/klauspost/compress v1.19.2` into the main `require` block (it is already an indirect dependency of minio-go; BSD-3-Clause/Apache-2.0/MIT). `go.sum` is unchanged.

- [ ] **Step 2: Write the failing tests**

**Create `internal/infra/orchestrator/docker_compose/backuprun_test.go`:**

```go
package dockercompose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/klauspost/compress/zstd"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
)

// fakeHelper stands in for `docker run` of the helper: it records the
// arguments and answers with a dump of payload whose fingerprint is fp
// (exit 3 when --since already is fp), or with exit/stderr when set.
type fakeHelper struct {
	mu      sync.Mutex
	calls   [][]string
	payload string
	fp      string
	exit    int
	stderr  string
	err     error
}

func (f *fakeHelper) run(_ context.Context, _ string, args []string, out io.Writer) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	if f.err != nil || f.exit != 0 {
		return f.exit, []byte(f.stderr), f.err
	}
	if i := slices.Index(args, "--since"); i >= 0 && args[i+1] == f.fp {
		return helper.ExitUnchanged, []byte(`{"unchanged":true}` + "\n"), nil
	}
	if _, err := io.WriteString(out, f.payload); err != nil {
		return 1, nil, err
	}
	res, _ := json.Marshal(helper.Result{Fingerprint: f.fp, RawSize: int64(len(f.payload)), Warnings: []string{"a note"}})
	return 0, append([]byte("progress\n"), res...), nil
}

func (f *fakeHelper) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

// backupRepo is an app with backupCompose, its database and media backups
// on the local storage "disk", a fake helper, compose config and docker, and
// a clock at now.
type backupRepo struct {
	*Repository
	disk   string
	helper *fakeHelper
	docker []string
	clock  time.Time
}

func newBackupRepo(t *testing.T) *backupRepo {
	t.Helper()
	r := newTestRepo(t)
	b := &backupRepo{Repository: r, disk: t.TempDir(), helper: &fakeHelper{payload: "SQLITE-SNAPSHOT", fp: "sqlite:1"},
		clock: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	if err := r.storages.Save(localStorage("disk", b.disk), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(
		`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","enabled":true,"storage":"disk","exclude":["*.tmp"]}]`)); err != nil {
		t.Fatal(err)
	}
	(&fakeComposeConfig{out: configJSON}).install(r)
	r.runHelper = b.helper.run
	r.docker = func(_ context.Context, args ...string) error {
		b.docker = append(b.docker, strings.Join(args, " "))
		return nil
	}
	r.helperBinary = func() (string, error) { return "/usr/local/bin/winterflow", nil }
	r.now = func() time.Time { return b.clock }
	return b
}

func (b *backupRepo) run(t *testing.T, name string, kind command.PointKind) (BackupResult, error) {
	t.Helper()
	return b.RunBackup(context.Background(), BackupJob{AppID: "app-1", Backup: name, Kind: kind})
}

// stored lists the disk's object keys.
func (b *backupRepo) stored(t *testing.T) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(b.disk, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			rel, _ := filepath.Rel(b.disk, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestRunBackupStoresAPoint(t *testing.T) {
	b := newBackupRepo(t)
	res, err := b.run(t, "database", command.PointScheduled)
	if err != nil {
		t.Fatal(err)
	}
	id := "20261011T120000Z-scheduled"
	if res.Unchanged || res.Point == nil || res.Point.ID != id || res.Fingerprint != "sqlite:1" ||
		len(res.Warnings) != 1 || res.Warnings[0] != "a note" {
		t.Fatalf("result = %+v", res)
	}
	want := []string{"app-1/database/" + id + ".db.zst", "app-1/database/" + id + ".json"}
	if got := b.stored(t); !slices.Equal(got, want) {
		t.Fatalf("stored = %v", got)
	}
	data, _ := os.ReadFile(filepath.Join(b.disk, want[0]))
	dec, _ := zstd.NewReader(bytes.NewReader(data))
	plain, err := io.ReadAll(dec)
	if err != nil || string(plain) != "SQLITE-SNAPSHOT" {
		t.Fatalf("stored copy = %q, %v", plain, err)
	}
	var m layout.Manifest
	raw, _ := os.ReadFile(filepath.Join(b.disk, want[1]))
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if m.SHA256 != hex.EncodeToString(sum[:]) || m.Size != int64(len(data)) || m.RawSize != 15 || m.Fingerprint != "sqlite:1" ||
		m.Type != recipe.TypeSQLite || m.Kind != command.PointScheduled || m.AppID != "app-1" || m.Backup != "database" || m.AgentVersion == "" {
		t.Fatalf("manifest = %+v", m)
	}
	if res.Size != m.Size || res.Point.Size != m.Size || res.Point.Expires != b.clock.Add(180*24*time.Hour).Unix() {
		t.Fatalf("point = %+v", res.Point)
	}
	args := strings.Join(b.helper.last(), " ")
	for _, part := range []string{
		"run --rm --name wf-backup-",
		"--network none --memory 256m --cpu-shares 256 --cap-drop ALL --cap-add DAC_OVERRIDE --cap-add SETUID --cap-add SETGID --security-opt no-new-privileges",
		"--label winterflow.role=backup-helper --label winterflow.app=app-1",
		"--mount type=volume,source=wf-app-1_data,target=/src/0 --mount type=bind,source=/usr/local/bin/winterflow,target=/wf,readonly",
		"alpine:3.20 /wf backup dump sqlite /src/0/app.db",
	} {
		if !strings.Contains(args, part) {
			t.Errorf("args lack %q:\n%s", part, args)
		}
	}
	if strings.Contains(args, "--since") {
		t.Fatal("the first copy compared with nothing")
	}
	if !slices.Contains(b.docker, "image inspect --format {{.Id}} alpine:3.20") || !slices.Contains(b.docker, "volume inspect --format {{.Name}} wf-app-1_data") {
		t.Fatalf("docker calls = %v", b.docker)
	}

	// Unchanged data: compared with the newest copy, nothing uploaded, and
	// the image is not looked up again.
	b.docker = nil
	b.clock = b.clock.Add(time.Hour)
	res, err = b.run(t, "database", command.PointScheduled)
	if err != nil || !res.Unchanged || res.Point != nil || res.Fingerprint != "sqlite:1" {
		t.Fatalf("unchanged = %+v, %v", res, err)
	}
	if !strings.HasSuffix(strings.Join(b.helper.last(), " "), "--since sqlite:1") || len(b.stored(t)) != 2 {
		t.Fatalf("args = %v, stored = %v", b.helper.last(), b.stored(t))
	}
	if slices.Contains(b.docker, "image inspect --format {{.Id}} alpine:3.20") {
		t.Fatal("the image was looked up twice")
	}
	// A pre-restore copy is always made.
	if res, err := b.run(t, "database", command.PointPreRestore); err != nil || res.Unchanged {
		t.Fatalf("pre-restore = %+v, %v", res, err)
	}
	// Every copy deleted by hand: the next run compares with nothing, so
	// unchanged data is uploaded again.
	if err := os.RemoveAll(filepath.Join(b.disk, "app-1")); err != nil {
		t.Fatal(err)
	}
	if res, err := b.run(t, "database", command.PointScheduled); err != nil || res.Unchanged || slices.Contains(b.helper.last(), "--since") {
		t.Fatalf("after deleting every copy: %+v, %v", res, err)
	}
}

func TestRunBackupFiles(t *testing.T) {
	b := newBackupRepo(t)
	if _, err := b.run(t, "media", command.PointManual); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(b.helper.last(), " ")
	if !strings.Contains(args, "--mount type=bind,source=/data/apps-data/app-1/media,target=/src/0,readonly") ||
		!strings.HasSuffix(args, "/wf backup dump files --path /src/0=app/media --exclude *.tmp") {
		t.Fatalf("args = %s", args)
	}
	if got := b.stored(t); len(got) != 2 || got[1] != "app-1/media/20261011T120000Z-manual.tar.zst" {
		t.Fatalf("stored = %v", got)
	}
}

// When the newest scheduled copy is older than half the ceiling, the run
// uploads even unchanged data, so an idle backup keeps a copy.
func TestRunBackupRefreshesBeforeTheCeiling(t *testing.T) {
	b := newBackupRepo(t)
	t.Setenv("BACKUP_RETENTION_DAYS", "10")
	if _, err := b.run(t, "database", command.PointScheduled); err != nil {
		t.Fatal(err)
	}
	b.clock = b.clock.Add(4 * 24 * time.Hour)
	if res, _ := b.run(t, "database", command.PointScheduled); !res.Unchanged {
		t.Fatal("4 days old: unchanged")
	}
	b.clock = b.clock.Add(2 * 24 * time.Hour)
	res, err := b.run(t, "database", command.PointScheduled)
	if err != nil || res.Unchanged || slices.Contains(b.helper.last(), "--since") {
		t.Fatalf("6 days old: %+v, %v, %v", res, err, b.helper.last())
	}
}

// After a run, retention prunes: here the tiers keep only the newest copy.
func TestRunBackupPrunes(t *testing.T) {
	b := newBackupRepo(t)
	if _, err := saveConfig(t, b.Repository, "app-1", backupCompose, withBackups(
		`[{"name":"database","enabled":true,"storage":"disk","keep":{"recent":"1h","daily":0,"monthly":0}}]`)); err != nil {
		t.Fatal(err)
	}
	b.Repository.forgetApp("app-1")
	for i := range 3 {
		b.helper.fp = "sqlite:" + string(rune('a'+i))
		if _, err := b.run(t, "database", command.PointScheduled); err != nil {
			t.Fatal(err)
		}
		b.clock = b.clock.Add(2 * time.Hour)
	}
	if got := b.stored(t); len(got) != 2 || !strings.HasPrefix(got[0], "app-1/database/20261011T160000Z") {
		t.Fatalf("stored = %v", got)
	}
}

func TestRunBackupFailures(t *testing.T) {
	b := newBackupRepo(t)
	// Missing database: the helper's reason, no object left behind.
	b.helper.exit, b.helper.stderr = helper.ExitMissing, `{"error":"database file not found: /src/0/app.db"}`
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || err.Error() != "database file not found: /src/0/app.db" {
		t.Fatalf("missing: %v", err)
	}
	// Docker failing before the helper ran: its own words.
	b.helper.exit, b.helper.stderr = 125, "docker: Error response from daemon: mount denied"
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || !strings.Contains(err.Error(), "exited with 125: docker: Error response from daemon: mount denied") {
		t.Fatalf("docker error: %v", err)
	}
	b.helper.exit, b.helper.err = -1, errors.New("stopped after its time limit: context deadline exceeded")
	if _, err := b.run(t, "database", command.PointScheduled); err == nil || !strings.Contains(err.Error(), "the backup helper: stopped after its time limit") {
		t.Fatalf("killed: %v", err)
	}
	if got := b.stored(t); len(got) != 0 {
		t.Fatalf("a failed run left %v", got)
	}
	b.helper.exit, b.helper.err = 0, nil

	// Not runnable.
	for _, tc := range []struct{ name, want string }{
		{"replica", "the backup replica is off"},
		{"nope", "the app has no backup nope"},
	} {
		if _, err := b.run(t, tc.name, command.PointManual); !errors.Is(err, ErrNotRunnable) || err.Error() != tc.want {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := saveConfig(t, b.Repository, "app-1", backupCompose, withBackups(`[{"name":"replica","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.run(t, "replica", command.PointManual); err == nil || !strings.Contains(err.Error(), "is a custom backup") {
		t.Fatalf("custom: %v", err)
	}
	if _, err := b.RunBackup(context.Background(), BackupJob{AppID: "gone", Backup: "database"}); !errors.Is(err, ErrNotRunnable) {
		t.Fatalf("a missing app: %v", err)
	}
}

func TestRunBackupDockerChecks(t *testing.T) {
	b := newBackupRepo(t)
	pulls := 0
	b.Repository.docker = func(_ context.Context, args ...string) error {
		switch {
		case args[0] == "image":
			return errors.New("no such image")
		case args[0] == "pull":
			if pulls++; pulls > 1 {
				return nil
			}
			return errors.New("network unreachable")
		case args[0] == "volume":
			return errors.New("no such volume")
		}
		return nil
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || !strings.Contains(err.Error(), "pull the backup helper image alpine:3.20: network unreachable") {
		t.Fatalf("pull: %v", err)
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || !strings.Contains(err.Error(), "the volume wf-app-1_data does not exist") {
		t.Fatalf("volume: %v", err)
	}
	// A storage the server lost.
	if _, err := b.storages.Delete("disk"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || err.Error() != "there is no storage disk on this server" {
		t.Fatalf("storage: %v", err)
	}
}

// failingPut is a store whose uploads are refused.
type failingPut struct{ store.Store }

func (failingPut) Put(context.Context, string, io.Reader) error { return errors.New("Access Denied") }

// An upload that fails is the reported cause (not the helper it stopped).
func TestRunBackupUploadFails(t *testing.T) {
	b := newBackupRepo(t)
	b.Repository.openStorage = func(s model.Storage, secret string) (store.Store, error) {
		st, err := store.Open(s, secret)
		return failingPut{st}, err
	}
	if _, err := b.run(t, "database", command.PointManual); err == nil || err.Error() != "upload the copy: Access Denied" {
		t.Fatalf("err = %v", err)
	}
}

func TestHelperArgsQuoteMountFields(t *testing.T) {
	plan := helperPlan{Mounts: []helperMount{{Bind: `/srv/a,b"c`}}, Paths: []helperPath{{Sub: "x", Entry: "x"}}}
	args := helperArgs("n", "app-1", "/opt/wf,1/winterflow", plan, recipe.EffectiveBackup{Type: recipe.TypeFiles}, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, `--mount type=bind,"source=/srv/a,b""c",target=/src/0,readonly`) ||
		!strings.Contains(joined, `--mount type=bind,"source=/opt/wf,1/winterflow",target=/wf,readonly`) {
		t.Fatalf("args = %s", joined)
	}
}

func TestRestorePoints(t *testing.T) {
	b := newBackupRepo(t)
	for i := range 2 {
		b.helper.fp = "sqlite:" + string(rune('a'+i))
		if _, err := b.run(t, "database", command.PointManual); err != nil {
			t.Fatal(err)
		}
		b.clock = b.clock.Add(time.Minute)
	}
	// An incomplete point (no manifest) is not listed.
	if err := os.WriteFile(filepath.Join(b.disk, "app-1/database/20261011T130000Z-scheduled.db.zst"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	points, err := b.ListRestorePoints(context.Background(), "app-1", "database")
	if err != nil || len(points) != 2 || points[0].ID != "20261011T120100Z-manual" || points[0].Kind != command.PointManual {
		t.Fatalf("points = %+v, %v", points, err)
	}
	if err := b.DeleteRestorePoint(context.Background(), "app-1", "database", points[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteRestorePoint(context.Background(), "app-1", "database", "20261011T130000Z-scheduled"); err != nil {
		t.Fatalf("an incomplete point can be deleted too: %v", err)
	}
	points, _ = b.ListRestorePoints(context.Background(), "app-1", "database")
	if len(points) != 1 || len(b.stored(t)) != 2 {
		t.Fatalf("after delete: %+v, %v", points, b.stored(t))
	}
	if err := b.DeleteRestorePoint(context.Background(), "app-1", "database", "20200101T000000Z-manual"); err == nil ||
		!strings.Contains(err.Error(), "has no restore point 20200101T000000Z-manual") {
		t.Fatalf("missing point: %v", err)
	}
	for _, tc := range []struct{ app, name, want string }{
		{"app-1", "replica", "is a custom backup"},
		{"app-1", "nope", "the app has no backup nope"},
		{"gone", "database", "app gone not found"},
	} {
		if _, err := b.ListRestorePoints(context.Background(), tc.app, tc.name); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s/%s: %v", tc.app, tc.name, err)
		}
	}
	if _, err := saveConfig(t, b.Repository, "app-1", backupCompose, withBackups(`[]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ListRestorePoints(context.Background(), "app-1", "database"); err == nil || !strings.Contains(err.Error(), "has no storage") {
		t.Fatalf("no storage: %v", err)
	}
}

// A storage that never answers fails the listing at its deadline instead of
// holding the command pipeline.
func TestWithDeadlineAbandonsAHungCall(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	err := withDeadline(context.Background(), 20*time.Millisecond, "list", func(context.Context) error { <-release; return nil })
	if err == nil || !strings.Contains(err.Error(), "the storage did not answer within 20ms") {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := withDeadline(ctx, time.Second, "list", func(context.Context) error { <-release; return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}
```

**Create `internal/infra/orchestrator/docker_compose/backups_test.go`:**

```go
package dockercompose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
)

// updateRepo is an app with backupCompose deployed (its mark at HEAD),
// storages "disk" and "hetzner", fake compose config, and fake hooks for
// which projects run and redeploys.
func updateRepo(t *testing.T) (*Repository, *fakeComposeHooks) {
	t.Helper()
	r := newTestRepo(t)
	for _, name := range []string{"disk", "hetzner"} {
		if err := r.storages.Save(localStorage(name, t.TempDir()), nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := saveConfig(t, r, "app-1", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	r.markDeployed("app-1", "")
	(&fakeComposeConfig{out: configJSON}).install(r)
	hooks := &fakeComposeHooks{up: map[string]bool{"wf-app-1": true}}
	hooks.install(r)
	return r, hooks
}

func update(r *Repository, list ...recipe.BackupConfig) (string, []string, bool, error) {
	resp, err := r.UpdateBackups(context.Background(), "app-1", list)
	return resp.Revision, resp.Warnings, resp.Redeployed, err
}

// Turning on a sqlite backup changes nothing that runs: a commit, no
// redeploy, and the commit counts as deployed.
func TestUpdateBackupsWithoutRedeploy(t *testing.T) {
	r, hooks := updateRepo(t)
	dir := r.appDataDir("app-1")
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{
		{Name: "database", Enabled: true, Storage: "disk", Every: "1h"}, // every equals the default: kept, as the user set it
	})
	if err != nil {
		t.Fatal(err)
	}
	head, _ := gitHead(dir)
	if resp.Revision != head || resp.Redeployed || len(hooks.redeploys) != 0 || r.hasDraft("app-1") {
		t.Fatalf("resp = %+v, redeploys %v, draft %v", resp, hooks.redeploys, r.hasDraft("app-1"))
	}
	if len(resp.Backups) != 3 || !resp.Backups[0].Enabled || resp.Backups[0].Storage != "disk" {
		t.Fatalf("backups = %+v", resp.Backups)
	}
	got, _ := r.readOverrides(dir)
	if len(got) != 1 || got[0].Every != "1h" || !got[0].Enabled {
		t.Fatalf("stored = %+v", got)
	}
}

// Turning a custom backup on or off changes the compose profiles: a running
// app is redeployed.
func TestUpdateBackupsRedeploysOnProfileChange(t *testing.T) {
	r, hooks := updateRepo(t)
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}})
	if err != nil || !resp.Redeployed || len(hooks.redeploys) != 1 {
		t.Fatalf("resp = %+v, %v, redeploys %v", resp, err, hooks.redeploys)
	}
	// A failed redeploy is a warning: the settings are saved.
	hooks.failUp = map[string]error{"app-1": errors.New("port in use")}
	resp, err = r.UpdateBackups(context.Background(), "app-1", nil)
	if err != nil || resp.Redeployed || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "redeploy failed: port in use") {
		t.Fatalf("resp = %+v, %v", resp, err)
	}
}

func TestUpdateBackupsLeavesStoppedAndDraftAppsAlone(t *testing.T) {
	r, hooks := updateRepo(t)
	hooks.up = map[string]bool{}
	resp, err := r.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}})
	if err != nil || resp.Redeployed || len(hooks.redeploys) != 0 || r.hasDraft("app-1") {
		t.Fatalf("stopped: %+v, %v", resp, err)
	}
	// A draft ahead of what runs: never deployed behind the user's back.
	hooks.up = map[string]bool{"wf-app-1": true}
	writeFileT(t, r.appDataDir("app-1"), "notes.txt", "draft")
	if _, err := gitCommitAll(r.appDataDir("app-1"), "draft"); err != nil {
		t.Fatal(err)
	}
	resp, err = r.UpdateBackups(context.Background(), "app-1", nil)
	if err != nil || resp.Redeployed || len(hooks.redeploys) != 0 || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "undeployed changes") {
		t.Fatalf("draft: %+v, %v", resp, err)
	}
	// docker ps failing: a warning, no redeploy.
	r2, hooks2 := updateRepo(t)
	hooks2.psErr = errors.New("daemon down")
	resp, err = r2.UpdateBackups(context.Background(), "app-1", []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "hetzner"}})
	if err != nil || resp.Redeployed || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "daemon down") {
		t.Fatalf("ps error: %+v, %v", resp, err)
	}
}

// Nothing is written when the list can't be used.
func TestUpdateBackupsRefuses(t *testing.T) {
	r, _ := updateRepo(t)
	head, _ := gitHead(r.appDataDir("app-1"))
	for _, tc := range []struct {
		list []recipe.BackupConfig
		want string
	}{
		{[]recipe.BackupConfig{{Name: "gone", Every: "1h"}}, `the compose file has no backup "gone": a new backup needs its type`},
		{[]recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "nas"}}, `backup database: there is no storage "nas" on this server`},
		{[]recipe.BackupConfig{{Name: "media", Enabled: true, Storage: "disk", Paths: []string{"/etc/app"}}}, "/etc/app is not on a mount of service web"},
		{[]recipe.BackupConfig{{Name: "database", Every: "1m"}}, "backups[0].every: 1m is too often"},
	} {
		if _, _, _, err := update(r, tc.list...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.list, err, tc.want)
		}
	}
	if now, _ := gitHead(r.appDataDir("app-1")); now != head {
		t.Fatal("a refused update committed")
	}
	if _, err := r.UpdateBackups(context.Background(), "gone", nil); err == nil {
		t.Fatal("a missing app was updated")
	}
}

func TestBackupPlan(t *testing.T) {
	r := newTestRepo(t)
	if plan, err := r.BackupPlan(); err != nil || len(plan) != 0 {
		t.Fatalf("no apps: %v %v", plan, err)
	}
	if _, err := saveConfig(t, r, "app-1", backupCompose, withBackups(`[{"name":"database","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "app-2", "services: {}\n", plainConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "app-3", backupCompose, plainConfig); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, r.appDataDir("app-3"), "compose.yml", strings.Replace(backupCompose, "volume: data", "volume: gone", 1))
	plan, err := r.BackupPlan()
	if err == nil || !strings.Contains(err.Error(), "app-3: ") {
		t.Fatalf("err = %v", err)
	}
	if len(plan) != 3 || plan[0].AppID != "app-1" || plan[0].Name != "database" || !plan[0].Enabled || plan[2].Name != "replica" {
		t.Fatalf("plan = %+v", plan)
	}
}
```

**Create `internal/infra/orchestrator/docker_compose/backup_docker_test.go`:**

```go
package dockercompose

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
)

// requireDocker skips a test without a usable Docker daemon (and in -short).
func requireDocker(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("docker test in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Skip("docker is not available:", err)
	}
}

// buildAgent builds the agent binary the helper runs: static, so it runs
// inside alpine.
func buildAgent(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "winterflow-agent")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, "winterflow/cmd/agent")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the agent: %v\n%s", err, msg)
	}
	return out
}

func dockerT(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestBackupThroughDocker runs real helper containers: a sqlite database on
// a named volume (owned by an app user, WAL mode) and files on a bind
// mount, then an unchanged run, a run killed at its deadline, and the
// startup cleanup of leftover helpers.
func TestBackupThroughDocker(t *testing.T) {
	requireDocker(t)
	r := newTestRepo(t)
	agent := buildAgent(t)
	r.helperBinary = func() (string, error) { return agent, nil }
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	vol := "wf-itg-backup-" + stamp
	dockerT(t, "volume", "create", vol)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", vol).Run() })

	// The database: made here, copied into the volume as uid 1000.
	src := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(src, "app.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, body TEXT); INSERT INTO items (body) VALUES ('a'), ('b'), ('c')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	dockerT(t, "run", "--rm", "-v", vol+":/v", "-v", src+":/h:ro", helper.Image, "sh", "-c", "cp /h/app.db /v/app.db && chown -R 1000:1000 /v")

	// The files: a bind mount.
	media := t.TempDir()
	writeFileT(t, media, "a.txt", "alpha")
	writeFileT(t, media, "sub/b.txt", "beta")
	writeFileT(t, media, "skip.tmp", "no")

	compose := fmt.Sprintf(`services:
  app:
    image: %s
    volumes:
      - data:/data
      - %s:/media
volumes:
  data:
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
      exclude: ["*.tmp"]
`, helper.Image, media, vol)
	disk := t.TempDir()
	if err := r.storages.Save(localStorage("disk", disk), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := saveConfig(t, r, "itg", compose, withBackups(
		`[{"name":"database","enabled":true,"storage":"disk"},{"name":"media","enabled":true,"storage":"disk"}]`)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "database", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(copyPath, decompress(t, filepath.Join(disk, "itg", "database", res.Point.ID+".db.zst")), 0o600); err != nil {
		t.Fatal(err)
	}
	cp, err := sql.Open("sqlite", "file:"+copyPath)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := cp.QueryRow("SELECT count(*) FROM items").Scan(&n); err != nil || n != 3 {
		t.Fatalf("copy rows = %d, %v", n, err)
	}
	cp.Close()
	// The helper opened the database as its owner: no root-owned -shm.
	for _, uid := range strings.Fields(dockerT(t, "run", "--rm", "-v", vol+":/v", helper.Image, "sh", "-c", "stat -c %u /v/*")) {
		if uid != "1000" {
			t.Fatalf("a file in the volume is owned by %s", uid)
		}
	}

	// Unchanged since the copy: exit 3, nothing uploaded.
	again, err := r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "database", Kind: command.PointScheduled})
	if err != nil || !again.Unchanged {
		t.Fatalf("unchanged run = %+v, %v", again, err)
	}

	res, err = r.RunBackup(ctx, BackupJob{AppID: "itg", Backup: "media", Kind: command.PointManual})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(decompress(t, filepath.Join(disk, "itg", "media", res.Point.ID+".tar.zst"))))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		names[h.Name] = string(body)
	}
	if names["media/a.txt"] != "alpha" || names["media/sub/b.txt"] != "beta" || len(names) != 4 {
		t.Fatalf("archive = %v", names)
	}

	// A run past its deadline: the helper container is removed.
	name := "wf-backup-itg-" + stamp
	kctx, kcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer kcancel()
	_, _, err = runDockerHelper(kctx, name, []string{"run", "--rm", "--name", name, "--label", helper.RoleLabel, helper.Image, "sleep", "60"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "time limit") {
		t.Fatalf("killed run: %v", err)
	}
	if left := dockerT(t, "ps", "-aq", "--filter", "name="+name); left != "" {
		t.Fatalf("the killed helper is still there: %s", left)
	}

	// Leftover helpers from a crashed agent are removed at start.
	dockerT(t, "run", "-d", "--label", helper.RoleLabel, "--label", helper.AppLabel+"=itg", helper.Image, "sleep", "60")
	if err := r.RemoveHelpers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if left := dockerT(t, "ps", "-aq", "--filter", "label="+helper.RoleLabel); left != "" {
		t.Fatalf("leftover helpers: %s", left)
	}
}

func decompress(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := zstd.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	out, err := io.ReadAll(dec)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test -short ./internal/infra/orchestrator/docker_compose/`

Expected: build failure: `undefined: BackupJob`, `r.runHelper undefined`, `undefined: PlannedBackup`, ….

- [ ] **Step 4: Implement**

**Create `internal/infra/orchestrator/docker_compose/backuprun.go`:**

```go
package dockercompose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/helper"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
	"winterflow/pkg/version"
)

// Time limits of a backup run (the scheduler bounds the whole run: 30
// minutes for dump plus upload).
const (
	helperPullTimeout  = 5 * time.Minute  // the image pull on first use
	storageCallTimeout = 30 * time.Second // one list, manifest or delete call
	dockerCallTimeout  = 30 * time.Second // one short docker call (inspect, rm)
	helperStopGrace    = 10 * time.Second // after a kill, for docker's output to drain
)

// Helper container limits: a run must not starve the apps it backs up on a
// small box, and gets no network and only the capabilities a dump needs
// (read every file; switch to a database's owner).
var helperLimits = []string{
	"--network", "none", "--memory", "256m", "--cpu-shares", "256",
	"--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE", "--cap-add", "SETUID", "--cap-add", "SETGID",
	"--security-opt", "no-new-privileges",
}

// BackupJob is one run of a backup.
type BackupJob struct {
	AppID  string
	Backup string
	Kind   command.PointKind
}

// BackupResult is the outcome of a run: a new point, or Unchanged (nothing
// uploaded). Fingerprint is the source's, Size the stored size.
type BackupResult struct {
	Unchanged   bool
	Point       *command.RestorePoint
	Fingerprint string
	Size        int64
	Warnings    []string
}

// errNotBackedUp aborts an upload the helper produced nothing for.
var errNotBackedUp = errors.New("no backup was produced")

// ErrNotRunnable marks a run refused before it started: the backup is off,
// custom, or not the app's. The scheduler records no attempt for it.
var ErrNotRunnable = errors.New("backup not runnable")

// notRunnable is an ErrNotRunnable with its reason as the message.
type notRunnable string

func (e notRunnable) Error() string        { return string(e) }
func (e notRunnable) Is(target error) bool { return target == ErrNotRunnable }

// RunBackup runs one sqlite or files backup: the helper container dumps the
// data from the app's own mounts, the agent compresses it (zstd) and streams
// it to the backup's storage with its sha256, writes the manifest last,
// then prunes by the backup's retention under BACKUP_RETENTION_DAYS. When the
// data hasn't changed since the newest copy, nothing is uploaded — unless
// that copy is about to reach the ceiling (or the run is a pre-restore
// copy). ctx bounds the whole run.
func (r *Repository) RunBackup(ctx context.Context, job BackupJob) (BackupResult, error) {
	dir := r.appDataDir(job.AppID)
	if !r.appExists(job.AppID) {
		return BackupResult{}, notRunnable("app " + job.AppID + " not found")
	}
	files := r.appComposeFiles(dir)
	cfg, err := r.appBackups(dir, files)
	if err != nil {
		return BackupResult{}, err
	}
	b, err := runnableBackup(cfg.backups, job.Backup)
	if err != nil {
		return BackupResult{}, err
	}
	st, err := r.openBackupStorage(b.Storage)
	if err != nil {
		return BackupResult{}, err
	}
	plans, err := r.resolveBackups(ctx, job.AppID, files, []recipe.EffectiveBackup{b})
	if err != nil {
		return BackupResult{}, err
	}
	plan := plans[b.Name]
	folder := layout.Folder(job.AppID, b.Name)
	points, err := listFolder(ctx, st, folder)
	if err != nil {
		return BackupResult{}, err
	}
	ceiling := r.cfg.GetBackupRetentionDays()
	now := r.now().UTC()
	var res BackupResult
	since := ""
	if job.Kind != command.PointPreRestore && !layout.NeedsRefresh(points, ceiling, now) {
		if p, ok := layout.Newest(points, command.PointScheduled, command.PointManual); ok {
			m, err := readManifest(ctx, st, p.ManifestKey)
			if err != nil {
				res.Warnings = append(res.Warnings, "the newest copy's manifest could not be read, so a full copy was made: "+err.Error())
			}
			since = m.Fingerprint
		}
	}
	if err := r.ensureHelperImage(ctx); err != nil {
		return BackupResult{}, err
	}
	if err := r.checkHelperVolumes(ctx, plan); err != nil {
		return BackupResult{}, err
	}
	exe, err := r.helperBinary()
	if err != nil {
		return BackupResult{}, fmt.Errorf("find the agent executable: %w", err)
	}
	id := layout.PointID(now, job.Kind)
	name := "wf-backup-" + strconv.FormatInt(now.UnixNano(), 36)
	args := helperArgs(name, job.AppID, exe, plan, b, since)
	dataKey := layout.DataKey(job.AppID, b.Name, id, b.Type)
	dump, err := r.dumpAndUpload(ctx, st, dataKey, name, args)
	res.Warnings = append(res.Warnings, dump.result.Warnings...)
	switch {
	case err != nil:
		return res, err
	case dump.unchanged:
		res.Unchanged = true
		res.Fingerprint = since
	default:
		m := layout.Manifest{
			AppID: job.AppID, Backup: b.Name, Type: b.Type, Kind: job.Kind, Created: now,
			SHA256: dump.sha256, Size: dump.size, RawSize: dump.result.RawSize,
			Fingerprint: dump.result.Fingerprint, AgentVersion: version.GetVersion(),
		}
		if err := writeManifest(ctx, st, layout.ManifestKey(job.AppID, b.Name, id), m); err != nil {
			return res, err
		}
		p := layout.Point{ID: id, Time: now.Truncate(time.Second), Kind: job.Kind, DataKey: dataKey,
			ManifestKey: layout.ManifestKey(job.AppID, b.Name, id), Size: dump.size}
		info := p.Info(ceiling)
		res.Point, res.Fingerprint, res.Size = &info, m.Fingerprint, dump.size
		points = append([]layout.Point{p}, points...)
	}
	policy := layout.Policy{Keep: b.Keep.Retention(), CeilingDays: ceiling}
	if err := deletePoints(ctx, st, layout.Prune(points, policy, now)); err != nil {
		res.Warnings = append(res.Warnings, "retention: "+err.Error())
	}
	return res, nil
}

// runnableBackup finds a backup the agent runs: one of the app's, enabled,
// sqlite or files.
func runnableBackup(backups []recipe.EffectiveBackup, name string) (recipe.EffectiveBackup, error) {
	for _, b := range backups {
		if b.Name != name {
			continue
		}
		switch {
		case !b.Enabled:
			return b, notRunnable("the backup " + name + " is off")
		case !b.Builtin():
			return b, notRunnable("the backup " + name + " is a custom backup: its own service runs it")
		}
		return b, nil
	}
	return recipe.EffectiveBackup{}, notRunnable("the app has no backup " + name)
}

// openBackupStorage opens a storage of this server by name, its secret
// decrypted in memory only.
func (r *Repository) openBackupStorage(name string) (store.Store, error) {
	s, ok, err := r.storages.Get(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("there is no storage %s on this server", name)
	}
	secret := ""
	if s.S3 != nil {
		if secret, err = r.storageSecret(s); err != nil {
			return nil, err
		}
	}
	return r.openStorage(s, secret)
}

// listFolder lists a backup folder's points (30s).
func listFolder(ctx context.Context, st store.Store, folder string) ([]layout.Point, error) {
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	objs, err := st.List(ctx, folder)
	if err != nil {
		return nil, fmt.Errorf("list the backup's copies: %w", err)
	}
	return layout.Points(objs, folder), nil
}

func readManifest(ctx context.Context, st store.Store, key string) (layout.Manifest, error) {
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	rc, err := st.Get(ctx, key)
	if err != nil {
		return layout.Manifest{}, err
	}
	defer rc.Close()
	var m layout.Manifest
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&m); err != nil {
		return layout.Manifest{}, err
	}
	return m, nil
}

func writeManifest(ctx context.Context, st store.Store, key string, m layout.Manifest) error {
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := st.Put(ctx, key, strings.NewReader(string(raw)+"\n")); err != nil {
		return fmt.Errorf("write the manifest: %w", err)
	}
	return nil
}

// deletePoints removes the points' objects (30s).
func deletePoints(ctx context.Context, st store.Store, points []layout.Point) error {
	var keys []string
	for _, p := range points {
		keys = append(keys, p.Keys()...)
	}
	if len(keys) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, storageCallTimeout)
	defer cancel()
	return st.Delete(ctx, keys...)
}

// helperArgs is the `docker run` of one dump: the backup's sources mounted
// at /src/<n> (read-only for files; sqlite needs to take its locks), the
// agent's own static binary at /wf.
func helperArgs(name, appID, exe string, plan helperPlan, b recipe.EffectiveBackup, since string) []string {
	args := []string{"run", "--rm", "--name", name}
	args = append(args, helperLimits...)
	args = append(args, "--label", helper.RoleLabel, "--label", helper.AppLabel+"="+appID)
	ro := b.Type == recipe.TypeFiles
	for i, m := range plan.Mounts {
		spec := "type=volume," + csvField("source="+m.Volume)
		if m.Bind != "" {
			spec = "type=bind," + csvField("source="+m.Bind)
		}
		spec += ",target=/src/" + strconv.Itoa(i)
		if ro {
			spec += ",readonly"
		}
		args = append(args, "--mount", spec)
	}
	args = append(args, "--mount", "type=bind,"+csvField("source="+exe)+",target=/wf,readonly", helper.Image, "/wf", "backup", "dump")
	if b.Type == recipe.TypeSQLite {
		args = append(args, "sqlite", plan.Paths[0].Src())
	} else {
		args = append(args, "files")
		for _, p := range plan.Paths {
			args = append(args, "--path", p.Src()+"="+p.Entry)
		}
		for _, e := range b.Exclude {
			args = append(args, "--exclude", e)
		}
	}
	if since != "" {
		args = append(args, "--since", since)
	}
	return args
}

// csvField quotes one field of a --mount value (docker parses it as CSV):
// a path with a comma or a quote must not split the spec.
func csvField(f string) string {
	if !strings.ContainsAny(f, ",\"") {
		return f
	}
	return `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
}

// dumpOutcome is what the helper produced and what was uploaded.
type dumpOutcome struct {
	unchanged bool
	result    helper.Result
	sha256    string
	size      int64
}

// dumpAndUpload runs the helper and streams its stdout through zstd into
// key, hashing what is stored. Unless the helper succeeds, the upload is
// aborted, which leaves no object behind.
func (r *Repository) dumpAndUpload(ctx context.Context, st store.Store, key, name string, args []string) (dumpOutcome, error) {
	pr, pw := io.Pipe()
	put := make(chan error, 1)
	go func() {
		err := st.Put(ctx, key, pr)
		pr.CloseWithError(errNotBackedUp) // a failed upload fails the writes, never blocks them
		put <- err
	}()
	sum := sha256.New()
	stored := &countWriter{}
	enc, _ := zstd.NewWriter(io.MultiWriter(pw, sum, stored), zstd.WithEncoderConcurrency(1))
	exit, stderr, runErr := r.runHelper(ctx, name, args, enc)
	encErr := enc.Close()
	res, parsed := helper.ParseResult(stderr)
	out := dumpOutcome{result: res}
	if runErr == nil && exit == helper.ExitOK && encErr == nil {
		pw.Close()
		if err := <-put; err != nil {
			return out, fmt.Errorf("upload the copy: %w", err)
		}
		out.sha256, out.size = hex.EncodeToString(sum.Sum(nil)), stored.n
		return out, nil
	}
	// An upload that failed by itself is the cause (the helper then died
	// writing to it); otherwise abort it.
	var early error
	select {
	case early = <-put:
	default:
		pw.CloseWithError(errNotBackedUp)
		<-put
	}
	switch {
	case early != nil:
		return out, fmt.Errorf("upload the copy: %w", early)
	case runErr != nil:
		return out, fmt.Errorf("the backup helper: %w", runErr)
	case exit == helper.ExitUnchanged:
		out.unchanged = true
		return out, nil
	case exit == helper.ExitOK:
		return out, fmt.Errorf("compress the copy: %w", encErr)
	case parsed && res.Error != "":
		return out, errors.New(res.Error)
	default:
		return out, fmt.Errorf("the backup helper exited with %d: %s", exit, lastLines(stderr, 3))
	}
}

// lastLines is the end of a command's output, for an error.
func lastLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// tailBuffer keeps the last max bytes written (a helper's stderr).
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

// runDockerHelper runs `docker run` (args) with stdout to out. When ctx ends
// the helper container is removed (docker rm -f) and the CLI killed;
// docker's output gets helperStopGrace to drain.
func runDockerHelper(ctx context.Context, name string, args []string, out io.Writer) (int, []byte, error) {
	stderr := &tailBuffer{max: 64 << 10}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = out, stderr
	cmd.Cancel = func() error {
		removeHelper(name)
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = helperStopGrace
	err := cmd.Run()
	if ctx.Err() != nil {
		return -1, stderr.buf, fmt.Errorf("stopped after its time limit: %w", ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), stderr.buf, nil
	}
	if err != nil {
		return -1, stderr.buf, err
	}
	return 0, stderr.buf, nil
}

// removeHelper force-removes a helper container (best effort, bounded).
func removeHelper(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), dockerCallTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
}

// ensureHelperImage pulls the helper image once (5 minutes); later runs
// only remember that it is there.
func (r *Repository) ensureHelperImage(ctx context.Context) error {
	if r.helperImageReady.Load() {
		return nil
	}
	ictx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	err := r.docker(ictx, "image", "inspect", "--format", "{{.Id}}", helper.Image)
	cancel()
	if err != nil {
		pctx, cancel := context.WithTimeout(ctx, helperPullTimeout)
		defer cancel()
		if err := r.docker(pctx, "pull", helper.Image); err != nil {
			return fmt.Errorf("pull the backup helper image %s: %w", helper.Image, err)
		}
	}
	r.helperImageReady.Store(true)
	return nil
}

// checkHelperVolumes requires every Docker volume of a plan to exist:
// mounting a missing one would create it, empty, outside compose.
func (r *Repository) checkHelperVolumes(ctx context.Context, plan helperPlan) error {
	for _, m := range plan.Mounts {
		if m.Volume == "" {
			continue
		}
		vctx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
		err := r.docker(vctx, "volume", "inspect", "--format", "{{.Name}}", m.Volume)
		cancel()
		if err != nil {
			return fmt.Errorf("the volume %s does not exist (has the app been started?): %w", m.Volume, err)
		}
	}
	return nil
}

// RemoveHelpers removes helper containers left behind by an agent that
// stopped mid-run: one `docker ps` and at most one `docker rm`.
func (r *Repository) RemoveHelpers(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	defer cancel()
	out, err := execOutput(ctx, "docker", "ps", "-aq", "--filter", "label="+helper.RoleLabel)
	if err != nil {
		return err
	}
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		return dockerCombined(ctx, append([]string{"rm", "-f"}, ids...)...)
	}
	return nil
}

// agentExecutable is the binary the helper runs: this process's own,
// symlinks resolved.
func agentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}
```

**Create `internal/infra/orchestrator/docker_compose/backups.go`:**

```go
package dockercompose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/infra/backup/layout"
	"winterflow/internal/infra/backup/store"
)

// backupUpdateBudget bounds backup.update, its redeploy included.
const backupUpdateBudget = 10 * time.Minute

// withDeadline runs fn in a goroutine that is abandoned when the deadline d
// comes first (see detached): storage calls on a hung network mount ignore
// their context, and these run on the command pipeline.
func withDeadline(parent context.Context, d time.Duration, what string, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	select {
	case err := <-detached(func() error { return fn(ctx) }):
		return err
	case <-ctx.Done():
		if err := parent.Err(); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return fmt.Errorf("%s: the storage did not answer within %s", what, d)
	}
}

// backupStorage finds an app's backup (any state) and opens the storage it
// names: what listing and deleting restore points need.
func (r *Repository) backupStorage(appID, name string) (store.Store, string, error) {
	if !r.appExists(appID) {
		return nil, "", fmt.Errorf("app %s not found", appID)
	}
	dir := r.appDataDir(appID)
	cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
	if err != nil {
		return nil, "", err
	}
	b, ok := recipe.Declared(cfg.backups)[name]
	switch {
	case !ok:
		return nil, "", fmt.Errorf("the app has no backup %s", name)
	case !b.Builtin():
		return nil, "", fmt.Errorf("the backup %s is a custom backup: its copies are kept by its own tool", name)
	case b.Storage == "":
		return nil, "", fmt.Errorf("the backup %s has no storage", name)
	}
	st, err := r.openBackupStorage(b.Storage)
	return st, layout.Folder(appID, name), err
}

// ListRestorePoints lists a backup's complete restore points, newest first
// (30 seconds; on request only).
func (r *Repository) ListRestorePoints(ctx context.Context, appID, name string) ([]command.RestorePoint, error) {
	out := []command.RestorePoint{}
	err := withDeadline(ctx, storageCallTimeout, "list the restore points", func(ctx context.Context) error {
		st, folder, err := r.backupStorage(appID, name)
		if err != nil {
			return err
		}
		points, err := listFolder(ctx, st, folder)
		if err != nil {
			return err
		}
		for _, p := range layout.Complete(points) {
			out = append(out, p.Info(r.cfg.GetBackupRetentionDays()))
		}
		return nil
	})
	return out, err
}

// DeleteRestorePoint deletes one restore point (any kind, complete or not)
// and its manifest (30 seconds).
func (r *Repository) DeleteRestorePoint(ctx context.Context, appID, name, id string) error {
	return withDeadline(ctx, storageCallTimeout, "delete the restore point", func(ctx context.Context) error {
		st, folder, err := r.backupStorage(appID, name)
		if err != nil {
			return err
		}
		points, err := listFolder(ctx, st, folder)
		if err != nil {
			return err
		}
		for _, p := range points {
			if p.ID == id {
				return deletePoints(ctx, st, []layout.Point{p})
			}
		}
		return fmt.Errorf("the backup %s has no restore point %s", name, id)
	})
}

// UpdateBackups replaces an app's backups list (its overrides) and commits
// it. The list is checked against the compose file's recipe, the server's
// storages and the app's mounts first; nothing is written when it fails. A
// running app is brought up again only when its compose profiles or storage
// variables change (a custom backup turned on or off, or one its compose
// file references through WF_BACKUP_ moved), never when it has undeployed
// changes (a warning). Warnings also name the overrides dropped because the
// recipe no longer has their backup.
func (r *Repository) UpdateBackups(ctx context.Context, appID string, overrides []recipe.BackupConfig) (command.UpdateBackupsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, backupUpdateBudget)
	defer cancel()
	resp := command.UpdateBackupsResponse{AppID: appID}
	if !r.appExists(appID) {
		return resp, fmt.Errorf("app %s not found", appID)
	}
	dir := r.appDataDir(appID)
	files := r.appComposeFiles(dir)
	contents, err := composeContents(dir, files)
	if err != nil {
		return resp, fmt.Errorf("read compose files: %w", err)
	}
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return resp, err
	}
	backups, err := recipe.Check(comp, overrides)
	if err != nil {
		return resp, err
	}
	for _, b := range backups {
		if !b.Enabled {
			continue
		}
		if _, ok, err := r.storages.Get(b.Storage); err != nil {
			return resp, err
		} else if !ok {
			return resp, fmt.Errorf("backup %s: there is no storage %q on this server", b.Name, b.Storage)
		}
	}
	if _, err := r.resolveBackups(ctx, appID, files, backups); err != nil {
		return resp, err
	}
	prev, _ := r.appBackups(dir, files)
	oldEnv, _ := r.renderStorageEnv(ctx, dir, files, prev.backups, false)
	newEnv, err := r.renderStorageEnv(ctx, dir, files, backups, false)
	if err != nil {
		return resp, err
	}
	changed := !bytes.Equal(oldEnv, newEnv) || !slices.Equal(r.composeProfiles(dir, prev.backups), r.composeProfiles(dir, backups))

	mark, marked := readDeployedMark(dir)
	prevHead, _ := gitHead(dir)
	deployed := marked && mark == prevHead
	kept, warnings := recipe.Clean(comp, overrides)
	if err := r.writeOverrides(dir, kept); err != nil {
		return resp, err
	}
	hash, err := gitCommitAll(dir, "update backups")
	if err != nil {
		return resp, fmt.Errorf("commit: %w", err)
	}
	resp.Revision, resp.Backups, resp.Warnings = hash, backups, warnings
	if !deployed {
		if changed && marked {
			resp.Warnings = append(resp.Warnings, "the app has undeployed changes and was not redeployed: deploy it to apply the backup settings")
		}
		return resp, nil
	}
	if !changed {
		// What runs is unchanged: the new commit is as deployed as the last.
		r.markDeployed(appID, hash)
		return resp, nil
	}
	psCtx, cancel := context.WithTimeout(ctx, dockerCallTimeout)
	running, err := r.runningProjects(psCtx)
	cancel()
	switch {
	case err != nil:
		resp.Warnings = append(resp.Warnings, "could not check whether the app is running, so it was not redeployed: "+err.Error())
	case !running[projectName(appID)]:
		r.markDeployed(appID, hash) // stopped: its next start deploys this commit
	default:
		if err := r.redeploy(ctx, appID); err != nil {
			resp.Warnings = append(resp.Warnings, "redeploy failed: "+err.Error())
		} else {
			resp.Redeployed = true
			r.markDeployed(appID, hash)
		}
	}
	return resp, nil
}

// PlannedBackup is one backup of one app, as the scheduler plans it.
type PlannedBackup struct {
	AppID string
	recipe.EffectiveBackup
}

// BackupPlan reads every app's effective backups (file reads only: the
// scheduler calls it when something changed, never on a timer). An app
// whose backups can't be read is left out and named in the error.
func (r *Repository) BackupPlan() ([]PlannedBackup, error) {
	entries, err := os.ReadDir(r.cfg.GetAppsDataDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var (
		out      []PlannedBackup
		problems []string
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(r.cfg.GetAppsDataDir(), e.Name())
		cfg, err := r.appBackups(dir, r.appComposeFiles(dir))
		if err != nil {
			problems = append(problems, e.Name()+": "+err.Error())
			continue
		}
		for _, b := range cfg.backups {
			out = append(out, PlannedBackup{AppID: e.Name(), EffectiveBackup: b})
		}
	}
	if len(problems) > 0 {
		return out, errors.New(strings.Join(problems, "; "))
	}
	return out, nil
}

// ServiceContainerPrefix starts the names of the containers compose runs for
// an app's service (<project>-<service>-<n>).
func ServiceContainerPrefix(appID, service string) string {
	return projectName(appID) + "-" + service + "-"
}
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
import (
	"context"
	"path/filepath"
	"sync"
	"time"
```

**with:**

```go
import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
	// mountCache holds each app's resolved compose model for backups,
	// keyed by HEAD and the derived env files (see appComposeModel).
	mountMu    sync.Mutex
	mountCache map[string]mountCacheEntry
}
```

**with:**

```go
	// mountCache holds each app's resolved compose model for backups,
	// keyed by HEAD and the derived env files (see appComposeModel).
	mountMu    sync.Mutex
	mountCache map[string]mountCacheEntry

	// Backup runs: the helper image is pulled once; runHelper runs one
	// helper container, docker one short docker command, helperBinary
	// finds the binary the helper runs, now is the clock (all replaced in
	// tests).
	helperImageReady atomic.Bool
	runHelper        func(ctx context.Context, name string, args []string, stdout io.Writer) (exit int, stderr []byte, err error)
	docker           func(ctx context.Context, args ...string) error
	helperBinary     func() (string, error)
	now              func() time.Time
}
```

**In `internal/infra/orchestrator/docker_compose/repository.go`, replace:**

```go
		composeOutput: dockerOutputIn,
		mountCache:    make(map[string]mountCacheEntry),
	}
```

**with:**

```go
		composeOutput: dockerOutputIn,
		mountCache:    make(map[string]mountCacheEntry),
		runHelper:     runDockerHelper,
		docker:        dockerCombined,
		helperBinary:  agentExecutable,
		now:           time.Now,
	}
```

- [ ] **Step 5: Run the tests**

Run: `go mod tidy && git diff --exit-code go.sum && go test -race ./internal/infra/orchestrator/docker_compose/ && go test -count=1 -v -run TestBackupThroughDocker ./internal/infra/orchestrator/docker_compose/ | grep -E '^(---|ok)'`

Expected: PASS, and `--- PASS: TestBackupThroughDocker` (≈50 s the first time: it builds the agent with `CGO_ENABLED=0` and pulls `alpine:3.20`; ≈9 s after). Without Docker it reports `--- SKIP` — run it on a machine with Docker before committing; CI's runners have it.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add go.mod internal/infra/orchestrator/docker_compose
git commit -F - <<'EOF'
feat(agent): backup engine: helper runs, zstd upload, manifests, retention, restore points

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 9: The backup scheduler

**Files:**
- Create: `internal/app/agent/backup/scheduler.go`, `scheduler_test.go`

**Interfaces:**
- Consumes: `dockercompose.PlannedBackup`, `BackupJob`, `BackupResult`, `ErrNotRunnable`, `ServiceContainerPrefix` (Task 8); `command.BackupHealth`/`BackupState*`, `RunBackupResponse`, `PointKind` (Task 3); `fsutil.WriteFile`.
- Produces (package `backup`, import `winterflow/internal/app/agent/backup`): `type Engine interface{ BackupPlan() ([]dockercompose.PlannedBackup, error); RunBackup(ctx, dockercompose.BackupJob) (dockercompose.BackupResult, error) }`; `type Clock interface{ Now() time.Time; After(d) <-chan time.Time }`; `New(engine Engine, dataDir string, log *logger.Logger) *Scheduler`; `(s) Run(ctx)`; `(s) Reload()`; `(s) Forget(appID string)`; `(s) RunNow(appID, name string) <-chan Outcome`; `(s) Health(appID string, now time.Time, running func(service string) bool) []command.BackupHealth`; `(s) Decorate(apps []command.AppStatus, now time.Time) []command.AppStatus`; `type Outcome struct{ Result command.RunBackupResponse; Err error }`; `type State struct{ LastAttempt, LastSuccess time.Time; LastError string; LastSize int64; Fingerprint string; EnabledAt time.Time }` (state.json, keyed `<app-id>/<backup>`); `Offset(appID, name string, every time.Duration) time.Duration`; `RunTimeout = 30*time.Minute`

- [ ] **Step 1: Write the failing tests**

**Create `internal/app/agent/backup/scheduler_test.go`:**

```go
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/pkg/logger"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// fakeClock is a manual clock: After fires once Advance passes its time;
// armed reports every wait the scheduler starts.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	armed  chan time.Duration
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newClock() *fakeClock { return &fakeClock{now: t0, armed: make(chan time.Duration, 100)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, fakeTimer{at: c.now.Add(d), ch: ch})
	c.armed <- d
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, tm := range c.timers {
		if !tm.at.After(c.now) {
			tm.ch <- c.now
			continue
		}
		kept = append(kept, tm)
	}
	c.timers = kept
}

// fakeEngine plans plan and records runs; a run answers result, or waits on
// block (or its context) first.
type fakeEngine struct {
	mu     sync.Mutex
	plan   []dockercompose.PlannedBackup
	runs   chan dockercompose.BackupJob
	block  chan struct{}
	ignore bool // keep blocking past the context
	result func(dockercompose.BackupJob) (dockercompose.BackupResult, error)
}

func newEngine(plan ...dockercompose.PlannedBackup) *fakeEngine {
	return &fakeEngine{plan: plan, runs: make(chan dockercompose.BackupJob, 100)}
}

func (e *fakeEngine) BackupPlan() ([]dockercompose.PlannedBackup, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]dockercompose.PlannedBackup(nil), e.plan...), nil
}

func (e *fakeEngine) setPlan(plan ...dockercompose.PlannedBackup) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.plan = plan
}

func (e *fakeEngine) RunBackup(ctx context.Context, job dockercompose.BackupJob) (dockercompose.BackupResult, error) {
	e.runs <- job
	e.mu.Lock()
	block, ignore, result := e.block, e.ignore, e.result
	e.mu.Unlock()
	if block != nil {
		if ignore {
			<-block
		} else {
			select {
			case <-block:
			case <-ctx.Done():
				return dockercompose.BackupResult{}, ctx.Err()
			}
		}
	}
	if result != nil {
		return result(job)
	}
	return dockercompose.BackupResult{Point: &command.RestorePoint{ID: "p", Size: 42}, Fingerprint: "fp-" + job.Backup, Size: 42}, nil
}

func planned(app, name string, typ recipe.Type, enabled bool, every string) dockercompose.PlannedBackup {
	return dockercompose.PlannedBackup{AppID: app, EffectiveBackup: recipe.EffectiveBackup{
		Name: name, Type: typ, Enabled: enabled, Storage: "disk", Every: every, Service: "svc"}}
}

// start runs a scheduler on a fake clock (opts adjust it first) until the
// test ends.
func start(t *testing.T, e *fakeEngine, opts ...func(*Scheduler)) (*Scheduler, *fakeClock, string) {
	t.Helper()
	dir := t.TempDir()
	s := New(e, dir, logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	clock := newClock()
	s.clock = clock
	for _, o := range opts {
		o(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s, clock, dir
}

// idle waits until the scheduler waits on its timer and returns the wait.
func idle(t *testing.T, c *fakeClock) time.Duration {
	t.Helper()
	select {
	case d := <-c.armed:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduler never went idle")
		return 0
	}
}

func ran(t *testing.T, e *fakeEngine) dockercompose.BackupJob {
	t.Helper()
	select {
	case j := <-e.runs:
		return j
	case <-time.After(5 * time.Second):
		t.Fatal("no run")
		return dockercompose.BackupJob{}
	}
}

func noRun(t *testing.T, e *fakeEngine) {
	t.Helper()
	select {
	case j := <-e.runs:
		t.Fatalf("unexpected run %+v", j)
	default:
	}
}

// Backups enabled together start at stable offsets within their interval,
// earliest first, then every interval after their last attempt; manual and
// custom backups and backups that are off are never scheduled.
func TestScheduleOffsetsAndOrder(t *testing.T) {
	a, b := planned("app-1", "db", recipe.TypeSQLite, true, "1h"), planned("app-2", "db", recipe.TypeSQLite, true, "1h")
	e := newEngine(a, b,
		planned("app-1", "files", recipe.TypeFiles, true, recipe.Manual),
		planned("app-1", "replica", recipe.TypeCustom, true, ""),
		planned("app-3", "db", recipe.TypeSQLite, false, "1h"))
	_, clock, _ := start(t, e)
	offA, offB := Offset("app-1", "db", time.Hour), Offset("app-2", "db", time.Hour)
	if offA == offB || offA >= time.Hour || offB >= time.Hour || Offset("app-1", "db", time.Hour) != offA {
		t.Fatalf("offsets %s %s", offA, offB)
	}
	first, second := "app-1", "app-2"
	if offB < offA {
		first, second, offA, offB = "app-2", "app-1", offB, offA
	}
	if wait := idle(t, clock); wait != offA {
		t.Fatalf("first wait = %s, want %s", wait, offA)
	}
	clock.Advance(offB)
	if j := ran(t, e); j.AppID != first || j.Kind != command.PointScheduled {
		t.Fatalf("first run = %+v", j)
	}
	if j := ran(t, e); j.AppID != second {
		t.Fatalf("second run = %+v", j)
	}
	// Next: the first one, an hour after its attempt (at offB).
	if wait := idle(t, clock); wait != time.Hour {
		t.Fatalf("next wait = %s", wait)
	}
	noRun(t, e)
}

func TestStateIsRecordedAndReloaded(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"))
	s, clock, dir := start(t, e)
	idle(t, clock)
	clock.Advance(5 * time.Minute)
	ran(t, e)
	idle(t, clock)
	raw, err := os.ReadFile(filepath.Join(dir, "backups", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	got := st["app-1/db"]
	if !got.EnabledAt.Equal(t0) || got.LastAttempt.IsZero() || !got.LastSuccess.Equal(got.LastAttempt) || got.LastSize != 42 || got.Fingerprint != "fp-db" || got.LastError != "" {
		t.Fatalf("state = %+v", got)
	}
	if info, _ := os.Stat(filepath.Join(dir, "backups", "state.json")); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode())
	}
	h := s.Health("app-1", clock.Now(), nil)
	if len(h) != 1 || h[0].State != command.BackupOK || h[0].LastSuccess != got.LastSuccess.Unix() {
		t.Fatalf("health = %+v", h)
	}
	// A new scheduler reads the state back.
	again := New(e, dir, s.log)
	if st := again.state["app-1/db"]; st == nil || !st.LastAttempt.Equal(got.LastAttempt) {
		t.Fatalf("reloaded = %+v", st)
	}
	// An unreadable state file is an empty state.
	if err := os.WriteFile(filepath.Join(dir, "backups", "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fresh := New(e, dir, s.log); len(fresh.state) != 0 {
		t.Fatalf("state = %+v", fresh.state)
	}
}

// Back up now goes ahead of the schedule; a request for a backup already
// running or queued joins that run.
func TestRunNowJoinsAndGoesFirst(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"), planned("app-1", "media", recipe.TypeFiles, true, recipe.Manual))
	e.block = make(chan struct{})
	s, clock, _ := start(t, e)
	idle(t, clock)
	clock.Advance(10 * time.Minute)
	if j := ran(t, e); j.Backup != "db" {
		t.Fatalf("run = %+v", j)
	}
	// db is running: a request for it joins; media queues once for two.
	joined := s.RunNow("app-1", "db")
	m1, m2 := s.RunNow("app-1", "media"), s.RunNow("app-1", "media")
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupRunning || h[1].State != command.BackupNever {
		t.Fatalf("health = %+v", h)
	}
	close(e.block)
	if o := <-joined; o.Err != nil || o.Result.Backup != "db" || o.Result.Point == nil {
		t.Fatalf("joined = %+v", o)
	}
	if j := ran(t, e); j.Backup != "media" || j.Kind != command.PointManual {
		t.Fatalf("manual run = %+v", j)
	}
	o1, o2 := <-m1, <-m2
	if o1.Err != nil || o2.Err != nil || o1.Result.AppID != "app-1" {
		t.Fatalf("outcomes = %+v %+v", o1, o2)
	}
	idle(t, clock)
	noRun(t, e)
}

// A run past its deadline fails (the engine stops its helper on the
// context); one that doesn't stop is abandoned after the grace.
func TestRunDeadline(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.block = make(chan struct{})
	defer close(e.block)
	s, clock, _ := start(t, e, func(s *Scheduler) { s.runTimeout, s.grace = 20*time.Millisecond, 20*time.Millisecond })
	idle(t, clock)
	o := <-s.RunNow("app-1", "db")
	if o.Err == nil || !strings.Contains(o.Err.Error(), "deadline exceeded") {
		t.Fatalf("outcome = %+v", o)
	}
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupFailing || !strings.Contains(h[0].LastError, "deadline") {
		t.Fatalf("health = %+v", h)
	}
	e.mu.Lock()
	e.ignore = true
	e.mu.Unlock()
	o = <-s.RunNow("app-1", "db")
	if o.Err == nil || !strings.Contains(o.Err.Error(), "did not stop after its time limit") {
		t.Fatalf("abandoned = %+v", o)
	}
	ran(t, e)
	ran(t, e)
}

func TestReloadAndForget(t *testing.T) {
	e := newEngine()
	s, clock, dir := start(t, e)
	if wait := idle(t, clock); wait != idleWait {
		t.Fatalf("idle wait = %s", wait)
	}
	e.setPlan(planned("app-1", "db", recipe.TypeSQLite, true, "5m"), planned("app-2", "db", recipe.TypeSQLite, true, "5m"))
	s.Reload()
	idle(t, clock)
	clock.Advance(5 * time.Minute)
	ran(t, e)
	ran(t, e)
	idle(t, clock)
	// Turned off: no longer scheduled, and its enabled time is cleared.
	e.setPlan(planned("app-1", "db", recipe.TypeSQLite, false, "5m"), planned("app-2", "db", recipe.TypeSQLite, true, "5m"))
	s.Reload()
	idle(t, clock)
	s.mu.Lock()
	if !s.state["app-1/db"].EnabledAt.IsZero() {
		t.Fatal("enabled_at kept for a backup turned off")
	}
	s.mu.Unlock()
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupOff {
		t.Fatalf("health = %+v", h)
	}
	// A deleted app: queued requests fail, state pruned, plan dropped.
	e.mu.Lock()
	e.block = make(chan struct{})
	e.mu.Unlock()
	queued := s.RunNow("app-2", "db")
	running := ran(t, e)
	if running.AppID != "app-2" {
		t.Fatalf("run = %+v", running)
	}
	again := s.RunNow("app-2", "other")
	e.setPlan(planned("app-1", "db", recipe.TypeSQLite, false, "5m"))
	s.Forget("app-2")
	if o := <-again; o.Err == nil || o.Err.Error() != "the app was deleted" {
		t.Fatalf("queued = %+v", o)
	}
	if o := <-queued; o.Err == nil {
		t.Fatalf("the deleted app's run was not stopped: %+v", o)
	}
	close(e.block)
	idle(t, clock)
	raw, _ := os.ReadFile(filepath.Join(dir, "backups", "state.json"))
	if strings.Contains(string(raw), "app-2") {
		t.Fatalf("state kept the deleted app: %s", raw)
	}
	if h := s.Health("app-2", clock.Now(), nil); len(h) != 0 {
		t.Fatalf("health = %+v", h)
	}
}

// A run that finds nothing changed is a success.
func TestUnchangedIsASuccess(t *testing.T) {
	e := newEngine(planned("app-1", "db", recipe.TypeSQLite, true, recipe.Manual))
	e.result = func(dockercompose.BackupJob) (dockercompose.BackupResult, error) {
		return dockercompose.BackupResult{Unchanged: true, Fingerprint: "fp"}, nil
	}
	s, clock, _ := start(t, e)
	idle(t, clock)
	o := <-s.RunNow("app-1", "db")
	if o.Err != nil || !o.Result.Unchanged || o.Result.Point != nil {
		t.Fatalf("outcome = %+v", o)
	}
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupOK {
		t.Fatalf("health = %+v", h)
	}
	e.mu.Lock()
	e.result = func(dockercompose.BackupJob) (dockercompose.BackupResult, error) {
		return dockercompose.BackupResult{}, errors.New("boom")
	}
	e.mu.Unlock()
	if o := <-s.RunNow("app-1", "db"); o.Err == nil {
		t.Fatal("failure lost")
	}
	if h := s.Health("app-1", clock.Now(), nil); h[0].State != command.BackupFailing {
		t.Fatalf("health = %+v", h)
	}
	ran(t, e)
	ran(t, e)
}

// countingEngine counts how often the plan is read.
type countingEngine struct {
	*fakeEngine
	mu    sync.Mutex
	plans int
}

func (c *countingEngine) BackupPlan() ([]dockercompose.PlannedBackup, error) {
	c.mu.Lock()
	c.plans++
	c.mu.Unlock()
	return c.fakeEngine.BackupPlan()
}

// A run refused before it started (the backup was turned off, or the app
// changed, without a Reload) records no failure; the plan is reread, and a
// scheduled run counts as an attempt so it isn't picked again at once.
func TestNotRunnableRereadsThePlan(t *testing.T) {
	e := &countingEngine{fakeEngine: newEngine(planned("app-1", "db", recipe.TypeSQLite, true, "5m"))}
	e.result = func(dockercompose.BackupJob) (dockercompose.BackupResult, error) {
		return dockercompose.BackupResult{}, fmt.Errorf("refused: %w", dockercompose.ErrNotRunnable)
	}
	s := New(e, t.TempDir(), logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	clock := newClock()
	s.clock = clock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	idle(t, clock)
	clock.Advance(5 * time.Minute)
	ran(t, e.fakeEngine)
	if wait := idle(t, clock); wait != 5*time.Minute {
		t.Fatalf("next wait = %s: the refused run was not counted as an attempt", wait)
	}
	e.mu.Lock()
	plans := e.plans
	e.mu.Unlock()
	if plans != 2 {
		t.Fatalf("plan read %d times, want 2", plans)
	}
	s.mu.Lock()
	st := *s.state["app-1/db"]
	s.mu.Unlock()
	if st.LastError != "" || !st.LastSuccess.IsZero() || st.LastAttempt.IsZero() {
		t.Fatalf("state = %+v", st)
	}
	// Back up now refused: no attempt either.
	if o := <-s.RunNow("app-1", "db"); !errors.Is(o.Err, dockercompose.ErrNotRunnable) {
		t.Fatalf("outcome = %+v", o)
	}
	ran(t, e.fakeEngine)
}

func TestHealthStates(t *testing.T) {
	now := t0
	sched := recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Enabled: true, Every: "1h"}
	manual := recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Enabled: true, Every: recipe.Manual}
	custom := recipe.EffectiveBackup{Name: "r", Type: recipe.TypeCustom, Enabled: true, Service: "litestream"}
	off := recipe.EffectiveBackup{Name: "db", Type: recipe.TypeSQLite, Every: "1h"}
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	up := func(s string) bool { return s == "litestream" }
	for _, tc := range []struct {
		name    string
		b       recipe.EffectiveBackup
		st      *State
		running bool
		svc     func(string) bool
		want    command.BackupState
	}{
		{"off", off, &State{LastError: "x"}, false, nil, command.BackupOff},
		{"running", sched, &State{LastError: "x"}, true, nil, command.BackupRunning},
		{"failing", sched, &State{LastError: "x", LastSuccess: ago(time.Minute)}, false, nil, command.BackupFailing},
		{"never", sched, &State{EnabledAt: ago(time.Hour)}, false, nil, command.BackupNever},
		{"never, no state", sched, nil, false, nil, command.BackupNever},
		{"late, never run", sched, &State{EnabledAt: ago(3 * time.Hour)}, false, nil, command.BackupLate},
		{"ok", sched, &State{LastSuccess: ago(90 * time.Minute)}, false, nil, command.BackupOK},
		{"late", sched, &State{LastSuccess: ago(121 * time.Minute)}, false, nil, command.BackupLate},
		{"manual never", manual, &State{EnabledAt: ago(1000 * time.Hour)}, false, nil, command.BackupNever},
		{"manual ok forever", manual, &State{LastSuccess: ago(1000 * time.Hour)}, false, nil, command.BackupOK},
		{"custom running", custom, nil, false, up, command.BackupOK},
		{"custom stopped", custom, nil, false, func(string) bool { return false }, command.BackupOff},
		{"custom unknown", custom, nil, false, nil, command.BackupOff},
	} {
		if got := state(tc.b, tc.st, tc.running, now, tc.svc); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestDecorate(t *testing.T) {
	e := newEngine(planned("app-1", "replica", recipe.TypeCustom, true, ""), planned("app-1", "db", recipe.TypeSQLite, false, "1h"))
	s, clock, _ := start(t, e)
	idle(t, clock)
	apps := s.Decorate([]command.AppStatus{
		{AppID: "app-1", Containers: []command.ContainerStatus{{Name: "wf-app-1-svc-1", StatusCode: command.ContainerStatusActive}}},
		{AppID: "app-2"},
	}, clock.Now())
	if len(apps[0].Backups) != 2 || apps[0].Backups[0].State != command.BackupOK || apps[0].Backups[1].State != command.BackupOff || apps[1].Backups != nil {
		t.Fatalf("apps = %+v", apps)
	}
	apps[0].Containers[0].StatusCode = command.ContainerStatusStopped
	if got := s.Decorate(apps, clock.Now()); got[0].Backups[0].State != command.BackupOff {
		t.Fatalf("stopped service = %+v", got[0].Backups)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/agent/backup/`

Expected: build failure: `no non-test Go files`.

- [ ] **Step 3: Implement**

**Create `internal/app/agent/backup/scheduler.go`:**

```go
// Package backup is the agent's backup scheduler: one goroutine, next to the
// source poller and independent of the command pipeline, that runs every
// enabled sqlite and files backup on its schedule and the Back up now
// requests, strictly one run at a time. It wakes on a single timer set to
// the earliest due time, keeps each backup's last attempt and success in
// {AGENT_DATA_DIR}/backups/state.json, and reports each backup's health for
// apps.status.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	dockercompose "winterflow/internal/infra/orchestrator/docker_compose"
	"winterflow/pkg/fsutil"
	"winterflow/pkg/logger"
)

// Engine runs backups: the Docker Compose orchestrator.
type Engine interface {
	BackupPlan() ([]dockercompose.PlannedBackup, error)
	RunBackup(ctx context.Context, job dockercompose.BackupJob) (dockercompose.BackupResult, error)
}

// Clock is the scheduler's time (a fake in tests).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Limits of one run.
const (
	// RunTimeout bounds a run: the dump and the upload.
	RunTimeout = 30 * time.Minute
	// abandonAfter is how long past its deadline a run gets to stop before
	// the scheduler gives up on it (a storage call stuck on a dead mount)
	// and moves on.
	abandonAfter = time.Minute
	// idleWait is the timer when nothing is scheduled.
	idleWait = 24 * time.Hour
)

// State is one backup's record in state.json.
type State struct {
	LastAttempt time.Time `json:"last_attempt,omitzero"`
	LastSuccess time.Time `json:"last_success,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	LastSize    int64     `json:"last_size,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	EnabledAt   time.Time `json:"enabled_at,omitzero"`
}

// Outcome is what a Back up now request learns when its run ends.
type Outcome struct {
	Result command.RunBackupResponse
	Err    error
}

type key struct{ app, name string }

func (k key) String() string { return k.app + "/" + k.name }

// job is one run, waited for by the requests that joined it.
type job struct {
	key     key
	kind    command.PointKind
	waiters []chan Outcome
	cancel  context.CancelFunc
}

// Scheduler runs backups. Its methods are safe for concurrent use; Run is
// the one goroutine that runs them.
type Scheduler struct {
	engine     Engine
	path       string
	log        *logger.Logger
	clock      Clock
	runTimeout time.Duration
	grace      time.Duration

	wake chan struct{}

	mu      sync.Mutex
	plan    []dockercompose.PlannedBackup
	state   map[string]*State
	dirty   bool // the plan must be reread
	queue   []*job
	running *job
}

// New makes a scheduler keeping its state in dataDir/backups/state.json.
func New(engine Engine, dataDir string, log *logger.Logger) *Scheduler {
	s := &Scheduler{
		engine: engine, path: filepath.Join(dataDir, "backups", "state.json"), log: log,
		clock: realClock{}, runTimeout: RunTimeout, grace: abandonAfter,
		wake: make(chan struct{}, 1), state: map[string]*State{}, dirty: true,
	}
	s.load()
	return s
}

// load reads state.json; a missing or unreadable file is an empty state.
func (s *Scheduler) load() {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var st map[string]*State
	if err := json.Unmarshal(raw, &st); err != nil {
		s.log.Warn("backup state unreadable, starting afresh", "error", err)
		return
	}
	for k, v := range st {
		if v != nil {
			s.state[k] = v
		}
	}
}

// saveLocked writes state.json atomically. The caller holds mu.
func (s *Scheduler) saveLocked() {
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(s.path), 0o700); err == nil {
			err = fsutil.WriteFile(s.path, raw, 0o600)
		}
	}
	if err != nil {
		s.log.Warn("save backup state", "error", err)
	}
}

func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Reload rereads the plan before the next decision: an app was saved,
// rolled back, renamed or deleted, its backups changed, or a storage was
// saved or deleted.
func (s *Scheduler) Reload() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
	s.signal()
}

// Forget drops a deleted app: its queued requests fail, a run in progress is
// stopped, and its state is pruned.
func (s *Scheduler) Forget(appID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.queue[:0]
	for _, j := range s.queue {
		if j.key.app == appID {
			finish(j, Outcome{Err: errors.New("the app was deleted")})
			continue
		}
		kept = append(kept, j)
	}
	s.queue = kept
	if s.running != nil && s.running.key.app == appID && s.running.cancel != nil {
		s.running.cancel()
	}
	plan := s.plan[:0]
	for _, p := range s.plan {
		if p.AppID != appID {
			plan = append(plan, p)
		}
	}
	s.plan = plan
	changed := false
	for k := range s.state {
		if app, _, _ := strings.Cut(k, "/"); app == appID {
			delete(s.state, k)
			changed = true
		}
	}
	if changed {
		s.saveLocked()
	}
	s.dirty = true
	s.signal()
}

// RunNow queues a manual run of an app's backup ahead of the scheduled ones
// and returns where its outcome arrives. A request for a backup that is
// already running or queued joins that run. Whether the backup can run
// (enabled, sqlite or files) is decided when it runs.
func (s *Scheduler) RunNow(appID, name string) <-chan Outcome {
	ch := make(chan Outcome, 1)
	k := key{appID, name}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil && s.running.key == k {
		s.running.waiters = append(s.running.waiters, ch)
		return ch
	}
	for _, j := range s.queue {
		if j.key == k {
			j.waiters = append(j.waiters, ch)
			return ch
		}
	}
	s.queue = append(s.queue, &job{key: k, kind: command.PointManual, waiters: []chan Outcome{ch}})
	s.signal()
	return ch
}

// Run runs backups until ctx is done.
func (s *Scheduler) Run(ctx context.Context) {
	for ctx.Err() == nil {
		s.refresh()
		now := s.clock.Now()
		if j := s.next(now); j != nil {
			s.execute(ctx, j)
			continue
		}
		select {
		case <-ctx.Done():
		case <-s.clock.After(s.untilDue(now)):
		case <-s.wake:
		}
	}
	s.mu.Lock()
	for _, j := range s.queue {
		finish(j, Outcome{Err: errors.New("the agent is stopping")})
	}
	s.queue = nil
	s.mu.Unlock()
}

// refresh rereads the plan when asked to, and records when each backup was
// turned on (its first run is offset from then).
func (s *Scheduler) refresh() {
	s.mu.Lock()
	dirty := s.dirty
	s.dirty = false
	s.mu.Unlock()
	if !dirty {
		return
	}
	plan, err := s.engine.BackupPlan()
	if err != nil {
		s.log.Warn("backup plan: some apps' backups can't be read", "error", err)
	}
	now := s.clock.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plan = plan
	changed := false
	for _, p := range plan {
		k := key{p.AppID, p.Name}.String()
		st := s.state[k]
		switch {
		case p.Enabled && (st == nil || st.EnabledAt.IsZero()):
			if st == nil {
				st = &State{}
				s.state[k] = st
			}
			st.EnabledAt, changed = now, true
		case !p.Enabled && st != nil && !st.EnabledAt.IsZero():
			st.EnabledAt, changed = time.Time{}, true
		}
	}
	if changed {
		s.saveLocked()
	}
}

// next is the job to run now: the oldest Back up now request, else the
// scheduled backup most overdue.
func (s *Scheduler) next(now time.Time) *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) > 0 {
		j := s.queue[0]
		s.queue = s.queue[1:]
		s.running = j
		return j
	}
	var (
		best    *dockercompose.PlannedBackup
		bestDue time.Time
	)
	for i, p := range s.plan {
		due, ok := s.dueLocked(p)
		if ok && !due.After(now) && (best == nil || due.Before(bestDue)) {
			best, bestDue = &s.plan[i], due
		}
	}
	if best == nil {
		return nil
	}
	s.running = &job{key: key{best.AppID, best.Name}, kind: command.PointScheduled}
	return s.running
}

// dueLocked is when a scheduled backup runs next: its last attempt plus
// every; never run, the time it was turned on plus a stable offset (so
// backups enabled together spread out). ok is false for backups that are
// off, manual or custom.
func (s *Scheduler) dueLocked(p dockercompose.PlannedBackup) (time.Time, bool) {
	every := p.Interval()
	if !p.Enabled || !p.Builtin() || every <= 0 {
		return time.Time{}, false
	}
	st := s.state[key{p.AppID, p.Name}.String()]
	if st == nil {
		return time.Time{}, false
	}
	if !st.LastAttempt.IsZero() {
		return st.LastAttempt.Add(every), true
	}
	return st.EnabledAt.Add(Offset(p.AppID, p.Name, every)), true
}

// Offset is a backup's stable delay within its interval: a hash of
// app-id/backup name, mod every.
func Offset(appID, name string, every time.Duration) time.Duration {
	h := fnv.New64a()
	h.Write([]byte(appID + "/" + name))
	return time.Duration(h.Sum64() % uint64(every))
}

// untilDue is how long until the earliest scheduled backup is due.
func (s *Scheduler) untilDue(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	wait := idleWait
	for _, p := range s.plan {
		if due, ok := s.dueLocked(p); ok {
			wait = min(wait, max(due.Sub(now), 0))
		}
	}
	return wait
}

// execute runs one job within its time limit and records the outcome. A
// run that doesn't stop at its deadline is abandoned after the grace.
func (s *Scheduler) execute(parent context.Context, j *job) {
	ctx, cancel := context.WithTimeout(parent, s.runTimeout)
	defer cancel()
	start := s.clock.Now()
	s.mu.Lock()
	j.cancel = cancel
	s.mu.Unlock()

	type done struct {
		res dockercompose.BackupResult
		err error
	}
	ch := make(chan done, 1)
	go func() {
		res, err := s.engine.RunBackup(ctx, dockercompose.BackupJob{AppID: j.key.app, Backup: j.key.name, Kind: j.kind})
		ch <- done{res, err}
	}()
	var d done
	select {
	case d = <-ch:
	case <-ctx.Done():
		select {
		case d = <-ch:
		case <-time.After(s.grace):
			d.err = fmt.Errorf("the run did not stop after its time limit of %s", s.runTimeout)
		}
	}
	if d.err == nil && ctx.Err() != nil {
		d.err = fmt.Errorf("the run was stopped: %w", ctx.Err())
	}
	s.record(j, start, d.res, d.err)
}

// record stores a run's outcome as the backup's attempt at start (not for
// an app deleted meanwhile) and answers its waiters. A run refused before
// it started means the plan is stale (an app changed without a Reload, say
// by its upstream): the plan is reread, and only a scheduled run counts as
// an attempt, so it isn't picked again at once.
func (s *Scheduler) record(j *job, start time.Time, res dockercompose.BackupResult, err error) {
	resp := command.RunBackupResponse{AppID: j.key.app, Backup: j.key.name, Unchanged: res.Unchanged, Point: res.Point, Warnings: res.Warnings}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = nil
	if errors.Is(err, dockercompose.ErrNotRunnable) {
		s.dirty = true
		if st := s.state[j.key.String()]; st != nil && j.kind == command.PointScheduled {
			st.LastAttempt = start
			s.saveLocked()
		}
		finish(j, Outcome{Result: resp, Err: err})
		return
	}
	if s.plannedLocked(j.key) {
		st := s.state[j.key.String()]
		if st == nil {
			st = &State{}
			s.state[j.key.String()] = st
		}
		st.LastAttempt = start
		if err != nil {
			st.LastError = err.Error()
			s.log.Warn("backup failed", "app_id", j.key.app, "backup", j.key.name, "error", err)
		} else {
			st.LastError, st.LastSuccess, st.Fingerprint = "", start, res.Fingerprint
			if res.Point != nil {
				st.LastSize = res.Point.Size
			}
		}
		s.saveLocked()
	}
	finish(j, Outcome{Result: resp, Err: err})
}

func (s *Scheduler) plannedLocked(k key) bool {
	for _, p := range s.plan {
		if p.AppID == k.app && p.Name == k.name {
			return true
		}
	}
	return false
}

func finish(j *job, o Outcome) {
	for _, w := range j.waiters {
		w <- o
	}
	j.waiters = nil
}

// Health is the health of every backup of the app at now. running reports
// whether one of the app's services runs (custom backups are ok while
// theirs does).
func (s *Scheduler) Health(appID string, now time.Time, running func(service string) bool) []command.BackupHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []command.BackupHealth
	for _, p := range s.plan {
		if p.AppID != appID {
			continue
		}
		h := command.BackupHealth{Name: p.Name}
		st := s.state[key{p.AppID, p.Name}.String()]
		if st != nil && !st.LastSuccess.IsZero() {
			h.LastSuccess = st.LastSuccess.Unix()
		}
		if st != nil {
			h.LastError = st.LastError
		}
		isRunning := s.running != nil && s.running.key == key{p.AppID, p.Name}
		h.State = state(p.EffectiveBackup, st, isRunning, now, running)
		out = append(out, h)
	}
	return out
}

// state is one backup's health.
func state(b recipe.EffectiveBackup, st *State, running bool, now time.Time, serviceRunning func(string) bool) command.BackupState {
	switch {
	case !b.Enabled:
		return command.BackupOff
	case b.Type == recipe.TypeCustom:
		if serviceRunning != nil && serviceRunning(b.Service) {
			return command.BackupOK
		}
		return command.BackupOff
	case running:
		return command.BackupRunning
	case st != nil && st.LastError != "":
		return command.BackupFailing
	}
	every := b.Interval()
	if st == nil || st.LastSuccess.IsZero() {
		if every > 0 && st != nil && !st.EnabledAt.IsZero() && now.Sub(st.EnabledAt) > 2*every {
			return command.BackupLate
		}
		return command.BackupNever
	}
	if every > 0 && now.Sub(st.LastSuccess) > 2*every {
		return command.BackupLate
	}
	return command.BackupOK
}

// Decorate adds each app's backup health at now to its status. A custom
// backup's service runs when one of the app's containers for it is active.
func (s *Scheduler) Decorate(apps []command.AppStatus, now time.Time) []command.AppStatus {
	for i := range apps {
		a := &apps[i]
		running := func(service string) bool {
			prefix := dockercompose.ServiceContainerPrefix(a.AppID, service)
			for _, c := range a.Containers {
				if strings.HasPrefix(c.Name, prefix) && c.StatusCode == command.ContainerStatusActive {
					return true
				}
			}
			return false
		}
		a.Backups = s.Health(a.AppID, now, running)
	}
	return apps
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race -count=3 -cover ./internal/app/agent/backup/`

Expected: PASS three times under the race detector; coverage ≈ 95%.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/app/agent/backup/scheduler_test.go internal/app/agent/backup/scheduler.go
git commit -F - <<'EOF'
feat(agent): backup scheduler: one timer, one run at a time, state.json, health

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 10: Wire backups into the agent and the standalone bridge

**Files:**
- Create: `internal/app/agent/backups.go` (`Backups` interface, `noBackups`, `SetBackups`, `runBackup`, `RunBackups`, `WithBackupHealth`), `dispatcher_backups_test.go`
- Modify: `internal/app/agent/dispatcher.go` (4 handlers; Reload/Forget hooks; health on `apps.status`), `inprocess.go` (detached commands get their own goroutine), `inprocess_test.go`
- Modify: `internal/infra/bootstrap/standalone.go`, `cmd/agent/main.go` (start the scheduler, decorate the status reporter)
- Modify: `CLAUDE.md` (command surface + a Backups paragraph)

**Interfaces:**
- Consumes: `backup.Scheduler`, `backup.Outcome` (Task 9); `dockercompose.(*Repository).UpdateBackups/ListRestorePoints/DeleteRestorePoint/RemoveHelpers` (Task 8); `command.Type.Detached` (Task 3).
- Produces (package `agent`): `type Backups interface{ Reload(); Forget(appID string); RunNow(appID, name string) <-chan backup.Outcome; Decorate([]command.AppStatus, time.Time) []command.AppStatus }`; `(d *Dispatcher) SetBackups(Backups)`; `RunBackups(ctx, cleaner HelperCleaner, s *backup.Scheduler, log)`; `WithBackupHealth(src StatusSource, b Backups) StatusSource`; handlers for `backup.update|points|run|delete-point`.

- [ ] **Step 1: Write the failing tests**

**Create `internal/app/agent/dispatcher_backups_test.go`:**

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

	"winterflow/internal/app/agent/backup"
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/grpc/proto"
)

// fakeBackups records what the dispatcher asks of the scheduler.
type fakeBackups struct {
	mu       sync.Mutex
	reloads  int
	forgets  []string
	runs     []string
	outcome  backup.Outcome
	hold     chan struct{} // RunNow answers only once closed
	decorate int
}

func (f *fakeBackups) Reload() { f.mu.Lock(); f.reloads++; f.mu.Unlock() }
func (f *fakeBackups) Forget(appID string) {
	f.mu.Lock()
	f.forgets = append(f.forgets, appID)
	f.mu.Unlock()
}
func (f *fakeBackups) RunNow(appID, name string) <-chan backup.Outcome {
	f.mu.Lock()
	f.runs = append(f.runs, appID+"/"+name)
	o, hold := f.outcome, f.hold
	f.mu.Unlock()
	ch := make(chan backup.Outcome, 1)
	go func() {
		if hold != nil {
			<-hold
		}
		ch <- o
	}()
	return ch
}
func (f *fakeBackups) Decorate(apps []command.AppStatus, _ time.Time) []command.AppStatus {
	f.mu.Lock()
	f.decorate++
	f.mu.Unlock()
	for i := range apps {
		apps[i].Backups = []command.BackupHealth{{Name: "db", State: command.BackupOK}}
	}
	return apps
}

func (f *fakeBackups) reloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reloads
}

const backupsCompose = `services:
  web:
    image: example/web
    volumes: [data:/data]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
volumes:
  data:
x-winterflow:
  backups:
    - name: database
      type: sqlite
      volume: data
      path: app.db
    - name: replica
      type: custom
      service: litestream
`

func TestDispatchBackupCommands(t *testing.T) {
	d := newTestDispatcher(t)
	fb := &fakeBackups{outcome: backup.Outcome{Result: command.RunBackupResponse{AppID: "app-1", Backup: "database", Unchanged: true}}}
	d.SetBackups(fb)
	ok := proto.ResponseCode_RESPONSE_CODE_SUCCESS
	if resp := dispatchJSON(t, d, command.TypeStorageSave, command.SaveStorageRequest{Create: true,
		Storage: model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: t.TempDir()}}}); resp.Base.ResponseCode != ok {
		t.Fatalf("storage.save = %+v", resp.Base)
	}
	resp := dispatchJSON(t, d, command.TypeAppSave, command.SaveAppRequest{Draft: true, App: command.AppPayload{AppID: "app-1",
		Config: []byte(`{"name":"demo","files":[{"filename":"compose.yml"}],"backups":[{"name":"database","storage":"disk"}]}`),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(backupsCompose)}}}})
	if resp.Base.ResponseCode != ok {
		t.Fatalf("app.save = %+v", resp.Base)
	}
	reloads := fb.reloadCount()
	if reloads != 2 {
		t.Fatalf("reloads after storage.save and app.save = %d", reloads)
	}

	resp = dispatchJSON(t, d, command.TypeBackupUpdate, command.UpdateBackupsRequest{AppID: "app-1",
		Backups: []recipe.BackupConfig{{Name: "database", Storage: "disk"}, {Name: "replica", Enabled: true, Storage: "disk"}}})
	var updated command.UpdateBackupsResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &updated) != nil || updated.Revision == "" || !updated.Backups[1].Enabled {
		t.Fatalf("backup.update = %+v %s", resp.Base, resp.Payload)
	}
	if fb.reloadCount() != reloads+1 {
		t.Fatal("backup.update did not reload the scheduler")
	}
	resp = dispatchJSON(t, d, command.TypeBackupUpdate, command.UpdateBackupsRequest{AppID: "app-1",
		Backups: []recipe.BackupConfig{{Name: "replica", Enabled: true, Storage: "nas"}}})
	if resp.Base.ResponseCode == ok || !strings.Contains(resp.Base.Detail, `there is no storage "nas"`) {
		t.Fatalf("refused update = %+v", resp.Base)
	}

	resp = dispatchJSON(t, d, command.TypeBackupPoints, command.ListRestorePointsRequest{AppID: "app-1", Backup: "database"})
	var listed command.ListRestorePointsResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &listed) != nil || listed.Points == nil || len(listed.Points) != 0 {
		t.Fatalf("backup.points = %+v %s", resp.Base, resp.Payload)
	}
	resp = dispatchJSON(t, d, command.TypeBackupDeletePoint, command.DeleteRestorePointRequest{AppID: "app-1", Backup: "database", Point: "20261011T120000Z-manual"})
	if resp.Base.ResponseCode == ok || !strings.Contains(resp.Base.Detail, "has no restore point") {
		t.Fatalf("backup.delete-point = %+v", resp.Base)
	}

	resp = dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "database"})
	var run command.RunBackupResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &run) != nil || !run.Unchanged || fb.runs[0] != "app-1/database" {
		t.Fatalf("backup.run = %+v %s", resp.Base, resp.Payload)
	}
	fb.outcome = backup.Outcome{Err: errors.New("database file not found")}
	if resp = dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "database"}); resp.Base.Detail != "database file not found" {
		t.Fatalf("failed run = %+v", resp.Base)
	}
	// A request whose caller gave up stops waiting.
	fb.hold = make(chan struct{})
	defer close(fb.hold)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	payload, _ := json.Marshal(command.RunBackupRequest{AppID: "app-1", Backup: "database"})
	if resp := d.Dispatch(ctx, envelope(string(command.TypeBackupRun), payload)); resp.Base.ResponseCode == ok {
		t.Fatalf("canceled run = %+v", resp.Base)
	}

	resp = dispatchJSON(t, d, command.TypeAppsStatus, command.GetAppsStatusRequest{})
	var status command.GetAppsStatusResponse
	if resp.Base.ResponseCode != ok || json.Unmarshal(resp.Payload, &status) != nil || len(status.Apps) != 1 || status.Apps[0].Backups[0].Name != "db" {
		t.Fatalf("apps.status = %+v %s", resp.Base, resp.Payload)
	}

	if resp = dispatchJSON(t, d, command.TypeAppRename, command.RenameAppRequest{AppID: "app-1", Name: "renamed"}); resp.Base.ResponseCode != ok {
		t.Fatalf("rename = %+v", resp.Base)
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
		if resp := dispatchJSON(t, d, typ, req); resp.Base.ResponseCode == ok || !strings.HasPrefix(resp.Base.Detail, "invalid payload: ") {
			t.Errorf("%s: %+v", typ, resp.Base)
		}
	}
}

// Without a scheduler, Back up now fails cleanly and statuses pass through.
func TestDispatchWithoutBackups(t *testing.T) {
	d := newTestDispatcher(t)
	resp := dispatchJSON(t, d, command.TypeBackupRun, command.RunBackupRequest{AppID: "app-1", Backup: "db"})
	if resp.Base.ResponseCode == proto.ResponseCode_RESPONSE_CODE_SUCCESS || resp.Base.Detail != errNoBackups.Error() {
		t.Fatalf("backup.run = %+v", resp.Base)
	}
	var nb noBackups
	nb.Reload()
	nb.Forget("x")
	if apps := nb.Decorate([]command.AppStatus{{AppID: "a"}}, time.Now()); apps[0].Backups != nil {
		t.Fatal("decorated without a scheduler")
	}
}

type fakeStatus struct{ err error }

func (f fakeStatus) GetAppsStatus(context.Context) ([]command.AppStatus, error) {
	return []command.AppStatus{{AppID: "app-1"}}, f.err
}

type fakeCleaner struct{ err error }

func (f fakeCleaner) RemoveHelpers(context.Context) error { return f.err }

func TestWithBackupHealthAndRunBackups(t *testing.T) {
	fb := &fakeBackups{}
	apps, err := WithBackupHealth(fakeStatus{}, fb).GetAppsStatus(context.Background())
	if err != nil || apps[0].Backups[0].State != command.BackupOK {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	if _, err := WithBackupHealth(fakeStatus{err: errors.New("docker down")}, fb).GetAppsStatus(context.Background()); err == nil {
		t.Fatal("a failed collection passed")
	}
	// RunBackups cleans up, then runs the scheduler until ctx ends.
	d := newTestDispatcher(t)
	s := backup.New(d.orch, t.TempDir(), d.log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { RunBackups(ctx, fakeCleaner{err: errors.New("docker down")}, s, d.log); close(done) }()
	o := <-s.RunNow("app-1", "db")
	if o.Err == nil || !strings.Contains(o.Err.Error(), "app app-1 not found") {
		t.Fatalf("outcome = %+v", o)
	}
	cancel()
	<-done
}
```

**In `internal/app/agent/inprocess_test.go`, replace:**

```go
	"winterflow/internal/infra/transport/bus"
	membus "winterflow/internal/infra/transport/mem/bus"
```

**with:**

```go
	"winterflow/internal/infra/transport/bus"
	"winterflow/internal/infra/transport/codec"
	"winterflow/internal/infra/transport/grpc/proto"
	membus "winterflow/internal/infra/transport/mem/bus"
```

**Append to `internal/app/agent/inprocess_test.go`:**

```go

// blockingRunner holds backup.run until released and answers everything
// else at once.
type blockingRunner struct{ release chan struct{} }

func (b blockingRunner) Dispatch(_ context.Context, req *proto.RequestEnvelope) *proto.ResponseEnvelope {
	if command.Type(req.Type) == command.TypeBackupRun {
		<-b.release
	}
	resp, _ := codec.EncodeResponse("local", req.RequestId, command.Type(req.Type), proto.ResponseCode_RESPONSE_CODE_SUCCESS, "ok", struct{}{})
	return resp
}

// A backup.run waiting for its run never holds up the commands behind it.
func TestInProcessBridgeDetachesBackupRuns(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	log := logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
	cfg := config.NewServerConfig("standalone")
	b := membus.NewBus(log)
	runner := blockingRunner{release: make(chan struct{})}
	bridge := NewInProcessBridge(b, cfg, runner, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results, cancelSub, _ := b.Subscribe(ctx, cfg.GetBusResponseQueue())
	defer cancelSub()
	_ = bridge.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	for _, c := range []bus.CommandMessage{
		{AgentID: "local", RequestID: "run", Type: string(command.TypeBackupRun), Payload: []byte(`{}`)},
		{AgentID: "local", RequestID: "list", Type: string(command.TypeAppsList), Payload: []byte(`{}`)},
	} {
		if err := b.Publish(ctx, cfg.GetBusRequestQueue(), c); err != nil {
			t.Fatal(err)
		}
	}
	next := func() string {
		select {
		case msg := <-results:
			var ntf model.Notification
			_ = json.Unmarshal([]byte(msg.Payload), &ntf)
			return ntf.Ref
		case <-time.After(2 * time.Second):
			t.Fatal("no result")
			return ""
		}
	}
	if ref := next(); ref != "list" {
		t.Fatalf("first result = %s: the bridge waited for backup.run", ref)
	}
	close(runner.release)
	if ref := next(); ref != "run" {
		t.Fatalf("second result = %s", ref)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/agent/`

Expected: build failure: `d.SetBackups undefined`, `undefined: noBackups`, `undefined: WithBackupHealth`, ….

- [ ] **Step 3: Implement**

**Create `internal/app/agent/backups.go`:**

```go
package agent

import (
	"context"
	"errors"
	"time"

	"winterflow/internal/app/agent/backup"
	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

// Backups is the backup scheduler as the dispatcher and the status reporter
// use it (*backup.Scheduler).
type Backups interface {
	// Reload rereads the backups after a change to an app or a storage.
	Reload()
	// Forget drops a deleted app's bookkeeping.
	Forget(appID string)
	// RunNow queues (or joins) a manual run; its outcome arrives when the
	// run ends.
	RunNow(appID, name string) <-chan backup.Outcome
	// Decorate adds each app's backup health to its status.
	Decorate(apps []command.AppStatus, now time.Time) []command.AppStatus
}

var _ Backups = (*backup.Scheduler)(nil)

// errNoBackups answers backup.run on an agent without a scheduler (tests).
var errNoBackups = errors.New("backups are not available on this agent")

// noBackups is the Backups of a dispatcher built without a scheduler.
type noBackups struct{}

func (noBackups) Reload()       {}
func (noBackups) Forget(string) {}
func (noBackups) RunNow(string, string) <-chan backup.Outcome {
	ch := make(chan backup.Outcome, 1)
	ch <- backup.Outcome{Err: errNoBackups}
	return ch
}
func (noBackups) Decorate(apps []command.AppStatus, _ time.Time) []command.AppStatus { return apps }

// SetBackups wires the backup scheduler (standalone and agent do; tests
// may not).
func (d *Dispatcher) SetBackups(b Backups) { d.backups = b }

// runBackup waits for a Back up now request's run (on the backup scheduler,
// never on the command pipeline: backup.run is a detached command).
func (d *Dispatcher) runBackup(ctx context.Context, in command.RunBackupRequest) (command.RunBackupResponse, error) {
	select {
	case o := <-d.backups.RunNow(in.AppID, in.Backup):
		return o.Result, o.Err
	case <-ctx.Done():
		return command.RunBackupResponse{AppID: in.AppID, Backup: in.Backup}, ctx.Err()
	}
}

// HelperCleaner removes leftover backup helper containers.
type HelperCleaner interface {
	RemoveHelpers(ctx context.Context) error
}

// RunBackups is the backup goroutine: it removes the helpers a previous run
// of the agent left behind, then runs the scheduler until ctx is done.
func RunBackups(ctx context.Context, cleaner HelperCleaner, s *backup.Scheduler, log *logger.Logger) {
	if err := cleaner.RemoveHelpers(ctx); err != nil {
		log.Warn("remove leftover backup helpers", "error", err)
	}
	s.Run(ctx)
}

// WithBackupHealth is src with each app's backup health added: what the
// status reporter sends, so a change in it reaches browsers like any other
// status change.
func WithBackupHealth(src StatusSource, b Backups) StatusSource {
	return healthSource{src: src, backups: b}
}

type healthSource struct {
	src     StatusSource
	backups Backups
}

func (h healthSource) GetAppsStatus(ctx context.Context) ([]command.AppStatus, error) {
	apps, err := h.src.GetAppsStatus(ctx)
	if err != nil {
		return nil, err
	}
	return h.backups.Decorate(apps, time.Now()), nil
}
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
	"fmt"
	"strings"
```

**with:**

```go
	"fmt"
	"strings"
	"time"
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
	orch     *dockercompose.Repository
	ingress  port.IngressManager
	log      *logger.Logger
```

**with:**

```go
	orch     *dockercompose.Repository
	ingress  port.IngressManager
	backups  Backups
	log      *logger.Logger
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
	d := &Dispatcher{orch: orch, ingress: ingress, log: log}
```

**with:**

```go
	d := &Dispatcher{orch: orch, ingress: ingress, backups: noBackups{}, log: log}
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
			if err == nil {
				resp.Warnings = append(resp.Warnings, d.reloadIngress(ctx)...)
			}
			return resp, err
```

**with:**

```go
			if err == nil {
				resp.Warnings = append(resp.Warnings, d.reloadIngress(ctx)...)
			}
			d.backups.Reload()
			return resp, err
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
			apps, err := d.orch.GetAppsStatus(ctx)
			return command.GetAppsStatusResponse{Apps: apps}, err
```

**with:**

```go
			apps, err := d.orch.GetAppsStatus(ctx)
			return command.GetAppsStatusResponse{Apps: d.backups.Decorate(apps, time.Now())}, err
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
				err = fmt.Errorf("unknown control action %q", in.Action)
			}
			return command.ControlAppResponse{AppID: in.AppID, Action: in.Action}, err
```

**with:**

```go
				err = fmt.Errorf("unknown control action %q", in.Action)
			}
			if in.Action == command.ControlUpdate {
				d.backups.Reload() // a git-sourced app's compose file may have moved
			}
			return command.ControlAppResponse{AppID: in.AppID, Action: in.Action}, err
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
			res, err := d.orch.DeleteApp(ctx, in.AppID, in.DeleteVolumes)
```

**with:**

```go
			d.backups.Forget(in.AppID) // stop a backup of it before its volumes go
			res, err := d.orch.DeleteApp(ctx, in.AppID, in.DeleteVolumes)
			d.backups.Forget(in.AppID) // and drop what a reload meanwhile read back
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
				return command.RenameAppResponse{AppID: in.AppID, Name: in.Name}, err
			}
			d.reloadIngress(ctx)
```

**with:**

```go
				return command.RenameAppResponse{AppID: in.AppID, Name: in.Name}, err
			}
			d.reloadIngress(ctx)
			d.backups.Reload()
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
			newHead, err := d.orch.Rollback(ctx, in.AppID, in.Hash)
			if err != nil {
```

**with:**

```go
			newHead, err := d.orch.Rollback(ctx, in.AppID, in.Hash)
			d.backups.Reload() // the restored revision has its own backups
			if err != nil {
```

**In `internal/app/agent/dispatcher.go`, replace:**

```go
		command.TypeStorageSave: handle(d, func(ctx context.Context, in command.SaveStorageRequest) (command.SaveStorageResponse, error) {
			return d.orch.SaveStorage(ctx, in.Storage, in.Create)
		}),
		command.TypeStorageDelete: handle(d, func(ctx context.Context, in command.DeleteStorageRequest) (command.DeleteStorageResponse, error) {
			return command.DeleteStorageResponse{Name: in.Name}, d.orch.DeleteStorage(ctx, in.Name)
		}),
```

**with:**

```go
		command.TypeStorageSave: handle(d, func(ctx context.Context, in command.SaveStorageRequest) (command.SaveStorageResponse, error) {
			defer d.backups.Reload()
			return d.orch.SaveStorage(ctx, in.Storage, in.Create)
		}),
		command.TypeStorageDelete: handle(d, func(ctx context.Context, in command.DeleteStorageRequest) (command.DeleteStorageResponse, error) {
			defer d.backups.Reload()
			return command.DeleteStorageResponse{Name: in.Name}, d.orch.DeleteStorage(ctx, in.Name)
		}),

		command.TypeBackupUpdate: handle(d, func(ctx context.Context, in command.UpdateBackupsRequest) (command.UpdateBackupsResponse, error) {
			defer d.backups.Reload()
			return d.orch.UpdateBackups(ctx, in.AppID, in.Backups)
		}),
		command.TypeBackupPoints: handle(d, func(ctx context.Context, in command.ListRestorePointsRequest) (command.ListRestorePointsResponse, error) {
			points, err := d.orch.ListRestorePoints(ctx, in.AppID, in.Backup)
			return command.ListRestorePointsResponse{AppID: in.AppID, Backup: in.Backup, Points: points}, err
		}),
		command.TypeBackupRun: handle(d, d.runBackup),
		command.TypeBackupDeletePoint: handle(d, func(ctx context.Context, in command.DeleteRestorePointRequest) (command.DeleteRestorePointResponse, error) {
			err := d.orch.DeleteRestorePoint(ctx, in.AppID, in.Backup, in.Point)
			return command.DeleteRestorePointResponse{AppID: in.AppID, Backup: in.Backup, Point: in.Point}, err
		}),
```

**In `internal/app/agent/inprocess.go`, replace:**

```go
import (
	"context"

	"winterflow/internal/infra/transport/bus"
```

**with:**

```go
import (
	"context"

	"winterflow/internal/domain/command"
	"winterflow/internal/infra/transport/bus"
```

**In `internal/app/agent/inprocess.go`, replace:**

```go
	bus.SubscribeJSON(ctx, br.bus, br.cfg.GetBusRequestQueue(), br.log, func(cmd bus.CommandMessage) {
		br.handle(ctx, cmd)
	})
```

**with:**

```go
	bus.SubscribeJSON(ctx, br.bus, br.cfg.GetBusRequestQueue(), br.log, func(cmd bus.CommandMessage) {
		// A detached command (backup.run) waits for work done elsewhere: it
		// gets its own goroutine so the pipeline keeps draining.
		if command.Type(cmd.Type).Detached() {
			go br.handle(ctx, cmd)
			return
		}
		br.handle(ctx, cmd)
	})
```

**In `internal/infra/bootstrap/standalone.go`, replace:**

```go
	appagent "winterflow/internal/app/agent"
```

**with:**

```go
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/app/agent/backup"
```

**In `internal/infra/bootstrap/standalone.go`, replace:**

```go
	agentDispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log)
	bridge := appagent.NewInProcessBridge(b, cfg, agentDispatcher, log)
```

**with:**

```go
	agentDispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log)
	// Backups run on their own goroutine, never on the bridge's.
	backups := backup.New(orchestrator, cfg.GetAgentDataDir(), log)
	agentDispatcher.SetBackups(backups)
	go appagent.RunBackups(ctx, orchestrator, backups, log)
	bridge := appagent.NewInProcessBridge(b, cfg, agentDispatcher, log)
```

**In `internal/infra/bootstrap/standalone.go`, replace:**

```go
	go appagent.RunStatusReporter(ctx, orchestrator, func(kind bus.EventKind, payload []byte) error {
```

**with:**

```go
	go appagent.RunStatusReporter(ctx, appagent.WithBackupHealth(orchestrator, backups), func(kind bus.EventKind, payload []byte) error {
```

**In `cmd/agent/main.go`, replace:**

```go
	appagent "winterflow/internal/app/agent"
	backupcli "winterflow/internal/app/backup/cli"
```

**with:**

```go
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/app/agent/backup"
	backupcli "winterflow/internal/app/backup/cli"
```

**In `cmd/agent/main.go`, replace:**

```go
	agent.SetDispatcher(appagent.NewDispatcher(orchestrator, ingressManager, log))
```

**with:**

```go
	dispatcher := appagent.NewDispatcher(orchestrator, ingressManager, log)
	// Backups run on their own goroutine, next to the source poller.
	backups := backup.New(orchestrator, cfg.GetAgentDataDir(), log)
	dispatcher.SetBackups(backups)
	go appagent.RunBackups(ctx, orchestrator, backups, log)
	agent.SetDispatcher(dispatcher)
```

**In `cmd/agent/main.go`, replace:**

```go
	go appagent.RunStatusReporter(ctx, orchestrator, func(kind bus.EventKind, payload []byte) error {
```

**with:**

```go
	go appagent.RunStatusReporter(ctx, appagent.WithBackupHealth(orchestrator, backups), func(kind bus.EventKind, payload []byte) error {
```

**In `CLAUDE.md`, replace:**

````markdown
**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; ingress — `domain.list/create/delete` (registered domains, see below); storages — `storage.list/test/save/delete` (see below); agent — `agent.update` (self-update). All are agent-bound (202 + `request_id`, result over SSE).
````

**with:**

````markdown
**Implemented command surface** (the v1→v2 migration is complete for these): app lifecycle — `app.save` (create/edit, new revision + redeploy), `app.get`, `apps.list` (reconcile), `apps.status`, `app.control` (start/stop/restart/update), `app.delete` (optional `delete_volumes` also removes the app's named volumes), `app.rename`, `app.logs`; Docker resources — `registry.list/create/delete`, `network.list/create/delete`, `volume.list/create/delete`; ingress — `domain.list/create/delete` (registered domains, see below); storages — `storage.list/test/save/delete` (see below); backups — `backup.update/points/run/delete-point` (see below); agent — `agent.update` (self-update). All are agent-bound (202 + `request_id`, result over SSE).
````

**In `CLAUDE.md`, replace:**

````markdown
**Storages:** a storage is a local folder or an S3-compatible bucket registered per server (`{AGENT_DATA_DIR}/storages/<name>.json`, 0600, secret ECIES-encrypted at rest; mirrored without secrets into the API's `server_storages`). Drivers live in `internal/infra/backup/store` (`local`, `s3` via minio-go); every save runs the 30s probe test first. Compose files reference storages as `${WF_STORAGE_<NAME>_<FIELD>}`: only referenced values are materialized at every deploy into the gitignored, never-committed `.winterflow/storage.env` (passed between `.env` and `.env.secrets`); user variables may not start with `WF_STORAGE_`/`WF_BACKUP_`; save-app 400s on a reference to a missing storage. Compose files are always passed with explicit `-f` (`composeFiles`, compose's own detection order plus override), and `run.sh` mirrors the flags. `storage.save` redeploys the running apps that reference it (stopped apps and drafts are left alone); `storage.delete` is refused while an app references it. A deploy is refused while a referenced local storage's folder is missing (10s-bounded stat, so docker never creates a bind source on the system disk); under `/mnt` and `/media` a folder on the same filesystem as `/` (an unmounted mount point) is refused too.
````

**with:**

````markdown
**Storages:** a storage is a local folder or an S3-compatible bucket registered per server (`{AGENT_DATA_DIR}/storages/<name>.json`, 0600, secret ECIES-encrypted at rest; mirrored without secrets into the API's `server_storages`). Drivers live in `internal/infra/backup/store` (`local`, `s3` via minio-go); every save runs the 30s probe test first. Compose files reference storages as `${WF_STORAGE_<NAME>_<FIELD>}`: only referenced values are materialized at every deploy into the gitignored, never-committed `.winterflow/storage.env` (passed between `.env` and `.env.secrets`); user variables may not start with `WF_STORAGE_`/`WF_BACKUP_`; save-app 400s on a reference to a missing storage. Compose files are always passed with explicit `-f` (`composeFiles`, compose's own detection order plus override), and `run.sh` mirrors the flags. `storage.save` redeploys the running apps that reference it (stopped apps and drafts are left alone); `storage.delete` is refused while an app references it. A deploy is refused while a referenced local storage's folder is missing (10s-bounded stat, so docker never creates a bind source on the system disk); under `/mnt` and `/media` a folder on the same filesystem as `/` (an unmounted mount point) is refused too.

**Backups:** a compose file declares what can be backed up under `x-winterflow.backups` (the recipe: `sqlite`, `files`, `custom`; parsed and validated by `internal/domain/backup/recipe`, shared by the API's save-app 400s and the agent); per-deployment settings (on/off, storage, schedule, paths, UI-added backups) are overrides in the committed `.winterflow/config.json` → `backups`, replaced as a whole by `backup.update` and kept across app-editor saves (`keepBackups`). `${WF_BACKUP_<NAME>_<FIELD>}` resolves to the backup's storage (PREFIX narrowed to `<app-id>/<backup>/`, empty while off); an enabled `custom` backup activates the compose profile `wf-backup-<name>` (passed with the user's `COMPOSE_PROFILES` as `--profile`; a disabled one's service is removed with `rm -fs`, since `up --remove-orphans` leaves it). The agent runs `sqlite`/`files` backups itself: the scheduler (`internal/app/agent/backup`, one goroutine, one timer, one run at a time, `{AGENT_DATA_DIR}/backups/state.json`) calls `RunBackup`, which mounts the app's own volumes/binds (resolved from one cached `docker compose config`) into a capped `alpine:3.20` helper running this binary's hidden `backup dump` subcommand (`internal/app/backup/cli`, exit 3 unchanged / 4 missing), zstd-compresses its stdout to the storage with a manifest written last (`internal/infra/backup/layout`), then prunes by tiers under `BACKUP_RETENTION_DAYS`. `backup.run` is a detached command: the in-process bridge runs it off its single goroutine, and the API awaits its result for 2h (`DispatchInput.Wait`). Health rides on `apps.status` (`AppStatus.Backups`).
````

- [ ] **Step 4: Run the tests**

Run: `go build -buildvcs=false ./... && go test -race ./internal/app/agent/... ./internal/infra/bootstrap/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/app/agent/dispatcher_backups_test.go internal/app/agent/inprocess_test.go internal/app/agent/backups.go internal/app/agent/dispatcher.go internal/app/agent/inprocess.go internal/infra/bootstrap/standalone.go cmd/agent/main.go CLAUDE.md
git commit -F - <<'EOF'
feat(agent): run backups beside the command pipeline; backup commands and health

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

### Task 11: API: backup routes, save-app backup checks, contract suite

**Files:**
- Modify: `internal/domain/port/command.go` (`DispatchInput.Wait`), `internal/infra/transport/dispatch/manager.go` (+ test)
- Create: `internal/domain/usecase/app/backups.go` (`checkBackups`, `requireBackupStorages`, `UpdateBackups`, `ListRestorePoints`, `RunBackup`, `DeleteRestorePoint`), `backups_test.go`
- Modify: `internal/domain/usecase/app/storages.go` (`requireStorageRefs` checks the recipe and `WF_BACKUP_`), `usecase.go` (passes `isNew`)
- Create: `internal/app/web/handler/app/backups.go` (`dispatchServerBody`, 4 handlers), `backups_test.go`
- Modify: `internal/app/web/handler/app/routes.go`, `routes_test.go`
- Regenerate: `api/openapi.yaml`, `internal/app/web/testdata/routes.golden`
- Create: `internal/app/web/contract/backups_test.go`; modify `internal/app/web/contract/fakeagent_test.go` (default answers for `backup.*`), `matrix_test.go` (probes)

**Interfaces:**
- Consumes: `recipe.*` (Task 2), backup commands (Task 3).
- Produces: `port.DispatchInput.Wait time.Duration` (0 = the 10-minute default; longer is honored); usecase `UpdateBackups(ctx, userID, serverID string, req command.UpdateBackupsRequest) (string, error)`, `ListRestorePoints(…, req command.ListRestorePointsRequest)`, `RunBackup(…, req command.RunBackupRequest)` (Wait 2h), `DeleteRestorePoint(…, req command.DeleteRestorePointRequest)`; handler types `UpdateBackupsRequest{ServerID; command.UpdateBackupsRequest}`, `RunBackupRequest{ServerID; command.RunBackupRequest}`, `DeleteRestorePointRequest{ServerID; command.DeleteRestorePointRequest}` (embedded: one flat JSON body), `RestorePointsQuery{ServerID, AppID, Backup}`; routes `POST /api/v1/app/update-backups`, `GET /api/v1/app/list-restore-points`, `POST /api/v1/app/run-backup`, `POST /api/v1/app/delete-restore-point`.

- [ ] **Step 1: Write the failing tests**

**Append to `internal/infra/transport/dispatch/manager_test.go`:**

```go

// A command whose result comes when long work ends is awaited longer; a
// shorter Wait never cuts the default.
func TestDispatchWait(t *testing.T) {
	m, _ := newManager(t)
	long := portInput("agent-1", "user-1", command.TypeBackupRun, command.RunBackupRequest{})
	long.Wait = 2 * time.Hour
	short := portInput("agent-1", "user-1", command.TypeAppGet, command.GetAppRequest{})
	short.Wait = time.Second
	ids := map[string]time.Duration{}
	for in, want := range map[*port.DispatchInput]time.Duration{&long: 2 * time.Hour, &short: pendingTTL} {
		id, err := m.Dispatch(context.Background(), *in)
		if err != nil {
			t.Fatal(err)
		}
		ids[id] = want
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, want := range ids {
		left := time.Until(m.pending[id].expires)
		if left < want-time.Minute || left > want {
			t.Errorf("%s waits %s, want %s", id, left, want)
		}
	}
}
```

**In `internal/infra/transport/dispatch/manager_test.go`, replace:**

```go
	"sync"
	"testing"
```

**with:**

```go
	"sync"
	"testing"
	"time"
```

**Create `internal/domain/usecase/app/backups_test.go`:**

```go
package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

const recipeCompose = `services:
  web:
    volumes: [data:/data]
  litestream:
    profiles: [wf-backup-replica]
    environment:
      BUCKET: ${WF_BACKUP_REPLICA_BUCKET}
volumes:
  data:
x-winterflow:
  backups:
    - {name: database, type: sqlite, volume: data, path: app.db}
    - {name: replica, type: custom, service: litestream}
`

func TestSaveAppChecksBackups(t *testing.T) {
	ss := newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb"})
	save := func(id, compose, config string) (*recordingDispatcher, error) {
		disp := &recordingDispatcher{}
		_, err := newStoragesUseCase(disp, ss).SaveApp(context.Background(), "u", "srv-1", model.App{ID: id, Name: "demo"}, command.AppPayload{
			Config: []byte(config), Files: []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
		}, false)
		return disp, err
	}
	for name, tc := range map[string]struct{ id, compose, config string }{
		"a recipe and its own references":  {"", recipeCompose, `{"name":"demo"}`},
		"an enabled backup on a storage":   {"", recipeCompose, `{"backups":[{"name":"replica","enabled":true,"storage":"disk"}]}`},
		"a UI-added backup referenced":     {"", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{"backups":[{"name":"audit","type":"sqlite","volume":"data","path":"a.db"}]}`},
		"an edit without the backups list": {"app-1", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{"name":"demo"}`},
	} {
		if disp, err := save(tc.id, tc.compose, tc.config); err != nil || len(disp.all) != 1 {
			t.Errorf("%s: err %v, dispatches %d", name, err, len(disp.all))
		}
	}
	for name, tc := range map[string]struct{ id, compose, config, want string }{
		"invalid recipe": {"", strings.Replace(recipeCompose, "volume: data", "volume: gone", 1), `{}`,
			`x-winterflow.backups[0].volume: "gone" is not a volume of this compose file`},
		"undeclared backup, new app": {"", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{}`,
			`${WF_BACKUP_AUDIT_PATH}: there is no backup "audit" in this app`},
		"undeclared backup, with the list": {"app-1", recipeCompose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{"backups":[]}`,
			`there is no backup "audit" in this app`},
		"storage not on the server": {"", recipeCompose, `{"backups":[{"name":"database","enabled":true,"storage":"nas"}]}`,
			`backup database: there is no storage "nas" on this server`},
		"bad backups list": {"", recipeCompose, `{"backups":{"name":"x"}}`, "backups: json: cannot unmarshal"},
		"invalid override": {"", recipeCompose, `{"backups":[{"name":"database","every":"1s"}]}`, "backups[0].every: 1s is too often"},
	} {
		disp, err := save(tc.id, tc.compose, tc.config)
		if !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
		if len(disp.all) != 0 {
			t.Errorf("%s: reached the agent", name)
		}
	}
}

func TestBackupCommands(t *testing.T) {
	ctx := context.Background()
	disp := &recordingDispatcher{}
	uc := newStoragesUseCase(disp, newFakeServerStorages(model.StorageSummary{Name: "disk", Type: model.StorageLocal, Location: "/mnt/usb"}))

	req := command.UpdateBackupsRequest{AppID: "a1", Backups: []recipe.BackupConfig{{Name: "db", Enabled: true, Storage: "disk"}, {Name: "x", Storage: "gone"}}}
	if id, err := uc.UpdateBackups(ctx, "u", "srv-1", req); err != nil || id != "req-backup.update" || disp.last().Payload.(command.UpdateBackupsRequest).AppID != "a1" {
		t.Fatalf("update = %q, %v", id, err)
	}
	req.Backups[1].Enabled = true
	if _, err := uc.UpdateBackups(ctx, "u", "srv-1", req); !errors.Is(err, model.ErrRejected) || !strings.Contains(err.Error(), `backup x: there is no storage "gone"`) {
		t.Fatalf("update with a missing storage: %v", err)
	}
	if len(disp.all) != 1 {
		t.Fatal("a refused update reached the agent")
	}
	if _, err := uc.RunBackup(ctx, "u", "srv-1", command.RunBackupRequest{AppID: "a1", Backup: "db"}); err != nil || disp.last().Wait != backupRunWait || disp.last().Type != command.TypeBackupRun {
		t.Fatalf("run = %+v, %v", disp.last(), err)
	}
	if _, err := uc.ListRestorePoints(ctx, "u", "srv-1", command.ListRestorePointsRequest{AppID: "a1", Backup: "db"}); err != nil || disp.last().Type != command.TypeBackupPoints {
		t.Fatalf("points: %v", err)
	}
	if _, err := uc.DeleteRestorePoint(ctx, "u", "srv-1", command.DeleteRestorePointRequest{AppID: "a1", Backup: "db", Point: "p"}); err != nil || disp.last().Type != command.TypeBackupDeletePoint {
		t.Fatalf("delete point: %v", err)
	}
	// Without the index, the agent's check is the only one.
	bare := newStoragesUseCase(&recordingDispatcher{}, nil)
	if _, err := bare.UpdateBackups(ctx, "u", "srv-1", req); err != nil {
		t.Fatalf("no index: %v", err)
	}
}
```

**Create `internal/app/web/handler/app/backups_test.go`:**

```go
package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/pkg/logger"
)

func TestBackupRoutesDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(*Handler) http.HandlerFunc
		request *http.Request
		typ     command.Type
	}{
		{"update", func(h *Handler) http.HandlerFunc { return h.UpdateBackups },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backups":[{"name":"db","every":"1h"}]}`)), command.TypeBackupUpdate},
		{"points", func(h *Handler) http.HandlerFunc { return h.ListRestorePoints },
			httptest.NewRequest("GET", "/x?server_id=s1&app_id=a1&backup=db", nil), command.TypeBackupPoints},
		{"run", func(h *Handler) http.HandlerFunc { return h.RunBackup },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`)), command.TypeBackupRun},
		{"delete point", func(h *Handler) http.HandlerFunc { return h.DeleteRestorePoint },
			httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db","point":"20261011T120000Z-manual"}`)), command.TypeBackupDeletePoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			w := do(tc.handler(h), authed(tc.request))
			if w.Code != http.StatusAccepted || decodeRequestID(t, w) != "req-123" {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if fd.last.Type != tc.typ || fd.last.AgentID != "s1" || fd.last.UserID != "u1" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
}

func TestBackupRoutesRejectBadInput(t *testing.T) {
	type route = func(*Handler) http.HandlerFunc
	update := route(func(h *Handler) http.HandlerFunc { return h.UpdateBackups })
	points := route(func(h *Handler) http.HandlerFunc { return h.ListRestorePoints })
	run := route(func(h *Handler) http.HandlerFunc { return h.RunBackup })
	del := route(func(h *Handler) http.HandlerFunc { return h.DeleteRestorePoint })
	for _, tc := range []struct {
		name    string
		handler route
		method  string
		target  string
		body    string
		want    int
		msg     string
	}{
		{"update bad json", update, "POST", "/x", `{`, http.StatusBadRequest, "invalid request body"},
		{"update no server", update, "POST", "/x", `{"app_id":"a1"}`, http.StatusBadRequest, "server_id is required"},
		{"update bad app", update, "POST", "/x", `{"server_id":"s1","app_id":".."}`, http.StatusBadRequest, "invalid app id"},
		{"update enabled without storage", update, "POST", "/x", `{"server_id":"s1","app_id":"a1","backups":[{"name":"db","enabled":true}]}`,
			http.StatusBadRequest, "an enabled backup needs a storage"},
		{"update foreign server", update, "POST", "/x", `{"server_id":"other","app_id":"a1"}`, http.StatusForbidden, ""},
		{"run bad backup", run, "POST", "/x", `{"server_id":"s1","app_id":"a1","backup":"Bad"}`, http.StatusBadRequest, "is not a valid backup name"},
		{"run foreign server", run, "POST", "/x", `{"server_id":"other","app_id":"a1","backup":"db"}`, http.StatusForbidden, ""},
		{"delete bad point", del, "POST", "/x", `{"server_id":"s1","app_id":"a1","backup":"db","point":"x"}`, http.StatusBadRequest, "invalid restore point id"},
		{"points no server", points, "GET", "/x?app_id=a1&backup=db", "", http.StatusBadRequest, "server_id is required"},
		{"points foreign server", points, "GET", "/x?server_id=other&app_id=a1&backup=db", "", http.StatusForbidden, ""},
		{"points no backup", points, "GET", "/x?server_id=s1&app_id=a1", "", http.StatusBadRequest, "is not a valid backup name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fd, _ := newHandler(t)
			r := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			w := do(tc.handler(h), authed(r))
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.msg) {
				t.Fatalf("status %d, want %d %q: %s", w.Code, tc.want, tc.msg, w.Body.String())
			}
			if fd.last.Type != "" {
				t.Fatalf("dispatched %+v", fd.last)
			}
		})
	}
	h, _, _ := newHandler(t)
	if w := do(h.RunBackup, httptest.NewRequest("POST", "/x", strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`))); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", w.Code)
	}
}

func TestBackupRoutesReportFailures(t *testing.T) {
	failing := NewHandler(&Deps{
		Logger:            logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}),
		CommandDispatcher: failingDispatcher{},
		AppRepository:     &fakeAppRepo{},
		Servers:           &fakeServerAccess{owned: map[string]bool{"s1": true}},
	})
	for want, w := range map[string]*httptest.ResponseRecorder{
		"failed to run backup": do(failing.RunBackup, authed(httptest.NewRequest("POST", "/x",
			strings.NewReader(`{"server_id":"s1","app_id":"a1","backup":"db"}`)))),
		"failed to list restore points": do(failing.ListRestorePoints, authed(httptest.NewRequest("GET", "/x?server_id=s1&app_id=a1&backup=db", nil))),
	} {
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), want) {
			t.Errorf("%s: %d %s", want, w.Code, w.Body.String())
		}
	}
	// A rejection by the usecase (a storage the server doesn't have) is a
	// 400 with its reason.
	h := storagesHandler(fakeServerStorages{rows: []model.StorageSummary{{Name: "disk"}}})
	w := do(h.UpdateBackups, authed(httptest.NewRequest("POST", "/x",
		strings.NewReader(`{"server_id":"s1","app_id":"a1","backups":[{"name":"db","enabled":true,"storage":"nas"}]}`))))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `backup db: there is no storage \"nas\" on this server`) {
		t.Fatalf("rejected = %d %s", w.Code, w.Body.String())
	}
}
```

**In `internal/app/web/handler/app/routes_test.go`, replace:**

```go
	want := map[string]bool{
		"GET /api/v1/app/get-apps":         true,
		"GET /api/v1/app/get-apps-status":  true,
		"POST /api/v1/app/refresh-apps":    true,
		"POST /api/v1/app/save-app":        false,
		"GET /api/v1/app/get-app":          false,
		"GET /api/v1/app/get-logs":         false,
		"GET /api/v1/app/get-revisions":    false,
		"POST /api/v1/app/rollback-app":    false,
		"GET /api/v1/image/get-tags":       false,
		"POST /api/v1/app/control-app":     false,
		"POST /api/v1/app/delete-app":      false,
		"POST /api/v1/app/rename-app":      false,
		"GET /api/v1/domain/get-domains":   false,
		"GET /api/v1/domain/list":          false,
		"POST /api/v1/domain/create":       false,
		"POST /api/v1/domain/delete":       false,
		"GET /api/v1/domains/check":        false,
		"GET /api/v1/storage/get-storages": false,
		"GET /api/v1/storage/list":         false,
		"POST /api/v1/storage/test":        false,
		"POST /api/v1/storage/save":        false,
		"POST /api/v1/storage/delete":      false,
	}
```

**with:**

```go
	want := map[string]bool{
		"GET /api/v1/app/get-apps":              true,
		"GET /api/v1/app/get-apps-status":       true,
		"POST /api/v1/app/refresh-apps":         true,
		"POST /api/v1/app/save-app":             false,
		"GET /api/v1/app/get-app":               false,
		"GET /api/v1/app/get-logs":              false,
		"GET /api/v1/app/get-revisions":         false,
		"POST /api/v1/app/rollback-app":         false,
		"GET /api/v1/image/get-tags":            false,
		"POST /api/v1/app/control-app":          false,
		"POST /api/v1/app/delete-app":           false,
		"POST /api/v1/app/rename-app":           false,
		"POST /api/v1/app/update-backups":       false,
		"GET /api/v1/app/list-restore-points":   false,
		"POST /api/v1/app/run-backup":           false,
		"POST /api/v1/app/delete-restore-point": false,
		"GET /api/v1/domain/get-domains":        false,
		"GET /api/v1/domain/list":               false,
		"POST /api/v1/domain/create":            false,
		"POST /api/v1/domain/delete":            false,
		"GET /api/v1/domains/check":             false,
		"GET /api/v1/storage/get-storages":      false,
		"GET /api/v1/storage/list":              false,
		"POST /api/v1/storage/test":             false,
		"POST /api/v1/storage/save":             false,
		"POST /api/v1/storage/delete":           false,
	}
```

**Create `internal/app/web/contract/backups_test.go`:**

```go
package contract_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	happ "winterflow/internal/app/web/handler/app"
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
	"winterflow/internal/infra/transport/bus"
)

func TestBackupRoutes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	addStorage(t, owner, sid, model.Storage{Name: "disk", Type: model.StorageLocal, Local: &model.LocalStorage{Path: "/mnt/usb"}})

	t.Run("update-backups sends the whole list", func(t *testing.T) {
		var res command.UpdateBackupsResponse
		owner.succeed(t, post("/api/v1/app/update-backups", happ.UpdateBackupsRequest{ServerID: sid, UpdateBackupsRequest: command.UpdateBackupsRequest{
			AppID: anApp, Backups: []recipe.BackupConfig{{Name: "database", Enabled: true, Storage: "disk", Every: "15m"}, {Name: "media"}},
		}}), &res)
		if res.AppID != anApp || res.Revision != "rev-2" {
			t.Fatalf("result = %+v", res)
		}
		got := e.agent.seen(command.TypeBackupUpdate)
		in := decode[command.UpdateBackupsRequest](got[len(got)-1].Payload)
		if len(in.Backups) != 2 || in.Backups[0].Every != "15m" || !in.Backups[0].Enabled || in.Backups[1].Name != "media" {
			t.Fatalf("agent got %+v", in)
		}
	})
	t.Run("update-backups refuses before the agent", func(t *testing.T) {
		n := e.agent.count()
		for body, want := range map[string]string{
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backups":[{"name":"database","enabled":true,"storage":"nas"}]}`: `backup database: there is no storage \"nas\" on this server`,
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backups":[{"name":"database","enabled":true}]}`:                 "backups[0].storage: an enabled backup needs a storage",
			`{"server_id":"` + sid + `","app_id":"` + anApp + `","backups":[{"name":"Data_Base"}]}`:                               "is not a valid backup name",
			`{"app_id":"` + anApp + `","backups":[]}`:                                                                             "server_id is required",
			`{"server_id":"` + sid + `","app_id":"../x","backups":[]}`:                                                            "invalid app id",
		} {
			r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/update-backups", Body: body}).expect(t, http.StatusBadRequest)
			if !strings.Contains(string(r.Body), want) {
				t.Errorf("%s: body = %s, want %q", body, r.Body, want)
			}
		}
		if e.agent.count() != n {
			t.Fatal("a refused update reached the agent")
		}
	})
	t.Run("an agent refusal is the operation's error", func(t *testing.T) {
		e.agent.fail(command.TypeBackupUpdate, `backup media: /etc/app is not on a mount of service web`)
		owner.failWith(t, post("/api/v1/app/update-backups", map[string]any{"server_id": sid, "app_id": anApp, "backups": []any{}}),
			`backup media: /etc/app is not on a mount of service web`)
	})
	t.Run("list-restore-points", func(t *testing.T) {
		var res command.ListRestorePointsResponse
		owner.succeed(t, get("/api/v1/app/list-restore-points", url.Values{"server_id": {sid}, "app_id": {anApp}, "backup": {"database"}}), &res)
		if res.Backup != "database" || len(res.Points) != 1 || res.Points[0].Kind != command.PointScheduled {
			t.Fatalf("points = %+v", res)
		}
		for _, q := range []url.Values{
			{"server_id": {sid}, "app_id": {anApp}},
			{"server_id": {sid}, "app_id": {"a/b"}, "backup": {"database"}},
			{"app_id": {anApp}, "backup": {"database"}},
		} {
			owner.do(t, call{Method: http.MethodGet, Path: "/api/v1/app/list-restore-points", Query: q, Invalid: true}).expect(t, http.StatusBadRequest)
		}
	})
	t.Run("run-backup answers when the run ends", func(t *testing.T) {
		var res command.RunBackupResponse
		owner.succeed(t, post("/api/v1/app/run-backup", map[string]any{"server_id": sid, "app_id": anApp, "backup": "database"}), &res)
		if res.Unchanged || res.Point == nil || res.Point.Kind != command.PointManual {
			t.Fatalf("run = %+v", res)
		}
		e.agent.fail(command.TypeBackupRun, "database file not found: /src/0/app.db")
		owner.failWith(t, post("/api/v1/app/run-backup", map[string]any{"server_id": sid, "app_id": anApp, "backup": "database"}),
			"database file not found: /src/0/app.db")
	})
	t.Run("delete-restore-point", func(t *testing.T) {
		var res command.DeleteRestorePointResponse
		owner.succeed(t, post("/api/v1/app/delete-restore-point", map[string]any{
			"server_id": sid, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"}), &res)
		if res.Point != "20261011T120000Z-scheduled" {
			t.Fatalf("deleted = %+v", res)
		}
		r := owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/delete-restore-point", Invalid: true,
			Body: map[string]any{"server_id": sid, "app_id": anApp, "backup": "database", "point": "../../x"}}).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), "invalid restore point id") {
			t.Fatalf("body = %s", r.Body)
		}
	})
}

// Save-app checks the compose file's backups recipe and its WF_BACKUP_
// references when the compose file is in the request.
func TestSaveAppBackups(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	const compose = `services:
  web:
    image: example/web
    volumes: [data:/data]
  litestream:
    image: litestream/litestream
    profiles: [wf-backup-replica]
    environment:
      BUCKET: ${WF_BACKUP_REPLICA_BUCKET}
volumes:
  data:
x-winterflow:
  backups:
    - {name: database, type: sqlite, volume: data, path: app.db}
    - {name: replica, type: custom, service: litestream}
`
	save := func(compose, config string) *response {
		return owner.do(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
			ServerID: sid, App: model.App{Name: "tenant", Icon: "box", Color: "#112233"}, Config: json.RawMessage(config),
			Files: []happ.ContentItemRequest{{Name: "compose.yml", Content: compose}},
		}})
	}
	for name, tc := range map[string]struct{ compose, config, want string }{
		"invalid recipe":      {strings.Replace(compose, "volume: data", "volume: gone", 1), `{}`, `x-winterflow.backups[0].volume: \"gone\" is not a volume of this compose file`},
		"unknown key":         {strings.Replace(compose, "path: app.db", "path: app.db, enabled: true", 1), `{}`, "a recipe never turns a backup on"},
		"undeclared backup":   {compose + "# ${WF_BACKUP_AUDIT_PATH}\n", `{}`, `there is no backup \"audit\" in this app`},
		"enabled, no storage": {compose, `{"backups":[{"name":"replica","enabled":true,"storage":"nas"}]}`, `backup replica: there is no storage \"nas\" on this server`},
	} {
		r := save(tc.compose, tc.config).expect(t, http.StatusBadRequest)
		if !strings.Contains(string(r.Body), tc.want) {
			t.Errorf("%s: body = %s, want %q", name, r.Body, tc.want)
		}
	}
	if n := len(e.agent.seen(command.TypeAppSave)); n != 0 {
		t.Fatalf("%d refused save(s) reached the agent", n)
	}
	owner.succeed(t, call{Method: http.MethodPost, Path: "/api/v1/app/save-app", Body: happ.SaveAppRequest{
		ServerID: sid, App: model.App{Name: "tenant", Icon: "box", Color: "#112233"}, Config: json.RawMessage(`{}`),
		Files: []happ.ContentItemRequest{{Name: "compose.yml", Content: compose}},
	}}, nil)
}

// Backup health rides on the cached app status.
func TestAppsStatusCarriesBackupHealth(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	owner := e.claim(t)
	sid := owner.serverID(t)
	status, _ := json.Marshal(command.GetAppsStatusResponse{Apps: []command.AppStatus{{AppID: anApp, StatusCode: command.ContainerStatusActive,
		Containers: []command.ContainerStatus{}, Backups: []command.BackupHealth{{Name: "database", State: command.BackupLate, LastSuccess: 1791720000}}}}})
	e.publishEvent(t, bus.EventMessage{ServerID: sid, Kind: bus.EventAppsStatus, Payload: status})
	eventually(t, func() bool {
		var got command.GetAppsStatusResponse
		owner.get(t, "/api/v1/app/get-apps-status", sq(sid)).expect(t, http.StatusOK).data(t, &got)
		return len(got.Apps) == 1 && len(got.Apps[0].Backups) == 1 && got.Apps[0].Backups[0].State == command.BackupLate
	})
}
```

**In `internal/app/web/contract/fakeagent_test.go`, replace:**

```go
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/domain/command"
```

**with:**

```go
	appagent "winterflow/internal/app/agent"
	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
```

**In `internal/app/web/contract/fakeagent_test.go`, replace:**

```go
	case command.TypeAgentUpdate:
```

**with:**

```go
	case command.TypeBackupUpdate:
		return func(raw json.RawMessage) reply {
			in := decode[command.UpdateBackupsRequest](raw)
			return reply{Payload: command.UpdateBackupsResponse{AppID: in.AppID, Revision: "rev-2", Backups: []recipe.EffectiveBackup{}}}
		}
	case command.TypeBackupPoints:
		return func(raw json.RawMessage) reply {
			in := decode[command.ListRestorePointsRequest](raw)
			return reply{Payload: command.ListRestorePointsResponse{AppID: in.AppID, Backup: in.Backup, Points: []command.RestorePoint{
				{ID: "20261011T120000Z-scheduled", Time: 1791720000, Kind: command.PointScheduled, Size: 1024, Expires: 1807272000}}}}
		}
	case command.TypeBackupRun:
		return func(raw json.RawMessage) reply {
			in := decode[command.RunBackupRequest](raw)
			return reply{Payload: command.RunBackupResponse{AppID: in.AppID, Backup: in.Backup,
				Point: &command.RestorePoint{ID: "20261011T130000Z-manual", Time: 1791723600, Kind: command.PointManual, Size: 2048}}}
		}
	case command.TypeBackupDeletePoint:
		return func(raw json.RawMessage) reply {
			in := decode[command.DeleteRestorePointRequest](raw)
			return reply{Payload: command.DeleteRestorePointResponse{AppID: in.AppID, Backup: in.Backup, Point: in.Point}}
		}
	case command.TypeAgentUpdate:
```

**In `internal/app/web/contract/fakeagent_test.go`, replace:**

```go
		command.TypeStorageList, command.TypeStorageTest, command.TypeStorageSave, command.TypeStorageDelete,
	} {
```

**with:**

```go
		command.TypeStorageList, command.TypeStorageTest, command.TypeStorageSave, command.TypeStorageDelete,
		command.TypeBackupUpdate, command.TypeBackupPoints, command.TypeBackupRun, command.TypeBackupDeletePoint,
	} {
```

**In `internal/app/web/contract/matrix_test.go`, replace:**

```go
	"GET /api/v1/domain/get-domains": {valid: func(s string) call { return get("/api/v1/domain/get-domains", sq(s)) }, malformed: bad("GET", "/api/v1/domain/get-domains")},
```

**with:**

```go
	"POST /api/v1/app/update-backups": {valid: func(s string) call {
		return post("/api/v1/app/update-backups", map[string]any{"server_id": s, "app_id": anApp, "backups": []any{map[string]any{"name": "database"}}})
	}, malformed: bad("POST", "/api/v1/app/update-backups")},
	"GET /api/v1/app/list-restore-points": {valid: func(s string) call {
		return get("/api/v1/app/list-restore-points", url.Values{"server_id": {s}, "app_id": {anApp}, "backup": {"database"}})
	}, malformed: bad("GET", "/api/v1/app/list-restore-points")},
	"POST /api/v1/app/run-backup": {valid: func(s string) call {
		return post("/api/v1/app/run-backup", map[string]any{"server_id": s, "app_id": anApp, "backup": "database"})
	}, malformed: bad("POST", "/api/v1/app/run-backup")},
	"POST /api/v1/app/delete-restore-point": {valid: func(s string) call {
		return post("/api/v1/app/delete-restore-point", map[string]any{"server_id": s, "app_id": anApp, "backup": "database", "point": "20261011T120000Z-scheduled"})
	}, malformed: bad("POST", "/api/v1/app/delete-restore-point")},
	"GET /api/v1/domain/get-domains": {valid: func(s string) call { return get("/api/v1/domain/get-domains", sq(s)) }, malformed: bad("GET", "/api/v1/domain/get-domains")},
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/infra/transport/dispatch/ ./internal/domain/usecase/app/ ./internal/app/web/handler/app/`

Expected: build failure: `unknown field Wait in struct literal`, `uc.UpdateBackups undefined`, `h.UpdateBackups undefined`, ….

- [ ] **Step 3: Implement**

**In `internal/domain/port/command.go`, replace:**

```go
import (
	"context"
	"winterflow/internal/domain/command"
)
```

**with:**

```go
import (
	"context"
	"time"

	"winterflow/internal/domain/command"
)
```

**In `internal/domain/port/command.go`, replace:**

```go
	OnResult func(CommandResult)
}
```

**with:**

```go
	OnResult func(CommandResult)

	// Wait is how long the result is awaited (0: the dispatcher's default):
	// longer for a command whose result comes when long work ends
	// (backup.run).
	Wait time.Duration
}
```

**In `internal/infra/transport/dispatch/manager.go`, replace:**

```go
	m.remember(requestID, in.UserID, in.OnResult)
```

**with:**

```go
	ttl := pendingTTL
	if in.Wait > ttl {
		ttl = in.Wait
	}
	m.remember(requestID, in.UserID, in.OnResult, ttl)
```

**In `internal/infra/transport/dispatch/manager.go`, replace:**

```go
func (m *Manager) remember(requestID, userID string, onResult func(port.CommandResult)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictExpiredLocked()
	m.pending[requestID] = pending{userID: userID, expires: time.Now().Add(pendingTTL), onResult: onResult}
}
```

**with:**

```go
func (m *Manager) remember(requestID, userID string, onResult func(port.CommandResult), ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictExpiredLocked()
	m.pending[requestID] = pending{userID: userID, expires: time.Now().Add(ttl), onResult: onResult}
}
```

**Create `internal/domain/usecase/app/backups.go`:**

```go
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"winterflow/internal/domain/backup/recipe"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/port"
)

// backupRunWait is how long the API awaits a Back up now result: the run
// may queue behind others, and runs for up to 30 minutes.
const backupRunWait = 2 * time.Hour

// checkBackups validates the backups a save makes when its compose file is
// in the request: the recipe, the request's backups list when its config
// carries one (an enabled backup's storage must be on the server), and
// returns which backup names WF_BACKUP_ references may use. The app editor
// doesn't send the backups list (the agent keeps the stored one), so for an
// edit without it a name outside the recipe is left to the agent.
func (uc *UseCase) checkBackups(ctx context.Context, serverID string, contents [][]byte, config []byte, isNew bool) (func(string) bool, error) {
	comp, err := recipe.ParseCompose(contents...)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Backups *[]recipe.BackupConfig `json:"backups"`
	}
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("%w: backups: %v", recipe.ErrInvalid, err)
		}
	}
	var overrides []recipe.BackupConfig
	if cfg.Backups != nil {
		overrides = *cfg.Backups
	}
	backups, err := recipe.Effective(comp, overrides)
	if err != nil {
		return nil, err
	}
	if err := uc.requireBackupStorages(ctx, serverID, backups); err != nil {
		return nil, err
	}
	declared := recipe.Declared(backups)
	known := cfg.Backups != nil || isNew
	return func(name string) bool {
		_, ok := declared[name]
		return ok || !known
	}, nil
}

// requireBackupStorages refuses an enabled backup whose storage isn't in
// the server's storage index (without the index, the agent's check is the
// only one).
func (uc *UseCase) requireBackupStorages(ctx context.Context, serverID string, backups []recipe.EffectiveBackup) error {
	if uc.serverStorages == nil {
		return nil
	}
	var have map[string]bool
	for _, b := range backups {
		if !b.Enabled {
			continue
		}
		if have == nil {
			list, err := uc.serverStorages.ListForServer(ctx, serverID)
			if err != nil {
				uc.log.Error("backups: list storages", "error", err, "server_id", serverID)
				return err
			}
			have = map[string]bool{}
			for _, s := range list {
				have[s.Name] = true
			}
		}
		if !have[b.Storage] {
			return fmt.Errorf("%w: backup %s: there is no storage %q on this server", recipe.ErrInvalid, b.Name, b.Storage)
		}
	}
	return nil
}

// UpdateBackups dispatches backup.update (callers validate req first): the
// app's backups list replaced as a whole. An enabled backup's storage must
// be in the server's index (400); the agent checks the list against the
// compose file's recipe and the app's mounts.
func (uc *UseCase) UpdateBackups(ctx context.Context, userID, serverID string, req command.UpdateBackupsRequest) (string, error) {
	var enabled []recipe.EffectiveBackup
	for _, b := range req.Backups {
		if b.Enabled {
			enabled = append(enabled, recipe.EffectiveBackup{Name: b.Name, Enabled: true, Storage: b.Storage})
		}
	}
	if err := uc.requireBackupStorages(ctx, serverID, enabled); err != nil {
		return "", err
	}
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupUpdate, Payload: req,
	})
}

// ListRestorePoints dispatches backup.points.
func (uc *UseCase) ListRestorePoints(ctx context.Context, userID, serverID string, req command.ListRestorePointsRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupPoints, Payload: req,
	})
}

// RunBackup dispatches backup.run; its result arrives when the run ends.
func (uc *UseCase) RunBackup(ctx context.Context, userID, serverID string, req command.RunBackupRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupRun, Payload: req, Wait: backupRunWait,
	})
}

// DeleteRestorePoint dispatches backup.delete-point.
func (uc *UseCase) DeleteRestorePoint(ctx context.Context, userID, serverID string, req command.DeleteRestorePointRequest) (string, error) {
	return uc.dispatcher.Dispatch(ctx, port.DispatchInput{
		AgentID: serverID, UserID: userID, Type: command.TypeBackupDeletePoint, Payload: req,
	})
}
```

**In `internal/domain/usecase/app/storages.go`, replace:**

```go
// requireStorageRefs refuses a save whose compose file (in the request)
// references a WF_STORAGE_ variable of a storage the server doesn't have,
// or any WF_BACKUP_ variable. The compose files are picked the way the
// agent picks them (model.DetectComposeFiles). A git-sourced app's compose
// file is not in the request: the agent checks it at deploy. Without the
// index (old wiring) the agent's check is the only one.
func (uc *UseCase) requireStorageRefs(ctx context.Context, serverID string, p command.AppPayload) error {
	if p.Source != nil || uc.serverStorages == nil {
		return nil
	}
	files := map[string][]byte{}
	for _, f := range p.Files {
		if !f.Encrypted {
			files[f.ItemName()] = f.Content
		}
	}
	var contents [][]byte
	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok }) {
		contents = append(contents, files[name])
	}
	refs := model.ScanStorageRefs(contents...)
	if len(refs) == 0 {
		return nil
	}
	list, err := uc.serverStorages.ListForServer(ctx, serverID)
	if err != nil {
		uc.log.Error("SaveApp: list storages", "error", err, "server_id", serverID)
		return err
	}
	have := make(map[string]bool, len(list))
	for _, s := range list {
		have[s.Name] = true
	}
	_, err = model.CheckStorageRefs(refs, func(name string) bool { return have[name] }, nil)
	return err
}

```

**with:**

```go
// requireStorageRefs refuses a save whose compose file (in the request)
// declares invalid backups, or references a WF_STORAGE_ variable of a
// storage the server doesn't have or a WF_BACKUP_ variable of a backup the
// app doesn't declare. The compose files are picked the way the agent picks
// them (model.DetectComposeFiles). A git-sourced app's compose file is not
// in the request: the agent checks it at save and deploy. Without the
// storage index (old wiring) the agent's storage check is the only one.
func (uc *UseCase) requireStorageRefs(ctx context.Context, serverID string, p command.AppPayload, isNew bool) error {
	if p.Source != nil {
		return nil
	}
	files := map[string][]byte{}
	for _, f := range p.Files {
		if !f.Encrypted {
			files[f.ItemName()] = f.Content
		}
	}
	var contents [][]byte
	for _, name := range model.DetectComposeFiles(func(n string) bool { _, ok := files[n]; return ok }) {
		contents = append(contents, files[name])
	}
	backupDeclared, err := uc.checkBackups(ctx, serverID, contents, p.Config, isNew)
	if err != nil {
		return err
	}
	refs := model.ScanStorageRefs(contents...)
	if len(refs) == 0 {
		return nil
	}
	have := map[string]bool{}
	if uc.serverStorages != nil {
		list, err := uc.serverStorages.ListForServer(ctx, serverID)
		if err != nil {
			uc.log.Error("SaveApp: list storages", "error", err, "server_id", serverID)
			return err
		}
		for _, s := range list {
			have[s.Name] = true
		}
	}
	_, err = model.CheckStorageRefs(refs, func(name string) bool { return uc.serverStorages == nil || have[name] }, backupDeclared)
	return err
}

```

**In `internal/domain/usecase/app/usecase.go`, replace:**

```go
	if app.ID == "" {
		app.ID = util.GenerateID()
	}
```

**with:**

```go
	isNew := app.ID == ""
	if isNew {
		app.ID = util.GenerateID()
	}
```

**In `internal/domain/usecase/app/usecase.go`, replace:**

```go
	if err := uc.requireStorageRefs(ctx, serverID, payload); err != nil {
```

**with:**

```go
	if err := uc.requireStorageRefs(ctx, serverID, payload, isNew); err != nil {
```

**Create `internal/app/web/handler/app/backups.go`:**

```go
package server

import (
	"context"
	"errors"
	"net/http"

	webutil "winterflow/internal/app/web/util"
	"winterflow/internal/domain/command"
	"winterflow/internal/domain/model"
)

// UpdateBackupsRequest is the body of POST /api/v1/app/update-backups: the
// app's backups list, replaced as a whole.
type UpdateBackupsRequest struct {
	ServerID string `json:"server_id"`
	command.UpdateBackupsRequest
}

// RunBackupRequest is the body of POST /api/v1/app/run-backup.
type RunBackupRequest struct {
	ServerID string `json:"server_id"`
	command.RunBackupRequest
}

// DeleteRestorePointRequest is the body of POST
// /api/v1/app/delete-restore-point.
type DeleteRestorePointRequest struct {
	ServerID string `json:"server_id"`
	command.DeleteRestorePointRequest
}

// RestorePointsQuery is the query of GET /api/v1/app/list-restore-points.
type RestorePointsQuery struct {
	ServerID string `query:"server_id" required:"true"`
	AppID    string `query:"app_id" required:"true"`
	Backup   string `query:"backup" required:"true" description:"Backup name."`
}

func (r UpdateBackupsRequest) server() string      { return r.ServerID }
func (r RunBackupRequest) server() string          { return r.ServerID }
func (r DeleteRestorePointRequest) server() string { return r.ServerID }

// serverBody is a JSON body naming its server: server_id plus the agent
// request.
type serverBody interface {
	Validate() error
	server() string
}

// dispatchServerBody serves an app-scoped command whose body carries
// server_id: it decodes and validates the body (400 with the reason),
// authorizes the caller for the server (401/403), dispatches, and answers
// 202 with the request id. A rejection by the usecase (model.ErrRejected)
// is a 400 with its message.
func dispatchServerBody[T serverBody](h *Handler, w http.ResponseWriter, r *http.Request, what string,
	dispatch func(ctx context.Context, userID, serverID string, req T) (string, error)) {
	userID, ok := webutil.RequireUser(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[T](w, r)
	if !ok {
		return
	}
	if req.server() == "" {
		webutil.Error(w, "server_id is required", nil)
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	if !webutil.RequireServerAccess(w, r, h.servers, userID, req.server()) {
		return
	}
	reqID, err := dispatch(r.Context(), userID, req.server(), req)
	if err != nil {
		if errors.Is(err, model.ErrRejected) {
			webutil.Error(w, err.Error(), nil)
			return
		}
		webutil.Error(w, "failed to "+what, nil)
		return
	}
	accepted(w, what+" dispatched", reqID)
}

// UpdateBackups replaces an app's backups list; the agent checks it against
// the compose file, commits it and redeploys only when the compose profiles
// or storage variables change.
func (h *Handler) UpdateBackups(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "update backups", func(ctx context.Context, userID, serverID string, req UpdateBackupsRequest) (string, error) {
		return h.usecase.UpdateBackups(ctx, userID, serverID, req.UpdateBackupsRequest)
	})
}

// RunBackup runs a backup now; the outcome arrives over SSE when the run
// ends.
func (h *Handler) RunBackup(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "run backup", func(ctx context.Context, userID, serverID string, req RunBackupRequest) (string, error) {
		return h.usecase.RunBackup(ctx, userID, serverID, req.RunBackupRequest)
	})
}

// DeleteRestorePoint deletes one restore point.
func (h *Handler) DeleteRestorePoint(w http.ResponseWriter, r *http.Request) {
	dispatchServerBody(h, w, r, "delete restore point", func(ctx context.Context, userID, serverID string, req DeleteRestorePointRequest) (string, error) {
		return h.usecase.DeleteRestorePoint(ctx, userID, serverID, req.DeleteRestorePointRequest)
	})
}

// ListRestorePoints lists a backup's restore points; they arrive over SSE.
func (h *Handler) ListRestorePoints(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := command.ListRestorePointsRequest{AppID: q.Get("app_id"), Backup: q.Get("backup")}
	userID, serverID, ok := h.serverFromQuery(w, r)
	if !ok {
		return
	}
	if err := req.Validate(); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.usecase.ListRestorePoints(r.Context(), userID, serverID, req)
	if err != nil {
		webutil.Error(w, "failed to list restore points", nil)
		return
	}
	accepted(w, "list restore points dispatched", reqID)
}
```

**In `internal/app/web/handler/app/routes.go`, replace:**

```go
		{
			Method: http.MethodGet, Path: "/api/v1/domain/get-domains", Handler: h.GetDomains, Auth: api.User,
```

**with:**

```go
		{
			Method: http.MethodPost, Path: "/api/v1/app/update-backups", Handler: h.UpdateBackups, Auth: api.User,
			Summary: "Replace an app's backups list (new revision; redeploys only when profiles or storage variables change)", Tag: "backups",
			Description: "`backups` is the whole list of the app's backup settings: entries named like a backup of the compose file's " +
				"`x-winterflow.backups` override it (only the fields that differ, never `type`); other entries are backups added here " +
				"and carry `type` and every field it needs. `enabled: true` needs `storage`, a storage on the server.",
			Request: UpdateBackupsRequest{}, Accepted: true, Result: command.UpdateBackupsResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/app/list-restore-points", Handler: h.ListRestorePoints, Auth: api.User,
			Summary: "List a backup's restore points, newest first (30s deadline)", Tag: "backups",
			Query: RestorePointsQuery{}, Accepted: true, Result: command.ListRestorePointsResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/run-backup", Handler: h.RunBackup, Auth: api.User,
			Summary: "Back up now (joins a run of the same backup already running or queued)", Tag: "backups",
			Description: "The result arrives when the run ends, which can take up to 30 minutes after it starts.",
			Request:     RunBackupRequest{}, Accepted: true, Result: command.RunBackupResponse{}, Errors: server,
		},
		{
			Method: http.MethodPost, Path: "/api/v1/app/delete-restore-point", Handler: h.DeleteRestorePoint, Auth: api.User,
			Summary: "Delete one restore point (any kind)", Tag: "backups",
			Request: DeleteRestorePointRequest{}, Accepted: true, Result: command.DeleteRestorePointResponse{}, Errors: server,
		},
		{
			Method: http.MethodGet, Path: "/api/v1/domain/get-domains", Handler: h.GetDomains, Auth: api.User,
```

- [ ] **Step 4: Regenerate the OpenAPI document and the routes golden**

```bash
make openapi
go test ./internal/app/web/ -run TestRoutesGolden -update
```

Expected: `api/openapi.yaml` gains the four routes under the `backups` tag (bodies `UpdateBackupsRequest`, `RunBackupRequest`, `DeleteRestorePointRequest` with `server_id` flattened next to the command fields; results `UpdateBackupsResponse`, `ListRestorePointsResponse`, `RunBackupResponse`, `DeleteRestorePointResponse`), and `routes.golden` gains four lines.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/infra/transport/dispatch/ ./internal/domain/... ./internal/app/web/... && WF_CONTRACT_GUARD=require go test -count=1 ./internal/app/web/contract/ && make test-pg`

Expected: PASS on SQLite and Postgres (`make test-pg` needs Docker). C2a adds no table, so the parity golden is unchanged.

- [ ] **Step 6: Commit**

```bash
gofmt -l internal cmd pkg   # prints nothing
git add internal/infra/transport/dispatch/manager_test.go internal/domain/usecase/app/backups_test.go internal/app/web/handler/app/backups_test.go internal/app/web/handler/app/routes_test.go internal/app/web/contract/backups_test.go internal/app/web/contract/fakeagent_test.go internal/app/web/contract/matrix_test.go internal/domain/port/command.go internal/infra/transport/dispatch/manager.go internal/domain/usecase/app/backups.go internal/domain/usecase/app/storages.go internal/domain/usecase/app/usecase.go internal/app/web/handler/app/backups.go internal/app/web/handler/app/routes.go api/openapi.yaml internal/app/web/testdata/routes.golden
git commit -F - <<'EOF'
feat(api): backup routes and save-app backup checks, with contract tests

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
EOF
```


---

## Seams left for C2b

- **Restore** reuses `layout` (`Points`, `Manifest`, `DataKey`/`ManifestKey`, `Expires`), `openBackupStorage`, `resolveBackups`/`helperPlan` and `helperArgs`'s mount specs (restore mounts read-write), `RunBackup` with `Kind: command.PointPreRestore` (which already skips the unchanged check), the `helper` exit codes, and `cli.Main` (a `restore` subcommand slots in beside `dump`; today it answers usage). A restore job runs on the scheduler's goroutine — which is what makes "a per-app lock shared with restore and delete" hold: everything that touches an app's backups runs there, and `Forget` stops it. `command.Type.Detached` and `port.DispatchInput.Wait` serve `backup.restore` the same way as `backup.run`.
- **Delete with `delete_backups`, `.deleted` markers, the daily sweep, orphans** read the same layout (`Folder`, `Points`) and the storage registry; the sweep is one more timer case in `Scheduler.Run` (`idleWait` today).
- **UI**: `app.get` already carries `backups` (`EffectiveBackup` with `FromRecipe`/`InRecipe`) and `backups_error`; `apps.status` carries health; restore points carry `expires`; the four routes exist. New routes follow `dispatchServerBody`, `fakeAgent.defaultScript` and `probes`.

## Self-review

- Spec coverage (C2a scope): recipe and validation → Task 2; addressing and its errors → Tasks 2, 7; overrides and `FromRecipe` → Tasks 2, 6; `WF_BACKUP_` and profiles → Tasks 3, 6; scheduler (one goroutine, one timer, offsets, manual never scheduled, one at a time, reload hooks, `state.json`, leftover helper cleanup) → Tasks 8–10; one run (helper, unchanged/missing, `VACUUM INTO` + `quick_check`, tar, zstd + sha256, layout + manifest, prune tiers + ceiling + refresh, deadlines) → Tasks 4, 5, 8; health → Tasks 3, 9, 10; restore points, delete-point, Back up now → Tasks 8–11; API routes → Task 11; `BACKUP_RETENTION_DAYS` → Task 4; mount-point detection → Task 1.
- Verified: `go test -p 2 ./...`, `make test` (race, coverage), `WF_CONTRACT_GUARD=require go test ./internal/app/web/contract/...`, `make test-pg`, `make openapi` (up to date), `gofmt`, `go vet ./...`, the Docker integration test against Docker 29.8.1 / Compose v5.5.1, and diff coverage (Go ≥ 80% of changed lines; no web changes). No query or migration changed, so `make sqlc-check` is unaffected.
