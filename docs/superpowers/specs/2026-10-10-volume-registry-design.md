# Volume registry and delete-with-volumes — design

Date: 2026-10-10. Status: approved in conversation, awaiting spec review.

## Context

This is sub-project **A** of "winterflow as the runtime for twillingate cloud
tenants" (one standalone host first, distributed later). The full set:

| # | Sub-project | State |
|---|---|---|
| A | Volume registry + delete-with-volumes | this spec |
| B | Registered domains, wildcard TLS, subdomain allocation | later |
| C | Global backup targets + Litestream sidecar injection | later |
| D | Templates + instantiate API (ties A–C together) | later |

Today winterflow does not model volumes at all. They exist only inside an
app's compose YAML. `app.delete` runs `docker compose down --remove-orphans`
(no `-v`), so named volumes outlive their app. The delete dialog says so:
"Named Docker volumes are kept." Cloud tenants need a delete that really
removes the tenant's data. Admins also need to see which volumes exist,
which app owns each one, and which are orphaned.

## Goals

1. `app.delete` can also remove the app's volumes. This is opt-in per call
   and the default keeps today's behaviour.
2. A per-server volume inventory with create and delete, built on the same
   pattern as registries and networks.
3. Idle cost stays zero: volumes are only listed when someone asks.

## Non-goals

- Volume sizes. `docker system df -v` took >30 s on a dev box, too slow for
  a list call. An on-demand "measure" action may come later.
- Managed volumes with a lifecycle separate from apps (detach and re-attach),
  and per-volume backups. Backups belong to sub-project C. Template-owned
  volumes belong to D.
- Volume drivers other than `local` on create.

## 1. Delete an app with its volumes

### Contract

- `command.DeleteAppRequest` gains `DeleteVolumes bool \`json:"delete_volumes,omitempty"\``.
- `command.DeleteAppResponse` gains `Warnings []string \`json:"warnings,omitempty"\``.
- `POST /api/v1/app/delete-app` accepts optional `"delete_volumes": true`
  (default false). The usecase `DeleteApp` passes it through.
- Authorization is unchanged: `authorize` / `RequireServerAccess`. Members
  keep the full app lifecycle, so a member may delete an app's volumes as
  well. The flag only touches volumes the app itself owns.

### Agent behaviour (`docker_compose.Repository.DeleteApp`)

The signature becomes `DeleteApp(ctx, appID string, deleteVolumes bool) ([]string, error)`,
returning warnings. When `deleteVolumes` is false, nothing changes from
today. When it is true:

1. Run `compose down -v --remove-orphans`, with the same project, env-file
   and compose-file arguments as today's `composeDown`. `-v` removes the
   named volumes the current compose file declares and the anonymous
   volumes of its containers. It never removes `external: true` volumes.
2. **Container sweep.** If step 1 failed (for example a broken compose file),
   force-remove any container still labelled
   `com.docker.compose.project=wf-<appID>`:
   `docker ps -aq --filter label=…` then `docker rm -f <ids…>`. Otherwise
   those containers would keep their volumes in use.
3. **Volume sweep.** List `docker volume ls -q --filter label=com.docker.compose.project=wf-<appID>`
   and `docker volume rm` each one. This catches volumes from earlier
   revisions that the current compose file no longer declares, and covers
   the case where step 1 failed. External volumes carry no project label,
   so they are never matched.
4. Continue with today's steps: symlink, `os.RemoveAll(appDataDir)`,
   `forgetApp`.

Failures in steps 2–3 do not fail the delete. Each one becomes a warning
(`"volume <name> not removed: <docker stderr>"`) and is logged at Warn,
because the app is gone either way. A failure to remove the app directory
is still an error, as it is today.

All docker arguments are `[]string`. The app id goes through
`filepath.Base` (`projectName` already does this). The whole delete already
runs under the command context. No new network operations are involved.

## 2. Volume registry

### Commands (`internal/domain/command/docker.go`)

```go
type Volume struct {
    Name      string `json:"name"`
    Driver    string `json:"driver"`
    AppID     string `json:"app_id,omitempty"`  // from com.docker.compose.project=wf-<id>
    Project   string `json:"project,omitempty"` // raw compose project label (non-winterflow stacks)
    InUse     bool   `json:"in_use"`            // mounted by any container, running or stopped
    Orphan    bool   `json:"orphan"`            // wf- project label but no such app on this agent
    Anonymous bool   `json:"anonymous"`         // com.docker.volume.anonymous label
}

type ListVolumesRequest struct{}
type ListVolumesResponse struct{ Volumes []Volume `json:"volumes"` }

type CreateVolumeRequest struct{ Name string `json:"name"` }
type CreateVolumeResponse struct{ Name string `json:"name"` }

type DeleteVolumeRequest struct{ Name string `json:"name"` }
type DeleteVolumeResponse struct{ Name string `json:"name"` }
```

