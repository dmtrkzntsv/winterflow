# Volume registry and delete-with-volumes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `app.delete` optionally remove an app's Docker volumes, and add a per-server volume inventory (list/create/delete) mirroring registries and networks.

**Architecture:** New agent-bound commands `volume.list|create|delete` ride the existing JSON envelope: command structs in `internal/domain/command`, one dispatcher line each, orchestrator methods in `docker_compose/docker.go`, HTTP handlers in `handler/docker`, routes in `routes.go`. `DeleteAppRequest` gains `delete_volumes`; the orchestrator runs `compose down -v` plus a project-label sweep. The web UI gets a Volumes tab and a checkbox in the delete-app dialog.

**Tech Stack:** Go 1.25 (chi router, go-pkgz/auth), docker CLI via `os/exec` (always `[]string` args), React 19 + Vite + vendored shadcn/ui, pnpm.

**Spec:** `docs/superpowers/specs/2026-10-10-volume-registry-design.md`

## Global Constraints

- Docker/compose CLI args are always `[]string`; never shell strings. App ids go through `filepath.Base` (`projectName` already does).
- Every server-addressed route verifies org ownership (`caller` → `webutil.RequireServerAccess`); tests assert 403 for a foreign server.
- Create/delete volume routes are admin-only via `adminMW` in `routes.go`; list is open to any authenticated member with server access.
- Volume listing runs only on request (never on a poll) and costs exactly two execs: `docker volume ls --format json` and `docker ps --all --no-trunc --format json` (unfiltered).
- No `docker system df` (too slow). No volume sizes.
- Volume name rule: `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`, length 1–255, checked by `command.ValidateVolumeName` in both the HTTP handler (400) and the agent.
- `delete_volumes` defaults to false: old clients keep today's behaviour exactly.
- With `delete_volumes`, failures to remove containers/volumes are warnings (logged at Warn, returned in `DeleteAppResponse.Warnings`), never a delete failure. Failing to remove the app dir is still an error.
- External (`external: true`) volumes are never removed — they carry no compose project label, and the sweep filters by `label=com.docker.compose.project=wf-<appID>`.
- `docker volume rm` is never called with `--force`.
- Never delete anything under `web/src/components/ui/` (vendored shadcn).
- Commit messages: conventional (`feat(agent): …`, `feat(web): …`), ending with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. A volume named like a flag (`-rf`, `--all`) or with a path (`../x`) reaching `docker volume create/rm` — must be rejected before exec, at both handler and agent (Task 1 test + Task 4 test).
2. `delete_volumes=true` on an app whose compose file is broken/missing — containers must be force-removed and labelled volumes still swept, delete still succeeds with warnings (Task 3 test of `appDeleteSweepPlan` + integration test).
3. A volume mounted only by a non-compose container (e.g. `docker run -v`) must show `in_use=true` — hence the unfiltered `docker ps` (Task 2 test).
4. A `wf-` labelled volume whose app dir is gone must be `orphan=true`; one whose app exists must not; non-winterflow compose projects (`twillingate-cloud`) must have `project` set and empty `app_id`, never orphan (Task 2 test).
5. Deleting an in-use volume from the UI must surface Docker's "volume is in use" reason in the toast, not a generic error (Task 2 returns the stderr; Task 5 shows `e.message`).

---

## File map

| File | Change |
|---|---|
| `internal/domain/command/docker.go` | + `Volume`, List/Create/Delete Volume Req/Resp, `ValidateVolumeName` |
| `internal/domain/command/app.go` | `DeleteAppRequest.DeleteVolumes`, `DeleteAppResponse.Warnings` |
| `internal/domain/command/command.go` | + `TypeVolumeList/Create/Delete` |
| `internal/domain/command/volume_test.go` | new: validator tests |
| `internal/infra/orchestrator/docker_compose/volumes.go` | new: parse helpers, `ListVolumes`, `CreateVolume`, `DeleteVolume`, delete sweep |
| `internal/infra/orchestrator/docker_compose/volumes_test.go` | new |
| `internal/infra/orchestrator/docker_compose/lifecycle.go` | `DeleteApp(ctx, appID, deleteVolumes) ([]string, error)` |
| `internal/infra/orchestrator/docker_compose/operations.go` | `composeDown(ctx, appID, volumes bool)` |
| `internal/infra/orchestrator/docker_compose/*_test.go` | update `DeleteApp` call sites |
| `internal/infra/orchestrator/docker_compose/volumes_integration_test.go` | new, `//go:build integration` |
| `internal/app/agent/dispatcher.go` | delete passes flag + warnings; 3 volume lines |
| `internal/domain/usecase/app/usecase.go` | `DeleteApp(..., deleteVolumes bool)` |
| `internal/app/web/handler/app/lifecycle.go` | `delete_volumes` in request |
| `internal/app/web/handler/docker/handler.go` | `ListVolumes`, `CreateVolume`, `DeleteVolume` |
| `internal/app/web/routes.go` | 3 routes |
| tests in `handler/app`, `handler/docker`, `usecase/app` | updated/new |
| `web/src/context/use-docker.ts` | `Volume` type + 3 calls |
| `web/src/context/apps-context.tsx` (+ its base types file) | `remove(appId, opts)` returns warnings |
| `web/src/pages/settings.tsx` | Volumes tab + `VolumesCard` |
| `web/src/pages/app-details.tsx` | delete copy + checkbox + warnings toast |

---

### Task 1: Command contract

**Files:**
- Modify: `internal/domain/command/docker.go` (append after the networks section)
- Modify: `internal/domain/command/app.go:180-186`
- Modify: `internal/domain/command/command.go` (type constants next to `TypeNetwork*`)
- Create: `internal/domain/command/volume_test.go`

