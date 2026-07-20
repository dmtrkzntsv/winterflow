# WinterFlow v2 — Deployment Rework, Frontend Refresh & Backend Cleanup

Design approved 2026-07-02. Execution order: **Cleanup → B (frontend refresh)** now;
**A1 → A2 (deployment rework)** in follow-up sessions. This spec records the whole
approved design so A1/A2 need no re-litigation.

## Context

The v1→v2 migration (see `MIGRATION.md`) is functionally complete. Three gaps
remain: the frontend looks worse than v1, the deployment model double-stores
every app (`apps_templates/{rev}` sources rendered into `apps/`), and the
backend accumulated dead code and pass-through layers during the port.

Constraints that hold everywhere:
- The two topologies (standalone in-process vs API/Redis/Hub/Agent) stay; only
  transport differs.
- Transport-agnostic domain logic stays (`bus.Bus`, `CommandDispatcher`,
  domain ports).
- v2 is unreleased: **no data migration** from the old on-disk layout.

---

## Phase 0 — Backend cleanup (execute now)

Source: two review passes over the backend (2026-07-02). ~400 lines of the
~900-line total reduction land here; the rest rides along with A1.

### Dead code (pure deletion)
- PAT-token management: `CreateToken`/`ListTokens`/`RevokeToken` in
  `internal/infra/db/repository/user.go` + port methods + `model.Token`. No
  route can create a token, so the `BasicAuthChecker` PAT path is unreachable;
  **keep** `FindByToken` + the checker (PATs return as a feature later), delete
  the unreachable management surface.
- The no-op `AddServer` chain across port/repo/service/usecase + empty
  `dto.ServerDTO`.
- `models.ReleaseVersion` (registered, table never created) + registration +
  drop-list entry.