The type constants are `volume.list`, `volume.create` and `volume.delete`,
next to `network.*` in `command.go`, with one `handle(d, …)` line each in
the dispatcher's `newHandlers`.

### Orchestrator (`docker_compose/docker.go`)

- **`ListVolumes`** runs exactly two execs, and only when called:
  - `docker volume ls --format json` gives name, driver and labels (labels
    come as one `k=v,k=v` string).
  - `docker ps --all --no-trunc --format json`, unfiltered, because
    non-compose containers can mount volumes too. Its `Mounts` field is a
    comma-separated list of volume names and bind sources. A volume is
    `InUse` if its name appears there.
  - `AppID` is the project label with the `wf-` prefix stripped. `Orphan`
    means an `AppID` exists but `appDataDir(AppID)` is not on disk (a stat,
    no exec).
  - Parsing lives in pure functions (`parseVolumeLS`, `parseMountedVolumes`)
    so it can be tested without Docker. Malformed lines are logged and
    skipped, as `ListNetworks` does.
  - Sort order: winterflow-owned first (by app id), then other named
    volumes, then anonymous ones, each group by name.
- **`CreateVolume`** validates the name against Docker's rule
  `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$` (≤ 255 characters). That rule also blocks
  any value beginning with `-` from being read as a flag. It then runs
  `docker volume create <name>`.
- **`DeleteVolume`** applies the same validation, then runs
  `docker volume rm <name>` without `--force`. Docker's "volume is in use"
  refusal is returned as the error text, so the UI shows why.

### HTTP

Handlers go in `internal/app/web/handler/docker/handler.go` and reuse
`caller` (`server_id` and `RequireServerAccess`), `dispatch` and `accepted`.
Routes go in `routes.go`, next to networks:

| Route | Who | Body |
|---|---|---|
| `GET /api/v1/volume/list?server_id=` | any member with server access | — |
| `POST /api/v1/volume/create?server_id=` | admin (`adminMW`) | `{"name"}` |
| `POST /api/v1/volume/delete?server_id=` | admin (`adminMW`) | `{"name"}` |

This matches the network routes (`routes.go:92-94`): `server_id` comes from
the query through `caller`, and the body is decoded with `webutil.DecodeBody`.
The create and delete handlers also check the volume name with the shared
validator and return 400 before dispatching. The agent checks it again. All
three routes are agent-bound and answer 202 + `request_id`, with the result
over SSE.

The API does not enrich the response with app names. The web UI already
has the app list and maps `app_id` to a name client-side, the same way the
app pages do.

## 3. Web UI

- `web/src/context/use-docker.ts`: `listVolumes`, `createVolume` and
  `deleteVolume`, following the network calls.
- `web/src/pages/settings.tsx`: a **Volumes** tab beside Registries and
  Networks, rendered by a `VolumesCard` that follows `NetworksCard`:
  - columns: name, app (linked to the app page when known), in use, and an
    "orphan" badge;
  - anonymous volumes are hidden by default behind a "Show anonymous" toggle;
  - admins see Create (a name dialog) and a per-row Delete with a
    confirmation. A delete error (in use) is shown in a toast.
- `web/src/pages/app-details.tsx`, Delete app section:
  - the description becomes "Stops the containers and removes the
    deployment and its history.";
  - the confirmation dialog gains an unchecked checkbox, "Also delete this
    app's Docker volumes. This permanently erases their data.", sent as
    `delete_volumes`;
  - warnings from the result are shown in a toast.
- Only vendored shadcn/ui components are used. Nothing under
  `components/ui` is removed.

## 4. Testing

- **Orchestrator (unit, no Docker):**
  - `parseVolumeLS` with real captured output: compose-labelled,
    anonymous, plain and malformed lines;
  - `parseMountedVolumes`, including bind paths and empty `Mounts`;
  - app id, orphan and in-use derivation with a temp `apps-data` dir;
  - name validation, including a leading `-`, `../x` and an empty name;
  - an argument builder for the delete steps, asserting `down -v` and the
    project-label filters.
- **Dispatcher:** the three new types are registered and decode their
  payloads through the codec.
- **Handlers:** each volume route answers 403 without server access;
  create/delete answer 403 for a non-admin member; delete-app passes
  `delete_volumes` through to the dispatched command.
- **Integration (existing Docker-gated `deploy_integration_test.go`
  style):** deploy an app with a named volume and delete it with
  `delete_volumes=true`, then check the volume is gone. A second app with
  an `external: true` volume deleted the same way leaves that volume in
  place.
- **Web:** `pnpm --dir web run build` and `lint`, then a browser check of
  the Volumes tab and the delete dialog.

## Compatibility

- Old API clients that don't send `delete_volumes` keep today's behaviour.
- Distributed mode: an older agent decodes `DeleteAppRequest` without the
  field and ignores it, so its volumes are kept and no error is raised. It
  answers `volume.*` with an unknown-command error, which the UI shows as
  the result. No proto change is needed, since everything rides the
  existing JSON envelope.
- No DB migration.