**Interfaces:**
- Produces: `command.Volume`, `command.ListVolumesRequest/Response`, `command.CreateVolumeRequest/Response`, `command.DeleteVolumeRequest/Response`, `command.ValidateVolumeName(name string) error`, `command.TypeVolumeList/TypeVolumeCreate/TypeVolumeDelete`, `DeleteAppRequest.DeleteVolumes bool`, `DeleteAppResponse.Warnings []string`.

- [ ] **Step 1: Write the failing test** — `internal/domain/command/volume_test.go`:

```go
package command

import (
	"strings"
	"testing"
)

func TestValidateVolumeName(t *testing.T) {
	ok := []string{"data", "a", "app_data-1.v2", "0abc", strings.Repeat("a", 255)}
	for _, n := range ok {
		if err := ValidateVolumeName(n); err != nil {
			t.Errorf("%q should be valid: %v", n, err)
		}
	}
	bad := []string{"", "-rf", "--all", "_x", ".x", "../x", "a/b", "a b", "a;b", "$(x)", strings.Repeat("a", 256)}
	for _, n := range bad {
		if err := ValidateVolumeName(n); err == nil {
			t.Errorf("%q should be rejected", n)
		}
	}
}
```

- [ ] **Step 2: Run it** — `go test ./internal/domain/command/ -run TestValidateVolumeName` → FAIL (undefined: ValidateVolumeName).

- [ ] **Step 3: Implement.** Append to `docker.go` (add `"errors"`, `"regexp"` imports):

```go
// --- volumes ------------------------------------------------------------------

// Volume is a Docker volume as seen by the agent. AppID is derived from the
// compose project label wf-<appID>; Orphan means that label names an app that
// no longer exists on the agent.
type Volume struct {
	Name      string `json:"name"`
	Driver    string `json:"driver,omitempty"`
	AppID     string `json:"app_id,omitempty"`
	Project   string `json:"project,omitempty"`
	InUse     bool   `json:"in_use"`
	Orphan    bool   `json:"orphan"`
	Anonymous bool   `json:"anonymous"`
}

type ListVolumesRequest struct{}

type ListVolumesResponse struct {
	Volumes []Volume `json:"volumes"`
}

type CreateVolumeRequest struct {
	Name string `json:"name"`
}

type CreateVolumeResponse struct {
	Name string `json:"name"`
}

type DeleteVolumeRequest struct {
	Name string `json:"name"`
}

type DeleteVolumeResponse struct {
	Name string `json:"name"`
}

var volumeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidateVolumeName applies Docker's volume-name rule. Because the first
// character must be alphanumeric, a name can never be parsed as a CLI flag.
func ValidateVolumeName(name string) error {
	if name == "" {
		return errors.New("volume name is required")
	}
	if len(name) > 255 {
		return errors.New("volume name is too long (max 255)")
	}
	if !volumeNameRe.MatchString(name) {
		return errors.New("volume name must start with a letter or digit and contain only letters, digits, '_', '.' or '-'")
	}
	return nil
}
```

In `app.go` replace the delete structs:

```go
type DeleteAppRequest struct {
	AppID string `json:"app_id"`
	// DeleteVolumes also removes the app's compose-owned Docker volumes
	// (never external ones). False keeps them, as before.
	DeleteVolumes bool `json:"delete_volumes,omitempty"`
}

type DeleteAppResponse struct {
	AppID string `json:"app_id"`
	// Warnings lists cleanup steps that failed after the app was removed
	// (e.g. a volume still in use).
	Warnings []string `json:"warnings,omitempty"`
}
```

In `command.go`, next to the network constants (match their exact style):

```go
	TypeVolumeList   Type = "volume.list"
	TypeVolumeCreate Type = "volume.create"
	TypeVolumeDelete Type = "volume.delete"
```

- [ ] **Step 4: Run** `go test ./internal/domain/command/ && go build ./...` → PASS.
- [ ] **Step 5: Commit** — `feat(command): volume commands and delete_volumes flag`.

---

### Task 2: Orchestrator volume registry

**Files:**
- Create: `internal/infra/orchestrator/docker_compose/volumes.go`
- Create: `internal/infra/orchestrator/docker_compose/volumes_test.go`

**Interfaces:**
- Consumes: Task 1 types, `command.ValidateVolumeName`; existing `projectName(appID)`, `r.appDataDir(appID)`, `r.log`.
- Produces: `func (r *Repository) ListVolumes(ctx) ([]command.Volume, error)`, `CreateVolume(ctx, name string) error`, `DeleteVolume(ctx, name string) error`; pure helpers `parseVolumeLS([]byte, *logger.Logger) []command.Volume`, `parseMountedVolumes([]byte) map[string]bool`, `parseLabels(string) map[string]string`, `annotateVolumes(vols []command.Volume, mounted map[string]bool, appExists func(string) bool)`, `sortVolumes([]command.Volume)`; constant `wfProjectPrefix = "wf-"` (reuse if `projectName` already exposes one).

Docker output facts (captured on Docker 29.8.1):
- `docker volume ls --format json` → one JSON object per line: `{"Driver":"local","Labels":"com.docker.compose.project=wf-01a…,com.docker.compose.volume=data,…","Name":"analytics_data_01a…",…}`; anonymous volumes have label `com.docker.volume.anonymous=`. `Labels` is a single `k=v,k=v` string (may be empty).
- `docker ps --all --no-trunc --format json` → one object per line with `"Mounts":"twillingate-cloud_pgdata,/host/path"` (comma-separated volume names and bind sources; may be empty).

- [ ] **Step 1: Write failing tests** — `volumes_test.go`:

```go
package dockercompose

import (
	"os"
	"testing"

	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

func testLog() *logger.Logger {
	return logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"})
}

const volumeLSOut = `{"Driver":"local","Labels":"com.docker.compose.project=wf-app-1,com.docker.compose.volume=data","Name":"wf-app-1_data"}
{"Driver":"local","Labels":"com.docker.compose.project=wf-gone,com.docker.compose.volume=db","Name":"wf-gone_db"}
{"Driver":"local","Labels":"com.docker.compose.project=twillingate-cloud,com.docker.compose.volume=pgdata","Name":"twillingate-cloud_pgdata"}
{"Driver":"local","Labels":"","Name":"plain"}
{"Driver":"local","Labels":"com.docker.volume.anonymous=","Name":"0f9c6dc6d5ad"}
not json
`

const psOut = `{"Names":"a","Mounts":"wf-app-1_data,/srv/host"}
{"Names":"b","Mounts":"plain"}
{"Names":"c","Mounts":""}
`

func TestParseVolumeLS(t *testing.T) {
	vols := parseVolumeLS([]byte(volumeLSOut), testLog())
	if len(vols) != 5 {
		t.Fatalf("want 5 volumes (malformed line skipped), got %d", len(vols))
	}
	by := map[string]command.Volume{}
	for _, v := range vols {
		by[v.Name] = v
	}
	if v := by["wf-app-1_data"]; v.AppID != "app-1" || v.Project != "wf-app-1" || v.Driver != "local" {
		t.Errorf("wf volume: %+v", v)
	}
	if v := by["twillingate-cloud_pgdata"]; v.AppID != "" || v.Project != "twillingate-cloud" {
		t.Errorf("foreign compose volume: %+v", v)
	}
	if v := by["0f9c6dc6d5ad"]; !v.Anonymous {
		t.Errorf("anonymous: %+v", v)
	}
	if v := by["plain"]; v.AppID != "" || v.Project != "" || v.Anonymous {
		t.Errorf("plain: %+v", v)
	}
}

func TestParseMountedVolumes(t *testing.T) {
	m := parseMountedVolumes([]byte(psOut))
	if !m["wf-app-1_data"] || !m["plain"] {
		t.Fatalf("mounted = %v", m)
	}
	if m[""] {
		t.Fatal("empty Mounts must not produce an empty name")
	}
}

func TestAnnotateVolumes(t *testing.T) {
	vols := parseVolumeLS([]byte(volumeLSOut), testLog())
	annotateVolumes(vols, parseMountedVolumes([]byte(psOut)), func(id string) bool { return id == "app-1" })
	by := map[string]command.Volume{}
	for _, v := range vols {
		by[v.Name] = v
	}
	if v := by["wf-app-1_data"]; !v.InUse || v.Orphan {
		t.Errorf("app-1 data: %+v", v)
	}
	if v := by["wf-gone_db"]; v.InUse || !v.Orphan {
		t.Errorf("gone db must be orphan: %+v", v)
	}
	if v := by["twillingate-cloud_pgdata"]; v.Orphan {
		t.Errorf("non-winterflow project is never orphan: %+v", v)
	}
	if v := by["plain"]; !v.InUse {
		t.Errorf("plain is mounted by a non-compose container: %+v", v)
	}
}

func TestSortVolumes(t *testing.T) {
	vols := []command.Volume{
		{Name: "z-anon", Anonymous: true},
		{Name: "b-plain"},
		{Name: "x", AppID: "b"},
		{Name: "a-plain"},
		{Name: "y", AppID: "a"},
	}
	sortVolumes(vols)
	got := []string{}
	for _, v := range vols {
		got = append(got, v.Name)
	}
	want := []string{"y", "x", "a-plain", "b-plain", "z-anon"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestCreateDeleteVolumeRejectBadNames(t *testing.T) {
	r := newTestRepo(t)
	for _, n := range []string{"", "-rf", "../x"} {
		if err := r.CreateVolume(t.Context(), n); err == nil {
			t.Errorf("create %q must fail validation", n)
		}
		if err := r.DeleteVolume(t.Context(), n); err == nil {
			t.Errorf("delete %q must fail validation", n)
		}
	}
}

func TestAppExistsUsesAppDir(t *testing.T) {
	r := newTestRepo(t)
	if err := os.MkdirAll(r.appDataDir("app-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !r.appExists("app-1") || r.appExists("nope") {
		t.Fatal("appExists must reflect apps-data dirs")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/infra/orchestrator/docker_compose/ -run 'Volume|Mounted|AppExists'` → FAIL (undefined symbols).

- [ ] **Step 3: Implement** `volumes.go`:

```go
package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"winterflow/internal/domain/command"
	"winterflow/pkg/logger"
)

const (
	composeProjectKey = "com.docker.compose.project"
	anonymousVolKey   = "com.docker.volume.anonymous"
	wfProjectPrefix   = "wf-"
)

type dockerVolume struct {
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Labels string `json:"Labels"`
}

// parseLabels splits docker's "k=v,k=v" label string.
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

func parseVolumeLS(out []byte, log *logger.Logger) []command.Volume {
	vols := make([]command.Volume, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var dv dockerVolume
		if err := json.Unmarshal([]byte(line), &dv); err != nil {
			log.Warn("parse volume line", "error", err)
			continue
		}
		labels := parseLabels(dv.Labels)
		v := command.Volume{Name: dv.Name, Driver: dv.Driver, Project: labels[composeProjectKey]}
		if id, ok := strings.CutPrefix(v.Project, wfProjectPrefix); ok && id != "" {
			v.AppID = id
		}
		_, v.Anonymous = labels[anonymousVolKey]
		vols = append(vols, v)
	}
	return vols
}

// parseMountedVolumes returns the set of mount sources named in `docker ps`
// output. Bind sources (host paths) land in the set too; they never collide
// with volume names, which cannot contain '/'.
func parseMountedVolumes(out []byte) map[string]bool {
	mounted := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var c struct {
			Mounts string `json:"Mounts"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &c) != nil {
			continue
		}
		for _, m := range strings.Split(c.Mounts, ",") {
			if m = strings.TrimSpace(m); m != "" {
				mounted[m] = true
			}
		}
	}
	return mounted
}