- `port.AgentService` interface (impl doesn't satisfy it) + the four
  constant-returning stubs on the concrete service; keep `Register`.
- Package `internal/infra/transport/dto` (zero importers).
- Hub registry: `pendingRequests` + lock, `metrics` map, and the no-op
  `cancelFunc` machinery (`context.WithCancel` result discarded).
- `redisbus.BusMessage` alias; `codec.UnixTimestamp`;
  `internal/app/web/middleware/timeout/`; `webutil.Failure`;
  `pkg/util.FileExists`; stray `uuid.New()` in `pkg/util/id.go`; unused
  config getters (`GetApiURL`, `GetMode`, `GetHubCSRPath`,
  `GetHubFullchainPath`, `GetAgentCSRPath`); the 10 single-caller
  `Get*Filename()` getters become constants in `internal/infra/cert`.
- Dead model surface: unpopulated `model.Server.Certificate`;
  `model.Organization`, `RoleAdmin` (keep `RoleOwner`).
  **Exception:** `model.Server.Capabilities` is NOT deleted — Part B populates
  and serializes it.
- `pkg/util.NewDateTime` moves into `internal/infra/db/types` (fixes the
  pkg→internal import inversion).

### Structural (no behavior change)
- `startResponseSubscriber` takes `bus.Bus` (interface) instead of concrete
  `*redisbus.Bus`; delete standalone's byte-identical inline copy.
- Extract shared `wireCore(ctx, bus, db, cfg, log) *Deps` used by
  `BootstrapStandalone` and `BootstrapAPI`; each adds only its own extras
  (certs/bridge/self-register vs Redis connection).
- Shared bridge conversions in `codec`: `EnvelopeFromCommand(bus.CommandMessage)
  *proto.RequestEnvelope` and `NotificationFromResponse(*proto.ResponseEnvelope)
  model.Notification`, used by both the Hub bridge and `inprocess.go`
  (three copies exist today; the hub one hardcodes version constants).
  `codec.NewRequestPayload`/`EncodeRequest` registry: `EncodeRequest` is
  repurposed by `EnvelopeFromCommand`; `NewRequestPayload` is deleted with the
  dispatcher refactor in A1 (not now — it still backs `codec_test.go`).
- A `subscribeJSON[T any]` helper in `bus` collapsing the four identical
  consume loops (responses ×2, events, request bridges).

### Explicitly kept
`bus.Bus`, `CommandDispatcher`, `usecase/app` (`OnResult` hooks are real domain
logic), org tables (~70 lines, the multi-tenant seam; only dead role values
trimmed), `grpc/handler/stats.go`.

### TDD
Deletions: existing suite stays green. Refactors (wireCore, codec conversions,
subscribeJSON): characterization tests written first against current behavior,
then refactor under them.

---

## Phase B — Frontend refresh + live status (execute now)

### B1. Live status pipeline (backend)

**The apps-status producer is missing today** — the API consumes
`EventAppsStatus` and serves `get-apps-status`, but nothing produces the event;
the endpoint always returns `[]`. Fix as part of this phase:

- **Agent**: a periodic status reporter (interval 30s, same cadence as
  heartbeat) collects container status via the existing orchestrator status op
  and pushes an `apps.status` event up the stream (standalone: via the
  in-process bridge's event path). Sent immediately on connect, then on ticker.
- **Hub**: forwards it as `bus.EventAppsStatus` (consumer already exists).

**Server liveness over SSE** (replaces polling):
- New `model.NotificationType` `server_status`, payload
  `{server_id, liveness}`.
- `status.Cache.MarkOnline` returns whether liveness transitioned
  (unknown→online). `handleEvent` publishes the notification on transition to
  every user of the server's organization (new repo method: user IDs for a
  server's org).
- **Sweeper** goroutine (ticker 15s): cache exposes servers whose liveness
  expired since last check; publishes `liveness: unknown` transitions.
- `apps_status` notification: on `EventAppsStatus`, publish
  `{server_id, apps: [{app_id, status}]}` to org users the same way, so app
  cards update live too. `get-apps-status`/`get-servers-status` remain as
  seed endpoints for initial page load.

**Hardware specs & IP**:
- Agent capability collectors ported from v1 (`server_ip`,
  `system_cpu_cores`, `system_memory_total`, `system_disk_total` — stdlib
  only: `syscall.Statfs`, `/proc/meminfo` or equivalent, outbound-socket
  local-addr trick for IP). Reported once at connect with existing
  capabilities.
- `model.Capability` gets JSON tags (`name`, `value`); repository populates
  `Server.Capabilities`; `get-servers` thereby serializes them. No new
  endpoint.

### B2. Web frontend

- **URLs (v1 structure)**: `/apps/new` → `/create-app`;
  `/apps/:appId/edit` deleted. Kept: `/`, `/app/:appId`, `/settings`,
  `/login`.
- **Theme**: v1 tokens into `web/src/index.css` — radius `0.5rem`, primary
  `oklch(0.623 0.214 259.815)`, white surfaces, neutral-gray sidebar (values
  lifted from v1 `web/src/styles/index.css`, incl. `.dark`).
- **App cards (v1 style + animation)**: square tile, colored icon block,
  status-colored border, status dot/label, hover-rotate transition, staggered
  entrance animation (CSS only, `tw-animate-css`-style keyframes; no
  animation library). Status fed live from `apps_status` SSE.
- **Server cards (v1 layout)**: status dot (online/unknown), IP,
  "N cores • X GB • Y GB disk" from capabilities, agent version, last-seen
  when not online. Liveness seeded from `get-servers-status`, updated via
  `server_status` SSE; on unknown→online the context refetches `get-servers`
  (fresh last_seen/capabilities).
- **notifications-context**: `subscribe(type, handler)` API alongside
  `waitFor` for unsolicited event types.
- **Editor tab**: `app-details` tabs become **Logs / Editor / Settings**
  (History arrives with A1), active tab in `?tab=` query param. Load/save
  logic (get-app, ECIES encrypt, save + waitFor) extracted from
  `create-app.tsx` into an `AppEditorPanel` used by the tab;
  `create-app.tsx` becomes create-only.
- **Code editor**: CodeMirror 6 (`@uiw/react-codemirror`, YAML language for
  compose + lang-by-extension) replaces the `Textarea` in `AppEditor`.
- **Logs viewer**: lightweight custom component — monospace scroll area,
  auto-scroll-to-bottom (sticky unless user scrolled up), tail-size selector
  (200/500/1000), refresh. No third-party log library.

### TDD / coverage
Backend work in this phase (collectors, cache transitions, sweeper,
notification fan-out, event handling, apps-status producer) is test-first.
Target: >60% statement coverage on the backend overall (`go test ./...
-cover`), acknowledging transport-heavy packages (grpc hub/agent) drag the
mean; touched packages aim well above.

---

## Phase A1 — Deployment core (future session)

One folder per app that IS the deployment; git for history.

```
{AGENT_DATA_DIR}/
  apps/                        # names only — `ls apps/` = the app list
    grafana -> ../apps-data/3f2a…/
  apps-data/{appID}/           # canonical (compose.yml, files, .env,
                               #   .winterflow/config.json, .git/)
```

- Symlinks: slugified app name (collision → short-id suffix), relative
  targets; `app.rename` swaps the link; `apps.list` reconcile self-heals
  missing/dangling links. Agent always operates on the canonical path
  (compose project identity stable across renames).
- **Versioning**: go-git (pure Go — no host git dependency). Every `app.save`
  commits. Rollback = restore a previous commit's tree as a NEW commit
  (linear history, never rewritten) + redeploy.
- **Vars/secrets, compose-native**: plain vars in committed `.env`; secrets
  ECIES-encrypted in committed `.winterflow/config.json`, decrypted to
  gitignored `.env.secrets` at deploy; compose runs with
  `--env-file .env --env-file .env.secrets`. No custom `${VAR}` rendering —
  `pkg/template` deleted; non-compose files used verbatim.
- Commands: `app.save/get/delete/rename` rewritten (revision fields removed
  from the command surface); new `app.revisions` (git log →
  `{rev, timestamp, source}`) and `app.rollback {app_id, revision}`.
  UI: **History tab** with rollback + confirm.
- Ride-along refactors: agent dispatcher switch → generic
  `handle[Req, Resp]` registration map (deletes `codec.NewRequestPayload`);
  `DispatchJSON` generic web-handler helper (also fixes auth returning 400
  instead of 401); delete `db/service` shim + `usecase/server` +
  `usecase/docker` pass-throughs.

## Phase A2 — Git-sourced apps + image tags (future session)

- App source type: `repo_url`, `branch`, optional `compose_path`, optional
  ECIES-encrypted access token. Upstream clone in `source/` inside the app
  folder, gitignored from winterflow's own history repo.
- Compose resolution: `compose_path` if set → root compose in repo →
  winterflow-authored root `compose.yml` (required if repo has none;
  references `./source` as build context).
- Every deploy pins the source SHA in the winterflow commit; rollback
  restores config AND checks out `source/` at that SHA.
- Auto-redeploy: agent-side polling (default ~2 min, per-app, disableable);
  on new commit: pull, pin, redeploy, SSE notification. Webhooks later.
- `image.tags {image}` command: registry HTTP v2 tags list using docker
  config credentials (Docker Hub token flow, pagination). UI: editor detects
  `image:` refs in compose, per-image "browse tags" → pick to insert.

## Out of scope (unchanged from MIGRATION.md)

Catalog/templates (explicitly dropped during this design), organizations &
members, billing.

## Verification (phases 0 + B)

- `make build` + `go build ./cmd/hub ./cmd/agent`, `go vet ./...`,
  `go test ./... -cover` (coverage reported honestly per package).
- `pnpm --dir web run build` + `run lint` (only the pre-existing
  `sidebar.tsx` finding tolerated).
- Standalone E2E: deploy an app, watch card status go live without reload;
  stop agent → server card flips to unknown after TTL via SSE; restart →
  online; edit via Editor tab; logs viewer tail/refresh.
- Each feature = its own commit on `migration`.