func annotateVolumes(vols []command.Volume, mounted map[string]bool, appExists func(string) bool) {
	for i := range vols {
		vols[i].InUse = mounted[vols[i].Name]
		vols[i].Orphan = vols[i].AppID != "" && !appExists(vols[i].AppID)
	}
}

// sortVolumes: winterflow-owned (by app id, then name), other named, anonymous.
func sortVolumes(vols []command.Volume) {
	rank := func(v command.Volume) int {
		switch {
		case v.AppID != "":
			return 0
		case v.Anonymous:
			return 2
		default:
			return 1
		}
	}
	sort.SliceStable(vols, func(i, j int) bool {
		a, b := vols[i], vols[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if a.AppID != b.AppID {
			return a.AppID < b.AppID
		}
		return a.Name < b.Name
	})
}

func (r *Repository) appExists(appID string) bool {
	_, err := os.Stat(r.appDataDir(appID))
	return err == nil
}

// ListVolumes returns every Docker volume with its owning app, whether any
// container (running or stopped) mounts it, and whether it is orphaned.
// Exactly two execs; called only on request, never polled.
func (r *Repository) ListVolumes(ctx context.Context) ([]command.Volume, error) {
	lsOut, err := exec.CommandContext(ctx, "docker", "volume", "ls", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker volume ls: %w", err)
	}
	psOut, err := exec.CommandContext(ctx, "docker", "ps", "--all", "--no-trunc", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	vols := parseVolumeLS(lsOut, r.log)
	annotateVolumes(vols, parseMountedVolumes(psOut), r.appExists)
	sortVolumes(vols)
	return vols, nil
}

// CreateVolume creates a local Docker volume.
func (r *Repository) CreateVolume(ctx context.Context, name string) error {
	if err := command.ValidateVolumeName(name); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "docker", "volume", "create", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker volume create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteVolume removes a Docker volume. No --force: Docker refuses a volume
// in use and that reason is returned to the caller.
func (r *Repository) DeleteVolume(ctx context.Context, name string) error {
	if err := command.ValidateVolumeName(name); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "docker", "volume", "rm", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker volume rm: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
```

If `logger.Logger` has no `Warn` with that signature, match what `ListNetworks` uses (`r.log.Warn("…", "error", err)`). If `t.Context()` is unavailable, use `context.Background()`.

- [ ] **Step 4: Run** `go test ./internal/infra/orchestrator/docker_compose/ -run 'Volume|Mounted|AppExists' && go vet ./internal/infra/orchestrator/...` → PASS.
- [ ] **Step 5: Commit** — `feat(agent): list, create and delete docker volumes`.

---

### Task 3: Delete an app with its volumes (agent) + dispatcher wiring

**Files:**
- Modify: `internal/infra/orchestrator/docker_compose/operations.go` (`composeDown`)
- Modify: `internal/infra/orchestrator/docker_compose/lifecycle.go:76-92` (`DeleteApp`)
- Modify: `internal/infra/orchestrator/docker_compose/volumes.go` (sweep helpers)
- Modify: every `DeleteApp(` call site in `docker_compose/*_test.go` (incl. `deploy_integration_test.go:51`, `repository_test.go:277`) → `_, err := r.DeleteApp(ctx, id, false)` form
- Modify: `internal/app/agent/dispatcher.go:127-133` and add 3 volume lines after the network block (~:184)
- Create: `internal/infra/orchestrator/docker_compose/volumes_integration_test.go`
- Test: `volumes_test.go` (add)

**Interfaces:**
- Consumes: Task 1 (`DeleteAppRequest.DeleteVolumes`, `DeleteAppResponse.Warnings`, volume types/constants), Task 2 (`ListVolumes`, `CreateVolume`, `DeleteVolume`, `composeProjectKey`).
- Produces: `func (r *Repository) DeleteApp(ctx context.Context, appID string, deleteVolumes bool) ([]string, error)`; `func composeDownVerb(volumes bool) []string`; `func projectLabelFilter(appID string) string`.

- [ ] **Step 1: Write failing tests** (append to `volumes_test.go`):

```go
func TestComposeDownVerb(t *testing.T) {
	if got := strings.Join(composeDownVerb(false), " "); got != "down --remove-orphans" {
		t.Fatalf("keep volumes: %q", got)
	}
	if got := strings.Join(composeDownVerb(true), " "); got != "down -v --remove-orphans" {
		t.Fatalf("delete volumes: %q", got)
	}
}

func TestProjectLabelFilter(t *testing.T) {
	if got := projectLabelFilter("app-1"); got != "label=com.docker.compose.project=wf-app-1" {
		t.Fatalf("filter = %q", got)
	}
	// app ids are path-cleaned like projectName
	if got := projectLabelFilter("../evil"); got != "label=com.docker.compose.project=wf-evil" {
		t.Fatalf("filter must use filepath.Base: %q", got)
	}
}

// Without Docker the sweep commands fail; delete must still succeed, remove
// the dir, and report what it could not clean up as warnings, not errors.
func TestDeleteAppWithVolumesNeverFailsOnCleanup(t *testing.T) {
	r := newTestRepo(t)
	savedApp(t, r, "app-1", "demo", "x\n")
	t.Setenv("PATH", t.TempDir()) // no docker binary → every exec fails
	if _, err := r.DeleteApp(context.Background(), "app-1", true); err != nil {
		t.Fatalf("delete must not fail on cleanup errors: %v", err)
	}
	if r.appExists("app-1") {
		t.Fatal("app dir must be removed")
	}
}
```

(add `"context"`, `"strings"` to the test imports.)

- [ ] **Step 2: Run** → FAIL (undefined `composeDownVerb`, `projectLabelFilter`; `DeleteApp` arity).

- [ ] **Step 3: Implement.**

In `volumes.go`:

```go
// composeDownVerb is the `compose down` verb; -v also removes the named
// volumes the compose file declares and anonymous volumes (never external).
func composeDownVerb(volumes bool) []string {
	if volumes {
		return []string{"down", "-v", "--remove-orphans"}
	}
	return []string{"down", "--remove-orphans"}
}

func projectLabelFilter(appID string) string {
	return "label=" + composeProjectKey + "=" + projectName(appID)
}

// sweepAppVolumes force-removes any container still labelled with the app's
// project (left behind when `compose down` failed) and then every volume
// carrying that label — including volumes earlier revisions declared. Every
// failure becomes a warning; the caller deletes the app regardless.
func (r *Repository) sweepAppVolumes(ctx context.Context, appID string) []string {
	var warnings []string
	warn := func(msg string, err error) {
		r.log.Warn("delete: "+msg, "app_id", appID, "error", err)
		warnings = append(warnings, fmt.Sprintf("%s: %v", msg, err))
	}
	filter := projectLabelFilter(appID)

	if out, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", filter).Output(); err != nil {
		warn("could not list leftover containers", err)
	} else if ids := strings.Fields(string(out)); len(ids) > 0 {
		args := append([]string{"rm", "-f"}, ids...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			warn("could not remove leftover containers", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
		}
	}

	out, err := exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", filter).Output()
	if err != nil {
		warn("could not list volumes", err)
		return warnings
	}
	for _, name := range strings.Fields(string(out)) {
		if out, err := exec.CommandContext(ctx, "docker", "volume", "rm", name).CombinedOutput(); err != nil {
			warn("volume "+name+" not removed", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out))))
		}
	}
	return warnings
}
```

In `operations.go`, change `composeDown`:

```go
// composeDown is a no-op (nil) if the app dir does not exist. volumes=true
// adds -v (see composeDownVerb).
func (r *Repository) composeDown(ctx context.Context, appID string, volumes bool) error {
	if _, err := os.Stat(r.appDataDir(appID)); err != nil {
		return nil
	}
	return r.composeRun(ctx, appID, composeDownVerb(volumes)...)
}
```

Update every other `composeDown(ctx, id)` caller (grep) to pass `false`.

In `lifecycle.go`:

```go
// DeleteApp stops the app's containers and removes its folder and symlink.
// The git history goes with the folder — deletion is final. With
// deleteVolumes it also removes the app's compose-owned volumes; cleanup
// failures come back as warnings, never as an error.
func (r *Repository) DeleteApp(ctx context.Context, appID string, deleteVolumes bool) ([]string, error) {
	var warnings []string
	if err := r.composeDown(ctx, appID, deleteVolumes); err != nil {
		// Best-effort stop; continue with removal so a broken deployment can
		// still be deleted.
		r.log.Warn("delete: compose down failed, continuing", "app_id", appID, "error", err)
	}
	if deleteVolumes {
		warnings = r.sweepAppVolumes(ctx, appID)
	}
	if err := removeAppSymlink(r.cfg.GetAppsDir(), appID); err != nil {
		r.log.Warn("delete: symlink removal failed, continuing", "app_id", appID, "error", err)
	}
	if err := os.RemoveAll(r.appDataDir(appID)); err != nil {
		return warnings, fmt.Errorf("remove app dir: %w", err)
	}
	r.forgetApp(appID)
	return warnings, nil
}
```

(The sweep runs even when `compose down -v` succeeded: it is what catches volumes from earlier revisions. When everything is already gone it costs three quick execs, only on an explicit delete.)

In `dispatcher.go` replace the `TypeAppDelete` entry:

```go
		command.TypeAppDelete: handle(d, func(ctx context.Context, in command.DeleteAppRequest) (command.DeleteAppResponse, error) {
			warnings, err := d.orch.DeleteApp(ctx, in.AppID, in.DeleteVolumes)
			if err != nil {
				return command.DeleteAppResponse{AppID: in.AppID, Warnings: warnings}, err
			}
			d.reloadIngress(ctx)
			return command.DeleteAppResponse{AppID: in.AppID, Warnings: warnings}, nil
		}),
```

and after the `TypeNetworkDelete` entry:

```go

		command.TypeVolumeList: handle(d, func(ctx context.Context, _ command.ListVolumesRequest) (command.ListVolumesResponse, error) {
			vols, err := d.orch.ListVolumes(ctx)
			return command.ListVolumesResponse{Volumes: vols}, err
		}),
		command.TypeVolumeCreate: handle(d, func(ctx context.Context, in command.CreateVolumeRequest) (command.CreateVolumeResponse, error) {
			return command.CreateVolumeResponse{Name: in.Name}, d.orch.CreateVolume(ctx, in.Name)
		}),
		command.TypeVolumeDelete: handle(d, func(ctx context.Context, in command.DeleteVolumeRequest) (command.DeleteVolumeResponse, error) {
			return command.DeleteVolumeResponse{Name: in.Name}, d.orch.DeleteVolume(ctx, in.Name)
		}),
```

If a dispatcher test enumerates registered types (grep `newHandlers` in `internal/app/agent/*_test.go`), add the three volume types there.

Create `volumes_integration_test.go`:

```go
//go:build integration

package dockercompose

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"winterflow/internal/domain/command"
	"winterflow/pkg/config"
	"winterflow/pkg/logger"
)

// TestDeleteAppWithVolumes deploys an app owning a named volume and mounting
// an external one, deletes it with deleteVolumes=true, and checks the owned
// volume is gone while the external one survives. Run with:
//
//	go test -tags integration -run TestDeleteAppWithVolumes ./internal/infra/orchestrator/docker_compose/
func TestDeleteAppWithVolumes(t *testing.T) {
	t.Setenv("AGENT_DATA_DIR", t.TempDir())
	repo := NewRepository(config.NewServerConfig("standalone"),
		logger.NewLogger(logger.LoggerConfiguration{LogLevel: "error", Service: "test"}))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	const ext = "wf-itg-external-vol"
	if out, err := exec.Command("docker", "volume", "create", ext).CombinedOutput(); err != nil {
		t.Fatalf("create external: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", ext).Run() })

	appID := "itg-vol-app"
	compose := "services:\n  s:\n    image: alpine:3.20\n    command: [\"sleep\", \"300\"]\n" +
		"    volumes: [\"data:/data\", \"ext:/ext\"]\n" +
		"volumes:\n  data: {}\n  ext:\n    external: true\n    name: " + ext + "\n"
	app := command.AppPayload{
		AppID:  appID,
		Config: mustJSON(t, map[string]any{"name": "itg-vol", "files": []map[string]any{{"filename": "compose.yml"}}}),
		Files:  []command.ContentItem{{Name: "compose.yml", Content: []byte(compose)}},
	}
	if _, err := repo.SaveApp(ctx, app); err != nil {
		t.Fatalf("SaveApp: %v", err)
	}
	owned := projectName(appID) + "_data"
	if !volumeExists(t, owned) {
		t.Fatalf("owned volume %s not created", owned)
	}

	warnings, err := repo.DeleteApp(ctx, appID, true)
	if err != nil {
		t.Fatalf("DeleteApp: %v (warnings %v)", err, warnings)
	}
	if volumeExists(t, owned) {
		t.Fatalf("owned volume %s must be removed", owned)
	}
	if !volumeExists(t, ext) {
		t.Fatal("external volume must survive")
	}
}

func volumeExists(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("docker", "volume", "ls", "-q", "--filter", "name=^"+name+"$").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out)) == name
}
```

- [ ] **Step 4: Run** `go build ./... && go test ./internal/infra/orchestrator/... ./internal/app/agent/...` → PASS. If Docker is available: `go test -tags integration -run TestDeleteAppWithVolumes ./internal/infra/orchestrator/docker_compose/` → PASS (report if Docker is unavailable; do not skip silently).
- [ ] **Step 5: Commit** — `feat(agent): delete an app's volumes on request`.

---

### Task 4: API — volume routes and delete_volumes passthrough

**Files:**
- Modify: `internal/app/web/handler/docker/handler.go` (new `// --- volumes ---` section after networks; update package doc to mention volumes)
- Modify: `internal/app/web/routes.go:92-94` (add 3 lines after the network routes)
- Modify: `internal/app/web/handler/app/lifecycle.go` (`deleteAppRequest`, `DeleteApp`)
- Modify: `internal/domain/usecase/app/usecase.go:97` (`DeleteApp` signature)
- Modify: `CLAUDE.md` "Implemented command surface" paragraph: add `volume.list/create/delete` to Docker resources and note `app.delete` accepts `delete_volumes`
- Test: `internal/app/web/handler/docker/handler_test.go`, `internal/app/web/handler/app/handler_test.go`, `internal/domain/usecase/app/usecase_test.go`

**Interfaces:**
- Consumes: Task 1 types/constants/validator.
- Produces: HTTP `GET /api/v1/volume/list?server_id=` (members), `POST /api/v1/volume/create?server_id=` body `{"name"}` (admin), `POST /api/v1/volume/delete?server_id=` body `{"name"}` (admin), all 202 `{data:{request_id}}`; `POST /api/v1/app/delete-app` accepts `delete_volumes`. Usecase: `func (uc *UseCase) DeleteApp(ctx context.Context, userID, serverID, appID string, deleteVolumes bool) (string, error)`.

- [ ] **Step 1: Write failing tests.**

In `handler/docker/handler_test.go`, add three cases to the `TestDockerHandlersReturn202` table:

```go
		{"list-volumes", func(h *Handler) http.HandlerFunc { return h.ListVolumes }, "GET", ""},
		{"create-volume", func(h *Handler) http.HandlerFunc { return h.CreateVolume }, "POST", `{"name":"data"}`},
		{"delete-volume", func(h *Handler) http.HandlerFunc { return h.DeleteVolume }, "POST", `{"name":"data"}`},
```

and new tests:

```go
func TestVolumeHandlersRejectForeignServer(t *testing.T) {
	for name, hf := range map[string]func(*Handler) http.HandlerFunc{
		"list":   func(h *Handler) http.HandlerFunc { return h.ListVolumes },
		"create": func(h *Handler) http.HandlerFunc { return h.CreateVolume },
		"delete": func(h *Handler) http.HandlerFunc { return h.DeleteVolume },
	} {
		t.Run(name, func(t *testing.T) {
			h, fd := newHandler(t)
			r := authed(httptest.NewRequest("POST", "/x?server_id=other", strings.NewReader(`{"name":"data"}`)))
			w := httptest.NewRecorder()
			hf(h)(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("foreign server must be 403, got %d", w.Code)
			}
			if fd.last.AgentID != "" {
				t.Fatalf("must not dispatch, got %+v", fd.last)
			}
		})
	}
}

func TestVolumeHandlersRejectBadNames(t *testing.T) {
	for _, body := range []string{`{}`, `{"name":"-rf"}`, `{"name":"../x"}`, `{"name":"a b"}`} {
		for name, hf := range map[string]func(*Handler) http.HandlerFunc{
			"create": func(h *Handler) http.HandlerFunc { return h.CreateVolume },
			"delete": func(h *Handler) http.HandlerFunc { return h.DeleteVolume },
		} {
			h, fd := newHandler(t)
			r := authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(body)))
			w := httptest.NewRecorder()
			hf(h)(w, r)
			if w.Code != http.StatusBadRequest || fd.last.AgentID != "" {
				t.Fatalf("%s %s: status %d, dispatched %+v", name, body, w.Code, fd.last)
			}
		}
	}
}

func TestVolumeHandlersDispatchTypes(t *testing.T) {
	h, fd := newHandler(t)
	r := authed(httptest.NewRequest("POST", "/x?server_id=s1", strings.NewReader(`{"name":"data"}`)))
	h.DeleteVolume(httptest.NewRecorder(), r)
	if fd.last.Type != command.TypeVolumeDelete {
		t.Fatalf("type = %q", fd.last.Type)
	}
	if p, ok := fd.last.Payload.(command.DeleteVolumeRequest); !ok || p.Name != "data" {
		t.Fatalf("payload = %#v", fd.last.Payload)
	}
}
```

(add `"winterflow/internal/domain/command"` import.)

Admin gating lives in `routes.go` (`adminMW`), not the handler. Check whether any test exercises `routes.go` route→middleware wiring (grep `adminMW|/api/v1/network/create` in `*_test.go`). If one exists, add the volume routes to it. If none does, assert it by reading: the three new lines must sit beside the network lines with identical middleware — the reviewer checks this.

In `handler/app/handler_test.go`, find the fake usecase/dispatcher used by the `delete` case (~line 194) and add a test that posts `{"server_id":"s1","app_id":"a1","delete_volumes":true}` and asserts the dispatched payload is `command.DeleteAppRequest{AppID:"a1", DeleteVolumes:true}`. Also assert omitting it yields `DeleteVolumes:false`. Model it on the existing delete case's setup in that file.

In `usecase/app/usecase_test.go`, update the `uc.DeleteApp(...)` call at ~line 178 to pass `false`, and add:

```go
func TestDeleteAppPassesDeleteVolumes(t *testing.T) {
	// build uc with the same capturing dispatcher TestDeleteAppRemovesDomainRows uses
	// ...
	if _, err := uc.DeleteApp(context.Background(), "u", "s", "app-7", true); err != nil {
		t.Fatal(err)
	}
	// assert captured payload == command.DeleteAppRequest{AppID: "app-7", DeleteVolumes: true}
}
```

(Fill the two commented lines by copying the exact construction and capture from `TestDeleteAppRemovesDomainRows` in the same file.)

- [ ] **Step 2: Run** `go test ./internal/app/web/... ./internal/domain/usecase/...` → FAIL.

- [ ] **Step 3: Implement.**

`handler/docker/handler.go`:

```go
// --- volumes ---

func (h *Handler) ListVolumes(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.caller(w, r)
	if !ok {
		return
	}
	reqID, err := h.dispatch(r.Context(), userID, serverID, command.TypeVolumeList, command.ListVolumesRequest{})
	if err != nil {
		webutil.Error(w, "failed to list volumes", nil)
		return
	}
	accepted(w, "list volumes dispatched", reqID)
}

func (h *Handler) CreateVolume(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.caller(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.CreateVolumeRequest](w, r)
	if !ok {
		return
	}
	if err := command.ValidateVolumeName(req.Name); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.dispatch(r.Context(), userID, serverID, command.TypeVolumeCreate, req)
	if err != nil {
		webutil.Error(w, "failed to create volume", nil)
		return
	}
	accepted(w, "create volume dispatched", reqID)
}

func (h *Handler) DeleteVolume(w http.ResponseWriter, r *http.Request) {
	userID, serverID, ok := h.caller(w, r)
	if !ok {
		return
	}
	req, ok := webutil.DecodeBody[command.DeleteVolumeRequest](w, r)
	if !ok {
		return
	}
	if err := command.ValidateVolumeName(req.Name); err != nil {
		webutil.Error(w, err.Error(), nil)
		return
	}
	reqID, err := h.dispatch(r.Context(), userID, serverID, command.TypeVolumeDelete, req)
	if err != nil {
		webutil.Error(w, "failed to delete volume", nil)
		return
	}
	accepted(w, "delete volume dispatched", reqID)
}
```

(Check how `ListNetworks` calls `h.dispatch` — if it passes `command.ListNetworksRequest{}` match that; confirm `webutil.Error(w, msg, nil)` produces 400 as `CreateNetwork`'s empty-name branch relies on.)

`routes.go`, after the network lines:

```go
	s.Router.With(authMW).Get("/api/v1/volume/list", dockerAPI.ListVolumes)
	s.Router.With(authMW, adminMW).Post("/api/v1/volume/create", dockerAPI.CreateVolume)
	s.Router.With(authMW, adminMW).Post("/api/v1/volume/delete", dockerAPI.DeleteVolume)
```

`handler/app/lifecycle.go`: add `DeleteVolumes bool \`json:"delete_volumes"\`` to `deleteAppRequest`; call `h.usecase.DeleteApp(r.Context(), userID, req.ServerID, req.AppID, req.DeleteVolumes)`. Update the usecase interface the handler depends on (grep `DeleteApp(ctx context.Context, userID` in `handler/app`) and its test fake.

`usecase.go`: add `deleteVolumes bool` param; payload `command.DeleteAppRequest{AppID: appID, DeleteVolumes: deleteVolumes}`.

- [ ] **Step 4: Run** `go build ./... && go vet ./... && go test ./...` → PASS.
- [ ] **Step 5: Commit** — `feat(api): volume routes and delete_volumes on delete-app`.

---

### Task 5: Web UI — Volumes tab and delete-with-volumes

**Files:**
- Modify: `web/src/context/use-docker.ts`
- Modify: `web/src/context/apps-context.tsx` and the context's type file (grep `remove:` in `web/src/context/apps-context-base.ts` or equivalent)
- Modify: `web/src/pages/settings.tsx`
- Modify: `web/src/pages/app-details.tsx` (~:500-590) and its parent that passes `onDelete` (grep `onDelete=`)

**Interfaces:**
- Consumes: Task 4 HTTP routes; SSE result payloads `{volumes: Volume[]|null}` and `{app_id, warnings?: string[]}`.
- Produces: `useDocker().listVolumes(): Promise<Volume[]>`, `createVolume(name)`, `deleteVolume(name)`; apps context `remove(appId: string, opts?: { deleteVolumes?: boolean }): Promise<string[]>` (returns warnings).

- [ ] **Step 1: `use-docker.ts`.** Add the type and calls, following the network ones exactly:

```ts
export type Volume = {
  name: string;
  driver?: string;
  app_id?: string;
  project?: string;
  in_use: boolean;
  orphan: boolean;
  anonymous: boolean;
};
```

```ts
  const listVolumes = useCallback(
    () =>
      dispatch<{ volumes: Volume[] | null }>("/api/v1/volume/list", {}).then(
        (p) => p.volumes ?? [],
      ),
    [dispatch],
  );

  const createVolume = useCallback(
    (name: string) =>
      dispatch("/api/v1/volume/create", { method: "POST", body: { name } }),
    [dispatch],
  );

  const deleteVolume = useCallback(
    (name: string) =>
      dispatch("/api/v1/volume/delete", { method: "POST", body: { name } }),
    [dispatch],
  );
```

Add all three to the returned object.

- [ ] **Step 2: apps context.** Make `dispatchAndWait` return the SSE result payload in addition to refreshing (keep its current return for existing callers — e.g. return `{ list, payload }` only if no caller uses the return; otherwise add an optional out-param). Simplest compatible shape:

```ts
  const remove = useCallback(
    async (appId: string, opts?: { deleteVolumes?: boolean }): Promise<string[]> => {
      const payload = await dispatchAndWaitPayload("delete-app", {
        app_id: appId,
        delete_volumes: opts?.deleteVolumes ?? false,
      });
      return (payload as { warnings?: string[] } | undefined)?.warnings ?? [];
    },
    [dispatchAndWaitPayload],
  );
```

where `dispatchAndWaitPayload` is `dispatchAndWait` refactored so the fetch+waitFor part returns `result.payload` and the refresh still runs (have `dispatchAndWait` call it and keep returning the refreshed list). Update the context's TS type for `remove`.

- [ ] **Step 3: delete dialog** in `app-details.tsx`:
  - Section copy: `Stops the containers and removes the deployment and its history.` (drop the volumes sentence).
  - Add `const [withVolumes, setWithVolumes] = useState(false);`, reset to false when the dialog closes (`AlertDialog onOpenChange`).
  - Inside `AlertDialogContent`, after the description, a vendored `Checkbox` + `Label`:

```tsx
<div className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-3">
  <Checkbox
    id="delete-volumes"
    checked={withVolumes}
    onCheckedChange={(v) => setWithVolumes(v === true)}
  />
  <Label htmlFor="delete-volumes" className="text-sm font-normal leading-snug">
    Also delete this app's Docker volumes. This permanently erases their data.
  </Label>
</div>
```

  - `onDelete` prop becomes `(opts: { deleteVolumes: boolean }) => Promise<string[]>`; `del` does:

```ts
      const warnings = await onDelete({ deleteVolumes: withVolumes });
      if (warnings.length) {
        toast.warning("App deleted, some volumes were kept", {
          description: warnings.join("\n"),
        });
      } else {
        toast.success(withVolumes ? "App and volumes deleted" : "App deleted");
      }
```

  - Update the parent passing `onDelete` to forward `opts` to `remove(appId, opts)` and return its result.

- [ ] **Step 4: `VolumesCard`** in `settings.tsx`: add `<TabsTrigger value="volumes">Volumes</TabsTrigger>` after Networks and `<TabsContent value="volumes"><VolumesCard docker={docker} /></TabsContent>`. Check how the tab value is validated from `searchParams` (~line 48-70) and add `"volumes"` to any allowed list. `VolumesCard` follows `NetworksCard` (load/add/remove, Dialog for create, Table) with these differences:
  - `const { isAdmin } = useProfile();` — Create button and per-row delete render only when `isAdmin`.
  - `const { apps } = useApps();` (check the apps context hook name/shape) to map `app_id` → app name; app cell is a `Link` to the app page route used elsewhere (grep `to={\`/apps/` for the exact path) when the app is known, else `—`; for non-winterflow compose volumes show `project` in muted text.
  - `const [showAnon, setShowAnon] = useState(false);` with a `Switch` + label "Show anonymous" in the header; `visible = items.filter(v => showAnon || !v.anonymous)`.
  - Columns: Name (mono), App, Status: `Badge` "In use" (secondary) when `in_use`, `Badge` "Orphan" (outline, amber text) when `orphan`, "Unused" muted text otherwise.
  - Delete uses an `AlertDialog` confirm ("Delete volume <name>? Its data is erased permanently."); error toast shows `e.message` (carries Docker's "volume is in use" text).
  - Create dialog input validates client-side with the same regex `/^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/` and disables Create until valid; show the rule as helper text.
  - Empty state: "No volumes." Hidden-anonymous hint when all are anonymous: "Only anonymous volumes — toggle "Show anonymous"."

- [ ] **Step 5: Verify** `pnpm --dir web run build && pnpm --dir web run lint` → both pass. Then `go build ./...` (embeds dist).
- [ ] **Step 6: Commit** — `feat(web): volumes tab and delete-with-volumes`.

